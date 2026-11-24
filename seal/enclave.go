package seal

import (
	"bytes"
	"crypto/ecdh"

	"github.com/nimblescape/nimble/seal/internal/macos"
)

// EnclaveKey is a private key that macOS keeps on a token: the Secure Enclave (keys made
// with sc_auth create-ctk-identity) or a smart card.
type EnclaveKey struct {
	Label       string
	Token       string // the token ID, e.g. com.apple.setoken
	Hash        []byte // the public key hash: a recipient's handle
	Public      []byte // the P-256 public key (65 bytes), if it is one
	KeyExchange bool   // the token runs ECDH with it: tokens can be sealed for it
}

// Recipient is the recipient for this key, named name.
func (k EnclaveKey) Recipient(name string) Recipient {
	return Recipient{Name: name, Kind: KindEnclave, Public: k.Public, Handle: k.Hash}
}

// EnclaveKeys lists the token keys of this Mac (none elsewhere). It needs no Touch ID.
func EnclaveKeys() ([]EnclaveKey, error) {
	keys, err := macos.TokenKeys()
	if err != nil {
		return nil, err
	}
	out := make([]EnclaveKey, 0, len(keys))
	for _, k := range keys {
		e := EnclaveKey{Label: k.Label, Token: k.Token, Hash: k.Hash, KeyExchange: k.KeyExchange}
		if len(k.Public) == 65 && k.Public[0] == 4 {
			e.Public = k.Public
		}
		out = append(out, e)
	}
	return out, nil
}

// enclaveOpener runs the key agreement in the Secure Enclave of this Mac.
type enclaveOpener struct {
	listed bool
	keys   []EnclaveKey
}

func (o *enclaveOpener) name() string { return "the Secure Enclave of this Mac" }

func (o *enclaveOpener) has(r Recipient) bool {
	if r.Kind != KindEnclave || !macos.Supported {
		return false
	}
	if !o.listed {
		o.keys, _ = EnclaveKeys()
		o.listed = true
	}
	for _, k := range o.keys {
		if k.KeyExchange && bytes.Equal(k.Hash, r.Handle) && bytes.Equal(k.Public, r.Public) {
			return true
		}
	}
	return false
}

func (o *enclaveOpener) kek(r Recipient, eph []byte, prompt string) ([]byte, error) {
	if _, err := ecdh.P256().NewPublicKey(eph); err != nil { // checked here, not only by macOS
		return nil, err
	}
	shared, err := macos.ECDH(r.Handle, eph, "nimble: "+prompt)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	return DeriveKEK(shared, eph, r.Public)
}

func (o *enclaveOpener) offers() []agentKey {
	keys, _ := EnclaveKeys()
	var out []agentKey
	for _, k := range keys {
		if k.KeyExchange && k.Public != nil {
			out = append(out, agentKey{kind: KindEnclave, public: k.Public})
		}
	}
	return out
}

// EnclaveSupported reports whether this build reaches the Secure Enclave (macOS).
func EnclaveSupported() bool { return macos.Supported }
