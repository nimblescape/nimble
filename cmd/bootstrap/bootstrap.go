package bootstrap

import (
	"github.com/nimblescape/nimble/pkg/harden"
	"github.com/spf13/cobra"
)

func NewCmdBootstrap() *cobra.Command {
	var userName, keysFile, binary string
	cmd := &cobra.Command{
		Use:    "bootstrap",
		Short:  "Prepare a fresh host as root: admin user, trusted keys, nimble",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return harden.Bootstrap(userName, keysFile, binary)
		},
	}
	cmd.Flags().StringVar(&userName, "user", "debian", "the admin user to create or prepare")
	cmd.Flags().StringVar(&keysFile, "keys", "", "file with the public keys to authorize")
	cmd.Flags().StringVar(&binary, "binary", "", "nimble binary to install for the admin user")
	return cmd
}
