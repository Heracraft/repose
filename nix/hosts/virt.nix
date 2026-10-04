# Hypervisor plumbing: Cloud Hypervisor, virtiofsd, the users hostd drops
# to, /dev/kvm permissions, and the store guests see.
#
# Each guest's virtiofsd serves a store view of its own closure only
# (DECISIONS I-463): /run/repose/store-view is an empty tmpfs inside that
# virtiofsd unit's private mount namespace, and hostd binds the closure's
# store paths into it. Nothing of other projects' closures, their fragment
# sources or the host's own system is in it. The directory below is only
# the mount point; on the host it stays empty.
#
# The whole-store export (/run/repose/store-export, a read-only bind of
# /nix/store with an empty tmpfs over `.links`) is kept for one release:
# guests started before I-463 still use it until their next start, and
# `hostd --store-export /run/repose/store-export` switches back to it.
# Remove it in the release after.
{ config, lib, pkgs, ... }:
let
  exportDir = "/run/repose/store-export";
  storeExport = pkgs.writeShellApplication {
    name = "repose-store-export";
    runtimeInputs = [ pkgs.util-linux pkgs.coreutils ];
    text = ''
      # NixOS bind-mounts /nix/store read-only; creating .links needs the
      # same temporary rw remount nix itself uses.
      if [ ! -d /nix/store/.links ]; then
        mount -o remount,bind,rw /nix/store
        mkdir -p /nix/store/.links
        mount -o remount,bind,ro /nix/store
      fi
      mkdir -p ${exportDir}
      if ! mountpoint -q ${exportDir}; then
        mount --bind /nix/store ${exportDir}
      fi
      # The bind starts in /nix/store's peer group (shared propagation), so
      # the tmpfs mounted over .links below would also appear on
      # /nix/store/.links and every store write would fail with EROFS
      # (DECISIONS I-61). Make the export private first.
      mount --make-private ${exportDir}
      mount -o remount,bind,ro,nosuid,nodev ${exportDir}
      if ! mountpoint -q ${exportDir}/.links; then
        mount -t tmpfs -o ro,nosuid,nodev,noexec,size=4k,mode=0555 repose-links-mask ${exportDir}/.links
      fi
      echo "store export ready at ${exportDir}"
    '';
  };
in
{
  environment.systemPackages = [
    pkgs.cloud-hypervisor # ships cloud-hypervisor and ch-remote
    pkgs.virtiofsd
  ];

  users.groups.virtiofsd = { };
  users.users.virtiofsd = {
    isSystemUser = true;
    group = "virtiofsd";
    # In group hostd for two reasons the first host found (DECISIONS I-69):
    # the per-guest directory is 1770 root:hostd (I-49), which it must
    # traverse to reach its own socket directory, and `--socket-group hostd`
    # is a chgrp an unprivileged process may only do into a group it is in.
    # Group membership gives it no write access under the store export.
    extraGroups = [ "hostd" ];
    description = "virtiofsd store share (no write access anywhere under the store)";
  };

  # The Claude login share (DECISIONS I-278): one directory per user under
  # /var/lib/repose/users, served into each of that user's guests by a
  # virtiofsd@-like unit running as this account. It owns every user's
  # share and nothing else; the store's virtiofsd user cannot write a
  # credential and this one cannot read the store. In group hostd for the
  # store user's reasons (I-69): the guest directory it traverses and
  # `--socket-group hostd`.
  users.groups.repose-auth = { };
  users.users.repose-auth = {
    isSystemUser = true;
    group = "repose-auth";
    extraGroups = [ "hostd" ];
    description = "virtiofsd Claude login share (owns /var/lib/repose/users/*/claude-auth)";
  };

  # hostd itself runs as root (LVM, nftables, taps). The guest@<id> units
  # it starts, Cloud Hypervisor, run as this account (DECISIONS I-51,
  # security review H-2): it owns the taps (`ip tuntap add ... user hostd`),
  # is in `kvm` for /dev/kvm, and is the group of every guest volume through
  # the udev rule below. The unit's DeviceAllow then narrows a guest's
  # hypervisor to /dev/kvm, /dev/net/tun and its own volume.
  users.groups.hostd = { };
  users.users.hostd = {
    isSystemUser = true;
    group = "hostd";
    extraGroups = [ "kvm" ];
    description = "runs guest hypervisors; owner of guest tap devices";
  };

  services.udev.extraRules = ''
    KERNEL=="kvm", GROUP="kvm", MODE="0660"
    KERNEL=="vhost-vsock", GROUP="kvm", MODE="0660"
    KERNEL=="vhost-net", GROUP="kvm", MODE="0660"
    # Guest volumes g-<id> in vg-guests are group hostd so an unprivileged
    # guest@<id> can open its disk; snapshots (snap-*) and the pool stay
    # root:disk. DM_* come from lvm's own rules, which run first.
    SUBSYSTEM=="block", KERNEL=="dm-*", ENV{DM_VG_NAME}=="vg-guests", ENV{DM_LV_NAME}=="g-*", GROUP="hostd", MODE="0660"
  '';

  systemd.services.repose-store-export = {
    description = "Read-only /nix/store export for virtiofsd, with .links masked";
    wantedBy = [ "multi-user.target" ];
    before = [ "hostd.service" ];
    unitConfig.DefaultDependencies = false;
    after = [ "local-fs.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${storeExport}/bin/repose-store-export";
      ExecStop = "${pkgs.writeShellScript "repose-store-export-stop" ''
        ${pkgs.util-linux}/bin/umount -R ${exportDir} || true
      ''}";
    };
  };

  systemd.tmpfiles.rules = [
    "d /run/repose 0755 root root -"
    "d /run/repose/store-view 0755 root root -"
  ];
}
