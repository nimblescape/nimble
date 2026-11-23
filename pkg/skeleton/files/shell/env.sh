# nimble: the environment for every shell — login, interactive, and commands over ssh.
# Managed by nimble; your own additions go into shell/common.sh or ~/.nimble/local.sh.
# POSIX sh: read by bash, zsh and sh. Safe to load more than once.

: "${NIMBLE_HOME:=$HOME/.nimble}"
export NIMBLE_HOME

# put a directory first / last in PATH (moving it if it is already there), if it exists
_nimble_path_rm() { PATH=$(printf '%s' ":$PATH:" | sed "s#:$1:#:#g; s#^:##; s#:\$##"); }
_nimble_path_front() { [ -d "$1" ] && { _nimble_path_rm "$1"; PATH="$1${PATH:+:$PATH}"; }; }
_nimble_path_back() { [ -d "$1" ] && { _nimble_path_rm "$1"; PATH="${PATH:+$PATH:}$1"; }; }

if [ "$(uname -s)" = Darwin ]; then
  # Homebrew: /opt/homebrew on Apple silicon, /usr/local on Intel
  if [ -x /opt/homebrew/bin/brew ] || [ ! -x /usr/local/bin/brew ]; then
    export HOMEBREW_PREFIX=/opt/homebrew HOMEBREW_CELLAR=/opt/homebrew/Cellar HOMEBREW_REPOSITORY=/opt/homebrew
  else
    export HOMEBREW_PREFIX=/usr/local HOMEBREW_CELLAR=/usr/local/Cellar HOMEBREW_REPOSITORY=/usr/local/Homebrew
  fi
  _nimble_path_front "$HOMEBREW_PREFIX/sbin"
  _nimble_path_front "$HOMEBREW_PREFIX/bin"
  _nimble_path_back "$HOME/.docker/bin"                                        # Docker Desktop
  _nimble_path_back "$HOME/Library/Application Support/JetBrains/Toolbox/scripts"
fi
_nimble_path_front "$HOME/.cargo/bin"   # rust (rustup)
_nimble_path_front "$HOME/.local/bin"   # nimble, uv, python, claude
_nimble_path_front "$HOME/.local/go/bin"   # the pinned Go: ~/.local/go → ~/sdk/go<version> (nimble tool use go)
_nimble_path_back "$HOME/go/bin"        # go install …
export PATH

export NVM_DIR="$HOME/.nvm"
export UV_PYTHON_PREFERENCE=only-managed   # uv only uses the Pythons it installed: same on every machine
export STARSHIP_CONFIG="$NIMBLE_HOME/shell/starship.toml"
