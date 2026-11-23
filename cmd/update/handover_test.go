package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nimblescape/nimble/pkg/keys"
)

// A complete daily-key handover ends after the update; a complete YubiKey handover stays
// (its old key goes only with the YubiKey plugged in); an unfinished one stays too.
func TestFinishHandovers(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no old handle in ~/.ssh: nothing to delete from a Secure Enclave
	repo := scratch(t, "address: swan\n")
	os.WriteFile(filepath.Join(repo, "hosts", "grizzly.yaml"), []byte("address: grizzly\n"), 0o644)
	now := time.Now()
	for _, h := range []keys.Handover{
		{Key: "enclave/mac-ssh", Since: now, Until: now.Add(time.Hour)},
		{Key: "yubikey/safe", OldApp: keys.BackupApplication, Since: now, Until: now.Add(time.Hour)},
	} {
		if err := keys.StartHandover(h); err != nil {
			t.Fatal(err)
		}
	}

	KeysSynced("swan")
	FinishHandovers() // grizzly still waits: both stay
	if len(keys.Handovers()) != 2 {
		t.Fatalf("ended too early: %+v", keys.Handovers())
	}
	KeysSynced("grizzly")
	FinishHandovers()
	if _, ok := keys.FindHandover("enclave/mac-ssh"); ok {
		t.Error("the complete daily-key handover didn't end")
	}
	if y, ok := keys.FindHandover("yubikey/safe"); !ok || len(y.Done) != 2 {
		t.Errorf("the YubiKey handover must wait for --drop-old: %+v %v", y, ok)
	}
}
