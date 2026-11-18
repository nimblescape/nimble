package status

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status [host…|all]",
		Short: "What this machine's template asks for vs. what is installed",
		Long: heredoc.Doc(`
			Show this machine's host file and template, its login shell, the repo state and,
			for every tool of the template, whether it is installed at the pinned version.
			With hosts, run the same on each of them.
		`),
		Example:           "  nimble status\n  nimble status all",
		ValidArgsFunction: hosts.Complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			return hosts.LocalOrRemote(args, "status", local)
		},
	}
}

func local() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	p, err := config.LoadPins()
	if err != nil {
		return err
	}
	r := &nimble.Report{}
	host, _ := os.Hostname()
	self, found := hosts.Self("")
	var hp *config.Host
	hostFile := "none (default template)"
	if found {
		hp = &self
		hostFile = "hosts/" + self.Name + ".yaml"
	}
	res, err := config.Resolve(c, hp)
	if err != nil {
		return err
	}

	nimble.Header("This machine")
	nimble.Info("system", fmt.Sprintf("%s %s, %s@%s", runtime.GOOS, runtime.GOARCH, nimble.CurrentUser(), strings.Split(host, ".")[0]))
	nimble.Info("host file", hostFile)
	tmpl := res.Template
	if tmpl == "" {
		tmpl = "none"
	}
	nimble.Info("template", tmpl)
	if nimble.IsSnapshot() {
		nimble.Info("config", nimble.Tilde(nimble.Repo())+" — "+nimble.SnapshotInfo())
	} else {
		nimble.Info("config repo", nimble.Tilde(nimble.Repo())+" — "+repoState())
	}
	shell := nimble.LoginShell()
	switch want := res.Shell; {
	case want == "":
		r.OK("login shell", shell+" (system default)")
	case want == "zsh" && (strings.HasSuffix(shell, "/zsh") || strings.HasSuffix(shell, "/nimble-shell")):
		r.OK("login shell", shell)
	case want == "bash" && strings.HasSuffix(shell, "/bash"):
		r.OK("login shell", shell)
	default:
		r.Bad("login shell", shell+" (template wants "+want+") — nimble apply")
	}

	nimble.Header("Tools (from the template and host file)")
	if len(res.Tools) == 0 {
		nimble.Info("", "none — nothing is installed by nimble on this machine")
	}
	for _, name := range res.Tools {
		t, ok := setup.FindTool(name)
		if !ok {
			r.Bad(name, "not in the tool catalog")
			continue
		}
		switch name {
		case "starship":
			if v := nimble.Field(nimble.Output("starship", "--version"), 1); v != "" {
				r.OK("starship", v+" (kept current)")
			} else {
				r.Bad("starship", "missing")
			}
		case "node":
			nodeStatus(r, p)
		case "go":
			goStatus(r, p)
		case "python":
			pythonStatus(r, p)
		case "rust":
			have := strings.SplitN(nimble.Output(filepath.Join(nimble.Home(), ".cargo", "bin", "rustup"), "default"), "-", 2)[0]
			r.Same("rust (rustup default)", p.Rust, have)
		default:
			if t.Installed() {
				r.OK(name, "installed")
			} else {
				r.Bad(name, "missing")
			}
		}
	}
	if nimble.IsDebian() {
		if have := nimble.ReadTrim("/usr/local/share/pam_rssh.version"); have == "" {
			r.Bad("pam_rssh", "missing — the host isn't hardened yet: nimble init")
		} else {
			r.OK("pam_rssh", nimble.Field(have, 0)+" (shipped with nimble)")
		}
	}

	var added []string
	for _, f := range []string{".zshenv", ".zprofile", ".zshrc", ".bashrc", ".profile"} {
		if n := len(setup.StubTail(f)); n > 0 {
			added = append(added, fmt.Sprintf("%d in ~/%s", n, f))
		}
	}
	if len(added) > 0 {
		nimble.Header("Shell files")
		r.Note("added by other tools", strings.Join(added, ", ")+" (kept as they are)")
	}
	return r.Done("run `nimble apply` (or `nimble update` to pull the config first)")
}

func nodeStatus(r *nimble.Report, p *config.Pins) {
	nvm := os.Getenv("NVM_DIR")
	if v := nimble.JSONField(filepath.Join(nvm, "package.json"), "version"); v != "" {
		r.OK("nvm", v+" (kept current)")
	} else {
		r.Bad("nvm", "missing")
	}
	nodeDir := filepath.Join(nvm, "versions", "node", "v"+p.Node)
	have := ""
	if nimble.Exists(filepath.Join(nodeDir, "bin", "node")) {
		have = strings.TrimPrefix(nimble.ReadTrim(filepath.Join(nvm, "alias", "default")), "v")
	}
	r.Same("node (default)", p.Node, have)
	// node brings ts (@nimblescape/typescript) and the TypeScript ts installs globally
	for _, pkg := range []struct{ label, name string }{{"ts", "@nimblescape/typescript"}, {"typescript (global)", "typescript"}} {
		if got := nimble.JSONField(filepath.Join(nodeDir, "lib", "node_modules", pkg.name, "package.json"), "version"); got != "" {
			r.OK(pkg.label, got+" (kept current)")
		} else {
			r.Bad(pkg.label, "missing")
		}
	}
}

func goStatus(r *nimble.Report, p *config.Pins) {
	r.Same("go (~/sdk, default)", p.Go, setup.GoVersion())
	gobin := setup.GoBinDir()
	if gobin == "" {
		gobin = filepath.Join(nimble.Home(), "go", "bin")
	}
	var names []string
	for _, pkg := range p.GoTools {
		names = append(names, setup.GoToolBin(pkg))
	}
	r.Present("go tools", names, func(n string) bool { return nimble.Exists(filepath.Join(gobin, n)) })
}

func pythonStatus(r *nimble.Report, p *config.Pins) {
	if v := nimble.Field(nimble.Output("uv", "--version"), 1); v != "" {
		r.OK("uv", v+" (kept current)")
	} else {
		r.Bad("uv", "missing")
	}
	r.Same("python (default)", p.Python,
		nimble.Output(filepath.Join(nimble.Home(), ".local", "bin", "python3"), "-c", "import platform; print(platform.python_version())"))
	r.Same("uv global pin", p.Python, nimble.ReadTrim(filepath.Join(nimble.XDGConfig(), "uv", ".python-version")))
}

func repoState() string {
	if !nimble.IsCheckout() {
		return "no git checkout"
	}
	s := fmt.Sprintf("%s @ %s", nimble.Git("rev-parse", "--abbrev-ref", "HEAD"), nimble.Git("log", "-1", "--format=%h %cs"))
	if n := len(nimble.Lines(nimble.Git("status", "--porcelain"))); n > 0 {
		s += fmt.Sprintf(", %d uncommitted", n)
	}
	if lr := strings.Fields(nimble.Git("rev-list", "--left-right", "--count", "@{u}...HEAD")); len(lr) == 2 {
		if lr[0] == "0" && lr[1] == "0" {
			s += ", in sync with " + nimble.Git("rev-parse", "--abbrev-ref", "@{u}") + " (last fetch)"
		} else {
			s += fmt.Sprintf(", %s behind / %s ahead of origin (last fetch)", lr[0], lr[1])
		}
	}
	return s
}
