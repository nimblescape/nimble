package versions

import (
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestEntries(t *testing.T) {
	p := &config.Pins{Node: "26.8.1", Go: "1.27.2", NPMGlobal: []string{"npm", "typescript@6.0.3"}}
	if got := Labels(Entries(p)); len(got) != 3 || got[2] != "typescript" {
		t.Errorf("pinned: %v", got) // node, go, typescript: what nimble tool list checks
	}
	sel := Selectable(p)
	if e, ok := Find(sel, "npm"); !ok || e.Current != "" {
		t.Errorf("nimble tool use must offer an unpinned npm package, so it can be pinned: %v", Labels(sel))
	}
}
