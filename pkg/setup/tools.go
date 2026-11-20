package setup

import (
	"errors"
	"fmt"
	"github.com/nimblescape/nimble/pkg/releases"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// Tool is something a template can list. Install sets it up on macOS and Debian alike,
// only what is missing or different; Remove takes it off again where that is safe.
type Tool struct {
	Name, Desc string
	Short      string // a few words, for tool select's list (Desc is for tool list)
	OS         string // "" (macOS and Debian) or macos: offered only when configuring a Mac
	Requires   []string
	Install    func(*config.Pins) error
	Remove     func() error // nil: nimble won't remove it (toolchains: delete by hand)
	Installed  func() bool
	Update     func(*config.Pins) error // tools that stay current: their own update (nimble update)
}

func has(cmd string) func() bool {
	return func() bool { _, err := exec.LookPath(cmd); return err == nil }
}

// nodeInstalled: the tool is Node, so it counts as installed when the pinned Node is there.
// nvm is only how nimble installs it (node installs nvm when it is missing, nimble update
// moves it to its newest release).
func nodeInstalled() bool {
	p, err := config.LoadPins()
	return err == nil && p.Node != "" && nimble.Exists(filepath.Join(nodeBin(p), "node"))
}

// npmHas checks for a global npm command of the pinned Node (nvm's bin isn't on nimble's PATH).
func npmHas(bin string) func() bool {
	return func() bool {
		p, err := config.LoadPins()
		return err == nil && nimble.Exists(filepath.Join(nodeBin(p), bin))
	}
}

// Catalog lists every tool nimble can set up.
var Catalog = []Tool{
	{Name: "starship", Desc: "the starship prompt, the same in bash and zsh (shell/starship.toml)", Short: "shell prompt",
		Install: starshipInstall, Installed: has("starship"), Update: starshipUpdate},
	{Name: "node", Desc: "Node at the pinned version (through nvm, installed with it when missing), the newest npm and ts (@nimblescape/typescript), and the TypeScript ts installs", Short: "Node + nvm, npm, ts (TypeScript)",
		Install: node, Installed: nodeInstalled, Update: nodeUpdate},
	{Name: "go", Desc: "Go (go.dev release in ~/sdk, the default go) and the go tools in pins.yaml", Short: "Go + go tools", Install: goInstall, Installed: func() bool { return GoVersion() != "" }},
	{Name: "python", Desc: "uv, the pinned Python, the uv tools in pins.yaml", Short: "Python (uv) + uv tools", Install: python, Installed: has("uv"), Update: uvUpdate},
	{Name: "rust", Desc: "rustup and the pinned Rust toolchain", Short: "Rust (rustup)", Install: rust,
		Installed: func() bool { return nimble.Exists(filepath.Join(nimble.Home(), ".cargo", "bin", "rustup")) }},
	{Name: "gh", Desc: "GitHub CLI", Installed: has("gh"),
		Install: either(brewFormula("gh"), aptRepo("githubcli", "https://cli.github.com/packages/githubcli-archive-keyring.gpg", false,
			"https://cli.github.com/packages stable main", "gh")),
		Remove: either0(brewRemove("gh", false), aptRemove("gh")), Update: updateWith(func() error { return brewUpgrade("gh", false) }, func() error { return aptUpgrade("gh") })},
	{Name: "azure", Desc: "Azure CLI", Installed: has("az"),
		Install: either(brewCask("azure-cli"), aptRepo("microsoft-azure-cli", "https://packages.microsoft.com/keys/microsoft.asc", true,
			"https://packages.microsoft.com/repos/azure-cli/ {codename} main", "azure-cli")),
		Remove: either0(brewRemove("azure-cli", true), aptRemove("azure-cli")), Update: updateWith(func() error { return brewUpgrade("azure-cli", true) }, func() error { return aptUpgrade("azure-cli") })},
	{Name: "gcloud", Desc: "Google Cloud CLI", Installed: has("gcloud"),
		Install: either(brewCask("gcloud-cli"), aptRepo("google-cloud-cli", "https://packages.cloud.google.com/apt/doc/apt-key.gpg", true,
			"https://packages.cloud.google.com/apt cloud-sdk main", "google-cloud-cli")),
		Remove: either0(brewRemove("gcloud-cli", true), aptRemove("google-cloud-cli")), Update: updateWith(func() error { return brewUpgrade("gcloud-cli", true) }, func() error { return aptUpgrade("google-cloud-cli") })},
	{Name: "docker", Desc: "Docker (Docker Desktop on macOS, Docker Engine + compose on Debian and Ubuntu)", Short: "Docker", Installed: has("docker"),
		Install: either(brewCask("docker-desktop"), aptRepo("docker", "https://download.docker.com/linux/{distro}/gpg", true,
			"https://download.docker.com/linux/{distro} {codename} stable",
			"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")),
		Remove: either0(brewRemove("docker-desktop", true), aptRemove("docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")), Update: updateWith(func() error { return nil /* Docker Desktop updates itself */ }, func() error {
			return aptUpgrade("docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
		})},
	{Name: "firebase", Desc: "Firebase CLI (npm firebase-tools)", Short: "Firebase CLI", Requires: []string{"node"}, Installed: npmHas("firebase"),
		Install: npmPackage("firebase-tools", "firebase"), Remove: npmRemove("firebase-tools"), Update: firebaseUpdate},
	{Name: "claude", Desc: "Claude Code (official installer, ~/.local/bin/claude)", Short: "Claude Code", Installed: has("claude"),
		Install: claudeCode},
	{Name: "lima", Desc: "Lima Linux VMs (limactl)", Short: "Linux VMs", Installed: has("limactl"),
		Install: either(brewUnlessPresent("limactl", "lima", false), limaDebian),
		Remove:  either0(brewRemove("lima", false), limaRemove), Update: either(func(*config.Pins) error { return brewUpgrade("lima", false) }, limaDebianUpdate)},
	{Name: "task", Desc: "Task runner for Taskfile.yml (go-task)", Short: "task runner", Installed: has("task"),
		Install: either(brewUnlessPresent("task", "go-task", false), taskDebian),
		Remove:  either0(brewRemove("go-task", false), removeLocal("task")), Update: either(func(*config.Pins) error { return brewUpgrade("go-task", false) }, taskDebianUpdate)},
	{Name: "iterm2", Desc: "iTerm2 terminal (settings: personal/macos/iterm2)", Short: "terminal app", OS: "macos", Installed: itermInstalled,
		Install: either(func(*config.Pins) error {
			if itermInstalled() {
				return nil // also when installed without Homebrew (/Applications or ~/Applications)
			}
			return brew("iterm2", true)
		}, func(*config.Pins) error {
			nimble.Say("note: iterm2 is a macOS app — skipped here")
			return nil
		}),
		Remove: either0(brewRemove("iterm2", true), func() error { return nil })},
}

// removeLocal deletes release binaries nimble put into ~/.local/bin.
func removeLocal(files ...string) func() error {
	return func() error {
		for _, f := range files {
			if err := os.Remove(filepath.Join(localBin(), f)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
}

// limaRemove deletes Lima's release from ~/.local (the QEMU packages stay).
func limaRemove() error {
	local := filepath.Join(nimble.Home(), ".local")
	paths := []string{filepath.Join(local, "bin", "limactl"), filepath.Join(local, "bin", "lima"),
		filepath.Join(local, "share", "lima"), filepath.Join(local, "share", "doc", "lima"), filepath.Join(local, "libexec", "lima")}
	for _, pattern := range []string{filepath.Join(local, "bin", "*.lima"), filepath.Join(local, "share", "man", "man1", "lima*.1")} {
		m, _ := filepath.Glob(pattern)
		paths = append(paths, m...)
	}
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func itermInstalled() bool {
	return nimble.Exists("/Applications/iTerm.app") || nimble.Exists(filepath.Join(nimble.Home(), "Applications", "iTerm.app"))
}

// FindTool looks a tool up in the catalog.
func FindTool(name string) (Tool, bool) {
	for _, t := range Catalog {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// For reports whether a tool is offered for a machine with the given OS (macos, debian;
// "" = any).
func (t Tool) For(os string) bool { return t.OS == "" || os == "" || t.OS == os }

// CatalogFor is the catalog as offered for a machine with the given OS.
func CatalogFor(os string) []Tool {
	var out []Tool
	for _, t := range Catalog {
		if t.For(os) {
			out = append(out, t)
		}
	}
	return out
}

// ToolNames are the names in the catalog.
func ToolNames() []string {
	var out []string
	for _, t := range Catalog {
		out = append(out, t.Name)
	}
	return out
}

// KnownTools are the names a template or host file may list (what config.Validate checks
// against): the catalog's.
func KnownTools() []string { return ToolNames() }

// InstallTools sets up the named tools (in catalog order, required tools first).
func InstallTools(names []string) error {
	p, err := config.LoadPins()
	if err != nil {
		return err
	}
	want := map[string]bool{}
	var add func(string) error
	add = func(n string) error {
		t, ok := FindTool(n)
		if !ok {
			return fmt.Errorf("unknown tool %q (known: %s)", n, strings.Join(ToolNames(), ", "))
		}
		for _, r := range t.Requires {
			if err := add(r); err != nil {
				return err
			}
		}
		want[n] = true
		return nil
	}
	for _, n := range names {
		if err := add(n); err != nil {
			return err
		}
	}
	// one tool failing (a vendor's server down) doesn't stop the others
	var failed []string
	for _, t := range Catalog {
		if want[t.Name] {
			if err := t.Install(p); err != nil {
				nimble.Warn(t.Name + ": " + err.Error())
				failed = append(failed, t.Name)
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("not installed: %s (see above)", strings.Join(failed, ", "))
	}
	return nil
}

// either picks the macOS or the Debian recipe.
func either(mac, debian func(*config.Pins) error) func(*config.Pins) error {
	return func(p *config.Pins) error {
		if Darwin() {
			return mac(p)
		}
		return debian(p)
	}
}

func either0(mac, debian func() error) func() error {
	return func() error {
		if Darwin() {
			return mac()
		}
		return debian()
	}
}

// --- Homebrew (macOS) -------------------------------------------------------------------

// needBrew installs Homebrew on a Mac that lacks it (nimble installs its tools with it):
// Homebrew's official installer, in the terminal — it asks for an admin's password, and
// installs Xcode's Command Line Tools (git among them) when they are missing. Homebrew's
// bin is in nimble's PATH from the start (SetupEnv), so brew works right after. Without a
// terminal it can't ask, and says what to do.
func needBrew() error {
	if _, err := exec.LookPath("brew"); err == nil {
		return nil
	}
	if !nimble.Interactive() {
		return errors.New("Homebrew isn't installed — install it (https://brew.sh), then nimble update")
	}
	nimble.Step("Homebrew isn't installed: its official installer (brew.sh) runs now and asks for your password")
	tmp, err := os.CreateTemp("", "homebrew-install-*.sh")
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := nimble.Download("https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh", tmp.Name()); err != nil {
		return err
	}
	if err := nimble.Run("/bin/bash", tmp.Name()); err != nil {
		return fmt.Errorf("Homebrew's installer: %w", err)
	}
	if _, err := exec.LookPath("brew"); err != nil {
		return errors.New("Homebrew's installer finished, but there is no brew in " + nimble.BrewPrefix() + "/bin")
	}
	return nil
}

// Prerequisites makes sure of what nimble itself needs before it clones or creates the
// config repo: git — on a Mac through Homebrew (its installer brings the Command Line
// Tools, and /usr/bin/git is only a stub without them), on Debian from apt.
func Prerequisites() error {
	switch {
	case Darwin():
		return needBrew()
	case nimble.IsDebian():
		_, err := nimble.AptInstall("git", "ca-certificates")
		return err
	}
	return nil
}

func brew(name string, cask bool) error {
	if err := needBrew(); err != nil {
		return err
	}
	kind := "--formula"
	if cask {
		kind = "--cask"
	}
	if exec.Command("brew", "list", kind, name).Run() == nil {
		return nil
	}
	return nimble.Wait("brew install "+kind+" "+name, nimble.Cmd{Name: "brew", Args: []string{"install", kind, name}, Env: []string{"HOMEBREW_NO_ENV_HINTS=1"}}.Do)
}

func brewFormula(name string) func(*config.Pins) error {
	return func(*config.Pins) error { return brew(name, false) }
}

func brewCask(name string) func(*config.Pins) error {
	return func(*config.Pins) error { return brew(name, true) }
}

func brewRemove(name string, cask bool) func() error {
	return func() error {
		kind := "--formula"
		if cask {
			kind = "--cask"
		}
		return nimble.Cmd{Name: "brew", Args: []string{"uninstall", kind, name}}.Do()
	}
}

// --- apt (Debian) -----------------------------------------------------------------------

// aptRepo installs packages from a vendor's apt repository. In the key URL and the repo,
// {distro} is this system's ID (debian, ubuntu) and {codename} its release (trixie, noble).
func aptRepo(name, keyURL string, dearmor bool, repo string, pkgs ...string) func(*config.Pins) error {
	return func(*config.Pins) error {
		distro, _ := nimble.Distro()
		keyURL = strings.ReplaceAll(keyURL, "{distro}", distro)
		repo = strings.ReplaceAll(repo, "{distro}", distro)
		missing := false
		for _, p := range pkgs {
			if !nimble.DebInstalled(p) {
				missing = true
			}
		}
		if !missing {
			return nil
		}
		keyring := "/etc/apt/keyrings/" + name + ".gpg"
		if !nimble.Exists(keyring) {
			if dearmor {
				if _, err := nimble.AptInstall("gnupg"); err != nil {
					return err
				}
			}
			tmp, err := os.MkdirTemp("", "nimble-apt")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			key := filepath.Join(tmp, "key")
			if err := nimble.Download(keyURL, key); err != nil {
				return err
			}
			if dearmor {
				if err := (nimble.Cmd{Name: "gpg", Args: []string{"--dearmor", "-o", key + ".gpg", key}}).Do(); err != nil {
					return err
				}
				key += ".gpg"
			}
			if err := nimble.AsRoot(nimble.Cmd{Name: "install", Args: []string{"-D", "-m", "644", key, keyring}}); err != nil {
				return err
			}
		}
		suite := repo
		if strings.Contains(repo, "{codename}") {
			codename, err := vendorCodename(repo)
			if err != nil {
				return err
			}
			suite = strings.ReplaceAll(repo, "{codename}", codename)
		}
		list := "/etc/apt/sources.list.d/" + name + ".list"
		line := fmt.Sprintf("deb [arch=%s signed-by=%s] %s\n", nimble.Output("dpkg", "--print-architecture"), keyring, suite)
		if err := writeRoot(list, line, 0o644); err != nil {
			return err
		}
		if err := nimble.AsRoot(nimble.Cmd{Name: "apt-get", Args: []string{"update", "-qq"}, Quiet: true}); err != nil {
			// a source apt can't read breaks every later apt-get update: take it out again
			_ = nimble.AsRoot(nimble.Cmd{Name: "rm", Args: []string{"-f", list}})
			return fmt.Errorf("the %s apt repository doesn't work here (removed it again): %w", name, err)
		}
		_, err := nimble.AptInstall(pkgs...)
		return err
	}
}

// distroReleases lists the codenames of each distribution nimble supports, newest first —
// the fallbacks for a vendor repo that has no build for this release yet. A release newer
// than these lists falls back to all of them.
var distroReleases = map[string][]string{
	"debian": {"forky", "trixie", "bookworm"},
	"ubuntu": {"stonking", "resolute", "questing", "plucky", "oracular", "noble"},
}

// codenameCandidates is the order to try a vendor repo's suites in: this release, then the
// older ones of its distribution, newest first (every known one for a release newer than
// the list).
func codenameCandidates(distro, codename string) []string {
	known := distroReleases[distro]
	out := []string{codename}
	for i, c := range known {
		if c == codename {
			return append(out, known[i+1:]...)
		}
	}
	return append(out, known...)
}

// vendorCodename picks the codename for a vendor repo ("<url> {codename} <component>"):
// this system's, or — when the vendor has no build for it yet — the newest older release
// of the same distribution it has, which is what vendors recommend until they catch up.
func vendorCodename(repo string) (string, error) {
	distro, codename := nimble.Distro()
	base := strings.TrimSuffix(strings.Fields(repo)[0], "/") + "/dists/"
	for _, c := range codenameCandidates(distro, codename) {
		if nimble.URLExists(base+c+"/Release") || nimble.URLExists(base+c+"/InRelease") {
			if c != codename {
				nimble.Warn(fmt.Sprintf("%s has no packages for %s yet — using %s's", base, nimble.DistroName(), c))
			}
			return c, nil
		}
	}
	return "", fmt.Errorf("%s has no packages for %s", base, nimble.DistroName())
}

func aptRemove(pkgs ...string) func() error {
	return func() error {
		return nimble.AsRoot(nimble.Cmd{Name: "apt-get", Args: append([]string{"purge", "-y", "-qq", "--autoremove"}, pkgs...),
			Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Quiet: true})
	}
}

// --- npm, installers --------------------------------------------------------------------

func npmRemove(pkg string) func() error {
	return func() error {
		p, err := config.LoadPins()
		if err != nil {
			return err
		}
		return nimble.Cmd{Name: filepath.Join(nodeBin(p), "npm"), Env: npmEnv(p), Args: []string{"uninstall", "-g", pkg}}.Do()
	}
}

// ErrNoRemove says why a tool without Remove stays installed.
var ErrNoRemove = errors.New("nimble doesn't uninstall it; it only stops managing it")

// brewUnlessPresent installs a Homebrew package unless its command is there already
// (installed another way, e.g. from a tap), so nothing ends up twice.
func brewUnlessPresent(cmd, pkg string, cask bool) func(*config.Pins) error {
	return func(*config.Pins) error {
		if _, err := exec.LookPath(cmd); err == nil {
			return nil
		}
		return brew(pkg, cask)
	}
}

// latestTag is a GitHub repo's newest release tag.
func latestTag(repo string) (string, error) {
	tags, err := releases.GitHubReleases(repo)
	if err != nil || len(tags) == 0 {
		return "", fmt.Errorf("no release of %s found: %v", repo, err)
	}
	return tags[0], nil
}

// limaDebian: Lima's release into ~/.local (bin/, share/lima), with the QEMU it runs VMs in.
func limaDebian(*config.Pins) error {
	if _, err := exec.LookPath("limactl"); err == nil {
		return nil
	}
	return limaRelease()
}

func limaDebianUpdate(*config.Pins) error {
	if tag, err := latestTag("lima-vm/lima"); err != nil || nimble.Field(nimble.Output("limactl", "--version"), 2) == strings.TrimPrefix(tag, "v") {
		return nil
	}
	return limaRelease()
}

func limaRelease() error {
	pkgs := map[string][]string{"amd64": {"qemu-system-x86", "qemu-utils"}, "arm64": {"qemu-system-arm", "qemu-efi-aarch64", "qemu-utils"}}[runtime.GOARCH]
	if _, err := nimble.AptInstall(pkgs...); err != nil {
		return err
	}
	tag, err := latestTag("lima-vm/lima")
	if err != nil {
		return err
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	url := fmt.Sprintf("https://github.com/lima-vm/lima/releases/download/%s/lima-%s-Linux-%s.tar.gz", tag, strings.TrimPrefix(tag, "v"), arch)
	return nimble.Wait("lima "+tag+" → ~/.local", func() error {
		tmp, err := os.MkdirTemp("", "nimble-lima")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := nimble.Download(url, filepath.Join(tmp, "lima.tar.gz")); err != nil {
			return err
		}
		local := filepath.Join(nimble.Home(), ".local")
		if err := os.MkdirAll(local, 0o755); err != nil {
			return err
		}
		return nimble.Cmd{Name: "tar", Args: []string{"-xzf", filepath.Join(tmp, "lima.tar.gz"), "-C", local}}.Do()
	})
}

// taskDebian: Task's release binary into ~/.local/bin.
func taskDebian(*config.Pins) error {
	if _, err := exec.LookPath("task"); err == nil {
		return nil
	}
	return taskRelease()
}

func taskDebianUpdate(*config.Pins) error {
	f := strings.Fields(nimble.Output("task", "--version"))
	if tag, err := latestTag("go-task/task"); err != nil || len(f) == 0 || strings.TrimPrefix(f[len(f)-1], "v") == strings.TrimPrefix(tag, "v") {
		return nil
	}
	return taskRelease()
}

func taskRelease() error {
	tag, err := latestTag("go-task/task")
	if err != nil {
		return err
	}
	return nimble.Wait("task "+tag+" → ~/.local/bin", func() error {
		return installFromTarball(fmt.Sprintf("https://github.com/go-task/task/releases/download/%s/task_linux_%s.tar.gz", tag, runtime.GOARCH), 0, "task")
	})
}

func npmPackage(pkg, bin string) func(*config.Pins) error {
	return func(p *config.Pins) error {
		if nimble.Exists(filepath.Join(nodeBin(p), bin)) {
			return nil
		}
		return nimble.Wait("npm install -g "+pkg, nimble.Cmd{Name: filepath.Join(nodeBin(p), "npm"), Env: npmEnv(p),
			Args: []string{"install", "-g", "--no-fund", "--no-audit", "--loglevel=error", pkg}}.Do)
	}
}

// claudeCode runs Claude Code's official installer (it installs into ~/.local/bin).
func claudeCode(*config.Pins) error {
	if _, err := exec.LookPath("claude"); err == nil {
		return nil
	}
	return nimble.Wait("Claude Code (claude.ai/install.sh)", func() error {
		tmp, err := os.CreateTemp("", "claude-install-*.sh")
		if err != nil {
			return err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := nimble.Download("https://claude.ai/install.sh", tmp.Name()); err != nil {
			return err
		}
		return nimble.Cmd{Name: "bash", Args: []string{tmp.Name()}, Quiet: true}.Do()
	})
}

// firebaseUpdate keeps the firebase-tools npm package on its newest release.
func firebaseUpdate(p *config.Pins) error {
	npm := filepath.Join(nodeBin(p), "npm")
	cmd := exec.Command(npm, "view", "firebase-tools", "version")
	cmd.Env = append(os.Environ(), npmEnv(p)...)
	out, err := cmd.Output()
	newest := strings.TrimSpace(string(out))
	if err != nil || newest == "" || nimble.Output(filepath.Join(nodeBin(p), "firebase"), "--version") == newest {
		return nil
	}
	return nimble.Wait("npm install -g firebase-tools@"+newest, nimble.Cmd{Name: npm, Env: npmEnv(p), Args: []string{"install", "-g", "--no-fund", "--no-audit", "--loglevel=error", "firebase-tools@" + newest}}.Do)
}

// UpdateTools runs the own update of each wanted tool that stays current (nimble update);
// a failure is a warning, the others still update.
func UpdateTools(names []string) {
	p, err := config.LoadPins()
	if err != nil {
		return
	}
	for _, t := range Catalog {
		for _, n := range names {
			if t.Name == n && t.Update != nil && t.Installed() {
				if err := t.Update(p); err != nil {
					nimble.Warn(t.Name + " update: " + err.Error())
				}
			}
		}
	}
}
