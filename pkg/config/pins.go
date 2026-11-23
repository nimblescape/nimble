package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// Pins is toolchain/pins.yaml: the versions of the programming languages every machine uses —
// Node, Go, Rust, Python. Every other tool stays current (node brings @nimblescape/typescript,
// at its newest release).
type Pins struct {
	Node    string   `yaml:"node"`
	Go      string   `yaml:"go"`
	Rust    string   `yaml:"rust"`
	Python  string   `yaml:"python"`
	GoTools []string `yaml:"goTools"` // go install targets (@latest unless a version is given)
	UVTools []string `yaml:"uvTools"` // uv tool install specs
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
// stay exactly as they are): on the line of the top-level key, current becomes latest.
func ReplaceVersion(key, current, latest string) error {
	b, err := os.ReadFile(PinsFile())
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, key+":") {
			continue
		}
		if r := replaceToken(l, current, latest); r != l {
			lines[i] = r
			return os.WriteFile(PinsFile(), []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("pins.yaml: %s %s not found", key, current)
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
