#!/bin/sh
# Builds the pam_rssh modules nimble ships (pkg/harden/module/) on this machine, in Docker:
# one Debian 12 container per CPU, running build.sh. CI does the same in
# .github/workflows/pam-rssh.yml; use this to try a new pin before pushing it.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$(cd "$here/../.." && pwd)/pkg/harden/module
mkdir -p "$out"
for platform in ${PLATFORMS:-linux/amd64 linux/arm64}; do
  echo "--- $platform"
  docker run --rm --platform "$platform" -v "$here/build.sh:/build.sh:ro" -v "$out:/out" debian:bookworm sh /build.sh /out
done
ls -la "$out"
