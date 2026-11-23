package versions

import (
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestEntries(t *testing.T) {
	p := &config.Pins{Node: "26.8.1", Go: "1.27.2", Python: "3.12.16"} // rust not pinned
	if got := Labels(Entries(p)); len(got) != 3 || got[0] != "node" || got[2] != "python" {
		t.Errorf("pinned: %v", got) // the pinned languages, in the order of pins.yaml
	}
	if e, ok := Find(Entries(p), "go"); !ok || e.Current != "1.27.2" {
		t.Errorf("find go: %+v", e)
	}
	if _, ok := Find(Entries(p), "rust"); ok {
		t.Error("an unpinned language is offered")
	}
}
