package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "swan.yaml")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	const code = "/usr/local/bin/code"
	for _, c := range []struct {
		name, goos, code string
		env              map[string]string
		paths            []string
		want             string
	}{
		{"mac desktop: VS Code waits for a file", "darwin", code, nil, []string{file}, code + " --wait " + file},
		{"mac desktop: a folder just opens", "darwin", code, nil, []string{dir}, code + " " + dir},
		{"VS Code wins over VISUAL on a desktop", "darwin", code, map[string]string{"VISUAL": "vim"}, []string{file}, code + " --wait " + file},
		{"no VS Code: VISUAL", "darwin", "", map[string]string{"VISUAL": "nvim -p"}, []string{file}, "nvim -p " + file},
		{"no VS Code, no VISUAL: vi", "darwin", "", nil, []string{file}, "vi " + file},
		{"over ssh: VISUAL", "darwin", code, map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22", "VISUAL": "vim"}, []string{file}, "vim " + file},
		{"linux without a display: VISUAL", "linux", code, map[string]string{"VISUAL": "vim"}, []string{file}, "vim " + file},
		{"linux desktop: VS Code", "linux", code, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{file}, code + " --wait " + file},
		{"VS Code's terminal over Remote-SSH: VS Code", "linux", code, map[string]string{"TERM_PROGRAM": "vscode", "SSH_CONNECTION": "x"}, []string{file}, code + " --wait " + file},
	} {
		getenv := func(k string) string { return c.env[k] }
		if got := strings.Join(editorCommand(getenv, c.goos, c.code, c.paths), " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
