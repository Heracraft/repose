# Wrap one agent: same package, same binary name, but the entry point sets
# the terminal environment, runs the idempotent hook registration for that
# agent, and execs the real binary with "$@". The binary is never modified
# (docs/features/agents.md "Claude login and the policy behind it").
{ pkgs }:
{ name, pkg }:
let
  bin = pkg.meta.mainProgram or name;
  devshell = pkgs.reposeDevshell;
  wrapper = pkgs.writeShellScript "repose-${bin}-wrapper" ''
    if [ -n "''${TMUX:-}" ]; then
      export TERM=tmux-256color
    fi
    export COLORTERM=truecolor
    # `repose run "prompt"` starts the agent as tmux new-window's command:
    # a bash that is neither login nor interactive, with the environment
    # of a tmux server a user unit started before the project's env or
    # any secret existed. PATH reaches it (sessionVariables, I-227);
    # REPOSE_PROJECT (/etc/repose/env) and named secrets
    # (/run/repose/secrets.env, e.g. CLAUDE_CODE_OAUTH_TOKEN) come only
    # from this file, which is idempotent (env.nix; DECISIONS I-241).
    if [ -r /etc/profile.d/repose.sh ]; then
      . /etc/profile.d/repose.sh
    fi
    export REPOSE_HOOK_AGENT=${bin}
    # Registration failures must never block an agent (a blocked agent is a
    # silently wasted night), so setup is best-effort.
    ${pkgs.repose-agent-setup}/bin/repose-agent-setup ${bin} || true
    # The checkout's dev environment: its .envrc, or its flake's dev shell
    # when it has no .envrc (DECISIONS I-259). Never blocks the agent.
    . ${devshell}
    _repose_devshell ${bin}
    unset -f _repose_devshell _repose_devshell_done _repose_devshell_flake \
      _repose_devshell_flake_root _repose_devshell_shadow _repose_devshell_prompt
    ${pkgs.lib.optionalString (bin == "codex") codexDaemon}
    exec ${pkg}/bin/${bin} "$@"
  '';

  # Interactive Codex runs its turns in a shared app-server daemon whose
  # environment is the one its first start had, so a codex started in
  # another checkout or worktree ran its commands with the first one's
  # dev shell PATH (DECISIONS I-547). The daemon stays on (I-487); an
  # interactive start whose dev environment (PATH and the direnv
  # directory) differs from the running daemon's gets --no-daemon. The
  # stamp is written when no daemon runs, so it names the environment of
  # the daemon this start brings up. exec, app-server and the other
  # subcommands are left as they are.
  codexDaemon = ''
    repose_mode=
    if [ $# -eq 0 ]; then
      repose_mode=top
    else
      case "$1" in -*) repose_mode=top ;; resume|fork) repose_mode=sub ;; esac
    fi
    for repose_a in "$@"; do [ "$repose_a" = --no-daemon ] && repose_mode=; done
    if [ -n "$repose_mode" ]; then
      repose_cd="''${CODEX_HOME:-$HOME/.codex}/app-server-daemon"
      repose_key=$(printf '%s\n%s\n' "''${DIRENV_DIR-}" "$PATH" | ${pkgs.coreutils}/bin/sha256sum)
      repose_key=''${repose_key%% *}
      repose_pid=$(${pkgs.gnused}/bin/sed -n 's/^{"pid":\([0-9][0-9]*\).*/\1/p' "$repose_cd/daemon.pid" 2>/dev/null) || repose_pid=
      if [ -n "$repose_pid" ] && ${pkgs.gnugrep}/bin/grep -qa app-server "/proc/$repose_pid/cmdline" 2>/dev/null; then
        if [ "$(${pkgs.coreutils}/bin/cat "$repose_cd/repose-env" 2>/dev/null)" != "$repose_key" ]; then
          if [ "$repose_mode" = top ]; then
            set -- --no-daemon "$@"
          else
            repose_a=$1; shift; set -- "$repose_a" --no-daemon "$@"
          fi
        fi
      else
        { ${pkgs.coreutils}/bin/mkdir -p "$repose_cd" && printf '%s\n' "$repose_key" > "$repose_cd/repose-env"; } 2>/dev/null || true
      fi
      unset repose_cd repose_key repose_pid
    fi
    unset repose_mode repose_a
  '';
in
pkgs.symlinkJoin {
  name = "repose-${name}-${pkg.version or "0"}";
  paths = [ pkg ];
  postBuild = ''
    rm -f $out/bin/${bin}
    cp ${wrapper} $out/bin/${bin}
    chmod +x $out/bin/${bin}
  '';
  passthru = {
    unwrapped = pkg;
    binary = bin;
    inherit (pkg) version;
  };
  # The wrapper itself is free; the licence check ran on `pkg` already.
  meta = (builtins.removeAttrs (pkg.meta or { }) [ "license" ]) // { mainProgram = bin; };
}
