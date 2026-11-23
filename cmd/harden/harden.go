package harden

import (
	"os"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/harden"
	"github.com/spf13/cobra"
)

func NewCmdHarden() *cobra.Command {
	var userName, as string
	var check bool
	cmd := &cobra.Command{
		Use:    "harden",
		Short:  "Lock down this Debian host (as root)",
		Hidden: true,
		Long: heredoc.Doc(`
			Lock down this Debian host for its admin user (by default the one who ran sudo):
			root cannot log in at all, SSH keys only (the keys its domain trusts), only the
			admin user may sudo, confirmed with a hardware key through the forwarded agent.
			Safe to re-run; it also adds and removes keys. init and update run it. Every run
			ends with an audit of the effective configuration (sshd -T, sudo -l for every
			account, PAM, accounts, the sudo keys); --check runs only the audit.
		`),
		Example: `  sudo ~/.local/bin/nimble harden --as devbox`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return harden.Check(userName, as)
			}
			return harden.Run(userName, as)
		},
	}
	def := os.Getenv("SUDO_USER")
	if def == "" {
		def = "debian"
	}
	cmd.Flags().StringVar(&userName, "user", def, "the admin user")
	cmd.Flags().StringVar(&as, "as", "", "this host's name in hosts/ (default: remembered from the last run)")
	cmd.Flags().BoolVar(&check, "check", false, "only audit the effective configuration, change nothing")
	return cmd
}
