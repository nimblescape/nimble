package template

import (
	"errors"
	"os"
	"strings"

	"github.com/nimblescape/nimble/cmd/keyval"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/setup"
)

// settings are template set's keys for template t, with t's values: what tab completion
// offers and the guided menu shows.
func settings(t config.Template) []keyval.Setting {
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	var tools []keyval.Choice
	for _, tool := range setup.CatalogFor(t.OS) {
		tools = append(tools, keyval.Choice{Value: tool.Name, Desc: tool.Short})
	}
	return []keyval.Setting{
		{Key: "description", Help: "what the template is for", Value: t.Description, Show: or(t.Description, "—")},
		{Key: "os", Help: "the machines it is offered for", Value: t.OS, Show: or(t.OS, "both"), Choices: []keyval.Choice{
			{Value: "debian", Desc: "Debian and Ubuntu hosts"}, {Value: "macos", Desc: "Macs"}, {Value: "", Desc: "both"},
		}},
		{Key: "shell", Help: "login shell", Value: t.Shell, Show: or(t.Shell, "the system's"), Choices: []keyval.Choice{
			{Value: "zsh", Desc: "the Z shell"}, {Value: "bash", Desc: "GNU bash"}, {Value: "", Desc: "the system's login shell"},
		}},
		{Key: "tools", Help: "tools a host made from it gets", Value: strings.Join(t.Tools, ","),
			Show: or(strings.Join(t.Tools, ", "), "none"), List: true, Choices: tools},
	}
}

// completeSet completes template set: template names, then key=value.
var completeSet = keyval.Complete(templateNames, func(name string) []keyval.Setting {
	t, err := config.FindTemplate(name)
	if err != nil {
		return nil
	}
	return settings(t)
})

// preview is template name as it would be with the changes: written, checked and read
// back, then the file is restored.
func preview(name string, changes []string) (config.Template, error) {
	file := config.TemplateFile(name)
	before, err := os.ReadFile(file)
	if err != nil {
		return config.Template{}, err
	}
	defer os.WriteFile(file, before, 0o644)
	for _, kv := range changes {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return config.Template{}, errors.New(kv + ": expected key=value")
		}
		if err := config.SetTemplate(name, k, v); err != nil {
			return config.Template{}, err
		}
	}
	if err := check(name); err != nil {
		return config.Template{}, err
	}
	return config.FindTemplate(name)
}

// menu is template set's guided way: the changes for template name, or nil.
func menu(name string) ([]string, error) {
	return keyval.Menu(name, func(changes []string) ([]keyval.Setting, error) {
		t, err := preview(name, changes)
		if err != nil {
			return nil, err
		}
		return settings(t), nil
	})
}
