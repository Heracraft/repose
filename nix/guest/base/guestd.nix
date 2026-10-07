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
          for _ in $(seq 1 1800); do
            [ -e /run/repose/paths-registered ] && exit 0
            sleep 0.1
          done
          echo "no path registration from hostd after 180 s; continuing"
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
