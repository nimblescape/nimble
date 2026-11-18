//go:build linux && amd64

package harden

import _ "embed"

//go:embed module/libpam_rssh_amd64.so
var pamModule []byte
