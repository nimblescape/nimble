# The organization's shared shell config for every machine and both shells (bash, zsh): POSIX.
#   zsh-only extras:  shell/zsh/*.zsh      bash-only extras: shell/bash/*.bash
#   your own additions: personal/shell/common.sh   only this machine: ~/.nimble/local.sh

# Node via nvm (tool: node)
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"

# Editor for git and friends: VS Code inside its own terminal (also over Remote-SSH), vim otherwise
if [ "$TERM_PROGRAM" = vscode ] && command -v code >/dev/null 2>&1; then
  VISUAL='code --wait'
elif command -v vim >/dev/null 2>&1; then
  VISUAL=vim
else
  VISUAL=vi
fi
export VISUAL
export EDITOR="$VISUAL"

# one ls color scheme on every machine (macOS's default look): BSD ls (macOS) reads LSCOLORS,
# GNU ls (Linux) LS_COLORS
export CLICOLOR=1 LSCOLORS=exfxcxdxbxegedabagacad
export LS_COLORS='di=34:ln=35:so=32:pi=33:ex=31:bd=34;46:cd=34;43:su=30;41:sg=30;46:tw=30;42:ow=30;43'
alias ls='ls --color=auto'
alias ll='ls -la'

# the prompt's user@host: grey here, bold yellow on a remote host (shell/starship.toml)
if [ -z "${SSH_CONNECTION:-}" ]; then
  NIMBLE_LOCAL_USERHOST="$(id -un)@$(hostname -s 2>/dev/null || uname -n)"
  export NIMBLE_LOCAL_USERHOST
else
  unset NIMBLE_LOCAL_USERHOST
fi
alias vs='code -r .'      # open the current folder in the current VS Code window
