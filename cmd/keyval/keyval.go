// Package keyval gives the commands that change a file's settings (host set, template set:
// <name> key=value…) the same two ways besides the command line: tab completion of the keys
// and their values, and a guided menu when no key=value is given on a terminal.
package keyval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

// Setting is one key of a set command.
type Setting struct {
	Key, Help string
	Value     string   // the current value, as key=value takes it ("" = not set)
	Show      string   // the current value for the menu (default: Value)
	List      bool     // a comma list: completion goes on after a comma, the menu is a checklist
	Choices   []Choice // the values to pick from; none: free text
}

// Choice is one value of a setting. Value "" removes the key (Desc says what that means);
// Alone: chosen in a list, it stands for itself (reach=all).
type Choice struct {
	Value, Desc string
	Alone       bool
}

// Complete completes `<name> key=value…`: names first, then the keys not given yet and,
// after key=, the values — in a list after its last comma, without those already in it.
func Complete(names func() []string, settings func(name string) []Setting) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return names(), cobra.ShellCompDirectiveNoFileComp
		}
		given := map[string]bool{}
		for _, a := range args[1:] {
			k, _, _ := strings.Cut(a, "=")
			given[k] = true
		}
		var out []string
		key, val, hasValue := strings.Cut(toComplete, "=")
		for _, s := range settings(args[0]) {
			if !hasValue {
				if !given[s.Key] && strings.HasPrefix(s.Key, key) {
					out = append(out, s.Key+"=\t"+s.Help)
				}
				continue
			}
			if s.Key != key {
				continue
			}
			if len(s.Choices) == 0 { // free text: the current value, to edit
				if s.Value != "" {
					out = append(out, key+"="+s.Value+"\tcurrent")
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			}
			prefix, chosen := key+"=", map[string]bool{}
			if i := strings.LastIndex(val, ","); s.List && i >= 0 {
				prefix += val[:i+1]
				for _, v := range strings.Split(val[:i], ",") {
					chosen[v] = true
				}
			}
			for _, c := range s.Choices {
				if c.Value != "" && !chosen[c.Value] && !(c.Alone && len(chosen) > 0) {
					out = append(out, prefix+c.Value+"\t"+c.Desc)
				}
			}
			if s.List { // a comma and the next one may follow
				return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
}

// CompleteFlag completes a flag (host create --mode …) with the values of setting key; a list
// flag after its last comma.
func CompleteFlag(key string, settings func() []Setting) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		for _, s := range settings() {
			if s.Key != key {
				continue
			}
			prefix, chosen := "", map[string]bool{}
			if i := strings.LastIndex(toComplete, ","); s.List && i >= 0 {
				prefix = toComplete[:i+1]
				for _, v := range strings.Split(toComplete[:i], ",") {
					chosen[v] = true
				}
			}
			var out []string
			for _, c := range s.Choices {
				if c.Value != "" && !chosen[c.Value] && !(c.Alone && len(chosen) > 0) {
					out = append(out, prefix+c.Value+"\t"+c.Desc)
				}
			}
			if s.List {
				return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// Menu is the guided way: it lists the settings with their values, edits the one picked —
// a list to pick from, a checklist or a text field — and checks each change at once. It
// returns the changes as key=value (nil: nothing to save). settings shows the values as they
// are with the changes so far; it fails when the changes don't validate.
func Menu(title string, settings func(changes []string) ([]Setting, error)) ([]string, error) {
	if !nimble.Interactive() {
		return nil, errors.New("missing key=value (no terminal for the menu)")
	}
	var changes []string
	ss, err := settings(nil)
	if err != nil {
		return nil, err
	}
	for {
		changed := map[string]bool{}
		for _, kv := range changes {
			k, _, _ := strings.Cut(kv, "=")
			changed[k] = true
		}
		w := 0
		for _, s := range ss {
			w = max(w, len(s.Key))
		}
		var labels []string
		byLabel := map[string]Setting{}
		for _, s := range ss {
			show := s.Show
			if show == "" {
				show = s.Value
			}
			l := fmt.Sprintf("%-*s  %s", w, s.Key, show)
			if changed[s.Key] {
				l += "   " + nimble.Yellow("(changed)")
			}
			labels, byLabel[l] = append(labels, l), s
		}
		save, discard := "✔ save", "✗ leave without saving"
		if len(changes) > 0 {
			save = "✔ save: " + strings.Join(changes, " ")
			labels = append(labels, save)
		}
		labels = append(labels, discard)
		pick, err := nimble.Select(title+" — pick a setting (ESC: leave without saving):", labels, "")
		if errors.Is(err, nimble.ErrCancelled) || pick == discard {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if pick == save {
			return changes, nil
		}
		s := byLabel[pick]
		v, err := edit(s)
		if errors.Is(err, nimble.ErrCancelled) { // back to the menu
			continue
		}
		if err != nil {
			return nil, err
		}
		if v == s.Value && !changed[s.Key] {
			continue
		}
		next := []string{}
		for _, kv := range changes { // a later value of the same key replaces the earlier one
			if k, _, _ := strings.Cut(kv, "="); k != s.Key {
				next = append(next, kv)
			}
		}
		next = append(next, s.Key+"="+v)
		got, err := settings(next)
		if err != nil {
			nimble.Warn(err.Error() + " — not taken")
			continue
		}
		changes, ss = next, got
	}
}

// edit asks for a setting's new value: a checklist for a list, a list to pick from for
// choices, a text field otherwise. ErrCancelled (ESC) goes back to the menu.
func edit(s Setting) (string, error) {
	msg := s.Key + ": " + s.Help
	if len(s.Choices) == 0 {
		return nimble.Input(msg+" (empty removes it):", s.Value, nil)
	}
	w := 0
	for _, c := range s.Choices {
		w = max(w, len(c.Value))
	}
	var labels []string
	value := map[string]Choice{}
	for _, c := range s.Choices {
		shown := c.Value
		if shown == "" {
			shown = "—"
		}
		l := fmt.Sprintf("%-*s  %s", w, shown, c.Desc)
		labels, value[l] = append(labels, l), c
	}
	if !s.List {
		def := ""
		for _, l := range labels {
			if value[l].Value == s.Value {
				def = l
			}
		}
		pick, err := nimble.Select(msg+" (ESC: back):", labels, def)
		return value[pick].Value, err
	}
	current := map[string]bool{}
	for _, v := range strings.Split(s.Value, ",") {
		current[v] = true
	}
	var checked []string
	for _, l := range labels {
		if current[value[l].Value] {
			checked = append(checked, l)
		}
	}
	picks, err := nimble.Checklist(msg+" (space toggles, enter takes, ESC: back):", labels, checked)
	if err != nil {
		return "", err
	}
	var vs []string
	for _, l := range picks {
		c := value[l]
		if c.Alone {
			return c.Value, nil
		}
		vs = append(vs, c.Value)
	}
	return strings.Join(vs, ","), nil
}

// Action is an entry of a command group's menu: one of its subcommands.
type Action struct{ Label, Sub string }

// Actions makes a command group (nimble host, nimble template) a menu of its subcommands on
// a terminal; without one it prints the help as before.
func Actions(cmd *cobra.Command, title string, actions []Action) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, args []string) error {
		if !nimble.Interactive() {
			return c.Help()
		}
		var labels []string
		sub := map[string]string{}
		for _, a := range actions {
			labels, sub[a.Label] = append(labels, a.Label), a.Sub
		}
		pick, err := nimble.Select(title+" (ESC or Ctrl+C to cancel):", labels, "")
		if errors.Is(err, nimble.ErrCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
		s, _, err := cmd.Find([]string{sub[pick]})
		if err != nil {
			return err
		}
		return s.RunE(s, nil)
	}
}
