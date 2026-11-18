package keys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// YubiKey backups need an OpenSSH that talks to FIDO keys and ykman (PIN, which key is
// plugged in). nimble apply sets both up on every workstation (setup.SSHClient).

// BackupApplication names nimble's resident key on a YubiKey (its FIDO application).
const BackupApplication = "ssh:nimble-backup"

// FIDOSSH returns the ssh and ssh-keygen in PATH, and whether they talk to FIDO keys such
// as a YubiKey. On a Mac that is Homebrew's OpenSSH, linked into PATH ahead of macOS's own,
// which has no FIDO support; Homebrew's reaches the Secure Enclave too. Elsewhere it is
// the system's.
func FIDOSSH() (ssh, keygen string, ok bool) {
	ssh, err1 := exec.LookPath("ssh")
	keygen, err2 := exec.LookPath("ssh-keygen")
	ok = err1 == nil && err2 == nil
	if runtime.GOOS == "darwin" {
		brew := nimble.BrewPrefix() + string(filepath.Separator)
		ok = ok && strings.HasPrefix(ssh, brew) && strings.HasPrefix(keygen, brew)
	}
	return ssh, keygen, ok
}

// Plugged counts the YubiKeys that are plugged in (ykman list); -1 if ykman can't tell.
func Plugged() int {
	out, err := exec.Command("ykman", "list", "--serials").Output()
	if err != nil {
		return -1
	}
	return len(nimble.Lines(string(out)))
}

// FIDOPin reports whether the plugged-in YubiKey has a FIDO2 PIN (ykman fido info); known
// is false when ykman can't tell.
func FIDOPin() (set, known bool) {
	for _, l := range nimble.Lines(nimble.OutputC("ykman", "fido", "info")) {
		if low := strings.ToLower(strings.TrimSpace(l)); strings.HasPrefix(low, "pin") {
			return !strings.Contains(low, "not set"), true
		}
	}
	return false, false
}

// BackupHandle downloads the handle of nimble's resident key from the plugged-in YubiKey
// into dir (ssh-keygen -K, which asks for the YubiKey's PIN) and returns its path — "" if
// the YubiKey carries no nimble key.
func BackupHandle(keygen, dir string) (string, error) {
	c := exec.Command(keygen, "-K", "-N", "")
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("reading the keys on the YubiKey (ssh-keygen -K): %w", err)
	}
	pubs, _ := filepath.Glob(filepath.Join(dir, "id_*_sk_rk_"+strings.TrimPrefix(BackupApplication, "ssh:")+"*.pub"))
	if len(pubs) == 0 {
		return "", nil
	}
	return strings.TrimSuffix(pubs[0], ".pub"), nil
}

// Registered finds the key of a public key file among the YubiKey backups in keys/yubikey/.
func Registered(pubFile string) (Public, bool) {
	p := Parse(nimble.ReadTrim(pubFile))
	for _, k := range Repo() {
		if k.Kind == "yubikey" && p.Key != "" && k.ID() == p.ID() {
			return k, true
		}
	}
	return Public{}, false
}
