# Sourced by every agent wrapper (wrap.nix) right before it execs the agent:
# loads the checkout's dev environment into the wrapper's own process, so
# the agent and every command its tools run see the project's tools and
# variables however the agent was started (DECISIONS I-259,
# guest-conventions.md "Agent wrappers").
#
#   .envrc found by direnv (here or above)  its environment; one never
#                                           allowed on this machine is
#                                           allowed, one denied is left out
#   else flake.nix naming a devShell        that dev shell, through a
#                                           generated .envrc (`use flake`)
#                                           under ~/.cache/repose/devshell,
#                                           with its flake.lock there when
#                                           the checkout has none
#   else                                    nothing
#
# A load that fails prints why and the agent starts without it. While a
# load runs inside tmux the pane carries @repose-devshell=loading, which
# `repose run` waits on before it types the prompt. Messages go to stderr,
# which is the pane.
#
# @direnv@, @jq@, @tmux@ and @coreutils@ are store paths, substituted by
# wrap.nix. Everything is local to the function; the caller runs it as
# `_repose_devshell <agent>` and then execs.
#
# REPOSE_DEVSHELL_QUIET=1 (set by `repose exec`, whose stderr is the
# user's own command output) keeps a load that works silent: direnv's
# messages are held back and shown only when the load fails, and the
# "loading" line appears only once a load has taken 2 seconds.
_repose_devshell() {
  local agent=$1 status rc allowed dir label d root shadow lockargs want out loaded quiet= errf= timer=
  [ "${REPOSE_DEVSHELL_QUIET:-}" = 1 ] && quiet=1

  # Where the NixOS module puts the direnvrc that loads nix-direnv; a
  # variable of /etc/set-environment, which a tmux server started by a
  # user unit may not have.
  if [ -z "${DIRENV_CONFIG:-}" ] && [ -d /etc/direnv ]; then
    export DIRENV_CONFIG=/etc/direnv
  fi
  status=$(@direnv@/bin/direnv status --json 2>/dev/null) || status=
  rc=$(printf '%s' "$status" | @jq@/bin/jq -r '.state.foundRC.path // empty' 2>/dev/null) || rc=
  if [ -n "$rc" ]; then
    allowed=$(printf '%s' "$status" | @jq@/bin/jq -r '.state.foundRC.allowed' 2>/dev/null) || allowed=
    case $allowed in
      0) ;;
      2)
        echo "repose: $rc is denied (direnv deny); starting $agent without it" >&2
        return 0
        ;;
      *)
        # Never allowed on this machine: every new guest, every worktree,
        # every edit of the file. The agent is about to run this checkout's
        # code anyway (DECISIONS I-259).
        if ! @direnv@/bin/direnv allow "$rc" 2>/dev/null; then
          echo "repose: could not allow $rc; starting $agent without it" >&2
          return 0
        fi
        [ -n "$quiet" ] || echo "repose: allowed $rc (direnv allow) so $agent starts in its environment" >&2
        ;;
    esac
    dir=${rc%/*}
    label=$rc
  else
    root=
    d=$PWD
    while [ -n "$d" ] && [ "$d" != / ] && [ "$d" != "$HOME" ]; do
      if [ -f "$d/flake.nix" ]; then
        root=$d
        break
      fi
      d=${d%/*}
    done
    [ -n "$root" ] || return 0
    grep -q devShell "$root/flake.nix" 2>/dev/null || return 0
    shadow=${XDG_CACHE_HOME:-$HOME/.cache}/repose/devshell/$(printf '%s' "$root" | @coreutils@/bin/sha256sum | @coreutils@/bin/cut -c1-16)
    # A flake with no flake.lock gets one written next to the generated
    # .envrc, not into the checkout, where it would block the user's next
    # `repose sync` as an uncommitted change (DECISIONS I-483). Once the
    # repository commits its own lock, that one is used.
    lockargs=
    if [ ! -e "$root/flake.lock" ]; then
      lockargs=" --reference-lock-file $(printf '%q' "$shadow/flake.lock") --output-lock-file $(printf '%q' "$shadow/flake.lock")"
    fi
    want="# Written by the repose agent wrapper for $root, which has a flake.nix and no .envrc (DECISIONS I-259).
use flake $(printf '%q' "$root")$lockargs"
    if [ "$(@coreutils@/bin/cat "$shadow/.envrc" 2>/dev/null)" != "$want" ]; then
      @coreutils@/bin/mkdir -p "$shadow" && printf '%s\n' "$want" >"$shadow/.envrc" || return 0
    fi
    @direnv@/bin/direnv allow "$shadow" 2>/dev/null || return 0
    dir=$shadow
    label=$root/flake.nix
  fi

  loaded=
  [ "${DIRENV_DIR:-}" = "-$dir" ] && loaded=1
  if [ -z "$loaded" ]; then
    if [ -n "$quiet" ]; then
      (@coreutils@/bin/sleep 2 && echo "repose: loading the dev shell from $label (the first load can take minutes)" >&2) &
      timer=$!
    else
      echo "repose: loading the dev shell from $label (the first load can take minutes)" >&2
    fi
  fi
  if [ -n "$quiet" ]; then
    errf=$(@coreutils@/bin/mktemp) || errf=
  fi
  if [ -n "${TMUX_PANE:-}" ]; then
    @tmux@/bin/tmux set-option -p -t "$TMUX_PANE" @repose-devshell loading 2>/dev/null || true
  fi
  # direnv export prints nothing when this process already has the
  # current environment of $dir, and a diff otherwise.
  if out=$(cd "$dir" && @direnv@/bin/direnv export bash 2>"${errf:-/dev/stderr}"); then
    eval "$out"
    # A dev shell that failed to evaluate is not an error to direnv:
    # nix-direnv falls back to the last one it built (none, the first
    # time), says so above and sets this.
    if [ -n "${NIX_DIRENV_DID_FALLBACK:-}" ]; then
      _repose_devshell_done "$timer" "$errf" 1
      echo "repose: the dev shell from $label did not load (the error is above); starting $agent with the last one that did, if any" >&2
      [ -n "$quiet" ] || sleep 3
    else
      _repose_devshell_done "$timer" "$errf" ""
    fi
  else
    _repose_devshell_done "$timer" "$errf" 1
    echo "repose: the dev shell from $label did not load (the error is above); starting $agent without it" >&2
    [ -n "$quiet" ] || sleep 3
  fi
  if [ -n "${TMUX_PANE:-}" ]; then
    @tmux@/bin/tmux set-option -p -u -t "$TMUX_PANE" @repose-devshell 2>/dev/null || true
  fi
  return 0
}

# _repose_devshell_done TIMER ERRFILE FAILED stops the quiet mode's
# "loading" timer and, when the load failed, prints what direnv said.
_repose_devshell_done() {
  if [ -n "$1" ]; then
    kill "$1" 2>/dev/null
    wait "$1" 2>/dev/null
  fi
  if [ -n "$2" ]; then
    [ -n "$3" ] && @coreutils@/bin/cat "$2" >&2
    @coreutils@/bin/rm -f "$2"
  fi
  return 0
}
