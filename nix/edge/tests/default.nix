# NixOS VM test for the edge's hub rules (docs/workstreams/19-mac-mini.md
# item 19b, DECISIONS I-1368, I-1382): the edge built from the same module
# as the real one, with two host peers, the control plane and the
# monitoring peer on a real WireGuard hub, and real packets through the
# real ruleset.
#
# The fleet has one host, so "a host cannot reach another host" cannot be
# shown on staging with a `nc` from a second host; this is where the
# packet is sent. What is asserted, each a `nc` or `curl` through wg0:
#
#   * a host reaches no other host, by its wg address or its guests';
#   * a host reaches the api's gRPC at the control plane's hub address and
#     nothing else there (I-1382);
#   * a host pushes to the monitoring peer's Loki and nothing else there;
#     the control plane alone reaches its remote-write port;
#   * the monitoring peer scrapes a host;
#   * a host reaches nothing on the edge itself but ICMP echo: not the
#     operator sshd, not the scrapes, not the view relay; a guest behind
#     it reaches the hook ingest and nothing more;
#   * every refusal is counted in `host_forward_dropped` or
#     `host_input_dropped`, so a probe is a number an operator reads.
#
# WireGuard keys are made inside the VMs at run time and exchanged by the
# test script, as wgsync would exchange them; nothing is imported from a
# derivation.
{ pkgs, disko, edgeModule, gateway }:
let
  lib = pkgs.lib;
  edgeIP = "203.0.113.1";
  # The public side of the edge: the "internet" every peer dials from.
  peer = { lan, wg, guests ? null }: { pkgs, lib, ... }: {
    networking.useDHCP = false;
    networking.firewall.enable = false;
    networking.interfaces.eth1.ipv4.addresses = [{ address = lan; prefixLength = 24; }];
    virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
    virtualisation.memorySize = 768;
    systemd.network.wait-online.enable = false;
    environment.systemPackages = with pkgs; [ wireguard-tools iproute2 netcat-openbsd curl iputils python3 ];
    # The script reads these back to set the peer up.
    environment.etc."repose-test/wg".text = wg;
    environment.etc."repose-test/guests".text = if guests == null then "" else guests;
  };
in
{
  edge-hub-isolation = pkgs.testers.runNixOSTest {
    name = "repose-edge-hub-isolation";
    nodes.edge = { lib, pkgs, ... }: {
      imports = [ disko.nixosModules.disko edgeModule ];
      repose.edge = {
        gatewayPackage = gateway;
        # No static peers: every peer is added by the script, as wgsync
        # adds a host. The monitoring peer's address is declared, as
        # edge-01.nix declares it, so its scrape and push rules exist.
        staticPeers = [ ];
        monitoring.peerCIDRs = [ "10.255.0.3/32" ];
        operatorCIDRs = [ ];
        webhookHost = "";
      };
      disko.enableConfig = false;
      networking.hostName = lib.mkForce "edge";
      # The test driver logs in through its backdoor shell; root's
      # password comes from the console file on the real edge.
      users.users.root.hashedPassword = lib.mkForce null;
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.efi.canTouchEfiVariables = lib.mkForce false;
      networking.useDHCP = false;
      networking.interfaces.eth1.ipv4.addresses = [{ address = edgeIP; prefixLength = 24; }];
      virtualisation = {
        memorySize = 1024;
        interfaces.eth1 = { vlan = 1; assignIP = false; };
      };
      systemd.network.wait-online.enable = false;
      environment.systemPackages = with pkgs; [ wireguard-tools iproute2 netcat-openbsd iputils nftables ];
    };
    # Two hosts as the allocator would number them (10.255.0.4 and .5,
    # the monitoring peer holding .3), each with its guest /22.
    nodes.hostA = peer { lan = "203.0.113.11"; wg = "10.255.0.4"; guests = "10.64.4.0/22"; };
    nodes.hostB = peer { lan = "203.0.113.12"; wg = "10.255.0.5"; guests = "10.64.8.0/22"; };
    nodes.control = peer { lan = "203.0.113.20"; wg = "10.255.255.1"; };
    nodes.mon = peer { lan = "203.0.113.30"; wg = "10.255.0.3"; };
    testScript = ''
      import re

      peers = {"hostA": hostA, "hostB": hostB, "control": control, "mon": mon}
      start_all()
      for m in [edge] + list(peers.values()):
          m.wait_for_unit("multi-user.target")
      edge.wait_for_unit("nftables.service")

      with subtest("the edge's hub comes up once its key exists"):
          edge.succeed("mkdir -p /var/lib/repose/edge && umask 077 && wg genkey > /var/lib/repose/edge/wg.key")
          edge.succeed("systemctl start wireguard-wg0.service")
          edge.wait_until_succeeds("ip -4 addr show wg0 | grep -q '10.255.0.1/16'")
          edge_pub = edge.succeed("wg show wg0 public-key").strip()

      # Every peer makes its own key and dials the hub, as a host does
      # from behind NAT; the edge learns each peer the way wgsync adds a
      # host: its wg address and, for a host, its guest /22.
      pubs = {}
      for name, m in peers.items():
          wg = m.succeed("cat /etc/repose-test/wg").strip()
          guests = m.succeed("cat /etc/repose-test/guests").strip()
          m.succeed("umask 077 && wg genkey > /root/wg.key")
          pubs[name] = m.succeed("wg pubkey < /root/wg.key").strip()
          m.succeed("ip link add wg0 type wireguard")
          m.succeed("wg set wg0 private-key /root/wg.key")
          m.succeed(f"ip addr add {wg}/16 dev wg0 && ip link set wg0 up")
          m.succeed(f"wg set wg0 peer {edge_pub} endpoint ${edgeIP}:51820 allowed-ips 10.255.0.0/16,10.64.0.0/12 persistent-keepalive 2")
          m.succeed("ip route add 10.64.0.0/12 dev wg0")
          allowed = f"{wg}/32" + (f",{guests}" if guests else "")
          edge.succeed(f"wg set wg0 peer {pubs[name]} allowed-ips {allowed}")
          if guests:
              edge.succeed(f"ip route add {guests} dev wg0")
              # A guest's address on the host, so a packet can be sent as
              # the guest would send it: through the host's tunnel with
              # the guest's own source address.
              first = guests.split("/")[0].rsplit(".", 1)[0] + ".2"
              m.succeed(f"ip addr add {first}/32 dev lo")

      with subtest("every peer completes a handshake and pings the hub"):
          for name, m in peers.items():
              m.wait_until_succeeds("ping -c1 -W2 10.255.0.1", timeout=60)
          print(edge.succeed("wg show wg0"))

      def listen(m, addr, port):
          # A listener a `nc -z` can complete a handshake with; `-k` so one
          # probe does not end it.
          unit = "listen-" + addr.replace(".", "-") + f"-{port}"
          m.succeed(f"systemd-run --unit {unit} nc -k -l {addr} {port}")
          m.wait_until_succeeds(f"ss -ltn | grep -q '{addr}:{port} '")

      def counter(name):
          out = edge.succeed(f"nft list counter inet repose-edge {name}")
          m = re.search(r"packets (\d+)", out)
          assert m is not None, f"no packet count in: {out}"
          return int(m.group(1))

      def refused(m, target, port, ctr, src=None):
          # The probe must time out, not be refused by a closed port, so
          # the counter is read on both sides of it.
          before = counter(ctr)
          s = f"-s {src} " if src else ""
          m.fail(f"nc -z -w 3 {s}{target} {port}")
          after = counter(ctr)
          assert after > before, f"{target}:{port} was not dropped by the hub: {ctr} {before} -> {after}"

      def reached(m, target, port, src=None):
          s = f"-s {src} " if src else ""
          m.succeed(f"nc -z -w 3 {s}{target} {port}")

      # Listeners standing for the real services.
      listen(hostB, "10.255.0.5", 22)      # a host's sshd on wg0
      listen(hostB, "10.255.0.5", 9100)    # a host's node_exporter
      listen(hostB, "10.64.8.2", 22)       # a guest's sshd
      listen(control, "10.255.255.1", 8443)  # the api's gRPC
      listen(control, "10.255.255.1", 8444)  # the api's /internal, the gateway's alone
      listen(mon, "10.255.0.3", 3100)      # Loki push
      listen(mon, "10.255.0.3", 9090)      # Prometheus remote-write
      listen(edge, "10.255.0.1", 8443)     # hook ingest
      listen(edge, "10.255.0.1", 6081)     # the view relay
      listen(edge, "10.255.0.1", 9102)     # the gateway's metrics
      edge.wait_until_succeeds("ss -ltn | grep -q ':2222 '")  # the operator sshd, the module's own

      with subtest("a host reaches no other host (I-1368)"):
          refused(hostA, "10.255.0.5", 22, "host_forward_dropped")
          refused(hostA, "10.255.0.5", 9100, "host_forward_dropped")
          refused(hostA, "10.64.8.2", 22, "host_forward_dropped")
          # nor as one of its guests
          refused(hostA, "10.64.8.2", 22, "host_forward_dropped", src="10.64.4.2")
          refused(hostA, "10.255.0.5", 22, "host_forward_dropped", src="10.64.4.2")

      with subtest("a host reaches the api's gRPC at the control plane and nothing else there (I-1382)"):
          reached(hostA, "10.255.255.1", 8443)
          reached(hostB, "10.255.255.1", 8443)
          refused(hostA, "10.255.255.1", 8444, "host_forward_dropped")
          # a guest is not a host: no gRPC for it
          refused(hostA, "10.255.255.1", 8443, "host_forward_dropped", src="10.64.4.2")

      with subtest("a host pushes to Loki and nothing else on the monitoring peer; remote-write is the control plane's"):
          reached(hostA, "10.255.0.3", 3100)
          refused(hostA, "10.255.0.3", 9090, "host_forward_dropped")
          reached(control, "10.255.0.3", 9090)
          reached(control, "10.255.0.3", 3100)

      with subtest("the monitoring peer scrapes a host"):
          reached(mon, "10.255.0.5", 9100)
          # and a host does not scrape the monitoring peer back
          refused(hostA, "10.255.0.3", 9100, "host_forward_dropped")

      with subtest("a host reaches nothing on the edge but ICMP echo; a guest reaches the hook ingest"):
          hostA.succeed("ping -c1 -W2 10.255.0.1")
          refused(hostA, "10.255.0.1", 2222, "host_input_dropped")
          refused(hostA, "10.255.0.1", 6081, "host_input_dropped")
          refused(hostA, "10.255.0.1", 9102, "host_input_dropped")
          refused(hostA, "10.255.0.1", 8443, "host_input_dropped")
          reached(hostA, "10.255.0.1", 8443, src="10.64.4.2")
          refused(hostA, "10.255.0.1", 6081, "host_input_dropped", src="10.64.4.2")
          refused(hostA, "10.255.0.1", 2222, "host_input_dropped", src="10.64.4.2")

      with subtest("the peers that are not hosts keep their paths"):
          reached(control, "10.255.0.1", 6081)   # the view relay, from the control plane only
          reached(control, "10.255.0.1", 2222)   # the operator sshd from the wg network
          reached(mon, "10.255.0.1", 9102)       # the monitoring peer scrapes the gateway
          refused(mon, "10.255.0.1", 6081, "host_input_dropped")

      print(edge.succeed("nft list counters table inet repose-edge"))
    '';
  };
}
