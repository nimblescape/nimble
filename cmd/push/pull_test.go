package push

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoMachines is a config repo published to origin and cloned on two machines, here and
// there, both at the commit with notes.txt = "base".
func twoMachines(t *testing.T) (here, there string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tmp, "gitconfig"))
	t.Setenv("NIMBLE_HOME", filepath.Join(tmp, "elsewhere")) // not a config repo: no YAML check
	os.WriteFile(filepath.Join(tmp, "gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	origin := filepath.Join(tmp, "origin.git")
	here, there = filepath.Join(tmp, "here"), filepath.Join(tmp, "there")
	git(t, tmp, "init", "--bare", "-q", origin)
	git(t, tmp, "clone", "-q", origin, here)
	write(t, here, "notes.txt", "base\n")
	git(t, here, "add", ".")
	git(t, here, "commit", "-qm", "init")
	git(t, here, "push", "-q", "-u", "origin", "main")
	git(t, tmp, "clone", "-q", origin, there)
	return here, there
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// publishFrom commits notes.txt = body on a machine and pushes it.
func publishFrom(t *testing.T, dir, body string) {
	t.Helper()
	write(t, dir, "notes.txt", body)
	git(t, dir, "commit", "-qam", "notes from "+filepath.Base(dir))
	git(t, dir, "push", "-q")
}

func TestPullMergesDifferentFiles(t *testing.T) {
	here, there := twoMachines(t)
	write(t, there, "other.txt", "x\n")
	git(t, there, "add", ".")
	git(t, there, "commit", "-qm", "other")
	git(t, there, "push", "-q")
	write(t, here, "notes.txt", "mine\n")
	git(t, here, "commit", "-qam", "mine")

	if err := Pull(here); err != nil {
		t.Fatal(err)
	}
	if read(t, here, "notes.txt") != "mine\n" || read(t, here, "other.txt") != "x\n" {
		t.Error("not both changes")
	}
	if log := git(t, here, "log", "--format=%s"); log != "mine\nother\ninit" {
		t.Errorf("log:\n%s", log)
	}
}

func TestPullConflictLeavesTheRepoAsItWas(t *testing.T) {
	here, there := twoMachines(t)
	publishFrom(t, there, "from there\n")
	write(t, here, "notes.txt", "from here\n")
	git(t, here, "commit", "-qam", "mine")
	before := git(t, here, "rev-parse", "HEAD")

	err := Pull(here) // no terminal: it can't resolve with you
	if err == nil || !strings.Contains(err.Error(), "notes.txt") || !strings.Contains(err.Error(), "nothing pulled") {
		t.Fatalf("err %v", err)
	}
	if rebasing(here) || git(t, here, "rev-parse", "HEAD") != before || read(t, here, "notes.txt") != "from here\n" {
		t.Error("the repo isn't as it was")
	}

	// resolved in the editor: the rebase goes on, my commit on top of the published one
	editFiles = func(paths ...string) error {
		if len(paths) != 1 || filepath.Base(paths[0]) != "notes.txt" || !hasMarkers(read(t, here, "notes.txt")) {
			t.Errorf("editor got %v with %q", paths, read(t, here, "notes.txt"))
		}
		write(t, here, "notes.txt", "from here and there\n")
		return nil
	}
	t.Cleanup(func() { editFiles = nil })
	if err := rebaseWithYou(here); err != nil {
		t.Fatal(err)
	}
	if rebasing(here) || read(t, here, "notes.txt") != "from here and there\n" {
		t.Error("not resolved")
	}
	if log := git(t, here, "log", "--format=%s"); log != "mine\nnotes from there\ninit" {
		t.Errorf("log:\n%s", log)
	}
}

func TestPullEditorLeavesMarkers(t *testing.T) {
	here, there := twoMachines(t)
	publishFrom(t, there, "from there\n")
	write(t, here, "notes.txt", "from here\n")
	git(t, here, "commit", "-qam", "mine")
	before := git(t, here, "rev-parse", "HEAD")
	_ = Pull(here)

	editFiles = func(...string) error { return nil } // closed without resolving
	t.Cleanup(func() { editFiles = nil })
	if err := rebaseWithYou(here); err == nil || !strings.Contains(err.Error(), "conflict markers") {
		t.Fatalf("err %v", err)
	}
	if rebasing(here) || git(t, here, "rev-parse", "HEAD") != before || read(t, here, "notes.txt") != "from here\n" {
		t.Error("not back to how it was")
	}
}

func TestPullUncommittedClash(t *testing.T) {
	here, there := twoMachines(t)
	publishFrom(t, there, "from there\n")
	write(t, here, "notes.txt", "my hand edit\n") // not committed

	if err := Pull(here); err != nil { // the pull goes through; the edit waits in the stash
		t.Fatal(err)
	}
	if read(t, here, "notes.txt") != "from there\n" || git(t, here, "rev-parse", "HEAD") != git(t, here, "rev-parse", "@{u}") {
		t.Errorf("not exactly what was pulled: %q", read(t, here, "notes.txt"))
	}
	if st := git(t, here, "status", "--porcelain"); st != "" {
		t.Errorf("left in the files: %q", st)
	}
	if !strings.Contains(git(t, here, "stash", "show", "-p", "stash@{0}"), "+my hand edit") {
		t.Error("the hand edit isn't in the stash")
	}
}

// push commits hand edits before it pulls: a clash is an ordinary conflict, and the commit is
// undone again — no stash involved.
func TestPublishEditsClash(t *testing.T) {
	here, there := twoMachines(t)
	publishFrom(t, there, "from there\n")
	write(t, here, "notes.txt", "my hand edit\n") // not committed
	before := git(t, here, "rev-parse", "HEAD")

	if err := publish(here, "", false); err == nil || !strings.Contains(err.Error(), "nothing pulled") {
		t.Fatalf("err %v", err)
	}
	if git(t, here, "rev-parse", "HEAD") != before || read(t, here, "notes.txt") != "my hand edit\n" {
		t.Error("not as it was")
	}
	if st := git(t, here, "status", "--porcelain"); st != "M notes.txt" { // git trims the leading space
		t.Errorf("status %q: the edit isn't uncommitted again", st)
	}
	if git(t, here, "stash", "list") != "" {
		t.Error("something went into the stash")
	}
}

// an edit that is what was published already: its commit vanishes in the rebase, and
// nothing that isn't ours is undone
func TestPublishEditsAlreadyPublished(t *testing.T) {
	here, there := twoMachines(t)
	publishFrom(t, there, "the same\n")
	write(t, here, "notes.txt", "the same\n")

	if err := publish(here, "", false); err != nil {
		t.Fatal(err)
	}
	if git(t, here, "rev-parse", "HEAD") != git(t, here, "rev-parse", "@{u}") || git(t, here, "status", "--porcelain") != "" {
		t.Error("not exactly what was published")
	}
	if log := git(t, here, "log", "--format=%s"); log != "notes from there\ninit" {
		t.Errorf("log:\n%s", log)
	}
}

// push --diff shows the full diff of what goes out, then publishes as usual
func TestPublishDiff(t *testing.T) {
	here, there := twoMachines(t)
	write(t, here, "notes.txt", "my hand edit\n")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	err = publish(here, "", true)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "-base\n+my hand edit") {
		t.Errorf("no diff in:\n%s", out)
	}
	git(t, there, "pull", "-q")
	if read(t, there, "notes.txt") != "my hand edit\n" {
		t.Error("not published")
	}
}

func TestHasMarkers(t *testing.T) {
	if !hasMarkers("a\n<<<<<<< HEAD\nb\n=======\nc\n>>>>>>> x\n") || hasMarkers("title\n=======\ntext\n") {
		t.Error("markers")
	}
}
