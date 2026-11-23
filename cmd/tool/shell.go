package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/update"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

// newShell is tool shell: pick a machine's login shell and apply it.
func newShell() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [zsh|bash|default]",
		Short: "Select a machine's login shell: zsh, bash or the system's (this machine, or --host)",
		Long: heredoc.Doc(`
			Pick the machine's login shell, or name it: zsh, bash, or default — the system's
			(the host file then sets none). It is recorded in the host file (shell:) and
			applied now: on this machine the dotfiles and the login shell (tools are left as
			they are), with --host (from the workstation) on that host. On a host it changes
			the host's own file; the workstation's next nimble update <host> takes the change
			into the config repo.
		`),
		Example: heredoc.Doc(`
			nimble tool shell                    # pick this machine's shell
			nimble tool shell bash               # this machine: bash
			nimble tool shell zsh --host devbox  # devbox, from the workstation
			nimble tool shell default            # the system's login shell
		`),
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"zsh\tthe Z shell", "bash\tGNU bash", "default\tthe system's login shell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			h, remote, err := target()
			if err != nil {
				return err
			}
			if remote && nimble.Interactive() {
				// first take in what was changed on the host since its last snapshot
				if err := update.ReadBack(h.Name); err != nil {
					return err
				}
				h, _ = config.FindHost(h.Name)
			}
			var want string
			if len(args) > 0 {
				if want = strings.ToLower(args[0]); want != "zsh" && want != "bash" && want != "default" {
					return fmt.Errorf("unknown shell %q (zsh, bash or default)", args[0])
				}
			} else if want, err = pickShell(h); err != nil {
				return err
			}
			return setShell(h, remote, want)
		},
	}
	cmd.Flags().StringVar(&onHost, "host", "", "a host instead of this machine (from the workstation)")
	cmd.RegisterFlagCompletionFunc("host", hosts.CompleteHostNames)
	return cmd
}

// shellChoices is tool shell's list, the host file's setting marked. names maps a label
// back to zsh, bash or default.
func shellChoices(current string) (labels []string, selected string, names map[string]string) {
	names = map[string]string{}
	if current == "" {
		current = "default"
	}
	for _, s := range []struct{ name, desc string }{{"zsh", "the Z shell"}, {"bash", "GNU bash"}, {"default", "the system's login shell"}} {
		l := fmt.Sprintf("%-8s %s", s.name, s.desc)
		if s.name == current {
			l += " (current)"
			selected = l
		}
		labels, names[l] = append(labels, l), s.name
	}
	return labels, selected, names
}

func pickShell(h config.Host) (string, error) {
	if !nimble.Interactive() {
		return "", errors.New("missing shell (zsh, bash or default): nimble tool shell bash")
	}
	labels, selected, names := shellChoices(h.Shell)
	chosen, err := nimble.Select("Login shell of "+h.Name+" (ESC or Ctrl+C to cancel):", labels, selected)
	if err != nil {
		return "", err
	}
	return names[chosen], nil
}

// setShell writes the shell into h's host file ("default" removes it), commits it and
// applies it: here only the configs and the login shell, with --host on that host.
func setShell(h config.Host, remote bool, want string) error {
	value := want
	if want == "default" {
		value = ""
	}
	switch {
	case value == h.Shell && value == "":
		nimble.Say(h.Name + "'s host file sets no shell already — the system's login shell")
		return nil
	case value == h.Shell:
		nimble.Say(h.Name + "'s shell is " + value + " already")
		return nil
	}
	if errs := config.Validate(setup.KnownTools()); len(errs) > 0 { // nothing to apply it with
		return fmt.Errorf("%w — fix that first (nimble doctor lists the problems)", errs[0])
	}
	if err := config.SetHost(h.Name, "shell", value); err != nil {
		return err
	}
	what := "shell " + want
	if value == "" {
		what = "shell default (the system's)"
		nimble.Say("hosts/" + h.Name + ".yaml: no shell — the system's login shell")
	} else {
		nimble.Say("hosts/" + h.Name + ".yaml: shell " + value)
	}
	if err := nimble.Record("hosts: "+h.Name+" "+what, "hosts"); err != nil {
		return err
	}
	if remote {
		return update.ToHosts([]string{h.Name})
	}
	return setup.Apply(setup.Options{NoTools: true})
}
