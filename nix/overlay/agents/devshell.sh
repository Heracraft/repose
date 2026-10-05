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
# Every interactive bash sources this file too (tools.nix), and gets
# _repose_devshell_prompt as its direnv hook, so the user's own shells
# load the same flake dev shell an agent gets (DECISIONS I-488).
#
# REPOSE_DEVSHELL_QUIET=1 (set by `repose exec`, whose stderr is the
# user's own command output) keeps a load that works silent: direnv's
# messages are held back and shown only when the load fails, and the
# "loading" line appears only once a load has taken 2 seconds.
_repose_devshell() {
  local agent=$1 status rc allowed dir label root shadow out loaded quiet= errf= timer=
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
    _repose_devshell_flake || return 0
    _repose_devshell_shadow || return 0
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

# _repose_devshell_flake sets the caller's `root` to the nearest folder at
# or above $PWD, below $HOME, that holds a flake.nix whose text names a dev
# shell, and fails when there is none. The caller has already found no
# .envrc.
_repose_devshell_flake() {
  _repose_devshell_flake_root || return 1
  grep -q devShell "$root/flake.nix" 2>/dev/null || { root=; return 1; }
}

# _repose_devshell_flake_root sets the caller's `root` to the nearest
# folder at or above $PWD, below $HOME, with a flake.nix, without reading
# it.
_repose_devshell_flake_root() {
  local d=$PWD
  root=
  while [ -n "$d" ] && [ "$d" != / ] && [ "$d" != "$HOME" ]; do
    if [ -f "$d/flake.nix" ]; then
      root=$d
      return 0
    fi
    d=${d%/*}
  done
  return 1
}

# _repose_devshell_shadow sets the caller's `shadow` to the generated
# .envrc's directory for `root`, writes that .envrc when it is missing or
# out of date and allows it.
_repose_devshell_shadow() {
  local lockargs want
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
    @coreutils@/bin/mkdir -p "$shadow" && printf '%s\n' "$want" >"$shadow/.envrc" || return 1
  fi
  @direnv@/bin/direnv allow "$shadow" 2>/dev/null
}

# _repose_devshell_prompt is what an interactive bash runs before each
# prompt in place of direnv's own export (tools.nix redefines _direnv_hook
# to call it; DECISIONS I-488). Where direnv finds an .envrc, here or
# above, or no flake.nix naming a dev shell is found, it is direnv's hook
# unchanged: an .envrc loads only once you allow it, a denied one stays
# out, and leaving its folder unloads it. In a folder with such a
# flake.nix and no .envrc, it exports from the generated .envrc the agent
# wrapper uses, so the shell gets the agent's tools, and leaving the
# folder unloads them as direnv does. Entering prints one line; a cold
# load blocks the prompt until it is built, and Ctrl-C ends it, leaving
# the shell without the dev shell until you leave the folder and come
# back or flake.nix changes (direnv keeps a failed load as loaded). The
# root last seen and its generated directory stay in the shell variables
# _repose_devshell_key and _repose_devshell_dir, so a prompt inside a
# loaded checkout costs the one direnv call it cost before.
_repose_devshell_prompt() {
  local d=$PWD root= shadow= lock=0
  while [ -n "$d" ] && [ ! -f "$d/.envrc" ]; do
    d=${d%/*}
  done
  if [ -z "$d" ] && [ ! -f /.envrc ]; then
    _repose_devshell_flake_root
  fi
  if [ -n "$root" ]; then
    [ -e "$root/flake.lock" ] && lock=1
    # Entering the folder, its flake.lock appearing or going, a loaded dev
    # shell gone from this shell, or the generated .envrc gone from the
    # cache: check the text and write the .envrc again. Otherwise the last
    # answer stands.
    if [ "$root:$lock" != "${_repose_devshell_key-}" ] ||
      { [ -n "${_repose_devshell_dir-}" ] &&
        { [ "${DIRENV_DIR-}" != "-$_repose_devshell_dir" ] || [ ! -f "$_repose_devshell_dir/.envrc" ]; }; }; then
      _repose_devshell_key=$root:$lock
      _repose_devshell_dir=
      if grep -q devShell "$root/flake.nix" 2>/dev/null && _repose_devshell_shadow; then
        _repose_devshell_dir=$shadow
      fi
    fi
  else
    _repose_devshell_key=
    _repose_devshell_dir=
  fi
  if [ -z "$_repose_devshell_dir" ]; then
    eval "$(@direnv@/bin/direnv export bash)"
    return 0
  fi
  if [ "${DIRENV_DIR-}" != "-$_repose_devshell_dir" ]; then
    echo "repose: loading the dev shell from $root/flake.nix (the first load can take minutes; Ctrl-C skips it until you leave the folder)" >&2
  fi
  eval "$(builtin cd -- "$_repose_devshell_dir" && @direnv@/bin/direnv export bash)"
  return 0
}
