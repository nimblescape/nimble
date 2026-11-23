# nimble

nimble sets up a developer workstation (macOS, Debian or Ubuntu) and the Debian and Ubuntu hosts it reaches over SSH, and keeps them identical: the shell and prompt, linked config files, pinned language versions, tools, SSH keys, host hardening and services. Everything is described in a Git config repo of your own, checked out at `~/.nimble` on the workstation; hosts get a snapshot of just what they need. Private keys never leave their hardware, and nothing is installed unless a template asks for it.

How it all works in detail: [docs/how-it-works.md](docs/how-it-works.md).

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nimblescape/nimble/main/install.sh | sh
```

The installer puts the newest release in `~/.local/bin` and checks its checksum. Homebrew (`brew install nimblescape/tap/nimble`) and apt follow with the first public release. `nimble upgrade` updates nimble through whichever of these installed it, and so does `nimble update`.

## Get started

```bash
nimble init                           # set this machine up: asks whether to create your config repo (first machine) or clone it
nimble key enclave                    # this Mac's daily SSH key, in the Secure Enclave (Touch ID for every use)
nimble key yubikey                    # set up and register YubiKey backups, trusted by every host
nimble host create devbox             # describe a Debian host (guided); it offers to set it up
nimble init devbox                    # set it up and harden it (again any time)
nimble push                           # publish the changes
```

## Everyday use

```bash
nimble update                         # pull the config, apply it here, bring tools up to date
nimble update all                     # the same on every managed host
nimble tool select --host devbox      # pick a host's tools: check to install, uncheck to uninstall
nimble tool shell bash                # this machine's login shell: zsh, bash or the system's
nimble tool use go                    # pick a Go version from its releases and pin it
nimble tool list                      # the catalog, and updates of the pinned languages
nimble service add claude owner/app --host devbox   # a Claude Code server for a repo
nimble status; nimble doctor all      # what is installed vs. wanted; a health check
nimble update all --yubikey           # reach the hosts with a YubiKey backup (new Mac, lost or expired daily key)
nimble push                           # check, commit and publish config changes
```

Commands that change the config check it and commit it (`tool use` and `tool update` only edit `pins.yaml`; `push` commits that); `nimble push` publishes, and machines pick changes up with `nimble update`. On a host, `tool select`, `tool shell`, `service` and `host set|edit` change its own host file; the workstation's next `nimble update <host>` merges that into the config repo (a clash opens in the editor) and commits it. Any missing argument is asked for on a terminal: a list to pick from, an editable default, answers checked before they are taken. Hand edits to the YAML files are fine too: nimble validates them.

## Commands

| Command | What it does |
|---|---|
| `init [host…]` | Set this machine up (`--new` creates a config repo, `--repo` clones one), or a Debian host from the workstation |
| `update [host…\|all]` | Pull and apply here, or read back what managed hosts changed, send them their snapshot and nimble, then harden and apply there |
| `status`, `doctor [host…\|all]` | Wanted vs. installed (tools, and on a host its services: enabled, running); a health check of shell, links, keys, ssh config and hardening |
| `push [-m msg] [--diff]` | Check the YAML, pull, show what goes out (`--diff`: the full diff) and ask, push |
| `changes [--diff]` | What has changed and isn't published yet: what the next `push` publishes (on a host: what changed in its host file there) |
| `host create\|set\|edit\|list\|show\|remove` | The machines, one YAML file each; `create` (and `set <host> template=<name>`) copies a template's tools and shell in; `show` prints one with the defaults |
| `template create\|set\|edit\|list\|show\|default\|remove` | Starting points for a host's tools and login shell — copied, never referenced |
| `tool list`, `tool select [--host h]` | The tool catalog and updates of the pinned versions; pick a machine's tools (check to install, uncheck to uninstall), here or on a host from the workstation |
| `tool shell [zsh\|bash\|default] [--host h]` | Pick a machine's login shell (`default`: the system's) and apply it, here or on a host from the workstation |
| `tool use [language] [version]` | Pin Node, Go, Rust or Python to a version picked from its releases |
| `tool update [name…]` | Write bugfix updates of the pinned languages (feature updates on request) |
| `service list\|add\|enable\|disable\|remove [--host h]` | A Claude Code server per repo on a host |
| `key list\|enclave\|yubikey\|remove` | Daily keys (create, renew) and YubiKey backups (set up, register, renew); a renewal hands over: the new key is primary at once, the old one opens the hosts not updated yet until every host has the new one (`--handover`, default 14 days) |
| `domain list\|create` | Administration domains: which keys a group of hosts trusts |
| `version`, `upgrade`, `path`, `edit` | nimble itself and the config repo |

## The config repo

| Path | Holds |
|---|---|
| `nimble.yaml` | The default template, the domains (the first is the default), shared links |
| `hosts/<name>.yaml` | One machine: address, admin user, domain, mode, tools, shell, services, hardening level |
| `templates/<name>.yaml` | Tools and login shell that `host create` copies into a new host file; `base` installs nothing |
| `keys/` | The public keys of the hardware keys in use |
| `toolchain/pins.yaml` | The pinned languages, and the npm, Go and uv tools to install |
| `shell/` | Shared shell files for bash and zsh, and the starship prompt |
| `ssh/defaults` | Settings for every connection in the generated ssh config |
| `personal/` | Your own layer: git identity, app settings, extra links and shell lines |
| `.github/workflows/` | The daily pin updates: bugfix updates as one pull request, feature updates in an issue |

Keep the config repo private and in your own account; one in an organization's account is a configuration shared by its members. Not in the repo: `~/.nimble/local.sh` (this machine's shell additions), logins, tokens and `~/.ssh`.

## Requirements

A Mac, or a machine with Debian 12 (bookworm) or newer or Ubuntu 24.04 or newer, as the workstation, with `gh` logged in for GitHub (on a Mac also Homebrew; nimble installs `git`). Hosts run Debian 12 or newer, or Ubuntu 24.04 or newer — LTS and interim releases while Canonical supports them. Every supported release, new ones included, is tested weekly. A Mac with Touch ID, or a YubiKey, confirms SSH logins and sudo on the hosts.

## Development

Go, cobra, dependencies vendored, `CGO_ENABLED=0`. Commands are in `cmd/<name>/`, shared code in `pkg/`, the new-config-repo skeleton in `pkg/skeleton/`.

```bash
go test ./... && go build -o ~/.local/bin/nimble .
```

```bash
test/host.sh
```

```bash
test/workstation.sh
```

`test/host.sh` sets a host up in Debian and Ubuntu containers and checks the hardening, sudo with the shipped pam_rssh module and the commands that run under sudo. `test/workstation.sh` makes a Debian or Ubuntu container the first workstation (`init --new`, `host create`, the ssh config) and hardens it. Both need Docker and default to the oldest supported releases; pass images to test others (`test/images.sh` lists every supported release). The pam_rssh modules in `pkg/harden/module/` come from `build/pam_rssh/`.

With `export NIMBLE_SRC="$HOME/<path to this checkout>"` in `~/.nimble/local.sh`, `nimble upgrade` and every `nimble update` rebuild nimble from the checkout, and hosts get a Linux build of it instead of the release. Tagging `vX.Y.Z` builds the release (macOS and Linux binaries, Debian packages, checksums) with GoReleaser.

## License

[MIT](LICENSE)
