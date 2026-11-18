package harden

import (
	_ "embed"
	"strings"
)

// nimble ships pam_rssh, the PAM module that confirms sudo with a hardware key through the
// forwarded SSH agent: build/pam_rssh builds it from its pinned source for amd64 and arm64
// (on Debian 12, so it also loads on newer systems), the results live in module/, and the
// Linux builds of nimble carry the one for their CPU. A host never builds or downloads it.

//go:embed module/VERSION
var pamVersion string

// PamVersion is the pam_rssh version this nimble ships.
func PamVersion() string { return strings.TrimSpace(pamVersion) }
