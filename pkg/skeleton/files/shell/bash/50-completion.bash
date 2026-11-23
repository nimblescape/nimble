# bash: completion — the system's bash-completion (Debian's package, Homebrew's
# bash-completion@2 on macOS: nimble installs it for a bash login shell) plus cached tool
# completions. The same tools as zsh (shell/zsh/50-completion.zsh), except the arguments of
# gh extensions.
if [ -r /usr/share/bash-completion/bash_completion ]; then
  . /usr/share/bash-completion/bash_completion
elif [ -n "$HOMEBREW_PREFIX" ] && [ -r "$HOMEBREW_PREFIX/etc/profile.d/bash_completion.sh" ]; then
  . "$HOMEBREW_PREFIX/etc/profile.d/bash_completion.sh"   # also Homebrew's formulae's completions
fi
[ -s "$NVM_DIR/bash_completion" ] && . "$NVM_DIR/bash_completion"

# cache `<tool> completion bash` output; regenerate only when the tool changes
_nimble_comp() {   # _nimble_comp <name> <command> [args…]
  local out="$NIMBLE_HOME/cache/bash/$1" bin
  bin=$(command -v "$2" 2>/dev/null) || return 0
  bin=$(readlink -f "$bin" 2>/dev/null || echo "$bin")
  shift
  if [ ! -s "$out" ] || [ "$bin" -nt "$out" ]; then
    mkdir -p "${out%/*}"
    "$@" >"$out" 2>/dev/null || { rm -f "$out"; return 0; }
  fi
  . "$out"
}
_nimble_comp nimble nimble completion bash
_nimble_comp gh     gh completion -s bash
_nimble_comp uv     uv generate-shell-completion bash
_nimble_comp uvx    uvx --generate-shell-completion bash
_nimble_comp rustup rustup completions bash
_nimble_comp cargo  rustup completions bash cargo
_nimble_comp docker docker completion bash
unset -f _nimble_comp

# gcloud ships its completion next to its bin/
if command -v gcloud >/dev/null 2>&1; then
  _gc=$(readlink -f "$(command -v gcloud)" 2>/dev/null)
  [ -r "${_gc%/bin/*}/completion.bash.inc" ] && . "${_gc%/bin/*}/completion.bash.inc"
  unset _gc
fi

# firebase: its top-level commands (the CLI ships no completion; for zsh the same list is in
# shell/zsh/functions/_firebase)
_nimble_firebase() {
  local cur=${COMP_WORDS[COMP_CWORD]} cword=$COMP_CWORD
  # emulators:start is one word, though bash splits at ":" (bash-completion's helpers mend that)
  declare -F _get_comp_words_by_ref >/dev/null && _get_comp_words_by_ref -n : cur cword
  if [ "$cword" -le 1 ]; then
    COMPREPLY=($(compgen -W "login logout init use deploy serve open
      emulators:start emulators:exec emulators:export
      projects:list projects:addfirebase
      apps:list apps:create apps:sdkconfig
      functions:log functions:shell functions:delete
      hosting:channel:create hosting:channel:deploy
      hosting:channel:list hosting:channel:delete
      hosting:clone hosting:disable" -- "$cur"))
    declare -F __ltrim_colon_completions >/dev/null && __ltrim_colon_completions "$cur"
  fi
}
complete -o default -F _nimble_firebase firebase
