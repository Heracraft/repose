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
    ./tools.nix
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

  # The guest never builds its own system: the host does (DESIGN.md §5), so
  # nothing that only serves `nixos-rebuild` inside the guest is installed.
  documentation.enable = false;
  documentation.nixos.enable = false;
  # NixOS's own handler reads a channel's programs.sqlite, which a flake
  # system does not have; devtools.nix installs the nix-index one (I-219).
  programs.command-not-found.enable = false;
}
