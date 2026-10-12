# Host hardening. One human account (root, via the Host CA or the bootstrap
# key), no sudo, tmpfs /tmp, bounded journal, weekly fstrim, and no
# automatic reboots: an unattended reboot kills every tenant, so kernel
# updates are applied by draining a host (workstream 03 owns Drain).
{ config, lib, pkgs, ... }:
{
  security.sudo.enable = false;
  security.polkit.enable = lib.mkDefault false;

  users.mutableUsers = false;
  # root's password is console-only and comes from a file placed by hand;
  # without it root is locked (vm-hardening.nix, DECISIONS I-654).
  # With bootstrap.keyUntilHostCA the keys are not static: repose-host-net
  # renders them into /run/repose/bootstrap_authorized_keys only while the
  # host has no Host CA (network.nix, DECISIONS I-177).
  users.users.root.openssh.authorizedKeys.keys =
    lib.mkIf (config.repose.host.bootstrap.enable && !config.repose.host.bootstrap.keyUntilHostCA)
      config.repose.host.bootstrap.authorizedKeys;
  services.openssh.authorizedKeysFiles =
    lib.mkIf (config.repose.host.bootstrap.enable && config.repose.host.bootstrap.keyUntilHostCA)
      [ "/run/repose/bootstrap_authorized_keys" ];

  # hostd loads tun devices and Cloud Hypervisor opens /dev/kvm after boot;
  # the module list in kernel.nix is the control instead.
  security.lockKernelModules = false;

  boot.tmp.useTmpfs = true;
  boot.tmp.cleanOnBoot = true;

  services.journald.settings.Journal = {
    SystemMaxUse = "4G";
    RateLimitIntervalSec = "30s";
    RateLimitBurst = 20000;
  };

  # Over SSH root has no password and, outside bootstrap, no static key:
  # logins are Host CA certificates (ssh.nix), which NixOS cannot see at
  # build time. The console password file may be absent.
  users.allowNoPasswordLogin = true;

  services.fstrim.enable = true;
  services.fstrim.interval = "weekly";

  system.autoUpgrade.enable = false;
  # A watchdog reboot is an unattended reboot; both stay at their default (off).
  systemd.settings.Manager.RuntimeWatchdogSec = "off";
  systemd.settings.Manager.RebootWatchdogSec = "off";

  # No LLMNR or mDNS responders: they would be the only sockets bound on
  # every interface, including the provider NIC.
  services.resolved.settings.Resolve = {
    LLMNR = "false";
    MulticastDNS = "false";
  };

  # Nothing on a host reads documentation or needs a shell for a tenant.
  documentation.enable = false;
  documentation.nixos.enable = false;
  programs.command-not-found.enable = false;
}
