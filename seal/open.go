package seal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
)

// An opener reaches private keys and runs the key agreement of a stanza with them.
type opener interface {
	// name says where the key is, for messages.
	name() string
	// has reports, without Touch ID or a PIN, whether this opener can try recipient r.
	has(r Recipient) bool
	// kek runs the key agreement of the stanza with ephemeral key eph and returns its
	// key-wrapping key (DeriveKEK). prompt says what for, where the key shows a prompt.
	kek(r Recipient, eph []byte, prompt string) ([]byte, error)
	// offers lists the keys an agent offers with this opener (see AgentKeysExtension).
	offers() []agentKey
}

// localOpeners are the keys of this machine: the Secure Enclave and a plugged-in YubiKey.
var localOpeners = func() []opener {
	return []opener{&enclaveOpener{}, &fido2Opener{}}
}

// Open returns the secret of the token. A token in clear text needs no key, but its file
// must be readable by its owner only. A sealed token is decrypted with the first key that
// opens it: a key of this machine (the Secure Enclave, then a plugged-in YubiKey), else the
// agent at SSH_AUTH_SOCK (a nimble agent, also one forwarded over ssh). The key may ask for
// Touch ID or a PIN. Without a reachable key it returns an error that wraps ErrNoKey.
func (t *Token) Open() ([]byte, error) {
	openers := localOpeners()
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		openers = append(openers, &agentOpener{socket: sock})
	}
	return t.open(openers)
}

func (t *Token) open(openers []opener) ([]byte, error) {
	if !t.Sealed() {
		return t.openClear()
	}
	var errs []error
	prompt := "open the sealed token " + t.Name
	for _, o := range openers {
		for _, s := range t.stanzas {
			if !o.has(s.Recipient) {
				continue
			}
			kek, err := o.kek(s.Recipient, s.Ephemeral, prompt)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s through %s: %w", s.Name, o.name(), err))
				continue
			}
			secret, err := t.decrypt(s, kek)
			clear(kek)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s through %s: %w", s.Name, o.name(), err))
				continue
			}
			return secret, nil
		}
	}
	names := make([]string, len(t.stanzas))
	for i, s := range t.stanzas {
		names[i] = s.Name
	}
	err := fmt.Errorf("%w (token %s is sealed for %s)", ErrNoKey, t.Name, strings.Join(names, ", "))
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: %w", err, errors.Join(errs...))
	}
	return nil, err
}

// localKEK runs the key agreement for a stanza with a key of this machine. nimble agent
// uses it to answer requests.
func localKEK(r Recipient, eph []byte, prompt string) ([]byte, error) {
	for _, o := range localOpeners() {
		if o.has(r) {
			return o.kek(r, eph, prompt)
		}
	}
	return nil, ErrNoKey
}

// Probe runs one key agreement with the key of r on this machine and checks the result
// against the public key of r: it shows that this machine opens tokens sealed for r. The
// key asks for Touch ID or a PIN.
func Probe(r Recipient) error {
	if err := checkRecipient(r); err != nil {
		return err
	}
	pub, err := ecdh.P256().NewPublicKey(r.Public)
	if err != nil {
		return err
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return err
	}
	want, err := DeriveKEK(shared, eph.PublicKey().Bytes(), r.Public)
	if err != nil {
		return err
	}
	got, err := localKEK(r, eph.PublicKey().Bytes(), "test the sealing key "+r.Name)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("the key agreement on this machine gives another result than the public key: wrong key")
	}
	return nil
}

// Openable reports, without Touch ID or a PIN, which recipients of the token a key of
// this machine may open: a Secure Enclave key that is here, or a FIDO2 key while a
// YubiKey is plugged in.
func (t *Token) Openable() []string {
	var out []string
	for _, o := range localOpeners() {
		for _, s := range t.stanzas {
			if o.has(s.Recipient) {
				out = append(out, s.Name)
			}
		}
	}
	return out
}

// openClear returns the secret of a token in clear text, if only its owner can read the file.
func (t *Token) openClear() ([]byte, error) {
	if t.Path != "" {
		fi, err := os.Stat(t.Path)
		if err != nil {
			return nil, err
		}
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("the token in clear text %s can be read by others (mode %o): chmod 600 %s", t.Path, fi.Mode().Perm(), t.Path)
		}
	}
	return append([]byte(nil), t.clear...), nil
}

// Clear makes a token in clear text, for a machine without a key to seal with. Save writes
// it with mode 600.
func Clear(name string, secret []byte) (*Token, error) {
	if len(secret) == 0 || IsSealed(secret) {
		return nil, errors.New("a token in clear text needs a secret that is not a sealed token")
	}
	return &Token{Name: name, Labels: Labels{}, clear: append([]byte(nil), secret...)}, nil
}
