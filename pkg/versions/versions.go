// Package versions knows every version pinned in toolchain/pins.yaml: where its releases
// come from, how far its bugfix updates go, and which native tool installs it. nimble tool
// list, tool update and tool use all read this one list.
package versions

import (
	"fmt"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/releases"
)

// Entry is one pinned version.
type Entry struct {
	Key       string // pins.yaml key; npmGlobal for npm packages
	Name      string // the npm package, for npmGlobal entries
	Label     string // how nimble shows it and how you name it: go, typescript, npm, …
	Current   string // "" = not pinned (follows the newest release)
	Level     string // bugfix scheme: patch (default), minor, major
	Installer string // the native tool that installs it
	Source    func() ([]string, error)
	Marks     func() (map[string]string, error) // optional notes per release (node: its LTS line)
}

func gh(repo string) func() ([]string, error) {
	return func() ([]string, error) { return releases.GitHubReleases(repo) }
}

// Entries lists the pinned versions — the programming languages: node, go, rust, python —
// and the pinned npm packages, in the order of pins.yaml. nimble tool list and tool update
// check these.
func Entries(p *config.Pins) []Entry { return entries(p, false) }

// Selectable lists what nimble tool use can set: the pinned versions, plus the npm packages
// that follow the newest release (use can pin them, and unpin them again).
func Selectable(p *config.Pins) []Entry { return entries(p, true) }

func entries(p *config.Pins, unpinnedNPM bool) []Entry {
	es := []Entry{
		{Key: "node", Label: "node", Current: p.Node, Installer: "nvm", Source: releases.Node, Marks: releases.NodeLTS},
		{Key: "go", Label: "go", Current: p.Go, Installer: "go.dev release in ~/sdk", Source: releases.Go},
		{Key: "rust", Label: "rust", Current: p.Rust, Level: "minor", Installer: "rustup", Source: gh("rust-lang/rust")},
		{Key: "python", Label: "python", Current: p.Python, Installer: "uv", Source: releases.Python},
	}
	for _, spec := range p.NPMGlobal {
		name, cur := nimble.SplitSpec(spec)
		es = append(es, Entry{Key: "npmGlobal", Name: name, Label: name, Current: cur, Installer: "npm",
			Source: func() ([]string, error) { return releases.NPM(name) }})
	}
	var out []Entry
	for _, e := range es {
		if e.Current != "" || (unpinnedNPM && e.Key == "npmGlobal" && e.Name != "") {
			out = append(out, e)
		}
	}
	return out
}

// Find looks an entry up by its label, key or npm name.
func Find(es []Entry, name string) (Entry, bool) {
	for _, e := range es {
		if name == e.Label || name == e.Key || (e.Name != "" && name == e.Name) {
			return e, true
		}
	}
	return Entry{}, false
}

// Labels lists the entries' labels.
func Labels(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Label)
	}
	return out
}

// Write pins version for the entry in pins.yaml (text edit: comments and layout stay).
func Write(e Entry, version string) error {
	if e.Key == "npmGlobal" {
		return config.SetNPMGlobal(e.Name, version)
	}
	if e.Current == "" {
		return fmt.Errorf("%s isn't pinned in toolchain/pins.yaml", e.Label)
	}
	return config.ReplaceVersion(e.Key, e.Name, e.Current, version)
}
