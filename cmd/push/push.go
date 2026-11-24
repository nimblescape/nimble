package push

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/nimblescape/nimble/seal"
	"github.com/spf13/cobra"
)

func NewCmdPush() *cobra.Command {
	var message string
	var diff bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish the config changes: pull, show what goes out and ask, push",
		Long: heredoc.Doc(`
			Commands that change the inventories (host, template, tool, service, key, domain)
			only commit, and nimble tool use and tool update only edit pins.yaml; nothing leaves
			this machine until nimble push. It checks every YAML file, commits the hand edits,
			pulls what was published meanwhile (your commits go on top; a conflict is resolved
			with you, or nothing changes), shows what goes out (--diff: the full diff too, in
			git's pager) and on a terminal asks — declined, the hand edits are uncommitted
			again — then asks for their commit message (or -m) and pushes; the personal layer
			too when it is a repo of its own. Machines pick the changes up with nimble update.
		`),
		Example: heredoc.Doc(`
			nimble host set devbox tools=docker   # commits
			nimble template set dev shell=zsh     # commits
			nimble push                           # publishes both
			nimble push --diff                    # looking at the full diff first
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !nimble.IsCheckout() {
				return errors.New("no config repo at " + nimble.Tilde(nimble.Repo()) + " — run nimble init")
			}
			if errs := config.Validate(setup.KnownTools()); len(errs) > 0 {
				for _, e := range errs {
					nimble.Warn(e.Error())
				}
				return errors.New("fix these first (nimble host edit / template edit) — nothing was pushed")
			}
			if err := checkTokens(); err != nil {
				return err
			}
			for _, d := range dirs() {
				if err := publish(d, message, diff); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message for hand edits")
	cmd.Flags().BoolVarP(&diff, "diff", "d", false, "show the full diff of what goes out before asking (in git's pager)")
	return cmd
}

// Unpublished counts the commits in dir that its upstream doesn't have yet.
func Unpublished(dir string) int {
	if nimble.Output("git", "-C", dir, "rev-parse", "--abbrev-ref", "@{u}") == "" {
		return 0
	}
	return len(nimble.Lines(nimble.Output("git", "-C", dir, "log", "--oneline", "@{u}..HEAD")))
}

// dirs are the repos push publishes: the config repo, and the personal layer when it is a
// git repo of its own.
func dirs() []string {
	out := []string{nimble.Repo()}
	if p := nimble.RepoFile("personal"); nimble.Exists(p + "/.git") {
		out = append(out, p)
	}
	return out
}

// pending is what the next nimble push publishes from one repo.
type pending struct {
	dir, name, up string   // up: the upstream branch ("" = none: a host's snapshot)
	ahead         []string // commits the upstream doesn't have yet, oldest first
	changed       []string // hand edits, as git status --porcelain lists them
}

func look(dir string) pending {
	p := pending{dir: dir, name: nimble.Tilde(dir), up: nimble.Output("git", "-C", dir, "rev-parse", "--abbrev-ref", "@{u}")}
	if p.up != "" {
		p.ahead = gitLines(dir, "log", "--oneline", "--reverse", "@{u}..HEAD")
	}
	p.changed = gitLines(dir, "status", "--porcelain")
	return p
}

func (p pending) empty() bool { return len(p.ahead) == 0 && len(p.changed) == 0 }

// show prints what goes out: the commits, the hand edits, and the changed files.
func (p pending) show() {
	if p.up != "" {
		fmt.Println(nimble.Bold(p.name + " — to publish to " + p.up + ":"))
	} else {
		fmt.Println(nimble.Bold(p.name + " — changed here (no upstream to publish to):"))
	}
	if len(p.ahead) > 0 {
		fmt.Println("  commits:")
		for _, l := range p.ahead {
			fmt.Println("    " + l)
		}
	}
	if len(p.changed) > 0 {
		fmt.Println("  not committed yet (nimble push commits them with your message):")
		for _, l := range p.changed {
			fmt.Println("    " + l)
		}
	}
	base := "@{u}"
	if p.up == "" {
		base = "HEAD"
	}
	if stat := gitLines(p.dir, "-c", "color.ui=never", "diff", "--stat", base); len(stat) > 0 {
		fmt.Println("  changes:")
		for _, l := range stat {
			fmt.Println("   " + l) // git's own leading space makes four
		}
	}
}

// gitLines runs git in dir and keeps each line as git wrote it: status and diffstat lines
// start with a space (nimble.Output trims the whole output).
func gitLines(dir string, args ...string) []string {
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return nimble.Lines(string(out))
}

// publish: commit the hand edits, pull (always — conflicts are resolved before anything goes
// out), show what goes out and ask, push. The hand edits are committed before the pull so a
// clash with what was pulled is an ordinary conflict (aborted, named, resolved with you) and
// nothing goes through git's stash; declined, cancelled or not pulled, that commit is undone
// and the edits are uncommitted again. Its message is asked for once you've said yes. A push
// rejected because another machine pushed in the meantime pulls and pushes again. diff adds
// the full diff of what goes out, before the question.
func publish(dir, message string, diff bool) error {
	p := look(dir)
	def := defaultMessage(p.changed)
	if message == "" {
		message = def
	}
	if p.up == "" {
		if len(p.changed) > 0 {
			p.show()
			if err := commit(dir, nimble.Ask("Commit message?", message)); err != nil {
				return err
			}
		}
		if nimble.IsDebian() && !setup.Darwin() {
			return errors.New(p.name + " doesn't publish itself here — from the workstation, nimble update <this host> picks the commits up, then nimble push there")
		}
		return errors.New(p.name + " has no upstream branch to push to (git -C " + p.name + " push -u origin main)")
	}

	edits := len(p.changed) > 0
	if edits {
		if err := commit(dir, message); err != nil {
			return err
		}
	}
	// ours: the hand edits' commit is still on top and not published (the rebase skips it
	// when the edits turn out to be what was published already)
	ours := func() bool {
		return edits && strings.TrimSpace(gitOut(dir, "log", "-1", "--format=%s")) == message &&
			exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "HEAD", "@{u}").Run() != nil
	}
	undo := func(why string) {
		if ours() && gitRun(dir, nil, "reset", "-q", "HEAD~1") == nil {
			nimble.Say(why + " — your edits are uncommitted again")
		} else {
			nimble.Say(why)
		}
	}

	nimble.Step("pull " + p.up)
	if err := Pull(dir); err != nil {
		undo("nothing published")
		return err
	}
	p = look(dir) // what goes out on top of what was just pulled
	if p.empty() {
		nimble.Say(p.name + ": nothing to publish")
		return nil
	}
	p.show() // nimble changes shows the same
	if diff {
		if err := nimble.Run("git", "-C", dir, "diff", "@{u}"); err != nil { // in git's pager
			undo("nothing published")
			return err
		}
	}
	if nimble.Interactive() {
		yes, err := nimble.ConfirmOrCancel("Publish these?", true)
		if err != nil || !yes {
			undo("nothing published")
			return err
		}
		if ours() && message == def { // no -m: now the message for the edits
			m, err := nimble.Input("Commit message for your edits?", def, nil)
			if err != nil {
				undo("nothing published")
				return err
			}
			if m != "" && m != def {
				if err := gitRun(dir, nil, "commit", "--amend", "-q", "-m", m); err != nil {
					return err
				}
				nimble.Say("committed: " + m)
			}
		}
	}
	for try := 1; ; try++ {
		ahead := gitLines(dir, "log", "--oneline", "@{u}..HEAD")
		if err := (nimble.Cmd{Name: "git", Args: []string{"-C", dir, "push", "--quiet"}}).Do(); err == nil {
			nimble.Say(fmt.Sprintf("published %d commit(s) to %s", len(ahead), p.up))
			return nil
		} else if try == 3 {
			return errors.New("push failed — " + p.name + " keeps the commits; try nimble push again")
		}
		nimble.Step("rejected — another machine pushed meanwhile: pull " + p.up + " again") // or the network
		if err := Pull(dir); err != nil {
			return err
		}
	}
}

// defaultMessage names the hand edits (changed: git status lines): "edit hosts/swan.yaml".
func defaultMessage(changed []string) string {
	var files []string
	for _, l := range changed {
		if len(l) > 3 {
			files = append(files, strings.TrimSpace(l[3:]))
		}
	}
	if len(files) > 3 {
		return "edit " + strings.Join(files[:3], ", ") + fmt.Sprintf(" and %d more", len(files)-3)
	}
	return "edit " + strings.Join(files, ", ")
}

// commit commits all hand edits with message.
func commit(dir, message string) error {
	if err := (nimble.Cmd{Name: "git", Args: []string{"-C", dir, "add", "--all"}}).Do(); err != nil {
		return err
	}
	if err := (nimble.Cmd{Name: "git", Quiet: true, Args: []string{"-C", dir, "commit", "-m", message}}).Do(); err != nil {
		return err
	}
	nimble.Say("committed: " + message)
	return nil
}

// checkTokens refuses to publish tokens/ with a token in clear text (nimble never shares
// one) or a file that is no readable sealed token.
func checkTokens() error {
	ts, err := seal.ListIn(seal.Dir())
	bad := err != nil
	if err != nil {
		nimble.Warn(err.Error())
	}
	for _, t := range ts {
		if !t.Sealed() {
			bad = true
			nimble.Warn(nimble.Tilde(t.Path) + " is a token in clear text: nimble never shares one — seal it (nimble token seal " + t.Name + ") or move it to " + nimble.Tilde(seal.LocalDir()))
		}
	}
	if bad {
		return errors.New("fix the tokens first — nothing was pushed")
	}
	return nil
}
