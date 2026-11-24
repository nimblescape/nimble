package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The algorithms of version 1: ECDH on P-256, HKDF-SHA256, AES-256-GCM.
//
//	data key   32 random bytes per sealing
//	data       nonce (12 random bytes) || AES-256-GCM(data key, nonce, secret, header)
//	stanza     ephemeral P-256 key e; shared = ECDH(e, recipient) (the x coordinate)
//	           KEK = HKDF-SHA256(shared, salt = e.pub || recipient.pub, info = kekInfo)
//	           wrapped = AES-256-GCM(KEK, 12 zero bytes, data key, header)
//	header     the authenticated name and labels (see header)
//
// Each KEK encrypts exactly one data key, so a fixed nonce is safe.
const kekInfo = "nimble seal 1 key-wrapping key"

var errWrongKey = errors.New("the key doesn't open this token")

// header is the additional data that binds the name and the labels to the token: a
// changed name or label makes it unreadable.
func header(name string, labels Labels) []byte {
	var b strings.Builder
	b.WriteString("nimble seal 1\nname " + name + "\n")
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("label " + k + " " + labels[k] + "\n")
	}
	return []byte(b.String())
}

// DeriveKEK turns the ECDH shared secret of a stanza into its key-wrapping key. An agent
// that runs the key agreement for a stanza returns this key, not the raw shared secret.
func DeriveKEK(shared, ephemeral, public []byte) ([]byte, error) {
	salt := append(append(make([]byte, 0, len(ephemeral)+len(public)), ephemeral...), public...)
	return hkdf.Key(sha256.New, shared, salt, kekInfo, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (t *Token) encrypt(secret []byte, to []Recipient) error {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return err
	}
	defer clear(dek)
	ad := header(t.Name, t.Labels)
	gcm, err := newGCM(dek)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	t.data = gcm.Seal(nonce, nonce, secret, ad)
	t.stanzas = nil
	for _, r := range to {
		pub, err := ecdh.P256().NewPublicKey(r.Public)
		if err != nil {
			return fmt.Errorf("recipient %s: %w", r.Name, err)
		}
		eph, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		shared, err := eph.ECDH(pub)
		if err != nil {
			return fmt.Errorf("recipient %s: %w", r.Name, err)
		}
		kek, err := DeriveKEK(shared, eph.PublicKey().Bytes(), r.Public)
		clear(shared)
		if err != nil {
			return err
		}
		kgcm, err := newGCM(kek)
		clear(kek)
		if err != nil {
			return err
		}
		wrapped := kgcm.Seal(nil, make([]byte, kgcm.NonceSize()), dek, ad)
		t.stanzas = append(t.stanzas, stanza{Recipient: r, Ephemeral: eph.PublicKey().Bytes(), Wrapped: wrapped})
	}
	return nil
}

// decrypt opens the stanza s with its key-wrapping key and returns the secret.
func (t *Token) decrypt(s stanza, kek []byte) ([]byte, error) {
	ad := header(t.Name, t.Labels)
	kgcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	dek, err := kgcm.Open(nil, make([]byte, kgcm.NonceSize()), s.Wrapped, ad)
	if err != nil {
		return nil, errWrongKey
	}
	defer clear(dek)
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	if len(t.data) < gcm.NonceSize() {
		return nil, errors.New("the sealed data is too short")
	}
	n := gcm.NonceSize()
	secret, err := gcm.Open(nil, t.data[:n], t.data[n:], ad)
	if err != nil {
		return nil, errors.New("the sealed data is damaged, or its name or labels were changed")
	}
	return secret, nil
}

// fido2Key derives the P-256 key of a FIDO2 recipient from the hmac-secret of its credential.
// A scalar out of range (probability about 2^-32) takes the next counter.
func fido2Key(hmacSecret []byte) (*ecdh.PrivateKey, error) {
	for i := 0; i < 16; i++ {
		d, err := hkdf.Key(sha256.New, hmacSecret, nil, fmt.Sprintf("nimble seal 1 fido2 p256 %d", i), 32)
		if err != nil {
			return nil, err
		}
		if k, err := ecdh.P256().NewPrivateKey(d); err == nil {
			return k, nil
		}
	}
	return nil, errors.New("no P-256 key from this hmac-secret")
}
