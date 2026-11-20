// Package harden locks down a Debian host:
//
//   - root cannot log in at all: root password locked, no root login over SSH
//   - SSH accepts keys only; the keys in keys/ are authorized for the user
//   - only the user may sudo; sudo is confirmed with a hardware key through the
//     forwarded SSH agent (pam_rssh), and falls back to the user's password without one
//
// It runs as root and is safe to re-run; it also syncs added or removed keys. Which keys
// a host trusts depends on its administration domain (see package keys).
package harden

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
)

const (
	sudoKeysDir = "/etc/security/authorized_keys"         // root-owned: the user can't add sudo keys
	pamMarker   = "/usr/local/share/pam_rssh.version"     // "<version> <sha256 of the module>"
	sshdConf    = "/etc/ssh/sshd_config.d/10-nimble.conf" // before 50-cloud-init.conf; first value wins
	sudoersConf = "/etc/sudoers.d/10-nimble"
	blockBegin  = "# >>> nimble keys (managed by nimble harden) >>>"
	blockEnd    = "# <<< nimble keys <<<"
)

// Run hardens this host for the given user. as is the host's name in the inventory (""
// = the name remembered from the last run); it decides the domain and so the trusted keys.
func Run(userName, as string) error {
	if os.Geteuid() != 0 {
		return errors.New("needs root: sudo nimble harden")
	}
	if !nimble.IsDebian() {
		return errors.New("not a Debian or Ubuntu host")
	}
	u, err := user.Lookup(userName)
	if err != nil {
		return fmt.Errorf("user %q: %w", userName, err)
	}
	if !inGroup(userName, "sudo") {
		return fmt.Errorf("%s is not in the sudo group", userName)
	}
	// the password is the sudo fallback without a forwarded agent — without it, locking root could lock you out
	if nimble.Field(nimble.Output("passwd", "-S", userName), 1) != "P" {
		return fmt.Errorf("%s has no password; set one first: sudo passwd %s", userName, userName)
	}
	repo := filepath.Join(u.HomeDir, ".nimble")
	os.Setenv("NIMBLE_HOME", repo) // the user's config: its host file, keys, domains
	h, d, err := thisHost(as)
	if err != nil {
		return err
	}
	var trusted []string
	for _, k := range keys.Trusted(d) {
		trusted = append(trusted, k.Line())
	}
	if len(trusted) == 0 {
		return fmt.Errorf("no trusted keys for domain %s in %s/keys", d.Name, repo)
	}
	nimble.Step("domain " + d.Name + ": " + fmt.Sprint(len(trusted)) + " trusted key(s)")

	level := h.HardenLevel()
	if level == "sudo" { // a host others administer: only sudo with the hardware key
		nimble.Step("harden: sudo only (hosts/ file: harden: sudo)")
		if err := pamRssh(); err != nil {
			return err
		}
		if err := sudoKeys(userName, trusted); err != nil {
			return err
		}
		return report(AuditLevel(userName, nil, level))
	}
	if err := authorizedKeys(u, trusted); err != nil { // 1. SSH login keys
		return err
	}
	if err := pamRssh(); err != nil { // 2. the module (built before anything else changes)
		return err
	}
	allowed := h.SudoAllowed
	if err := sudo(userName, trusted, allowed); err != nil { // 3. sudo via a hardware key, only this user (and sudoAllowed)
		return err
	}
	if nimble.Field(nimble.Output("passwd", "-S", "root"), 1) != "L" { // 4. root
		nimble.Say("lock the root password (no console login, no su)")
		if err := (nimble.Cmd{Name: "passwd", Args: []string{"-l", "root"}, Quiet: true}).Do(); err != nil {
			return err
		}
	}
	if err := sshd(); err != nil { // 5. no root login, keys only
		return err
	}
	if err := rootKeys(); err != nil { // 6. root's authorized_keys, unused from here on
		return err
	}
	return report(Audit(userName, allowed)) // 7. check the effective config, whatever wrote it
}

// rootKeys offers to remove /root/.ssh/authorized_keys: with root login off its keys are
// unused, but they would work again the moment someone allows root login (cloud images
// often put the default user's key there too). The admin's key login was checked before
// harden runs, so this can't lock anyone out. Without a terminal, or declined, the file
// stays and the audit reports it.
func rootKeys() error {
	ask := func() bool {
		return nimble.Interactive() && nimble.Confirm("Remove it? (a backup goes to /var/backups/nimble)", true)
	}
	return removeRootKeys("/root/.ssh/authorized_keys", "/var/backups/nimble", ask)
}

// removeRootKeys removes path if the user agrees, after a backup in backups (the first
// original is kept: a later run never overwrites it).
func removeRootKeys(path, backups string, ask func() bool) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	nimble.Warn(path + " exists — unused while root can't log in, but its keys would work again if root login were allowed")
	if !ask() {
		return nil
	}
	backup := backupPath(path, backups)
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return err
	}
	if !nimble.Exists(backup) {
		if err := os.WriteFile(backup, b, 0o600); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	nimble.Say("removed " + path + " (backup: " + backup + ")")
	return nil
}

// Check only audits (sudo nimble harden --check).
func Check(userName, as string) error {
	if os.Geteuid() != 0 {
		return errors.New("needs root: sudo nimble harden --check")
	}
	if u, err := user.Lookup(userName); err == nil {
		os.Setenv("NIMBLE_HOME", filepath.Join(u.HomeDir, ".nimble")) // the admin's config: its host file
	}
	level, allowed := "full", []string(nil)
	if h, _, err := thisHost(as); err == nil {
		level, allowed = h.HardenLevel(), h.SudoAllowed
	}
	return report(AuditLevel(userName, allowed, level))
}

// Ready reports whether this machine can be hardened: it has a host file and its domain
// trusts at least one key (key-only SSH and sudo with a hardware key need one to trust).
// Otherwise missing says what to do first.
func Ready() (name, missing string) {
	h, ok := hosts.Self("")
	if !ok {
		return "", "this machine has no host file (nimble host create " + hosts.MachineName() + " --mode workstation)"
	}
	c, err := config.Load()
	if err != nil {
		return "", err.Error()
	}
	d, err := c.Domain(h.Domain)
	if err != nil {
		return "", err.Error()
	}
	if len(keys.Trusted(d)) == 0 {
		return "", "domain " + d.Name + " trusts no key yet (nimble key yubikey)"
	}
	return h.Name, ""
}

func report(findings []Finding) error {
	nimble.Header("Audit: the effective configuration")
	r := &nimble.Report{}
	for _, f := range findings {
		if f.OK {
			r.OK(f.Name, f.Detail)
		} else {
			r.Bad(f.Name, f.Detail)
		}
	}
	return r.Done("the host is not fully hardened — fix the lines above, then nimble update <host>")
}

// thisHost finds this host's inventory entry and its domain: the name passed with --as,
// else the one nimble apply remembered in ~/.nimble/cache/host. Root only reads there.
func thisHost(as string) (config.Host, config.Domain, error) {
	if as == "" {
		as = nimble.ReadTrim(nimble.CacheDir("host"))
	}
	h, ok := config.FindHost(as)
	if !ok {
		return h, config.Domain{}, fmt.Errorf("host %q is not in the inventory (%s) — pass --as <name>", as, nimble.RepoFile("hosts"))
	}
	c, err := config.Load()
	if err != nil {
		return h, config.Domain{}, err
	}
	d, err := c.Domain(h.Domain)
	return h, d, err
}

func inGroup(userName, group string) bool {
	for _, g := range strings.Fields(nimble.Output("id", "-nG", userName)) {
		if g == group {
			return true
		}
	}
	return false
}

// ManagedBlock replaces (or appends) nimble's block of keys in an authorized_keys file.
// Lines outside the block that authorize one of the same keys (e.g. added by hand
// before nimble) are dropped, so each key is listed once; other lines stay untouched.
func ManagedBlock(current string, keys []string) string {
	managed := map[string]bool{}
	for _, k := range keys {
		managed[keyID(k)] = true
	}
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(blockBegin) + `.*?` + regexp.QuoteMeta(blockEnd) + `\n?`)
	rest := re.ReplaceAllLiteralString(current, "")
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(rest, "\n"), "\n") {
		if l != "" && !managed[keyID(l)] {
			kept = append(kept, l)
		}
	}
	block := blockBegin + "\n" + strings.Join(keys, "\n") + "\n" + blockEnd + "\n"
	if len(kept) == 0 {
		return block
	}
	return strings.Join(kept, "\n") + "\n" + block
}

// keyID is "type base64" of an authorized_keys line ("" for comments and options-only lines).
func keyID(line string) string {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if strings.HasPrefix(f[i], "ssh-") || strings.HasPrefix(f[i], "ecdsa-") || strings.HasPrefix(f[i], "sk-") {
			return f[i] + " " + f[i+1]
		}
	}
	return ""
}

func authorizedKeys(u *user.User, keys []string) error {
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	dir := filepath.Join(u.HomeDir, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := userPath(dir, nil, 0o700, uid, gid); err != nil {
		return err
	}
	path := filepath.Join(dir, "authorized_keys")
	cur, _ := os.ReadFile(path)
	want := ManagedBlock(string(cur), keys)
	if want == string(cur) {
		return userPath(path, nil, 0o600, uid, gid)
	}
	nimble.Say(fmt.Sprintf("authorize %d key(s) for %s", len(keys), u.Username))
	return userPath(path, []byte(want), 0o600, uid, gid)
}

// userPath writes a file (data != nil) or only sets the owner and mode of a file or folder
// in the user's home, as root, without following a symlink: the user owns the folder, so a
// link there could point root at any file on the system.
func userPath(path string, data []byte, perm os.FileMode, uid, gid int) error {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW
	if data != nil {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC | syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flags, perm)
	if err != nil {
		return fmt.Errorf("%w (nimble doesn't write through a symlink there)", err)
	}
	defer f.Close()
	if data != nil {
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	return f.Chown(uid, gid)
}

// pamLoads makes sure the module loads: it needs libpam, a glibc as new as Debian 12's and
// OpenSSL 3. A release that moved on to a newer OpenSSL (Ubuntu 26.10: OpenSSL 4) still
// packages OpenSSL 3; that package is installed then, and the distribution keeps it patched.
func pamLoads(module string) error {
	missing := lddMissing(module)
	if needsOpenSSL3(missing) {
		nimble.Say("OpenSSL 3 for pam_rssh: this release's own OpenSSL is newer")
		if _, err := nimble.AptInstall("libssl3t64"); err != nil {
			if _, err2 := nimble.AptInstall("libssl3"); err2 != nil {
				return fmt.Errorf("pam_rssh needs OpenSSL 3, and this release has no package for it: %v; %v", err, err2)
			}
		}
		missing = lddMissing(module)
	}
	if len(missing) > 0 {
		return fmt.Errorf("pam_rssh can't load on this system (nimble supports Debian 12 or newer and Ubuntu 24.04 or newer): %s", strings.Join(missing, "; "))
	}
	return nil
}

// lddMissing lists the libraries a shared object needs that the system doesn't have.
func lddMissing(so string) []string {
	var missing []string
	for _, l := range nimble.Lines(nimble.OutputC("ldd", so)) {
		if strings.Contains(l, "not found") {
			missing = append(missing, strings.TrimSpace(l))
		}
	}
	return missing
}

// needsOpenSSL3 reports whether OpenSSL 3's libraries are among the missing ones.
func needsOpenSSL3(missing []string) bool {
	for _, m := range missing {
		if strings.HasPrefix(m, "libssl.so.3 ") || strings.HasPrefix(m, "libcrypto.so.3 ") {
			return true
		}
	}
	return false
}

// pamRssh installs the pam_rssh module this nimble ships (module.go): nothing is built
// or downloaded on the host.
func pamRssh() error {
	if len(pamModule) == 0 {
		return fmt.Errorf("this nimble carries no pam_rssh module for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	pamDir := ""
	if m, _ := filepath.Glob("/usr/lib/*/security/pam_unix.so"); len(m) > 0 {
		pamDir = filepath.Dir(m[0])
	} else if m, _ := filepath.Glob("/lib/*/security/pam_unix.so"); len(m) > 0 {
		pamDir = filepath.Dir(m[0])
	}
	if pamDir == "" {
		return errors.New("PAM module directory not found")
	}
	module := filepath.Join(pamDir, "libpam_rssh.so")
	sum := sha256.Sum256(pamModule)
	marker := PamVersion() + " " + hex.EncodeToString(sum[:]) + "\n"
	if cur, err := os.ReadFile(module); err != nil || sha256.Sum256(cur) != sum {
		nimble.Say("pam_rssh " + PamVersion() + " (sudo confirmed with a hardware key)")
		// a new file renamed into place: a sudo that is running keeps the module it has loaded
		if err := replaceFile(module, pamModule, 0o644); err != nil {
			return err
		}
	}
	// every run: a release upgrade can take a library away
	if err := pamLoads(module); err != nil {
		return err
	}
	if nimble.ReadTrim(pamMarker)+"\n" == marker {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pamMarker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pamMarker, []byte(marker), 0o644)
}

// sudo: keys for pam_rssh, the PAM line, agent socket for sudo, only this user in group sudo.
func sudo(userName string, keys, allowed []string) error {
	if err := sudoKeys(userName, keys); err != nil {
		return err
	}
	return sudoOnlyAdmin(userName, allowed)
}

// sudoKeys: the root-owned sudo keys, pam_rssh in /etc/pam.d/sudo, the agent socket for sudo.
func sudoKeys(userName string, keys []string) error {
	if err := os.MkdirAll(sudoKeysDir, 0o755); err != nil {
		return err
	}
	keyFile := filepath.Join(sudoKeysDir, userName+".keys")
	want := strings.Join(keys, "\n") + "\n"
	if nimble.ReadTrim(keyFile)+"\n" != want {
		nimble.Say("sudo keys → " + keyFile)
		if err := os.WriteFile(keyFile, []byte(want), 0o644); err != nil {
			return err
		}
	}

	pam := nimble.ReadTrim("/etc/pam.d/sudo")
	if PamLine(pam, "libpam_rssh.so") < 0 {
		// `sufficient` before the normal password check: agent signature → done, else the password
		line := "auth sufficient libpam_rssh.so auth_key_file=" + sudoKeysDir + "/${user}.keys"
		at := PamLine(pam, "@include common-auth")
		if at < 0 {
			return errors.New("/etc/pam.d/sudo has no @include common-auth line")
		}
		nimble.Say("sudo: confirmed with a hardware key through the forwarded agent (pam_rssh)")
		if !nimble.Exists("/etc/pam.d/sudo.before-nimble") { // the original, from the first change
			_ = os.WriteFile("/etc/pam.d/sudo.before-nimble", []byte(pam+"\n"), 0o644)
		}
		lines := strings.Split(pam, "\n")
		lines = append(lines[:at], append([]string{line}, lines[at:]...)...)
		if err := replaceFile("/etc/pam.d/sudo", []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}

	sudoers := "# managed by nimble harden\n# pam_rssh needs the forwarded agent socket to confirm sudo with a hardware key\nDefaults env_keep += \"SSH_AUTH_SOCK\"\n"
	if nimble.ReadTrim(sudoersConf)+"\n" != sudoers {
		tmp := sudoersConf + ".tmp"
		if err := os.WriteFile(tmp, []byte(sudoers), 0o440); err != nil {
			return err
		}
		if err := exec.Command("visudo", "-cqf", tmp).Run(); err != nil {
			os.Remove(tmp)
			return errors.New("sudoers snippet failed validation")
		}
		if err := os.Rename(tmp, sudoersConf); err != nil {
			return err
		}
	}

	return nil
}

// PamLine is the index of the first active (not commented-out) line of a PAM file that
// contains needle, or -1.
func PamLine(pam, needle string) int {
	for i, l := range strings.Split(pam, "\n") {
		if t := strings.TrimSpace(l); !strings.HasPrefix(t, "#") && strings.Contains(t, needle) {
			return i
		}
	}
	return -1
}

// sudoOnlyAdmin: only the admin (and sudoAllowed accounts) in group sudo; NOPASSWD offered for removal.
func sudoOnlyAdmin(userName string, allowed []string) error {
	if f := strings.Split(nimble.Output("getent", "group", "sudo"), ":"); len(f) >= 4 && f[3] != "" {
		for _, m := range strings.Split(f[3], ",") {
			if m != userName && !contains(allowed, m) {
				nimble.Say("remove " + m + " from group sudo")
				if err := (nimble.Cmd{Name: "gpasswd", Args: []string{"-d", m, "sudo"}, Quiet: true}).Do(); err != nil {
					return err
				}
			}
		}
	}
	return nopasswd(allowed)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// nopasswd offers to comment out NOPASSWD rules: they skip authentication, so sudo would
// never ask for the hardware key (cloud images often add one for the default user). The
// admin user's password stays the fallback, so nobody is locked out. Without a terminal, or
// declined, the rule stays and the audit reports it.
func nopasswd(allowed []string) error {
	ask := func(string) bool {
		return nimble.Interactive() && nimble.Confirm("Comment it out? (sudo then asks for the hardware key, the password as fallback)", true)
	}
	validate := func() error { return exec.Command("visudo", "-cq").Run() } // the whole sudoers must still parse
	return disableNopasswd(append([]string{"/etc/sudoers"}, globSorted("/etc/sudoers.d/*")...), "/var/backups/nimble", ask, validate, allowed)
}

// disableNopasswd comments out NOPASSWD rules the user agrees to — never those of accounts
// in allowed (the host file's sudoAllowed).
func disableNopasswd(files []string, backups string, ask func(rule string) bool, validate func() error, allowed []string) error {
	for _, file := range files {
		if strings.Contains(filepath.Base(file), ".") || strings.HasSuffix(file, "~") {
			continue // sudo ignores these
		}
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		changed := false
		for i, l := range lines {
			if !NopasswdRule(l) || contains(allowed, strings.Fields(l)[0]) {
				continue
			}
			nimble.Warn(file + ": " + strings.TrimSpace(l) + " — skips authentication, so sudo never asks for the hardware key")
			if !ask(l) {
				continue
			}
			lines[i] = "# disabled by nimble harden (skipped authentication): " + l
			changed = true
		}
		if !changed {
			continue
		}
		backup := backupPath(file, backups)
		if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
			return err
		}
		if !nimble.Exists(backup) { // keep the original from the first change
			if err := os.WriteFile(backup, b, 0o600); err != nil {
				return err
			}
		}
		fi, err := os.Stat(file)
		if err != nil {
			return err
		}
		if err := replaceFile(file, []byte(strings.Join(lines, "\n")), fi.Mode().Perm()); err != nil {
			return err
		}
		if err := validate(); err != nil {
			_ = replaceFile(file, b, fi.Mode().Perm())
			return fmt.Errorf("%s failed validation after the change — restored it", file)
		}
		nimble.Say("disabled the NOPASSWD rule(s) in " + file + " (backup: " + backup + ")")
	}
	return nil
}

// backupPath is where harden keeps the original of a file it changes: in backups, named
// after its path (/etc/sudoers.d/x → etc_sudoers.d_x).
func backupPath(path, backups string) string {
	return filepath.Join(backups, strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", "_"))
}

// replaceFile writes a file atomically: a temp file (its name has a dot, so sudo would ignore
// a leftover) renamed over the original.
func replaceFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".nimble-tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NopasswdRule reports whether a sudoers line is an active rule with NOPASSWD.
func NopasswdRule(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "Defaults") && strings.Contains(t, "NOPASSWD")
}

func sshd() error {
	if !regexp.MustCompile(`(?m)^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf`).MatchString(nimble.ReadTrim("/etc/ssh/sshd_config")) {
		nimble.Warn("/etc/ssh/sshd_config doesn't include sshd_config.d/*.conf — " + sshdConf + " won't apply")
	}
	want := "# managed by nimble harden\nPermitRootLogin no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"
	if nimble.ReadTrim(sshdConf)+"\n" == want {
		return nil
	}
	nimble.Say("sshd: no root login, keys only")
	if err := os.WriteFile(sshdConf, []byte(want), 0o644); err != nil {
		return err
	}
	if err := exec.Command("sshd", "-t").Run(); err != nil {
		os.Remove(sshdConf)
		return errors.New("sshd rejected the new settings; removed " + sshdConf + ", SSH is unchanged")
	}
	if exec.Command("systemctl", "is-active", "-q", "ssh").Run() == nil {
		return nimble.Cmd{Name: "systemctl", Args: []string{"reload", "ssh"}}.Do()
	}
	return nil
}
