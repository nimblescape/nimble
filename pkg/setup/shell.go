package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

const (
	launcherPath = "/usr/local/bin/nimble-shell"
	guardPath    = "/usr/local/bin/nimble-apt-guard"
	guardConf    = "/etc/apt/apt.conf.d/50nimble-zsh-guard"
)

// launcher is the login shell for `shell: zsh` on Debian: zsh when it works, else bash,
// so logging in never fails — even after zsh was removed.
const launcher = `#!/bin/sh
# nimble: login shell for "shell: zsh" — zsh if it works, else bash, so logging in never fails.
case "$0" in -*) login=-l ;; *) login= ;; esac
if /usr/bin/zsh -fc true 2>/dev/null; then
  SHELL=/usr/bin/zsh; export SHELL; exec /usr/bin/zsh $login "$@"
fi
SHELL=/bin/bash; export SHELL; exec /bin/bash $login "$@"
`

// guard refuses an apt transaction that removes zsh while nimble-shell is someone's login
// shell (apt hands its planned actions to the hook, protocol version 2).
const guard = `#!/bin/sh
# nimble: refuse to remove zsh while it is a login shell through nimble-shell.
users=$(awk -F: '$7 == "/usr/local/bin/nimble-shell" { print $1 }' /etc/passwd | tr '\n' ' ')
[ -n "$users" ] || exit 0
while read -r line; do
  case "$line" in
    "zsh "*"**REMOVE**")
      echo "nimble: zsh is the login shell of: $users(shell: zsh in its nimble template)." >&2
      echo "nimble: switch to bash first: nimble host set <host> shell=bash, then nimble update." >&2
      exit 1 ;;
  esac
done
exit 0
`

const guardAptConf = `// nimble: keep zsh while it is a login shell (see /usr/local/bin/nimble-apt-guard)
DPkg::Pre-Install-Pkgs { "/usr/local/bin/nimble-apt-guard"; };
DPkg::Tools::Options::/usr/local/bin/nimble-apt-guard::Version "2";
`

// writeRoot installs a file as root (directly when nimble is root, else through sudo).
func writeRoot(path, content string, mode os.FileMode) error {
	if nimble.ReadTrim(path) == strings.TrimSpace(content) {
		return nil
	}
	tmp, err := os.CreateTemp("", "nimble-root")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		return err
	}
	tmp.Close()
	return nimble.AsRoot(nimble.Cmd{Name: "install", Args: []string{"-D", "-m", fmt.Sprintf("%o", mode), tmp.Name(), path}})
}

func inEtcShells(shell string) bool {
	for _, l := range nimble.Lines(nimble.ReadTrim("/etc/shells")) {
		if strings.TrimSpace(l) == shell {
			return true
		}
	}
	return false
}

func addEtcShells(shell string) error {
	if inEtcShells(shell) {
		return nil
	}
	return nimble.AsRoot(nimble.Cmd{Name: "sh", Args: []string{"-c", "echo " + shell + " >> /etc/shells"}})
}

func chsh(shell string) error {
	nimble.Say("login shell: " + nimble.LoginShell() + " → " + shell)
	if nimble.IsDebian() {
		return nimble.AsRoot(nimble.Cmd{Name: "chsh", Args: []string{"-s", shell, nimble.CurrentUser()}})
	}
	return nimble.Run("chsh", "-s", shell) // macOS asks for the password itself
}

// LoginShell makes the login shell what the template (or host file) says. "" keeps the
// system default and changes nothing, unless nimble had set zsh before.
func LoginShell(want string) error {
	cur := nimble.LoginShell()
	switch {
	case Darwin():
		target := ""
		switch want {
		case "zsh":
			target = "/bin/zsh"
		case "bash":
			// Homebrew's bash, and the bash-completion that loads its formulae's completions
			for _, formula := range []string{"bash", "bash-completion@2"} {
				if err := brew(formula, false); err != nil {
					return err
				}
			}
			target = filepath.Join(nimble.BrewPrefix(), "bin", "bash")
			if !nimble.Exists(target) {
				return fmt.Errorf("Homebrew's bash isn't at %s", target)
			}
		}
		if target == "" || cur == target {
			return nil
		}
		if err := addEtcShells(target); err != nil {
			return err
		}
		return chsh(target)

	case nimble.IsDebian():
		if want == "zsh" {
			if _, err := nimble.AptInstall("zsh"); err != nil {
				return err
			}
			for _, f := range []struct {
				path, body string
				mode       os.FileMode
			}{{launcherPath, launcher, 0o755}, {guardPath, guard, 0o755}, {guardConf, guardAptConf, 0o644}} {
				if err := writeRoot(f.path, f.body, f.mode); err != nil {
					return err
				}
			}
			if err := addEtcShells(launcherPath); err != nil {
				return err
			}
			if cur != launcherPath {
				return chsh(launcherPath)
			}
			return nil
		}
		// bash, or the default after nimble had set zsh: back to Debian's bash — with
		// bash-completion, which minimal images lack (git, ssh, systemctl, … complete with it).
		// Only where sudo works: apply runs as the user, and a missing completion mustn't stop it.
		if _, err := nimble.AptInstall("bash-completion"); err != nil {
			nimble.Warn("bash-completion not installed (" + err.Error() + ") — sudo apt install bash-completion")
		}
		if want == "bash" && strings.HasSuffix(cur, "/bash") {
			return nil
		}
		if cur == launcherPath || (want == "bash" && cur != "/bin/bash") {
			return chsh("/bin/bash")
		}
	}
	return nil
}

// LauncherFallback reports whether the zsh launcher would fall back to bash right now.
func LauncherFallback() bool {
	return exec.Command("/usr/bin/zsh", "-fc", "true").Run() != nil
}
