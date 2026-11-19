package keys

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

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

// RenewedApplication is the FIDO application of a renewed backup key: its own, so the new
// credential doesn't replace the old one on the YubiKey during the handover.
func RenewedApplication(t time.Time) string {
	return BackupApplication + "-" + t.Format("20060102-150405")
}

// BackupHandles downloads the handles of nimble's resident keys from the plugged-in YubiKey
// into dir (ssh-keygen -K, which asks for the YubiKey's PIN) and returns their paths, the
// registered one (keys/yubikey/) first, then the newest — none if it carries no nimble key.
// More than one is there during a handover: the renewed key and the old one.
func BackupHandles(keygen, dir string) ([]string, error) {
	c := exec.Command(keygen, "-K", "-N", "")
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("reading the keys on the YubiKey (ssh-keygen -K): %w", err)
	}
	pubs, _ := filepath.Glob(filepath.Join(dir, "id_*_sk_rk_"+strings.TrimPrefix(BackupApplication, "ssh:")+"*.pub"))
	var out []string
	for _, p := range pubs {
		out = append(out, strings.TrimSuffix(p, ".pub"))
	}
	sortHandles(out, func(h string) bool { _, ok := Registered(h + ".pub"); return ok })
	return out, nil
}

// sortHandles puts the registered handle first, then the others newest first (the
// application of a renewed key ends in its date, and sorts after the first key's).
func sortHandles(hs []string, registered func(string) bool) {
	sort.SliceStable(hs, func(i, j int) bool {
		if ri, rj := registered(hs[i]), registered(hs[j]); ri != rj {
			return ri
		}
		return Application(hs[i]) > Application(hs[j])
	})
}

var handleApp = regexp.MustCompile(`_sk_rk_(.+)$`)

// Application is the FIDO application of a resident key's handle (ssh-keygen -K names it
// id_<type>_sk_rk_<application without "ssh:">).
func Application(handle string) string {
	if m := handleApp.FindStringSubmatch(filepath.Base(handle)); m != nil {
		return "ssh:" + m[1]
	}
	return ""
}

// Credential is a resident credential on the plugged-in YubiKey.
type Credential struct{ ID, App string }

// Credentials lists the resident credentials on the plugged-in YubiKey (ykman fido
// credentials list, which asks for the PIN).
func Credentials() ([]Credential, error) {
	c := exec.Command("ykman", "fido", "credentials", "list", "--csv")
	c.Stdin, c.Stderr = os.Stdin, os.Stderr // the PIN prompt
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("listing the credentials on the YubiKey (ykman fido credentials list): %w", err)
	}
	return parseCredentials(string(out))
}

// parseCredentials reads ykman's CSV by its header (credential_id, rp_id, …).
func parseCredentials(out string) ([]Credential, error) {
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	id, rp := -1, -1
	for i, h := range rows[0] {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "credential_id":
			id = i
		case "rp_id":
			rp = i
		}
	}
	if id < 0 || rp < 0 {
		return nil, fmt.Errorf("unexpected ykman output: %q", strings.Join(rows[0], ","))
	}
	var creds []Credential
	for _, r := range rows[1:] {
		if len(r) > id && len(r) > rp {
			creds = append(creds, Credential{ID: r[id], App: r[rp]})
		}
	}
	return creds, nil
}

// DeleteCredential removes a resident credential from the plugged-in YubiKey (asks for the
// PIN). It is gone for good: no host can then be reached with it.
func DeleteCredential(cred Credential) error {
	return nimble.Run("ykman", "fido", "credentials", "delete", "--force", cred.ID)
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
