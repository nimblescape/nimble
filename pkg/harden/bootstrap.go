package harden

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// Bootstrap prepares a fresh host as root, before key login works: sudo installed, the
// admin user with a password and sudo rights, the trusted keys authorized for it, and
// nimble installed in its home. Nothing is locked down yet — that is harden's job, once
// key login as the admin user is confirmed.
func Bootstrap(userName, keysFile, binary string) error {
	if os.Geteuid() != 0 {
		return errors.New("needs root")
	}
	if !nimble.IsDebian() {
		return errors.New("not a Debian host")
	}
	if _, err := nimble.AptInstall("sudo"); err != nil {
		return err
	}
	if _, err := user.Lookup(userName); err != nil {
		nimble.Say("create user " + userName)
		if err := (nimble.Cmd{Name: "adduser", Args: []string{"--disabled-password", "--gecos", "", userName}, Quiet: true}).Do(); err != nil {
			return err
		}
	}
	u, err := user.Lookup(userName)
	if err != nil {
		return err
	}
	if nimble.Field(nimble.Output("passwd", "-S", userName), 1) != "P" {
		nimble.Say("set a password for " + userName + " — sudo asks for it until Touch ID works, and it stays the fallback")
		if err := nimble.Run("passwd", userName); err != nil {
			return err
		}
	}
	if !inGroup(userName, "sudo") {
		nimble.Say("add " + userName + " to group sudo")
		if err := (nimble.Cmd{Name: "usermod", Args: []string{"-aG", "sudo", userName}}).Do(); err != nil {
			return err
		}
	}
	keys := nimble.Lines(nimble.ReadTrim(keysFile))
	if len(keys) == 0 {
		return errors.New("no keys to authorize")
	}
	if err := authorizedKeys(u, keys); err != nil {
		return err
	}

	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	bin := filepath.Join(u.HomeDir, ".local", "bin")
	for _, d := range []string{filepath.Join(u.HomeDir, ".local"), bin} {
		_ = os.MkdirAll(d, 0o755)
		_ = os.Chown(d, uid, gid)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "nimble"), data, 0o755); err != nil {
		return err
	}
	if err := os.Chown(filepath.Join(bin, "nimble"), uid, gid); err != nil {
		return err
	}
	nimble.Say(fmt.Sprintf("nimble → %s/nimble", bin))
	return nil
}
