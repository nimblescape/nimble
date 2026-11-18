package push

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
			full diff, in git's pager. On a host: what was changed in its host file there and
			isn't in the config repo yet (the workstation's next nimble update <host> takes it).
		`),
		Example: heredoc.Doc(`
			nimble changes          # what nimble push would publish
			nimble changes --diff   # and the full diff
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nimble.IsSnapshot() {
				return hostChanges(diff)
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

// hostChanges is changes on a host: what was changed in its host file there since the
// snapshot, which the workstation reads back with its next nimble update <host>.
func hostChanges(diff bool) error {
	name := nimble.SnapshotHost()
	changed, log := nimble.HostChanges()
	if !changed {
		nimble.Say("hosts/" + name + ".yaml: nothing changed here since the " + nimble.SnapshotInfo())
		return nil
	}
	nimble.Say("hosts/" + name + ".yaml changed here:")
	if len(log) == 0 {
		log = []string{"(edited by hand)"}
	}
	for _, l := range log {
		fmt.Println("  " + l)
	}
	if diff {
		cmd := exec.Command("diff", "-u", "--label", "as sent", "--label", "here",
			nimble.RepoFile(nimble.SentDir, "hosts", name+".yaml"), nimble.RepoFile("hosts", name+".yaml"))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		var exit *exec.ExitError
		if err := cmd.Run(); err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) { // 1: they differ
			return err
		}
	}
	fmt.Println()
	nimble.Warn(hostPending(name))
	return nil
}

func hostPending(name string) string {
	return "hosts/" + name + ".yaml changed here, not in the config repo yet — the workstation's next nimble update " + name + " takes it"
}

// WarnUnpublished warns when the config repo (or the personal layer) has changes that aren't
// published yet. main calls it after every command; on a host it warns while its host file
// has changes the workstation hasn't read back yet.
func WarnUnpublished() {
	if nimble.IsSnapshot() {
		if changed, _ := nimble.HostChanges(); changed {
			nimble.Warn(hostPending(nimble.SnapshotHost()) + " (nimble changes shows it)")
		}
		return
	}
	if !nimble.IsCheckout() {
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
