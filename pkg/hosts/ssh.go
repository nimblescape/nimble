package hosts

import (
	"errors"
	"os"
	"os/exec"

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
	handle, err := keys.BackupHandle(keygen, dir)
	if err != nil {
		return err
	}
	if handle == "" {
		return errors.New("this YubiKey carries no nimble key — register it first: nimble key yubikey")
	}
	k, ok := keys.Registered(handle + ".pub")
	if !ok {
		return errors.New("this YubiKey's key isn't in keys/yubikey/, so no host trusts it — nimble key yubikey registers it")
	}
	backup.ssh = ssh
	backup.opts = []string{"-o", "IdentitiesOnly=yes", "-o", "IdentityFile=" + handle, "-o", "SecurityKeyProvider=internal"}
	nimble.Step("using the YubiKey " + k.Name + ": ssh asks for its PIN and a touch for each host; sudo there asks for the admin's password")
	return nil
}

// YubiKey reports whether this run reaches the hosts with a YubiKey backup.
func YubiKey() bool { return backup.ssh != "" }

// Cleanup removes what UseYubiKey left (the key handle; it is useless without the YubiKey).
func Cleanup() {
	if backup.dir != "" {
		os.RemoveAll(backup.dir)
	}
}

// SSH is an ssh command with the given arguments: the ssh in PATH with the generated
// config, or with --yubikey with the backup key.
func SSH(args ...string) *exec.Cmd {
	if !YubiKey() {
		return exec.Command("ssh", args...)
	}
	return exec.Command(backup.ssh, append(append([]string{}, backup.opts...), args...)...)
}
