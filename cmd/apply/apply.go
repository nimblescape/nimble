package apply

import (
	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdApply() *cobra.Command {
	var o setup.Options
	cmd := &cobra.Command{
		Use:    "apply",
		Short:  "Apply the config repo to this machine, without pulling",
		Hidden: true,
		Long: heredoc.Doc(`
			Apply ~/.nimble as it is: the managed dotfiles, the linked config, the ssh
			config (workstations), the login shell and the tools of this machine's template.
			init and update run it; safe to re-run.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return setup.Apply(o) },
	}
	cmd.Flags().BoolVar(&o.NoTools, "no-tools", false, "only configs, no tools")
	cmd.Flags().BoolVar(&o.Upgrade, "upgrade", false, "also update the tools that stay current (nimble update does)")
	cmd.Flags().StringVar(&o.As, "as", "", "this host's name in hosts/ (remembered)")
	return cmd
}
