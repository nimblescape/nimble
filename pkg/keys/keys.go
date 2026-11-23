// Package keys knows the SSH keys nimble manages, by administration domain:
//
//	nimble.yaml domains                  domain → label of its daily Secure Enclave key
//	keys/enclave/<mac>-<label>.pub       daily keys: one per Mac and label (Touch ID)
//	keys/yubikey/<name>.pub              backups: hardware keys in safes, trusted in every domain
//
// A host trusts the enclave keys of its domain's label (from every Mac) and all backups.
package keys

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// SKProvider is macOS's provider for Secure Enclave SSH keys.
const SKProvider = "/usr/lib/ssh-keychain.dylib"

// Public is one key file in keys/.
type Public struct {
	Name               string // file name without .pub
	Kind               string // "enclave" or "yubikey"
	Machine            string // enclave keys: the Mac
	Label              string // enclave keys: the Secure Enclave label
	Type, Key, Comment string
}

// ID is "type key", comparable across files.
func (p Public) ID() string { return p.Type + " " + p.Key }

// Line is the authorized_keys line.
func (p Public) Line() string { return strings.TrimSpace(p.Type + " " + p.Key + " " + p.Comment) }

// Path is the key's file in the repo.
func (p Public) Path() string { return nimble.RepoFile("keys", p.Kind, p.Name+".pub") }

// Repo lists all keys in keys/enclave and keys/yubikey.
func Repo() []Public {
	var out []Public
	for _, kind := range []string{"enclave", "yubikey"} {
		files, _ := filepath.Glob(nimble.RepoFile("keys", kind, "*.pub"))
		sort.Strings(files)
		for _, f := range files {
			line, _, _ := strings.Cut(nimble.ReadTrim(f), "\n") // one key per file: further lines are not trusted
			k := Parse(line)
			k.Kind, k.Name = kind, strings.TrimSuffix(filepath.Base(f), ".pub")
			if kind == "enclave" {
				if i := strings.LastIndex(k.Name, "-"); i > 0 {
					k.Machine, k.Label = k.Name[:i], k.Name[i+1:]
				}
			}
			out = append(out, k)
		}
	}
	return out
}

// Trusted returns the keys a host of the domain accepts: its enclave label + all backups.
func Trusted(d config.Domain) []Public {
	var out []Public
	for _, k := range Repo() {
		if k.Kind == "yubikey" || k.Label == d.Label {
			out = append(out, k)
		}
	}
	return out
}

// Parse reads a public key line ("type key comment").
func Parse(line string) Public {
	f := strings.SplitN(strings.TrimSpace(line), " ", 3)
	var p Public
	if len(f) > 0 {
		p.Type = f[0]
	}
	if len(f) > 1 {
		p.Key = f[1]
	}
	if len(f) > 2 {
		p.Comment = f[2]
	}
	return p
}

// HandlePath is where the ssh key handle of a Secure Enclave key lives.
func HandlePath(label string) string {
	return filepath.Join(nimble.Home(), ".ssh", "id_ecdsa_sk_rk_"+label)
}

// Enclave is a key in this Mac's Secure Enclave (a CryptoTokenKit identity). Labels need
// not be unique there, so a key is identified by its fingerprint.
type Enclave struct {
	Hash              string // public key hash (SHA-1): what sc_auth delete-ctk-identity takes
	Fingerprint       string // SHA256:… as ssh prints it
	Label, Protection string
	ValidTo           time.Time // zero if unknown
	Valid             bool
}

// ExpiresSoon reports whether the key is invalid or runs out within 30 days.
func (e Enclave) ExpiresSoon() bool {
	return !e.Valid || (!e.ValidTo.IsZero() && time.Until(e.ValidTo) < 30*24*time.Hour)
}

var dateRe = regexp.MustCompile(`(\d{1,2})([./])(\d{1,2})[./](\d{2,4})`)

// Enclaves lists the Secure Enclave identities (`sc_auth list-ctk-identities`).
func Enclaves() []Enclave {
	return parseEnclaves(nimble.Output("sc_auth", "list-ctk-identities"), nimble.Output("sc_auth", "list-ctk-identities", "-t", "ssh"))
}

// parseEnclaves reads sc_auth's two listings of the same identities: with SHA-1 hashes
// (the default) and with ssh fingerprints (-t ssh), row by row.
func parseEnclaves(sha1, ssh string) []Enclave {
	rows := func(out string) [][]string {
		var r [][]string
		for i, line := range nimble.Lines(out) {
			if f := strings.Fields(line); i > 0 && len(f) >= 6 { // the first line is the header
				r = append(r, f)
			}
		}
		return r
	}
	prints := rows(ssh)
	var out []Enclave
	for i, f := range rows(sha1) {
		e := Enclave{Hash: f[1], Protection: f[2], Label: f[3], Valid: f[len(f)-1] == "YES"}
		if i < len(prints) && prints[i][3] == e.Label && strings.HasPrefix(prints[i][1], "SHA256:") {
			e.Fingerprint = prints[i][1]
		}
		if m := dateRe.FindStringSubmatch(f[len(f)-3]); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[3])
			y, _ := strconv.Atoi(m[4])
			if y < 100 {
				y += 2000
			}
			day, month := a, b // 14.11.27
			if m[2] == "/" {
				day, month = b, a // 11/14/27
			}
			e.ValidTo = time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.Local)
		}
		out = append(out, e)
	}
	return out
}

// FindEnclave returns a Secure Enclave key with exactly this label, if any.
func FindEnclave(label string) (Enclave, bool) {
	for _, e := range Enclaves() {
		if e.Label == label {
			return e, true
		}
	}
	return Enclave{}, false
}

// Fingerprint is the SHA256 fingerprint of a public key line, as ssh-keygen -l prints it
// ("" if the line holds no key).
func Fingerprint(pubLine string) string {
	blob, err := base64.StdEncoding.DecodeString(Parse(pubLine).Key)
	if err != nil || len(blob) == 0 {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// DropOldEnclave removes the key a renewal kept on this Mac: the Secure Enclave key behind
// handle.old, and that handle. Hosts that don't trust the new key yet can then only be
// reached with a YubiKey.
func DropOldEnclave(handle string) error {
	old := handle + ".old"
	if e, ok := DailyKey(old); ok {
		if cur, ok := DailyKey(handle); ok && cur.Hash == e.Hash {
			return errors.New("the old handle points at the key in use — not removing it")
		}
		if err := nimble.Run("sc_auth", "delete-ctk-identity", "-h", e.Hash); err != nil {
			return err
		}
	}
	for _, suffix := range []string{"", ".pub"} {
		if err := os.Remove(old + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// DailyKey is the Secure Enclave key behind a key handle file (~/.ssh/id_ecdsa_sk_rk_<label>,
// or its .old copy): the one with the handle's fingerprint.
func DailyKey(handle string) (Enclave, bool) {
	fp := Fingerprint(nimble.ReadTrim(handle + ".pub"))
	if fp == "" {
		return Enclave{}, false
	}
	for _, e := range Enclaves() {
		if e.Fingerprint == fp {
			return e, true
		}
	}
	return Enclave{}, false
}
