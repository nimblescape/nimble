package releases

import (
	"encoding/json"
	"testing"
)

func TestNewest(t *testing.T) {
	rel := []string{"1.27.1", "1.27.4", "v1.28.0", "1.28.2", "2.0.0-rc1", "1.26.9"}
	for _, c := range []struct{ cur, level, compat, feature string }{
		{"1.27.2", "patch", "1.27.4", "1.28.2"},
		{"1.27.4", "patch", "", "1.28.2"},
		{"1.27.2", "minor", "1.28.2", ""},
		{"1.28.2", "patch", "", ""},
	} {
		if got := NewestCompatible(c.cur, rel, c.level); got != c.compat {
			t.Errorf("NewestCompatible(%s, %s) = %q, want %q", c.cur, c.level, got, c.compat)
		}
		if got := NewestFeature(c.cur, rel, c.level); got != c.feature {
			t.Errorf("NewestFeature(%s, %s) = %q, want %q", c.cur, c.level, got, c.feature)
		}
	}
	if got := NewestFeature("1.27.2", append(rel, "2.1.0"), "minor"); got != "2.1.0" {
		t.Errorf("major for minor level: %q", got)
	}
}

func TestNodeLTS(t *testing.T) {
	var rs []nodeRelease
	if err := json.Unmarshal([]byte(`[{"version":"v25.2.0","lts":false},{"version":"v24.11.1","lts":"Krypton"},{"version":"v24.9.0","lts":false},{"version":"v22.21.1","lts":"Jod"}]`), &rs); err != nil {
		t.Fatal(err)
	}
	m := nodeLTS(rs)
	if len(m) != 2 || m["24.11.1"] != "LTS Krypton" || m["22.21.1"] != "LTS Jod" {
		t.Errorf("lts: %v", m)
	}
}
