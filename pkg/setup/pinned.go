package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/releases"
)

// The programming languages are pinned (toolchain/pins.yaml, nimble tool use): Node via nvm,
// npm packages via npm, Go from go.dev into ~/sdk, Rust via rustup, Python via uv. Every other tool
// stays current: installed at its newest version and brought up to date by its own
// mechanism (Homebrew, apt, npm, its self-updater) on nimble update.

// goInstall puts the pinned Go where VS Code's Go extension (and golang.org/dl) keep their
// Go versions — ~/sdk/go<version>, go.dev's release — and makes it the default go:
// ~/.local/go links to it and comes first in PATH. The extension lists every ~/sdk
// version and can switch a workspace to another one; nimble sets no GOTOOLCHAIN, which
// would override that choice.
func goInstall(p *config.Pins) error {
	sdk := filepath.Join(nimble.Home(), "sdk", "go"+p.Go)
	if !nimble.Exists(filepath.Join(sdk, "bin", "go")) {
		if err := goSDK(p.Go, sdk); err != nil {
			return err
		}
	}
	if t, _ := os.Readlink(goLink()); t != sdk {
		nimble.Say("go " + p.Go + " → default (~/.local/go → ~/sdk/go" + p.Go + ")")
		_ = os.Remove(goLink())
		if err := os.MkdirAll(filepath.Dir(goLink()), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(sdk, goLink()); err != nil {
			return err
		}
	}
	if have := GoVersion(); have != p.Go {
		return fmt.Errorf("go reports %s, pins.yaml says %s — check GOTOOLCHAIN (go env)", have, p.Go)
	}
	return goTools(p)
}

func goLink() string { return filepath.Join(nimble.Home(), ".local", "go") }

// GoVersion is the version of the default Go (~/.local/go), "" if there is none.
func GoVersion() string {
	return strings.TrimPrefix(nimble.Output(filepath.Join(goLink(), "bin", "go"), "env", "GOVERSION"), "go")
}

// goSDK unpacks go.dev's release into dir the way golang.org/dl does (with its marker).
func goSDK(version, dir string) error {
	return nimble.Wait("go "+version+" (go.dev) → "+nimble.Tilde(dir), func() error {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(filepath.Dir(dir), ".nimble-go")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		tgz := filepath.Join(tmp, "go.tar.gz")
		if err := nimble.Download(fmt.Sprintf("https://go.dev/dl/go%s.%s-%s.tar.gz", version, runtime.GOOS, runtime.GOARCH), tgz); err != nil {
			return err
		}
		if err := (nimble.Cmd{Name: "tar", Args: []string{"-xzf", tgz, "-C", tmp}}).Do(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, "go", ".unpacked-success"), nil, 0o644); err != nil {
			return err
		}
		_ = os.RemoveAll(dir)
		return os.Rename(filepath.Join(tmp, "go"), dir)
	})
}

// uvInstall: uv's own installer (newest version, into ~/.local/bin); uv self update keeps
// it current (uvUpdate).
func uvInstall(*config.Pins) error {
	if _, err := os.Stat(filepath.Join(localBin(), "uv")); err == nil {
		return nil
	}
	return nimble.Wait("uv (uv's installer) → ~/.local/bin", func() error {
		return runInstaller("https://astral.sh/uv/install.sh", []string{"UV_NO_MODIFY_PATH=1"}, "sh")
	})
}

func uvUpdate(*config.Pins) error {
	uv := filepath.Join(localBin(), "uv")
	if _, err := os.Stat(uv); err != nil {
		return nil // installed some other way (e.g. Homebrew): its package manager updates it
	}
	return nimble.Cmd{Name: uv, Args: []string{"self", "update", "--quiet"}, Quiet: true}.Do()
}

// nvmNewest is nvm's newest release ("" if GitHub can't be asked).
func nvmNewest() string {
	tags, err := releases.GitHubReleases("nvm-sh/nvm")
	if s := releases.Sorted(tags); err == nil && len(s) > 0 {
		return s[0]
	}
	return ""
}

// nvmInstall clones nvm at its newest release, if it isn't there yet.
func nvmInstall(dir string) error {
	if nimble.Exists(filepath.Join(dir, "nvm.sh")) {
		return nil
	}
	newest := nvmNewest()
	if newest == "" {
		return errors.New("can't find nvm's newest release on GitHub")
	}
	return nimble.Wait("nvm "+newest, nimble.Cmd{Name: "git", Args: []string{"-c", "advice.detachedHead=false", "clone", "-q", "--depth", "1",
		"--branch", "v" + newest, "https://github.com/nvm-sh/nvm.git", dir}}.Do)
}

// nvmUpdate moves an nvm checkout to the newest release (nimble update).
func nvmUpdate(dir string) error {
	if !nimble.Exists(filepath.Join(dir, "nvm.sh")) {
		return nvmInstall(dir) // gone (deleted by hand): back at the newest release
	}
	newest := nvmNewest()
	if newest == "" || nimble.JSONField(filepath.Join(dir, "package.json"), "version") == newest {
		return nil
	}
	return nimble.Wait("nvm → "+newest, func() error {
		if err := (nimble.Cmd{Name: "git", Quiet: true, Args: []string{"-C", dir, "fetch", "-q", "--depth", "1", "origin", "tag", "v" + newest}}).Do(); err != nil {
			return err
		}
		return nimble.Cmd{Name: "git", Quiet: true, Args: []string{"-c", "advice.detachedHead=false", "-C", dir, "checkout", "-q", "v" + newest}}.Do()
	})
}

// starshipInstall: Homebrew on macOS, the newest release binary on Debian.
func starshipInstall(*config.Pins) error {
	if _, err := exec.LookPath("starship"); err == nil {
		return nil
	}
	if Darwin() {
		return brew("starship", false)
	}
	return starshipRelease()
}

func starshipUpdate(*config.Pins) error {
	if Darwin() {
		return brewUpgrade("starship", false)
	}
	tags, err := releases.GitHubReleases("starship/starship")
	if err != nil || len(releases.Sorted(tags)) == 0 {
		return nil
	}
	if nimble.Field(nimble.Output("starship", "--version"), 1) == releases.Sorted(tags)[0] {
		return nil
	}
	return starshipRelease()
}

func starshipRelease() error {
	target := map[string]string{"amd64": "x86_64-unknown-linux-musl", "arm64": "aarch64-unknown-linux-musl"}[runtime.GOARCH]
	return nimble.Wait("starship (newest release) → ~/.local/bin", func() error {
		return installFromTarball("https://github.com/starship/starship/releases/latest/download/starship-"+target+".tar.gz", 0, "starship")
	})
}

// brewUpgrade upgrades a Homebrew package when it is installed through Homebrew and outdated.
func brewUpgrade(pkg string, cask bool) error {
	kind := "--formula"
	if cask {
		kind = "--cask"
	}
	if exec.Command("brew", "list", kind, pkg).Run() != nil {
		return nil // not from Homebrew
	}
	if strings.TrimSpace(nimble.Output("brew", "outdated", kind, "--quiet", pkg)) == "" {
		return nil
	}
	return nimble.Wait("brew upgrade "+pkg, nimble.Cmd{Name: "brew", Args: []string{"upgrade", kind, pkg}, Env: []string{"HOMEBREW_NO_ENV_HINTS=1"}}.Do)
}

// aptUpgrade upgrades Debian packages that are installed.
func aptUpgrade(pkgs ...string) error {
	var have []string
	for _, p := range pkgs {
		if nimble.DebInstalled(p) {
			have = append(have, p)
		}
	}
	if len(have) == 0 {
		return nil
	}
	if err := nimble.AsRoot(nimble.Cmd{Name: "apt-get", Args: []string{"update", "-qq"}, Quiet: true}); err != nil {
		return err
	}
	return nimble.AsRoot(nimble.Cmd{Name: "apt-get", Args: append([]string{"install", "-y", "-qq", "--only-upgrade"}, have...),
		Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Quiet: true})
}

// updateWith picks the macOS or the Debian update.
func updateWith(mac, debian func() error) func(*config.Pins) error {
	return func(*config.Pins) error {
		if Darwin() {
			return mac()
		}
		return debian()
	}
}

// runInstaller downloads a vendor's install script and runs it with args.
func runInstaller(url string, env []string, shell string, args ...string) error {
	tmp, err := os.CreateTemp("", "nimble-installer-*.sh")
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := nimble.Download(url, tmp.Name()); err != nil {
		return err
	}
	return nimble.Cmd{Name: shell, Args: append([]string{tmp.Name()}, args...), Env: env, Quiet: true}.Do()
}
