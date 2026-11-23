package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

// swan and grizzly share a VLAN; grizzly is reached only through swan. edge is behind
// grizzly, vm is an ssh entry, mac the workstation.
func lab() []config.Host {
	return []config.Host{
		{Name: "edge", Address: "10.0.30.2", Jump: "grizzly"},
		{Name: "grizzly", Address: "10.0.20.5", Jump: "swan"},
		{Name: "mac", Mode: "workstation"},
		{Name: "swan", Address: "swan.example.com", Reach: []string{"grizzly"}},
		{Name: "vm", Address: "vm.example.com", User: "hannes", Mode: "ssh"},
	}
}

func find(hs []config.Host, name string) config.Host {
	for _, h := range hs {
		if h.Name == name {
			return h
		}
	}
	return config.Host{}
}

func TestHostSSHConfig(t *testing.T) {
	hs := lab()
	// swan: grizzly is reached through swan itself, so directly; no keys, the agent goes on
	got := HostSSHConfig(find(hs, "swan"), hs)
	want := "Host grizzly\n  HostName 10.0.20.5\n  User debian\n  ForwardAgent yes\n\n"
	if got != want {
		t.Errorf("swan:\n%s\nwant:\n%s", got, want)
	}

	// all: every host but itself and the workstation; a host on the way gets its ProxyJump
	edge := config.Host{Name: "edge2", Address: "e", Reach: []string{"all"}}
	got = HostSSHConfig(edge, append(hs, edge))
	for _, w := range []string{"Host edge\n", "Host grizzly\n  HostName 10.0.20.5\n  User debian\n  ProxyJump swan\n", "Host swan\n",
		"Host vm\n  HostName vm.example.com\n  User hannes\n\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("reach all: missing %q in\n%s", w, got)
		}
	}
	if strings.Contains(got, "Host mac") || strings.Contains(got, "Host edge2") || strings.Contains(got, "IdentityFile") || strings.Contains(got, "AddKeysToAgent") {
		t.Errorf("reach all:\n%s", got)
	}

	// reaching edge pulls in the hosts on its way: grizzly (through swan) and swan
	build := config.Host{Name: "build", Address: "b", Reach: []string{"edge"}}
	got = HostSSHConfig(build, append(hs, build))
	if !strings.Contains(got, "Host edge\n  HostName 10.0.30.2\n  User debian\n  ProxyJump grizzly\n") ||
		!strings.Contains(got, "Host grizzly\n") || !strings.Contains(got, "Host swan\n") || strings.Contains(got, "Host vm") {
		t.Errorf("reach edge:\n%s", got)
	}

	// grizzly reaching edge: edge is behind grizzly itself, so directly
	g := find(hs, "grizzly")
	g.Reach = []string{"edge"}
	if got := HostSSHConfig(g, hs); got != "Host edge\n  HostName 10.0.30.2\n  User debian\n  ForwardAgent yes\n\n" {
		t.Errorf("grizzly:\n%s", got)
	}

	// no reach: nothing
	if got := HostSSHConfig(find(hs, "edge"), hs); got != "" {
		t.Errorf("no reach: %q", got)
	}
}

func TestWorkstationSSHConfigJump(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIMBLE_HOME", dir)
	w := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("nimble.yaml", "format: 2\ndomains:\n  home:\n    label: ssh\nlinks: []\n")
	w("hosts/swan.yaml", "address: swan.example.com\nreach: [grizzly]\n")
	w("hosts/grizzly.yaml", "address: 10.0.20.5\njump: swan\n")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := SSHConfig(c, func(label string) string { return "/k/id_" + label })
	want := "Host grizzly\n  HostName 10.0.20.5\n  User debian\n  ProxyJump swan\n  IdentityFile /k/id_ssh\n  ForwardAgent yes\n  AddKeysToAgent yes\n\n" +
		"Host swan\n  HostName swan.example.com\n  User debian\n  IdentityFile /k/id_ssh\n  ForwardAgent yes\n  AddKeysToAgent yes\n\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestYubikeyConfig(t *testing.T) {
	got := yubikeyConfig("/usr/bin/ssh", []string{"/tmp/y/id_new", "/tmp/y/id_old"})
	for _, w := range []string{"IdentitiesOnly yes\n", "SecurityKeyProvider internal\n", "IdentityFile /tmp/y/id_new\nIdentityFile /tmp/y/id_old\n",
		"/.ssh/config\nInclude /etc/ssh/ssh_config\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	// the backup key comes before the Include: ssh takes the first value it finds
	if strings.Index(got, "IdentitiesOnly") > strings.Index(got, "Include") {
		t.Errorf("order:\n%s", got)
	}
}
