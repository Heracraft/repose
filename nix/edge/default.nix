# The edge: the one public entry point to tenant environments
# (docs/workstreams/06-gateway-edge.md §5.1, docs/DESIGN.md §7). It runs:
#
#   * the SSH gateway on :22 (public), which relays a user into their guest;
#   * the WireGuard hub on 51820/udp that every host dials outbound, so the
#     gateway can reach guests without a host having any inbound port;
#   * the preview-proxy stub on :443 (a static page until the feature exists);
#   * the hook-ingest forwarder and the metrics endpoint on the WireGuard
#     address only;
#   * an operator sshd on 2222, key-only, restricted to the operator address
#     list and the WireGuard network.
#
# It is NixOS rather than a Coolify container because it needs raw TCP/UDP
# ports and the kernel's WireGuard, and a Coolify port mapping would cost an
# app its rolling deploys (DECISIONS R4-13). Built here, deployed with
# nixos-anywhere / nixos-rebuild by workstream 11; never installed on the dev
# box.
#
# The secrets and certificates the units reference are placed under
# /var/lib/repose/edge/ by `repose-admin edge init` and workstream 11's
# deploy (§5.1); every unit is conditioned on its files existing, so a fresh
# edge boots and the gateway comes up as soon as they are in place.
{ config, pkgs, lib, ... }:
let
  cfg = config.repose.edge;
  stateDir = "/var/lib/repose/edge";
  tlsDir = "${stateDir}/tls";

  # Indented strings strip the common leading whitespace of their own
  # literal lines, not of interpolated ones, so a multi-line value has to
  # carry the post-strip indentation itself. nftables does not care; the
  # generated file being readable does.
  ruleSep = "\n    ";
  set = xs: "{ ${lib.concatStringsSep ", " xs} }";
  ports = xs: set (map toString xs);
  # Empty when no monitoring peer is declared, so the forward chain keeps
  # its established-only shape on an edge that nothing scrapes.
  monitoringForward =
    lib.optionals (cfg.monitoring.peerCIDRs != [ ]) [
      ''iifname "wg0" oifname "wg0" ip saddr ${set cfg.monitoring.peerCIDRs} tcp dport ${ports cfg.monitoring.scrapePorts} accept''
      ''iifname "wg0" oifname "wg0" ip daddr ${set cfg.monitoring.peerCIDRs} tcp dport ${ports cfg.monitoring.logPorts} accept''
    ];
in
{
  imports = [ ./disko.nix ];

  options.repose.edge = {
    osDevice = lib.mkOption {
      type = lib.types.str;
      default = "/dev/nvme0n1";
      description = "OS disk for the disko layout: /dev/nvme0n1 on the NVMe-only v7 sizes (DECISIONS I-39); /dev/sda on a SCSI size.";
    };

    gatewayPackage = lib.mkOption {
      type = lib.types.package;
      description = "The gateway binary (flake packages.gateway). Provides `gateway serve` and `gateway wgsync`.";
    };

    controlWgAddress = lib.mkOption {
      type = lib.types.str;
      default = "10.255.255.1";
      description = "The control plane's address on this hub (infra/azure/modules/coolify `wireguard_address`); the api's gRPC and /internal listeners are reached there, never on its public IP (ops/coolify/README.md).";
    };

    apiUrl = lib.mkOption {
      type = lib.types.str;
      default = "https://${cfg.controlWgAddress}:8444";
      defaultText = "https://<controlWgAddress>:8444";
      description = "Base URL of the api's /internal listener the gateway resolves routes and issues certificates through (docs/interfaces/api.md /internal; the `api-grpc` app on 8444, DECISIONS I-42). Not the public name: Coolify's proxy terminates TLS there and cannot present the gateway's client certificate.";
    };

    staticPeers = lib.mkOption {
      type = lib.types.listOf (lib.types.submodule {
        options = {
          publicKey = lib.mkOption { type = lib.types.singleLineStr; description = "The peer's WireGuard public key."; };
          allowedIPs = lib.mkOption { type = lib.types.listOf lib.types.str; description = "Addresses reached through this peer, e.g. the control plane's /32."; };
        };
      });
      default = [ ];
      description = ''
        Peers declared on wg0 that are not hosts: the control plane
        (DECISIONS I-92). Hosts are added at run time by wgsync from
        /internal/hosts; these are configured at boot and wgsync never
        removes them (WG_STATIC_PEERS). The peer dials this hub, so no
        endpoint is needed here.
      '';
    };

    wgAddress = lib.mkOption {
      type = lib.types.str;
      default = "10.255.0.1";
      description = "The edge's WireGuard address; the hub of the 10.255.0.0/16 plan this workstream owns (§5.1). Hosts get 10.255.0.x; the gateway binds its metrics and hook-ingest listeners here.";
    };

    wgPort = lib.mkOption {
      type = lib.types.port;
      default = 51820;
      description = "WireGuard listen port every host dials.";
    };

    operatorSSHPort = lib.mkOption {
      type = lib.types.port;
      default = 2222;
      description = "Operator sshd port; 22 belongs to the user gateway. Every host provisioner jumps through it (DECISIONS I-23).";
    };

    operatorCIDRs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Source ranges allowed to reach the operator sshd on ${toString cfg.operatorSSHPort}, in addition to the WireGuard network. The Azure NSG is the outer gate (workstream 11); this is defence in depth.";
    };

    lokiUrl = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Loki push URL Fluent Bit ships the edge journal to, over WireGuard (docs/ops/OBSERVABILITY.md). Empty disables shipping.";
    };

    monitoring = {
      peerCIDRs = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        example = [ "10.255.0.3/32" ];
        description = ''
          The monitoring server's address on this hub. It is the one peer
          that is neither a host nor the control plane, and the only one
          allowed to route *through* the edge to another peer: Prometheus
          lives outside Azure, hosts have no inbound, and the control
          plane's metrics are on its WireGuard address, so every scrape is
          a forwarded packet (ops/prometheus/wireguard-peer.conf).

          Empty leaves the forward chain as it was — established and
          related only — which is the right state for an edge with no
          monitoring peer.
        '';
      };

      scrapePorts = lib.mkOption {
        type = lib.types.listOf lib.types.port;
        default = [ 9100 9101 2021 9104 443 ];
        description = ''
          Ports the monitoring peer may reach on other peers:
          node_exporter (9100), hostd (9101) and Fluent Bit (2021) on a
          host; `api-grpc`'s published metrics port (9104) and the
          control plane's Traefik (443) on the control VM, because the
          `api` application publishes no port of its own and its
          /metrics is served by a router behind an IP allow-list
          (DECISIONS I-133, ops/coolify/README.md "The api's metrics").
          Not 9103: nothing listens on it outside the container.
          Nothing else is forwarded, so the monitoring peer cannot reach
          a host's sshd or a guest.
        '';
      };

      logPorts = lib.mkOption {
        type = lib.types.listOf lib.types.port;
        default = [ 3100 ];
        description = ''
          Ports peers may reach *on* the monitoring server: Loki's push
          endpoint, which every host's Fluent Bit writes to. The other
          direction of the same tunnel, and the reason a host that cannot
          reach Loki buffers for ever instead of failing (alert
          FluentBitStuck).
        '';
      };
    };
  };

  config = {
    # --- base / boot (Azure v7, NVMe-only; see nix/hosts/azure.nix) ---------
    boot.loader.systemd-boot.enable = true;
    boot.loader.efi.canTouchEfiVariables = true;
    boot.initrd.kernelModules = [ "hv_vmbus" "hv_netvsc" "hv_utils" "hv_storvsc" "pci-hyperv" "nvme" ];
    boot.initrd.availableKernelModules = [ "nvme" "pci-hyperv" ];
    boot.kernelParams = [ "console=ttyS0" "earlyprintk=ttyS0" "rootdelay=300" ];
    networking.usePredictableInterfaceNames = false;
    networking.hostName = lib.mkDefault "repose-edge";
    system.stateVersion = "26.11";
    time.timeZone = "UTC";

    # The edge routes guest traffic between the gateway process and the
    # WireGuard peers, so forwarding is on.
    boot.kernel.sysctl = {
      "net.ipv4.ip_forward" = 1;
      "net.ipv4.conf.all.rp_filter" = lib.mkForce 2; # loose: replies from a guest arrive via wg0, not the default route
    };

    # --- WireGuard hub -----------------------------------------------------
    # Peers are added at runtime by `gateway wgsync` from GET /internal/hosts
    # (§5.6); none are declared here. The private key is placed by workstream
    # 11 / `repose-admin edge init`.
    networking.wireguard.enable = true;
    networking.wireguard.interfaces.wg0 = {
      ips = [ "${cfg.wgAddress}/16" ];
      listenPort = cfg.wgPort;
      privateKeyFile = "${stateDir}/wg.key";
      peers = map (p: { inherit (p) publicKey allowedIPs; }) cfg.staticPeers;
    };
    # wg-quick/wireguard-wg0 must not start before its key exists, or the
    # unit fails on a fresh edge; wgsync (below) is what fills the peers.
    systemd.services."wireguard-wg0".unitConfig.ConditionPathExists = "${stateDir}/wg.key";

    # --- firewall: exactly the ports of §5.1 -------------------------------
    networking.firewall.enable = false; # the nftables table below is the firewall
    networking.nftables.enable = true;
    networking.nftables.ruleset = ''
      table inet repose-edge {
        # Per-source bounds on the gateway port (DECISIONS I-435): open
        # connections from one address, and new ones a second. The
        # gateway's own pre-auth limits are the finer check; these keep a
        # flood from reaching it at all.
        set gw_conns4 { type ipv4_addr; flags dynamic; size 65535; }
        set gw_conns6 { type ipv6_addr; flags dynamic; size 65535; }
        set gw_rate4 { type ipv4_addr; flags dynamic,timeout; timeout 1m; size 65535; }
        set gw_rate6 { type ipv6_addr; flags dynamic,timeout; timeout 1m; size 65535; }

        chain input {
          type filter hook input priority filter; policy drop;
          iifname "lo" accept
          ct state established,related accept
          ct state invalid drop

          # Public: the user SSH gateway, the preview stub, the WireGuard hub.
          tcp dport 22 ct state new add @gw_conns4 { ip saddr ct count over 64 } counter reject with tcp reset
          tcp dport 22 ct state new add @gw_conns6 { ip6 saddr ct count over 64 } counter reject with tcp reset
          tcp dport 22 ct state new update @gw_rate4 { ip saddr limit rate over 20/second burst 40 packets } counter drop
          tcp dport 22 ct state new update @gw_rate6 { ip6 saddr limit rate over 20/second burst 40 packets } counter drop
          tcp dport 22 accept
          tcp dport 443 accept
          udp dport ${toString cfg.wgPort} accept

          # Operator sshd: the WireGuard network and the documented operator
          # addresses only.
          ip saddr 10.255.0.0/16 tcp dport ${toString cfg.operatorSSHPort} accept
          ${lib.concatMapStringsSep "\n          "
            (c: ''ip saddr ${c} tcp dport ${toString cfg.operatorSSHPort} accept'')
            cfg.operatorCIDRs}

          # Guests reach the hook-ingest and noVNC-relay ports over WireGuard.
          iifname "wg0" tcp dport { 8443, 6081 } accept

          # Scrapes over WireGuard only: node_exporter and the gateway's own
          # metrics.
          iifname "wg0" tcp dport { 9100, 9102 } accept
          iifname "wg0" icmp type echo-request accept

          # ICMP that keeps TCP working on the public NIC.
          icmp type { destination-unreachable, time-exceeded, parameter-problem, echo-request } limit rate 5/second accept
          icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept
          counter drop
        }

        chain forward {
          type filter hook forward priority filter; policy drop;
          # The gateway originates every connection to a guest, so its return
          # traffic is `established`; hosts never route through the edge to
          # one another (that is enforced by per-host AllowedIPs).
          ct state established,related accept
          # The one exception, and why it is one: Prometheus is not in Azure
          # and hosts have no inbound, so a scrape is a packet the edge
          # forwards from the monitoring peer to a host or to the control
          # plane, and Fluent Bit's push to Loki is the same packet the
          # other way. Named ports from named addresses; everything else
          # between peers stays dropped. With no monitoring peer declared
          # these lines are absent and the chain is what it was.
          ${lib.concatStringsSep ruleSep monitoringForward}
          counter drop
        }

        chain output {
          type filter hook output priority filter; policy accept;
        }
      }
    '';

    # --- the gateway service ------------------------------------------------
    systemd.services.gateway = {
      description = "repose SSH gateway (relay to tenant guests)";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" "wireguard-wg0.service" ];
      wants = [ "network-online.target" ];
      # Needs the mTLS client cert, the api CA and the host key; without them
      # it cannot reach /internal or present a verifiable host certificate.
      unitConfig.ConditionPathExists = [
        "${stateDir}/gateway.crt"
        "${stateDir}/ssh_host_ed25519_key"
      ];
      environment = {
        API_URL = cfg.apiUrl;
        GATEWAY_CLIENT_CERT = "${stateDir}/gateway.crt";
        GATEWAY_CLIENT_KEY = "${stateDir}/gateway.key";
        API_CA = "${stateDir}/api-ca.pem";
        HOST_KEY = "${stateDir}/ssh_host_ed25519_key";
        HOST_CERT = "${stateDir}/ssh_host_ed25519_key-cert.pub";
        GATEWAY_SSH_KEY = "${stateDir}/gateway_ssh_key";
        GATEWAY_LISTEN = ":22";
        METRICS_LISTEN = "${cfg.wgAddress}:9102";
        HOOK_LISTEN = "${cfg.wgAddress}:8443";
        HOOK_TLS_CERT = "${tlsDir}/edge-internal.crt";
        HOOK_TLS_KEY = "${tlsDir}/edge-internal.key";
        PREVIEW_LISTEN = ":443";
        PREVIEW_TLS_CERT = "${tlsDir}/wildcard.crt";
        PREVIEW_TLS_KEY = "${tlsDir}/wildcard.key";
      };
      serviceConfig = {
        ExecStart = "${cfg.gatewayPackage}/bin/gateway serve";
        # 22 and 443 are privileged; DynamicUser needs the capability to bind
        # them. Everything else the gateway touches is a file it reads and a
        # network dial, which need no privilege.
        DynamicUser = true;
        SupplementaryGroups = [ "repose-edge" ];
        AmbientCapabilities = [ "CAP_NET_BIND_SERVICE" ];
        CapabilityBoundingSet = [ "CAP_NET_BIND_SERVICE" ];
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        ReadOnlyPaths = [ stateDir ];
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
        Restart = "always";
        RestartSec = 1;
      };
    };

    # --- wgsync: WireGuard peer reconciler ---------------------------------
    # A separate unit so CAP_NET_ADMIN (needed to add peers and routes) is
    # not held by the relay. It loops internally every 30 s (§5.6).
    systemd.services.wgsync = {
      description = "repose WireGuard peer sync from the api host list";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" "wireguard-wg0.service" ];
      wants = [ "network-online.target" ];
      unitConfig.ConditionPathExists = "${stateDir}/gateway.crt";
      path = [ pkgs.wireguard-tools pkgs.iproute2 ];
      environment = {
        API_URL = cfg.apiUrl;
        GATEWAY_CLIENT_CERT = "${stateDir}/gateway.crt";
        GATEWAY_CLIENT_KEY = "${stateDir}/gateway.key";
        API_CA = "${stateDir}/api-ca.pem";
        WG_INTERFACE = "wg0";
        WG_STATIC_PEERS = lib.concatMapStringsSep "," (p: p.publicKey) cfg.staticPeers;
      };
      serviceConfig = {
        ExecStart = "${cfg.gatewayPackage}/bin/gateway wgsync";
        DynamicUser = true;
        SupplementaryGroups = [ "repose-edge" ];
        AmbientCapabilities = [ "CAP_NET_ADMIN" ];
        CapabilityBoundingSet = [ "CAP_NET_ADMIN" ];
        NoNewPrivileges = true;
        Restart = "always";
        RestartSec = 5;
      };
    };

    # A group the gateway and wgsync join to read the certificates and keys
    # under stateDir that repose-admin/workstream 11 place there (mode 0640
    # root:repose-edge).
    users.groups.repose-edge = { };
    systemd.tmpfiles.rules = [
      "d ${stateDir} 0750 root repose-edge -"
      "d ${tlsDir} 0750 root repose-edge -"
    ];

    # --- operator sshd on 2222 --------------------------------------------
    # Key-only; the nftables table and the NSG restrict who reaches it. Root
    # is the only account (this is an appliance), certificate or the deploy
    # key. Password auth off, and a verbose log so a probe is recorded
    # (review M-2).
    services.openssh = {
      enable = true;
      ports = [ cfg.operatorSSHPort ];
      openFirewall = false;
      settings = {
        PermitRootLogin = "prohibit-password";
        PasswordAuthentication = false;
        KbdInteractiveAuthentication = false;
        AllowUsers = [ "root" ];
        LogLevel = "VERBOSE";
        X11Forwarding = false;
      };
    };

    # --- observability -----------------------------------------------------
    services.prometheus.exporters.node = {
      enable = true;
      port = 9100;
      listenAddress = cfg.wgAddress;
      enabledCollectors = [ "systemd" "textfile" ];
    };
    systemd.services.prometheus-node-exporter = {
      after = [ "wireguard-wg0.service" ];
      unitConfig.ConditionPathExists = "${stateDir}/wg.key";
    };

    services.fluent-bit = lib.mkIf (cfg.lokiUrl != "") (
      let
        # lokiUrl is scheme://host[:port][/path]; take host and port.
        afterScheme = lib.last (lib.splitString "://" cfg.lokiUrl);
        hostPort = builtins.head (lib.splitString "/" afterScheme);
        lokiHost = builtins.head (lib.splitString ":" hostPort);
        parts = lib.splitString ":" hostPort;
        lokiPort = if lib.length parts > 1 then lib.last parts else "3100";
      in {
        enable = true;
        settings = {
          service.flush = 5;
          pipeline = {
            inputs = [{ name = "systemd"; tag = "edge.*"; read_from_tail = "on"; }];
            # The unit name as `component` and `service_name`, like a
            # host's (nix/hosts/fluent-bit.nix): Grafana groups log streams
            # by service_name and shows "unknown_service" without it.
            filters = [{
              name = "lua";
              match = "*";
              call = "unit";
              script = "${pkgs.writeText "repose-edge-labels.lua" ''
                function unit(tag, ts, record)
                  local u = record["_SYSTEMD_UNIT"] or record["SYSLOG_IDENTIFIER"] or "kernel"
                  u = string.gsub(u, "%.service$", "")
                  u = string.gsub(u, "@.*$", "")
                  u = string.gsub(u, "^session%-%d+%.scope$", "session")
                  record["component"] = u
                  record["service_name"] = u
                  return 2, ts, record
                end
              ''}";
            }];
            outputs = [{
              name = "loki";
              match = "*";
              host = lokiHost;
              port = lokiPort;
              labels = "host=edge";
              label_keys = "$component,$service_name";
              line_format = "json";
            }];
          };
        };
      });

    # --- hardening: an appliance, updated by rebuild, never auto ----------
    system.autoUpgrade.enable = false;
    services.fstrim.enable = true;
    documentation.enable = false;
    documentation.nixos.enable = false;
    users.mutableUsers = false;
    users.users.root.hashedPassword = "!";
    users.allowNoPasswordLogin = true;
    services.journald.settings.Journal.SystemMaxUse = "2G";
    services.resolved.settings.Resolve = { LLMNR = "false"; MulticastDNS = "false"; };

    # openssl: the gateway's key pair and its certificate requests are made
    # here so the private key never leaves the edge (ops/RUNBOOK.md "Edge").
    environment.systemPackages = with pkgs; [ wireguard-tools iproute2 openssl ];
  };
}
