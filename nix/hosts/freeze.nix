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
      type = lib.types.ints.between 10 300;
      default = 240;
      description = ''
        Seconds a freeze of every guest may take. With writeRateMBs it is
        how much guest memory a host restart saves: snapshots are admitted
        until the ones already running plus the next would pass this, and
        the guests that do not fit are stopped by the shutdown as before,
        losing everything they were running (counted, and
        GuestLeftUnfrozen alerts). 240 at host-01's measured 400 MB/s is
        96 GB, more memory than its 64 GiB can place (DECISIONS I-796).
        The cost is a host restart that holds the shutdown as long as the
        freeze takes. At most 300: the freeze unit may hold the shutdown
        for this plus 150 s, and the rest of the shutdown (the guests'
        clean stops, virtiofsd, the unmounts, LVM, the kernel) needs its
        share of the about 600 s Azure waits for a graceful shutdown.
      '';
    };
    restoreTimeout = lib.mkOption {
      type = lib.types.ints.between 10 600;
      default = 180;
      description = ''
        Seconds one restore's surroundings may take at the next start: the
        shares, the image open, the resume and guestd's first answer. The
        image's own read is added to it from writeRateMBs, so this does
        not have to move when a host's guests grow -- raise it for a host
        whose restores are slow for another reason. A thaw that passes its
        window discards the images left, and those guests start cold
        having spent the previous shutdown being written (I-796).
      '';
    };
    writeRateMBs = lib.mkOption {
      type = lib.types.ints.between 10 5000;
      default = 100;
      description = ''
        MB/s the snapshots are assumed to write guest memory at, until
        this freeze's own first finished snapshot shows the real rate. It
        is what decides whether a guest's snapshot is started at all, so
        it is measured per host on the disk the guest thin pool is on,
        where the images are written (RUNBOOK "Guest freeze", DECISIONS
        I-796). The default of 100 is a floor for a host nobody has
        measured, not a safe guess: understating the rate is what skips
        guests, and a skipped guest loses everything it was running.
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

    # cryptsetup for the image's dm-crypt, btrfs-progs to make its
    # filesystem: the image is compressed btrfs, which is what makes a
    # freeze and a restore write and read a fraction of the guest's
    # memory (I-685).
    systemd.services.hostd.path = [ pkgs.cryptsetup pkgs.btrfs-progs ];

    # Loaded at boot, not when the host is already shutting down.
    boot.kernelModules = [ "btrfs" ];

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
        # systemd cuts the ExecStop at this timeout, and hostd killed
        # there counts nothing and writes no tally: the margin is not a
        # guess about snapshot sizes but a wall hostd keeps itself. A
        # FreezeAll waits for nothing past its deadline plus its 120 s
        # StopGrace (freeze.go), whatever a snapshot is doing, and then
        # reports; 150 s leaves 30 s for hostd to get there. At the
        # deadline's cap that is 450 s, which leaves the rest of the
        # shutdown its share of Azure's ~600 s; at the default, 390 s
        # (DECISIONS I-796).
        TimeoutStopSec = fz.deadline + 150;
      };
    };

    # guest@ gets the same Before= from hostd.nix's drop-in, which holds
    # the clean stop (I-650); a second text for that unit would clash.
    systemd.services."virtiofsd@" = guestUnitOrder;
    systemd.services."virtiofsd-auth@" = guestUnitOrder;
  };
}
