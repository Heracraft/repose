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
    exec ${pkg}/bin/${bin} "$@"
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
