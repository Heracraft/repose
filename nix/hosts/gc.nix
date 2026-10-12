# Nix store policy on a host: weekly GC that keeps hostd's roots
# (/nix/var/nix/gcroots/repose/<guest_id>), free-space floors so a tenant
# build never fills the store, sandboxed builds capped to the numbers in
# DECISIONS R5-4, and only root trusted.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
in
{
  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 14d";
    randomizedDelaySec = "1h";
  };

  # No daily `nix-store --optimise` pass (DECISIONS I-652). It replaces a
  # store file already in use with a hard link to an identical one, so a
  # guest's virtiofsd holds a deleted inode it cannot name in a freeze
  # snapshot, and that guest's restore fails: on host-stg-01 the 03:59 run
  # (2026-10-12) cost all four frozen guests their restore. New paths are
  # still deduplicated as they are added (auto-optimise-store below),
  # before any guest can open them.
  nix.optimise.automatic = false;

  nix.settings = {
    min-free = 50 * 1024 * 1024 * 1024;
    max-free = 100 * 1024 * 1024 * 1024;
    sandbox = true;
    trusted-users = [ "root" ];
    allowed-users = [ "root" ];
    substituters = [ "https://cache.nixos.org" ] ++ lib.optional (cfg.overlayCache.url != "") cfg.overlayCache.url;
    trusted-public-keys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ]
      ++ lib.optional (cfg.overlayCache.publicKey != "") cfg.overlayCache.publicKey;
    max-jobs = 2;
    cores = 8;
    auto-optimise-store = true;
    experimental-features = [ "nix-command" "flakes" ];
  };

  # Tenant builds run under nix-daemon in system.slice; guests in
  # guests.slice weigh twice as much (hostd.nix), so a build never starves a
  # running agent.
  systemd.services.nix-daemon.serviceConfig.CPUWeight = 50;

  systemd.tmpfiles.rules = [
    "d /nix/var/nix/gcroots/repose 0755 root root -"
  ];
}
