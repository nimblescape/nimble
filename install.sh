#!/bin/sh
# Install nimble — https://github.com/nimblescape/nimble
#
#   curl -fsSL https://raw.githubusercontent.com/nimblescape/nimble/main/install.sh | sh
#
# Puts the newest release (or NIMBLE_VERSION=vX.Y.Z) into ~/.local/bin, checksum checked.
# Usual alternatives: brew install nimblescape/tap/nimble (macOS), apt (Debian, see README).
set -eu
repo=nimblescape/nimble

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux; [ -r /etc/debian_version ] || { echo "nimble: Linux means Debian only" >&2; exit 1; } ;;
  *) echo "nimble: macOS and Debian only" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "nimble: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac
command -v curl >/dev/null || { echo "nimble: needs curl (sudo apt install curl)" >&2; exit 1; }

version=${NIMBLE_VERSION:-$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
  sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)}
[ -n "$version" ] || { echo "nimble: no release found" >&2; exit 1; }

file="nimble_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
cd "$tmp"
grep "  $file\$" checksums.txt >expected || { echo "nimble: $file not in checksums.txt" >&2; exit 1; }
if command -v sha256sum >/dev/null; then sha256sum -c expected >/dev/null; else shasum -a 256 -c expected >/dev/null; fi
tar -xzf "$file" nimble
mkdir -p "$HOME/.local/bin"
install -m 755 nimble "$HOME/.local/bin/nimble"

echo "nimble $version → ~/.local/bin/nimble"
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo "(~/.local/bin isn't in PATH yet — run it as ~/.local/bin/nimble)" ;; esac
echo "next: nimble init   (asks whether to create your config repo or clone it)"
