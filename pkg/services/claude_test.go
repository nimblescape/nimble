package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	if Unit("nimblescape/nimbleSEC") != "claude-nimblescape-nimbleSEC.service" || Checkout("nimblescape/nimbleSEC") != "/home/claude/nimblescape/nimbleSEC" {
		t.Fatal(Unit("nimblescape/nimbleSEC"), Checkout("nimblescape/nimbleSEC"))
	}
	u := unit("nimblescape/nimbleSEC")
	for _, want := range []string{"User=claude", "WorkingDirectory=/home/claude/nimblescape/nimbleSEC",
		"ExecStart=/home/claude/.local/bin/claude remote-control --spawn=worktree", "KillSignal=SIGINT", "Restart=always", "WantedBy=multi-user.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q", want)
		}
	}
	if out := os.Getenv("NIMBLE_UNIT_OUT"); out != "" { // for systemd-analyze verify in a container
		os.WriteFile(out, []byte(u), 0o644)
	}
}

func TestOtherServers(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "claude-remote-control.service"), []byte("[Service]\nWorkingDirectory=/home/claude/nimblescape/nimbleSEC\nExecStart=/home/claude/.local/bin/claude remote-control --spawn=worktree\n"), 0o644)
	os.WriteFile(filepath.Join(d, "claude-nimblescape-nimbleSEC.service"), []byte(unit("nimblescape/nimbleSEC")), 0o644)
	os.WriteFile(filepath.Join(d, "other.service"), []byte("[Service]\nWorkingDirectory=/home/claude/nimblescape/nimbleSEC-2\nExecStart=claude remote-control\n"), 0o644)
	got := otherServers(d, "/home/claude/nimblescape/nimbleSEC", Unit("nimblescape/nimbleSEC"))
	if strings.Join(got, ",") != "claude-remote-control.service" {
		t.Fatal(got)
	}
}
