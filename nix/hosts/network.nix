# Host networking. systemd-networkd manages the provider NIC (DHCP) and the
# guest bridge; the bridge address and the WireGuard tunnel are configured
# at boot from host.json by `repose-host-net`, because the host's guest /22
# and its WireGuard keys are assigned at registration, not at build time.
#
# repose-host-net renders, from /var/lib/repose/hostd/host.json:
#   /run/repose/host.env                 ids and addresses for other units
#   /run/repose/wg0.conf                 wg-quick config (0600)
#   /run/repose/host_ca.pub              sshd TrustedUserCAKeys
#   /run/repose/bootstrap_authorized_keys  the bootstrap keys while host_ca.pub
#                                        is empty, else nothing (only with
#                                        bootstrap.keyUntilHostCA, I-177)
#   /run/repose/sshd.conf                sshd ListenAddress
#   /run/systemd/network/20-br-guests.network.d/10-address.conf
# then restarts the units that read them. Without host.json it logs
# `no host.json; bridge not configured` and exits 0 so the rest of the host
# boots and hostd can register.
#
# Before host.json, one thing more (DECISIONS I-1380): a host with no
# route to the api's VNet address registers over WireGuard, and the api
# minted its place on the hub at `repose host add` -- a private key, an
# address and the edge's peer -- which arrives as /run/repose/hub.json
# (the Mac appliance's launcher share, mac.nix). When that file exists
# and host.json does not, wg0.conf is rendered from it, so `hostd
# register` reaches the control plane's hub address (I-1382). Register
# answers with a new key and the same address, host.json takes over at
# the restart the register unit asks for, and hub.json is removed.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  hostJson = "/var/lib/repose/hostd/host.json";
  runDir = "/run/repose";
  hubJson = "${runDir}/hub.json";
  keyUntilHostCA = cfg.bootstrap.enable && cfg.bootstrap.keyUntilHostCA;
  bootstrapKeys = pkgs.writeText "repose-bootstrap-keys" (lib.concatLines cfg.bootstrap.authorizedKeys);

  hostNet = pkgs.writeShellApplication {
    name = "repose-host-net";
    runtimeInputs = [ pkgs.jq pkgs.iproute2 pkgs.systemd pkgs.coreutils ];
    text = ''
      hostJson=${hostJson}
      run=${runDir}
      mkdir -p "$run"
      # 0755 whatever created it first: the join-token delivery once made
      # it 0700 and every guest's unprivileged virtiofsd then failed to
      # reach the store export (DECISIONS I-97).
      chmod 0755 "$run"
      umask 022

      write_sshd() {
        # $1: WireGuard address or empty. With no address sshd listens on
        # all interfaces and the nftables input chain is the gate (operator
        # ssh on the provider NIC only while bootstrap is on). Once
        # registration has given the host a WireGuard address, sshd binds
        # that alone even with bootstrap on: bootstrap is "until
        # registered", so the installer and the token delivery reach a new
        # host over the VNet and nothing does afterwards (DECISIONS I-92).
        {
          if [ -z "$1" ]; then
            echo "ListenAddress 0.0.0.0"
          else
            echo "ListenAddress $1"
          fi
        } > "$run/sshd.conf.tmp"
        mv "$run/sshd.conf.tmp" "$run/sshd.conf"
      }

      write_bootstrap_keys() {
        # bootstrap.keyUntilHostCA (I-177): the plain operator key works
        # only while the host has no Host CA to check certificates with.
        # sshd reads AuthorizedKeysFile at each login, so no restart.
        ${lib.optionalString keyUntilHostCA ''
        if [ -s "$run/host_ca.pub" ]; then
          : > "$run/bootstrap_authorized_keys.tmp"
          echo "Host CA present; bootstrap key not accepted"
        else
          cat ${bootstrapKeys} > "$run/bootstrap_authorized_keys.tmp"
          echo "no Host CA; bootstrap key accepted"
        fi
        chmod 0644 "$run/bootstrap_authorized_keys.tmp"
        mv "$run/bootstrap_authorized_keys.tmp" "$run/bootstrap_authorized_keys"
        ''}
        :
      }

      write_wg0() {
        # $1: a JSON file with private_key, address, edge_pubkey and
        # edge_endpoint under the key $2 (`.wg` of host.json, `.` of
        # hub.json). Prints 1 when it wrote the file, 0 when the material
        # is incomplete and the file was removed.
        local key address edge_pubkey endpoint
        key=$(jq -r "$2.private_key // empty" "$1")
        address=$(jq -r "$2.address // empty" "$1")
        edge_pubkey=$(jq -r "$2.edge_pubkey // empty" "$1")
        endpoint=$(jq -r "$2.edge_endpoint // empty" "$1")
        if [ -n "$key" ] && [ -n "$address" ] && [ -n "$edge_pubkey" ] && [ -n "$endpoint" ]; then
          (
            umask 077
            {
              echo "[Interface]"
              echo "PrivateKey = $key"
              echo "Address = $address"
              echo
              echo "[Peer]"
              echo "PublicKey = $edge_pubkey"
              echo "Endpoint = $endpoint"
              echo "AllowedIPs = 10.255.0.0/16"
              echo "PersistentKeepalive = 25"
            } > "$run/wg0.conf.tmp"
            mv "$run/wg0.conf.tmp" "$run/wg0.conf"
          )
          echo 1
        else
          rm -f "$run/wg0.conf"
          echo 0
        fi
      }

      if [ ! -s "$hostJson" ]; then
        echo "no host.json; bridge not configured"
        : > "$run/host_ca.pub"
        write_bootstrap_keys
        write_sshd ""
        rm -f "$run/host.env"
        rm -rf /run/systemd/network/20-br-guests.network.d
        networkctl reload
        systemctl --no-block reload-or-restart sshd.service
        if [ -s "${hubJson}" ] && [ "$(write_wg0 "${hubJson}" .)" = 1 ]; then
          # The bootstrap tunnel (I-1380): up before registration so
          # hostd register can reach the hub; nothing else binds it.
          echo "hub.json applied: wg0 configured for registration"
          systemctl --no-block restart wg-quick-wg0.service
        else
          rm -f "$run/wg0.conf"
          systemctl --no-block stop wg-quick-wg0.service
        fi
        exit 0
      fi
      # Registered: the bootstrap material is spent (I-1380).
      rm -f "${hubJson}"

      host_id=$(jq -er .host_id "$hostJson")
      guest_cidr=$(jq -er .guest_cidr "$hostJson")
      # WireGuard material and the Host CA arrive with registration by the
      # api; `hostdev` (DECISIONS I-17) registers a host without them, and
      # the bridge, sshd and the exporters must still come up (I-40).
      wg_address=$(jq -r '.wg.address // empty' "$hostJson")
      wg_ip=''${wg_address%/*}
      loki_url=$(jq -r '.loki_url // empty' "$hostJson")

      # Bridge address: network address + 1 with the guest prefix length.
      IFS='./' read -r a b c d prefix <<< "$guest_cidr"
      bridge_ip="$a.$b.$c.$((d + 1))"
      bridge_address="$bridge_ip/$prefix"

      loki_host=""
      loki_port=3100
      if [ -n "$loki_url" ]; then
        rest=''${loki_url#*://}
        rest=''${rest%%/*}
        loki_host=''${rest%%:*}
        case "$rest" in *:*) loki_port=''${rest##*:} ;; esac
      fi

      cat > "$run/host.env.tmp" <<ENV
      HOST_ID=$host_id
      GUEST_CIDR=$guest_cidr
      BRIDGE_ADDR=$bridge_ip
      WG_ADDR=$wg_ip
      LOKI_HOST=$loki_host
      LOKI_PORT=$loki_port
      ENV
      mv "$run/host.env.tmp" "$run/host.env"

      if [ "$(write_wg0 "$hostJson" .wg)" != 1 ]; then
        # wg-quick-wg0.service is conditioned on this file; without it the
        # tunnel stays down and sshd listens on every interface behind the
        # nftables input chain (operator ssh only while bootstrap is on).
        echo "no WireGuard material in host.json; wg0 not configured"
      fi

      # An empty CA file trusts nobody; the bootstrap key still works.
      jq -r '.host_ca_pub // empty' "$hostJson" > "$run/host_ca.pub.tmp"
      mv "$run/host_ca.pub.tmp" "$run/host_ca.pub"
      write_bootstrap_keys

      write_sshd "$wg_ip"

      mkdir -p /run/systemd/network/20-br-guests.network.d
      printf '[Network]\nAddress=%s\n' "$bridge_address" > /run/systemd/network/20-br-guests.network.d/10-address.conf
      networkctl reload
      networkctl reconfigure br-guests

      echo "host.json applied: host_id=$host_id guest_cidr=$guest_cidr bridge=$bridge_address"
      systemctl --no-block reload-or-restart sshd.service
      systemctl --no-block restart prometheus-node-exporter.service fluent-bit.service
      if [ -s "$run/wg0.conf" ]; then
        systemctl --no-block restart wg-quick-wg0.service
      else
        systemctl --no-block stop wg-quick-wg0.service
      fi
    '';
  };
in
{
  networking.useNetworkd = true;
  networking.useDHCP = false;
  networking.dhcpcd.enable = false;
  networking.firewall.enable = false; # nftables.nix is the firewall
  networking.nftables.enable = true;

  systemd.network.enable = true;
  systemd.network.wait-online.anyInterface = true;

  systemd.network.networks."10-uplink" = {
    matchConfig.Name = cfg.uplinkInterface;
    networkConfig = {
      DHCP = "ipv4";
      IPv6AcceptRA = true;
    };
    linkConfig.RequiredForOnline = "routable";
  };

  systemd.network.netdevs."20-br-guests" = {
    netdevConfig = {
      Kind = "bridge";
      Name = "br-guests";
    };
    # No STP: taps never form loops, and a listening delay would add
    # seconds to every guest start. Guest-to-guest forwarding is stopped by
    # the nftables bridge table and by hostd setting every tap `isolated on`.
    bridgeConfig.STP = false;
  };

  systemd.network.networks."20-br-guests" = {
    matchConfig.Name = "br-guests";
    networkConfig = {
      ConfigureWithoutCarrier = true;
      DHCP = "no";
      LinkLocalAddressing = "no";
      IPv6AcceptRA = false;
    };
    linkConfig.RequiredForOnline = "no";
  };

  # hostd creates taps and attaches them itself; wg-quick owns wg0.
  systemd.network.networks."30-unmanaged" = {
    matchConfig.Name = "tap-* wg0";
    linkConfig.Unmanaged = true;
  };

  systemd.services.repose-host-net = {
    description = "Configure br-guests, wg0 and per-host files from host.json";
    wantedBy = [ "multi-user.target" ];
    after = [ "systemd-networkd.service" "nftables.service" ];
    wants = [ "systemd-networkd.service" ];
    before = [ "hostd.service" "wg-quick-wg0.service" "sshd.service" ];
    path = [ pkgs.iproute2 ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${hostNet}/bin/repose-host-net";
    };
  };

  networking.wg-quick.interfaces.wg0 = {
    configFile = "${runDir}/wg0.conf";
    autostart = true;
  };

  systemd.services.wg-quick-wg0 = {
    unitConfig.ConditionPathExists = "${runDir}/wg0.conf";
    after = [ "repose-host-net.service" ];
    partOf = [ "repose-host-net.service" ];
  };

  environment.systemPackages = [ hostNet ];
}
