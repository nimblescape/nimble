#!/usr/bin/env bash
# The Debian and Ubuntu releases nimble supports, as Docker images, found anew on every run:
# every Debian release from 12 (bookworm) on that Docker Hub has, and every Ubuntu release
# from 24.04 on that Canonical still supports (LTS and interim). A new release — Debian 14,
# Ubuntu 26.04 or 26.10 — is picked up the week its image appears; point releases come with
# the image (each run pulls it fresh).
#
#   test/images.sh            one image per line
#   test/images.sh --next     also the releases in development (debian:testing, ubuntu:devel)
#   test/images.sh --json     a GitHub Actions matrix: {"include":[{"image":…,"next":…}]}
#
# Needs curl (and jq for --json).
set -euo pipefail

next=false json=false
for a in "$@"; do
  case $a in
    --next) next=true ;;
    --json) json=true ;;
    *) echo "usage: test/images.sh [--next] [--json]" >&2; exit 2 ;;
  esac
done

min_debian=12
min_ubuntu=24.04

# hub_has <repo> <tag>: the official image has this tag on Docker Hub
hub_has() { curl -fs -o /dev/null "https://hub.docker.com/v2/namespaces/library/repositories/$1/tags/$2"; }

stable=()

# Debian: numbered releases from the minimum up, until Docker Hub has no image for one
n=$min_debian
while hub_has debian "$n"; do
  stable+=("debian:$n")
  n=$((n + 1))
done
[ $n -gt $min_debian ] || { echo "no debian:$min_debian on Docker Hub — is the Hub reachable?" >&2; exit 1; }

# Ubuntu: the supported releases from Canonical's release lists (the ones do-release-upgrade
# reads); each block has "Version: 24.04.3 LTS" and "Supported: 1". A fresh release stays in
# the development list until upgrades to it open, so both are read; a release still in
# development has no numbered image yet (only ubuntu:devel), which keeps it out.
ubuntu=$(for list in meta-release meta-release-development; do
  curl -fsSL "https://changelogs.ubuntu.com/$list"; echo
done | awk -v min="$min_ubuntu" '
  /^Version:/   { split($2, v, "."); version = v[1] "." v[2] }
  /^Supported:/ { if ($2 == 1 && version + 0 >= min + 0) print version }
' | sort -uV)
[ -n "$ubuntu" ] || { echo "no supported Ubuntu release ≥ $min_ubuntu in the meta-release list" >&2; exit 1; }
for v in $ubuntu; do
  if hub_has ubuntu "$v"; then
    stable+=("ubuntu:$v")
  fi
done

dev=()
if $next; then
  dev=(debian:testing ubuntu:devel)
fi

if $json; then
  # ${a[@]+…}: an empty array is "unbound" to set -u in older bash (macOS's 3.2)
  {
    for i in "${stable[@]}"; do jq -cn --arg i "$i" '{image: $i, next: false}'; done
    for i in ${dev[@]+"${dev[@]}"}; do jq -cn --arg i "$i" '{image: $i, next: true}'; done
  } | jq -cs '{include: .}'
else
  printf '%s\n' "${stable[@]}" ${dev[@]+"${dev[@]}"}
fi
