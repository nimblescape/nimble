package push

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/cmd/repo"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
)

// Every machine pushes to the config repo's main branch. Pull brings dir up to date with it
// the way nimble push and nimble update do: git pull --rebase, uncommitted changes stashed and
// put back (--autostash). Changes to different files, or different lines of one file, merge
// by themselves; only the same lines changed here and on another machine conflict. Then the
// repo is never left half-rebased:
//
//   - the rebase is aborted — everything is as it was — and nimble names the files and the
//     commits on both sides;
//   - on a terminal it resolves them with you: the files open in your editor, and once they
//     have no conflict markers left and the YAML is valid, the rebase goes on;
//   - uncommitted changes that clash with what was pulled (the pull itself goes through) are
//     resolved with you the same way; otherwise the files are exactly what was pulled, and
//     the changes wait in git's stash.
func Pull(dir string) error {
	old := strings.TrimSpace(gitOut(dir, "rev-parse", "HEAD"))
	stashes := len(gitLines(dir, "stash", "list"))
	err := nimble.Cmd{Name: "git", Args: []string{"-C", dir, "-c", "advice.mergeConflict=false", "pull", "--rebase", "--autostash", "--quiet"}}.Do()
	if err != nil {
		if !rebasing(dir) {
			return fmt.Errorf("pull failed in %s (see above)", nimble.Tilde(dir))
		}
		files := conflicted(dir)
		if err := gitRun(dir, nil, "rebase", "--abort"); err != nil {
			return err
		}
		describe(dir, old, files)
		if !nimble.Interactive() {
			return fmt.Errorf("%s: nothing pulled — on a terminal, nimble push or nimble update resolves %s with you", nimble.Tilde(dir), strings.Join(files, ", "))
		}
		const now = "resolve them now in your editor"
		choice, err := nimble.Select("The same lines were changed here and published from elsewhere:", []string{now, "stop here — nothing changes"}, now)
		if err != nil {
			return err
		}
		if choice != now {
			return stopped(dir)
		}
		if err := rebaseWithYou(dir); err != nil {
			return err
		}
	}
	if len(gitLines(dir, "stash", "list")) > stashes { // autostash couldn't put everything back
		return stashClash(dir)
	}
	return nil
}

// describe says what conflicts: the files, and the commits on both sides that touch them.
func describe(dir, old string, files []string) {
	name := nimble.Tilde(dir)
	nimble.Warn(name + ": " + strings.Join(files, ", ") + " changed here and on another machine, in the same lines")
	show := func(label, revs string) {
		if lines := gitLines(dir, append([]string{"log", "--oneline", revs, "--"}, files...)...); len(lines) > 0 {
			fmt.Println("  " + label)
			for _, l := range lines {
				fmt.Println("    " + l)
			}
		}
	}
	show("here, not published yet:", "@{u}.."+old)
	show("published from elsewhere:", old+"..@{u}")
	nimble.Say(name + " is as it was — nothing pulled yet")
}

// rebaseWithYou rebases onto the upstream again and resolves each conflict in the editor.
// Stopped or failed, the rebase is aborted: everything goes back to how it was.
func rebaseWithYou(dir string) (err error) {
	defer func() {
		if err != nil && rebasing(dir) {
			_ = gitRun(dir, nil, "rebase", "--abort")
		}
	}()
	if gitRun(dir, nil, "-c", "rebase.autoStash=true", "-c", "advice.mergeConflict=false", "rebase", "--quiet", "@{u}") == nil {
		return nil // no conflict this time (the upstream moved again)
	}
	for i := 0; rebasing(dir); i++ {
		if i > 100 {
			return errors.New("the rebase doesn't finish — sort it out in " + nimble.Tilde(dir) + " (git status)")
		}
		if files := conflicted(dir); len(files) > 0 {
			if err := Resolve(dir, files); err != nil {
				return err
			}
			if err := gitRun(dir, nil, append([]string{"add", "-A", "--"}, files...)...); err != nil {
				return err
			}
		}
		if gitRun(dir, []string{"GIT_EDITOR=true"}, "rebase", "--continue") != nil && rebasing(dir) && len(conflicted(dir)) == 0 {
			_ = gitRun(dir, nil, "rebase", "--skip") // resolved to what the upstream has: the commit is empty
		}
	}
	nimble.Say(nimble.Tilde(dir) + ": resolved — your commits are on top of the published ones")
	return nil
}

// stashClash: uncommitted changes clash with what was pulled. git keeps all of them in its
// stash, but also leaves the clash in the files, conflict markers and all (and says it
// succeeded) — nothing may read the config like that. Resolved, the changes are uncommitted
// again as before the pull; otherwise the files go back to exactly what was pulled, and the
// changes wait in the stash.
func stashClash(dir string) error {
	name := nimble.Tilde(dir)
	clash := conflicted(dir)
	nimble.Warn(name + ": your uncommitted changes to " + strings.Join(clash, ", ") + " clash with what was pulled")
	keep := func(err error) error {
		if e := gitRun(dir, nil, "reset", "--hard", "-q"); e != nil { // what was pulled; the stash has every change
			return e
		}
		nimble.Say("your uncommitted changes wait in git's stash: git -C " + name + " stash pop puts them back")
		if errors.Is(err, nimble.ErrCancelled) {
			return nil // stopped: the pulled config still applies
		}
		return err
	}
	if len(clash) == 0 || !nimble.Interactive() {
		return keep(nil)
	}
	const now = "resolve the clash now in your editor"
	choice, err := nimble.Select("Your uncommitted changes:", []string{now, "keep them in the stash for now"}, now)
	if err != nil || choice != now {
		return keep(err)
	}
	if err := Resolve(dir, clash); err != nil {
		return keep(err)
	}
	if err := gitRun(dir, nil, "reset", "-q"); err != nil { // resolved: uncommitted again, as before the pull
		return err
	}
	return gitRun(dir, nil, "stash", "drop", "-q")
}

// editFiles opens files in the editor and returns once they are closed (tests replace it).
var editFiles = repo.Editor

// Resolve has the user resolve files (relative to dir) in the editor until they have no
// conflict markers and the config is valid again — or until the user stops
// (nimble.ErrCancelled). Pull uses it, and so does reading back a host's changes.
func Resolve(dir string, files []string) error {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = filepath.Join(dir, f)
	}
	for {
		nimble.Step("resolve " + strings.Join(files, ", ") + " in your editor: keep what should stay, remove the <<<<<<< ======= >>>>>>> lines, save and close")
		if err := editFiles(paths...); err != nil {
			return err
		}
		problems := unresolved(dir, files)
		if len(problems) == 0 {
			return nil
		}
		for _, p := range problems {
			nimble.Warn(p)
		}
		if !nimble.Interactive() {
			return errors.New("not resolved: " + strings.Join(problems, "; "))
		}
		const again = "open them again"
		choice, err := nimble.Select("Not resolved yet:", []string{again, "stop — everything goes back to how it was"}, again)
		if err != nil {
			return err
		}
		if choice != again {
			return nimble.ErrCancelled
		}
	}
}

// unresolved lists what still keeps the files from counting as resolved.
func unresolved(dir string, files []string) []string {
	var problems []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err == nil && hasMarkers(string(b)) { // a deleted file is a resolution too
			problems = append(problems, f+": conflict markers left")
		}
	}
	if len(problems) == 0 && dir == nimble.Repo() {
		for _, e := range config.Validate(setup.KnownTools()) {
			problems = append(problems, e.Error())
		}
	}
	return problems
}

func hasMarkers(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "<<<<<<< ") || strings.HasPrefix(l, ">>>>>>> ") {
			return true
		}
	}
	return false
}

func stopped(dir string) error {
	nimble.Say("stopped — " + nimble.Tilde(dir) + " is as it was; nimble push or nimble update again resolves it")
	return nimble.ErrCancelled
}

// rebasing reports whether a rebase is in progress in dir.
func rebasing(dir string) bool {
	for _, p := range []string{"rebase-merge", "rebase-apply"} {
		path := strings.TrimSpace(gitOut(dir, "rev-parse", "--git-path", p))
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if path != "" && nimble.Exists(path) {
			return true
		}
	}
	return false
}

// conflicted lists the files with unresolved conflicts.
func conflicted(dir string) []string {
	return gitLines(dir, "diff", "--name-only", "--diff-filter=U")
}

// gitRun runs a git command in dir; its output beyond errors stays quiet.
func gitRun(dir string, env []string, args ...string) error {
	return nimble.Cmd{Name: "git", Args: append([]string{"-C", dir}, args...), Env: env, Quiet: true}.Do()
}

// gitOut is git's output as it is ("" on failure).
func gitOut(dir string, args ...string) string {
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out)
}
