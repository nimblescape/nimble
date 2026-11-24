package macos

import "errors"

// ErrNoKey says that no token key with the given public key hash is here.
var ErrNoKey = errors.New("no such key on this Mac")
