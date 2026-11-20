package initialize

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/update"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/remote"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/nimblescape/nimble/pkg/skeleton"
	"github.com/spf13/cobra"
)

func NewCmdInit() *cobra.Command {
	var repoURL string
	var fresh bool
	cmd := &cobra.Command{
		Use:   "init [host…]",
		Short: "Set up this machine, or (from a workstation) Debian hosts",
		Long: heredoc.Doc(`
			Without hosts: set up this machine. If ~/.nimble doesn't exist yet, clone a config
			repo there (--repo; a private repo needs your GitHub login) or create a new one
			(--new: the shared shell files, a base template that installs nothing, this
			machine's host file; offered as a private GitHub repo). Then apply it; on a Debian
			host, harden it first.

			With hosts (from a workstation), for each host in hosts/:

			  1. if key login as its admin user doesn't work yet: log in once with a password
			     as whatever user the host offers, become root (directly or via sudo), create
			     the admin user with a password, authorize the domain's trusted keys
			  2. confirm key login as the admin user — nothing is locked down before
			  3. send nimble and the host's config snapshot (only what it needs)
			  4. harden: root cannot log in at all, SSH keys only, only the admin user may
			     sudo, confirmed with a hardware key (asks for the password this once)
			  5. apply the host file: login shell, tools
			  6. check sudo with the hardware key
		`),
		Example: heredoc.Doc(`
			nimble init                                    # asks: create a config repo, or clone one
			nimble init --new                              # your first machine: a new config repo
			nimble init --repo <you>/nimble-config         # another workstation
			nimble init devbox                             # a host, from the workstation
		`),
		ValidArgsFunction: hosts.Complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return local(repoURL, fresh)
			}
			return hosts.Each(args, InitHost)
		},
	}
	cmd.Flags().StringVar(&repoURL, "repo", "", "config repo to clone into ~/.nimble (owner/name or URL)")
	cmd.Flags().BoolVar(&fresh, "new", false, "create a new config repo in ~/.nimble")
	return cmd
}

func local(repoURL string, fresh bool) error {
	if !nimble.IsCheckout() { // git first: cloning or creating the repo needs it
		if err := setup.Prerequisites(); err != nil {
			return err
		}
	}
	if !nimble.IsCheckout() {
		if nimble.Exists(nimble.Repo()) {
			return errors.New(nimble.Tilde(nimble.Repo()) + " exists but is not a git checkout")
		}
		if repoURL == "" && !fresh {
			if !nimble.Interactive() {
				return errors.New("no config repo at " + nimble.Tilde(nimble.Repo()) + " — pass --repo <owner/name or URL>, or --new")
			}
			choice, err := nimble.Select("No config repo at "+nimble.Tilde(nimble.Repo())+" yet:", []string{"create a new one", "clone an existing one"}, "")
			if err != nil {
				return err
			}
			if fresh = strings.HasPrefix(choice, "create"); !fresh {
				if repoURL, err = nimble.Input("Config repo (owner/name or URL):", "", func(v string) error {
					if strings.Contains(v, ":") || strings.HasPrefix(v, "/") {
						return nil
					}
					return nimble.GitHubRepo(v)
				}); err != nil {
					return err
				}
			}
		}
		if fresh {
			if err := create(); err != nil {
				return err
			}
			repoURL = ""
		}
	}
	if repoURL != "" && !nimble.IsCheckout() {
		if !strings.Contains(repoURL, ":") && !strings.HasPrefix(repoURL, "/") {
			repoURL = "https://github.com/" + repoURL + ".git"
		}
		nimble.Step("clone " + repoURL + " → " + nimble.Tilde(nimble.Repo()))
		if err := nimble.Run("git", "clone", "-q", repoURL, nimble.Repo()); err != nil {
			return errors.New("clone failed — for a private repo, log in first (gh auth login), then retry")
		}
	}
	if nimble.IsDebian() { // a Debian workstation is hardened like a host, once it has a key to trust
		if err := update.HardenHere(); err != nil {
			return err
		}
	}
	if err := setup.Apply(setup.Options{}); err != nil {
		return err
	}
	if setup.Darwin() {
		fmt.Println()
		nimble.Step("next on this Mac: nimble key enclave (daily SSH key in the Secure Enclave), then " +
			nimble.Tilde(nimble.RepoFile("macos", "touchid-sudo.sh")) + " (Touch ID for sudo)")
	}
	return nil
}

// InitHost sets up one host from the workstation (used by init and host create).
func InitHost(name string) error {
	h, ok := config.FindHost(name)
	if !ok {
		return fmt.Errorf("%s is not in hosts/ — create it first: nimble host create %s", name, name)
	}
	if !h.Managed() {
		return fmt.Errorf("%s is an ssh entry (mode: %s), not a managed host", name, h.Mode)
	}
	nimble.Step("admin user " + h.AdminUser() + ", domain " + h.Domain)

	nimble.Step("check key login (hardware key)")
	if !remote.KeyLogin(name) {
		if err := remote.Bootstrap(h); err != nil {
			return err
		}
		if !remote.KeyLogin(name) {
			return errors.New("key login as " + h.AdminUser() + " still fails — nothing was locked down")
		}
		nimble.Say("key login as " + h.AdminUser() + " works")
	}

	if err := nimble.Wait("send nimble", func() error { return remote.SendBinary(name) }); err != nil {
		return err
	}
	if err := update.SendConfig(name); err != nil { // a host set up before: what was changed there comes back first
		return err
	}

	nimble.Step("harden + apply (sudo asks for " + h.AdminUser() + "'s password this one time)")
	setupCmd := `sudo "$HOME/.local/bin/nimble" harden --user ` + h.AdminUser() + ` --as ` + name +
		` && "$HOME/.local/bin/nimble" apply --as ` + name
	if err := hosts.Run(name, hosts.Opts{Fresh: true, Agent: true}, setupCmd); err != nil {
		return err
	}
	update.KeysSynced(name)
	update.FinishHandovers()

	nimble.Step("check sudo with the hardware key")
	return hosts.Run(name, hosts.Opts{Fresh: true, Agent: true}, `sudo -k; sudo -v && echo "sudo with the hardware key: ok"`)
}

// create makes a new config repo in ~/.nimble from the skeleton, commits it and offers to
// publish it as a private GitHub repo.
func create() error {
	// the first commit needs who you are: say so before anything is asked or written
	if nimble.Output("git", "config", "user.name") == "" || nimble.Output("git", "config", "user.email") == "" {
		return errors.New(`git doesn't know who you are yet, and the new config repo is committed in your name — first: git config --global user.name "Your Name" && git config --global user.email you@example.com`)
	}
	dir := nimble.Repo()
	domain := "default"
	if nimble.Interactive() {
		var err error
		if domain, err = nimble.Input("First administration domain (who runs your hosts):", domain, func(v string) error {
			if v == "" || strings.ContainsAny(v, " /:") {
				return errors.New("a short name without spaces")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	machine := hosts.MachineName()
	nimble.Step("new config repo → " + nimble.Tilde(dir) + " (domain " + domain + ", this machine: hosts/" + machine + ".yaml)")
	if err := skeleton.Write(dir, domain, machine); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "nimble: new config repo"}} {
		if err := nimble.Run("git", append([]string{"-C", dir}, args...)...); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("gh"); err != nil || exec.Command("gh", "auth", "status").Run() != nil {
		nimble.Step("publish it later: gh repo create <name> --private --source " + nimble.Tilde(dir) + " --push")
		return nil
	}
	if !nimble.Interactive() || !nimble.Confirm("Create a private GitHub repo for it and push?", true) {
		nimble.Step("publish it later: gh repo create <name> --private --source " + nimble.Tilde(dir) + " --push")
		return nil
	}
	// A config repo in your own account is your configuration; one in an organization's
	// account is a configuration shared by its members.
	name, err := nimble.Input("Repo name (in your account; org/name shares the config with an organization):", "nimble-config", nil)
	if err != nil {
		return err
	}
	return nimble.Run("gh", "repo", "create", name, "--private", "--source", dir, "--remote", "origin", "--push")
}
