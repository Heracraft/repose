# Rootful Docker with overlay2 on the ext4 thin volume, compose plugin, and
# an address pool that can never overlap the guest network 10.64.0.0/12
# (docs/workstreams/02-guest-base.md "Docker address pools").
{ config, lib, pkgs, ... }:
{
  virtualisation.docker = {
    enable = true;
    storageDriver = "overlay2";
    enableOnBoot = true;
    autoPrune.enable = false;
    daemon.settings = {
      log-driver = "json-file";
      log-opts = { max-size = "50m"; max-file = "3"; };
      default-address-pools = [
        { base = "172.20.0.0/14"; size = 24; }
      ];
      # The default bridge, pinned to the first /24 of the pool, and the
      # DNS server every container gets: resolved's stub, which also
      # listens on 172.20.0.1 (network.nix). musl sends A and AAAA from one
      # socket at once, and straight to 1.1.1.1 the second answer was lost
      # past the guest, so an Alpine lookup waited 2.5 s for its retry;
      # through resolved it takes 0.01 s. The embedded resolver of user
      # networks (127.0.0.11) and BuildKit forward here too (DECISIONS I-537).
      bip = "172.20.0.1/24";
      dns = [ "172.20.0.1" ];
      # Containers keep running when dockerd stops or restarts; a base
      # update installs a new dockerd at the next boot anyway (I-536).
      # Incompatible with swarm, which no guest runs.
      live-restore = true;
      # The guest has no IPv6 route; Docker's default IPv6 probing only logs.
      ipv6 = false;
    };
  };

  # A base switch (guestd's 04:00 sweep) never restarts dockerd, which
  # would stop every container and leave one started without a restart
  # policy down; the new dockerd runs from the next boot (DECISIONS I-536,
  # the same failure as the tmux session's in I-496).
  systemd.services.docker.restartIfChanged = false;

  # `docker compose` (the CLI plugin, from nixpkgs docker's compose plugin)
  # and the standalone binary both work.
  environment.systemPackages = [ pkgs.docker-compose ];

  virtualisation.containerd.enable = lib.mkDefault false;
}
