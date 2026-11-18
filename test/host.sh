#!/usr/bin/env bash
# Host test: sets a Debian host up the way a workstation does and checks the result, in a
# throwaway Docker container per system. It runs what the unit tests can't: apply and the
# full hardening as root, the pam_rssh module nimble ships (sudo confirmed through an SSH
# agent, the password as fallback), the audit, and the commands that run under sudo.
#
#   test/host.sh [image…]      default: debian:bookworm debian:trixie ubuntu:24.04
#   PLATFORM=linux/amd64 test/host.sh debian:bookworm     another CPU (emulated)
#
# Needs Docker and Go. A software key stands in for the hardware key.
set -euo pipefail
cd "$(dirname "$0")/.."
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(debian:bookworm debian:trixie ubuntu:24.04)
name=""
work=$(mktemp -d)
# KEEP=1 leaves the container of a failed run for a look inside (docker exec -it <name> bash)
trap '[ -n "${KEEP:-}" ] || [ -z "$name" ] || docker stop "$name" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

platform=${PLATFORM:-linux/$(docker version --format '{{.Server.Arch}}')}
arch=${platform#linux/}
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-X main.version=host-test" -o "$work/nimble" .
ssh-keygen -q -t ed25519 -N "" -C host-test -f "$work/key"

# the snapshot a workstation would send for the host "devbox"
snap=$work/snap
mkdir -p "$snap"/{hosts,templates,toolchain,keys/yubikey}
cp -R pkg/skeleton/files/shell "$snap/shell"
cp pkg/skeleton/files/toolchain/pins.yaml "$snap/toolchain/"
cp "$work/key.pub" "$snap/keys/yubikey/test.pub"
printf 'address: devbox.example.com\nuser: debian\ntemplate: plain\nservices:\n  - service: claude\n    repo: owner/app\n    state: enabled\n' >"$snap/hosts/devbox.yaml"
printf 'description: no tools\ntools: []\n' >"$snap/templates/plain.yaml"
printf 'defaultTemplate: plain\ndomains:\n  main:\n    label: ssh\nlinks: []\n' >"$snap/nimble.yaml"
printf 'host: devbox\nfrom: host-test\ncommit: test\ndate: 2026-01-01T00:00:00Z\n' >"$snap/.snapshot"

root() { docker exec "$name" sh -c "$1"; }
user() { docker exec "$name" su - debian -c "$1"; }
fail() { echo "FAIL ($image): $1" >&2; exit 1; }
ok() { echo "  ok  $1"; }

for image in "${images[@]}"; do
  name=nimble-host-test-$(printf '%s' "$image" | tr -c 'a-zA-Z0-9' '-')
  echo "=== $image ($platform)"
  docker run -d --rm --platform "$platform" --name "$name" "$image" sleep 3600 >/dev/null
  root 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends sudo openssh-server openssh-client git ca-certificates >/dev/null 2>&1' || fail "apt"
  root 'id debian >/dev/null 2>&1 || useradd -m -s /bin/bash debian; echo "debian:host-test-password" | chpasswd; usermod -aG sudo debian; useradd -m -s /bin/bash other; usermod -aG sudo other; mkdir -p /run/sshd; ssh-keygen -A >/dev/null'
  root 'mkdir -p /home/debian/.local/bin /home/debian/.nimble'
  docker cp "$work/nimble" "$name":/home/debian/.local/bin/nimble
  docker cp "$snap/." "$name":/home/debian/.nimble/
  docker cp "$work/key" "$name":/home/debian/key
  root 'chown -R debian:debian /home/debian && chmod 600 /home/debian/key'
  n='~/.local/bin/nimble'

  # 1. apply as the admin, then the full hardening as root — nothing is built on the host
  user "$n apply --as devbox" >"$work/apply.log" 2>&1 || { cat "$work/apply.log"; fail "apply"; }
  ok "apply"
  root '/home/debian/.local/bin/nimble harden --user debian --as devbox' >"$work/harden.log" 2>&1 || { cat "$work/harden.log"; fail "harden"; }
  grep -q "All good." "$work/harden.log" || { cat "$work/harden.log"; fail "the audit found problems"; }
  if grep -q "apt install\|build pam_rssh" "$work/harden.log"; then cat "$work/harden.log"; fail "harden installed or built something"; fi
  ok "harden: the audit passes, nothing built"
  root 'test -f /etc/ssh/sshd_config.d/10-nimble.conf && grep -q "^auth sufficient libpam_rssh.so" /etc/pam.d/sudo && ls /usr/lib/*/security/libpam_rssh.so >/dev/null && grep -q "nimble keys" /home/debian/.ssh/authorized_keys && test -f /etc/security/authorized_keys/debian.keys' || fail "a hardening file is missing"
  root 'test "$(cut -d" " -f1 /usr/local/share/pam_rssh.version)" = "'"$(cat pkg/harden/module/VERSION)"'"' || fail "pam_rssh marker"
  root 'id -nG other | grep -qvw sudo' || fail "other is still in group sudo"
  ok "sshd, PAM, keys, sudo group"

  # 2. the module: sudo through the agent (no password), the password without it, nothing else
  # (not sudo -n: it gives up before PAM is asked)
  user 'eval "$(ssh-agent -s)" >/dev/null && ssh-add -q ~/key && sudo -k && sudo true </dev/null; rc=$?; ssh-agent -k >/dev/null; exit $rc' || fail "sudo with the key in the agent"
  ok "sudo confirmed through the SSH agent (pam_rssh)"
  user 'sudo -k; sudo true </dev/null 2>/dev/null' && fail "sudo without agent and password succeeded"
  user 'sudo -k; echo host-test-password | sudo -S -p "" true 2>/dev/null' || fail "sudo with the password"
  ok "sudo falls back to the password, and to nothing else"

  # 3. a second run changes nothing
  root '/home/debian/.local/bin/nimble harden --user debian --as devbox' >"$work/harden2.log" 2>&1 || { cat "$work/harden2.log"; fail "harden, second run"; }
  if grep -q "^==>" "$work/harden2.log"; then cat "$work/harden2.log"; fail "the second harden run changed something"; fi
  ok "harden is idempotent"

  # 4. what runs under sudo finds the admin's config (sudo resets HOME to /root)
  user 'eval "$(ssh-agent -s)" >/dev/null && ssh-add -q ~/key && sudo ~/.local/bin/nimble service sync --as devbox; rc=$?; ssh-agent -k >/dev/null; exit $rc' >"$work/sync.log" 2>&1 || { cat "$work/sync.log"; fail "service sync"; }
  grep -q "owner/app is not installed here yet" "$work/sync.log" || { cat "$work/sync.log"; fail "service sync didn't read the host file"; }
  root 'test ! -e /root/.nimble' || fail "/root/.nimble was created"
  ok "service sync under sudo reads the admin's host file"

  # 5. a host holds a snapshot: config changes are refused before anything is written
  user "cd ~/.nimble && $n host set devbox shell=zsh" >/dev/null 2>&1 && fail "host set on a snapshot host"
  user "cd ~/.nimble && $n tool select" >/dev/null 2>&1 && fail "tool select on a snapshot host"
  user 'grep -q shell ~/.nimble/hosts/devbox.yaml' && fail "the snapshot was edited"
  ok "config changes are refused on the host"

  # 6. doctor agrees (bash only: no zsh dotfiles expected)
  user "$n doctor" >"$work/doctor.log" 2>&1 || { cat "$work/doctor.log"; fail "doctor"; }
  ok "doctor"

  docker stop "$name" >/dev/null
  name=""
done
echo "host test: all passed"
