package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The workstation asks a host how a server stands, without root.
func TestState(t *testing.T) {
	for out, want := range map[string]string{
		"not installed":    "not installed",
		"enabled running":  "enabled, running",
		"enabled\n":        "enabled, not running",
		"disabled":         "disabled",
		"disabled running": "disabled, but running",
	} {
		if s, r := ParseState(out); Describe(s, r) != want {
			t.Errorf("%q: %q", out, Describe(s, r))
		}
	}
	if s, _ := ParseState(""); s != "" { // the host couldn't be asked
		t.Errorf("empty answer: %q", s)
	}
	// here, without the unit file
	out, err := exec.Command("sh", "-c", StateScript("owner/nosuchrepo")).Output()
	if s, _ := ParseState(string(out)); err != nil || s != "not installed" {
		t.Errorf("script: %q %v", out, err)
	}
}

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
