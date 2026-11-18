package harden

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// Finding is one result of the audit.
type Finding struct {
	OK           bool
	Name, Detail string
}

// Audit checks that the host really is hardened — the effective configuration, not just the
// files nimble wrote: sshd's resolved settings (sshd -T, Match blocks included), every
// account's sudo rights as sudo itself evaluates them (sudo -l), the PAM stack, the accounts
// and the sudo key file. Something else (cloud-init, an image's defaults, a package, a hand
// edit) may have overridden nimble's files. Needs root.
func Audit(admin string, allowed []string) []Finding { return AuditLevel(admin, allowed, "full") }

// AuditLevel audits a host hardened at level: "full", or "sudo" (only the sudo part:
// PAM, the sudo keys, no NOPASSWD rule for the admin).
func AuditLevel(admin string, allowed []string, level string) []Finding {
	var f []Finding
	add := func(ok bool, name, detail string) { f = append(f, Finding{ok, name, detail}) }
	if level == "sudo" {
		if strings.Contains(nimble.OutputC("sudo", "-l", "-U", admin), "NOPASSWD") {
			add(false, "sudo ("+admin+")", "a NOPASSWD rule skips authentication: "+nopasswdFiles())
		}
		return append(f, sudoAudit(admin)...)
	}

	// sshd: the resolved settings for root, the admin user and anyone else
	want := map[string]string{
		"permitrootlogin": "no", "passwordauthentication": "no", "kbdinteractiveauthentication": "no",
		"permitemptypasswords": "no", "pubkeyauthentication": "yes",
	}
	var order []string
	who := map[string][]string{} // violation → the users it applies to (Match blocks differ)
	for _, u := range []string{"root", admin, "nobody"} {
		out, err := exec.Command("/usr/sbin/sshd", "-T", "-C", "user="+u+",host=localhost,addr=127.0.0.1").Output()
		if err != nil {
			add(false, "sshd", "sshd -T failed — the config may not even load: "+err.Error())
			break
		}
		for _, bad := range SSHDViolations(string(out), want) {
			if who[bad] == nil {
				order = append(order, bad)
			}
			who[bad] = append(who[bad], u)
		}
	}
	for _, bad := range order {
		add(false, "sshd", bad+" (for "+strings.Join(who[bad], ", ")+")"+culprit(strings.Fields(bad)[0]))
	}
	if !hasBad(f, "sshd") {
		add(true, "sshd", "effective config: no root login, keys only, no empty passwords")
	}

	// accounts
	if st := nimble.Field(nimble.Output("passwd", "-S", "root"), 1); st == "L" {
		add(true, "root password", "locked")
	} else {
		add(false, "root password", "not locked ("+st+")")
	}
	if b, err := os.ReadFile("/etc/passwd"); err == nil {
		if extra := UIDZero(string(b)); len(extra) > 0 {
			add(false, "uid 0", "accounts besides root with uid 0: "+strings.Join(extra, ", "))
		}
	}
	if b, err := os.ReadFile("/etc/shadow"); err == nil {
		if empty := EmptyPasswords(string(b)); len(empty) > 0 {
			add(false, "empty passwords", strings.Join(empty, ", ")+" can log in without a password where passwords are accepted")
		} else {
			add(true, "empty passwords", "none")
		}
	}
	if nimble.Exists("/root/.ssh/authorized_keys") {
		add(false, "root authorized_keys", "/root/.ssh/authorized_keys exists — unused while root can't log in, but remove it (nimble harden offers to)")
	}

	// sudo, as sudo itself evaluates the rules (all of sudoers, its includes, groups)
	var others []string
	for _, u := range LoginUsers() {
		out := nimble.OutputC("sudo", "-l", "-U", u) // the C locale: sudo translates its messages
		switch {
		case u == admin && strings.Contains(out, "NOPASSWD"):
			add(false, "sudo ("+u+")", "a NOPASSWD rule skips authentication: "+nopasswdFiles())
		case u != admin && contains(allowed, u) && SudoAllowed(out):
			add(true, "sudo ("+u+")", "allowed by the host file (sudoAllowed)")
		case u != admin && SudoAllowed(out):
			others = append(others, u)
		}
	}
	if len(others) > 0 {
		add(false, "sudo", "others may sudo: "+strings.Join(others, ", ")+" (sudo -l -U <user>)")
	} else if !hasBad(f, "sudo") {
		add(true, "sudo", "only "+admin+", with authentication")
	}

	f = append(f, sudoAudit(admin)...)
	return f
}

func hasBad(f []Finding, prefix string) bool {
	for _, x := range f {
		if !x.OK && strings.HasPrefix(x.Name, prefix) {
			return true
		}
	}
	return false
}

// SSHDViolations compares sshd -T output with the wanted values ("key have, want x").
func SSHDViolations(out string, want map[string]string) []string {
	have := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			have[strings.ToLower(k)] = strings.TrimSpace(v)
		}
	}
	var bad []string
	for k, w := range want {
		if have[k] != w {
			bad = append(bad, k+" "+have[k]+", want "+w)
		}
	}
	sort.Strings(bad)
	return bad
}

// culprit names the sshd config lines that set key, so a failure points at the file to fix.
func culprit(key string) string {
	files := append([]string{"/etc/ssh/sshd_config"}, globSorted("/etc/ssh/sshd_config.d/*.conf")...)
	var hits []string
	for _, file := range files {
		b, _ := os.ReadFile(file)
		for i, l := range strings.Split(string(b), "\n") { // every line counts, blank ones too
			if fields := strings.Fields(l); len(fields) > 1 && strings.EqualFold(fields[0], key) {
				hits = append(hits, file+":"+strconv.Itoa(i+1)+" "+strings.TrimSpace(l))
			}
		}
	}
	if len(hits) == 0 {
		return " — sshd's default, set nowhere"
	}
	return " — set in " + strings.Join(hits, "; ")
}

func globSorted(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Strings(m)
	return m
}

// UIDZero lists accounts other than root with uid 0 (/etc/passwd).
func UIDZero(passwd string) []string {
	var out []string
	for _, l := range strings.Split(passwd, "\n") {
		if f := strings.Split(l, ":"); len(f) > 2 && f[2] == "0" && f[0] != "root" {
			out = append(out, f[0])
		}
	}
	return out
}

// EmptyPasswords lists accounts whose password field is empty (/etc/shadow).
func EmptyPasswords(shadow string) []string {
	var out []string
	for _, l := range strings.Split(shadow, "\n") {
		if f := strings.Split(l, ":"); len(f) > 1 && f[0] != "" && f[1] == "" {
			out = append(out, f[0])
		}
	}
	return out
}

// LoginUsers lists the people's accounts: uid 1000–59999 with a real shell.
func LoginUsers() []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(nimble.Output("getent", "passwd")))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		shell := filepath.Base(f[6])
		if (uid >= 1000 && uid < 60000) && shell != "nologin" && shell != "false" {
			out = append(out, f[0])
		}
	}
	return out
}

// SudoAllowed reads `sudo -l -U <user>`: may the user run anything?
func SudoAllowed(out string) bool {
	return strings.Contains(out, "may run the following commands")
}

// PermitAll finds an auth line that lets authentication succeed on its own.
func PermitAll(pam string) string {
	for _, l := range nimble.Lines(pam) {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "auth" && f[1] == "sufficient" && strings.HasSuffix(f[2], "pam_permit.so") {
			return strings.Join(f, " ")
		}
	}
	return ""
}

// nopasswdFiles names the sudoers files with NOPASSWD rules.
func nopasswdFiles() string {
	var hits []string
	for _, file := range append([]string{"/etc/sudoers"}, globSorted("/etc/sudoers.d/*")...) {
		for _, l := range nimble.Lines(nimble.ReadTrim(file)) {
			if !strings.HasPrefix(strings.TrimSpace(l), "#") && strings.Contains(l, "NOPASSWD") {
				hits = append(hits, file+": "+strings.TrimSpace(l))
			}
		}
	}
	if len(hits) == 0 {
		return "see sudo -l"
	}
	return strings.Join(hits, "; ")
}

// sudoAudit checks the sudo part: pam_rssh first in /etc/pam.d/sudo, nothing letting
// authentication pass by itself, the root-owned sudo keys.
func sudoAudit(admin string) []Finding {
	var f []Finding
	add := func(ok bool, name, detail string) { f = append(f, Finding{ok, name, detail}) }
	// PAM: sudo asks pam_rssh first, and nothing lets authentication pass by itself
	pam := nimble.ReadTrim("/etc/pam.d/sudo")
	switch rssh, common := PamLine(pam, "libpam_rssh.so"), PamLine(pam, "common-auth"); {
	case rssh < 0:
		add(false, "PAM", "/etc/pam.d/sudo has no active pam_rssh line")
	case common >= 0 && rssh > common:
		add(false, "PAM", "pam_rssh comes after common-auth in /etc/pam.d/sudo")
	default:
		add(true, "PAM", "sudo: pam_rssh, then the password")
	}
	for _, file := range []string{"/etc/pam.d/sudo", "/etc/pam.d/common-auth", "/etc/pam.d/sshd", "/etc/pam.d/su"} {
		if l := PermitAll(nimble.ReadTrim(file)); l != "" {
			add(false, "PAM", file+": "+l+" lets anyone authenticate")
		}
	}

	// the sudo keys: only root may change them
	keyFile := filepath.Join(sudoKeysDir, admin+".keys")
	for _, p := range []string{sudoKeysDir, keyFile} {
		fi, err := os.Stat(p)
		if err != nil {
			add(false, "sudo keys", p+" missing")
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
			add(false, "sudo keys", p+" can be changed by others than root ("+fi.Mode().Perm().String()+")")
		}
	}
	if !hasBad(f, "sudo keys") {
		add(true, "sudo keys", keyFile+", root-owned")
	}
	return f
}
