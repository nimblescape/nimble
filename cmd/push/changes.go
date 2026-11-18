package push

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

func NewCmdChanges() *cobra.Command {
	var diff bool
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Show what has changed and isn't published yet: what the next nimble push publishes",
		Long: heredoc.Doc(`
			Show what the next nimble push publishes, without committing or pushing anything:
			the commits the upstream doesn't have yet, the hand edits not committed yet, and the
			changed files — for the config repo, and the personal layer when it is a repo of
			its own. It ends with a warning while something isn't published. --diff adds the
			full diff, in git's pager.
		`),
		Example: heredoc.Doc(`
			nimble changes          # what nimble push would publish
			nimble changes --diff   # and the full diff
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nimble.IsSnapshot() {
				return errors.New("this host's config is a snapshot from the workstation — changes are made and published there")
			}
			if !nimble.IsCheckout() {
				return errors.New("no config repo at " + nimble.Tilde(nimble.Repo()) + " — run nimble init")
			}
			var ps []pending
			for _, d := range dirs() {
				p := look(d)
				ps = append(ps, p)
				if p.empty() {
					nimble.Say(p.name + ": nothing changed since the last push")
					continue
				}
				p.show()
				if diff {
					base := "@{u}"
					if p.up == "" {
						base = "HEAD"
					}
					if err := nimble.Run("git", "-C", d, "diff", base); err != nil {
						return err
					}
				}
			}
			if w := unpublished(ps); w != "" {
				fmt.Println()
				nimble.Warn(w)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&diff, "diff", "d", false, "also show the full diff (in git's pager)")
	return cmd
}

// unpublished says what isn't published yet ("" when everything is).
func unpublished(ps []pending) string {
	commits, edits := 0, 0
	for _, p := range ps {
		commits += len(p.ahead)
		edits += len(p.changed)
	}
	var parts []string
	if commits > 0 {
		parts = append(parts, plural(commits, "commit"))
	}
	if edits > 0 {
		parts = append(parts, plural(edits, "uncommitted change"))
	}
	if len(parts) == 0 {
		return ""
	}
	them := "them"
	if commits+edits == 1 {
		them = "it"
	}
	return strings.Join(parts, " and ") + " not published yet — the other machines don't have " + them + ": nimble push"
}

// WarnUnpublished warns when the config repo (or the personal layer) has changes that aren't
// published yet. main calls it after every command; on a host (a snapshot, not a
// checkout) it says nothing.
func WarnUnpublished() {
	if !nimble.IsCheckout() || nimble.IsSnapshot() {
		return
	}
	var ps []pending
	for _, d := range dirs() {
		ps = append(ps, look(d))
	}
	if w := unpublished(ps); w != "" {
		nimble.Warn(w + " (nimble changes shows them)")
	}
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
