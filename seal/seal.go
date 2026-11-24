// Package seal keeps secrets such as API tokens in files that only hardware keys open: the
// Secure Enclave of a Mac or a YubiKey. nimble writes these files (nimble token), and any Go
// program can read them with this package, for example to hand a provider token to its
// API client. It uses only the Go standard library and needs no cgo.
//
// A sealed token is one YAML file, by default in the tokens/ folder of the nimble config
// repo (see Dir). It has a name, labels (key-value pairs that programs search by, see
// Find), one stanza per recipient key, and the encrypted secret. Every recipient opens the
// token on its own:
//
//   - enclave: a P-256 key in the Secure Enclave of a Mac (sc_auth create-ctk-identity),
//     which runs the ECDH key agreement after Touch ID.
//   - fido2: a P-256 key derived from the FIDO2 hmac-secret of a credential on a YubiKey,
//     after its PIN and a touch (the tools of libfido2 talk to the YubiKey).
//
// Sealing needs only the public keys, so nimble seals a token for every registered key at
// once. Opening tries the keys of this machine first, then an agent: nimble agent on a
// workstation answers requests that reach it through a forwarded ssh agent socket
// (SSH_AUTH_SOCK), after Touch ID on the workstation.
//
// Version 1 of the format is one fixed set of algorithms: ECDH on P-256, HKDF with SHA-256,
// AES-256-GCM. Other algorithms make a new version.
package seal

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Version is the format version this package writes and reads.
const Version = 1

// Errors that callers can test with errors.Is.
var (
	ErrNotFound  = errors.New("no sealed token matches")
	ErrAmbiguous = errors.New("more than one sealed token matches")
	ErrNoKey     = errors.New("no key that opens this token is reachable")
)

// Labels are the searchable key-value pairs of a token, such as provider: cherry. Keys are
// lowerCamelCase; values have no spaces.
type Labels map[string]string

// Kinds of recipient keys.
const (
	KindEnclave = "enclave" // a key in the Secure Enclave of a Mac
	KindFIDO2   = "fido2"   // a key derived from the hmac-secret of a FIDO2 credential (YubiKey)
)

// Recipient is a key that a token is sealed for.
type Recipient struct {
	Name   string // e.g. enclave/hs-mbp or yubikey/safe-home
	Kind   string // KindEnclave or KindFIDO2
	Public []byte // the P-256 public key: an uncompressed point, 65 bytes
	// Handle finds the private key. enclave: the public key hash the keychain keeps
	// (kSecAttrApplicationLabel). fido2: the credential ID on the YubiKey.
	Handle []byte
}

// stanza is a recipient with its copy of the data key.
type stanza struct {
	Recipient
	Ephemeral []byte // the ephemeral P-256 public key of this stanza (65 bytes)
	Wrapped   []byte // the data key, encrypted with the key agreed with Recipient
}

// Token is a token file: sealed, or in clear text.
//
// A sealed token is the YAML document of this package. Any other file is a token in clear
// text: its content, without a final line break, is the secret, used as it is. It has no
// labels, and it must be readable by its owner only (mode 600), as ssh demands of a
// private key.
type Token struct {
	Name    string
	Labels  Labels
	Created time.Time
	Path    string // the file it was read from, if any

	stanzas []stanza
	data    []byte // nonce and ciphertext of the secret
	clear   []byte // the secret of a token in clear text; nil if sealed
}

// Sealed reports whether the token is sealed (not in clear text).
func (t *Token) Sealed() bool { return t.clear == nil }

// Recipients lists the keys the token is sealed for.
func (t *Token) Recipients() []Recipient {
	out := make([]Recipient, len(t.stanzas))
	for i, s := range t.stanzas {
		out[i] = s.Recipient
	}
	return out
}

// Matches reports whether the token has every label in want.
func (t *Token) Matches(want Labels) bool {
	for k, v := range want {
		if t.Labels[k] != v {
			return false
		}
	}
	return true
}

var (
	nameRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	recipientRe  = regexp.MustCompile(`^[a-z0-9]+/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	labelKeyRe   = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,63}$`)
	labelValueRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:/+=-]{0,254}$`)
)

// CheckName checks a token name: it becomes a file name.
func CheckName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("token name %q: letters, digits, '.', '-' and '_', at most 64", name)
	}
	return nil
}

// CheckLabel checks one label.
func CheckLabel(key, value string) error {
	if !labelKeyRe.MatchString(key) {
		return fmt.Errorf("label key %q: lowerCamelCase letters and digits", key)
	}
	if !labelValueRe.MatchString(value) {
		return fmt.Errorf("label %s=%q: letters, digits and . _ @ : / + = -, no spaces", key, value)
	}
	return nil
}

// ParseLabels reads key=value arguments.
func ParseLabels(args []string) (Labels, error) {
	l := Labels{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("label %q: key=value", a)
		}
		if err := CheckLabel(k, v); err != nil {
			return nil, err
		}
		l[k] = v
	}
	return l, nil
}

// String writes labels as sorted key=value pairs.
func (l Labels) String() string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + l[k]
	}
	return strings.Join(parts, " ")
}

func checkRecipient(r Recipient) error {
	if !recipientRe.MatchString(r.Name) {
		return fmt.Errorf("recipient name %q: <kind>/<name>", r.Name)
	}
	switch r.Kind {
	case KindEnclave, KindFIDO2:
	default:
		return fmt.Errorf("recipient %s: unknown kind %q", r.Name, r.Kind)
	}
	if len(r.Public) != 65 || r.Public[0] != 4 {
		return fmt.Errorf("recipient %s: the public key is not an uncompressed P-256 point", r.Name)
	}
	if len(r.Handle) == 0 || len(r.Handle) > 1024 {
		return fmt.Errorf("recipient %s: no key handle", r.Name)
	}
	return nil
}

// Seal encrypts secret for the recipients. It needs no hardware key: only the public keys.
func Seal(name string, labels Labels, secret []byte, to []Recipient) (*Token, error) {
	if err := CheckName(name); err != nil {
		return nil, err
	}
	for k, v := range labels {
		if err := CheckLabel(k, v); err != nil {
			return nil, err
		}
	}
	if len(to) == 0 {
		return nil, errors.New("no key to seal for")
	}
	seen := map[string]bool{}
	for _, r := range to {
		if err := checkRecipient(r); err != nil {
			return nil, err
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("recipient %s twice", r.Name)
		}
		seen[r.Name] = true
	}
	t := &Token{Name: name, Labels: Labels{}, Created: time.Now().UTC().Truncate(time.Second)}
	for k, v := range labels {
		t.Labels[k] = v
	}
	if err := t.encrypt(secret, to); err != nil {
		return nil, err
	}
	return t, nil
}

// Reseal encrypts the token again with a new data key for the recipients to, keeping its
// name, labels and creation time. Open it first to get secret. A token in clear text
// becomes a sealed one.
func (t *Token) Reseal(secret []byte, to []Recipient) error {
	n, err := Seal(t.Name, t.Labels, secret, to)
	if err != nil {
		return err
	}
	if t.Created.IsZero() {
		t.Created = n.Created
	}
	t.stanzas, t.data, t.clear = n.stanzas, n.data, nil
	return nil
}
