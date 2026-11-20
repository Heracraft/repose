# guestd: the in-guest daemon (workstream 04). This module only runs it and
# prepares the paths it needs (guest-conventions.md "Filesystem"):
# /run/repose (tmpfs, part of /run), /run/repose/secrets (0700 dev),
# /run/repose/mcp (0700 dev, the forward's sockets), and the hooks socket
# directory. guestd creates hooks.sock itself. The binary comes
# from the flake's packages.guestd through `repose.guestd.package`.
{ config, lib, pkgs, ... }:
{
  options.repose.guestd.package = lib.mkOption {
    type = lib.types.package;
    description = "The guestd binary package (flake packages.guestd).";
  };

  options.repose.guestd.pathsWait = lib.mkOption {
    type = lib.types.int;
    default = 1200;
    description = ''
      Seconds `repose-paths.service` waits for hostd's registration before
      it gives up and lets the boot continue without it (DECISIONS
      I-1408). It exits the moment the registration arrives, so this is
      only what a guest whose hostd never comes pays.

      **This must stay greater than `hostd --paths-timeout` plus `hostd
      --ready-timeout` on the slowest host that runs this closure** (10m
      and 5m on a Mac appliance, so 15 minutes, and this is 20). Raising
      either of those without raising this one re-opens the failure below;
      `nix/hosts/mac.nix` carries the same sentence beside them, and
      `TestTheGuestsPathsWaitOutlastsTheHostsPathsDeadline` reads both
      files and fails when the order breaks, because no evaluation sees
      both numbers (DECISIONS I-1416).

      Two clocks, and this one starts first, which is why the margin is
      not just the paths deadline: this unit is `after
      guestd.service`, so it begins waiting as soon as guestd's process
      starts, while the host's registration deadline begins later, at
      RegisterPaths after Ready. The guest therefore waits through the
      whole ready phase before the host's clock even starts.

      If it gives up first, home-manager runs against a store the guest's
      nix database does not know -- a start that reports success into a
      broken machine, with no deadline reported to anyone, which is why
      the order matters more than either number. At 180 s it was shorter
      than the host's deadline on any host slower than Azure, where the
      registration itself takes 2 min 6 s (ENG-80); at 600 s against a
      Mac's 600 s it was equal rather than greater, and so still expired
      first by the ready phase (ENG-124).

      The guest VM tests set it low: none of them runs a hostd, so every
      one of them otherwise waits this out before `multi-user.target`.
    '';
  };

  config = {
    systemd.tmpfiles.rules = [
      "d /run/repose 0755 root root -"
      "d /run/repose/secrets 0700 dev dev -"
      "d /run/repose/desktop 0700 dev dev -"
      # repose-mcp hold's sockets, one per forwarded MCP server (I-557).
      "d /run/repose/mcp 0700 dev dev -"
    ];

    systemd.services.guestd = {
      description = "repose guestd (vsock control agent)";
      wantedBy = [ "multi-user.target" ];
      # Before sshd so Ready is sent by something that saw sshd start; after
      # the tmpfiles rules so /run/repose exists. Not after docker.service:
      # dockerd took 2.4 s of a 13.8 s boot on host-01 with guestd, sshd and
      # everything behind them waiting for it, and nothing guestd does at
      # boot needs Docker (DECISIONS I-161). Docker starts in parallel;
      # guestd's docker_down warning allows it a grace to come up.
      before = [ "sshd.service" ];
      after = [ "systemd-tmpfiles-setup.service" "network.target" ];
      environment.GOMAXPROCS = "1";
      # A switch is run by guestd itself. Left to its defaults, the new
      # system's activation stops guestd when its binary changed, and the
      # stop kills the switch it is a child of before guestd is started
      # again (host-01, 2026-09-21, base 2026.09.21.3: three guests left
      # with no guestd, DECISIONS I-143). The activation now leaves guestd
      # alone; guestd restarts itself 3 s after it has answered hostd,
      # from a transient unit that is not its child.
      restartIfChanged = false;
      stopIfChanged = false;
      serviceConfig = {
        ExecStart = "${config.repose.guestd.package}/bin/guestd";
        Restart = "always";
        RestartSec = "2s";
        # Logging goes to the serial console (journal → console) so a frozen
        # root filesystem never blocks guestd's own writes.
        StandardOutput = "journal+console";
        StandardError = "journal+console";
        OOMScoreAdjust = -900;
        KillSignal = "SIGTERM";
      };
    };

    # The guest's nix database does not know the paths it sees through the
    # shared store until hostd sends RegisterPaths after Ready (DECISIONS
    # I-67). home-manager's activation is the first thing at boot that asks
    # nix about them, so it waits for guestd's stamp; a guest whose hostd
    # never comes proceeds after the timeout and the unit fails as before.
    systemd.services.repose-paths = {
      description = "Wait for hostd to register the shared store paths";
      wantedBy = [ "multi-user.target" ];
      after = [ "guestd.service" ];
      wants = [ "guestd.service" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${pkgs.writeShellScript "repose-paths-wait" ''
          # 0.1 s steps: this unit is on the path to the first login
          # (home-manager, then systemd-user-sessions), and a 0.5 s step
          # cost up to half a second of every boot (DECISIONS I-161).
          #
          # The wait is repose.guestd.pathsWait, which outlasts the
          # host's own registration deadline; its option says why.
          for _ in $(seq 1 ${toString (config.repose.guestd.pathsWait * 10)}); do
            [ -e /run/repose/paths-registered ] && exit 0
            sleep 0.1
          done
          echo "no path registration from hostd after ${toString config.repose.guestd.pathsWait} s; continuing without it"
        ''}";
      };
    };
    systemd.services.home-manager-dev = {
      after = [ "repose-paths.service" ];
      wants = [ "repose-paths.service" ];
    };

    environment.systemPackages = [ config.repose.guestd.package ];
  };
}
