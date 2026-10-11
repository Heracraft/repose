# Guest freeze across host restarts (DECISIONS I-652, I-653). Off by
# default. On, hostd freezes every running guest to an encrypted image of
# its memory when the host shuts down (Azure's graceful shutdown request
# included) and on `hostd freeze`, and restores each one at its next start;
# what does not restore starts cold as before.
#
# The shutdown hook is repose-guest-freeze.service: its ExecStop runs
# `hostd freeze --shutdown`. The guests' units (guest@, virtiofsd@,
# virtiofsd-auth@) are transient, so they cannot be ordered against it at
# creation; a drop-in on their template names gives each one
# Before=repose-guest-freeze.service, which systemd applies to transient
# units too and to those already running after the switch's daemon-reload
# (checked on systemd 255 with a transient unit, docs/research/guest-freeze.md).
# At shutdown systemd therefore stops the freeze unit, and waits for its
# ExecStop, before it stops any guest; and hostd and the pool stop after
# it. `hostd freeze --shutdown` does nothing unless the system is stopping,
# so a switch that restarts or removes this unit freezes nothing.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  fz = cfg.freeze;
  hostd = cfg.hostdPackage;
  guestUnitOrder = {
    overrideStrategy = "asDropin";
    before = [ "repose-guest-freeze.service" ];
  };
in
{
  options.repose.host.freeze = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Freeze running guests to an encrypted memory image when the host
        shuts down and restore them when it is back (DECISIONS I-652). Needs
        the image key: keyVaultUrl and keyName, or keyFile.
      '';
    };
    keyVaultUrl = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Key Vault holding the RSA key that wraps each image's key; the host identity (snapshots.identityClientId) needs Get and UnwrapKey on it (I-653).";
    };
    keyName = lib.mkOption {
      type = lib.types.str;
      default = "freeze-wrap";
      description = "Name of that key.";
    };
    keyFile = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "An RSA private key (PEM) on a path outside the host's disks, instead of Key Vault: staging and tests only.";
    };
    deadline = lib.mkOption {
      type = lib.types.ints.between 10 240;
      default = 120;
      description = ''
        Seconds a freeze of every guest may take; a snapshot that would
        not finish by then is not started, and guests not frozen are
        stopped by the shutdown as before. At most 240: the freeze unit
        may hold the shutdown for twice this plus 60 s, which must stay
        inside the about 600 s Azure waits for a graceful shutdown.
      '';
    };
    freePageReporting = lib.mkOption {
      type = lib.types.bool;
      default = fz.enable;
      description = "Start guests with a balloon that returns pages they free to the host, so images hold what guests use (I-652). Takes effect at each guest's next start.";
    };
    scheduledEvents = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Freeze guests on an Azure Scheduled Events Reboot or Redeploy notice
        and approve it. Turning it on turns Scheduled Events on for the VM:
        Azure then holds every reboot, the operator's own included, until it
        is approved or 15 minutes pass (RUNBOOK "Guest freeze").
      '';
    };
  };

  config = lib.mkIf fz.enable {
    assertions = [{
      assertion = fz.keyFile != "" || fz.keyVaultUrl != "";
      message = "repose.host.freeze.enable needs keyVaultUrl (with keyName) or keyFile.";
    }];

    systemd.services.hostd.path = [ pkgs.cryptsetup ];

    systemd.services.repose-guest-freeze = {
      description = "Freeze running guests before the host shuts down";
      wantedBy = [ "multi-user.target" ];
      wants = [ "hostd.service" ];
      after = [ "hostd.service" "repose-guest-pool.service" "local-fs.target" "network-online.target" ];
      # A switch never runs the ExecStop for a change to this unit.
      restartIfChanged = false;
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${pkgs.coreutils}/bin/true";
        ExecStop = "${hostd}/bin/hostd freeze --shutdown";
        # A snapshot cannot be interrupted, and hostd starts one only when
        # its estimate fits the deadline; twice the deadline leaves room
        # for an estimate that was off by half, and 60 s for the rest of
        # the freeze. 2 x 240 + 60 = 540 s, inside Azure's ~600 s wait.
        TimeoutStopSec = 2 * fz.deadline + 60;
      };
    };

    systemd.services."guest@" = guestUnitOrder;
    systemd.services."virtiofsd@" = guestUnitOrder;
    systemd.services."virtiofsd-auth@" = guestUnitOrder;
  };
}
