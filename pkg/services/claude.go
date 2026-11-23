// Package services runs long-lived services for repos on a Debian host. The claude
// service is a Claude Code Remote Control server for one repo: a dedicated service user
// without SSH login, its own Claude Code and GitHub login, the repo checked out in its
// home, one systemd unit per repo.
package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

const (
	svcUser    = "claude"
	svcHome    = "/home/claude"
	sshdDeny   = "/etc/ssh/sshd_config.d/90-nimble-deny-claude.conf"
	unitPrefix = "claude-"
)

// Unit is the systemd unit of a repo's Claude Code server.
func Unit(repo string) string {
	return unitPrefix + strings.ReplaceAll(repo, "/", "-") + ".service"
}

// Checkout is where the service user keeps the repo.
func Checkout(repo string) string { return filepath.Join(svcHome, repo) }

func unitPath(repo string) string { return "/etc/systemd/system/" + Unit(repo) }

// Installed reports whether the repo's server is set up on this host.
func Installed(repo string) bool { return nimble.Exists(unitPath(repo)) }

// Active reports whether the repo's server runs right now.
func Active(repo string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", Unit(repo)).Run() == nil
}

// State is "enabled", "disabled" or "not installed".
func State(repo string) string {
	if !Installed(repo) {
		return "not installed"
	}
	if exec.Command("systemctl", "is-enabled", "--quiet", Unit(repo)).Run() == nil {
		return "enabled"
	}
	return "disabled"
}

func needRoot() error {
	if os.Geteuid() != 0 || !nimble.IsDebian() {
		return errors.New("services run on Debian hosts, as root (sudo nimble service …)")
	}
	return nil
}

func run(name string, args ...string) error {
	return nimble.Cmd{Name: name, Args: args, Quiet: true}.Do()
}

// asSvc runs a command as the service user with its login environment; interactive ones
// get the terminal.
func asSvc(interactive bool, args ...string) error {
	c := nimble.Cmd{Name: "sudo", Args: append([]string{"-u", svcUser, "-H", "-i"}, args...), Quiet: !interactive}
	if interactive {
		return nimble.Run(c.Name, c.Args...)
	}
	return c.Do()
}

func svcOK(args ...string) bool {
	return exec.Command("sudo", append([]string{"-u", svcUser, "-H", "-i"}, args...)...).Run() == nil
}

// Install sets the repo's server up: the service user, Claude Code, the logins (asked for
// once, interactively), the checkout and the unit. admin gets read access to the checkout.
func Install(repo, admin string) error {
	if err := needRoot(); err != nil {
		return err
	}
	if _, err := exec.LookPath("gh"); err != nil { // before anything is set up: the login and the clone need it
		return errors.New("gh is needed on the host first: nimble tool select --host <host> (check gh)")
	}
	// 1. the service user: locked password, never logs in over SSH
	if _, err := user.Lookup(svcUser); err != nil {
		nimble.Say("user " + svcUser + " (locked password, no SSH login)")
		if err := run("useradd", "--create-home", "--home-dir", svcHome, "--shell", "/bin/bash", "--user-group", svcUser); err != nil {
			return err
		}
	}
	if err := run("passwd", "-l", svcUser); err != nil {
		return err
	}
	if err := os.Chmod(svcHome, 0o750); err != nil {
		return err
	}
	if nimble.ReadTrim(sshdDeny) == "" && !denied(svcUser) {
		if err := os.WriteFile(sshdDeny, []byte("# managed by nimble: the claude service account never logs in over SSH\nDenyUsers "+svcUser+"\n"), 0o644); err != nil {
			return err
		}
		if err := run("sshd", "-t"); err != nil {
			_ = os.Remove(sshdDeny)
			return fmt.Errorf("sshd rejects the DenyUsers line: %w", err)
		}
		_ = run("systemctl", "reload", "ssh")
	}

	// 2. Claude Code, the service user's own (it updates itself)
	if !nimble.Exists(filepath.Join(svcHome, ".local/bin/claude")) {
		if err := nimble.Wait("Claude Code for "+svcUser, func() error {
			f, err := os.CreateTemp("", "nimble-claude-install-*.sh")
			if err != nil {
				return err
			}
			tmp := f.Name()
			f.Close()
			defer os.Remove(tmp)
			if err := nimble.Download("https://claude.ai/install.sh", tmp); err != nil {
				return err
			}
			if err := os.Chmod(tmp, 0o755); err != nil { // the service user runs it
				return err
			}
			return asSvc(false, "bash", tmp)
		}); err != nil {
			return err
		}
	}

	// 3. read access for the admin (VS Code over SSH), git identity, settings
	if admin != "" {
		_ = run("usermod", "-aG", svcUser, admin)
		for _, k := range []string{"user.name", "user.email"} {
			if v := nimble.Output("sudo", "-u", admin, "-H", "git", "config", "--global", "--get", k); v != "" {
				_ = asSvc(false, "git", "config", "--global", k, v)
			}
		}
		if err := settings(admin); err != nil {
			nimble.Warn("Claude settings: " + err.Error())
		}
	}

	// 4. logins, asked for once: GitHub (a fine-grained token for the repos it works on), Claude
	if !svcOK("gh", "auth", "status") {
		nimble.Step("GitHub login for " + svcUser + ": paste a fine-grained token limited to " + repo + " (and other repos it serves)")
		token, err := nimble.Secret("Token (not shown):")
		if err != nil {
			return err
		}
		c := exec.Command("sudo", "-u", svcUser, "-H", "-i", "gh", "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--with-token")
		c.Stdin = strings.NewReader(token)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("gh auth login: %w", err)
		}
		if err := asSvc(false, "gh", "auth", "setup-git"); err != nil {
			return err
		}
	}
	if !claudeLoggedIn() {
		nimble.Step("Claude login for " + svcUser + ": choose claude.ai, open the URL, paste the code")
		if err := asSvc(true, "claude", "auth", "login"); err != nil {
			return err
		}
	}

	// 5. the checkout
	dir := Checkout(repo)
	if !nimble.Exists(filepath.Join(dir, ".git")) {
		if err := nimble.Wait("clone "+repo+" → "+dir, func() error {
			if err := asSvc(false, "mkdir", "-p", filepath.Dir(dir)); err != nil {
				return err
			}
			return asSvc(false, "gh", "repo", "clone", repo, dir, "--", "--quiet")
		}); err != nil {
			return err
		}
		exclude := filepath.Join(dir, ".git", "info", "exclude")
		if !strings.Contains(nimble.ReadTrim(exclude), ".claude/worktrees/") {
			_ = asSvc(false, "sh", "-c", "printf '# Claude Code session worktrees\\n.claude/worktrees/\\n' >> "+exclude)
		}
	}
	if admin != "" {
		for _, d := range []string{dir, dir + "/*"} {
			if !strings.Contains(nimble.Output("sudo", "-u", admin, "-H", "git", "config", "--global", "--get-all", "safe.directory"), d) {
				_ = run("sudo", "-u", admin, "-H", "git", "config", "--global", "--add", "safe.directory", d)
			}
		}
	}

	// 6. a server that already runs in this checkout (set up before nimble) is replaced,
	// never doubled; its folder is trusted already
	adopted := false
	for _, u := range otherServers("/etc/systemd/system", dir, Unit(repo)) {
		nimble.Warn(u + " already runs a Claude Code server in " + dir)
		if !nimble.Interactive() || !nimble.Confirm("Replace "+u+" with nimble's "+Unit(repo)+"?", true) {
			return fmt.Errorf("%s runs a server in %s — two would conflict; stop it or let nimble replace it", u, dir)
		}
		if err := run("systemctl", "disable", "--now", u); err != nil {
			return err
		}
		nimble.Say("stopped and disabled " + u + " (its unit file stays; delete it once nimble's runs)")
		adopted = true
	}

	// 7. the first run trusts the folder and connects; then the unit takes over
	if !Installed(repo) && !adopted {
		nimble.Step("first run: trust the folder, answer y, wait for 'Connected', then press Ctrl+C")
		ctrlC := make(chan os.Signal, 1)
		signal.Notify(ctrlC, os.Interrupt) // Ctrl+C ends the first run, not nimble
		_ = asSvc(true, "bash", "-c", "cd '"+dir+"' && claude remote-control --spawn=worktree")
		signal.Stop(ctrlC)
	}
	if err := os.WriteFile(unitPath(repo), []byte(unit(repo)), 0o644); err != nil {
		return err
	}
	return run("systemctl", "daemon-reload")
}

// otherServers lists systemd units (not own) that run claude remote-control in dir.
func otherServers(unitDir, dir, own string) []string {
	files, _ := filepath.Glob(filepath.Join(unitDir, "*.service"))
	var out []string
	for _, f := range files {
		b := nimble.ReadTrim(f)
		if filepath.Base(f) != own && strings.Contains(b, "remote-control") &&
			strings.Contains("\n"+b+"\n", "\nWorkingDirectory="+dir+"\n") {
			out = append(out, filepath.Base(f))
		}
	}
	return out
}

// denied reports whether an sshd drop-in already denies the user.
func denied(u string) bool {
	files, _ := filepath.Glob("/etc/ssh/sshd_config.d/*.conf")
	for _, f := range files {
		for _, l := range nimble.Lines(nimble.ReadTrim(f)) {
			if fs := strings.Fields(l); len(fs) > 1 && strings.EqualFold(fs[0], "DenyUsers") {
				for _, x := range fs[1:] {
					if x == u {
						return true
					}
				}
			}
		}
	}
	return false
}

// claudeLoggedIn reads `claude auth status` (JSON) of the service user.
func claudeLoggedIn() bool {
	out := strings.ReplaceAll(nimble.Output("sudo", "-u", svcUser, "-H", "-i", "claude", "auth", "status"), " ", "")
	return strings.Contains(out, `"loggedIn":true`)
}

func unit(repo string) string {
	return fmt.Sprintf(`# managed by nimble (nimble service … claude %[1]s)
[Unit]
Description=Claude Code Remote Control server (%[1]s, user %[2]s)
Documentation=https://code.claude.com/docs/en/remote-control
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%[2]s
Group=%[2]s
WorkingDirectory=%[3]s
Environment=PATH=%[4]s/.local/bin:/usr/local/bin:/usr/bin:/bin
Environment=TERM=xterm-256color
# worktree: each remotely started session gets its own git worktree. The server also
# creates one session in the main checkout at start; keep it, or the server archives all
# sessions on stop and none reconnect after a restart.
ExecStart=%[4]s/.local/bin/claude remote-control --spawn=worktree
# The server repaints its status about once a second; keep that out of the journal.
StandardOutput=null
StandardError=journal
# It exits by itself after ~10 minutes without network; bring it back.
Restart=always
RestartSec=15
# SIGINT = graceful stop (sessions are kept and reconnect on next start).
KillSignal=SIGINT
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`, repo, svcUser, Checkout(repo), svcHome)
}

// settings gives the service user the admin's Claude settings (once), keeping local
// transcripts ~10 years so archiving a session stays a manual decision.
func settings(admin string) error {
	dst := filepath.Join(svcHome, ".claude", "settings.json")
	if nimble.Exists(dst) {
		return nil
	}
	u, err := user.Lookup(admin)
	if err != nil {
		return err
	}
	d := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(u.HomeDir, ".claude", "settings.json")); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	d["cleanupPeriodDays"] = 3650
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := asSvc(false, "mkdir", "-p", filepath.Dir(dst)); err != nil {
		return err
	}
	c := exec.Command("sudo", "-u", svcUser, "-H", "tee", dst)
	c.Stdin = strings.NewReader(string(b) + "\n")
	return c.Run()
}

// Enable starts the server and starts it at boot.
func Enable(repo string) error {
	if err := needRoot(); err != nil {
		return err
	}
	if !Installed(repo) {
		return errors.New(repo + " isn't installed here — nimble service add claude " + repo)
	}
	nimble.Say("enable " + Unit(repo))
	return run("systemctl", "enable", "--now", Unit(repo))
}

// Disable stops the server and keeps it installed (sessions reconnect on the next start).
func Disable(repo string) error {
	if err := needRoot(); err != nil {
		return err
	}
	if !Installed(repo) {
		return nil
	}
	nimble.Say("disable " + Unit(repo))
	return run("systemctl", "disable", "--now", Unit(repo))
}

// Remove uninstalls the server: unit and checkout. A checkout with uncommitted or unpushed
// work stays unless force.
func Remove(repo string, force bool) error {
	if err := Disable(repo); err != nil {
		return err
	}
	if Installed(repo) {
		if err := os.Remove(unitPath(repo)); err != nil {
			return err
		}
		_ = run("systemctl", "daemon-reload")
		nimble.Say("removed " + Unit(repo))
	}
	dir := Checkout(repo)
	if !nimble.Exists(dir) {
		return nil
	}
	// a check that fails doesn't count as clean: the checkout may hold work that exists nowhere else
	git := func(args ...string) (string, bool) {
		out, err := exec.Command("sudo", append([]string{"-u", svcUser, "-H", "git", "-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	status, ok1 := git("status", "--porcelain")
	ahead, ok2 := git("log", "--oneline", "--branches", "--not", "--remotes")
	trees, ok3 := git("worktree", "list")
	clean := ok1 && ok2 && ok3 && status == "" && ahead == "" && len(nimble.Lines(trees)) <= 1
	if !clean && !force {
		nimble.Warn(dir + " has uncommitted or unpushed work or session worktrees (or can't be checked) — kept; remove it by hand, or nimble service remove … --force")
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	nimble.Say("removed " + dir)
	return nil
}
