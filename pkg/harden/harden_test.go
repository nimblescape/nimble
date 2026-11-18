package harden

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedBlock(t *testing.T) {
	enclave := "sk-ecdsa-sha2-nistp256@openssh.com AAAAenclave mac"
	yubi := "sk-ssh-ed25519@openssh.com AAAAyubi yubikey-a"
	other := "ssh-ed25519 AAAAother someone"

	// a host that already has the enclave key by hand, plus someone else's key
	got := ManagedBlock(other+"\n"+enclave+"\n", []string{enclave, yubi})
	want := other + "\n" + blockBegin + "\n" + enclave + "\n" + yubi + "\n" + blockEnd + "\n"
	if got != want {
		t.Fatalf("first run:\n%s\nwant:\n%s", got, want)
	}

	// re-run is stable; removing a key drops it from the block
	if again := ManagedBlock(got, []string{enclave, yubi}); again != got {
		t.Fatalf("not stable:\n%s", again)
	}
	removed := ManagedBlock(got, []string{enclave})
	if want := other + "\n" + blockBegin + "\n" + enclave + "\n" + blockEnd + "\n"; removed != want {
		t.Fatalf("remove:\n%s\nwant:\n%s", removed, want)
	}

	// options in front of a key still identify it
	if keyID(`from="10.0.0.1" `+enclave) != keyID(enclave) {
		t.Fatal("keyID with options")
	}
}

func TestRemoveRootKeys(t *testing.T) {
	dir := t.TempDir()
	keys, backups := filepath.Join(dir, "root", ".ssh", "authorized_keys"), filepath.Join(dir, "backups")
	write := func(body string) {
		if err := os.MkdirAll(filepath.Dir(keys), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keys, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	asked := 0
	answer := func(yes bool) func() bool { return func() bool { asked++; return yes } }
	run := func(yes bool) {
		t.Helper()
		if err := removeRootKeys(keys, backups, answer(yes)); err != nil {
			t.Fatal(err)
		}
	}

	run(true) // no file: nothing to ask
	if asked != 0 {
		t.Fatal("asked without a file")
	}

	write("ssh-ed25519 AAAAcloud cloud-init\n")
	run(false) // declined: the file stays, no backup
	if _, err := os.Stat(keys); err != nil || asked != 1 {
		t.Fatalf("declined: %v, asked %d", err, asked)
	}

	run(true) // agreed: removed, the original backed up
	if _, err := os.Stat(keys); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	b, err := os.ReadFile(backupPath(keys, backups))
	if err != nil || string(b) != "ssh-ed25519 AAAAcloud cloud-init\n" {
		t.Fatalf("backup %q %v", b, err)
	}

	write("ssh-ed25519 AAAAlater\n")
	run(true) // a later run keeps the first original
	if b, _ := os.ReadFile(backupPath(keys, backups)); string(b) != "ssh-ed25519 AAAAcloud cloud-init\n" {
		t.Fatalf("backup overwritten: %q", b)
	}
}
