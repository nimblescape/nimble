package keys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nimblescape/nimble/pkg/config"
)

func handoverRepo(t *testing.T) *config.Config {
	dir := t.TempDir()
	t.Setenv("NIMBLE_HOME", dir)
	for rel, body := range map[string]string{
		"nimble.yaml":        "format: 2\ndomains:\n  main:\n    label: ssh\n  lab:\n    label: lab\nlinks: []\n",
		"hosts/swan.yaml":    "address: swan\n",                  // main, the default domain
		"hosts/lab1.yaml":    "address: lab1\ndomain: lab\n",     // another label
		"hosts/vm.yaml":      "address: vm\nmode: ssh\n",         // keys managed by hand
		"hosts/mac.yaml":     "mode: workstation\n",              // not reached over ssh
		"hosts/grizzly.yaml": "address: grizzly\ndomain: main\n", // main, named
	} {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHandover(t *testing.T) {
	c := handoverRepo(t)
	now := time.Now()
	enclave := Handover{Key: "enclave/mac-ssh", Old: "SHA256:old", Since: now, Until: now.Add(DefaultHandover)}
	yubi := Handover{Key: "yubikey/safe", Old: "SHA256:y", OldApp: BackupApplication, Since: now, Until: now.Add(time.Hour)}
	if enclave.Kind() != "enclave" || enclave.Name() != "mac-ssh" || enclave.Label() != "ssh" || yubi.Label() != "" {
		t.Fatalf("%q %q %q", enclave.Kind(), enclave.Name(), enclave.Label())
	}
	// a daily key: the managed hosts of its label's domains; a backup: every managed host
	if w := strings.Join(enclave.Waiting(c), ","); w != "grizzly,swan" {
		t.Errorf("enclave waiting %q", w)
	}
	if w := strings.Join(yubi.Waiting(c), ","); w != "grizzly,lab1,swan" {
		t.Errorf("yubikey waiting %q", w)
	}

	for _, h := range []Handover{enclave, yubi} {
		if err := StartHandover(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkUpdated("swan"); err != nil {
		t.Fatal(err)
	}
	MarkUpdated("swan") // twice: noted once
	got, ok := FindHandover("enclave/mac-ssh")
	if !ok || strings.Join(got.Done, ",") != "swan" || strings.Join(got.Waiting(c), ",") != "grizzly" {
		t.Fatalf("after swan: %+v", got)
	}
	if !strings.Contains(got.Describe(c), "1 host(s) still without the new key: grizzly") {
		t.Errorf("describe %q", got.Describe(c))
	}
	MarkUpdated("grizzly")
	got, _ = FindHandover("enclave/mac-ssh")
	if len(got.Waiting(c)) != 0 || got.Describe(c) != "every host has the new key" {
		t.Errorf("complete: %+v %q", got, got.Describe(c))
	}
	y, _ := FindHandover("yubikey/safe")
	if w := y.Waiting(c); strings.Join(w, ",") != "lab1" || y.OldApp != BackupApplication {
		t.Errorf("yubikey: %+v", y)
	}
	y.Until = now.Add(-time.Hour)
	if !y.Overdue() || !strings.Contains(y.Describe(c), "passed — still without the new key: lab1") {
		t.Errorf("overdue: %q", y.Describe(c))
	}

	// a new renewal of the same key replaces the old record; ending it forgets it
	StartHandover(Handover{Key: "yubikey/safe", Since: now, Until: now.Add(time.Hour)})
	if hs := Handovers(); len(hs) != 2 {
		t.Fatalf("%d handovers", len(hs))
	}
	if y, _ := FindHandover("yubikey/safe"); len(y.Done) != 0 {
		t.Errorf("not replaced: %+v", y)
	}
	EndHandover("yubikey/safe")
	EndHandover("enclave/mac-ssh")
	if _, err := os.Stat(handoverFile()); !os.IsNotExist(err) || len(Handovers()) != 0 {
		t.Errorf("state left: %v %v", err, Handovers())
	}
}

func TestParseHandover(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "36h": 36 * time.Hour} {
		if got, err := ParseHandover(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "soon", "-3d", "0h", "3x"} {
		if _, err := ParseHandover(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBackupCredentials(t *testing.T) {
	if a := Application("/tmp/x/id_ed25519_sk_rk_nimble-backup"); a != BackupApplication {
		t.Errorf("first key: %q", a)
	}
	renewed := RenewedApplication(time.Date(2026, 11, 19, 8, 30, 0, 0, time.UTC))
	if renewed != "ssh:nimble-backup-20261119-083000" || Application("id_ed25519_sk_rk_nimble-backup-20261119-083000") != renewed {
		t.Errorf("renewed %q", renewed)
	}
	// the registered handle first, then the newest
	hs := []string{"id_ed25519_sk_rk_nimble-backup", "id_ed25519_sk_rk_nimble-backup-20250101-000000", "id_ed25519_sk_rk_nimble-backup-20261119-083000"}
	sortHandles(hs, func(h string) bool { return strings.HasSuffix(h, "20250101-000000") })
	if hs[0] != "id_ed25519_sk_rk_nimble-backup-20250101-000000" || hs[1] != "id_ed25519_sk_rk_nimble-backup-20261119-083000" || hs[2] != "id_ed25519_sk_rk_nimble-backup" {
		t.Errorf("order %q", hs)
	}

	creds, err := parseCredentials("credential_id,rp_id,user_name,user_display_name,user_id\n" +
		"a1b2c3,ssh:nimble-backup,openssh,openssh,00\n" +
		"d4e5f6,ssh:nimble-backup-20261119-083000,openssh,openssh,00\n" +
		"0909,github.com,me,me,11\n")
	if err != nil || len(creds) != 3 || creds[1] != (Credential{ID: "d4e5f6", App: "ssh:nimble-backup-20261119-083000"}) {
		t.Fatalf("%+v %v", creds, err)
	}
	if _, err := parseCredentials("id,name\n1,x\n"); err == nil {
		t.Error("a CSV without credential_id and rp_id accepted")
	}
}
