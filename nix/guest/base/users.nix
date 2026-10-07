# The fixed guest user. Claude Code refuses --dangerously-skip-permissions
# as root, MCP configs key on absolute paths, and every doc says
# /home/dev/<slug>, so `dev` at uid 1000 is not optional (DECISIONS R3-13).
{ config, lib, pkgs, ... }:
{
  users.mutableUsers = false;
  # Access is certificate-only through the platform CA (ssh.nix); there is
  # deliberately no password anywhere in the guest.
  users.allowNoPasswordLogin = true;

  users.groups.dev.gid = 1000;
  users.users.dev = {
    isNormalUser = true;
    uid = 1000;
    group = "dev";
    home = "/home/dev";
    createHome = true;
    shell = pkgs.bash;
    extraGroups = [ "wheel" "docker" "kvm" ];
    # A user unit (repose-tmux-session) must run without a login session.
    linger = true;
    # No password: SSH is certificate-only and sudo needs none.
    hashedPassword = "!";
  };

  # A stop powers the guest off, and dev's user manager holds every tmux
  # pane, shell and agent. On host-01 its stop took 22 s on one of
  # waterville's stops and was the only unit any guest console showed
  # waiting ("A stop job is running for User Manager for UID 1000", 76
  # lines across the consoles there, 2026-10-07); the default bound is
  # 2 minutes. Whatever is left of it after 10 s is killed: a process that
  # has ignored SIGTERM and SIGHUP that long is not finishing anything,
  # and a stop's snapshot was taken before the shutdown began (I-404).
  # DECISIONS I-572.
  systemd.services."user@".serviceConfig.TimeoutStopSec = "10s";

  users.users.root = {
    # Locked: `passwd -S root` shows L. Root is reached by `sudo -i` only.
    hashedPassword = "!";
  };

  security.sudo.enable = true;
  security.sudo.wheelNeedsPassword = false;
  security.sudo.execWheelOnly = true;
}
