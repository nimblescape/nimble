package tool

import (
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/setup"
)

func TestAddChoices(t *testing.T) {
	catalog := []setup.Tool{
		{Name: "starship", Desc: "the starship prompt, the same in bash and zsh", Short: "the prompt"},
		{Name: "gh", Desc: "GitHub CLI"}, // no Short: its Desc is short already
		{Name: "docker", Desc: "Docker"},
		{Name: "iterm2", Desc: "iTerm2", OS: "macos"},
	}
	r := config.Resolved{Template: "dev", TemplateTools: []string{"starship"}}
	has := toolSources(r, config.Host{Tools: []string{"gh", "starship"}})
	if has["starship"] != "template dev" || has["gh"] != "host file" || has["docker"] != "" {
		t.Fatalf("sources %v", has)
	}

	labels, checked, names := addChoices(catalog, has)
	if len(labels) != 4 || len(checked) != 2 {
		t.Fatalf("labels %q checked %q", labels, checked)
	}
	if checked[0] != "starship  the prompt (template)" { // the short label, not the long Desc
		t.Errorf("checked[0] %q", checked[0])
	}
	if checked[1] != "gh        GitHub CLI (host file)" || labels[2] != "docker    Docker" {
		t.Errorf("labels %q", labels)
	}

	// docker newly checked, gh (host file) unchecked: docker is added, gh removed
	add, drop, kept := picked(catalog, []string{checked[0], labels[2]}, names, has)
	if strings.Join(add, ",") != "docker" || strings.Join(drop, ",") != "gh" || len(kept) != 0 {
		t.Errorf("add %v drop %v kept %v", add, drop, kept)
	}
	// everything unchecked: gh removed, starship (template) stays
	add, drop, kept = picked(catalog, nil, names, has)
	if len(add) != 0 || strings.Join(drop, ",") != "gh" || strings.Join(kept, ",") != "starship" {
		t.Errorf("add %v drop %v kept %v", add, drop, kept)
	}
	// nothing touched: nothing changes
	add, drop, kept = picked(catalog, checked, names, has)
	if len(add)+len(drop)+len(kept) != 0 {
		t.Errorf("add %v drop %v kept %v", add, drop, kept)
	}
}

func TestCatalogFor(t *testing.T) {
	names := func(ts []setup.Tool) string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Name)
		}
		return "," + strings.Join(out, ",") + ","
	}
	if !strings.Contains(names(setup.CatalogFor("macos")), ",iterm2,") {
		t.Error("a Mac is offered no iterm2")
	}
	if debian := names(setup.CatalogFor("debian")); strings.Contains(debian, ",iterm2,") || !strings.Contains(debian, ",docker,") {
		t.Errorf("debian: %s", debian)
	}
}
