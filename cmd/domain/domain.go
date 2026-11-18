package domain

import (
	"fmt"

	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

func NewCmdDomain() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain <command>",
		Short: "Administration domains: who runs which hosts, and so which keys they trust",
		Long: heredoc.Doc(`
			Domains live in nimble.yaml. Each has the label of its daily key (a Secure Enclave
			key on every workstation); a host trusts its domain's daily keys plus every
			YubiKey backup. The first domain is the default.
		`),
	}
	var label string
	create := &cobra.Command{
		Use: "create <name>", Short: "Add a domain", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "domain name", nil)
			if err != nil {
				return err
			}
			if label == "" { // the name, without the "-" that separates machine and label in keys/enclave/
				label = strings.ReplaceAll(name, "-", "_")
			}
			if err := config.ValidLabel(label); err != nil {
				return err
			}
			if err := config.AddDomain(name, label); err != nil {
				return err
			}
			nimble.Say("domain " + name + " (daily key label " + label + ")")
			if err := nimble.Record("domains: add "+name, "nimble.yaml"); err != nil {
				return err
			}
			nimble.Step("its daily key on this Mac: nimble key enclave " + name)
			return nil
		},
	}
	create.Flags().StringVar(&label, "label", "", "label of the domain's daily key (default: the domain name)")
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "Show the domains and their trusted keys", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			for i, d := range c.Domains {
				def := ""
				if i == 0 {
					def = " (default)"
				}
				fmt.Printf("%s%s — daily key label %q, %d trusted key(s)\n", nimble.Bold(d.Name), def, d.Label, len(keys.Trusted(d)))
			}
			return nil
		},
	}, create)
	return cmd
}
