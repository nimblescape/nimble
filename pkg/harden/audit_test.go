package harden

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHDViolations(t *testing.T) {
	out := "port 22\npermitrootlogin without-password\npasswordauthentication yes\nkbdinteractiveauthentication no\npermitemptypasswords no\npubkeyauthentication yes\n"
	want := map[string]string{"permitrootlogin": "no", "passwordauthentication": "no", "kbdinteractiveauthentication": "no", "pubkeyauthentication": "yes"}
	got := strings.Join(SSHDViolations(out, want), "|")
	if got != "passwordauthentication yes, want no|permitrootlogin without-password, want no" {
		t.Fatal(got)
	}
	if v := SSHDViolations("permitrootlogin no\npasswordauthentication no\nkbdinteractiveauthentication no\npubkeyauthentication yes\n", want); len(v) != 0 {
		t.Fatal(v)
	}
}

func TestAccounts(t *testing.T) {
	if got := UIDZero("root:x:0:0::/root:/bin/bash\ntoor:x:0:0::/root:/bin/sh\ndebian:x:1000:1000::/home/debian:/bin/bash\n"); strings.Join(got, ",") != "toor" {
		t.Fatal(got)
	}
	if got := EmptyPasswords("root:!:19000::::::\ndebian:$y$j9T$abc:19000::::::\nguest::19000::::::\ndaemon:*:19000::::::\n"); strings.Join(got, ",") != "guest" {
		t.Fatal(got)
	}
}

func TestSudoAndPAM(t *testing.T) {
	if !SudoAllowed("User bob may run the following commands on h:\n    (ALL : ALL) ALL") || SudoAllowed("User bob is not allowed to run sudo on h.") {
		t.Fatal("sudo -l parsing")
	}
	if PermitAll("auth [success=1 default=ignore] pam_unix.so nullok\nauth requisite pam_deny.so\nauth required pam_permit.so") != "" {
		t.Fatal("Debian's common-auth flagged")
	}
	if PermitAll("auth sufficient pam_permit.so") == "" {
		t.Fatal("pam_permit sufficient not flagged")
	}
}

func TestNopasswdRule(t *testing.T) {
	for line, want := range map[string]bool{
		"debian ALL=(ALL) NOPASSWD:ALL":          true,
		"%sudo   ALL=(ALL:ALL) NOPASSWD: ALL":    true,
		"# debian ALL=(ALL) NOPASSWD:ALL":        false,
		"%sudo   ALL=(ALL:ALL) ALL":              false,
		"Defaults env_keep += \"SSH_AUTH_SOCK\"": false,
	} {
		if NopasswdRule(line) != want {
			t.Errorf("%q: want %v", line, want)
		}
	}
}

func TestDisableNopasswd(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "90-cloud-init-users")
	orig := "# Created by cloud-init\ndebian ALL=(ALL) NOPASSWD:ALL\n"
	os.WriteFile(file, []byte(orig), 0o440)
	yes := func(string) bool { return true }
	ok := func() error { return nil }

	if err := disableNopasswd([]string{file}, filepath.Join(dir, "bak"), func(string) bool { return false }, ok, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != orig {
		t.Fatal("changed although declined")
	}
	if err := disableNopasswd([]string{file}, filepath.Join(dir, "bak"), yes, func() error { return errors.New("bad") }, nil); err == nil {
		t.Fatal("invalid sudoers accepted")
	}
	if b, _ := os.ReadFile(file); string(b) != orig {
		t.Fatal("not restored after failed validation")
	}
	if err := disableNopasswd([]string{file}, filepath.Join(dir, "bak"), yes, ok, []string{"debian"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != orig {
		t.Fatal("changed a rule of an allowed account")
	}
	if err := disableNopasswd([]string{file}, filepath.Join(dir, "bak"), yes, ok, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "# disabled by nimble harden (skipped authentication): debian ALL=(ALL) NOPASSWD:ALL") || NopasswdRule(strings.Split(string(b), "\n")[1]) {
		t.Fatalf("not disabled:\n%s", b)
	}
	if bak, _ := os.ReadFile(filepath.Join(dir, "bak", strings.ReplaceAll(strings.TrimPrefix(file, "/"), "/", "_"))); string(bak) != orig {
		t.Fatal("no backup")
	}
}

func TestPamLine(t *testing.T) {
	pam := "#%PAM-1.0\n# auth sufficient libpam_rssh.so off\n@include common-auth\nauth sufficient libpam_rssh.so x\n"
	if PamLine(pam, "libpam_rssh.so") != 3 {
		t.Error("a commented-out line must not count")
	}
	if PamLine(pam, "common-auth") != 2 || PamLine(pam, "pam_tid") != -1 {
		t.Error("PamLine")
	}
}
