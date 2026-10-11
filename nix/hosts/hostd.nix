# The hostd unit, the guests slice, the nightly snapshot timer, and the
# state directories from host-conventions.md. hostd restarts at any time
# without touching guests: they are transient units in guests.slice, not
# its children (KillMode=process), and it reconciles from bbolt, LVM and
# `systemctl list-units 'guest@*'` at start (03-hostd).
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  hostd = cfg.hostdPackage;
  apiFlags = import ./api-flags.nix { inherit lib pkgs cfg; };
  # hostd refuses to start without a snapshot target (03-hostd §5.9): Blob
  # when the host has an account, its own disk otherwise.
  snapshotFlags =
    if cfg.snapshots.blobUrl != "" then
      "--blob-url ${lib.escapeShellArg cfg.snapshots.blobUrl} --blob-container ${lib.escapeShellArg cfg.snapshots.container}"
      + lib.optionalString (cfg.snapshots.identityClientId != "") " --blob-identity ${lib.escapeShellArg cfg.snapshots.identityClientId}"
    else
      "--snapshot-dir ${lib.escapeShellArg cfg.snapshots.localDir}";
  buildFlags = "--build-user ${lib.escapeShellArg cfg.buildUser}"
    + lib.optionalString (!cfg.claudeLoginShare) " --claude-login-share=false"
    + lib.optionalString (cfg.baseRepo.url != "") " --base-repo-url ${lib.escapeShellArg cfg.baseRepo.url}"
    + lib.optionalString (cfg.baseRepo.sshKeyFile != "") " --base-repo-ssh-key ${lib.escapeShellArg cfg.baseRepo.sshKeyFile}"
    + lib.optionalString (cfg.overlayCache.url != "") " --substituters ${lib.escapeShellArg "https://cache.nixos.org ${cfg.overlayCache.url}"}";
  stateDir = "/var/lib/repose/hostd";
  # The guest freeze (freeze.nix, DECISIONS I-652); nothing when it is off.
  freezeFlags = lib.optionalString cfg.freeze.enable (
    "--freeze --freeze-deadline ${toString cfg.freeze.deadline}s"
    + (if cfg.freeze.keyFile != "" then " --freeze-key-file ${lib.escapeShellArg cfg.freeze.keyFile}"
       else " --freeze-key-vault ${lib.escapeShellArg cfg.freeze.keyVaultUrl} --freeze-key-name ${lib.escapeShellArg cfg.freeze.keyName}")
    + lib.optionalString cfg.freeze.scheduledEvents " --scheduled-events --vm-name ${lib.escapeShellArg cfg.hostName}")
    + lib.optionalString cfg.freeze.freePageReporting " --free-page-reporting";

  # guests.slice gets everything but the host reserve: 8 GiB below 128 GiB
  # of RAM, 16 GiB above (DESIGN §4). Computed at boot; the slice cap is the
  # hard line that keeps hostd, virtiofsd and builds alive if the api's
  # reservation accounting is ever wrong.
  guestsSlice = pkgs.writeShellApplication {
    name = "repose-guests-slice";
    runtimeInputs = [ pkgs.systemd pkgs.gawk ];
    text = ''
      total=$(awk '/^MemTotal:/ { print $2 * 1024 }' /proc/meminfo)
      gib=$((1024 * 1024 * 1024))
      if [ "$total" -lt $((128 * gib)) ]; then reserve=$((8 * gib)); else reserve=$((16 * gib)); fi
      max=$((total - reserve))
      if [ "$max" -le 0 ]; then max=$((total / 2)); fi
      systemctl set-property --runtime guests.slice MemoryMax="$max"
      echo "guests.slice MemoryMax=$max (total=$total reserve=$reserve)"
    '';
  };
in
{
  systemd.slices.guests = {
    description = "Tenant guests (Cloud Hypervisor and virtiofsd transient units)";
    sliceConfig = {
      CPUWeight = 100;
      MemoryAccounting = true;
      CPUAccounting = true;
      IOAccounting = true;
    };
  };

  systemd.services.repose-guests-slice = {
    description = "Set guests.slice memory cap from the host's RAM";
    wantedBy = [ "multi-user.target" ];
    before = [ "hostd.service" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${guestsSlice}/bin/repose-guests-slice";
    };
  };

  systemd.services.hostd = {
    description = "repose hostd: runs tenants' guests on this host";
    wantedBy = [ "multi-user.target" ];
    wants = [
      "network-online.target"
      "repose-host-net.service"
      "repose-register.service"
      "repose-store-export.service"
      "repose-guests-slice.service"
    ];
    after = [
      "network-online.target"
      "nftables.service"
      "repose-host-net.service"
      "repose-register.service"
      "repose-store-export.service"
      "repose-guests-slice.service"
      "lvm2-monitor.service"
    ];
    path = with pkgs; [
      lvm2
      thin-provisioning-tools
      nftables
      iproute2
      util-linux
      e2fsprogs
      systemd
      nix
      git
      openssh
      cloud-hypervisor
      virtiofsd
      zstd
      coreutils
    ];
    serviceConfig = {
      ExecStart = "${hostd}/bin/hostd --state ${stateDir} ${apiFlags} ${snapshotFlags} ${buildFlags} ${freezeFlags}";
      Restart = "always";
      RestartSec = 2;
      # A join token that has been used is not retried (03-hostd §6).
      RestartPreventExitStatus = 3;
      # Guests are transient units, not children: a hostd restart or crash
      # must not stop them.
      KillMode = "process";
      LimitNOFILE = 1048576;
      # repose/builds and repose/guests are tmpfiles rules below: the build
      # user and virtiofsd must traverse them.
      StateDirectory = "repose/hostd";
      StateDirectoryMode = "0700";
      LogsDirectory = "repose";
      OOMScoreAdjust = -900;
      TimeoutStopSec = 30;
    };
  };

  # The operator commands docs/ops/RUNBOOK.md names (`hostd status`,
  # `hostd guests`, `hostd reconcile`, `hostd state export`) are this
  # binary over the control socket: it belongs on an operator's PATH.
  environment.systemPackages = [ hostd ];

  systemd.services.repose-snapshot = {
    description = "Nightly snapshot of every running guest";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${hostd}/bin/hostd snapshot-all";
    };
  };

  systemd.timers.repose-snapshot = {
    description = "Nightly snapshot at 03:00 local time";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "*-*-* 03:00:00";
      # A missed night is what the SnapshotStale alert is for; a snapshot
      # storm at boot is not wanted.
      Persistent = false;
      RandomizedDelaySec = "10min";
      Unit = "repose-snapshot.service";
    };
  };

  # Fragment evaluation and builds run as this account inside a transient
  # scope (12-nix-config-pipeline §5 "Flags and limits"): it owns each
  # revision's build directory (hostd chowns it), talks to nix-daemon, and
  # can read the base checkouts. /var/lib/repose/builds is 0711 so it can
  # traverse to its own directories and nothing else.
  users.groups.${cfg.buildUser} = { };
  users.users.${cfg.buildUser} = {
    isSystemUser = true;
    group = cfg.buildUser;
    home = "/var/lib/repose/${cfg.buildUser}";
    createHome = true;
    description = "repose tenant fragment builds";
  };
  nix.settings.allowed-users = [ cfg.buildUser ];

  systemd.tmpfiles.rules = [
    "d /var/lib/repose 0755 root root -"
    # 0711: virtiofsd reaches its socket directory under its guest's
    # directory; the per-guest directories are 1770 root:hostd (I-51).
    "d /var/lib/repose/guests 0711 root root -"
    # Per-user Claude login shares (I-278); hostd creates each user's
    # directory, the auth virtiofsd traverses this one.
    "d /var/lib/repose/users 0711 root root -"
    "d /var/lib/repose/builds 0711 root root -"
    "d /var/lib/repose/base 0755 root root -"
    "d /var/log/repose 0750 root root -"
  ] ++ lib.optional (cfg.snapshots.blobUrl == "") "d ${cfg.snapshots.localDir} 0700 root root -";
}
