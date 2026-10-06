# Platform guest base module: everything a guest has regardless of the user's
# fragment. docs/workstreams/02-guest-base.md is the spec, and
# docs/interfaces/guest-conventions.md lists every path, name and variable
# this module guarantees. The module is self-contained: importing it as
# `nixosModules.guestBase` brings the agent overlay with it, so a plain
# nixosSystem (the VM tests) and the microvm runner see the same guest.
{ config, lib, pkgs, ... }:
{
  imports = [
    ./options.nix
    ./boot.nix
    ./network.nix
    ./users.nix
    ./ssh.nix
    ./docker.nix
    ./caches.nix
    ./tmux.nix
    ./herdr.nix
    ./tools.nix
    ./shell.nix
    ./git.nix
    ./devtools.nix
    ./tools-carry.nix
    ./compat.nix
    ./agents.nix
    ./claude-auth.nix
    ./agent-guide.nix
    ./browser.nix
    ./desktop.nix
    ./sysctl.nix
    ./tmp.nix
    ./env.nix
    ./guestd.nix
    ./store.nix
    ./profile.nix
    ./version.nix
  ];

  system.stateVersion = "26.11";
  # Lowest priority: the runner passes the real name on the kernel line and
  # the NixOS test driver names its nodes itself.
  networking.hostName = lib.mkOverride 1100 "repose-guest";
  time.timeZone = lib.mkDefault "UTC";
  i18n.defaultLocale = "C.UTF-8";

  # Man pages for what is installed (`man ls`, `man git`), and nothing
  # else from the documentation modules (DECISIONS I-534). Every
  # documentation.* option is gated on documentation.enable. No caches:
  # building the man-db index at each base build costs time and `man`
  # finds a page without it. The NixOS manual and options pages only serve
  # `nixos-rebuild`, and the host builds the guest's system (DESIGN.md §5).
  documentation.enable = true;
  documentation.man.enable = true;
  documentation.man.cache.enable = false;
  documentation.doc.enable = false;
  documentation.info.enable = false;
  documentation.dev.enable = false;
  documentation.nixos.enable = false;
  # NixOS's own handler reads a channel's programs.sqlite, which a flake
  # system does not have; devtools.nix installs the nix-index one (I-219).
  programs.command-not-found.enable = false;
}
