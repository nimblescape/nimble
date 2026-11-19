// Package skeleton is the starting point of a new config repo (nimble init): the shared
// shell files, ssh defaults, pins, a base and an example template, the daily workflow.
package skeleton

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:files
var files embed.FS

// Write creates a new config repo in dir for a first domain and this machine.
func Write(dir, domain, machine string) error {
	err := fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, strings.TrimPrefix(path, "files/"))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") && strings.Contains(path, "macos/") {
			mode = 0o755
		}
		return os.WriteFile(dst, b, mode)
	})
	if err != nil {
		return err
	}
	conf := fmt.Sprintf(`# nimble configuration for this config repo.

# 2: host files hold their tools and shell themselves; templates are copied in.
format: 2

# The template host create offers to copy, and what a machine without a host file gets
# (nimble template default <name>).
defaultTemplate: base

# Administration domains: who runs the machines. Each has the label of its daily key
# (a Secure Enclave key on every workstation: nimble key enclave). A host trusts its
# domain's daily keys plus every YubiKey backup. The first domain is the default.
domains:
  %s:
    label: ssh

# Config files linked from this repo into $HOME for everyone (os: macos | debian limits a
# link). Personal links go into personal/nimble.yaml.
links: []
`, domain)
	if err := os.WriteFile(filepath.Join(dir, "nimble.yaml"), []byte(conf), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "hosts"), 0o755); err != nil {
		return err
	}
	host := "# this machine, the workstation that manages the hosts\nmode: workstation\n"
	return os.WriteFile(filepath.Join(dir, "hosts", machine+".yaml"), []byte(host), 0o644)
}
