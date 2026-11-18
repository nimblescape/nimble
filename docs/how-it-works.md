# How nimble works

nimble is one Go binary with two jobs. On a **workstation** (macOS or Debian) it keeps a checkout of your config repo at `~/.nimble`, applies it to the workstation itself, and sets up and updates the **hosts** over SSH. On a **host** (Debian) it applies a snapshot of the config that the workstation sends. This document describes what each part does, step by step, with the files and commands involved. For installing and everyday use, see the [README](../README.md).

## Contents

1. [The model](#the-model)
2. [The config repo](#the-config-repo)
3. [Applying the config to a machine](#applying-the-config-to-a-machine)
4. [Tools](#tools)
5. [Pinned languages](#pinned-languages)
6. [Hosts](#hosts)
7. [Hardening](#hardening)
8. [Keys and domains](#keys-and-domains)
9. [Services: the Claude Code server](#services-the-claude-code-server)
10. [Checking: status and doctor](#checking-status-and-doctor)
11. [nimble itself](#nimble-itself)
12. [Reference: environment and files](#reference-environment-and-files)

## The model

```
 edit (commands or by hand)        nimble push              nimble update
 workstation: ~/.nimble  ──commit──▶  GitHub (config repo)  ──pull──▶  every workstation
        │
        │ nimble update <host> | all
        ▼
 host: ~/.nimble = snapshot  +  ~/.local/bin/nimble   →  apply, harden, service sync
```

- **Workstation**: the machine with the config checkout. It is a Mac, or a Debian machine whose host file says `mode: workstation`. It generates the ssh config for the hosts and is the only place config changes are made. A Debian workstation is hardened like a host, once it has a host file and a key to trust.
- **Managed host**: a Debian machine in `hosts/` with mode `managed` (the default). nimble sets it up, hardens it and keeps it updated. It never sees the config repo, only a snapshot of what it needs.
- **ssh entry**: a host with `mode: ssh`. It only gets an entry in the workstation's ssh config.
- Commands that change an inventory write the file and commit it locally (`git add --all` of the directories they changed, then `git commit`). `tool use`, `tool update` and `edit` only write files; `nimble push` commits those. `nimble push` commits the hand edits, always pulls, then shows what goes out — the commits and `git diff --stat` against the upstream; with `--diff` also the full diff, in git's pager — and on a terminal asks; declined (or Esc, or nothing pulled), the hand edits' commit is undone and they are uncommitted again. Their commit message is asked for after the yes (or given with `-m`). A push rejected because another machine pushed meanwhile pulls and pushes again. Machines pick changes up with `nimble update`. `nimble changes` shows what the next push publishes without committing or pushing anything (`--diff` adds the full diff, in git's pager), for the config repo and a personal layer with its own git repo.
- **Conflicts.** Every machine pushes to the config repo's `main`; `push` (with the hand edits committed first, so nothing goes through git's stash) and a local `update` (hand edits stay uncommitted: `--autostash`) pull with `git pull --rebase`. Changes to different files, or different lines of one file, merge by themselves. When the same lines were changed here and on another machine, the rebase is aborted — the repo is exactly as before, never half-rebased — and nimble names the files and the commits on both sides. On a terminal it offers to resolve them: the files open in your editor (see "The editor"), and once they have no conflict markers left and the YAML is valid, the rebase goes on with your commits on top; stopped, everything goes back to how it was. Without a terminal it stops with an error. In a local `update`, uncommitted edits that clash with what was pulled (the pull itself goes through): resolved in the editor the same way, they stay uncommitted; otherwise the files are exactly what was pulled and the edits wait in git's stash (`git -C ~/.nimble stash pop`).
- **Unpublished changes.** After every command on a machine with the config checkout, nimble warns on stderr while something isn't published yet ("1 commit not published yet — the other machines don't have it: nimble push"). It leaves out shell completion, help (`help`, `--help`) and `changes` (which ends with that warning itself); hosts, with a snapshot instead of a checkout, never warn.
- **Prompts.** Lists filter as you type; Esc or Ctrl+C leaves any prompt and ends nimble gracefully (exit status 0), without changing anything further.
- **Output.** `---` starts a step, `==>` reports a change. A step that only waits (a build or download, an install, sending nimble or the snapshot to a host, release lookups) shows a spinner on a terminal: `⠹ send nimble` while it runs, then `✓ send nimble  2.1s` (or `✗` when it fails). Anything that prints meanwhile — nimble's own lines and prompts, a command's output, ssh asking for a key touch, sudo about to ask for a password — stops the spinner first and leaves a plain `--- send nimble` line, so a prompt is never drawn over. Without a terminal (pipes, CI) a waiting step is a plain `---` line; `tool update --markdown` prints none.

## The config repo

The repo lives at `$NIMBLE_HOME`, by default `~/.nimble` (`nimble path` prints it). `nimble init` asks whether to create it from a skeleton built into nimble (your first machine) or to clone yours; `--new` and `--repo owner/name` answer that up front, for scripts.

| Path | Holds |
|---|---|
| `nimble.yaml` | `defaultTemplate`, `domains`, shared `links` |
| `hosts/<name>.yaml` | One machine each, workstations included |
| `templates/<name>.yaml` | Which tools and login shell a machine gets |
| `keys/enclave/<machine>-<label>.pub` | A workstation's daily key (Secure Enclave) |
| `keys/yubikey/<name>.pub` | A YubiKey backup |
| `toolchain/pins.yaml` | The pinned languages and the npm, Go and uv tools |
| `shell/` | Shared shell files: `env.sh`, `common.sh`, `zshrc`, `bashrc`, `zsh/*.zsh`, `bash/*.bash`, `starship.toml` |
| `ssh/defaults` | Settings appended to the generated ssh config |
| `personal/` | The personal layer: `nimble.yaml` (links), `shell/`, app settings such as `macos/iterm2/` |
| `macos/touchid-sudo.sh` | Turns on Touch ID for sudo on a Mac (run by hand) |
| `.github/workflows/patch-updates.yml` | The daily pin updates |
| `local.sh`, `cache/`, `backup/` | This machine only, ignored by git (see [Reference](#reference-environment-and-files)) |

All YAML is parsed strictly: an unknown key or a wrong type is an error. Keys are camelCase. `apply` and `push` validate the whole repo first, `toolchain/pins.yaml` included, and stop on any problem; `nimble doctor` lists them.

### nimble.yaml

```yaml
defaultTemplate: dev        # template for machines whose host file names none
domains:                    # in order; the first is the default
  main:
    label: ssh              # label of the domain's daily key
  lab:
    label: lab
links:                      # files linked into $HOME on every machine
  - from: git/ignore        # relative to the repo
    to: .config/git/ignore  # relative to $HOME
    os: macos               # optional: macos | debian
```

An **administration domain** decides which keys a host trusts (see [Keys and domains](#keys-and-domains)). There must be at least one, and each needs a label.

### Host files

| Key | Values | Default |
|---|---|---|
| `address` | hostname or IP | required, except for `mode: workstation` |
| `user` | the admin user on the host | `debian` |
| `domain` | a domain from `nimble.yaml` | the first domain |
| `mode` | `managed`, `ssh`, `workstation` | `managed` |
| `template` | a template from `templates/` | `defaultTemplate` |
| `tools` | extra tools from the catalog | none |
| `shell` | `zsh`, `bash` (overrides the template's) | the template's |
| `services` | list of `{service: claude, repo: owner/name, state: enabled\|disabled}` | none |
| `harden` | `full`, or `sudo` for a host someone else manages | `full` |
| `sudoAllowed` | accounts that keep their sudo rights, e.g. a service account | none |

```yaml
# hosts/devbox.yaml
address: devbox.example.com
user: debian
template: dev
tools: [docker]
services:
  - service: claude
    repo: owner/app
    state: enabled
```

`host create` asks for mode, address, admin user, domain and template, checks and writes the file, regenerates the ssh config (on a workstation) and offers `nimble init <name>`. A host name has lower-case letters, digits, `.`, `-` and `_`. `host set <name> key=value…` changes `address`, `user`, `domain`, `mode`, `template`, `shell` or `tools` (comma list; an empty value removes the key), validates the whole repo and restores the file if that fails. `host edit` opens the file in your editor (see "The editor" below) and validates it when you close it. `host show <name>` prints the configuration as nimble applies it: the file's settings, the defaults it leaves out (`mode: managed`, `user: debian`, the first domain, `harden: full`; on a workstation no admin user, domain or hardening) and its template's tools and shell, each value not from the file marked with where it comes from (`# default`, `# template dev`); the file's leading comments are kept. `host remove` deletes the file; the host itself is not touched.

### Templates

| Key | Values | Default |
|---|---|---|
| `description` | free text, shown by `template list` | none |
| `os` | `macos`, `debian` | both |
| `shell` | `zsh`, `bash` | none: the login shell stays as it is |
| `tools` | names from the tool catalog | none: nothing is installed |

How a machine's setup is resolved:

1. The template is the host file's `template`, else `defaultTemplate`. A machine without a host file uses `defaultTemplate`.
2. If the template has an `os` and it isn't this machine's, apply stops.
3. The tools are the template's tools plus the host file's `tools`, without duplicates.
4. The login shell is the host file's `shell`, else the template's; with neither, it stays as it is.

`template create <name>` (`--from`, `--tools`, `--shell`, `--os`, `--description`, `--edit`), `set`, `edit`, `default <name>` and `remove` manage them. A template can't be removed while it is the default or a machine uses it. The skeleton ships `base` (no tools) as the default and `dev` (starship, node, go, python).

### The editor

`host edit`, `template edit` (and `template create --edit`) and `nimble edit` open VS Code when it is installed (`code` on PATH, or inside the app in `/Applications` or `~/Applications` on macOS) and nimble runs on a desktop: macOS, or Linux with a display (`DISPLAY` or `WAYLAND_DISPLAY`), and not in an ssh session. VS Code's own terminal counts too, also over Remote-SSH. A file opens with `code --wait`, so nimble checks it once its tab is closed; the config repo opens as a folder without waiting. Everywhere else they use `$VISUAL`, or `vi` when it is unset. This is only for nimble's `edit` commands: git and the shell keep `$VISUAL` from `shell/common.sh`.

### The personal layer

`personal/` holds one person's own settings inside the config repo: links in `personal/nimble.yaml` (sources relative to `personal/`), shell lines in `personal/shell/common.sh`, `personal/shell/zsh/*.zsh` and `personal/shell/bash/*.bash`, and app settings. nimble writes no git identity itself; link your git config from here (the skeleton's `personal/nimble.yaml` has the example `from: git/config`, `to: .config/git/config`). If `personal/` is a git repo of its own, `nimble update` pulls it and `nimble push` publishes it.

## Applying the config to a machine

`nimble init` first makes sure git is there, since creating or cloning the config repo needs it: on a Mac it runs Homebrew's official installer (brew.sh) in the terminal when Homebrew is missing — it asks for your password and brings Xcode's Command Line Tools, git among them (`/usr/bin/git` is only a stub without them); on Debian it installs `git` and `ca-certificates`. Any later step that needs Homebrew does the same; only without a terminal does it stop and say to install Homebrew. `nimble apply` does the work. `nimble init` runs it once the repo is there; on Debian it hardens this machine first, if it has a host file and a trusted key. A local `nimble update` pulls the repo (and a personal layer with its own git repo), runs apply with `--upgrade`, on Debian hardens and syncs the services under the same condition, and upgrades a released nimble when there is a newer release. On hosts, the workstation runs it. macOS and Debian share one path. In order:

1. **Checks.** Load and validate the config; stop on any problem. Stop on a system other than macOS or Debian (Debian means `/etc/debian_version` exists), and on Debian when run as root.
2. **This machine's host file.** The name comes from `--as` (remembered in `cache/host`), else the remembered name, else the machine name (`scutil --get LocalHostName` on macOS, the hostname elsewhere; lowercased, up to the first dot). Without a host file the default template applies.
3. **Resolve** template, tools and login shell (see [Templates](#templates)).
4. **Debian:** install `git` and `ca-certificates` if missing.
5. **Dotfiles:** write `~/.zshenv`, `~/.zprofile`, `~/.zshrc`, `~/.bashrc`, `~/.profile` (see [Shell](#shell)).
6. **Links** from `nimble.yaml` and `personal/nimble.yaml`.
7. **ssh config** for the hosts, on a workstation only.
8. **`~/.gitconfig`:** created with a comment if missing; a note if it repeats a `[user]` section.
9. **Login shell** set to what the template or host file asks for.
10. **Tools:** on a workstation first the ssh for the hardware keys (see [The ssh client](#the-ssh-client)); then install the resolved tools; with `--upgrade`, then update the ones that stay current (see [Tools](#tools)). `--no-tools` skips this step. A tool that fails to install is reported, the rest still runs, and apply ends with an error.
11. **Last:** on macOS, point iTerm2 at its settings in the repo; with `NIMBLE_SRC` set (and Go installed), rebuild nimble from its source.

Every step is idempotent: it compares, changes only what differs, and says what it changed. Files it replaces go to `~/.nimble/backup/<YYYYMMDD-HHMMSS>/`, keeping their path relative to `$HOME`.

### Shell

**Dotfiles.** nimble owns the five dotfiles in `$HOME`. Each starts with a managed part, then keeps everything below a marker:

```sh
# Managed by nimble — don't edit this part; nimble rewrites it.
#   every machine: ~/.nimble/shell/common.sh  (zsh only: shell/zsh/, bash only: shell/bash/)
#   this machine:  ~/.nimble/local.sh
source "$NIMBLE_HOME/shell/zshrc"

# ── Added by other tools (nimble keeps these as they are) ──
```

- The managed part is rewritten on every apply; lines that installers append below the marker stay. `nimble status` counts them.
- A dotfile that nimble didn't write is moved to the backup folder first; its lines are not carried over.
- The zsh files are written on macOS, when the login shell is zsh, or when zsh is installed. Otherwise a zsh file that nimble wrote and nothing was added to is removed.
- If `~/.bash_profile` exists, apply warns: bash login shells then skip `~/.profile`.

**Load order.**

| Shell | Order |
|---|---|
| zsh | `~/.zshenv` (sets `NIMBLE_HOME`, sources `shell/env.sh`) → `~/.zprofile` (login shells: `env.sh` again, after macOS's `path_helper`) → `~/.zshrc` → `shell/zshrc`: `shell/common.sh`, `personal/shell/common.sh`, `shell/zsh/*.zsh`, `personal/shell/zsh/*.zsh`, the prompt, `local.sh` |
| bash | `~/.profile` (login shells: `NIMBLE_HOME`, `env.sh`, then `~/.bashrc`) → `~/.bashrc` → `shell/bashrc`: `env.sh`, stop if not interactive, `common.sh`, `personal/shell/common.sh`, `shell/bash/*.bash`, `personal/shell/bash/*.bash`, the prompt, `local.sh` |

- `shell/env.sh` sets PATH (`~/.cargo/bin`, `~/.local/bin`, `~/.local/go/bin` first, Homebrew on macOS (`/opt/homebrew`, or `/usr/local` on Intel), `~/go/bin` last), `NVM_DIR=~/.nvm`, `UV_PYTHON_PREFERENCE=only-managed` and `STARSHIP_CONFIG=$NIMBLE_HOME/shell/starship.toml`.
- The prompt is starship when it is installed; otherwise a plain prompt that shows `user@host` in bold yellow over SSH.
- `local.sh` (not in git) is loaded last: this machine's own additions. `nimble edit local` opens it.
- Completions: both shells complete `nimble`, `gh`, `uv`, `uvx`, `rustup`, `cargo`, `docker`, `nvm`, `gcloud` and `firebase` (its top-level commands, from the skeleton's own list); zsh also completes the arguments of gh extensions and, on macOS, Homebrew's and Docker Desktop's completions. bash loads the system's bash-completion first — Debian's package, or Homebrew's `bash-completion@2` on macOS, which also brings the formulae's completions. Generated completions are cached in `~/.nimble/cache/zsh` and `~/.nimble/cache/bash` and made again only when the tool changes.
- These shell files belong to the config repo: the skeleton writes them once, then they are yours.

**Login shell.**

| Asked for | macOS | Debian |
|---|---|---|
| nothing | unchanged | unchanged (back to bash if it was the zsh launcher); `bash-completion` installed where sudo works (otherwise a warning) |
| `zsh` | `/bin/zsh` | `/usr/local/bin/nimble-shell`, the zsh launcher |
| `bash` | Homebrew's bash and `bash-completion@2` (installed if missing) | `/bin/bash`; `bash-completion` installed where sudo works (otherwise a warning) |

The shell is added to `/etc/shells` if needed and set with `chsh`.

**zsh on Debian, fail-safe.** With zsh as the login shell on Debian, nimble installs zsh and three files:

- `/usr/local/bin/nimble-shell`: starts zsh when `zsh -fc true` works, otherwise bash, with `$SHELL` set to match. Logging in never fails, even with zsh broken or removed.
- `/usr/local/bin/nimble-apt-guard` and `/etc/apt/apt.conf.d/50nimble-zsh-guard`: an APT hook (`DPkg::Pre-Install-Pkgs`) that refuses to remove zsh through apt while any user's login shell is the launcher, and names the clean way (`nimble host set <host> shell=bash`, then `nimble update`).

Installers are kept from editing shell files: rustup runs with `--no-modify-path`, uv's installer with `UV_NO_MODIFY_PATH=1`.

### Links

Each link symlinks `$HOME/<to>` to `<layer>/<from>` (absolute paths). Links with an `os` that doesn't match are skipped. A missing source fails validation. A symlink pointing elsewhere is replaced; any other file there is moved to the backup folder first. Removing a link from `nimble.yaml` does not delete the symlink.

### ssh config

On a workstation, apply writes `~/.nimble/cache/ssh_config` and, unless `~/.ssh/config` already includes it, adds `Include ~/.nimble/cache/ssh_config` at its top (the old file is backed up first). The generated file has one entry per host in `hosts/` with an address (workstations excluded), sorted by name, then `ssh/defaults`:

```
Host devbox
  HostName devbox.example.com
  User debian
  IdentityFile ~/.ssh/id_ecdsa_sk_rk_ssh
  ForwardAgent yes
  AddKeysToAgent yes
```

- `IdentityFile` is the handle of the domain's daily key (see [Keys and domains](#keys-and-domains)).
- `ForwardAgent` and `AddKeysToAgent` are set for managed hosts only, so a plain `ssh devbox` forwards the agent that confirms sudo there. `mode: ssh` entries get neither.
- The skeleton's `ssh/defaults` adds connection sharing (`ControlMaster auto`, `ControlPersist 5m`), `IdentitiesOnly yes`, the default daily key and the Secure Enclave key provider.
- The generated file starts with `IgnoreUnknown UseKeychain`: that option is known only to macOS's own ssh, and a line with it in `~/.ssh/config` would otherwise stop the OpenSSH nimble sets up.
- Hosts' own ssh config is never touched.

### The ssh client

Every workstation gets an ssh that works with both kinds of hardware key: the daily key in the Secure Enclave and the YubiKey backups. It is not a tool to choose; apply sets it up on every workstation, and `nimble update` keeps it current.

- **macOS:** macOS's own OpenSSH has no FIDO support, so it can't use a YubiKey. apply installs Homebrew's OpenSSH and links it; Homebrew's `bin` comes first in PATH, so it is the `ssh` of every shell and of nimble. It reaches the Secure Enclave through macOS's provider (`/usr/lib/ssh-keychain.dylib`) the same way macOS's own ssh does. Apps that call `/usr/bin/ssh` directly still reach the hosts with the daily key, through the same generated config. apply also installs `ykman` (a YubiKey's FIDO PIN, which one is plugged in). `nimble update` runs `brew upgrade` on both.
- **Debian:** Debian's `openssh-client` talks to FIDO keys itself; apply installs it and `yubikey-manager` (ykman) from apt.
- `nimble doctor` reports which ssh is in PATH and whether it can use YubiKeys.

### iTerm2

On macOS, if `personal/macos/iterm2/com.googlecode.iterm2.plist` exists, apply sets iTerm2's `PrefsCustomFolder` to that folder and `LoadPrefsFromCustomFolder` to true, so iTerm2 loads its settings from the repo from its next start.

Saving changes back into that folder is iTerm2's own choice: Settings → General → Settings → **Save changes**. At its default, Manually, changes stay in iTerm2 until you press Save Now — and iTerm2's next start loads the folder's older version over them. Set it to **Automatically** once on each Mac; iTerm2 keeps this choice to itself (it isn't in its preferences or the folder's plist), so nimble can't set it, and says so when it points iTerm2 at the folder. Changes then land in the repo like those of any linked file: `nimble push` shares them.

## Tools

The tool catalog is built into nimble; `nimble tool list` shows it with ✓ (wanted and installed), ✗ (wanted, missing) and • (installed, not wanted), then the pinned versions and their updates.

| Tool | macOS | Debian | `nimble update` keeps it current with |
|---|---|---|---|
| `starship` | Homebrew | newest release binary in `~/.local/bin` | `brew upgrade` / a newer release |
| `node` | the pinned Node through nvm (nvm is installed with it when missing), the newest npm and `@nimblescape/typescript` (the `ts` command), and the TypeScript `ts install --global` picks; installed means the pinned Node is there | the same | nvm, npm and `ts` move to their newest release, TypeScript to the one `ts` picks; Node only moves with its pin |
| `go` | the pinned Go in `~/sdk`, the Go tools | the same | — |
| `python` | uv, the pinned Python, the uv tools | the same | `uv self update` |
| `rust` | rustup and the pinned toolchain | the same | — |
| `gh` | Homebrew | GitHub's apt repository | `brew upgrade` / `apt-get install --only-upgrade` |
| `azure` | Homebrew cask `azure-cli` | Microsoft's apt repository | the same |
| `gcloud` | Homebrew cask `gcloud-cli` | Google's apt repository (`google-cloud-cli`) | the same |
| `docker` | Docker Desktop (cask) | Docker Engine, buildx and compose from Docker's apt repository | Docker Desktop updates itself / apt |
| `firebase` | `firebase-tools` via npm (needs `node`) | the same | npm, when a newer version exists |
| `claude` | Claude Code's official installer (`~/.local/bin/claude`) | the same | its own auto-update |
| `lima` | Homebrew | Lima's release in `~/.local`, QEMU from apt | `brew upgrade` / a newer release |
| `task` | Homebrew (`go-task`) | Task's release binary in `~/.local/bin` | `brew upgrade` / a newer release |
| `iterm2` | Homebrew cask (skipped if iTerm.app exists) | skipped (macOS only) | its own updater |

- On a Mac the tools come from Homebrew; without it, nimble runs Homebrew's installer first (see [Applying the config to a machine](#applying-the-config-to-a-machine)).
- What YubiKeys need is not a tool: every workstation gets it (see [The ssh client](#the-ssh-client)).
- **ts and TypeScript come with `node`.** After the pinned Node, nimble installs the newest `@nimblescape/typescript` and runs `ts install --global` without a terminal, so ts doesn't ask: it installs the TypeScript of the current VS Code (looked up online; offline its built-in fallback). `nimble update` repeats both, so TypeScript follows VS Code.
- **Install** runs on every apply and installs what is missing. Tools install in catalog order; `firebase` pulls in `node`. One tool failing is reported and the others still install; the updates and the later steps still run, and apply ends with an error. On a host, hardening and the services run regardless.
- **Update** runs with `apply --upgrade`, i.e. on `nimble update`. Tools that aren't installed are skipped; a failure is a warning.
- **Vendor apt repositories** get their key in `/etc/apt/keyrings/<name>.gpg` and a list in `/etc/apt/sources.list.d/<name>.list`. For repositories with a Debian codename, nimble uses this release's codename, or the newest older Debian release (trixie, bookworm) the vendor serves. If `apt-get update` fails after adding a list, the list is removed again.
- **`tool select [--host h]`** picks a machine's tools on a terminal: the catalog for its system (`iterm2`, macOS only, is offered only for a Mac; `--host` means a Debian host), with what the machine already has checked and marked `(template <name>)` or `(host file)`. Newly checked tools are installed and added to the host file's `tools`. Unchecked tools of the host file are taken out of it and uninstalled where nimble installed them — through Homebrew, apt or npm, or as its own release binaries (`gh`, `azure`, `gcloud`, `docker`, `firebase`, `iterm2`, `lima`, `task`); the language toolchains, starship and Claude Code stay installed and nimble only stops managing them. Before anything is removed nimble warns, tool by tool, what gets uninstalled and what it only stops managing, and asks (default no). A tool from the template stays: change the template instead. Everything is one host-file change and one commit; with `--host` it is recorded on the workstation, the host is updated right away (the full `nimble update <host>`) and the removed tools are uninstalled there. Without a terminal, `nimble host set <host> tools=a,b,c` changes the list.

## Pinned languages

Only the programming languages have pinned versions, the same on every machine. The tools stay current, except the Go and uv tools listed in `pins.yaml`, which are installed once.

### pins.yaml

```yaml
node: 26.8.1
go: 1.27.2
rust: 1.100.0
python: 3.12.15          # the default python / python3, and uv's global pin
goTools:                 # go install targets; @latest unless a version is given
  - golang.org/x/tools/gopls
uvTools: []              # uv tool install specs
```

### How each is installed

| Language | Where | How it becomes the default |
|---|---|---|
| Node | nvm in `~/.nvm` (moved to its newest release by `nimble update`); `nvm install <version>` | `nvm alias default <version>` |
| npm, ts, TypeScript | `npm install -g` into the pinned Node: the newest `npm` and `@nimblescape/typescript`; then `ts install --global` installs TypeScript | installed when missing; `nimble update` moves them to their newest release |
| Go | go.dev's release unpacked into `~/sdk/go<version>`, with the `.unpacked-success` marker (the layout of golang.org/dl and VS Code's Go extension) | `~/.local/go` is a symlink to it, first in PATH. nimble sets no `GOTOOLCHAIN` |
| Go tools | `go install <target>@latest` into `$GOBIN`, else `$GOPATH/bin` | installed once if missing, not upgraded |
| Rust | rustup (`rustup-init --profile default`) in `~/.cargo` | `rustup default <version>` |
| Python | uv (its installer, into `~/.local/bin`); `uv python install <version>` | `uv python install --default` puts `python3` in `~/.local/bin`; `uv python pin --global` |
| uv tools | `uv tool install <spec>` | installed once if missing, not upgraded |

Older versions are never removed (`~/sdk`, nvm, rustup and uv keep them), so switching back is quick and VS Code can still offer them.

### nimble tool use

`nimble tool use [language] [version]` pins a version. Without arguments it shows a list of the pinned languages (`node`, `go`, `rust`, `python`), then the language's releases, newest first, with the current one and the bugfix and feature updates marked, and for Node each release of an LTS line (`LTS Krypton`). Type to filter. With a version, it takes the newest release that matches exactly or by prefix (`nimble tool use go 1.28` takes the newest 1.28.x).

The release lists come from nodejs.org, go.dev (stable releases), GitHub (`rust-lang/rust`) and python-build-standalone's newest release (what uv installs). Only plain `x.y.z` releases count.

`tool use` edits `toolchain/pins.yaml` in place, keeping comments and layout. It doesn't commit or install: `nimble update` installs, `nimble push` commits and shares, `nimble update all` rolls it out.

### Updates: nimble tool list and tool update

A **bugfix update** is the newest release that keeps compatibility, by each language's own scheme; a **feature update** is anything newer than that.

| Pin | Bugfix updates | Feature updates |
|---|---|---|
| Node, Go, Python | the newest patch release of the same `x.y` | a newer minor or major |
| Rust | the newest `1.x` release (Rust ships its fixes there under its stability promise) | a new major |

- `nimble tool list` shows, after the catalog, each pin with its bugfix and feature updates (the release lists are looked up in parallel; a lookup that fails shows as "could not check"), and writes nothing.
- `nimble tool update` on a terminal shows a checklist: bugfix updates checked, feature updates unchecked. Without a terminal it writes every bugfix update.
- `nimble tool update <name>…` writes the bugfix update of the named pins; with `--feature`, their feature update.
- `--markdown` prints the result as a Markdown table (the daily workflow's pull request body). It carries the marker `<!-- feature-updates -->` when there are feature updates and `<!-- incomplete -->` when a lookup failed.
- Like `tool use`, `tool update` edits `pins.yaml` and doesn't commit.

### The daily workflow

The config repo's `.github/workflows/patch-updates.yml` runs every day at 05:17 UTC (and on demand):

1. Get nimble: its installer; while the nimble repo is private, a release or a build of its source through a `NIMBLE_TOKEN` secret (otherwise the run is skipped with a notice).
2. `nimble tool update --markdown`: every bugfix update is written.
3. If `pins.yaml` changed: commit to the branch `patch-updates` and open or update the pull request "Bugfix updates for pinned versions". If Actions may not open pull requests, an issue with a link to open it instead.
4. If there are feature updates: open or update the issue "Feature updates for pinned versions"; close it when there are none (it is left alone when a lookup failed).

After merging: `nimble update` on the workstation, then `nimble update all`.

## Hosts

### Setting up a host: nimble init <host>

The host must be in `hosts/` and managed. The steps:

1. **Key login.** Try `ssh <host> true` with key authentication only, as the admin user (through the generated ssh config).
2. **Bootstrap**, only if key login fails:
   1. Ask which user to log in as first (default `root`) and open one password session to the address, shared for the following steps.
   2. Detect the CPU (`uname -m`) and get a Linux build of nimble for it.
   3. Upload nimble and the domain's trusted public keys to `/tmp`, and run `nimble bootstrap` there as root (directly, or with sudo). It installs sudo, creates the admin user if missing, asks for a password if it has none (it stays the sudo fallback), adds the user to group `sudo`, writes the keys into the user's `authorized_keys` and puts nimble in `~/.local/bin`. The uploads are deleted.
3. **Confirm key login** as the admin user in a fresh connection. If it fails, nimble stops here with nothing locked down.
4. **Send nimble** to `~/.local/bin/nimble` and **send the snapshot** of the host's config.
5. **Harden, then apply**, in one connection with the agent forwarded: `sudo nimble harden --user <admin> --as <host> && nimble apply --as <host>`. sudo asks for the admin's password this one time; from then on, the hardware key confirms it. apply runs only if harden's audit passes.
6. **Check sudo** with the hardware key: `sudo -k; sudo -v`.

`nimble host create` offers to run this right away.

### Updating hosts: nimble update <host>… | all

`all` means every host with mode `managed`; a host named on the command line must be managed too (ssh entries and workstations are refused before anything is sent). The workstation must have the checkout on branch `main`. For each host:

1. Send the snapshot.
2. Send nimble.
3. Run, with the agent forwarded: `sudo nimble harden --as <host>`, `nimble apply --upgrade --as <host>`, `sudo nimble service sync --as <host>`. Each runs whatever the one before did, so a tool that fails to install doesn't keep the keys from being synced; the update fails if any of them failed.

nimble for the host is built from `$NIMBLE_SRC` when that is set (a checkout of nimble's source; `CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build`). Otherwise it is the release asset `nimble_<version>_linux_<arch>.tar.gz` of the running version, downloaded with `gh` (or over HTTPS) and checked against the release's `checksums.txt`. A development build without `NIMBLE_SRC` refuses.

### Snapshots

A host gets only what it needs, never the repo:

- `hosts/<host>.yaml`, its template, `toolchain/pins.yaml`, `shell/`, `personal/shell/` if present
- the public keys its domain trusts
- the sources of the links that apply to Debian
- a reduced `nimble.yaml` (its template as the default, only its own domain, the shared links) and, if there are personal links for Debian, `personal/nimble.yaml`
- `.snapshot`: `host`, `from` (the workstation), `commit` (the short commit, plus "+ uncommitted changes" when the checkout had any) and `date`

The snapshot is built from the files on disk, so uncommitted edits in the checkout are included. It is sent as a tar stream over SSH. On the host it is unpacked next to `~/.nimble` first; only a complete snapshot then replaces the old one (everything except `cache/`, `backup/` and `local.sh`), so a transfer that breaks off changes nothing.

On a host, nimble knows from `.snapshot` that it has no repo. Every command that changes the config refuses there before it writes anything and points to the workstation (`--host <name>` there, then `nimble update <name>`); only `nimble edit local` stays, for the host's own `local.sh`. A local `nimble update` on the host re-applies the snapshot it has.

## Hardening

`sudo nimble harden` runs on every `init` and `update`. It needs root, Debian, the host in `hosts/`, an admin user in group `sudo` with a password, and at least one trusted key for the host's domain.

### Full hardening (default)

In order:

1. **authorized_keys.** The admin user's `~/.ssh/authorized_keys` (0600, in a 0700 `~/.ssh`; root never writes through a symlink there) gets a managed block between `# >>> nimble keys (managed by nimble harden) >>>` and `# <<< nimble keys <<<` with the domain's trusted keys. Added keys appear, removed keys disappear; lines outside the block stay, except duplicates of the managed keys.
2. **pam_rssh**, the PAM module that confirms sudo through the forwarded SSH agent. nimble ships it: the Linux builds of nimble carry the module for their CPU (amd64 or arm64), and harden installs it next to `pam_unix.so` (e.g. `/usr/lib/x86_64-linux-gnu/security/libpam_rssh.so`), as a new file renamed into place, then checks that it loads. Nothing is built or downloaded on the host. `/usr/local/share/pam_rssh.version` records the version and the module's checksum. The modules are built from a pinned release and commit of pam_rssh on Debian 12 (see [nimble itself](#nimble-itself)), so they load on Debian 12 and newer and on Ubuntu 24.04 and newer.
3. **sudo with the hardware key.**
   - `/etc/security/authorized_keys/<admin>.keys`: the trusted keys, root-owned, so the user can't add their own.
   - `/etc/pam.d/sudo`: `auth sufficient libpam_rssh.so auth_key_file=/etc/security/authorized_keys/${user}.keys` inserted before `@include common-auth`, so the hardware key comes first and the password is the fallback. The original is kept as `/etc/pam.d/sudo.before-nimble`.
   - `/etc/sudoers.d/10-nimble`: `Defaults env_keep += "SSH_AUTH_SOCK"`, validated with `visudo` before it is put in place.
4. **Only the admin may sudo.** Other members of group `sudo` are removed, except those in `sudoAllowed`.
5. **NOPASSWD rules.** Active `NOPASSWD` rules in `/etc/sudoers` and `/etc/sudoers.d/*` skip authentication, so sudo would never ask for the hardware key. On a terminal, harden offers to comment each one out (`# disabled by nimble harden (skipped authentication): …`). The original file is backed up to `/var/backups/nimble/`, the change is written atomically and validated with `visudo -c`, and restored if validation fails. Rules whose user is a `sudoAllowed` account are left alone. A declined rule stays and the audit keeps reporting it.
6. **Root password** locked (`passwd -l root`).
7. **sshd.** `/etc/ssh/sshd_config.d/10-nimble.conf` with `PermitRootLogin no`, `PasswordAuthentication no` and `KbdInteractiveAuthentication no`. The file is checked with `sshd -t` (removed again if sshd rejects it), then ssh is reloaded.
8. **Root's authorized_keys.** With root login off, `/root/.ssh/authorized_keys` is unused, but its keys would work again if root login were allowed; cloud images often put the default user's key there. On a terminal, harden offers to remove it (default yes); the original goes to `/var/backups/nimble/root_.ssh_authorized_keys` first (an earlier backup is never overwritten). The admin's key login is checked before harden runs, so this can't lock anyone out. A declined file stays and the audit keeps reporting it.
9. **Audit.**

### harden: sudo

For a host someone else manages, `harden: sudo` in its host file limits hardening to sudo with the hardware key: pam_rssh, the sudo key file, the PAM line and the sudoers snippet, then the matching part of the audit. SSH, root, authorized_keys and other accounts stay as they are.

### The audit

Every harden run ends by checking the effective configuration, whatever wrote it. `sudo nimble harden --check` and `nimble doctor` run it on their own.

| Check | How | Wants |
|---|---|---|
| sshd | `sshd -T` for root, the admin and an ordinary user (Match blocks included) | no root login, no password or keyboard-interactive login, no empty passwords, public keys on. A finding names the file and line that set the value, or "sshd's default" |
| root | `passwd -S root` | locked |
| uid 0 | `/etc/passwd` | no account but root |
| empty passwords | `/etc/shadow` | none |
| root's authorized_keys | `/root/.ssh/authorized_keys` | absent |
| sudo rights | `sudo -l -U <user>` (read in the C locale) for every login account (uid 1000–59999 with a login shell) | only the admin (and `sudoAllowed`) may sudo, the admin without NOPASSWD |
| PAM | `/etc/pam.d/sudo`, `common-auth`, `sshd`, `su` | an active pam_rssh line before `common-auth`; no `auth sufficient pam_permit.so` |
| sudo keys | `/etc/security/authorized_keys/<admin>.keys` | present, owned by root, not writable by others |

The `sudo` level checks only the admin's sudo rights (no NOPASSWD), PAM and the sudo keys. Any finding makes harden fail and the update end with an error; apply and `service sync` still run.

## Keys and domains

| Key | Where | Protected by | Trusted by |
|---|---|---|---|
| Daily key | the workstation's Secure Enclave (TPM on Debian workstations: planned) | Touch ID for every use | the hosts of every domain that uses its label |
| YubiKey backup | a YubiKey with a resident key | PIN and touch | every managed host |

- **`nimble key enclave [domain]`** (macOS): creates the domain's daily key in the Secure Enclave (`sc_auth create-ctk-identity -k p-256-ne -t bio`) if this Mac has none, exports its key handle to `~/.ssh/id_ecdsa_sk_rk_<label>` (and `.pub`), writes `keys/enclave/<machine>-<label>.pub` and commits. Private keys never leave the hardware; the handle only lets ssh find the key. nimble identifies a key by its fingerprint, since labels aren't unique in the Secure Enclave.
- **Renewing the daily key.** A Secure Enclave key is valid for a year. When it is invalid or expires within 30 days, `nimble key enclave` creates a new key next to it (labelled `<label>-<date>`), keeps the old handle as `~/.ssh/id_ecdsa_sk_rk_<label>.old`, and puts the new public key into `keys/`. ssh offers both keys, so every host stays reachable while `nimble update all` rolls the new one out. After that, `nimble key enclave --drop-old` removes the old key from the Mac for good.
- **`nimble key yubikey [name]`** sets up and registers YubiKeys, one after another, each plugged in alone. A new YubiKey gets its FIDO2 PIN first (`ykman fido access change-pin`). Then nimble creates a resident key on it (`ssh-keygen -t ed25519-sk -O resident -O verify-required -O application=ssh:nimble-backup`: PIN and touch for every use), writes `keys/yubikey/<name>.pub` (one key per file) and commits. A YubiKey that already carries a nimble key is registered with that key, not overwritten. It uses ykman and the workstation's ssh (see [The ssh client](#the-ssh-client)).
- **Reaching the hosts with a YubiKey: `--yubikey`.** On a new workstation, or when the daily key is lost or expired, add `--yubikey` to a command (`nimble update all --yubikey`). nimble reads the key's handle from the plugged-in YubiKey (asking for its PIN), checks that it is a registered backup, and runs every ssh of that command with that key. ssh asks for the PIN and a touch for each host; sudo there asks for the admin's password, because the forwarded agent holds no trusted key. The generated ssh config itself offers only the daily key.
- **`nimble key remove <name>`** deletes the public key and commits. It refuses to remove the last key a domain trusts.
- **Which keys a host trusts**: every YubiKey, plus every daily key (from any workstation) whose label equals its domain's label. `nimble update all` rolls key changes out to the hosts' `authorized_keys` and sudo key files (on `harden: sudo` hosts, the sudo key file only).
- **`nimble key list`** shows the keys per domain, the YubiKey backups and this Mac's Secure Enclave keys with their validity.
- **Domains**: `nimble domain create <name> [--label L]` adds one (the label defaults to the name, with `_` for `-`: a label has no `-`, because it ends the file name `keys/enclave/<machine>-<label>.pub`). Domains that share a label trust the same daily keys; a label of its own separates them.

## Services: the Claude Code server

`nimble service add claude [owner/name] [--host h]` runs a [Claude Code Remote Control](https://code.claude.com/docs/en/remote-control) server for a repo on a Debian host. Inside a checkout, its GitHub repo is the default; the repo must exist on GitHub. The host needs `gh` (`nimble tool select --host h`, check `gh`). The service is recorded in the host file, the host is updated, and then, on the host as root:

1. **Service user** `claude` with a locked password and home `/home/claude` (0750). sshd gets `DenyUsers claude` (`/etc/ssh/sshd_config.d/90-nimble-deny-claude.conf`), so it never logs in over SSH.
2. **Claude Code** for that user, with Claude Code's installer.
3. **Admin access**: the admin user joins group `claude`; claude gets the admin's git name and email and, once, the admin's Claude Code settings.
4. **Logins**, asked for once: a GitHub fine-grained token limited to the repos it serves (not shown while pasted; stored by `gh` in claude's home), then the Claude login (`claude auth login`).
5. **Checkout** in `/home/claude/<owner>/<name>`, with `.claude/worktrees/` excluded from git.
6. **First run**, the first time only: `claude remote-control --spawn=worktree` once in the terminal, to answer its questions; Ctrl+C ends that run, not nimble.
7. **systemd unit** `/etc/systemd/system/claude-<owner>-<name>.service`: `claude remote-control --spawn=worktree` as user claude in the checkout, restarted on exit, stopped with SIGINT so sessions can reconnect. Enabled and started.

A service change is committed to the host file only once it worked on the host; a failed one leaves the inventory as it was. An existing unit that already runs a Remote Control server in the same checkout is offered for replacement. `service disable` stops the unit and keeps it installed; `service enable` starts it again; `service remove` removes the unit and deletes the checkout unless it has uncommitted or unpushed work or session worktrees, or can't be checked (`--force` deletes anyway). The user and its logins stay. `nimble update` runs `service sync`, which enables or disables installed services to match the host file; it never installs, because installing asks for logins.

## Checking: status and doctor

Both take host names or `all`; they then run on the hosts over SSH.

**`nimble status`**: this machine (system, host file, template, config: branch, commit, uncommitted changes and sync with GitHub as of the last fetch, or the snapshot), the login shell against the template, each tool against what is wanted (for the languages: the installed default against the pin), on Debian the pam_rssh version, and the lines other tools added to the dotfiles. It exits with 1 when something differs.

**`nimble doctor`**: the YAML is valid; the dotfiles are managed by nimble (zsh's only where zsh is); the zsh launcher works; every link is in place; the git identity. On a Mac: the daily key per label (present, valid, not expiring within 30 days, handle in `~/.ssh`), the YubiKey backups, Touch ID for sudo (and iTerm2's setting that hides the Touch ID prompt), the ssh config include, a trusted key in the SSH agent. On Debian: the hardening files, the sudo group (the admin and `sudoAllowed` accounts), root lock, the forwarded agent, and the audit.

## nimble itself

- **`nimble version`** prints the version, platform and how it was installed: `brew` (in Homebrew's prefix), `apt` (a Debian package), `source` (`NIMBLE_SRC` points at nimble's source) or `installer`; and the newest release.
- **`nimble upgrade`** follows that channel: `brew upgrade nimble`, `apt-get install --only-upgrade nimble`, a rebuild from source, or the installer's way: download the newest release for this platform, check it against the release's `checksums.txt` and replace the binary. `nimble update` on a workstation does the same when a newer release exists.
- **From source**: with `NIMBLE_SRC` pointing at a checkout of nimble and Go installed, `nimble upgrade` and every apply rebuild `~/.local/bin/nimble` (`go build` with the version from `git describe`), and hosts get a Linux build of it.
- **The pam_rssh modules** nimble ships live in `pkg/harden/module/`. `build/pam_rssh/build.sh` builds them from its pin (version, commit, Rust version) in a Debian 12 container, for amd64 and arm64; the workflow `pam_rssh` does that in CI when the pin changes and opens a pull request with the result (`build/pam_rssh/docker.sh` does it locally).
- **Tests**: `go test ./...`, and `test/host.sh`, which sets a host up in Debian and Ubuntu containers the way a workstation does: apply, the full hardening and its audit, sudo confirmed through an SSH agent with the shipped module, and the commands that run under sudo. The workflow `Test` runs both on every push and pull request.
- **Releases**: tagging `vX.Y.Z` builds binaries for macOS and Linux (amd64, arm64), Debian packages and `checksums.txt` with GoReleaser and publishes them as a GitHub release.

## Reference: environment and files

### Environment variables

| Variable | Used for |
|---|---|
| `NIMBLE_HOME` | the config repo (default `~/.nimble`); the dotfiles set it. Hosts always use `~/.nimble` |
| `NIMBLE_SRC` | a checkout of nimble's source: development builds |
| `GITHUB_TOKEN` | GitHub API requests (release lists, version check), to avoid rate limits |
| `VISUAL` | the editor for `edit` commands where VS Code isn't used (default `vi`) |
| `NVM_DIR` | nvm's folder (default `~/.nvm`) |
| `XDG_CONFIG_HOME` | where uv keeps its global Python pin |
| `NO_COLOR` | output without colors |
| `TERM=dumb` | no spinner on waiting steps |

### Files nimble writes

| Path | Machine | Purpose |
|---|---|---|
| `~/.zshenv`, `~/.zprofile`, `~/.zshrc`, `~/.bashrc`, `~/.profile` | all | managed dotfiles |
| links from `nimble.yaml` | all | config files linked from the repo |
| `~/.nimble/cache/host` | hosts | this machine's name in `hosts/` (from `--as`) |
| `~/.nimble/cache/ssh_config`, the include in `~/.ssh/config` | workstation | ssh config for the hosts |
| `~/.ssh/id_ecdsa_sk_rk_<label>`, `.pub` (after a renewal also `.old`, `.old.pub`) | Mac | handles of the Secure Enclave keys |
| `~/.gitconfig` | all | created empty (with a comment) if missing |
| `/etc/shells` | when the login shell changes | the new shell is added |
| `~/.nimble/backup/<timestamp>/` | all | files nimble replaced |
| `~/.local/bin/nimble` | hosts (and installer copies) | nimble |
| `~/sdk/go<version>`, `~/.local/go` | with `go` | Go |
| `~/.nvm`, `~/.cargo`, `~/.rustup`, `~/.local/bin/uv`, `~/.local/bin/python3`, `~/.config/uv/.python-version` | with the languages | the language toolchains |
| `~/.local/bin/starship`, `~/.local/bin/task`, `~/.local/bin/limactl` (and Lima in `~/.local`) | Debian, with those tools | release binaries |
| `/etc/apt/keyrings/<name>.gpg`, `/etc/apt/sources.list.d/<name>.list` | Debian | vendor apt repositories |
| `/usr/local/bin/nimble-shell`, `/usr/local/bin/nimble-apt-guard`, `/etc/apt/apt.conf.d/50nimble-zsh-guard` | Debian with zsh | the zsh launcher and guard |
| `~/.ssh/authorized_keys` (managed block) | hosts | the trusted keys |
| `/etc/ssh/sshd_config.d/10-nimble.conf` | hosts | sshd hardening |
| `/etc/security/authorized_keys/<admin>.keys`, `/etc/pam.d/sudo` (original kept as `sudo.before-nimble`), `/etc/sudoers.d/10-nimble` | hosts | sudo with the hardware key |
| `<module dir>/libpam_rssh.so`, `/usr/local/share/pam_rssh.version` | hosts | pam_rssh, as shipped with nimble |
| `/var/backups/nimble/` | hosts | sudoers files before NOPASSWD rules were commented out, root's removed `authorized_keys` |
| `/etc/ssh/sshd_config.d/90-nimble-deny-claude.conf`, `/etc/systemd/system/claude-*.service`, `/home/claude/` | hosts with the Claude service | the Claude Code server |
