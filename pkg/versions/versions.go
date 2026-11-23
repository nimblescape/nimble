// Package versions knows every version pinned in toolchain/pins.yaml — the programming
// languages: where their releases come from, how far their bugfix updates go, and which
// native tool installs them. nimble tool list, tool update and tool use all read this list.
package versions

import (
	"fmt"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/releases"
)

// Entry is one pinned version.
type Entry struct {
	Key       string // the pins.yaml key: node, go, rust, python
	Label     string // how nimble shows it and how you name it
	Current   string // the pinned version ("" = not pinned)
	Level     string // bugfix scheme: patch (default), minor, major
	Installer string // the native tool that installs it
	Source    func() ([]string, error)
	Marks     func() (map[string]string, error) // optional notes per release (node: its LTS line)
}

func gh(repo string) func() ([]string, error) {
	return func() ([]string, error) { return releases.GitHubReleases(repo) }
}

// Entries lists the pinned languages — node, go, rust, python — in the order of pins.yaml.
func Entries(p *config.Pins) []Entry {
	var out []Entry
	for _, e := range []Entry{
		{Key: "node", Label: "node", Current: p.Node, Installer: "nvm", Source: releases.Node, Marks: releases.NodeLTS},
		{Key: "go", Label: "go", Current: p.Go, Installer: "go.dev release in ~/sdk", Source: releases.Go},
		{Key: "rust", Label: "rust", Current: p.Rust, Level: "minor", Installer: "rustup", Source: gh("rust-lang/rust")},
		{Key: "python", Label: "python", Current: p.Python, Installer: "uv", Source: releases.Python},
	} {
		if e.Current != "" {
			out = append(out, e)
		}
	}
	return out
}

// Find looks an entry up by its label or key.
func Find(es []Entry, name string) (Entry, bool) {
	for _, e := range es {
		if name == e.Label || name == e.Key {
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
	if e.Current == "" {
		return fmt.Errorf("%s isn't pinned in toolchain/pins.yaml", e.Label)
	}
	return config.ReplaceVersion(e.Key, e.Current, version)
}
