package hosts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// Normally nimble reaches the hosts with the ssh in PATH and the daily key from the
// generated ssh config. With --yubikey it uses a YubiKey backup instead: for a new
// workstation whose daily key the hosts don't trust yet, or a lost or expired daily key.
// It is the same ssh either way (on a Mac Homebrew's, which nimble apply links into PATH).
var backup struct {
	ssh  string   // the OpenSSH that talks to FIDO keys
	opts []string // the backup key's handle, the built-in FIDO support
	dir  string   // where the handle is, for this run
}

// UseYubiKey switches every ssh of this run to the YubiKey that is plugged in: it reads
// the key's handle from the YubiKey (asking for its PIN) and checks that it is one of the
// registered backups.
func UseYubiKey() error {
	ssh, keygen, ok := keys.FIDOSSH()
	if !ok {
		return errors.New("--yubikey needs an OpenSSH that talks to FIDO keys, and " + ssh + " doesn't — nimble apply sets it up")
	}
	dir, err := os.MkdirTemp("", "nimble-yubikey")
	if err != nil {
		return err
	}
	backup.dir = dir
	for keys.Plugged() == 0 { // ykman sees none: say so before ssh-keygen asks for a PIN
		if !nimble.Interactive() {
			return errors.New("no YubiKey is plugged in")
		}
		nimble.Pause("No YubiKey found — plug in a registered backup")
	}
	nimble.Step("YubiKey: enter its PIN (and touch it when it blinks)")
	handles, err := keys.BackupHandles(keygen, dir)
	if err != nil {
		return err
	}
	if len(handles) == 0 {
		return errors.New("this YubiKey carries no nimble key — register it first: nimble key yubikey")
	}
	k, ok := keys.Registered(handles[0] + ".pub") // the registered one comes first
	if !ok {
		return errors.New("this YubiKey's key isn't in keys/yubikey/, so no host trusts it — nimble key yubikey registers it")
	}
	file := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(file, []byte(yubikeyConfig(ssh, handles)), 0o600); err != nil {
		return err
	}
	backup.ssh = ssh
	backup.opts = []string{"-F", file}
	nimble.Step("using the YubiKey " + k.Name + ": ssh asks for its PIN and a touch for each host; sudo there asks for the admin's password")
	return nil
}

// yubikeyConfig is the ssh config of a --yubikey run (ssh -F): the backup key's handles,
// then what ssh reads without -F. A file rather than -o options: ssh passes -F on to the
// hops of a ProxyJump (a host's jump), -o not — the jump host is reached with the YubiKey too.
func yubikeyConfig(ssh string, handles []string) string {
	conf := "# nimble --yubikey: this run's ssh, the hops through a jump host included\n" +
		"IdentitiesOnly yes\nSecurityKeyProvider internal\n"
	for _, h := range handles { // during a handover the old credential too: hosts not updated yet trust only that one
		conf += "IdentityFile " + h + "\n"
	}
	// the user's config (the generated host entries), then the system's of this ssh
	system := "/etc/ssh/ssh_config"
	if p := nimble.BrewPrefix(); strings.HasPrefix(ssh, p+"/") {
		system = p + "/etc/ssh/ssh_config"
	}
	return conf + "Include " + filepath.Join(nimble.Home(), ".ssh", "config") + "\nInclude " + system + "\n"
}

// YubiKey reports whether this run reaches the hosts with a YubiKey backup.
func YubiKey() bool { return backup.ssh != "" }

// Cleanup removes what UseYubiKey left (the key handle; it is useless without the YubiKey).
func Cleanup() {
	if backup.dir != "" {
		os.RemoveAll(backup.dir)
	}
}

// Through is the ssh options for a connection to a host's address (the first login, before
// it trusts any key) through its jump host; none without one.
func Through(jump string) []string {
	if jump == "" {
		return nil
	}
	return append(append([]string{}, backup.opts...), "-J", jump)
}

// SSH is an ssh command with the given arguments: the ssh in PATH with the generated
// config, or with --yubikey with the backup key.
func SSH(args ...string) *exec.Cmd {
	if !YubiKey() {
		return exec.Command("ssh", args...)
	}
	return exec.Command(backup.ssh, append(append([]string{}, backup.opts...), args...)...)
}
