# The `repose` nftables tables from docs/interfaces/host-conventions.md.
#
# inet repose
#   input      policy drop: lo, established, wg0 (ssh, exporters, Fluent Bit
#              metrics), DHCP and
#              ICMP on the provider NIC, ssh on the provider NIC only while
#              bootstrap is on; frames from guests go to guest_in.
#   guest_in   guests to the host: replies to flows the host itself opened
#              (operator ssh to a guest until the gateway exists), the
#              caches under the same per-guest connection cap and a rate
#              of their own (I-453), ICMP echo rate-limited, everything
#              else dropped (there is no DHCP; guests get static
#              addresses).
#   guest_fwd  policy drop: established both ways; the gateway over wg0 to
#              guest sshd; from guests: IPv6 dropped (guests have none),
#              tcp 25 dropped through smtp_drop (DECISIONS I-238), the
#              mining-pool ports dropped through stratum_drop (I-239), new
#              flows past the per-guest tracked-connection cap (I-453) or
#              over the per-guest rate (I-240) dropped through flows_drop;
#              then through guest_dyn (hostd's counters)
#              and then to the internet only: IMDS, the Azure wire server,
#              every private range (10.64.0.0/12 is other guests, the rest
#              is the VNet, the WireGuard mesh, link-local) all dropped,
#              except the edge's hook-ingest and noVNC relay ports.
#   guest_dyn  empty at boot; hostd adds `ip saddr <ip> counter name
#              egress-<guest_id>` rules and owns them. Reloading this
#              ruleset flushes the chains declared here and never touches
#              guest_dyn or its counters (see extraDeletions).
#   guest_smtp, guest_stratum, guest_flows
#              empty at boot; hostd adds `ip saddr <ip> counter name
#              <smtp|stratum|flows>-<guest_id>` to each, so a blocked
#              attempt is counted per guest before the static drop after
#              the jump. Owned by hostd like guest_dyn.
#   smtp_drop, stratum_drop, flows_drop
#              jump to hostd's counting chain, then drop. The drop is here,
#              not in hostd's rule, so a guest hostd has not (yet) given a
#              rule is still blocked.
#   nat        masquerade guest traffic leaving on the provider NIC.
#   output     policy accept for the host's own traffic, except the Nix build
#              accounts (the daemon's nixbld group, whose fixed-output
#              builders share the host's network namespace, and the build
#              user hostd evaluates fragments as), which go to build_out.
#   build_out  the build accounts reach the public internet on the provider
#              NIC and the resolved stub for DNS; IMDS, the wire server,
#              every private, loopback and link-local address, the mesh, the
#              guest bridge and the host's own services are dropped
#              (DECISIONS I-439).
#
# bridge repose
#   forward    policy drop: no frame is ever switched between two guests.
#   input      frames from a tap to the host: IPv4 and ARP only, and only
#              from the (mac, ip, port) tuple hostd registered in `guests`,
#              so a guest cannot spoof another guest's address or poison
#              the bridge's forwarding table.
{ config, lib, pkgs, ... }:
let
  cfg = config.repose.host;
  egress = cfg.guestEgress;
  uplink = cfg.uplinkInterface;
  edge = cfg.edgeWireGuardAddress;

  # Chains this configuration owns. They are declared (idempotently) and
  # flushed before every reload; hostd's chain and counters survive.
  inetChains = {
    input = "type filter hook input priority filter; policy drop;";
    output = "type filter hook output priority filter; policy accept;";
    guest_fwd = "type filter hook forward priority filter; policy drop;";
    nat = "type nat hook postrouting priority srcnat; policy accept;";
    guest_in = "";
    smtp_drop = "";
    stratum_drop = "";
    flows_drop = "";
    build_out = "";
  };
  bridgeChains = {
    forward = "type filter hook forward priority filter; policy drop;";
    input = "type filter hook input priority filter; policy drop;";
  };
  declare = family: name: chains: ''
    table ${family} ${name} {
      ${lib.concatStringsSep "\n  " (lib.mapAttrsToList (c: hook: "chain ${c} { ${hook} }") chains)}
    }
  '';
  flushes = family: name: chains:
    lib.concatStringsSep "\n" (map (c: "flush chain ${family} ${name} ${c}") (lib.attrNames chains));
  ports = l: lib.concatMapStringsSep ", " toString l;
  # The daemon runs every builder as a member of this group, so one match
  # covers all nixbld users however many there are (I-439).
  nixbldGid = config.ids.gids.nixbld;
in
{
  options.repose.host.guestEgress = {
    blockedTcpPorts = lib.mkOption {
      type = lib.types.listOf lib.types.port;
      # DECISIONS I-239: the stratum ports mining pools publish as their
      # defaults, minus any a developer commonly uses to reach a remote
      # service (4444 Selenium Grid, 8888 Jupyter, 9000/9999 too generic).
      default = [ 3333 5555 7777 14433 14444 ];
      description = "TCP destination ports a guest may not open outside the host, besides 25 (mining pools, I-239).";
    };
    flowRate = lib.mkOption {
      type = lib.types.ints.positive;
      default = 200;
      description = "New outbound flows per second a guest may open, sustained (DECISIONS I-240).";
    };
    flowBurst = lib.mkOption {
      type = lib.types.ints.positive;
      default = 2000;
      description = "New outbound flows a guest may open at once above flowRate before the rate applies (I-240).";
    };
    maxConnections = lib.mkOption {
      type = lib.types.ints.positive;
      # DECISIONS I-453: 55 guests (a D64's most, all small) at the cap
      # take 901,120 of the host's 1,048,576 conntrack entries (kernel.nix).
      default = 16384;
      description = "Connections one guest may hold in the host's connection tracking table at once, outbound and to the caches together (I-453).";
    };
  };

  config.assertions = [{
    # With auto-allocate-uids the daemon runs builders under a uid range of
    # its own and not the nixbld group, and build_out would match nothing.
    assertion = !(config.nix.settings.auto-allocate-uids or false);
    message = "repose hosts match Nix builders by the nixbld group in nftables (DECISIONS I-439); nix.settings.auto-allocate-uids would let them past build_out.";
  }];

  config.networking.nftables = {
    enable = true;
    flushRuleset = false;
    checkRuleset = true;
    # The check runs in a build sandbox without the build user; nobody
    # stands in for it there. The loaded ruleset resolves the real name.
    preCheckRuleset = ''
      sed -i 's/meta skuid "${cfg.buildUser}"/meta skuid "nobody"/' ruleset.conf
    '';

    # Applied before the ruleset on every start and reload, and on stop.
    extraDeletions = ''
      ${declare "inet" "repose" inetChains}
      ${flushes "inet" "repose" inetChains}
      ${declare "bridge" "repose" bridgeChains}
      ${flushes "bridge" "repose" bridgeChains}
    '';

    ruleset = ''
      table inet repose {
        # hostd-owned: per-guest egress counter rules. Declared here so it
        # exists from boot; never flushed by a reload.
        chain guest_dyn {
        }
        # hostd-owned: per-guest counters of blocked attempts (I-238..I-240).
        chain guest_smtp {
        }
        chain guest_stratum {
        }
        chain guest_flows {
        }

        # One token bucket per guest address for new outbound flows
        # (I-240). An element expires a minute after the guest's last new
        # flow; a reload keeps the elements.
        set guest_flow_rate {
          type ipv4_addr
          size 65535
          flags dynamic,timeout
          timeout 1m
        }
        # The same for new flows to the caches, which never leave the host
        # and so are not under guest_flow_rate (I-453).
        set guest_cache_rate {
          type ipv4_addr
          size 65535
          flags dynamic,timeout
          timeout 1m
        }
        # Each guest address's tracked connections, through the host and
        # to the caches (I-453). The host has one conntrack table; without
        # a cap one guest could fill it and every new flow on the host,
        # other guests' and the gateway's included, would be dropped. An
        # element goes when its last connection does.
        set guest_conns {
          type ipv4_addr
          size 65535
          flags dynamic
        }

        chain smtp_drop {
          jump guest_smtp
          counter drop
        }
        chain stratum_drop {
          jump guest_stratum
          counter drop
        }
        chain flows_drop {
          jump guest_flows
          counter drop
        }

        chain input {
          type filter hook input priority filter; policy drop;
          iifname "lo" accept
          # Guests first: conntrack marks a repeated ICMP echo as
          # established, which would skip the rate limit in guest_in.
          iifname "br-guests" jump guest_in
          ct state established,related accept
          ct state invalid drop

          # Operators and scrapes come over WireGuard only.
          iifname "wg0" tcp dport { 22, 9100, 9101, ${toString cfg.observability.fluentBitMetricsPort} } accept
          iifname "wg0" icmp type echo-request accept

          # The provider NIC: DHCP, ICMP that keeps TCP working, IPv6 ND.
          iifname "${uplink}" udp sport 67 udp dport 68 accept
          iifname "${uplink}" icmp type { destination-unreachable, time-exceeded, parameter-problem } accept
          iifname "${uplink}" icmp type echo-request limit rate 5/second accept
          iifname "${uplink}" icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept
          ${lib.optionalString cfg.bootstrap.enable ''
          # bootstrap: operator ssh on the provider NIC until the edge exists
          iifname "${uplink}" tcp dport 22 accept
          ''}
          counter drop
        }

        chain guest_in {
          # Replies to connections the host opened: operators reach a guest
          # on 22 by jumping through the host until the gateway exists
          # (DECISIONS I-74). `ct direction reply` is what keeps this from
          # being a way in: a guest's own first packet is the original
          # direction and falls through to the drop below, and a repeated
          # ICMP echo from a guest is original too, so the rate limit holds.
          ct direction reply ct state established,related accept
          ${lib.optionalString config.repose.host.caches.enable ''
          # The npm and Docker Hub caches, on the host services address
          # only (caches.nix, DECISIONS I-202), under the guest's
          # connection cap and a new-flow rate like outbound's (I-453).
          ip daddr ${config.repose.host.caches.address} tcp dport { ${toString config.repose.host.caches.npm.port}, ${toString config.repose.host.caches.docker.port} } ct state new add @guest_conns { ip saddr ct count over ${toString egress.maxConnections} } goto flows_drop
          ip daddr ${config.repose.host.caches.address} tcp dport { ${toString config.repose.host.caches.npm.port}, ${toString config.repose.host.caches.docker.port} } ct state new update @guest_cache_rate { ip saddr limit rate over ${toString egress.flowRate}/second burst ${toString egress.flowBurst} packets } goto flows_drop
          ip daddr ${config.repose.host.caches.address} tcp dport { ${toString config.repose.host.caches.npm.port}, ${toString config.repose.host.caches.docker.port} } accept
          ''}
          # A deliberate, rate-limited exception for debugging from a guest.
          icmp type echo-request limit rate 5/second accept
          counter drop
        }

        chain guest_fwd {
          type filter hook forward priority filter; policy drop;
          ct state established,related accept
          ct state invalid drop

          # The edge gateway relays SSH to guests over WireGuard.
          iifname "wg0" oifname "br-guests" ip daddr 10.64.0.0/12 tcp dport 22 accept

          iifname "br-guests" meta nfproto ipv6 counter drop
          # Mail straight to port 25 is how a rented machine sends spam; a
          # provider's authenticated submission on 465 and 587 stays open
          # (DECISIONS I-238). Before guest_dyn, so blocked attempts are
          # never counted as egress.
          iifname "br-guests" tcp dport 25 goto smtp_drop
          # Mining pools' default stratum ports (I-239).
          iifname "br-guests" tcp dport { ${ports egress.blockedTcpPorts} } goto stratum_drop
          # New flows past the guest's tracked-connection cap (I-453),
          # then past the per-guest rate: a scan or a flood, never a
          # package manager (I-240 has the measurement). Only the flows
          # over the limit are dropped; established ones are untouched.
          iifname "br-guests" ct state new add @guest_conns { ip saddr ct count over ${toString egress.maxConnections} } goto flows_drop
          iifname "br-guests" ct state new update @guest_flow_rate { ip saddr limit rate over ${toString egress.flowRate}/second burst ${toString egress.flowBurst} packets } goto flows_drop
          iifname "br-guests" jump guest_dyn

          # Azure instance metadata (managed-identity tokens) and the wire
          # server (extension secrets): never from a guest.
          iifname "br-guests" ip daddr { 169.254.169.254, 168.63.129.16 } counter drop
          # Other guests, on this host or any other.
          iifname "br-guests" ip daddr 10.64.0.0/12 counter drop
          # The edge: hook ingest and the noVNC relay, nothing else on the mesh.
          iifname "br-guests" oifname "wg0" ip daddr ${edge} tcp dport { 8443, 6081 } accept
          # Everything private: the VNet, the WireGuard mesh, link-local.
          iifname "br-guests" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16 } counter drop
          iifname "br-guests" oifname "${uplink}" counter accept
          counter drop
        }

        chain nat {
          type nat hook postrouting priority srcnat; policy accept;
          oifname "${uplink}" ip saddr 10.64.0.0/12 masquerade
        }

        chain output {
          type filter hook output priority filter; policy accept;
          # Tenant fragments are evaluated as the build user and built by
          # nix-daemon as nixbld users. A fixed-output builder runs in the
          # host's network namespace, so without this it could reach
          # everything the host can (DECISIONS I-439).
          meta skgid ${toString nixbldGid} jump build_out
          meta skuid "${cfg.buildUser}" jump build_out
        }

        chain build_out {
          # DNS through systemd-resolved's stub, which /etc/resolv.conf
          # names and the sandbox copies in; resolved itself asks Azure.
          ip daddr 127.0.0.53 meta l4proto { tcp, udp } th dport 53 accept
          # The host's own services: caches, exporters, hostd, anything on lo.
          oifname "lo" counter drop
          # IMDS (managed-identity tokens), the wire server, guests, the
          # VNet, the mesh, CGNAT, link-local, and the reserved ranges.
          ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 168.63.129.16, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/3 } counter drop
          ip6 daddr { ::1, fc00::/7, fe80::/10, ff00::/8 } counter drop
          # wg0, br-guests and anything else that is not the internet.
          oifname != "${uplink}" counter drop
        }
      }

      table bridge repose {
        # hostd-owned: one element per running guest, added at start and
        # removed at stop: { <mac> . <ip> . tap-<8hex> }.
        set guests {
          type ether_addr . ipv4_addr . ifname
        }

        chain forward {
          type filter hook forward priority filter; policy drop;
          counter drop
        }

        chain input {
          type filter hook input priority filter; policy drop;
          iifname != "tap-*" accept
          ether type arp ether saddr . arp saddr ip . iifname @guests accept
          ether type ip ether saddr . ip saddr . iifname @guests accept
          counter drop
        }
      }
    '';
  };
}
