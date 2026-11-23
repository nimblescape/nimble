//go:build !linux || !(amd64 || arm64)

package harden

// pamModule is empty where nimble doesn't harden (macOS) or ships no build.
var pamModule []byte
