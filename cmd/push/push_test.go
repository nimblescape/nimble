package push

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/nimble"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPublish(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tmp, "gitconfig"))
	os.WriteFile(filepath.Join(tmp, "gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	origin, repo := filepath.Join(tmp, "origin.git"), filepath.Join(tmp, "repo")
	git(t, tmp, "init", "--bare", "-q", origin)
	git(t, tmp, "clone", "-q", origin, repo)
	os.WriteFile(filepath.Join(repo, "nimble.yaml"), []byte("defaultTemplate: dev\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "init")
	git(t, repo, "push", "-q", "-u", "origin", "main")
	t.Setenv("NIMBLE_HOME", repo)

	// a command's commit, then a hand edit
	os.MkdirAll(filepath.Join(repo, "hosts"), 0o755)
	os.WriteFile(filepath.Join(repo, "hosts", "swan.yaml"), []byte("address: swan\n"), 0o644)
	if err := nimble.Record("hosts: add swan", "hosts"); err != nil {
		t.Fatal(err)
	}
	if git(t, origin, "log", "--oneline", "main") != git(t, repo, "log", "--oneline", "origin/main") {
		t.Fatal("Record pushed")
	}
	os.WriteFile(filepath.Join(repo, "nimble.yaml"), []byte("defaultTemplate: dev # edited\n"), 0o644)
	if Unpublished(repo) != 1 {
		t.Fatalf("unpublished %d", Unpublished(repo))
	}
	p := look(repo)
	if len(p.ahead) != 1 || len(p.changed) != 1 || p.up != "origin/main" {
		t.Fatalf("pending %+v", p)
	}
	if w := unpublished([]pending{p}); !strings.HasPrefix(w, "1 commit and 1 uncommitted change not published yet") || !strings.Contains(w, "have them") {
		t.Errorf("warning %q", w)
	}
	if err := publish(repo, "edit nimble.yaml", false); err != nil {
		t.Fatal(err)
	}
	log := git(t, origin, "log", "--format=%s", "main")
	if log != "edit nimble.yaml\nhosts: add swan\ninit" {
		t.Fatalf("origin log:\n%s", log)
	}
	if Unpublished(repo) != 0 || git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("not clean after push")
	}
	if w := unpublished([]pending{look(repo)}); w != "" {
		t.Errorf("warning after the push: %q", w)
	}

	// an unstaged edit (" M nimble.yaml") without -m: the default message names the file whole
	os.WriteFile(filepath.Join(repo, "nimble.yaml"), []byte("defaultTemplate: dev # again\n"), 0o644)
	if err := publish(repo, "", false); err != nil {
		t.Fatal(err)
	}
	if got := git(t, origin, "log", "-1", "--format=%s", "main"); got != "edit nimble.yaml" {
		t.Fatalf("default message %q", got)
	}

	// nothing left: says so, publishes nothing
	if err := publish(repo, "", false); err != nil {
		t.Fatal(err)
	}
}
