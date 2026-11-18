package remote

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
	"gopkg.in/yaml.v3"
)

// A host gets a snapshot of the config, not the repo: only what it needs — its own host
// file and template, the pins, the shell files, its domain's keys, the links (and their
// files) for Debian — without git history, other hosts, other people's or macOS-only files.
// The workstation keeps the repo and sends a fresh snapshot with every nimble update <host>.

// SnapshotMarker names the file that marks ~/.nimble on a host as a snapshot.
const SnapshotMarker = ".snapshot"

// Snapshot writes the snapshot for host name into a new temporary directory.
func Snapshot(name string) (string, error) {
	c, err := config.Load()
	if err != nil {
		return "", err
	}
	h, ok := config.FindHost(name)
	if !ok {
		return "", fmt.Errorf("%s is not in hosts/", name)
	}
	d, err := c.Domain(h.Domain)
	if err != nil {
		return "", err
	}
	tmpl := h.Template
	if tmpl == "" {
		tmpl = c.DefaultTemplate
	}
	dir, err := os.MkdirTemp("", "nimble-snapshot-"+name)
	if err != nil {
		return "", err
	}
	repo := nimble.Repo()
	copyRel := func(rel string) error {
		return copyPath(filepath.Join(repo, rel), filepath.Join(dir, rel))
	}

	files := []string{"hosts/" + name + ".yaml", "toolchain/pins.yaml", "shell"}
	if tmpl != "" {
		files = append(files, "templates/"+tmpl+".yaml")
	}
	if nimble.Exists(filepath.Join(repo, "personal", "shell")) {
		files = append(files, "personal/shell")
	}
	for _, k := range keys.Trusted(d) {
		rel, _ := filepath.Rel(repo, k.Path())
		files = append(files, rel)
	}
	var shared, personal []config.Link
	for _, l := range c.Links {
		if l.OS != "" && l.OS != "debian" {
			continue
		}
		rel, err := filepath.Rel(repo, l.Source())
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		files = append(files, rel)
		if l.Base == config.PersonalDir() {
			personal = append(personal, l)
		} else {
			shared = append(shared, l)
		}
	}
	for _, rel := range files {
		if err := copyRel(rel); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}

	head := strings.TrimSpace(nimble.Git("rev-parse", "--short", "HEAD"))
	if nimble.Git("status", "--porcelain") != "" {
		head += " + uncommitted changes"
	}
	note := fmt.Sprintf("# Snapshot of the config for %s, from %s (%s) — don't edit: the workstation's\n# nimble update %s replaces it.\n", name, hosts.MachineName(), head, name)
	conf := map[string]any{"defaultTemplate": tmpl, "domains": map[string]any{d.Name: map[string]string{"label": d.Label}}, "links": linkList(shared)}
	if err := writeYAMLFile(filepath.Join(dir, "nimble.yaml"), note, conf); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if len(personal) > 0 {
		if err := writeYAMLFile(filepath.Join(dir, "personal", "nimble.yaml"), note, map[string]any{"links": linkList(personal)}); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	marker := fmt.Sprintf("host: %s\nfrom: %s\ncommit: %s\ndate: %s\n", name, hosts.MachineName(), head, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, SnapshotMarker), []byte(marker), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func linkList(ls []config.Link) []map[string]string {
	out := []map[string]string{}
	for _, l := range ls {
		m := map[string]string{"from": l.From, "to": l.To}
		if l.OS != "" {
			m["os"] = l.OS
		}
		out = append(out, m)
	}
	return out
}

func writeYAMLFile(path, note string, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(note), b...), 0o644)
}

// copyPath copies a file or a directory tree (regular files only).
func copyPath(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, fi.Mode().Perm())
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !info.Mode().IsRegular() || info.Name() == ".DS_Store" {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		return copyPath(p, filepath.Join(dst, rel))
	})
}

// unpack runs on the host: the new snapshot is unpacked next to ~/.nimble first, and only a
// complete one replaces the old (a transfer that breaks off leaves the host's config as it
// was). cache/, backup/ and local.sh stay.
const unpack = `set -e
new="$HOME/.nimble.new"
rm -rf "$new"
mkdir -p "$new" "$HOME/.nimble"
tar -xzf - -C "$new"
cd "$HOME/.nimble"
find . -mindepth 1 -maxdepth 1 ! -name cache ! -name backup ! -name local.sh -exec rm -rf {} +
find "$new" -mindepth 1 -maxdepth 1 ! -name cache ! -name backup ! -name local.sh -exec mv {} . \;
rm -rf "$new"`

// SendSnapshot replaces the host's ~/.nimble with its snapshot; cache/, backup/ and
// local.sh stay (an earlier full checkout, git history included, goes).
func SendSnapshot(host string) error {
	dir, err := Snapshot(host)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tar := exec.Command("tar", "-C", dir, "--no-xattrs", "-czf", "-", ".")
	tar.Env = append(os.Environ(), "COPYFILE_DISABLE=1")
	tar.Stderr = nimble.Stderr()
	pipe, err := tar.StdoutPipe()
	if err != nil {
		return err
	}
	ssh := hosts.SSH(host, unpack)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = pipe, nimble.Stdout(), nimble.Stderr() // under a Wait: stop its spinner first
	if err := tar.Start(); err != nil {
		return err
	}
	if err := nimble.Exec(ssh); err != nil {
		_ = tar.Wait()
		return err
	}
	return tar.Wait()
}
