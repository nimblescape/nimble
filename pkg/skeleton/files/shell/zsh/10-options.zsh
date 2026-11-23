# zsh: history and line editing
HISTFILE=$HOME/.zsh_history
HISTSIZE=10000
SAVEHIST=10000
setopt HIST_IGNORE_DUPS HIST_IGNORE_SPACE INC_APPEND_HISTORY

# Emacs-style keys (zsh switches to vi mode when $EDITOR contains "vi"), plus Delete/Home/End
bindkey -e
bindkey '^[[3~' delete-char
bindkey '^[[H'  beginning-of-line
bindkey '^[OH'  beginning-of-line
bindkey '^[[F'  end-of-line
bindkey '^[OF'  end-of-line
