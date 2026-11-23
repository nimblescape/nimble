package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirAliases(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	if m, err := DirAliases(); err != nil || len(m) != 0 {
		t.Fatalf("no file: %v %v", m, err)
	}
	if err := SetDirAliases(map[string]string{"1a1": HomeDir(filepath.Join(home, "work", "1a")), "tmp": "/tmp/x y"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "personal", "aliases.yaml"))
	if s := string(b); !strings.HasPrefix(s, "# Directory aliases") || !strings.Contains(s, "1a1: ~/work/1a\n") || !strings.Contains(s, "tmp: /tmp/x y\n") {
		t.Errorf("aliases.yaml:\n%s", s)
	}
	m, err := DirAliases()
	if err != nil || m["1a1"] != "~/work/1a" || ExpandDir(m["1a1"]) != filepath.Join(home, "work", "1a") || ExpandDir(m["tmp"]) != "/tmp/x y" {
		t.Errorf("read back: %v %v", m, err)
	}
	if HomeDir(home) != "~" || ExpandDir("~") != home || HomeDir("/opt/x") != "/opt/x" {
		t.Error("HomeDir/ExpandDir")
	}
	tools := []string{"starship", "go", "docker"}
	if errs := Validate(tools); len(errs) > 0 {
		t.Errorf("valid aliases reported: %v", errs)
	}

	for _, bad := range [][2]string{{"-x", "~/a"}, {"a b", "~/a"}, {"a", "~/it's"}, {"a", "~/$HOME"}, {"a", ""}} {
		if CheckAlias(bad[0], bad[1]) == nil {
			t.Errorf("CheckAlias(%q, %q) passes", bad[0], bad[1])
		}
	}
	os.WriteFile(filepath.Join(dir, "personal", "aliases.yaml"), []byte("ok: ~/a\n\"a b\": ~/b\n"), 0o644)
	if errs := Validate(tools); len(errs) == 0 || !strings.Contains(errs[0].Error(), "personal/aliases.yaml") {
		t.Errorf("a bad alias name isn't reported: %v", errs)
	}

	// the last one removed: the file goes
	if err := SetDirAliases(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "personal", "aliases.yaml")); err == nil {
		t.Error("empty aliases.yaml left")
	}
}
