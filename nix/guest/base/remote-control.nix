# repose-remote-control: the machine's `remote-control` tmux window, in
# which `claude remote-control` offers this machine's Claude Code to the
# Claude app (phone, desktop, claude.ai/code). A function of pkgs, called
# by agents.nix (which installs it) and by nix/guest/base/tmux.nix at the
# end of the session unit; the claude wrapper (nix/overlay/agents/wrap.nix)
# and the CLI's carry call it from PATH. DECISIONS I-1132, I-1134, I-1136.
#
# Off by default: the window opens only where the laptop's config.toml
# said `remote_control = true`, which the CLI carries to
# ~/.repose/remote-control (I-1136). Claude Code's own keys win over it
# (I-196): `disableRemoteControl` or `remoteControlAtStartup: false` in
# ~/.claude/settings.json stops this before anything opens.
#
# It reconciles rather than only opening: a run that finds the switch off
# closes the window again, and only one this opened (the window option
# @repose-remote-control), so a window the user made by hand is theirs.
#
# Idempotent, silent and exit 0 on every path: it runs beside an agent's
# start and must never block one, nor print above it.
{ pkgs }:
let
  checkout = import ./checkout.nix { inherit pkgs; };
  loginPath = import ./login-path.nix { inherit pkgs; };
in
pkgs.writeShellApplication {
  name = "repose-remote-control";
  runtimeInputs = [ pkgs.coreutils pkgs.jq pkgs.tmux pkgs.gnugrep pkgs.git checkout loginPath ];
  text = ''
    home=''${HOME:-/home/dev}
    settings="$home/.claude/settings.json"
    secrets=''${REPOSE_SECRETS_DIR:-/run/repose/secrets}
    want=on

    # 1. The repose switch (I-1136). Absent is off (I-1134).
    [ "$(cat "$home/.repose/remote-control" 2>/dev/null || true)" = on ] || want=off

    # 2. Claude Code's own keys win, wherever they came from (I-196).
    if [ -s "$settings" ] && jq -e '.disableRemoteControl == true or .remoteControlAtStartup == false' "$settings" >/dev/null 2>&1; then
      want=off
    fi

    # 3. The login share holds a login (I-278). Its size only; the file is
    # never read (R2-8).
    [ -s "$home/.claude/.credentials.json" ] || want=off

    # 4. Remote Control refuses a setup token and an API key ("Remote
    # Control requires a full-scope login token"). By the presence of the
    # secret's file, never by its value.
    for s in CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN; do
      [ ! -e "$secrets/$s" ] || want=off
    done

    # 5. The project's tmux session. A herdr project has none, and no
    # window is built for it (I-1132).
    project="$home/.repose/project.json"
    [ -s "$project" ] || exit 0
    slug=$(jq -r '.slug // empty' "$project" 2>/dev/null || true)
    [ -n "$slug" ] || exit 0
    [ "$(jq -r '.multiplexer // "tmux"' "$project" 2>/dev/null || echo tmux)" != herdr ] || exit 0
    tmux has-session -t "=$slug" 2>/dev/null || exit 0

    target="=$slug:remote-control"
    if tmux list-windows -t "=$slug" -F '#{window_name}' 2>/dev/null | grep -qx remote-control; then
      # Already there. Off takes down the one this opened, and leaves a
      # window of that name the user made themselves.
      if [ "$want" = off ] && [ "$(tmux show-options -wqv -t "$target" @repose-remote-control 2>/dev/null || true)" = 1 ]; then
        tmux kill-window -t "$target" 2>/dev/null || true
      fi
      exit 0
    fi
    [ "$want" = on ] || exit 0

    dir=$(repose-checkout)
    # `--spawn worktree` keeps a session the phone starts out of the
    # checkout an agent is already working in; its branches reach the
    # laptop with `git fetch repose` (I-272). Claude Code 2.1.283 refuses
    # that mode outside a git repository ("Worktree mode requires a git
    # repository or WorktreeCreate hooks configured"), and a machine made
    # from a plain folder has none (I-1065), so that one serves sessions
    # in the checkout instead.
    spawn=worktree
    git -C "$dir" rev-parse --git-dir >/dev/null 2>&1 || spawn=same-dir

    # The restart loop: server mode exits after about 10 minutes without
    # network, which is what a freeze and a thaw look like from inside
    # (I-822), and the window must come back by itself. A start that ends
    # within a minute is a refusal that will not pass (an organization
    # policy, a login that went), so it waits a minute instead of
    # spinning; one that lasted longer is a reconnect and waits 5 s.
    # POSIX shell only: tmux starts the window with the user's shell.
    # shellcheck disable=SC2016 # the window's shell expands these, not this one
    loop='while :; do
      s=$(date +%s)
      claude remote-control --spawn '"$spawn"' --name '"$slug"' --no-chrome || true
      if [ $(( $(date +%s) - s )) -lt 60 ]; then sleep 60; else sleep 5; fi
    done'
    # A window opened by a client that is not attached takes this
    # process's PATH, not the tmux server's, and this one has no agent on
    # it (the same reason tmux.nix reopens windows this way, I-640).
    login_path=$(repose-login-path 2>/dev/null || true)
    tmux_bin=$(command -v tmux)
    if PATH="''${login_path:-$PATH}" "$tmux_bin" new-window -d -t "=$slug" -n remote-control -c "$dir" "$loop" 2>/dev/null; then
      tmux set-option -w -t "$target" @repose-remote-control 1 2>/dev/null || true
    fi
    exit 0
  '';
}
