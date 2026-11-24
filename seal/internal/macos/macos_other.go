//go:build !darwin

package macos

import "errors"

// Key is a private key on a token (macOS only).
type Key struct {
	Label       string
	Hash        []byte
	Token       string
	Public      []byte
	KeyExchange bool
}

var errNotMac = errors.New("token keys need macOS")

// TokenKeys lists nothing: tokens are a macOS feature.
func TokenKeys() ([]Key, error) { return nil, nil }

// ECDH fails: tokens are a macOS feature.
func ECDH(hash, peer []byte, prompt string) ([]byte, error) { return nil, errNotMac }

// Supported reports whether this build can reach the tokens of macOS.
const Supported = false
