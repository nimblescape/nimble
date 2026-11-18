package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/harden"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [host…|all]",
		Short: "Check shell setup, linked config, keys and hardening",
		Long: heredoc.Doc(`
			Check that this machine is set up the way the config repo describes it: the
			managed dotfiles, the linked config, git identity; on a workstation the daily
			keys (and how long they stay valid), Touch ID for sudo and the ssh config; on a
			Debian host the hardening and sudo with a hardware key. With hosts, run the same
			on each of them.
		`),
		Example:           "  nimble doctor\n  nimble doctor all",
		ValidArgsFunction: hosts.Complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			return hosts.LocalOrRemote(args, "doctor", local)
		},
	}
}

func local() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	r := &nimble.Report{}
	h := nimble.Home()

	nimble.Header("Config repo")
	if errs := config.Validate(setup.KnownTools()); len(errs) == 0 {
		r.OK("YAML files", "valid")
	} else {
		for _, e := range errs {
			r.Bad("invalid", e.Error())
		}
	}

	nimble.Header("Shell")
	r.OK("login shell", nimble.LoginShell())
	var hp *config.Host
	if self, ok := hosts.Self(""); ok {
		hp = &self
	}
	res, _ := config.Resolve(c, hp)
	zsh := setup.ZshFiles(res.Shell) // apply writes zsh's dotfiles only where zsh is
	for _, f := range []string{".zshenv", ".zprofile", ".zshrc", ".bashrc", ".profile"} {
		if strings.HasPrefix(f, ".z") && !zsh {
			continue
		}
		if strings.HasPrefix(nimble.ReadTrim(filepath.Join(h, f)), nimble.StubMark) {
			r.OK("~/"+f, "managed by nimble")
		} else {
			r.Bad("~/"+f, "not managed by nimble — nimble apply")
		}
	}
	if strings.HasSuffix(nimble.LoginShell(), "/nimble-shell") {
		if setup.LauncherFallback() {
			r.Bad("zsh", "missing or broken: the login shell falls back to bash — nimble apply reinstalls it")
		} else {
			r.OK("zsh", "the launcher starts zsh")
		}
	}

	nimble.Header("Linked config")
	for _, l := range c.Links {
		if l.OS != "" && l.OS != config.OS() {
			continue
		}
		dst := filepath.Join(h, l.To)
		t, err := os.Readlink(dst)
		switch {
		case err == nil && t == l.Source():
			r.OK("~/"+l.To, "")
		case nimble.Exists(dst):
			r.Bad("~/"+l.To, "a separate file now (an app replaced the link?) — compare, then nimble apply")
		default:
			r.Bad("~/"+l.To, "missing — nimble apply")
		}
	}
	nimble.Header("Git")
	if email := nimble.Output("git", "config", "--get", "user.email"); email != "" {
		r.OK("identity", nimble.Output("git", "config", "--get", "user.name")+" <"+email+">")
	} else {
		r.Note("identity", "not set (personal layer: personal/git/config)")
	}

	switch {
	case setup.Darwin():
		mac(r, c)
	case nimble.IsDebian():
		debian(r)
	}
	return r.Done("see the hints above")
}

func mac(r *nimble.Report, c *config.Config) {
	nimble.Header("Workstation")
	labels := map[string]bool{}
	for _, d := range c.Domains {
		labels[d.Label] = true
	}
	var sorted []string
	for label := range labels {
		sorted = append(sorted, label)
	}
	sort.Strings(sorted)
	for _, label := range sorted {
		handle := keys.HandlePath(label)
		e, ok := keys.DailyKey(handle) // the key behind the handle, whatever else carries the label
		switch {
		case !nimble.Exists(handle):
			r.Bad("daily key "+label, "no key handle in ~/.ssh — nimble key enclave")
		case !ok:
			r.Bad("daily key "+label, "its key is missing in the Secure Enclave — nimble key enclave")
		case !e.Valid:
			r.Bad("daily key "+label, "expired — nimble key enclave, then nimble update all")
		case !e.ValidTo.IsZero() && time.Until(e.ValidTo) < 30*24*time.Hour:
			r.Bad("daily key "+label, "expires "+e.ValidTo.Format("2006-01-02")+" — nimble key enclave, then nimble update all")
		default:
			valid := ""
			if !e.ValidTo.IsZero() {
				valid = ", valid until " + e.ValidTo.Format("2006-01-02")
			}
			r.OK("daily key "+label, "Secure Enclave"+valid)
		}
		if nimble.Exists(handle + ".old") {
			r.Note("old daily key "+label, "kept for hosts that don't have the new one yet — after nimble update all: nimble key enclave --drop-old")
		}
	}
	backups := 0
	for _, k := range keys.Repo() {
		if k.Kind == "yubikey" {
			backups++
		}
	}
	if backups == 0 {
		r.Note("YubiKey backups", "none registered — nimble key yubikey")
	} else {
		r.OK("YubiKey backups", strings.Repeat("●", backups))
	}
	ssh, _, fido := keys.FIDOSSH()
	_, ykman := exec.LookPath("ykman")
	switch {
	case !fido:
		r.Bad("ssh", ssh+" can't use YubiKeys (macOS's own has no FIDO support) — nimble apply")
	case ykman != nil:
		r.Bad("ykman", "missing — nimble apply")
	default:
		r.OK("ssh", ssh+": Secure Enclave and YubiKeys")
	}
	if regexp.MustCompile(`(?m)^auth\s.*pam_tid\.so`).MatchString(nimble.ReadTrim("/etc/pam.d/sudo_local")) {
		r.OK("Touch ID for sudo", "on")
		// iTerm2 keeps sessions alive across logouts by default, which hides the Touch ID prompt
		iterm := nimble.Exists("/Applications/iTerm.app") || nimble.Exists(filepath.Join(nimble.Home(), "Applications", "iTerm.app"))
		if iterm && nimble.Output("defaults", "read", "com.googlecode.iterm2", "BootstrapDaemon") != "0" {
			r.Note("Touch ID in iTerm2", "turn off Settings → Advanced → 'Allow sessions to survive logging out and back in', then quit and reopen iTerm2")
		}
	} else {
		r.Bad("Touch ID for sudo", "off — run "+nimble.Tilde(nimble.RepoFile("macos", "touchid-sudo.sh")))
	}
	if strings.Contains(nimble.ReadTrim(filepath.Join(nimble.Home(), ".ssh", "config")), "Include "+nimble.Tilde(nimble.CacheDir("ssh_config"))) {
		r.OK("ssh config", "includes the hosts from hosts/")
	} else {
		r.Bad("ssh config", "doesn't include nimble's host entries — nimble apply")
	}
	if nimble.AgentHasTrustedKey() {
		r.OK("SSH agent", "has a trusted key (forwarded to the hosts)")
	} else {
		r.Note("SSH agent", "no trusted key loaded yet — added on the next ssh login")
	}
}

func debian(r *nimble.Report) {
	u := nimble.CurrentUser()
	level := "full"
	allowed := map[string]bool{u: true}
	if h, ok := hosts.Self(""); ok {
		level = h.HardenLevel()
		for _, a := range h.SudoAllowed {
			allowed[a] = true
		}
	}
	nimble.Header("Host hardening (" + level + ")")
	if level == "sudo" {
		debianSudo(r, u)
		return
	}
	if nimble.Exists("/etc/ssh/sshd_config.d/10-nimble.conf") {
		r.OK("sshd", "no root login, keys only")
	} else {
		r.Bad("sshd", "not hardened — nimble init")
	}
	pamRssh(r)
	if nimble.Exists("/etc/security/authorized_keys/" + u + ".keys") {
		r.OK("sudo keys", "/etc/security/authorized_keys/"+u+".keys")
	} else {
		r.Bad("sudo keys", "missing — nimble init")
	}
	members, admin := "", false
	var others []string
	if f := strings.Split(nimble.Output("getent", "group", "sudo"), ":"); len(f) >= 4 {
		members = f[3]
		for _, m := range strings.Split(members, ",") {
			switch {
			case m == u:
				admin = true
			case m != "" && !allowed[m]:
				others = append(others, m)
			}
		}
	}
	switch {
	case !admin:
		r.Bad("sudo group", u+" isn't a member (members: "+members+") — nimble init")
	case len(others) > 0:
		r.Bad("sudo group", "also: "+strings.Join(others, ", ")+" (expected only "+u+" and the host file's sudoAllowed) — nimble update")
	default:
		r.OK("sudo group", members)
	}
	if st := nimble.Field(nimble.Output("sudo", "-n", "passwd", "-S", "root"), 1); st != "" {
		if st == "L" {
			r.OK("root password", "locked")
		} else {
			r.Bad("root password", "not locked — nimble init")
		}
	} else {
		r.Note("root password", "not checked (needs sudo — run `sudo -v` first)")
	}
	if os.Getenv("SSH_AUTH_SOCK") != "" && nimble.AgentHasTrustedKey() {
		r.OK("SSH agent", "forwarded — sudo is confirmed with your hardware key")
	} else {
		r.Note("SSH agent", "not forwarded — sudo asks for the password (the workstation forwards it to managed hosts by itself; from elsewhere: ssh -A)")
	}
	// the full audit reads root-only files and sshd's resolved config
	if self, err := os.Executable(); err == nil && exec.Command("sudo", "-n", "true").Run() == nil {
		if err := nimble.Run("sudo", "-n", self, "harden", "--check", "--user", u); err != nil {
			r.Bad("audit", "see above")
		}
	} else {
		r.Note("audit", "checking the effective config needs root: sudo nimble harden --check")
	}
}

// pamRssh checks that the module is installed and sudo's PAM file has an active line for it.
func pamRssh(r *nimble.Report) {
	mods, _ := filepath.Glob("/usr/lib/*/security/libpam_rssh.so")
	if len(mods) == 0 {
		mods, _ = filepath.Glob("/lib/*/security/libpam_rssh.so")
	}
	if len(mods) > 0 && harden.PamLine(nimble.ReadTrim("/etc/pam.d/sudo"), "libpam_rssh.so") >= 0 {
		r.OK("sudo with hardware key", "pam_rssh enabled")
	} else {
		r.Bad("sudo with hardware key", "not set up — nimble init")
	}
}

// debianSudo checks a host hardened at level sudo: only sudo with the hardware key.
func debianSudo(r *nimble.Report, u string) {
	pamRssh(r)
	if nimble.Exists("/etc/security/authorized_keys/" + u + ".keys") {
		r.OK("sudo keys", "/etc/security/authorized_keys/"+u+".keys")
	} else {
		r.Bad("sudo keys", "missing — nimble init")
	}
	if os.Getenv("SSH_AUTH_SOCK") != "" && nimble.AgentHasTrustedKey() {
		r.OK("SSH agent", "forwarded — sudo is confirmed with your hardware key")
	} else {
		r.Note("SSH agent", "not forwarded — sudo asks for the password (the workstation forwards it to managed hosts by itself)")
	}
	r.Note("rest", "administered by others (harden: sudo): sshd, root and other sudoers stay as they are")
}
