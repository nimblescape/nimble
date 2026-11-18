package remote

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestSnapshot(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("NIMBLE_HOME", repo)
	w := func(rel, body string) {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("nimble.yaml", "defaultTemplate: dev\ndomains:\n  home:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n")
	w("hosts/swan.yaml", "address: swan.example.com\nuser: debian\n")
	w("hosts/vm.yaml", "address: vm.example.com\nuser: hannes\ndomain: school\ntemplate: prompt\n")
	w("templates/dev.yaml", "tools: [go]\n")
	w("templates/prompt.yaml", "tools: [starship]\n")
	w("toolchain/pins.yaml", "go: 1.27.2\n")
	w("shell/common.sh", "# shared\n")
	w("keys/enclave/mac-ssh.pub", "sk-ecdsa-sha2-nistp256@openssh.com AAAA mac-ssh\n")
	w("keys/enclave/mac-htl.pub", "sk-ecdsa-sha2-nistp256@openssh.com BBBB mac-htl\n")
	w("keys/yubikey/safe1.pub", "sk-ssh-ed25519@openssh.com CCCC yubikey-safe1\n")
	w("personal/nimble.yaml", "links:\n  - from: git/config\n    to: .config/git/config\n  - from: macos/Brewfile\n    to: Brewfile\n    os: macos\n")
	w("personal/git/config", "[user]\n")
	w("personal/macos/Brewfile", "brew \"x\"\n")
	w("cli/main.go", "package main\n")

	dir, err := Snapshot("vm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var got []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, rel)
		}
		return nil
	})
	joined := strings.Join(got, " ")
	for _, want := range []string{"hosts/vm.yaml", "templates/prompt.yaml", "toolchain/pins.yaml", "shell/common.sh",
		"keys/enclave/mac-htl.pub", "keys/yubikey/safe1.pub", "personal/git/config", "personal/nimble.yaml", "nimble.yaml", ".snapshot"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	for _, not := range []string{"hosts/swan.yaml", "templates/dev.yaml", "keys/enclave/mac-ssh.pub", "personal/macos/Brewfile", "cli/main.go"} {
		if strings.Contains(joined, not) {
			t.Errorf("%s must not be in the snapshot", not)
		}
	}
	// the snapshot is a valid config for the host
	t.Setenv("NIMBLE_HOME", dir)
	if errs := config.Validate([]string{"starship", "go"}); len(errs) > 0 {
		t.Fatalf("snapshot invalid: %v", errs)
	}
	if c, err := config.Load(); err != nil || len(c.Domains) != 1 || c.Domains[0].Name != "school" || c.DefaultTemplate != "prompt" || len(c.Links) != 1 {
		t.Fatalf("snapshot config %+v %v", c, err)
	}
}

// The unpack script must replace the old snapshot only with a complete new one, and keep
// cache/, backup/ and local.sh.
func TestUnpack(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".nimble")
	for rel, body := range map[string]string{"nimble.yaml": "old\n", "hosts/a.yaml": "old\n", "cache/host": "a\n", "local.sh": "mine\n", ".snapshot": "old\n"} {
		p := filepath.Join(old, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	src := t.TempDir()
	for rel, body := range map[string]string{"nimble.yaml": "new\n", "hosts/b.yaml": "new\n", ".snapshot": "new\n"} {
		p := filepath.Join(src, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	tgz := filepath.Join(t.TempDir(), "s.tgz")
	if out, err := exec.Command("tar", "-C", src, "-czf", tgz, ".").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	run := func(stream []byte) error {
		c := exec.Command("sh", "-c", unpack)
		c.Env = append(os.Environ(), "HOME="+home)
		c.Stdin = bytes.NewReader(stream)
		return c.Run()
	}
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(old, rel)); return string(b) }
	data, _ := os.ReadFile(tgz)

	// a transfer that breaks off: nothing changes
	if err := run(data[:len(data)/2]); err == nil {
		t.Fatal("a truncated stream must fail")
	}
	if read("nimble.yaml") != "old\n" || read("hosts/a.yaml") != "old\n" {
		t.Fatal("a broken transfer changed the old snapshot")
	}
	// a complete one replaces it, and keeps what belongs to the host
	if err := run(data); err != nil {
		t.Fatal(err)
	}
	if read("nimble.yaml") != "new\n" || read(".snapshot") != "new\n" || read("hosts/b.yaml") != "new\n" {
		t.Fatal("the new snapshot isn't in place")
	}
	if read("hosts/a.yaml") != "" {
		t.Fatal("a file of the old snapshot stayed")
	}
	if read("cache/host") != "a\n" || read("local.sh") != "mine\n" {
		t.Fatal("cache/ or local.sh didn't stay")
	}
	if _, err := os.Stat(filepath.Join(home, ".nimble.new")); err == nil {
		t.Fatal(".nimble.new stayed")
	}
}
