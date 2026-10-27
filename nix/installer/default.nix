# The kexec installer image every repose machine is installed through.
#
# nixos-anywhere, given no `--kexec`, has the *target* download a fixed
# upstream tarball (nixos-images release nixos-25.05,
# nixos-kexec-installer-noninteractive-x86_64-linux.tar.gz, Linux 6.14.10).
# That image is not ours: we do not choose its kernel, we cannot read its
# console, and we cannot fix it. host-01, host-stg-01 and host-02 were all
# installed by those same bytes, so the installer is as much a part of a
# host as nix/hosts is, and it belongs in this flake.
#
# What this image changes, and why each one:
#
#   1. The kernel is the host kernel pin, not "the newest kernel ZFS
#      builds against" (nixos-images' nix/latest-zfs-kernel.nix). An
#      installer on a different kernel than the system it installs is a
#      second set of network and storage drivers to debug, on a machine
#      that has no console history and no journal to come back to.
#
#   2. restore-network is off and two networkd units replace it. This is
#      the defect that cost 17 hours on host-02; see "Why MAC matching is
#      wrong here" below.
#
#   3. consoleLogLevel is 7, not the 4 nixos-images ships. At 4 no
#      netvsc, mana or DHCP message reaches the serial console, which is
#      why host-02's failed install and host-stg-01's successful one emit
#      an identical set of 134 systemd messages and nothing else. The
#      next failure has to be legible from the boot diagnostics alone.
#
#   4. ZFS and bcachefs are off. nix/hosts/disko.nix is GPT + ESP + ext4
#      with LVM for guests, so neither is ever used, and both couple a
#      kernel module build to the kernel pin: with them on, an installer
#      build is an out-of-cache zfs_unstable compile against 6.18.57.
#
# This image is for Azure machines (hosts and the edge), which take their
# address from DHCP. It is not a general-purpose installer: with
# restore-network off, a target whose address is static and not in DHCP
# would come up unreachable.
{ config, lib, ... }:
{
  options.repose.installer = {
    kernelPackages = lib.mkOption {
      type = lib.types.raw;
      description = ''
        The installer kernel. The flake sets it from the same pin as
        `repose.host.kernelPackages` so a host is installed by the kernel
        it then runs (DECISIONS I-778 owns the pin itself).
      '';
    };
  };

  config = {
    # The tarball's filename, and so the last element of the path infra
    # hands to `--kexec`: ours rather than the release asset's name, so a
    # path on an operator's disk or in a blob container says which image it
    # is. nixos-anywhere picks its decompression from the extension, so the
    # path it is given names this file and not the directory holding it.
    system.kexec-installer.name = "repose-kexec-installer";

    # mkForce because nixos-images' latest-zfs-kernel.nix sets this to
    # whatever the newest ZFS-compatible kernel in nixpkgs is.
    boot.kernelPackages = lib.mkForce config.repose.installer.kernelPackages;

    # nixos-images' restore-network.service reads the addresses and routes
    # kexec-run.sh captured from the outgoing system and writes one
    # 00-<ifname>.network per non-loopback link, matching on
    # `MACAddress=` with `DHCP=yes`.
    #
    # Why MAC matching is wrong here: on Azure accelerated networking a
    # VM has two links for one NIC, the synthetic one (hv_netvsc) and the
    # SR-IOV VF (mana), and they SHARE A MAC. So each generated unit
    # matches both links, and the installer configures and DHCPs the VF
    # as though it were a second NIC. Microsoft's rule is that addresses
    # belong to the synthetic interface only; the VF is a datapath the
    # synthetic interface transparently uses. On host-02 (2026-10-26)
    # that left a discovery loop that never converged: 3,256 B/min in,
    # ~3,450 B/min out, byte-exact for 17 hours, with nothing on the
    # console because of consoleLogLevel 4.
    #
    # The units below match on DRIVER instead. A driver tells the two
    # links apart where the MAC cannot, and unlike an interface name it
    # does not depend on how many PCI devices the size happens to expose.
    # This is the same split nix/hosts/network.nix already makes on an
    # installed host, where 10-uplink matches the uplink by name and the
    # VF matches no unit at all.
    systemd.services.restore-network.enable = false;

    # The VF: networkd must not touch it. Named 05 so it is read before
    # any unit that could claim it, including nixpkgs' catch-all
    # 99-ethernet-default-dhcp. mlx5_core and mlx4_core are the VF
    # drivers on the older Azure SKUs; mana is the one the Intel v7 sizes
    # hosts must run (docs/DESIGN.md §4) use.
    systemd.network.networks."05-vf-unmanaged" = {
      matchConfig.Driver = "mana mlx5_core mlx4_core";
      linkConfig.Unmanaged = true;
    };

    # The synthetic NIC: the only link that gets an address. This is the
    # installer's equivalent of nix/hosts/network.nix's 10-uplink, under
    # the same name so the two read as the same thing.
    systemd.network.networks."10-uplink" = {
      matchConfig.Driver = "hv_netvsc";
      networkConfig = {
        DHCP = "ipv4";
        IPv6AcceptRA = true;
      };
      linkConfig.RequiredForOnline = "routable";
    };

    # The kernel's own messages on the console, not just systemd's units.
    # nixos-images sets 4, which hides exactly the netvsc, mana and DHCP
    # lines a failed install needs.
    boot.consoleLogLevel = 7;

    # Unused by nix/hosts/disko.nix, and both drag a kernel-coupled build
    # that the binary cache has never seen for our kernel pin.
    boot.supportedFilesystems = {
      zfs = lib.mkForce false;
      bcachefs = lib.mkForce false;
    };
  };
}
