package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// add of a server that is installed already installs nothing; it only records it when the
// host file didn't have it.
func TestInstalledAlready(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tmp, "gitconfig"))
	os.WriteFile(filepath.Join(tmp, "gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	repo := filepath.Join(tmp, "repo")
	for rel, body := range map[string]string{
		"nimble.yaml":        "defaultTemplate: dev\ndomains:\n  home:\n    label: ssh\nlinks: []\n",
		"templates/dev.yaml": "tools: []\n",
		"hosts/swan.yaml":    "address: swan\nuser: debian\nservices:\n  - service: claude\n    repo: owner/app\n    state: enabled\n",
	} {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	git(t, tmp, "init", "-q", repo)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "init")
	t.Setenv("NIMBLE_HOME", repo)

	installed := map[string]string{"owner/app": "enabled", "owner/other": "disabled"}
	probe = func(_ config.Host, r string) (string, bool) {
		if s, ok := installed[r]; ok {
			return s, s == "enabled"
		}
		return "not installed", false
	}
	t.Cleanup(func() { probe = nil })
	swan := func() config.Host { h, _ := config.FindHost("swan"); return h }
	head := git(t, repo, "rev-parse", "HEAD")

	// in the host file and running: nothing to do
	if done, err := installedAlready(swan(), "claude", "owner/app"); !done || err != nil {
		t.Fatalf("installed and recorded: %v %v", done, err)
	}
	if git(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("committed something")
	}
	// not installed: add goes on and installs it
	if done, err := installedAlready(swan(), "claude", "owner/new"); done || err != nil {
		t.Fatalf("not installed: %v %v", done, err)
	}
	// installed there, but not in the host file: recorded as it is, nothing installed
	if done, err := installedAlready(swan(), "claude", "owner/other"); !done || err != nil {
		t.Fatalf("installed, not recorded: %v %v", done, err)
	}
	var found bool
	for _, s := range swan().Services {
		found = found || (s.Repo == "owner/other" && s.State == "disabled")
	}
	if !found || len(swan().Services) != 2 {
		t.Fatalf("host file: %+v", swan().Services)
	}
	if s := git(t, repo, "log", "-1", "--format=%s"); s != "hosts: swan service add claude owner/other (installed already)" {
		t.Errorf("commit %q", s)
	}
}
