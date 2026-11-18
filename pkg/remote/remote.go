// Package remote sets hosts up from the workstation: key login, sending nimble and the
// config repo, and bootstrapping a fresh host through one password session.
package remote

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// KeyLogin reports whether ssh with keys alone works (a fresh connection, no password).
func KeyLogin(dest string) bool {
	args := []string{"-o", "ControlPath=none", "-o", "PreferredAuthentications=publickey"}
	if hosts.YubiKey() { // a YubiKey asks for its PIN, so no batch mode
		c := hosts.SSH(append(args, dest, "true")...)
		c.Stdin, c.Stderr = os.Stdin, os.Stderr
		return c.Run() == nil
	}
	return hosts.SSH(append(args, "-o", "BatchMode=yes", dest, "true")...).Run() == nil
}

// built keeps the binary per CPU for this run: nimble update all sends the same one to every host.
var built = map[string][]byte{}

// Build gets nimble for a host's CPU: built from the source checkout (NIMBLE_SRC) during
// development, otherwise the running version's release binary, checked against the
// release's checksums.
func Build(arch string) ([]byte, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("the host's CPU is %q (uname -m) — nimble has builds for amd64 and arm64", arch)
	}
	if b, ok := built[arch]; ok {
		return b, nil
	}
	b, err := build(arch)
	if err == nil {
		built[arch] = b
	}
	return b, err
}

func build(arch string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "nimble-linux")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "nimble")
	if src := nimble.SourceDir(); src != "" {
		if err := (nimble.Cmd{Name: "go", Dir: src, Env: []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch},
			Args: []string{"build", "-trimpath", "-ldflags", "-s -w -X main.version=" + nimble.SourceVersion(), "-o", bin, "."}}).Do(); err != nil {
			return nil, err
		}
		return os.ReadFile(bin)
	}
	if nimble.Version == "dev" || strings.Contains(nimble.Version, "-") {
		return nil, errors.New("this nimble is a development build and NIMBLE_SRC isn't set — point it at your checkout of nimblescape/nimble")
	}
	v := strings.TrimPrefix(nimble.Version, "v")
	asset := fmt.Sprintf("nimble_%s_linux_%s.tar.gz", v, arch)
	if err := release(tmp, "v"+v, asset, "checksums.txt"); err != nil {
		return nil, err
	}
	if err := nimble.VerifySHA256(filepath.Join(tmp, asset), filepath.Join(tmp, "checksums.txt"), asset); err != nil {
		return nil, err
	}
	if err := (nimble.Cmd{Name: "tar", Args: []string{"-xzf", filepath.Join(tmp, asset), "-C", tmp, "nimble"}}).Do(); err != nil {
		return nil, err
	}
	return os.ReadFile(bin)
}

// release downloads files of a nimble release into dir: with gh when it can (it also
// reaches a private repo), else over HTTPS.
func release(dir, tag string, names ...string) error {
	const repo = "nimblescape/nimble"
	if _, err := exec.LookPath("gh"); err == nil {
		args := []string{"release", "download", tag, "-R", repo, "-D", dir, "--clobber"}
		for _, n := range names {
			args = append(args, "-p", n)
		}
		if exec.Command("gh", args...).Run() == nil {
			return nil
		}
	}
	for _, n := range names {
		if err := nimble.Download("https://github.com/"+repo+"/releases/download/"+tag+"/"+n, filepath.Join(dir, n)); err != nil {
			return err
		}
	}
	return nil
}

// SendBinary installs nimble (built here for the host's CPU) as ~/.local/bin/nimble there.
func SendBinary(host string) error {
	data, err := Build(nimble.GoArch(hosts.Output(host, "uname -m")))
	if err != nil {
		return err
	}
	return hosts.Run(host, hosts.Opts{Stdin: bytes.NewReader(data)},
		`mkdir -p ~/.local/bin && cat >~/.local/bin/nimble.new && chmod 755 ~/.local/bin/nimble.new && mv ~/.local/bin/nimble.new ~/.local/bin/nimble`)
}

// Trusted returns the authorized_keys lines for a host (its domain's keys + backups).
func Trusted(h config.Host) ([]string, error) {
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	d, err := c.Domain(h.Domain)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys.Trusted(d) {
		out = append(out, k.Line())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no trusted keys for domain %s in keys/", d.Name)
	}
	return out, nil
}

// Bootstrap prepares a fresh host through ONE password session as whatever user the host
// offers (root, or a user with sudo): it uploads nimble and the trusted keys and runs
// `nimble bootstrap` as root, which creates the admin user and authorizes the keys.
// Nothing is locked down here.
func Bootstrap(h config.Host) error {
	initial := nimble.Ask("First login to "+h.Address+": which user?", "root")
	target := initial + "@" + h.Address
	tmp, err := os.MkdirTemp("", "nimble-boot")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	sock := filepath.Join(tmp, "cm")
	ssh := func(args ...string) []string {
		return append([]string{"-o", "ControlPath=" + sock}, args...)
	}

	nimble.Step("log in to " + target + " (password)")
	if err := nimble.Run("ssh", ssh("-o", "ControlMaster=yes", "-o", "ControlPersist=300", "-fN", target)...); err != nil {
		return err
	}
	defer exec.Command("ssh", ssh("-O", "exit", target)...).Run()

	arch := nimble.GoArch(nimble.Output("ssh", ssh(target, "uname -m")...))
	bin, err := Build(arch)
	if err != nil {
		return err
	}
	trusted, err := Trusted(h)
	if err != nil {
		return err
	}
	upload := func(data []byte, path string) error {
		c := exec.Command("ssh", ssh(target, "cat > "+path+" && chmod 755 "+path)...)
		c.Stdin, c.Stderr = bytes.NewReader(data), os.Stderr
		return c.Run()
	}
	if err := upload(bin, "/tmp/nimble-bootstrap"); err != nil {
		return err
	}
	if err := upload([]byte(strings.Join(trusted, "\n")+"\n"), "/tmp/nimble-keys"); err != nil {
		return err
	}
	nimble.Step("bootstrap as root: sudo, admin user " + h.AdminUser() + ", trusted keys, nimble")
	run := `boot="/tmp/nimble-bootstrap bootstrap --user ` + h.AdminUser() + ` --keys /tmp/nimble-keys --binary /tmp/nimble-bootstrap"
if [ "$(id -u)" = 0 ]; then $boot; elif command -v sudo >/dev/null; then sudo $boot; else echo "neither root nor sudo" >&2; false; fi
rc=$?; rm -f /tmp/nimble-bootstrap /tmp/nimble-keys; exit $rc`
	return nimble.Run("ssh", ssh("-t", target, run)...)
}
