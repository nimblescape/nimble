# zsh: completion — own functions + cached tool completions on fpath, compinit, then the
# tools that hook in afterwards.
typeset -U fpath
_zc=$NIMBLE_HOME/cache/zsh
[[ -d $_zc ]] || mkdir -p $_zc
fpath=($NIMBLE_HOME/shell/zsh/functions $_zc $fpath)
if [[ $OSTYPE == darwin* ]]; then
  fpath=(${HOMEBREW_PREFIX:-/opt/homebrew}/share/zsh/site-functions(N/) $HOME/.docker/completions(N/) $fpath)
fi

# cache `<tool> completion zsh` output; regenerate only when the tool itself changes
_zcomp() {   # _zcomp <file> <command> [args…]
  local out=$_zc/$1 bin=${commands[$2]:A}
  shift
  [[ -n $bin ]] || return 0
  [[ -s $out && $out -nt $bin ]] && return 0
  "$@" >| $out 2>/dev/null || rm -f $out
}
_zcomp _nimble nimble completion zsh
_zcomp _rustup rustup completions zsh
_zcomp _cargo  rustup completions zsh cargo
_zcomp _gh     gh completion -s zsh
_zcomp _uv     uv generate-shell-completion zsh
_zcomp _uvx    uvx --generate-shell-completion zsh
unfunction _zcomp

zstyle ':completion:*:(ssh|scp|sftp):*' hosts off   # hosts from ~/.ssh/config, not known_hosts
autoload -Uz compinit
compinit -d $_zc/.zcompdump
unset _zc

[[ -s $NVM_DIR/bash_completion ]] && source $NVM_DIR/bash_completion
if (( $+commands[gcloud] )) && [[ -r ${commands[gcloud]:A:h:h}/completion.zsh.inc ]]; then
  source ${commands[gcloud]:A:h:h}/completion.zsh.inc
fi

# gh extensions (e.g. `gh mmc`): gh doesn't complete their arguments, so ask the extension
# itself — cobra's __complete protocol: "value<TAB>description" lines, then ":<directive>".
_gh_ext() {
  local ext=${words[2]}
  if (( CURRENT > 2 )) && [[ -x ${XDG_DATA_HOME:-$HOME/.local/share}/gh/extensions/gh-$ext/gh-$ext ]]; then
    local -a out comps
    local line directive
    out=("${(@f)$(gh $ext __complete "${(@)words[3,CURRENT-1]}" "${words[CURRENT]}" 2>/dev/null)}")
    directive=${out[-1]#:}
    for line in "${(@)out[1,-2]}"; do
      [[ -n $line ]] && comps+=("${${line%%$'\t'*}//:/\\:}:${line#*$'\t'}")
    done
    if (( $#comps )); then
      _describe -t commands "gh $ext" comps
    elif (( ! (${directive:-0} & 4) )); then
      _files
    fi
    return
  fi
  _gh "$@"
}
(( $+commands[gh] )) && compdef _gh_ext gh
