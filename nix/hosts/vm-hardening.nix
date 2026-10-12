# Settings every repose VM on Azure gets, hosts and the edge alike
# (DECISIONS I-649, I-654). The edge imports this file from nix/edge.
#
#   mana_ib      blacklisted. Nothing uses RDMA, and the failed teardown
#                calls on 2026-10-10 were mana_ib's. Hygiene, not a proven fix.
#   NVMe         io_timeout 240 s (Microsoft's guidance for Linux on NVMe):
#                a live migration stalls the disk for longer than the default
#                30 s. Set three ways: the kernel parameter for the next boot,
#                a udev rule for disks that appear later, and a oneshot that
#                writes the running disks at a switch. A real disk failure
#                then shows as stuck I/O for up to 4 minutes.
#   ethtool      the dead-VF step in the RUNBOOK (HostUnreachable) needs it.
#   boot menu    10 s, so a human on the serial console can pick the previous
#                generation when a new one does not boot.
#   console      root's password hash is read from a file placed on the
#                machine by hand, never from the repo. Without the file root
#                stays locked. SSH never takes a password (ssh.nix), so the
#                password is for the serial console only (RUNBOOK "Console
#                access").
{ config, lib, pkgs, ... }:
let
  ioTimeoutS = 240;
  consoleHash = "/var/lib/repose/root-console.hash";
in
{
  boot.blacklistedKernelModules = [ "mana_ib" ];
  boot.kernelParams = [ "nvme_core.io_timeout=${toString ioTimeoutS}" ];
  services.udev.extraRules = ''
    ACTION=="add|change", SUBSYSTEM=="block", KERNEL=="nvme*n*", ENV{DEVTYPE}=="disk", ATTR{queue/io_timeout}="${toString (ioTimeoutS * 1000)}"
  '';
  # No `udevadm trigger` at a switch: on a host it would replay the LVM
  # rules over every guest's dm device. The value is written directly.
  systemd.services.repose-nvme-timeout = {
    description = "NVMe io_timeout ${toString ioTimeoutS} s on the running disks";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
    };
    script = ''
      for f in /sys/block/nvme*n*/queue/io_timeout; do
        [ -e "$f" ] || continue
        echo ${toString (ioTimeoutS * 1000)} > "$f"
        echo "$f $(cat "$f")"
      done
    '';
  };

  environment.systemPackages = [ pkgs.ethtool ];

  boot.loader.timeout = 10;

  # Read at activation (each switch and each boot). A missing file leaves
  # the shadow entry "!": locked. The hash goes in with
  # `install -m 0400 /dev/stdin /var/lib/repose/root-console.hash`, then
  # `/run/current-system/activate` (RUNBOOK "Console access").
  users.users.root.hashedPasswordFile = consoleHash;
}
