package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// The toolchain tools (node, go, python, rust, starship) install the versions pinned in
// toolchain/pins.yaml, the same way on macOS and Debian; only what differs changes.

func orNone(s string) string {
	if s == "" {
		return "not installed"
	}
	return s
}

func localBin() string { return filepath.Join(nimble.Home(), ".local", "bin") }

// installFromTarball puts the named files of a release tarball into ~/.local/bin.
func installFromTarball(url string, strip int, files ...string) error {
	tmp, err := os.MkdirTemp("", "nimble-dl")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "dl.tar.gz")
	if err := nimble.Download(url, tgz); err != nil {
		return err
	}
	if err := (nimble.Cmd{Name: "tar", Args: []string{"-xzf", tgz, "-C", tmp, fmt.Sprintf("--strip-components=%d", strip)}}).Do(); err != nil {
		return err
	}
	if err := os.MkdirAll(localBin(), 0o755); err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(tmp, f))
		if err != nil {
			return err
		}
		dst := filepath.Join(localBin(), filepath.Base(f))
		if err := os.WriteFile(dst+".new", data, 0o755); err != nil {
			return err
		}
		if err := os.Rename(dst+".new", dst); err != nil {
			return err
		}
	}
	return nil
}

// --- node -----------------------------------------------------------------------------

func nodeBin(p *config.Pins) string {
	return filepath.Join(os.Getenv("NVM_DIR"), "versions", "node", "v"+p.Node, "bin")
}

func node(p *config.Pins) error {
	if nimble.IsDebian() { // curl for nvm's downloads; the official Node binaries need libatomic
		if _, err := nimble.AptInstall("curl", "xz-utils", "libatomic1"); err != nil {
			return err
		}
	}
	nvm := os.Getenv("NVM_DIR")
	if err := nvmInstall(nvm); err != nil {
		return err
	}
	if !nimble.Exists(filepath.Join(nodeBin(p), "node")) {
		if err := nimble.Wait("node "+p.Node, nimble.Cmd{Name: "bash", Quiet: true, Args: []string{"-c",
			`. "$NVM_DIR/nvm.sh" && nvm install --no-progress "$1"`, "nvm", p.Node}}.Do); err != nil {
			return err
		}
	}
	// the exact version, not e.g. "node" (= newest installed), so the default can't drift
	if nimble.ReadTrim(filepath.Join(nvm, "alias", "default")) != p.Node {
		if err := (nimble.Cmd{Name: "bash", Quiet: true, Args: []string{"-c", `. "$NVM_DIR/nvm.sh" && nvm alias default "$1"`, "nvm", p.Node}}).Do(); err != nil {
			return err
		}
	}
	return npmGlobals(p, false)
}

// nodeUpdate is node's part of nimble update: nvm and the npm packages that aren't pinned
// move to their newest release. Node itself and pinned packages only move with their pin.
func nodeUpdate(p *config.Pins) error {
	if err := nvmUpdate(os.Getenv("NVM_DIR")); err != nil {
		return err
	}
	return npmGlobals(p, true)
}

func npmEnv(p *config.Pins) []string { return []string{"PATH=" + nodeBin(p) + ":" + os.Getenv("PATH")} }

// npmGlobals installs the global npm packages of pins.yaml into the pinned Node: a pinned
// one at exactly its version, an unpinned one if it is missing — or, with latest, at its
// newest release.
func npmGlobals(p *config.Pins, latest bool) error {
	bin := nodeBin(p)
	root := filepath.Join(filepath.Dir(bin), "lib", "node_modules")
	for _, spec := range p.NPMGlobal {
		name, want := nimble.SplitSpec(spec)
		if name == "" {
			continue
		}
		have := nimble.JSONField(filepath.Join(root, name, "package.json"), "version")
		switch {
		case want != "" && have == want:
			continue
		case want == "" && have != "" && !latest:
			continue
		case want == "" && have != "":
			cmd := exec.Command(filepath.Join(bin, "npm"), "view", name, "version")
			cmd.Env = append(os.Environ(), npmEnv(p)...)
			out, _ := cmd.Output()
			if want = strings.TrimSpace(string(out)); want == "" || want == have {
				continue
			}
		}
		target := name + "@latest"
		if want != "" {
			target = name + "@" + want
		}
		if err := nimble.Wait(fmt.Sprintf("npm install -g %s (was: %s)", target, orNone(have)), nimble.Cmd{Name: filepath.Join(bin, "npm"), Env: npmEnv(p),
			Args: []string{"install", "-g", "--no-fund", "--no-audit", "--loglevel=error", target}}.Do); err != nil {
			return err
		}
	}
	return nil
}

// --- go -------------------------------------------------------------------------------

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// GoToolBin is the command name `go install` gives a package: its last path element, or
// the one before a major-version suffix (…/yq/v4 → yq).
func GoToolBin(pkg string) string {
	pkg, _, _ = strings.Cut(pkg, "@")
	parts := strings.Split(strings.Trim(pkg, "/"), "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && majorSuffix.MatchString(last) {
		last = parts[len(parts)-2]
	}
	return last
}

// GoBinDir is where `go install` puts commands: $GOBIN, else the first $GOPATH's bin ("" without Go).
func GoBinDir() string {
	if d := nimble.Output("go", "env", "GOBIN"); d != "" {
		return d
	}
	gopath := nimble.Output("go", "env", "GOPATH")
	if gopath == "" {
		return ""
	}
	return filepath.Join(filepath.SplitList(gopath)[0], "bin")
}

func goTools(p *config.Pins) error {
	dir := GoBinDir()
	if dir == "" {
		return nil
	}
	for _, pkg := range p.GoTools {
		if nimble.Exists(filepath.Join(dir, GoToolBin(pkg))) {
			continue
		}
		if !strings.Contains(pkg, "@") {
			pkg += "@latest"
		}
		if err := nimble.Wait("go install "+pkg, nimble.Cmd{Name: "go", Args: []string{"install", pkg}}.Do); err != nil {
			return err
		}
	}
	return nil
}

// --- python (uv) ------------------------------------------------------------------------
// uv plays nvm + npm -g for Python: prebuilt Python versions, per-project selection via
// .python-version, virtual environments — the same on macOS and Debian.

func python(p *config.Pins) error {
	if err := uvInstall(p); err != nil {
		return err
	}
	if _, err := exec.LookPath("uv"); err != nil {
		return nil
	}
	if exec.Command("uv", "python", "find", "--no-project", "--no-config", p.Python).Run() != nil {
		if err := nimble.Wait("python "+p.Python+" (uv)", nimble.Cmd{Name: "uv", Args: []string{"-q", "python", "install", p.Python}}.Do); err != nil {
			return fmt.Errorf("%w — uv may not know Python %s yet: nimble update brings uv up to date", err, p.Python)
		}
	}
	py := filepath.Join(localBin(), "python3")
	if nimble.Output(py, "-c", "import platform; print(platform.python_version())") != p.Python {
		if err := nimble.Wait("default python → "+p.Python, nimble.Cmd{Name: "uv", Args: []string{"-q", "python", "install", "--default", "--force", p.Python}}.Do); err != nil {
			return err
		}
	}
	// global pin: used by `uv venv` / `uv run` outside projects with their own .python-version
	if nimble.ReadTrim(filepath.Join(nimble.XDGConfig(), "uv", ".python-version")) != p.Python {
		if err := (nimble.Cmd{Name: "uv", Args: []string{"-q", "python", "pin", "--global", p.Python}}).Do(); err != nil {
			return err
		}
	}
	installed := map[string]bool{}
	for _, l := range nimble.Lines(nimble.Output("uv", "tool", "list")) {
		if !strings.HasPrefix(l, "-") {
			installed[nimble.Field(l, 0)] = true
		}
	}
	for _, spec := range p.UVTools {
		fields := strings.FieldsFunc(spec, func(c rune) bool { return strings.ContainsRune("<>=!~@ [", c) })
		if len(fields) == 0 || installed[fields[0]] {
			continue
		}
		if err := nimble.Wait("uv tool install "+spec, nimble.Cmd{Name: "uv", Args: []string{"-q", "tool", "install", spec}}.Do); err != nil {
			return err
		}
	}
	return nil
}

// --- rust (rustup) ---------------------------------------------------------------------

func rust(p *config.Pins) error {
	if nimble.IsDebian() { // cargo links with cc; most crates also need a C toolchain
		if _, err := nimble.AptInstall("build-essential"); err != nil {
			return err
		}
	}
	rustup := filepath.Join(nimble.Home(), ".cargo", "bin", "rustup")
	if !nimble.Exists(rustup) {
		target := map[string]string{
			"darwin/arm64": "aarch64-apple-darwin", "darwin/amd64": "x86_64-apple-darwin",
			"linux/arm64": "aarch64-unknown-linux-gnu", "linux/amd64": "x86_64-unknown-linux-gnu",
		}[runtime.GOOS+"/"+runtime.GOARCH]
		tmp, err := os.MkdirTemp("", "nimble-rustup")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		init := filepath.Join(tmp, "rustup-init")
		return nimble.Wait("rustup + rust "+p.Rust, func() error {
			if err := nimble.Download("https://static.rust-lang.org/rustup/dist/"+target+"/rustup-init", init); err != nil {
				return err
			}
			if err := os.Chmod(init, 0o755); err != nil {
				return err
			}
			return nimble.Cmd{Name: init, Quiet: true, Args: []string{"-y", "--no-modify-path", "--profile", "default", "--default-toolchain", p.Rust}}.Do()
		})
	}
	if strings.HasPrefix(nimble.Output(rustup, "default"), p.Rust+"-") {
		return nil
	}
	return nimble.Wait("rust "+p.Rust+" (rustup default)", func() error {
		if err := (nimble.Cmd{Name: rustup, Quiet: true, Args: []string{"toolchain", "install", "--profile", "default", p.Rust}}).Do(); err != nil {
			return err
		}
		return nimble.Cmd{Name: rustup, Quiet: true, Args: []string{"default", p.Rust}}.Do()
	})
}
