// Package nimble holds what the nimble commands share: where the nimble repo is,
// the pinned versions, running commands, and the report output.
package nimble

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// StubMark starts every dotfile nimble manages (~/.zshenv, ~/.zprofile, ~/.zshrc, ~/.bashrc,
// ~/.profile).
const StubMark = "# Managed by nimble"

// Home is the user's home directory.
func Home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Repo is the config repo (on a host: its snapshot): $NIMBLE_HOME (set by the managed
// dotfiles and by SetupEnv), else ~/.nimble. Everything nimble persists lives there;
// backup/ and cache/ are not part of git.
func Repo() string {
	if d := os.Getenv("NIMBLE_HOME"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".nimble")
}

var (
	backupOnce sync.Once
	backupDir  string
)

// BackupDir is where this run moves the files it replaces (~/.nimble/backup/<date>).
func BackupDir() string {
	backupOnce.Do(func() { backupDir = filepath.Join(Repo(), "backup", time.Now().Format("20060102-150405")) })
	return backupDir
}

// CacheDir is a path in ~/.nimble/cache (generated completions, the ssh config, …).
func CacheDir(parts ...string) string {
	return filepath.Join(append([]string{Repo(), "cache"}, parts...)...)
}

// HomeRel writes a path under $HOME as "$HOME/…" (for shell files).
func HomeRel(p string) string {
	if h := Home(); strings.HasPrefix(p, h+"/") {
		return "$HOME" + p[len(h):]
	}
	return p
}

// RepoFile is a path inside the repo.
func RepoFile(parts ...string) string {
	return filepath.Join(append([]string{Repo()}, parts...)...)
}

// Tilde shortens paths under $HOME for display.
func Tilde(p string) string {
	if h := Home(); strings.HasPrefix(p, h+"/") {
		return "~" + p[len(h):]
	}
	return p
}

// SetupEnv makes the toolchain reachable for nimble and the scripts it runs, the way
// the login shell does — also when nimble is started over ssh without a login shell.
func SetupEnv() {
	h := Home()
	dirs := []string{filepath.Join(h, ".local/go/bin"), filepath.Join(h, ".local/bin")}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, filepath.Join(BrewPrefix(), "bin")) // Homebrew's go before an old /usr/local/go
	} else {
		dirs = append(dirs, "/usr/local/go/bin")
	}
	dirs = append(dirs, filepath.Join(h, "go/bin"), os.Getenv("PATH"))
	os.Setenv("PATH", strings.Join(dirs, ":"))
	os.Setenv("UV_PYTHON_PREFERENCE", "only-managed")
	if os.Getenv("NVM_DIR") == "" {
		os.Setenv("NVM_DIR", filepath.Join(h, ".nvm"))
	}
	// Plumbing that runs as root through sudo (harden, service …) works on the config of the
	// user who called sudo: sudo resets HOME to /root, where there is none.
	if os.Geteuid() == 0 && os.Getenv("NIMBLE_HOME") == "" {
		if su := os.Getenv("SUDO_USER"); su != "" && su != "root" {
			if u, err := user.Lookup(su); err == nil {
				os.Setenv("NIMBLE_HOME", filepath.Join(u.HomeDir, ".nimble"))
			}
		}
	}
	os.Setenv("NIMBLE_HOME", Repo())
}

// BrewPrefix is Homebrew's prefix on this Mac: /opt/homebrew on Apple silicon, /usr/local
// on Intel.
func BrewPrefix() string {
	for _, p := range []string{"/opt/homebrew", "/usr/local"} {
		if Exists(filepath.Join(p, "bin", "brew")) {
			return p
		}
	}
	if runtime.GOARCH == "arm64" {
		return "/opt/homebrew"
	}
	return "/usr/local"
}

// Output runs a command and returns its trimmed stdout, "" if it fails.
func Output(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// OutputC is Output in the C locale, for output nimble reads: tools translate their
// messages (sudo -l says "may run the following commands" in the host's language).
func OutputC(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Run runs a command with the terminal attached (an editor, git asking for credentials):
// a running spinner stops first.
func Run(name string, args ...string) error {
	stopSpinner()
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Script runs one of the repo's bash scripts with the terminal attached.
func Script(name string, args ...string) error {
	return Run("bash", append([]string{RepoFile(name)}, args...)...)
}

// Git runs git in the repo and returns its output ("" on failure).
func Git(args ...string) string {
	return Output("git", append([]string{"-C", Repo()}, args...)...)
}

// IsCheckout reports whether the repo is a git checkout (not a plain copy).
func IsCheckout() bool { return Exists(RepoFile(".git")) }

// ReadTrim returns a file's trimmed content ("" if unreadable).
func ReadTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false // a pipe or a file
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null) // /dev/null is a character device too
}

// Field returns the n-th whitespace-separated field of s ("" if missing).
func Field(s string, n int) string {
	if f := strings.Fields(s); n < len(f) {
		return f[n]
	}
	return ""
}

// Lines returns the non-blank lines of s.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// Entries returns the non-empty, non-comment lines of a list file in the repo.
func Entries(parts ...string) []string {
	f, err := os.Open(RepoFile(parts...))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		if line := strings.TrimSpace(s.Text()); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// SplitSpec splits "name@version" (the name may be @scoped); version is "" if unpinned.
func SplitSpec(spec string) (name, version string) {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

// JSONField reads a top-level string field from a JSON file.
func JSONField(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// CurrentUser is the login name of the user running nimble.
func CurrentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

// OSName is the kernel name: "Darwin" or "Linux".
func OSName() string {
	if runtime.GOOS == "darwin" {
		return "Darwin"
	}
	return "Linux"
}

// IsDebian reports whether this is a Debian (or Debian-based) host.
func IsDebian() bool { return runtime.GOOS == "linux" && Exists("/etc/debian_version") }

// LoginShell is the user's login shell from the user database.
func LoginShell() string {
	u := CurrentUser()
	if runtime.GOOS == "darwin" {
		return Field(Output("dscl", ".", "-read", "/Users/"+u, "UserShell"), 1)
	}
	if f := strings.Split(Output("getent", "passwd", u), ":"); len(f) >= 7 {
		return f[6]
	}
	return ""
}

// XDGConfig is $XDG_CONFIG_HOME or ~/.config.
func XDGConfig() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".config")
}

// AgentHasTrustedKey reports whether the (possibly forwarded) SSH agent offers one of
// the keys in keys/.
func AgentHasTrustedKey() bool {
	agent := Output("ssh-add", "-L")
	files, _ := filepath.Glob(RepoFile("keys", "*", "*.pub"))
	for _, f := range files {
		if k := Field(ReadTrim(f), 1); k != "" && strings.Contains(agent, k) {
			return true
		}
	}
	return false
}

// IsSnapshot reports whether ~/.nimble is a host's config snapshot (not the repo).
func IsSnapshot() bool { return Exists(RepoFile(".snapshot")) }

// SnapshotInfo describes the snapshot: where and when it came from.
func SnapshotInfo() string {
	from, commit, date := "", "", ""
	for _, l := range Lines(ReadTrim(RepoFile(".snapshot"))) {
		k, v, _ := strings.Cut(l, ": ")
		switch k {
		case "from":
			from = v
		case "commit":
			commit = v
		case "date":
			date = v
		}
	}
	return "snapshot from " + from + " (" + commit + ", " + date + ")"
}

// Version is the running nimble's version (set by main).
var Version = "dev"

// SourceDir is nimble's own source checkout for development — NIMBLE_SRC, if it holds the
// github.com/nimblescape/nimble module — or "" (a released nimble).
func SourceDir() string {
	dir := os.Getenv("NIMBLE_SRC")
	if dir == "" {
		return ""
	}
	if first, _, _ := strings.Cut(ReadTrim(filepath.Join(dir, "go.mod")), "\n"); strings.TrimSpace(first) == "module github.com/nimblescape/nimble" {
		return dir
	}
	return ""
}

// SourceVersion describes the source checkout's commit (git describe).
func SourceVersion() string {
	return Output("git", "-C", SourceDir(), "describe", "--tags", "--always", "--dirty")
}
