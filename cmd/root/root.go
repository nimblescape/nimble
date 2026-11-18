package root

import (
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/apply"
	"github.com/nimblescape/nimble/cmd/bootstrap"
	"github.com/nimblescape/nimble/cmd/doctor"
	"github.com/nimblescape/nimble/cmd/domain"
	"github.com/nimblescape/nimble/cmd/harden"
	"github.com/nimblescape/nimble/cmd/host"
	"github.com/nimblescape/nimble/cmd/initialize"
	"github.com/nimblescape/nimble/cmd/key"
	"github.com/nimblescape/nimble/cmd/push"
	"github.com/nimblescape/nimble/cmd/repo"
	"github.com/nimblescape/nimble/cmd/self"
	"github.com/nimblescape/nimble/cmd/service"
	"github.com/nimblescape/nimble/cmd/status"
	"github.com/nimblescape/nimble/cmd/template"
	"github.com/nimblescape/nimble/cmd/tool"
	"github.com/nimblescape/nimble/cmd/update"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

// edits lists the commands that change the config repo beyond one host file. A host holds
// only a snapshot of it, so they are refused there before they write anything. (host set,
// host edit, tool select, tool shell and service may change the host's own file there: they check
// nimble.EditableHost themselves.)
var edits = map[string]bool{
	"host create": true, "host remove": true,
	"template create": true, "template set": true, "template edit": true, "template default": true, "template remove": true,
	"domain create": true, "key enclave": true, "key yubikey": true, "key remove": true,
	"tool use": true, "tool update": true,
}

func NewRootCmd(version string) *cobra.Command {
	var yubikey bool
	cmd := &cobra.Command{
		Use:   "nimble <command>",
		Short: "Set up and keep identical the developer environment on workstations and Debian hosts",
		Long: heredoc.Doc(`
			nimble sets up a workstation (macOS or Debian) and the Debian hosts reached over
			SSH from one config repo (~/.nimble), and keeps them identical: shell, linked
			config, pinned toolchains and tools, SSH keys and host hardening.

			Verbs act on machines — this one, or hosts by name or "all". Nouns manage the
			inventories in the config repo; every change is checked and committed, and
			nimble push publishes it.
		`),
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(c *cobra.Command, args []string) error {
			if edits[strings.TrimPrefix(c.CommandPath(), "nimble ")] {
				if err := nimble.Editable(); err != nil {
					return err
				}
			}
			if yubikey {
				return hosts.UseYubiKey()
			}
			return nil
		},
	}
	cmd.PersistentFlags().BoolVar(&yubikey, "yubikey", false,
		"reach the hosts with a YubiKey backup instead of the daily key (a new workstation, a lost or expired key)")
	cmd.AddGroup(&cobra.Group{ID: "machines", Title: "Machines:"}, &cobra.Group{ID: "inventories", Title: "Inventories:"},
		&cobra.Group{ID: "nimble", Title: "nimble and its repo:"})

	for _, c := range []*cobra.Command{initialize.NewCmdInit(), update.NewCmdUpdate(), status.NewCmdStatus(), doctor.NewCmdDoctor()} {
		c.GroupID = "machines"
		cmd.AddCommand(c)
	}
	for _, c := range []*cobra.Command{host.NewCmdHost(), template.NewCmdTemplate(), tool.NewCmdTool(), service.NewCmdService(),
		key.NewCmdKey(), domain.NewCmdDomain()} {
		c.GroupID = "inventories"
		cmd.AddCommand(c)
	}
	for _, c := range []*cobra.Command{push.NewCmdPush(), push.NewCmdChanges(), self.NewCmdVersion(version), self.NewCmdUpgrade(), repo.NewCmdPath(), repo.NewCmdEdit()} {
		c.GroupID = "nimble"
		cmd.AddCommand(c)
	}
	// plumbing that init and update run
	cmd.AddCommand(apply.NewCmdApply(), harden.NewCmdHarden(), bootstrap.NewCmdBootstrap())
	return cmd
}
