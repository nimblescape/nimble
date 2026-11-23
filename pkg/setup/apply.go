// Package setup applies the config repo to this machine: the managed dotfiles, linked
// config, the generated ssh config, the login shell and the tools its template lists.
package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// Options tune Apply.
type Options struct {
	NoTools bool   // only configs
	Upgrade bool   // also bring the tools that stay current up to date (nimble update)
	As      string // Debian host: its name in hosts/ (remembered for later runs)
}

// ErrTools is returned by Apply when everything was applied but a tool failed to install.
var ErrTools = errors.New("some tools were not installed")

// ZshFiles reports whether this machine gets the zsh dotfiles: zsh is the Mac's shell,
// asked for by the template, or installed.
func ZshFiles(shell string) bool {
	_, err := exec.LookPath("zsh")
	return Darwin() || shell == "zsh" || err == nil
}

// Darwin reports whether this is a Mac.
func Darwin() bool { return runtime.GOOS == "darwin" }

// Apply sets up this machine from the config repo. Safe to re-run; only differences change.
func Apply(o Options) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	if config.OS() == "" {
		return errors.New("nimble supports macOS, Debian and Ubuntu only")
	}
	if errs := config.Validate(KnownTools()); len(errs) > 0 {
		for _, e := range errs {
			nimble.Warn(e.Error())
		}
		return fmt.Errorf("the config repo has %d problem(s) — fix them first (nimble doctor lists them)", len(errs))
	}
	if nimble.IsDebian() && os.Geteuid() == 0 {
		return errors.New("run nimble apply as your user, not as root")
	}
	self, found := hosts.Self(o.As)
	var hp *config.Host
	if found {
		hp = &self
	} else {
		nimble.Say("note: no host file for this machine (hosts/" + hosts.MachineName() + ".yaml) — using the default template")
	}
	r, err := config.Resolve(c, hp)
	if err != nil {
		return err
	}
	if nimble.IsDebian() { // what nimble itself needs
		if _, err := nimble.AptInstall("git", "ca-certificates"); err != nil {
			return err
		}
	}
	// zsh's files only where zsh is (macOS, a template with shell: zsh, or installed)
	zsh := ZshFiles(r.Shell)
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".bashrc", ".profile"} {
		if strings.HasPrefix(name, ".z") && !zsh {
			removeStub(name)
			continue
		}
		if err := stub(name); err != nil {
			return err
		}
	}
	if nimble.Exists(filepath.Join(nimble.Home(), ".bash_profile")) {
		nimble.Say("note: ~/.bash_profile exists, so bash login shells skip ~/.profile — move its lines into ~/.nimble/local.sh")
	}
	if err := links(c); err != nil {
		return err
	}
	workstation := Darwin() || (found && self.Workstation())
	if workstation {
		if err := SSHConfig(c); err != nil {
			return err
		}
	} else if err := HostSSHConfig(); err != nil {
		return err
	}
	if err := WriteAliases(); err != nil { // also where there is no ssh config: the nimble function
		return err
	}
	hostGitconfig()
	if err := LoginShell(r.Shell); err != nil {
		return err
	}
	// a tool that fails to install (a vendor's server down) is reported at the end: the other
	// tools, the updates and the steps below still run
	var toolErr error
	if !o.NoTools {
		if workstation { // not a tool to choose: every workstation needs it for its hardware keys
			if err := SSHClient(o.Upgrade); err != nil {
				nimble.Warn("ssh for the hardware keys: " + err.Error())
				toolErr = ErrTools
			}
		}
		if err := InstallTools(r.Tools); err != nil {
			nimble.Warn(err.Error())
			toolErr = ErrTools
		}
		if o.Upgrade {
			UpdateTools(r.Tools)
		}
	}
	if Darwin() {
		itermSettings()
	}
	if err := SelfBuild(); err != nil { // a development checkout (NIMBLE_SRC); otherwise nothing
		return err
	}
	return toolErr
}

// backup moves ~/<rel> into this run's backup folder.
func backup(rel string) error {
	dst := filepath.Join(nimble.BackupDir(), rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(nimble.Home(), rel), dst); err != nil {
		return err
	}
	nimble.Say("moved old ~/" + rel + " to " + nimble.Tilde(nimble.BackupDir()) + "/")
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// TailMarker starts the section of a managed dotfile that other tools appended to.
const TailMarker = "# ── Added by other tools (nimble keeps these as they are) ──"

func stubBody(name string) []string {
	home := `export NIMBLE_HOME="` + nimble.HomeRel(nimble.Repo()) + `"`
	// last, after your shell files (see aliasesScript)
	aliases := `[ -r "$NIMBLE_HOME/cache/` + AliasesFile + `" ] && . "$NIMBLE_HOME/cache/` + AliasesFile + `"   # nimble alias, <host> alone: ssh <host>`
	switch name {
	case ".zshenv":
		return []string{home,
			"skip_global_compinit=1                      # nimble runs compinit itself",
			"[[ -o interactive ]] || setopt no_nomatch   # commands over ssh: unmatched wildcards like bash",
			`. "$NIMBLE_HOME/shell/env.sh"`}
	case ".zprofile":
		return []string{`. "$NIMBLE_HOME/shell/env.sh"   # again, after macOS path_helper reordered PATH`}
	case ".zshrc":
		return []string{`source "$NIMBLE_HOME/shell/zshrc"`, aliases}
	case ".bashrc":
		return []string{home, `. "$NIMBLE_HOME/shell/bashrc"`, aliases}
	case ".profile":
		return []string{home, `. "$NIMBLE_HOME/shell/env.sh"`,
			`[ -n "$BASH_VERSION" ] && [ -r "$HOME/.bashrc" ] && . "$HOME/.bashrc"`}
	}
	return nil
}

// stub writes a managed dotfile: a header that says where things go, the lines that load
// the repo's shell files, and — kept as they are — lines that installers appended.
func stub(name string) error {
	head := []string{
		nimble.StubMark + " — don't edit this part; nimble rewrites it.",
		"#   every machine: ~/.nimble/shell/common.sh  (zsh only: shell/zsh/, bash only: shell/bash/)",
		"#   this machine:  ~/.nimble/local.sh",
	}
	want := strings.Join(append(head, stubBody(name)...), "\n") + "\n"

	path := filepath.Join(nimble.Home(), name)
	cur, err := os.ReadFile(path)
	ours := err == nil && bytes.HasPrefix(cur, []byte(nimble.StubMark))
	if !ours && lexists(path) {
		if err := backup(name); err != nil {
			return err
		}
	}
	tail := ""
	if ours {
		for _, marker := range []string{TailMarker, "# Host-specific additions"} {
			if i := bytes.Index(cur, []byte(marker)); i >= 0 {
				if j := bytes.IndexByte(cur[i:], '\n'); j >= 0 {
					tail = strings.TrimLeft(string(cur[i+j+1:]), "\n")
				}
				break
			}
		}
	}
	want += "\n" + TailMarker + "\n" + tail
	if ours && want == string(cur) {
		return nil
	}
	if ours {
		nimble.Say("updated ~/" + name)
	} else {
		nimble.Say("wrote ~/" + name)
	}
	return os.WriteFile(path, []byte(want), 0o644)
}

// StubTail returns the lines other tools appended to a managed dotfile.
func StubTail(name string) []string {
	cur := nimble.ReadTrim(filepath.Join(nimble.Home(), name))
	if i := strings.Index(cur, TailMarker); i >= 0 {
		return nimble.Lines(cur[i+len(TailMarker):])
	}
	return nil
}

// links links the config files listed in nimble.yaml into $HOME.
func links(c *config.Config) error {
	for _, l := range c.Links {
		if l.OS != "" && l.OS != config.OS() {
			continue
		}
		src := l.Source()
		dst := filepath.Join(nimble.Home(), l.To)
		t, err := os.Readlink(dst)
		if err == nil && t == src {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err == nil { // a link elsewhere (e.g. the repo's old place) holds nothing worth a backup
			if err := os.Remove(dst); err != nil {
				return err
			}
		} else if lexists(dst) {
			if err := backup(l.To); err != nil {
				return err
			}
		}
		if err := os.Symlink(src, dst); err != nil {
			return err
		}
		nimble.Say("linked ~/" + l.To)
	}
	return nil
}

// hostGitconfig keeps ~/.gitconfig for host-specific settings: git reads it after the
// shared ~/.config/git/config, and `git config --global` writes to it — not into the repo.
func hostGitconfig() {
	p := filepath.Join(nimble.Home(), ".gitconfig")
	if !nimble.Exists(p) {
		_ = os.WriteFile(p, []byte("# Host-specific git settings (not part of the nimble repo).\n"), 0o644)
	} else if strings.Contains(nimble.ReadTrim(p), "[user]") {
		nimble.Say("note: ~/.gitconfig repeats shared settings — keep only host-specific ones there")
	}
}

// SelfBuild rebuilds ~/.local/bin/nimble from nimble's source checkout (NIMBLE_SRC, for
// development); released copies update with nimble upgrade instead.
func SelfBuild() error {
	if _, err := exec.LookPath("go"); err != nil || nimble.SourceDir() == "" {
		return nil
	}
	bin := filepath.Join(nimble.Home(), ".local", "bin", "nimble")
	tmp := bin + ".new"
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	version := nimble.SourceVersion()
	err := nimble.Wait("build nimble "+version+" from "+nimble.Tilde(nimble.SourceDir()), nimble.Cmd{Name: "go", Dir: nimble.SourceDir(), Env: []string{"CGO_ENABLED=0"},
		Args: []string{"build", "-trimpath", "-ldflags", "-s -w -X main.version=" + version, "-o", tmp, "."}}.Do)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	old, _ := os.ReadFile(bin)
	built, _ := os.ReadFile(tmp)
	if bytes.Equal(old, built) {
		return os.Remove(tmp)
	}
	if err := os.Rename(tmp, bin); err != nil { // the running binary keeps its inode
		return err
	}
	nimble.Say("~/.local/bin/nimble → " + version)
	return nil
}

// removeStub deletes a dotfile nimble wrote, unless other tools added lines to it.
func removeStub(name string) {
	p := filepath.Join(nimble.Home(), name)
	if !strings.HasPrefix(nimble.ReadTrim(p), nimble.StubMark) || len(StubTail(name)) > 0 {
		return
	}
	if os.Remove(p) == nil {
		nimble.Say("removed ~/" + name + " (no zsh on this machine)")
	}
}
