package skeleton

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "home", "laptop"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitignore", "shell/zshrc", "shell/zsh/50-completion.zsh", "ssh/defaults", "toolchain/pins.yaml",
		"templates/base.yaml", "hosts/laptop.yaml", ".github/workflows/patch-updates.yml", "personal/nimble.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	t.Setenv("NIMBLE_HOME", dir)
	if errs := config.Validate([]string{"starship", "node", "go", "python"}); len(errs) > 0 {
		t.Fatalf("generated repo invalid: %v", errs)
	}
	if c, err := config.Load(); err != nil || c.DefaultTemplate != "base" || c.Domains[0].Name != "home" {
		t.Fatalf("%+v %v", c, err)
	}
	if h, ok := config.FindHost("laptop"); !ok || !h.Workstation() {
		t.Fatalf("host %+v", h)
	}
}
