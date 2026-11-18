package update

import (
	"errors"
	"fmt"
	"os"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/push"
	"github.com/nimblescape/nimble/cmd/self"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/harden"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/remote"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdUpdate() *cobra.Command {
	return &cobra.Command{
		Use:   "update [host…|all]",
		Short: "Pull the config and apply it here, or send it to hosts and apply it there",
		Long: heredoc.Doc(`
			Without hosts: pull ~/.nimble (e.g. a merged pin-update pull request; your own
			unpublished commits go on top), apply it on this machine and bring the tools and
			a released nimble up to date; on Debian, re-run the hardening too (it syncs keys).

			With hosts (from a workstation): first read back what was changed in a host's own
			host file there (tool select, service, host set|edit on the host) and merge it into
			the config repo — a clash opens in the editor — and commit it; then send each
			managed host its config snapshot — only what it needs, no git history, nothing
			about other machines — and nimble, then harden and apply there. Hosts need no
			GitHub access; sudo there is confirmed with your hardware key through the
			forwarded agent. On a host, nimble update re-applies its snapshot.
		`),
		Example: heredoc.Doc(`
			nimble update              # this machine
			nimble update all          # then every managed host
		`),
		ValidArgsFunction: hosts.Complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return local()
			}
			return ToHosts(args)
		},
	}
}

func local() error {
	if nimble.IsSnapshot() { // a host: its config comes from the workstation
		nimble.Step("config: " + nimble.SnapshotInfo() + " — new config: nimble update <this host> on the workstation")
	} else if !nimble.IsCheckout() {
		return errors.New("no config repo at " + nimble.Tilde(nimble.Repo()) + " — run nimble init")
	}
	if up := nimble.Git("rev-parse", "--abbrev-ref", "@{u}"); !nimble.IsSnapshot() && up != "" {
		nimble.Step("pull " + up)
		if err := push.Pull(nimble.Repo()); err != nil { // a conflict is resolved, or nothing changes
			return err
		}
	}
	if p := config_personal(); p != "" {
		nimble.Step("pull your personal layer")
		if err := push.Pull(p); err != nil {
			nimble.Warn("personal layer not pulled: " + err.Error()) // the shared config still applies
		}
	}
	nimble.Step("apply")
	applied := setup.Apply(setup.Options{Upgrade: true})
	if applied != nil && !errors.Is(applied, setup.ErrTools) {
		return applied
	}
	// a tool that failed to install doesn't hold up the hardening (it syncs the keys)
	if nimble.IsDebian() {
		if err := HardenHere(); err != nil {
			return err
		}
	}
	if !nimble.IsSnapshot() { // a host gets nimble from the workstation
		self.AutoUpgrade()
	}
	return applied
}

// HardenHere hardens this Debian machine (and brings its services in line) once that can
// work: with a host file and at least one trusted key. Until then it only says so.
func HardenHere() error {
	name, missing := harden.Ready()
	if missing != "" {
		nimble.Step(nimble.Yellow("note: this machine isn't hardened yet — " + missing + ", then nimble update"))
		return nil
	}
	nimble.Step("harden (syncs the keys)")
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := nimble.Run("sudo", exe, "harden", "--user", nimble.CurrentUser(), "--as", name); err != nil {
		return err
	}
	if h, ok := config.FindHost(name); ok && len(h.Services) > 0 {
		return nimble.Run("sudo", exe, "service", "sync", "--as", name)
	}
	return nil
}

// config_personal is the personal layer when it is a checkout of its own.
func config_personal() string {
	p := nimble.RepoFile("personal")
	if nimble.Exists(p + "/.git") {
		return p
	}
	return ""
}

// ToHosts sends the config to hosts and applies it there (nimble update <host>…).
func ToHosts(args []string) error {
	if !nimble.IsCheckout() {
		return errors.New("sending the config to hosts needs the checkout — run this on a workstation")
	}
	if b := nimble.Git("rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		return fmt.Errorf("the checkout is on %q; switch to main first", b)
	}
	list, err := hosts.List(args)
	if err != nil {
		return err
	}
	for _, name := range list { // before anything is sent: only managed hosts get a config, nimble and hardening
		h, ok := config.FindHost(name)
		if !ok {
			return fmt.Errorf("%s is not in hosts/ (nimble host list)", name)
		}
		if !h.Managed() {
			return fmt.Errorf("%s isn't a managed host (mode: %s) — nimble only sets up and updates managed hosts", name, h.Mode)
		}
	}
	if len(nimble.Lines(nimble.Git("status", "--porcelain"))) > 0 {
		nimble.Step(nimble.Yellow("note: the hosts get the files as they are here, uncommitted changes included — nimble push commits and publishes them"))
	}
	return hosts.Each(list, func(h string) error {
		if err := SendConfig(h); err != nil { // what was changed there comes back first
			return err
		}
		if err := nimble.Wait("send nimble", func() error { return remote.SendBinary(h) }); err != nil {
			return err
		}
		// Each step runs whatever the one before did: a tool that fails to install must not keep
		// the keys from being synced. The update fails if any of them failed.
		nimble.Step("harden (syncs the keys), apply, services")
		n := `"$HOME/.local/bin/nimble"`
		return hosts.Run(h, hosts.Opts{Agent: true},
			`sudo `+n+` harden --as `+h+`; r1=$?; `+n+` apply --upgrade --as `+h+`; r2=$?; sudo `+n+` service sync --as `+h+
				`; r3=$?; [ "$r1$r2$r3" = 000 ]`)
	})
}
