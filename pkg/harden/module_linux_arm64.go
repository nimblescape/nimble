//go:build linux && arm64

package harden

import _ "embed"

//go:embed module/libpam_rssh_arm64.so
var pamModule []byte
