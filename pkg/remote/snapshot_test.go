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
	w("nimble.yaml", "format: 2\ndefaultTemplate: dev\ndomains:\n  home:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n")
	w("hosts/swan.yaml", "address: swan.example.com\nuser: debian\n")
	w("hosts/vm.yaml", "address: vm.example.com\nuser: hannes\ndomain: school\ntools: [starship]\n")
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
	for _, want := range []string{"hosts/vm.yaml", "toolchain/pins.yaml", "shell/common.sh",
		"keys/enclave/mac-htl.pub", "keys/yubikey/safe1.pub", "personal/git/config", "personal/nimble.yaml", "nimble.yaml", ".snapshot",
		".sent/hosts/vm.yaml"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	// the host file as sent: the base for merging the host's own changes back
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(dir, rel)); return string(b) }
	if a, b := read("hosts/vm.yaml"), read(".sent/hosts/vm.yaml"); a == "" || a != b {
		t.Errorf("sent copy %q differs from the host file %q", b, a)
	}
	// nothing about the workstation: no machine name
	if m := read(".snapshot"); strings.Contains(m, "from") || !strings.HasPrefix(m, "host: vm\n") {
		t.Errorf("marker %q", m)
	}
	for _, not := range []string{"hosts/swan.yaml", "templates/", "keys/enclave/mac-ssh.pub", "personal/macos/Brewfile", "cli/main.go"} {
		if strings.Contains(joined, not) {
			t.Errorf("%s must not be in the snapshot", not)
		}
	}
	// the snapshot is a valid config for the host
	t.Setenv("NIMBLE_HOME", dir)
	if errs := config.Validate([]string{"starship", "go"}); len(errs) > 0 {
		t.Fatalf("snapshot invalid: %v", errs)
	}
	if c, err := config.Load(); err != nil || len(c.Domains) != 1 || c.Domains[0].Name != "school" || c.Format != 2 || c.DefaultTemplate != "" || len(c.Links) != 1 {
		t.Fatalf("snapshot config %+v %v", c, err)
	}
	if strings.Contains(joined, "ssh/") {
		t.Errorf("no reach, but an ssh config: %s", joined)
	}
}

// A host with reach gets its own ssh entries — only those, with no keys and nothing of the
// workstation's ssh config.
func TestSnapshotSSHConfig(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("NIMBLE_HOME", repo)
	w := func(rel, body string) {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("nimble.yaml", "format: 2\ndomains:\n  home:\n    label: ssh\nlinks: []\n")
	w("hosts/swan.yaml", "address: swan.example.com\nreach: [grizzly]\n")
	w("hosts/grizzly.yaml", "address: 10.0.20.5\njump: swan\n")
	w("hosts/vm.yaml", "address: vm.example.com\nmode: ssh\n")
	w("toolchain/pins.yaml", "go: 1.27.2\n")
	w("shell/common.sh", "# shared\n")
	w("ssh/defaults", "Host *\n  SecurityKeyProvider /usr/lib/ssh-keychain.dylib\n")
	w("keys/enclave/mac-ssh.pub", "sk-ecdsa-sha2-nistp256@openssh.com AAAA mac-ssh\n")

	dir, err := Snapshot("swan")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	b, err := os.ReadFile(filepath.Join(dir, "ssh/config"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "Host grizzly\n  HostName 10.0.20.5\n  User debian\n  ForwardAgent yes\n") {
		t.Errorf("ssh/config:\n%s", got)
	}
	for _, not := range []string{"ProxyJump", "IdentityFile", "Host vm", "Host swan", "keychain", "Host *"} {
		if strings.Contains(got, not) {
			t.Errorf("%s must not be in swan's ssh config:\n%s", not, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh/defaults")); err == nil {
		t.Error("the workstation's ssh/defaults must not be in the snapshot")
	}
	// grizzly reaches nothing: no ssh config
	dir2, err := Snapshot("grizzly")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)
	if _, err := os.Stat(filepath.Join(dir2, "ssh")); err == nil {
		t.Error("grizzly reaches no host, but got an ssh config")
	}
	// the snapshot is valid on the host, though grizzly's file isn't in it
	t.Setenv("NIMBLE_HOME", dir)
	if errs := config.Validate(nil); len(errs) > 0 {
		t.Fatalf("snapshot invalid: %v", errs)
	}
}

// The read-back script answers with the sent copy, the host file and the change log — or
// nothing on a host without a sent copy.
func TestFetchScript(t *testing.T) {
	home := t.TempDir()
	fetch := func() ([]byte, []byte, []string, error) {
		c := exec.Command("sh", "-c", fetchScript("swan"))
		c.Env = append(os.Environ(), "HOME="+home)
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		return parseFetched("swan", string(out))
	}
	if sent, _, _, err := fetch(); sent != nil || err != nil { // not set up yet
		t.Fatalf("no ~/.nimble: %q %v", sent, err)
	}
	w := func(rel, body string) {
		p := filepath.Join(home, ".nimble", rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("hosts/swan.yaml", "address: swan\ntools: [go, docker]\n")
	if sent, _, _, _ := fetch(); sent != nil {
		t.Fatal("answered without a sent copy")
	}
	w(".sent/hosts/swan.yaml", "address: swan\ntools: [go]\n")
	sent, now, log, err := fetch()
	if err != nil || string(sent) != "address: swan\ntools: [go]\n" || string(now) != "address: swan\ntools: [go, docker]\n" || len(log) != 0 {
		t.Fatalf("got %q %q %q %v", sent, now, log, err)
	}
	w(".changed", "hosts: swan add tools docker\nhosts: swan shell=bash\n")
	if _, _, log, _ := fetch(); len(log) != 2 || log[1] != "hosts: swan shell=bash" {
		t.Fatalf("log %q", log)
	}
	if _, _, _, err := FetchChanges("swan; rm -rf ~"); err == nil {
		t.Fatal("took a name that isn't one")
	}
}

// The unpack script must replace the old snapshot only with a complete new one, and keep
// cache/, backup/ and local.sh.
func TestUnpack(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".nimble")
	for rel, body := range map[string]string{"nimble.yaml": "old\n", "hosts/a.yaml": "old\n", "cache/host": "a\n", "local.sh": "mine\n", ".snapshot": "old\n",
		".sent/hosts/a.yaml": "old\n", ".changed": "hosts: a shell=bash\n"} {
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
	if read("hosts/a.yaml") != "" || read(".sent/hosts/a.yaml") != "" || read(".changed") != "" {
		t.Fatal("a file of the old snapshot stayed (the host's changes are read back before)")
	}
	if read("cache/host") != "a\n" || read("local.sh") != "mine\n" {
		t.Fatal("cache/ or local.sh didn't stay")
	}
	if _, err := os.Stat(filepath.Join(home, ".nimble.new")); err == nil {
		t.Fatal(".nimble.new stayed")
	}
}
