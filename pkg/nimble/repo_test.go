package nimble

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a host only its own host file may change; each change is noted for the workstation.
func TestHostChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NIMBLE_HOME", home)
	w := func(rel, body string) {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w(".snapshot", "host: swan\ncommit: 80c5c93\ndate: 2026-11-18T10:00:00Z\n")
	w("hosts/swan.yaml", "address: swan\n")
	w(SentDir+"/hosts/swan.yaml", "address: swan\n")

	if SnapshotHost() != "swan" || SnapshotInfo() != "snapshot of 80c5c93, sent 2026-11-18T10:00:00Z" {
		t.Fatalf("host %q, info %q", SnapshotHost(), SnapshotInfo())
	}
	if EditableHost("swan") != nil || EditableHost("other") == nil || Editable() == nil {
		t.Fatal("only the host's own file may change")
	}
	if changed, _ := HostChanges(); changed {
		t.Fatal("changed without a change")
	}
	if err := Record("templates: add x", "templates"); err == nil {
		t.Fatal("recorded a change beyond the host file")
	}

	w("hosts/swan.yaml", "address: swan\nshell: bash\n")
	if err := Record("hosts: swan shell=bash", "hosts"); err != nil {
		t.Fatal(err)
	}
	if err := Record("hosts: swan add tools docker", "hosts/swan.yaml"); err != nil {
		t.Fatal(err)
	}
	changed, log := HostChanges()
	if !changed || strings.Join(log, "|") != "hosts: swan shell=bash|hosts: swan add tools docker" {
		t.Fatalf("changed %v, log %q", changed, log)
	}

	// changed back by hand: nothing left to read back
	w("hosts/swan.yaml", "address: swan\n")
	if changed, _ := HostChanges(); changed {
		t.Fatal("changed back, still reported")
	}
}
