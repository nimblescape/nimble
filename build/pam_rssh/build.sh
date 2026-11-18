#!/bin/sh
# Builds pam_rssh — the PAM module that confirms sudo with a hardware key through the
# forwarded SSH agent — from its pinned source, for the CPU this runs on.
#
# Runs as root in a fresh Debian 12 (bookworm) container, the oldest system nimble supports:
# a module built against its glibc and OpenSSL 3 also loads on newer Debian and on Ubuntu
# 24.04+. Started by docker.sh (locally) and by .github/workflows/pam-rssh.yml (CI).
#
#   build.sh <output folder>
#
# Writes libpam_rssh_<arch>.so (arch: amd64 | arm64) and, once, LICENSE and VERSION.
set -eu

# --- the pin: change these three to move to another pam_rssh release ---------------------
PAM_RSSH_VERSION=1.2.1
PAM_RSSH_COMMIT=e8dd046c423f85df266c23bcc3d7033bfee81255   # the commit of tag v1.2.1
RUST_VERSION=1.100.0
# ------------------------------------------------------------------------------------------

out=${1:?usage: build.sh <output folder>}
case $(uname -m) in
  x86_64) arch=amd64 target=x86_64-unknown-linux-gnu ;;
  aarch64) arch=arm64 target=aarch64-unknown-linux-gnu ;;
  *) echo "unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl git gcc libc6-dev libpam0g-dev libssl-dev pkg-config >/dev/null

export CARGO_HOME=/tmp/cargo RUSTUP_HOME=/tmp/rustup
curl -fsSL "https://static.rust-lang.org/rustup/dist/$target/rustup-init" -o /tmp/rustup-init
chmod +x /tmp/rustup-init
/tmp/rustup-init -y -q --no-modify-path --profile minimal --default-toolchain "$RUST_VERSION"

git -c advice.detachedHead=false clone -q --depth 1 --recurse-submodules --branch "v$PAM_RSSH_VERSION" https://github.com/z4yx/pam_rssh /tmp/pam_rssh
got=$(git -C /tmp/pam_rssh rev-parse HEAD)
[ "$got" = "$PAM_RSSH_COMMIT" ] || { echo "tag v$PAM_RSSH_VERSION is at $got, the pin says $PAM_RSSH_COMMIT — not building" >&2; exit 1; }

cd /tmp/pam_rssh
/tmp/cargo/bin/cargo build --release --locked -q
strip --strip-unneeded target/release/libpam_rssh.so

mkdir -p "$out"
install -m 644 target/release/libpam_rssh.so "$out/libpam_rssh_$arch.so"
install -m 644 LICENSE "$out/LICENSE"
printf '%s\n' "$PAM_RSSH_VERSION" >"$out/VERSION"
echo "pam_rssh $PAM_RSSH_VERSION ($PAM_RSSH_COMMIT), $(/tmp/cargo/bin/rustc --version), $arch:"
sha256sum "$out/libpam_rssh_$arch.so"
