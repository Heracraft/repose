# One-shot registration before hostd: consumes /run/repose/join-token
# (written over SSH after the install on Azure, DECISIONS I-20; by the
# launcher's join unit on the Mac appliance, mac.nix, I-1380) through
# `hostd register`, which writes host.json, cert.pem and key.pem under
# /var/lib/repose/hostd and deletes the token. Skipped entirely once
# host.json exists or when there is no token (hostd then logs `waiting for
# join token` itself). A token the api rejects as used or invalid makes
# hostd exit 3, and the unit stops retrying; a network error retries every
# 30 seconds.
{ config, lib, pkgs, ... }:
let
  apiFlags = import ./api-flags.nix { inherit lib pkgs; cfg = config.repose.host; };
  hostd = config.repose.host.hostdPackage;
  stateDir = "/var/lib/repose/hostd";
  token = "/run/repose/join-token";
in
{
  systemd.services.repose-register = {
    description = "Register this host with the api using the join token";
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" ];
    # After wg0 too: a host with no route to the api's VNet address
    # registers over the bootstrap tunnel repose-host-net renders from
    # /run/repose/hub.json (network.nix, I-1380). On a VNet host wg0 is
    # skipped before registration and the ordering costs nothing.
    after = [ "network-online.target" "repose-host-net.service" "wg-quick-wg0.service" ]
      ++ lib.optional (config.repose.host.provider == "azure") "cloud-final.service";
    before = [ "hostd.service" ];
    unitConfig.ConditionPathExists = [
      "!${stateDir}/host.json"
      token
    ];
    path = [ pkgs.systemd ];
    serviceConfig = {
      Type = "oneshot";
      # Flags before the command (hostd parses "[flags] [command]"), and the
      # token flag is --join-token: with --token the unit exited 2 on host-01's
      # first boot while hostd's own startup registration succeeded.
      ExecStart = "${hostd}/bin/hostd --state ${stateDir} --join-token ${token} ${apiFlags} register";
      # The bridge, wg0 and sshd read host.json; apply it now.
      ExecStartPost = "${pkgs.systemd}/bin/systemctl --no-block restart repose-host-net.service";
      Restart = "on-failure";
      RestartSec = 30;
      RestartPreventExitStatus = 3;
      StateDirectory = "repose/hostd";
      StateDirectoryMode = "0700";
    };
  };
}
