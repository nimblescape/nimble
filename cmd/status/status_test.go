package status

import (
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestServiceLine(t *testing.T) {
	on := config.Service{Service: "claude", Repo: "nimblescape/nimbleSEC", State: "enabled"}
	off := config.Service{Service: "claude", Repo: "nimblescape/nimbleSEC", State: "disabled"}
	for _, c := range []struct {
		s       config.Service
		state   string
		running bool
		kind    string
		says    string
	}{
		{on, "enabled", true, "ok", "enabled, running (claude-nimblescape-nimbleSEC.service)"},
		{on, "enabled", false, "bad", "not running — systemctl status claude-nimblescape-nimbleSEC.service"},
		{on, "disabled", false, "bad", "the host file says enabled — nimble update (on swan)"},
		{on, "not installed", false, "bad", "nimble service add claude nimblescape/nimbleSEC (on swan"},
		{off, "disabled", false, "ok", "disabled, as the host file says"},
		{off, "enabled", true, "bad", "the host file says disabled — nimble update (on swan)"},
		{off, "not installed", false, "note", "the host file has it disabled"},
	} {
		kind, msg := serviceLine(c.s, "swan", c.state, c.running)
		if kind != c.kind || !strings.Contains(msg, c.says) {
			t.Errorf("%s/%s running=%v: %s %q, want %s with %q", c.s.State, c.state, c.running, kind, msg, c.kind, c.says)
		}
	}
}
