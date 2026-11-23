package alias

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckName(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "mytool"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	for name, ok := range map[string]bool{"1a1": true, "proj.x": true, "my_dir": true,
		"mytool": false, "cd": false, "nimble": false, "a b": false, "-x": false} {
		if err := checkName(name, "~/x"); (err == nil) != ok {
			t.Errorf("checkName(%q) = %v", name, err)
		}
	}
	// the folder's name is suggested when it makes a free name
	if s := suggest("/home/x/1a", map[string]string{}); s != "1a" {
		t.Errorf("suggest = %q", s)
	}
	if s := suggest("/home/x/1a", map[string]string{"1a": "~/y"}); s != "" {
		t.Errorf("taken name suggested: %q", s)
	}
	if s := suggest("/home/x/My Folder", nil); s != "" {
		t.Errorf("unusable name suggested: %q", s)
	}
}
