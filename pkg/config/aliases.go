package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// Directory aliases (nimble alias add|remove|list) live in the personal layer, so every
// workstation of yours has them: personal/aliases.yaml maps a name to a directory, written
// as ~/… under the home folder. <name> alone then runs cd <dir> (see setup.WriteAliases).

const aliasesHeader = "Directory aliases: <name> alone runs cd <dir>. nimble alias add|remove|list change them."

var (
	aliasName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	// they go into the alias between double quotes, inside single ones
	aliasDirBad = "'\"$`\\\n"
)

// AliasesFile is personal/aliases.yaml.
func AliasesFile() string { return filepath.Join(PersonalDir(), "aliases.yaml") }

// DirAliases reads personal/aliases.yaml (none: an empty map).
func DirAliases() (map[string]string, error) {
	out := map[string]string{}
	if !nimble.Exists(AliasesFile()) {
		return out, nil
	}
	if err := readYAML(AliasesFile(), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

// SetDirAliases writes personal/aliases.yaml (sorted by name); with none left, it goes.
func SetDirAliases(m map[string]string) error {
	if len(m) == 0 {
		if err := os.Remove(AliasesFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeYAML(AliasesFile(), m, aliasesHeader)
}

// CheckAlias checks a directory alias: a name a shell takes, a directory it can quote.
func CheckAlias(name, dir string) error {
	if !aliasName.MatchString(name) {
		return fmt.Errorf("alias %q: letters, digits, '_', '.' and '-' (not first)", name)
	}
	if dir == "" || strings.ContainsAny(dir, aliasDirBad) {
		return fmt.Errorf("alias %s: directory %q (no quotes, $, ` or \\)", name, dir)
	}
	return nil
}

// HomeDir writes dir as ~/… when it is under the home folder (the same on every Mac of
// yours), otherwise as it is.
func HomeDir(dir string) string {
	if h := nimble.Home(); dir == h {
		return "~"
	} else if rel, ok := strings.CutPrefix(dir, h+"/"); ok {
		return "~/" + rel
	}
	return dir
}

// ExpandDir is HomeDir undone: the directory on this machine.
func ExpandDir(dir string) string {
	if dir == "~" {
		return nimble.Home()
	}
	if rel, ok := strings.CutPrefix(dir, "~/"); ok {
		return filepath.Join(nimble.Home(), rel)
	}
	return dir
}

func validateAliases() []error {
	m, err := DirAliases()
	if err != nil {
		return []error{err}
	}
	var errs []error
	for name, dir := range m {
		if err := CheckAlias(name, dir); err != nil {
			errs = append(errs, fmt.Errorf("personal/aliases.yaml: %w", err))
		}
	}
	return errs
}
