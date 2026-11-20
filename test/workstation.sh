#!/usr/bin/env bash
# Workstation test: a Debian or Ubuntu machine becomes the first workstation the way a
# person sets one up, in a throwaway Docker container per system. It runs what the unit
# tests can't: nimble init --new (a new config repo, the ssh client with FIDO support and
# ykman from apt), host create (the generated ssh config for managed hosts and ssh entries),
# and the hardening of the workstation itself once its domain trusts a key — first as root,
# then the way nimble init does it, through sudo confirmed by the key in the agent.
#
#   test/workstation.sh [image…]      default: debian:13 ubuntu:24.04
#   PLATFORM=linux/amd64 test/workstation.sh ubuntu:26.04     another CPU (emulated)
#
# Needs Docker and Go. A software key stands in for the hardware key.
set -euo pipefail
cd "$(dirname "$0")/.."
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(debian:13 ubuntu:24.04)
name=""
work=$(mktemp -d)
# KEEP=1 leaves the container of a failed run for a look inside (docker exec -it <name> bash)
trap '[ -n "${KEEP:-}" ] || [ -z "$name" ] || docker stop "$name" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

platform=${PLATFORM:-linux/$(docker version --format '{{.Server.Arch}}')}
arch=${platform#linux/}
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-X main.version=workstation-test" -o "$work/nimble" .
ssh-keygen -q -t ed25519 -N "" -C workstation-test -f "$work/key"

root() { docker exec "$name" sh -c "$1"; }
user() { docker exec "$name" su - dev -c "$1"; }
agent() { user 'eval "$(ssh-agent -s)" >/dev/null && ssh-add -q ~/key && { '"$1"'; }; rc=$?; ssh-agent -k >/dev/null; exit $rc'; }
fail() { echo "FAIL ($image): $1" >&2; exit 1; }
ok() { echo "  ok  $1"; }

for image in "${images[@]}"; do
  name=nimble-workstation-test-$(printf '%s-%s' "$image" "$arch" | tr -c 'a-zA-Z0-9' '-')
  echo "=== $image ($platform)"
  docker run -d --rm --platform "$platform" --hostname ws --name "$name" "$image" sleep 3600 >/dev/null
  # a fresh install: sudo and an ssh server, nothing of nimble's
  root 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends sudo openssh-server >/dev/null 2>&1' || fail "apt"
  root 'useradd -m -s /bin/bash dev && echo "dev:workstation-test-password" | chpasswd && usermod -aG sudo dev && mkdir -p /run/sshd && ssh-keygen -A >/dev/null'
  # until it is hardened, sudo doesn't ask (the test has no terminal to type a password in)
  root 'echo "dev ALL=(ALL) NOPASSWD: ALL" >/etc/sudoers.d/90-test && chmod 440 /etc/sudoers.d/90-test'
  root 'mkdir -p /home/dev/.local/bin'
  docker cp "$work/nimble" "$name":/home/dev/.local/bin/nimble
  docker cp "$work/key" "$name":/home/dev/key
  docker cp "$work/key.pub" "$name":/home/dev/key.pub
  root 'chown -R dev:dev /home/dev && chmod 600 /home/dev/key'
  user 'git --version >/dev/null 2>&1' && fail "git is already there: init must install it"
  n='~/.local/bin/nimble'

  # 1. the first workstation: a new config repo, git and the ssh client from apt — but not
  # before git knows who commits it
  user "$n init --new" </dev/null >"$work/init.log" 2>&1 && { cat "$work/init.log"; fail "init --new without a git identity"; }
  grep -q "git doesn't know who you are" "$work/init.log" && user 'test ! -e ~/.nimble' || { cat "$work/init.log"; fail "init --new without a git identity left something behind"; }
  user 'git config --global user.name dev && git config --global user.email dev@example.com'
  user "$n init --new" </dev/null >"$work/init.log" 2>&1 || { cat "$work/init.log"; fail "init --new"; }
  user 'grep -qx "mode: workstation" ~/.nimble/hosts/ws.yaml && grep -qx "format: 2" ~/.nimble/nimble.yaml && git -C ~/.nimble log --oneline | grep -q "new config repo"' ||
    { cat "$work/init.log"; fail "the new config repo"; }
  grep -q "isn't hardened yet" "$work/init.log" || { cat "$work/init.log"; fail "init didn't say the workstation isn't hardened yet"; }
  user 'ssh -V 2>&1 | grep -q OpenSSH && ykman --version >/dev/null' || fail "ssh client or ykman missing"
  ok "init --new: config repo, git, ssh client, ykman"

  # 2. hosts: a managed host and an ssh entry get their ssh config, each its own way
  user "$n host create devbox --mode managed --address devbox.example.com --user debian --domain default --template none --no-init" </dev/null >"$work/create.log" 2>&1 ||
    { cat "$work/create.log"; fail "host create devbox"; }
  user "$n host create legacy --mode ssh --address legacy.example.com --user admin --domain default --no-init" </dev/null >>"$work/create.log" 2>&1 ||
    { cat "$work/create.log"; fail "host create legacy"; }
  user 'head -5 ~/.ssh/config | grep -q "^Include ~/.nimble/cache/ssh_config"' || fail "~/.ssh/config doesn't include the generated config"
  user 'ssh -G devbox 2>/dev/null' >"$work/devbox.ssh"
  grep -qx "hostname devbox.example.com" "$work/devbox.ssh" && grep -qx "user debian" "$work/devbox.ssh" && grep -qx "forwardagent yes" "$work/devbox.ssh" ||
    { cat "$work/devbox.ssh"; fail "devbox's ssh config"; }
  user 'ssh -G legacy 2>/dev/null' >"$work/legacy.ssh"
  grep -qx "hostname legacy.example.com" "$work/legacy.ssh" && grep -qx "user admin" "$work/legacy.ssh" && grep -qx "forwardagent no" "$work/legacy.ssh" ||
    { cat "$work/legacy.ssh"; fail "legacy's ssh config"; }
  user 'git -C ~/.nimble log --oneline | grep -q "hosts: add devbox" && git -C ~/.nimble log --oneline | grep -q "hosts: add legacy"' || fail "host create didn't commit"
  ok "host create: ssh config for a managed host and an ssh entry, committed"

  # 3. the domain trusts a key: the workstation is hardened like a host (as root, the first time)
  root 'rm /etc/sudoers.d/90-test'
  user 'mkdir -p ~/.nimble/keys/yubikey && cp ~/key.pub ~/.nimble/keys/yubikey/test.pub && git -C ~/.nimble add keys && git -C ~/.nimble commit -qm "keys: test"'
  root '/home/dev/.local/bin/nimble harden --user dev --as ws' </dev/null >"$work/harden.log" 2>&1 || { cat "$work/harden.log"; fail "harden"; }
  grep -q "All good." "$work/harden.log" || { cat "$work/harden.log"; fail "the audit found problems"; }
  root 'test -f /etc/ssh/sshd_config.d/10-nimble.conf && grep -q "^auth sufficient libpam_rssh.so" /etc/pam.d/sudo && test -f /etc/security/authorized_keys/dev.keys' ||
    fail "a hardening file is missing"
  ok "harden: the audit passes"

  # 4. from now on nimble init (and update) harden it through sudo, confirmed by the key
  user 'sudo -k; sudo true </dev/null 2>/dev/null' && fail "sudo without agent and password succeeded"
  agent "sudo -k && $n init" </dev/null >"$work/init2.log" 2>&1 || { cat "$work/init2.log"; fail "init on the hardened workstation"; }
  grep -q "harden (syncs the keys)" "$work/init2.log" && grep -q "All good." "$work/init2.log" || { cat "$work/init2.log"; fail "init didn't harden through sudo"; }
  ok "init hardens through sudo with the key in the agent"

  # 5. doctor agrees
  agent "$n doctor" </dev/null >"$work/doctor.log" 2>&1 || { cat "$work/doctor.log"; fail "doctor"; }
  ok "doctor"

  docker stop "$name" >/dev/null
  name=""
done
echo "workstation test: all passed"
