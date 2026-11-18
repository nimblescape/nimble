#!/usr/bin/env bash
# Enable Touch ID for sudo on this Mac. Run it yourself: ./macos/touchid-sudo.sh
#
# Uses /etc/pam.d/sudo_local, which macOS keeps across system updates.
# The password still works as a fallback (e.g. over ssh or with the lid closed).
set -euo pipefail

[ "$(id -u)" -eq 0 ] || exec sudo "$0" "$@"

f=/etc/pam.d/sudo_local
if [ -f "$f" ] && grep -Eq '^auth[[:space:]].*pam_tid\.so' "$f"; then
  echo "Touch ID for sudo is already enabled ($f)"
  exit 0
fi

tmp=$(mktemp)
sed -E 's/^#[[:space:]]*(auth[[:space:]].*pam_tid\.so)/\1/' /etc/pam.d/sudo_local.template >"$tmp"
grep -Eq '^auth[[:space:]].*pam_tid\.so' "$tmp" || echo 'auth       sufficient     pam_tid.so' >>"$tmp"
install -m 444 -o root -g wheel "$tmp" "$f"
rm -f "$tmp"
echo "Touch ID for sudo enabled ($f)"
echo "Note: inside tmux this needs pam_reattach; in iTerm2 turn off"
echo "'Allow sessions to survive logging out and back in' (Settings → Advanced)."
