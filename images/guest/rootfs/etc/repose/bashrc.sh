# Every interactive bash (sourced last by /etc/bash.bashrc; shell.nix,
# tools.nix, git.nix, devtools.nix): the profile, history, fzf's keys,
# eza's aliases, starship, zoxide, direnv with the dev shell hook, the
# not-found handler and gpg's terminal. A user's ~/.bashrc, read after
# this, may change any of it.
[ -r /etc/profile.d/repose.sh ] && . /etc/profile.d/repose.sh
shopt -s histappend
: "${HISTSIZE:=100000}"
: "${HISTFILESIZE:=200000}"
: "${HISTCONTROL:=ignoredups}"
[ -r /usr/share/doc/fzf/examples/key-bindings.bash ] && . /usr/share/doc/fzf/examples/key-bindings.bash
[ -r /usr/share/bash-completion/completions/fzf ] && . /usr/share/bash-completion/completions/fzf
alias la='eza -a --group-directories-first'
alias ll='eza -al --group-directories-first'
alias lt='eza -aT --group-directories-first'
if [ -z "${STARSHIP_CONFIG:-}" ] && [ ! -f "$HOME/.config/starship.toml" ]; then
  export STARSHIP_CONFIG=/etc/repose/starship.toml
fi
command -v starship >/dev/null && eval "$(starship init bash)"
command -v zoxide >/dev/null && eval "$(zoxide init bash)"
if command -v direnv >/dev/null; then
  eval "$(direnv hook bash)"
  if declare -F _direnv_hook >/dev/null && [ -r /etc/repose/devshell.sh ]; then
    . /etc/repose/devshell.sh
    _direnv_hook() {
      local previous_exit_status=$?
      trap -- '' SIGINT
      _repose_devshell_prompt
      trap - SIGINT
      return $previous_exit_status
    }
  fi
fi
command_not_found_handle() {
  /usr/local/bin/repose-command-not-found "$1"
}
if repose_tty=$(tty 2>/dev/null); then export GPG_TTY=$repose_tty; fi
unset repose_tty
