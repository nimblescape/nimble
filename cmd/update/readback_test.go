package update

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

// scratch is a config repo with host swan, as it was sent to swan (committed).
func scratch(t *testing.T, sent string) string {
	tmp := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tmp, "gitconfig"))
	os.WriteFile(filepath.Join(tmp, "gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	repo := filepath.Join(tmp, "repo")
	for rel, body := range map[string]string{
		"nimble.yaml":         "defaultTemplate: dev\ndomains:\n  home:\n    label: ssh\nlinks: []\n",
		"templates/dev.yaml":  "tools: [go]\n",
		"toolchain/pins.yaml": "go: 1.27.2\n",
		"shell/common.sh":     "# shared\n",
		"hosts/swan.yaml":     sent,
	} {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	git(t, tmp, "init", "-q", repo)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "init")
	t.Setenv("NIMBLE_HOME", repo)
	return repo
}

func hostFile(t *testing.T, repo string) string {
	b, _ := os.ReadFile(filepath.Join(repo, "hosts", "swan.yaml"))
	return string(b)
}

const sent = "address: swan.example.com\nuser: debian\ntools: [go]\nservices: []\n"

func TestMergeBackHostOnly(t *testing.T) {
	repo := scratch(t, sent)
	now := "address: swan.example.com\nuser: debian\ntools: [go, docker]\nservices: []\n"
	if err := mergeBack("swan", []byte(sent), []byte(now), []string{"hosts: swan add tools docker"}); err != nil {
		t.Fatal(err)
	}
	if got := hostFile(t, repo); got != now {
		t.Errorf("host file %q", got)
	}
	if s := git(t, repo, "log", "-1", "--format=%s"); s != "hosts: swan add tools docker (on swan)" {
		t.Errorf("commit %q", s)
	}
	if git(t, repo, "status", "--porcelain") != "" {
		t.Error("left uncommitted changes")
	}
}

func TestMergeBackBothSides(t *testing.T) {
	repo := scratch(t, sent)
	// here: the workstation changed the address since; there: the host added a tool
	here := "address: swan.lan\nuser: debian\ntools: [go]\nservices: []\n"
	os.WriteFile(filepath.Join(repo, "hosts", "swan.yaml"), []byte(here), 0o644)
	git(t, repo, "commit", "-qam", "hosts: swan address=swan.lan")
	now := "address: swan.example.com\nuser: debian\ntools: [go, docker]\nservices: []\n"
	if err := mergeBack("swan", []byte(sent), []byte(now), nil); err != nil {
		t.Fatal(err)
	}
	if got, want := hostFile(t, repo), "address: swan.lan\nuser: debian\ntools: [go, docker]\nservices: []\n"; got != want {
		t.Errorf("merged %q, want %q", got, want)
	}
	if s := git(t, repo, "log", "-1", "--format=%s"); s != "hosts: swan changed on swan" {
		t.Errorf("commit %q", s)
	}
}

func TestMergeBackClash(t *testing.T) {
	repo := scratch(t, sent)
	here := "address: swan.example.com\nuser: debian\ntools: [go, rust]\nservices: []\n"
	os.WriteFile(filepath.Join(repo, "hosts", "swan.yaml"), []byte(here), 0o644)
	git(t, repo, "commit", "-qam", "hosts: swan add tools rust")
	now := "address: swan.example.com\nuser: debian\ntools: [go, docker]\nservices: []\n"
	log := []string{"hosts: swan add tools docker", "hosts: swan shell=bash"}

	// without a terminal: nothing changes here, and the error keeps the snapshot from going out
	interactive = func() bool { return false }
	t.Cleanup(func() { interactive, resolve = nimble.Interactive, nil })
	if err := mergeBack("swan", []byte(sent), []byte(now), log); err == nil || !strings.Contains(err.Error(), "swan keeps its change") {
		t.Fatalf("err %v", err)
	}
	if hostFile(t, repo) != here {
		t.Fatal("the file here changed")
	}

	// on a terminal: resolved in the editor, then committed with what was done there
	interactive = func() bool { return true }
	resolve = func(dir string, files []string) error {
		if len(files) != 1 || files[0] != "hosts/swan.yaml" || !strings.Contains(hostFile(t, repo), "<<<<<<< here") ||
			!strings.Contains(hostFile(t, repo), ">>>>>>> changed on swan") {
			t.Errorf("resolve got %v with %q", files, hostFile(t, repo))
		}
		return os.WriteFile(filepath.Join(dir, files[0]), []byte("address: swan.example.com\nuser: debian\ntools: [go, rust, docker]\nservices: []\n"), 0o644)
	}
	if err := mergeBack("swan", []byte(sent), []byte(now), log); err != nil {
		t.Fatal(err)
	}
	if msg := git(t, repo, "log", "-1", "--format=%B"); msg != "hosts: swan changed on swan (2 changes)\n\nhosts: swan add tools docker\nhosts: swan shell=bash" {
		t.Errorf("commit %q", msg)
	}

	// stopped in the editor: as it was here
	before := hostFile(t, repo)
	resolve = func(string, []string) error { return nimble.ErrCancelled }
	if err := mergeBack("swan", []byte(before), []byte(strings.Replace(before, "rust, ", "", 1)+"shell: zsh\n"), nil); err != nil {
		t.Fatalf("a clean merge must not ask: %v", err)
	}
	after := hostFile(t, repo)
	if err := mergeBack("swan", []byte(before), []byte(strings.Replace(before, "[go, rust, docker]", "[go]", 1)), nil); err != nimble.ErrCancelled {
		t.Fatalf("err %v", err)
	}
	if hostFile(t, repo) != after {
		t.Error("not as it was after stopping")
	}
}

// End to end, with an ssh that runs the remote script here against a host's home: the
// first snapshot, a change on the host and one here, then the next update merges both,
// commits, and sends a snapshot that starts afresh.
func TestSendConfig(t *testing.T) {
	repo := scratch(t, sent)
	host := t.TempDir()
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nfor a; do last=$a; done\nHOME=\"$HOST_HOME\" exec sh -c \"$last\"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOST_HOME", host)
	onHost := func(rel string) string { b, _ := os.ReadFile(filepath.Join(host, ".nimble", rel)); return string(b) }

	if err := SendConfig("swan"); err != nil { // a new host: nothing to read back
		t.Fatal(err)
	}
	if onHost("hosts/swan.yaml") != sent || onHost(".sent/hosts/swan.yaml") != sent {
		t.Fatalf("first snapshot: %q", onHost("hosts/swan.yaml"))
	}

	// changed on swan (tool select there), and here (the address)
	there := "address: swan.example.com\nuser: debian\ntools: [go, docker]\nservices: []\n"
	os.WriteFile(filepath.Join(host, ".nimble", "hosts", "swan.yaml"), []byte(there), 0o644)
	os.WriteFile(filepath.Join(host, ".nimble", nimble.ChangeLog), []byte("hosts: swan add tools docker\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "hosts", "swan.yaml"), []byte(strings.Replace(sent, "swan.example.com", "swan.lan", 1)), 0o644)
	git(t, repo, "commit", "-qam", "hosts: swan address=swan.lan")

	if err := SendConfig("swan"); err != nil {
		t.Fatal(err)
	}
	merged := "address: swan.lan\nuser: debian\ntools: [go, docker]\nservices: []\n"
	if hostFile(t, repo) != merged {
		t.Fatalf("merged here: %q", hostFile(t, repo))
	}
	if s := git(t, repo, "log", "-1", "--format=%s"); s != "hosts: swan add tools docker (on swan)" {
		t.Errorf("commit %q", s)
	}
	if onHost("hosts/swan.yaml") != merged || onHost(".sent/hosts/swan.yaml") != merged || onHost(nimble.ChangeLog) != "" {
		t.Errorf("new snapshot: %q, sent %q, log %q", onHost("hosts/swan.yaml"), onHost(".sent/hosts/swan.yaml"), onHost(nimble.ChangeLog))
	}

	// nothing changed since: nothing to commit
	head := git(t, repo, "rev-parse", "HEAD")
	if err := SendConfig("swan"); err != nil || git(t, repo, "rev-parse", "HEAD") != head {
		t.Fatalf("a third update changed something: %v", err)
	}
}

func TestMergeBackInvalid(t *testing.T) {
	repo := scratch(t, sent)
	now := "address: swan.example.com\nuser: debian\ntools: [go, nosuchtool]\nservices: []\n"
	interactive = func() bool { return false }
	t.Cleanup(func() { interactive = nimble.Interactive })
	if err := mergeBack("swan", []byte(sent), []byte(now), nil); err == nil {
		t.Fatal("an invalid host file was taken")
	}
	if hostFile(t, repo) != sent {
		t.Error("the file here changed")
	}
}
