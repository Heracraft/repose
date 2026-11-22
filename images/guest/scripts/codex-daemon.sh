# shellcheck shell=bash
# The Codex daemon check of nix/overlay/agents/wrap.nix (DECISIONS I-547),
# spliced into the codex wrapper by install-agents.sh.
repose_mode=
if [ $# -eq 0 ]; then
  repose_mode=top
else
  # A first word that is no subcommand is the interactive start's
  # prompt (`codex "fix the build"`).
  case "$1" in
    resume|fork) repose_mode=sub ;;
    agents|exec|e|review|login|logout|mcp|mcp-server|plugin|app-server|remote-control|completion|update|doctor|sandbox|debug|apply|a|queue|archive|delete|migrate-rollouts|unarchive|cloud|exec-server|features|help) ;;
    *) repose_mode=top ;;
  esac
fi
for repose_a in "$@"; do [ "$repose_a" = --no-daemon ] && repose_mode=; done
if [ -n "$repose_mode" ]; then
  repose_cd="${CODEX_HOME:-$HOME/.codex}/app-server-daemon"
  repose_key=$(printf '%s\n%s\n' "${DIRENV_DIR-}" "$PATH" | /usr/bin/sha256sum)
  repose_key=${repose_key%% *}
  repose_pid=$(/usr/bin/sed -n 's/^{"pid":\([0-9][0-9]*\).*/\1/p' "$repose_cd/daemon.pid" 2>/dev/null) || repose_pid=
  if [ -n "$repose_pid" ] && /usr/bin/grep -qa app-server "/proc/$repose_pid/cmdline" 2>/dev/null; then
    if [ "$(/usr/bin/cat "$repose_cd/repose-env" 2>/dev/null)" != "$repose_key" ]; then
      if [ "$repose_mode" = top ]; then
        set -- --no-daemon "$@"
      else
        repose_a=$1; shift; set -- "$repose_a" --no-daemon "$@"
      fi
    fi
  else
    { /usr/bin/mkdir -p "$repose_cd" && printf '%s\n' "$repose_key" > "$repose_cd/repose-env"; } 2>/dev/null || true
  fi
  unset repose_cd repose_key repose_pid
fi
unset repose_mode repose_a
