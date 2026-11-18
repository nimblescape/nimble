package service

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/update"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/services"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

// Services nimble knows. claude is a Claude Code Remote Control server for one repo
// (pkg/services): a service user without SSH login, one unit per repo.
var known = map[string]string{
	"claude": "Claude Code server for a repo",
}

func NewCmdService() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "service <command>",
		Short: "Long-running services for repos on a host (e.g. a Claude Code server)",
		Long: heredoc.Doc(`
			Services live in their host's file (services:). add installs and enables one,
			disable stops it and keeps it installed, enable starts it again, remove uninstalls
			it. Without --host the commands act on this machine — on a host too: the
			workstation's next nimble update <host> takes the change into the config repo.
			Without <repo> they offer the repo checked out in the current folder (its origin,
			e.g. owner/app) as the default, and any repo must exist on GitHub.
		`),
	}
	cmd.PersistentFlags().StringVar(&host, "host", "", "the host (default: this machine)")
	cmd.PersistentFlags().BoolVar(&force, "force", false, "remove: also a checkout with uncommitted or unpushed work")
	cmd.RegisterFlagCompletionFunc("host", hosts.CompleteHostNames)
	pick := func() (config.Host, error) {
		if host != "" {
			if err := nimble.EditableHost(host); err != nil { // on a host: only its own
				return config.Host{}, err
			}
			h, ok := config.FindHost(host)
			if !ok {
				return h, errors.New("no host " + host)
			}
			return h, nil
		}
		h, ok := hosts.Self("")
		if !ok {
			return h, errors.New("this machine has no host file — pass --host or create one")
		}
		return h, nil
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "Show the services and their state", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				hs, err := config.Hosts()
				if err != nil {
					return err
				}
				for _, h := range hs {
					if host != "" && h.Name != host {
						continue
					}
					self, _ := hosts.Self("")
					for _, s := range h.Services {
						actual := ""
						if h.Name == self.Name && nimble.IsDebian() {
							actual = "  (here: " + services.State(s.Repo) + ")"
						}
						fmt.Printf("  %-12s %-8s %-34s %s%s\n", h.Name, s.Service, s.Repo, s.State, actual)
					}
				}
				return nil
			}},
		change("add", "Install a service for a repo and enable it", pick, func(list []config.Service, s config.Service) ([]config.Service, error) {
			for _, x := range list {
				if x.Service == s.Service && x.Repo == s.Repo {
					return list, nil // recorded already (e.g. before the host was set up): install it
				}
			}
			s.State = "enabled"
			return append(list, s), nil
		}),
		change("enable", "Start a service and start it at boot", pick, setState("enabled")),
		change("disable", "Stop a service and keep it installed", pick, setState("disabled")),
		change("remove", "Uninstall a service", pick, func(list []config.Service, s config.Service) ([]config.Service, error) {
			var out []config.Service
			found := false
			for _, x := range list {
				if x.Service == s.Service && x.Repo == s.Repo {
					found = true
					continue
				}
				out = append(out, x)
			}
			if !found {
				return nil, errors.New("no such service")
			}
			return out, nil
		}),
		newApply(), newSync(),
	)
	return cmd
}

var force bool

// newApply is the plumbing the service commands run on the host, as root.
func newApply() *cobra.Command {
	var admin string
	c := &cobra.Command{
		Use: "apply <add|enable|disable|remove> <service> <repo>", Hidden: true, Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			verb, repo := args[0], args[2]
			switch verb {
			case "add":
				if err := services.Install(repo, admin); err != nil {
					return err
				}
				return services.Enable(repo)
			case "enable":
				return services.Enable(repo)
			case "disable":
				return services.Disable(repo)
			case "remove":
				return services.Remove(repo, force)
			}
			return fmt.Errorf("unknown action %q", verb)
		},
	}
	c.Flags().StringVar(&admin, "user", os.Getenv("SUDO_USER"), "the admin user (read access to the checkouts)")
	return c
}

// newSync brings the installed services in line with the host file (nimble update runs it).
func newSync() *cobra.Command {
	var as string
	c := &cobra.Command{
		Use: "sync", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, ok := hosts.Self(as)
			if !ok {
				return nil
			}
			for _, s := range h.Services {
				switch {
				case !services.Installed(s.Repo):
					nimble.Step(nimble.Yellow("note: " + s.Service + " " + s.Repo + " is not installed here yet — nimble service add " + s.Service + " " + s.Repo + " (it asks for the logins)"))
				case s.State == "enabled" && services.State(s.Repo) != "enabled":
					if err := services.Enable(s.Repo); err != nil {
						return err
					}
				case s.State == "disabled" && services.State(s.Repo) != "disabled":
					if err := services.Disable(s.Repo); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&as, "as", "", "this host's name in hosts/")
	return c
}

// act carries a recorded change out on the host: here (as root), or from the workstation
// over ssh after sending the config.
func act(h config.Host, verb, svc, repo string) error {
	plumbing := "service apply " + verb + " " + svc + " " + repo + " --user " + h.AdminUser()
	if force {
		plumbing += " --force"
	}
	if self, ok := hosts.Self(""); ok && self.Name == h.Name {
		if setup.Darwin() {
			nimble.Step("recorded — services run on Debian hosts")
			return nil
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return nimble.Run("sudo", append([]string{exe}, strings.Fields(plumbing)...)...)
	}
	if !h.Managed() {
		return errors.New(h.Name + " isn't managed by nimble")
	}
	if err := update.ToHosts([]string{h.Name}); err != nil {
		return err
	}
	return hosts.Run(h.Name, hosts.Opts{Agent: true}, `sudo "$HOME/.local/bin/nimble" `+plumbing)
}

func setState(state string) func([]config.Service, config.Service) ([]config.Service, error) {
	return func(list []config.Service, s config.Service) ([]config.Service, error) {
		for i, x := range list {
			if x.Service == s.Service && x.Repo == s.Repo {
				list[i].State = state
				return list, nil
			}
		}
		return nil, errors.New("no such service — nimble service add first")
	}
}

func change(verb, short string, pick func() (config.Host, error),
	edit func([]config.Service, config.Service) ([]config.Service, error)) *cobra.Command {
	return &cobra.Command{
		Use: verb + " <service> [<repo>]", Short: short, Args: cobra.MaximumNArgs(2),
		Example: fmt.Sprintf("  nimble service %s claude owner/app --host devbox\n  cd ~/src/app && nimble service %s claude --host devbox", verb, verb),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := pick() // first: on a host it refuses another host's file before asking anything
			if err != nil {
				return err
			}
			svc, err := nimble.Arg(args, 0, "service", []string{"claude"})
			if err != nil {
				return err
			}
			here, _ := currentRepo()
			repo, err := nimble.ArgOr(args, 1, "repo", here, "from the current folder", nimble.GitHubRepo)
			if err != nil {
				return err
			}
			args = []string{svc, repo}
			if _, ok := known[args[0]]; !ok {
				return fmt.Errorf("unknown service %q (known: claude)", args[0])
			}
			if self, ok := hosts.Self(""); h.Managed() && (!ok || self.Name != h.Name) {
				// another host: first take in what was changed there since its last snapshot
				if err := update.ReadBack(h.Name); err != nil {
					return err
				}
				h, _ = config.FindHost(h.Name)
			}
			if verb == "add" {
				if done, err := installedAlready(h, args[0], args[1]); done || err != nil {
					return err
				}
			}
			list, err := edit(h.Services, config.Service{Service: args[0], Repo: args[1]})
			if err != nil {
				return fmt.Errorf("%s %s on %s: %w", args[0], args[1], h.Name, err)
			}
			// The host file changes first (the host gets it in its snapshot), but it is only
			// committed once the change worked there: a failed one leaves the inventory as it was.
			before, err := os.ReadFile(config.HostFile(h.Name))
			if err != nil {
				return err
			}
			if err := config.SetHostServices(h.Name, list); err != nil {
				return err
			}
			if err := act(h, verb, args[0], args[1]); err != nil {
				_ = os.WriteFile(config.HostFile(h.Name), before, 0o644)
				return err
			}
			return nimble.Record(fmt.Sprintf("hosts: %s service %s %s %s", h.Name, verb, args[0], args[1]), "hosts")
		},
	}
}

// installedAlready handles add for a server that is installed on the host already: nothing
// is installed again. It says how it stands and what would start it, and records it in the
// host file when only that was missing (installed from another workstation, or before the
// host file had it). done is false when it isn't installed, or the host can't be asked.
func installedAlready(h config.Host, svc, repo string) (done bool, err error) {
	state, running := probe(h, repo)
	if state == "" || state == "not installed" {
		return false, nil
	}
	where, flag := "on "+h.Name, " --host "+h.Name
	if self, ok := hosts.Self(""); ok && self.Name == h.Name {
		where, flag = "here", ""
	}
	nimble.Say(svc + " " + repo + " is installed " + where + " already (" + services.Describe(state, running) + ") — nothing to install")
	switch {
	case state == "disabled":
		nimble.Step("start it: nimble service enable " + svc + " " + repo + flag)
	case !running:
		nimble.Step("it doesn't run — " + where + ": sudo systemctl status " + services.Unit(repo) + ", then sudo systemctl restart " + services.Unit(repo))
	}
	for _, s := range h.Services {
		if s.Service == svc && s.Repo == repo {
			return true, nil
		}
	}
	list := append(append([]config.Service{}, h.Services...), config.Service{Service: svc, Repo: repo, State: state})
	if err := config.SetHostServices(h.Name, list); err != nil {
		return true, err
	}
	return true, nimble.Record(fmt.Sprintf("hosts: %s service add %s %s (installed already)", h.Name, svc, repo), "hosts")
}

// probe reads how a repo's server stands on h: here directly, on another host over ssh
// ("" when it can't be read). Tests replace it.
var probe = func(h config.Host, repo string) (state string, running bool) {
	if self, ok := hosts.Self(""); ok && self.Name == h.Name {
		if !nimble.IsDebian() {
			return "", false // a Mac: services run on Debian hosts
		}
		return services.State(repo), services.Active(repo)
	}
	if !h.Managed() {
		return "", false
	}
	var out string
	_ = nimble.Wait("check "+repo+" on "+h.Name, func() error {
		out = hosts.Output(h.Name, services.StateScript(repo))
		return nil
	})
	return services.ParseState(out)
}

// currentRepo names the repo checked out in the current folder, from its origin remote:
// owner/name on GitHub, host/owner/name elsewhere.
func currentRepo() (string, error) {
	url := nimble.Output("git", "remote", "get-url", "origin")
	if url == "" {
		return "", errors.New("not in a repo checkout with an origin remote — name the repo: <owner/name>")
	}
	return nimble.RepoName(url), nil
}
