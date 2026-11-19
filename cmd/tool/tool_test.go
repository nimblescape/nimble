package tool

import (
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/setup"
)

func TestAddChoices(t *testing.T) {
	catalog := []setup.Tool{
		{Name: "starship", Desc: "the starship prompt, the same in bash and zsh", Short: "the prompt"},
		{Name: "gh", Desc: "GitHub CLI"}, // no Short: its Desc is short already
		{Name: "docker", Desc: "Docker"},
		{Name: "iterm2", Desc: "iTerm2", OS: "macos"},
	}
	has := []string{"gh", "starship"} // the host file's tools

	labels, checked, names := addChoices(catalog, has)
	if len(labels) != 4 || len(checked) != 2 {
		t.Fatalf("labels %q checked %q", labels, checked)
	}
	if checked[0] != "starship  the prompt" { // the short label, not the long Desc; catalog order
		t.Errorf("checked[0] %q", checked[0])
	}
	if checked[1] != "gh        GitHub CLI" || labels[2] != "docker    Docker" {
		t.Errorf("labels %q", labels)
	}

	// docker newly checked, gh unchecked: docker is added, gh removed
	add, drop := picked(catalog, []string{checked[0], labels[2]}, names, has)
	if strings.Join(add, ",") != "docker" || strings.Join(drop, ",") != "gh" {
		t.Errorf("add %v drop %v", add, drop)
	}
	// everything unchecked: everything the host file has is removed
	add, drop = picked(catalog, nil, names, has)
	if len(add) != 0 || strings.Join(drop, ",") != "starship,gh" {
		t.Errorf("add %v drop %v", add, drop)
	}
	// nothing touched: nothing changes
	add, drop = picked(catalog, checked, names, has)
	if len(add)+len(drop) != 0 {
		t.Errorf("add %v drop %v", add, drop)
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

func TestShellChoices(t *testing.T) {
	labels, sel, names := shellChoices("")
	if len(labels) != 3 || sel != labels[2] || names[sel] != "default" {
		t.Fatalf("labels %q selected %q", labels, sel)
	}
	if labels[2] != "default  the system's login shell (current)" {
		t.Errorf("labels[2] %q", labels[2])
	}
	labels, sel, names = shellChoices("bash")
	if sel != labels[1] || names[sel] != "bash" || names[labels[0]] != "zsh" || strings.Contains(labels[2], "current") {
		t.Errorf("labels %q selected %q", labels, sel)
	}
}
