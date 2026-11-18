package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// Pins is toolchain/pins.yaml: the versions of the programming languages every machine uses —
// Node, Go, Rust, Python, and npm packages pinned to a version. Every other tool stays current.
type Pins struct {
	Node      string   `yaml:"node"`
	Go        string   `yaml:"go"`
	Rust      string   `yaml:"rust"`
	Python    string   `yaml:"python"`
	NPMGlobal []string `yaml:"npmGlobal"` // name or name@version (pinned)
	GoTools   []string `yaml:"goTools"`   // go install targets (@latest unless a version is given)
	UVTools   []string `yaml:"uvTools"`   // uv tool install specs
}

// PinsFile is the path of toolchain/pins.yaml.
func PinsFile() string { return nimble.RepoFile("toolchain", "pins.yaml") }

// LoadPins reads toolchain/pins.yaml.
func LoadPins() (*Pins, error) {
	var p Pins
	if err := readYAML(PinsFile(), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ReplaceVersion rewrites one pinned version in pins.yaml as text (comments and layout
// stay exactly as they are): on the line of key (a top-level key, or "npmGlobal" for
// an entry name@version), current becomes latest.
func ReplaceVersion(key, name, current, latest string) error {
	b, err := os.ReadFile(PinsFile())
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	inList := ""
	done := false
	keyRe := regexp.MustCompile(`^([a-zA-Z]+):`)
	for i, l := range lines {
		if m := keyRe.FindStringSubmatch(l); m != nil {
			inList = m[1]
		}
		switch {
		case key == "npmGlobal" && inList == "npmGlobal":
			if r := replaceSpec(l, name+"@"+current, name+"@"+latest); r != l {
				lines[i] = r
				done = true
			}
		case inList == key && (strings.HasPrefix(l, key+":") || strings.HasPrefix(strings.TrimSpace(l), "- ")):
			if r := replaceToken(l, current, latest); r != l {
				lines[i] = r
				done = true
			}
		}
		if done {
			break
		}
	}
	if !done {
		return fmt.Errorf("pins.yaml: %s %s not found", key, current)
	}
	return os.WriteFile(PinsFile(), []byte(strings.Join(lines, "\n")), 0o644)
}

// replaceSpec swaps the npm spec old (name@version) for latest where it stands on its own:
// not inside another package's name (typescript in @scope/typescript) or a longer version.
func replaceSpec(line, old, latest string) string {
	part := func(b byte) bool {
		return b == '@' || b == '/' || b == '.' || b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for start := 0; ; {
		i := strings.Index(line[start:], old)
		if i < 0 {
			return line
		}
		i += start
		end := i + len(old)
		if (i == 0 || !part(line[i-1])) && (end == len(line) || !part(line[end])) {
			return line[:i] + latest + line[end:]
		}
		start = end
	}
}

// replaceToken swaps the first occurrence of old that isn't part of a longer version.
func replaceToken(line, old, latest string) string {
	isVer := func(b byte) bool { return b == '.' || (b >= '0' && b <= '9') }
	for start := 0; ; {
		i := strings.Index(line[start:], old)
		if i < 0 {
			return line
		}
		i += start
		end := i + len(old)
		if (i == 0 || !isVer(line[i-1])) && (end == len(line) || !isVer(line[end])) {
			return line[:i] + latest + line[end:]
		}
		start = end
	}
}

// SetNPMGlobal pins (or, with version "", unpins) a global npm package in pins.yaml.
func SetNPMGlobal(name, version string) error {
	b, err := os.ReadFile(PinsFile())
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	inList, done := false, false
	keyRe := regexp.MustCompile(`^([a-zA-Z]+):`)
	for i, l := range lines {
		if m := keyRe.FindStringSubmatch(l); m != nil {
			inList = m[1] == "npmGlobal"
			continue
		}
		t := strings.TrimSpace(l)
		if !inList || !strings.HasPrefix(t, "- ") {
			continue
		}
		spec := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "- ")), `"'`)
		if n, _ := nimble.SplitSpec(spec); n != name {
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
		item := name
		if version != "" {
			item = name + "@" + version
		}
		if strings.HasPrefix(item, "@") {
			item = `"` + item + `"`
		}
		lines[i] = indent + "- " + item
		done = true
		break
	}
	if !done {
		return fmt.Errorf("pins.yaml: npmGlobal has no %s", name)
	}
	return os.WriteFile(PinsFile(), []byte(strings.Join(lines, "\n")), 0o644)
}
