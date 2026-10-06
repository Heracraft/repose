# herdr, the second multiplexer (DECISIONS I-501, I-503): the package in
# every guest, and the session unit a project that chooses herdr runs in
# place of repose-tmux-session. guest-conventions.md "Session units" and
# "herdr" are the contract.
#
# Neither session unit is wanted by anything. guestd's SetupProject
# writes ~/.repose/project.json for this boot and starts the unit its
# `multiplexer` names (I-503); each unit's ExecCondition runs
# repose-multiplexer-is, so a start of the wrong one (an older guestd
# starting the tmux unit by name, a hand-typed start) is skipped without
# failing, and the two never run together.
{ config, lib, pkgs, ... }:
let
  checkout = import ./checkout.nix { inherit pkgs; };
  herdr = pkgs.reposeHerdr;

  multiplexerIs = import ./multiplexer-is.nix { inherit pkgs; };

  # repose-herdr-workspace: the herdr counterpart of tmux's `shell`
  # window. When the running herdr server has no workspace labelled with
  # the checkout's directory name (`home` on a machine with no checkout),
  # it creates one there without taking focus. The server unit runs it
  # after start; the CLI runs it after the first sync. Exits 0 when herdr
  # is not running, and is safe to run again. At most 10 s waiting for
  # herdr and 5 s for the create.
  workspace = pkgs.writeShellApplication {
    name = "repose-herdr-workspace";
    runtimeInputs = [ herdr checkout pkgs.jq pkgs.systemd pkgs.coreutils ];
    text = ''
      state=$(systemctl --user show -p ActiveState --value repose-herdr-server.service 2>/dev/null || true)
      case "$state" in
        active|activating|reloading) ;;
        *) exit 0 ;;
      esac
      # Bounded by wall time, not by tries: the step runs as
      # ExecStartPost, inside the unit's start timeout and guestd's
      # SetupProject budget, and a herdr that accepts the connection
      # without answering (while it restores session.json) would make a
      # count of tries take minutes. Each try gets at most 2 s of the 10.
      list=
      deadline=$((SECONDS + 10))
      while [ "$SECONDS" -lt "$deadline" ]; do
        left=$((deadline - SECONDS))
        if [ "$left" -gt 2 ]; then left=2; fi
        if list=$(timeout "$left" herdr workspace list 2>/dev/null); then
          break
        fi
        list=
        sleep 0.25
      done
      if [ -z "$list" ]; then
        echo "repose-herdr-workspace: herdr did not answer within 10 s" >&2
        exit 0
      fi
      dir=$(repose-checkout)
      if [ "$dir" = "''${HOME:-/home/dev}" ]; then
        label=home
      else
        label=$(basename "$dir")
      fi
      if printf '%s' "$list" | jq -e --arg l "$label" \
        '[.result.workspaces[]? | select(.label == $l)] | length > 0' >/dev/null 2>&1; then
        exit 0
      fi
      if ! timeout 5 herdr workspace create --cwd "$dir" --label "$label" --no-focus >/dev/null; then
        echo "repose-herdr-workspace: herdr could not create the workspace $label" >&2
        exit 1
      fi
    '';
  };

  # repose-herdr-watch: ends the unit once no herdr server is left in it.
  # The unit has ExitType=cgroup, so it stays up while its cgroup holds
  # processes: a live handoff (`herdr update --handoff`) starts the new
  # server as a child of the old one, and the old one, the unit's main
  # process, then exits 0. With ExitType=main systemd would stop the unit
  # there and kill the new server and every pane. ExitType=cgroup alone
  # would keep the unit up with no server after a crash or `herdr server
  # stop`, for as long as one pane process outlived the hangup (a `nohup`
  # dev server), and nothing would restart it. The unit starts this
  # watcher in the background. A herdr server is a process named herdr
  # whose parent is dev's systemd ($MANAGERPID; guestd's rule, I-505); a
  # handed-off server gets that parent when the old one exits. Every
  # 0.5 s until a server shows up, then every 5 s, it looks for one, and
  # when there is none (or the main process ended before herdr ran) it
  # kills what is left of the cgroup. systemd then ends the unit and
  # restarts it (DECISIONS I-560).
  watch = pkgs.writeShellApplication {
    name = "repose-herdr-watch";
    runtimeInputs = [ pkgs.coreutils pkgs.gnused ];
    text = builtins.readFile ./herdr-watch.sh;
  };

  inherit (import ./herdr-config.nix { inherit pkgs herdr; }) seed;
  # repose-herdr-start, the unit's main process (DECISIONS I-563).
  start = import ./herdr-start.nix { inherit pkgs herdr; };
in
{
  # /run/current-system/sw/bin/herdr resolves: a laptop herdr's remote
  # discovery looks there.
  environment.systemPackages = [ herdr multiplexerIs workspace ];

  systemd.user.services.repose-herdr-server = {
    description = "repose: project herdr server";
    after = [ "default.target" ];
    # Never restarted by a switch: the unit's cgroup holds the herdr
    # server and every pane (I-496). A changed unit takes effect at the
    # server's next start.
    restartIfChanged = false;
    unitConfig = {
      StartLimitIntervalSec = 60;
      StartLimitBurst = 5;
    };
    serviceConfig = {
      Type = "simple";
      ExecCondition = "${multiplexerIs}/bin/repose-multiplexer-is herdr";
      ExecStartPre = "${seed}/bin/repose-herdr-config";
      # The login PATH without a login shell, and an environment whose
      # panes load the current one (see `start` above; I-563).
      ExecStart = "${start}/bin/repose-herdr-start";
      # The watcher runs in the background for the unit's life (see
      # `watch` above). A failed workspace step never stops the server.
      ExecStartPost = [
        "${pkgs.bash}/bin/bash -c '${watch}/bin/repose-herdr-watch &'"
        "-${workspace}/bin/repose-herdr-workspace"
      ];
      # Up while the cgroup holds processes, so a live handoff keeps every
      # pane; the watcher ends the unit when no server is left (I-560).
      ExitType = "cgroup";
      # The project's zone (TZ) and REPOSE_PROJECT.
      EnvironmentFile = "-/etc/repose/env";
      KillMode = "control-group";
      # A restarted server restores session.json and resumes its agents.
      # The unit ends when no server is left (a crash, an OOM kill of the
      # server, `herdr server stop`), and comes back 5 s later, as the
      # tmux unit does (I-551). A server that cannot start is tried five
      # times in a minute, then left failed until the next start
      # (DECISIONS I-560).
      Restart = "always";
      RestartSec = "5s";
      # herdr's panes run in this cgroup: the kernel's OOM kill of a test
      # run or dev server in a pane must not stop the unit, which would
      # take the server and every agent with it (the user manager's
      # default is stop). The tmux unit needs none: each tmux pane is a
      # scope of its own (DECISIONS I-560).
      OOMPolicy = "continue";
      TasksMax = "infinity";
      # No CPUWeight: herdr's panes live in this unit's cgroup, so a
      # weight would lift every build with them. guestd renices the
      # server's threads instead (I-505).
    };
  };
}
