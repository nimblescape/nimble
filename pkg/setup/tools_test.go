package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeInstalled(t *testing.T) {
	home, nvm := t.TempDir(), t.TempDir()
	t.Setenv("NIMBLE_HOME", home)
	t.Setenv("NVM_DIR", nvm)
	if err := os.MkdirAll(filepath.Join(home, "toolchain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "toolchain", "pins.yaml"), []byte("node: 26.13.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvm, "nvm.sh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if nodeInstalled() {
		t.Error("nvm alone counts as node")
	}
	bin := filepath.Join(nvm, "versions", "node", "v26.13.1", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !nodeInstalled() {
		t.Error("the pinned Node doesn't count as node")
	}
}

// A vendor repo without a build for this release falls back to the older releases of the
// same distribution; a release newer than nimble knows falls back to all of them.
func TestCodenameCandidates(t *testing.T) {
	for _, c := range []struct {
		distro, codename string
		want             string
	}{
		{"debian", "trixie", "trixie bookworm"},
		{"debian", "forky", "forky trixie bookworm"},
		{"debian", "duke", "duke forky trixie bookworm"},
		{"ubuntu", "noble", "noble"},
		{"ubuntu", "plucky", "plucky oracular noble"},
		{"ubuntu", "tenacious", "tenacious stonking resolute questing plucky oracular noble"},
	} {
		if got := strings.Join(codenameCandidates(c.distro, c.codename), " "); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.distro, c.codename, got, c.want)
		}
	}
}
