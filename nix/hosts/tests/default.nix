# NixOS VM tests for the host configuration (01-host-nixos §7). Each test
# boots a host built from the same modules as the real one, with the
# provider bits off, eth1 as the provider NIC, and the test VM's own root
# disk instead of the disko layout. They run under `nix flake check` and
# need KVM on the builder.
#
#   host-network   the nftables tables and bridge isolation, DHCP on the
#                  provider NIC, repose-host-net from a fixture host.json,
#                  wg0, listeners on wg0 only, the egress blocks (tcp 25 and
#                  the mining-pool ports dropped and counted per guest, 465,
#                  587 and 443 open, the per-guest new-flow rate) with
#                  hostd's nft commands, the per-guest egress shape
#                  (hostd's tc commands, measured), Fluent Bit to a real Loki
#   host-storage   the disko data-disk layout on a virtual disk: PV, VG,
#                  thin pool with autoextend, a thin volume, the pool monitor
#   host-services  the real hostd, guests.slice, transient guest survives a
#                  hostd restart and kill, store export with .links masked,
#                  registration against a `hostdev` in a second node
#                  (idempotent), sshd on wg0 with a CA certificate and the
#                  PAM audit hook
{ pkgs, nixpkgs, disko, hostModules, hostdev }:
let
  lib = pkgs.lib;
  system = pkgs.stdenv.hostPlatform.system;
  fixture = ./fixtures/host.json;
  hostId = (builtins.fromJSON (builtins.readFile fixture)).host_id;
  # hostd's tc commands for a guest's egress shape (DECISIONS I-217),
  # pinned by internal/hostd/net's Go test; host-network runs them.
  shapeGolden = ../../../internal/hostd/net/testdata/reshape.golden;
  # hostd's nft commands for a guest's rules, including the per-guest
  # counters of blocked attempts (DECISIONS I-238..I-240).
  createGolden = ../../../internal/hostd/net/testdata/create.golden;

  hostNode = { lib, pkgs, ... }: {
    imports = hostModules;
    repose.host = {
      hostName = "host-test";
      provider = "none";
      uplinkInterface = "eth1";
    };
    disko.enableConfig = false;
    # The test driver sets a root password file; the host's locked password
    # would conflict with it.
    users.users.root.hashedPassword = lib.mkForce null;
    boot.loader.systemd-boot.enable = lib.mkForce false;
    boot.loader.efi.canTouchEfiVariables = lib.mkForce false;
    virtualisation = {
      memorySize = 3072;
      cores = 2;
      writableStore = true;
      interfaces.eth1 = { vlan = 1; assignIP = false; };
    };
    # No DHCP server in the storage and services tests; do not wait 2 min.
    systemd.network.wait-online.enable = false;
    environment.systemPackages = with pkgs; [ curl iputils netcat-openbsd jq python3 ];
  };

  # host-network and host-storage exercise the host's units, not the daemon,
  # and have no api to register against: the stub stands in for hostd there.
  # host-services runs the real binary against the hostdev below.
  stubNode = { lib, pkgs, ... }: {
    imports = [ hostNode ];
    repose.host.hostdPackage = lib.mkForce (pkgs.callPackage ../hostd-stub.nix { });
  };

  # The provider-NIC side of every test: DHCP for the host's uplink.
  lanNode = { ... }: {
    virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
    networking.useDHCP = false;
    networking.firewall.enable = false;
    services.dnsmasq = {
      enable = true;
      resolveLocalQueries = false;
      settings = {
        interface = "eth1";
        bind-interfaces = true;
        port = 0;
        dhcp-range = "203.0.113.100,203.0.113.150,12h";
        dhcp-option = [ "option:router,${lanIP}" ];
      };
    };
  };

  lanIP = "203.0.113.9";

  # `hostdev` (DECISIONS I-17) is the api in host-services: one host, real
  # mTLS, real join token. `init` runs at build time so the host can be
  # built with `--api-ca` naming its CA; only the certificate crosses to the
  # host node, never the CA key or the token.
  hostdevState = pkgs.runCommand "repose-hostdev-test-state" { } ''
    mkdir -p $out
    ${hostdev}/bin/hostdev --state-dir $out init \
      --listen 0.0.0.0:8443 --names ${lanIP} --guest-cidr 10.64.4.0/22 > $out/init.log
  '';
  hostdevCA = pkgs.runCommand "repose-hostdev-test-ca" { } ''
    mkdir -p $out && cp ${hostdevState}/ca.pem $out/ca.pem
  '';
  hostdevDir = "/var/lib/repose-hostdev";

  apiNode = { pkgs, ... }: {
    imports = [ lanNode ];
    networking.interfaces.eth1.ipv4.addresses = [{ address = lanIP; prefixLength = 24; }];
    systemd.services.hostdev = {
      description = "hostdev: the api for this test";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];
      serviceConfig = {
        # The state directory is writable: hostdev records the used token,
        # the host's Hello and every command in it.
        ExecStartPre = pkgs.writeShellScript "hostdev-state" ''
          if [ ! -e ${hostdevDir}/state.json ]; then
            mkdir -p ${hostdevDir}
            cp -r ${hostdevState}/. ${hostdevDir}/
            chmod 0700 ${hostdevDir} && chmod 0600 ${hostdevDir}/*
          fi
        '';
        ExecStart = "${hostdev}/bin/hostdev --state-dir ${hostdevDir} serve";
        Restart = "always";
        RestartSec = 2;
      };
    };
    environment.systemPackages = [ hostdev pkgs.jq ];
  };

  # The "internet" next to the host: DHCP server for the provider NIC, a
  # web server, an impersonated IMDS address, and a Loki for Fluent Bit.
  inetNode = { pkgs, ... }: {
    imports = [ lanNode ];
    networking.interfaces.eth1.ipv4.addresses = [
      { address = lanIP; prefixLength = 24; }
      { address = "169.254.169.254"; prefixLength = 32; }
    ];
    systemd.services.web = {
      wantedBy = [ "multi-user.target" ];
      script = "cd /tmp && exec ${pkgs.python3}/bin/python3 -m http.server 80 --bind 0.0.0.0";
    };
    services.loki = {
      enable = true;
      configFile = "${pkgs.grafana-loki.src}/cmd/loki/loki-local-config.yaml";
    };
    environment.systemPackages = with pkgs; [ grafana-loki curl netcat-openbsd iputils ];
  };

  dataOnly = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      disko.nixosModules.disko
      {
        disko.devices = import ../disko-layout.nix {
          inherit lib;
          osDevice = "/dev/null";
          dataDevice = "/dev/vdb";
          withOs = false;
        };
        disko.enableConfig = false;
      }
    ];
  };
  formatScript = (dataOnly.config.disko.devices._scripts { inherit pkgs; }).formatScript;
in
{
  host-network = pkgs.testers.runNixOSTest {
    name = "repose-host-network";
    nodes.host = {
      imports = [ stubNode ];
      # A low rate so the test crosses it in a second (I-240); the real
      # default is in nftables.nix.
      repose.host.guestEgress = { flowRate = 20; flowBurst = 20; };
    };
    nodes.inet = inetNode;
    testScript = ''
      inet.start()
      host.start()
      inet.wait_for_unit("dnsmasq.service")
      inet.wait_for_unit("web.service")
      inet.wait_for_unit("loki.service")
      inet.wait_for_open_port(3100)

      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("nftables.service")
      host.wait_for_unit("repose-host-net.service")
      host.succeed("journalctl -u repose-host-net --no-pager | grep -q 'no host.json; bridge not configured'")

      with subtest("the provider NIC gets its address by DHCP through the default-drop input chain"):
          host.wait_until_succeeds("ip -4 addr show eth1 | grep -q 'inet 203.0.113.1'", timeout=120)
          host.wait_until_succeeds("ip route | grep -q '^default via 203.0.113.9'")
          host_ip = host.succeed("ip -4 -o addr show eth1 | awk '{print $4}' | cut -d/ -f1").strip()
          host.succeed("curl -sf -m5 http://203.0.113.9/ >/dev/null")
          # the host itself may reach the metadata service (waagent needs it)
          host.succeed("curl -sf -m5 http://169.254.169.254/ >/dev/null")

      with subtest("the repose tables have the documented chains"):
          print(host.succeed("nft list table inet repose"))
          print(host.succeed("nft list table bridge repose"))
          for chain in ["input", "guest_in", "guest_fwd", "guest_dyn", "nat"]:
              host.succeed(f"nft list chain inet repose {chain}")
          host.succeed("nft list chain inet repose guest_fwd | grep -q '169.254.169.254'")
          host.succeed("nft list chain inet repose guest_fwd | grep -q '10.64.0.0/12'")

      with subtest("repose-host-net configures br-guests and wg0 from host.json"):
          host.succeed("mkdir -p /var/lib/repose/hostd && install -m 0600 ${fixture} /var/lib/repose/hostd/host.json")
          host.succeed("systemctl restart repose-host-net.service")
          host.wait_until_succeeds("ip -4 addr show br-guests | grep -q '10.64.4.1/22'")
          print(host.succeed("ip addr show br-guests"))
          host.wait_for_unit("wg-quick-wg0.service")
          print(host.succeed("wg show wg0"))
          host.succeed("wg show wg0 | grep -q 'interface: wg0'")
          host.succeed("ip -4 addr show wg0 | grep -q '10.255.0.7/16'")
          print(host.succeed("cat /run/repose/host.env"))

      # Two guests as network namespaces on the bridge, attached the way
      # hostd attaches taps (host-conventions.md "Network").
      guests = {"ga": ("10.64.4.2", "52:54:00:aa:00:02"), "gb": ("10.64.4.3", "52:54:00:aa:00:03")}
      for ns, (ip, mac) in guests.items():
          host.succeed(" && ".join([
              f"ip netns add {ns}",
              f"ip link add veth-{ns} type veth peer name tap-{ns}",
              f"ip link set tap-{ns} master br-guests up",
              f"bridge link set dev tap-{ns} isolated on learning off flood off",
              f"bridge fdb add {mac} dev tap-{ns} master static",
              f"ip link set veth-{ns} netns {ns}",
              f"ip -n {ns} link set veth-{ns} address {mac}",
              f"ip -n {ns} addr add {ip}/22 dev veth-{ns}",
              f"ip -n {ns} link set lo up",
              f"ip -n {ns} link set veth-{ns} up",
              f"ip -n {ns} route add default via 10.64.4.1",
              f"nft add element bridge repose guests {{ {mac} . {ip} . tap-{ns} }}",
          ]))
      ga = "ip netns exec ga"

      with subtest("a guest reaches the internet through NAT and nothing else"):
          host.succeed(f"{ga} curl -sf -m5 http://203.0.113.9/ >/dev/null")
          host.fail(f"{ga} curl -sf -m3 http://169.254.169.254/")
          host.succeed(f"{ga} ping -c1 -W2 10.64.4.1")
          flood = host.succeed(f"{ga} ping -q -c 40 -i 0.01 -W1 10.64.4.1 || true")
          print(flood)
          assert "40 received" not in flood, "ICMP echo to the host is not rate limited"
          host.fail(f"{ga} ping -c1 -W2 10.64.4.3")
          host.fail(f"{ga} ping -c1 -W2 10.64.8.1")
          host.fail(f"{ga} curl -sf -m3 http://10.64.4.1:9100/")
          host.fail(f"{ga} curl -sf -m3 http://{host_ip}:9100/")
          host.fail(f"{ga} nc -z -w3 10.64.4.1 22")
          host.fail(f"{ga} nc -z -w3 {host_ip} 22")

      with subtest("the host reaches a guest on 22, and only in that direction"):
          # What the runbook's manual `nft insert` used to do until this rule
          # was declared (DECISIONS I-74): an operator jumps edge -> host ->
          # guest for SSH while the gateway does not exist yet.
          # Absolute paths: a transient unit gets systemd's PATH, and
          # `ip netns exec` execs its command from that.
          host.succeed(
              "systemd-run --unit ga-listen --collect ip netns exec ga"
              " ${pkgs.bash}/bin/sh -c '${pkgs.netcat-openbsd}/bin/nc -l 22 > /tmp/ga-in'"
          )
          host.wait_until_succeeds("ip netns exec ga ss -tlnH | grep -c ':22' >/dev/null")
          host.succeed("echo host-to-guest-22 | nc -N -w5 10.64.4.2 22")
          host.wait_until_succeeds("grep -q host-to-guest-22 /tmp/ga-in")
          host.succeed("ping -c1 -W2 10.64.4.2")
          print(host.succeed("nft list chain inet repose guest_in"))
          # The rule is `ct direction reply` only: a guest's own first packet
          # is the original direction, so nothing above opened a way in.
          host.fail(f"{ga} nc -z -w3 10.64.4.1 22")
          host.fail(f"{ga} nc -z -w3 10.64.4.1 9100")

      with subtest("a guest cannot use another guest's address"):
          host.succeed("ip -n ga addr add 10.64.4.3/22 dev veth-ga")
          host.fail(f"{ga} curl -sf -m3 --interface 10.64.4.3 http://203.0.113.9/")
          host.fail(f"{ga} ping -c1 -W2 -I 10.64.4.3 10.64.4.1")
          host.succeed("ip -n ga addr del 10.64.4.3/22 dev veth-ga")

      with subtest("hostd's counters and set elements survive a ruleset reload"):
          host.succeed("nft add counter inet repose egress-ga")
          host.succeed("nft add rule inet repose guest_dyn ip saddr 10.64.4.2 counter name egress-ga")
          host.succeed(f"{ga} curl -sf -m5 http://203.0.113.9/ >/dev/null")
          before = host.succeed("nft list counter inet repose egress-ga")
          print(before)
          assert "packets 0 " not in before
          host.succeed("systemctl reload nftables.service")
          host.succeed("nft list chain inet repose guest_dyn | grep -q 'counter name \"egress-ga\"'")
          host.succeed("nft list set bridge repose guests | grep -q 'tap-ga'")
          host.succeed(f"{ga} curl -sf -m5 http://203.0.113.9/ >/dev/null")
          host.fail(f"{ga} ping -c1 -W2 10.64.4.3")
          # host -> guest is declared in the ruleset, not inserted by hand,
          # so it comes back with the reload (DECISIONS I-74).
          host.succeed("ping -c1 -W2 10.64.4.2")

      with subtest("tcp 25 and the mining-pool ports are dropped and counted per guest; submission and https are open"):
          import re, time
          # DECISIONS I-238/I-239. hostd's own commands for the guest's
          # blocked-attempt counters, from the Go golden (g1 at 10.64.4.2
          # is ga here); its egress rule is already in from the subtest above.
          cmds = [l.replace("-g1", "-ga") for l in open("${createGolden}").read().splitlines()
                  if l.startswith("nft add ") and any(f" guest_{k} " in l or f" {k}-g1" in l for k in ("smtp", "stratum", "flows"))]
          print("\n".join(cmds))
          assert len(cmds) == 6, cmds
          for c in cmds:
              host.succeed(c)
          print(host.succeed("nft list chain inet repose guest_fwd"))
          for port in (25, 465, 587, 443, 3333, 5555, 7777, 14444, 4444, 8888):
              inet.succeed(f"systemd-run --unit sink-{port} --collect ${pkgs.bash}/bin/sh -c 'while true; do ${pkgs.netcat-openbsd}/bin/nc -l {port} >/dev/null; done'")
              inet.wait_until_succeeds(f"ss -tlnH | grep -c ':{port} ' >/dev/null")
          # Each port is reachable from the host itself: the listeners are up.
          host.succeed("nc -z -w3 203.0.113.9 25")

          def packets(counter):
              out = host.succeed(f"nft list counter inet repose {counter}")
              m = re.search(r"packets (\d+)", out)
              assert m is not None, out
              return int(m.group(1))

          egress_before = packets("egress-ga")
          host.fail(f"{ga} nc -z -w3 203.0.113.9 25")
          for port in (465, 587, 443, 4444, 8888):
              host.succeed(f"{ga} nc -z -w3 203.0.113.9 {port}")
          for port in (3333, 5555, 7777, 14444):
              host.fail(f"{ga} nc -z -w2 203.0.113.9 {port}")
          smtp, stratum = packets("smtp-ga"), packets("stratum-ga")
          print(f"smtp-ga {smtp} packets, stratum-ga {stratum} packets")
          assert smtp >= 1, "the port 25 attempt was not counted for the guest"
          assert stratum >= 4, "the stratum attempts were not counted for the guest"
          # Blocked before guest_dyn: a blocked SYN is not egress. guest_dyn
          # sees a flow's first packet (established ones are accepted above
          # it), so the five open connections are five packets and the
          # dozen blocked SYNs none.
          egress = packets("egress-ga") - egress_before
          assert egress < 10, f"{egress} egress packets for five connections and the blocked attempts"
          # Another guest's counters are its own.
          host.succeed("ip netns exec gb nc -z -w3 203.0.113.9 587")
          host.fail("ip netns exec gb nc -z -w3 203.0.113.9 25")
          assert packets("smtp-ga") == smtp, "gb's attempt counted as ga's"
          # hostd's chains and counters survive a reload, like guest_dyn.
          host.succeed("systemctl reload nftables.service")
          host.succeed("nft list chain inet repose guest_smtp | grep -q 'counter name \"smtp-ga\"'")
          host.fail(f"{ga} nc -z -w3 203.0.113.9 25")
          assert packets("smtp-ga") > smtp, "the block or its counter did not survive the reload"

      with subtest("new flows over the per-guest rate are dropped and counted; under it nothing is, and established flows carry on"):
          # I-240, at the test's 20 flows/second with a burst of 20. UDP
          # datagrams from fresh sockets are one new flow each.
          flows = "import socket,sys,time\nn,gap=int(sys.argv[1]),float(sys.argv[2])\nfor _ in range(n):\n  s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.sendto(b'x',('203.0.113.9',9999)); s.close(); time.sleep(gap)\n"
          host.succeed(f"cat > /tmp/flows.py <<'EOF'\n{flows}EOF")
          # A connection opened before the flood, sending after it.
          inet.succeed("systemd-run --unit est-sink --collect ${pkgs.bash}/bin/sh -c '${pkgs.netcat-openbsd}/bin/nc -l 9001 > /tmp/est'")
          inet.wait_until_succeeds("ss -tlnH | grep -c ':9001 ' >/dev/null")
          host.succeed(
              "systemd-run --unit est-client --collect ip netns exec ga"
              " ${pkgs.bash}/bin/sh -c '(echo before; sleep 6; echo after) | ${pkgs.netcat-openbsd}/bin/nc -N -w15 203.0.113.9 9001'"
          )
          inet.wait_until_succeeds("grep -q before /tmp/est")
          time.sleep(2)  # the bucket refills from the connection's own flow
          before = packets("flows-ga")
          # 30 flows at 10 a second: under the rate, nothing dropped.
          host.succeed(f"{ga} python3 /tmp/flows.py 30 0.1")
          assert packets("flows-ga") == before, f"{packets('flows-ga') - before} flows dropped under the rate"
          # 300 at once: the burst and the rate pass, the rest are dropped.
          host.succeed(f"{ga} python3 /tmp/flows.py 300 0")
          dropped = packets("flows-ga") - before
          print(f"{dropped} of 300 new flows dropped at 20/s burst 20")
          assert 200 < dropped < 300, f"{dropped} flows dropped"
          # The established connection was never touched.
          inet.wait_until_succeeds("grep -q after /tmp/est", timeout=20)
          # gb has its own bucket: its flows pass while ga is limited.
          host.succeed("ip netns exec gb nc -z -w3 203.0.113.9 587")

      with subtest("sshd and node_exporter listen on wg0 only; nothing on the provider NIC"):
          # grep -c reads to EOF; grep -q would SIGPIPE curl under pipefail and never succeed
          host.wait_until_succeeds("curl -sf -m3 http://10.255.0.7:9100/metrics | grep -c node_exporter_build_info >/dev/null")
          host.wait_until_succeeds("ss -tlnH | grep -q '10.255.0.7:22'")
          print(host.succeed("ss -tlnpH"))
          # column 4 of `ss -tlnH` is the local address:port; everything
          # must be on the WireGuard address or loopback.
          locals_ = host.succeed("ss -tlnH | awk '{print $4}'").split()
          print(locals_)
          for addr in locals_:
              ip = addr.rsplit(":", 1)[0]
              # the caches (I-202) listen on the host services address, which
              # only br-guests can reach (next subtest)
              if addr in ("10.63.255.254:4873", "10.63.255.254:5000"):
                  continue
              assert ip == "10.255.0.7" or ip.startswith("127.") or ip.startswith("[::1]"), f"listener on {addr}"
          inet.fail(f"curl -sf -m3 http://{host_ip}:9100/")
          inet.fail(f"nc -z -w3 {host_ip} 22")
          inet.succeed(f"ping -c1 -W2 {host_ip}")

      with subtest("a guest reaches the caches on the host services address; nothing else does"):
          # I-202: two ports on 10.63.255.254 from br-guests, and nothing
          # else on it. (host-caches tests what answers there; this VM has no
          # data disk, so no cache, and no registry to fall back to.)
          host.succeed("ip -4 addr show br-guests | grep -q '10.63.255.254/32'")
          host.wait_until_succeeds("ss -Hltn | grep -q '10.63.255.254:4873'")
          host.succeed(f"{ga} nc -z -w3 10.63.255.254 4873")
          # the front answers even with nothing behind it, within its 5 s
          code = host.succeed(f"{ga} curl -s -m20 -o /dev/null -w '%{{http_code}}' http://10.63.255.254:4873/-/ping || true").strip()
          assert code not in ("", "000"), f"the npm front did not answer: {code}"
          host.fail(f"{ga} nc -z -w3 10.63.255.254 22")
          host.fail(f"{ga} nc -z -w3 10.63.255.254 9100")
          inet.fail(f"nc -z -w3 {host_ip} 4873")
          inet.fail(f"nc -z -w3 {host_ip} 5000")

      with subtest("hostd's shape limits what a guest sends out and what it receives from outside the host, never host-local traffic"):
          # I-217 and I-451. The tc commands are hostd's own, from the Go
          # golden (a tap shaped before I-217 migrated, then re-applied),
          # with both caps at 20 Mbit/s here so the VM's links are well
          # above them.
          import re, time
          cap = 20
          blocks, cur = [], []
          for line in open("${shapeGolden}").read().splitlines():
              if line.startswith("tc qdisc show"):
                  cur = []
                  blocks.append(cur)
                  continue
              if blocks and line.startswith("tc "):
                  # the burst stays hostd's 500 ms of the rate
                  line = re.sub(r"rate \d+mbit burst \d+", f"rate {cap}mbit burst {cap * 125000 // 2}",
                                line.replace("tap-0192abcd", "tap-ga"))
                  # the download class keeps hostd's burst: 10 ms, at least 128 KiB
                  dburst = max(cap * 1250, 131072)
                  cur.append(re.sub(r"rate \d+mbit ceil \d+mbit burst \d+ cburst \d+",
                                    f"rate {cap}mbit ceil {cap}mbit burst {dburst} cburst {dburst}", line))
          migrate, reapply = blocks[0], [c for c in blocks[1] if " qdisc del " not in c]
          print("\n".join(migrate + reapply))

          def mbit(cmd, mb):
              t = time.monotonic()
              host.succeed(cmd)
              r = mb * 8 * 1.048576 / (time.monotonic() - t)
              print(f"{r:.0f} Mbit/s: {cmd}")
              return r

          # one-shot listeners: nc -l exits after its one connection
          sinks = iter(range(100))

          def ga_sink(port):
              host.succeed(
                  f"systemd-run --unit ga-sink-{next(sinks)} --collect ip netns exec ga"
                  f" ${pkgs.bash}/bin/sh -c '${pkgs.netcat-openbsd}/bin/nc -l {port} > /dev/null'"
              )
              host.wait_until_succeeds(f"ip netns exec ga ss -tlnH | grep -c ':{port} ' >/dev/null")

          def inet_sink():
              inet.succeed(f"systemd-run --unit inet-sink-{next(sinks)} --collect ${pkgs.bash}/bin/sh -c '${pkgs.netcat-openbsd}/bin/nc -l 9000 > /dev/null'")
              inet.wait_until_succeeds("ss -tlnH | grep -c ':9000 ' >/dev/null")

          # the shape before I-217 capped what the host sends to a guest
          host.succeed(f"tc qdisc replace dev tap-ga root handle 1: htb default 10 && tc class replace dev tap-ga parent 1: classid 1:10 htb rate {cap}mbit ceil {cap}mbit")
          ga_sink(22)
          old = mbit("head -c 10M /dev/zero | nc -N -w10 10.64.4.2 22", 10)
          assert old < cap * 1.5, f"legacy shape: host -> guest at {old:.0f} Mbit/s"

          for c in migrate:
              host.succeed(c)
          print(host.succeed("tc qdisc show dev tap-ga; tc class show dev tap-ga; tc filter show dev tap-ga ingress"))
          host.fail("tc qdisc show dev tap-ga | grep -q 'htb 1:'")
          host.succeed("tc qdisc show dev tap-ga | grep -q '^qdisc htb 2: root'")
          # re-applying (hostd restarted) replaces, never duplicates
          for c in reapply:
              host.succeed(c)
          filters = host.succeed("tc filter show dev tap-ga ingress | grep -c '^filter .* handle 0x1'").strip()
          assert filters == "3", f"{filters} filters after a re-apply, want 3"
          filters = host.succeed("tc filter show dev tap-ga parent 2: | grep -c '^filter .* handle 0x1'").strip()
          assert filters == "2", f"{filters} download filters after a re-apply, want 2"

          # host -> guest (the gateway, the caches): not capped
          ga_sink(22)
          down = mbit("head -c 100M /dev/zero | nc -N -w10 10.64.4.2 22", 100)
          assert down > cap * 3, f"host -> guest at {down:.0f} Mbit/s"
          # guest -> the host services address: not capped
          host.succeed("systemctl stop nginx.service")
          host.succeed("systemd-run --unit svc-sink --collect ${pkgs.bash}/bin/sh -c '${pkgs.netcat-openbsd}/bin/nc -l 10.63.255.254 4873 > /dev/null'")
          host.wait_until_succeeds("ss -tlnH | grep -c '10.63.255.254:4873 ' >/dev/null")
          local = mbit(f"{ga} sh -c 'head -c 100M /dev/zero | nc -N -w10 10.63.255.254 4873'", 100)
          assert local > cap * 3, f"guest -> services address at {local:.0f} Mbit/s"
          host.succeed("systemctl start nginx.service")
          # guest -> internet: capped; an unshaped guest on the same path is not
          inet_sink()
          free = mbit("ip netns exec gb sh -c 'head -c 30M /dev/zero | nc -N -w10 203.0.113.9 9000'", 30)
          assert free > cap * 2, f"unshaped guest -> internet at {free:.0f} Mbit/s; the link is too slow to test the cap"
          inet_sink()
          out = mbit(f"{ga} sh -c 'head -c 20M /dev/zero | nc -N -w30 203.0.113.9 9000'", 20)
          assert cap * 0.6 < out < cap * 1.3, f"shaped guest -> internet at {out:.0f} Mbit/s, cap {cap}"
          # internet -> guest, a download the guest opened: capped (I-451)
          inet.succeed(f"systemd-run --unit inet-src-{next(sinks)} --collect ${pkgs.bash}/bin/sh -c 'head -c 20M /dev/zero | ${pkgs.netcat-openbsd}/bin/nc -N -l 9002'")
          inet.wait_until_succeeds("ss -tlnH | grep -c ':9002 ' >/dev/null")
          dl = mbit(f"{ga} sh -c 'nc -d -w30 203.0.113.9 9002 > /dev/null'", 20)
          assert cap * 0.6 < dl < cap * 1.3, f"internet -> shaped guest at {dl:.0f} Mbit/s, cap {cap}"
          print(host.succeed("tc -s class show dev tap-ga"))
          print(host.succeed("tc -s filter show dev tap-ga ingress"))

      with subtest("Fluent Bit ships journald and console logs to Loki with the documented labels"):
          host.wait_for_unit("fluent-bit.service")
          # Its own metrics, on wg0 only, are what the FluentBitStuck alert
          # reads (DECISIONS I-56, ops/alerts.yaml).
          host.wait_until_succeeds("curl -sf -m3 http://10.255.0.7:2021/api/v1/metrics/prometheus | grep -c fluentbit_output_retries_failed_total >/dev/null")
          inet.fail(f"curl -sf -m3 http://{host_ip}:2021/api/v1/metrics/prometheus")
          host.succeed("logger -t repose-test 'repose fluent-bit smoke line'")
          inet.wait_until_succeeds(
              "logcli query --addr http://127.0.0.1:3100 --no-labels '{host=\"${hostId}\"}' | grep -c 'repose fluent-bit smoke line' >/dev/null",
              timeout=180,
          )
          host.succeed("mkdir -p /var/lib/repose/guests/g-test && echo 'guest console smoke line' >> /var/lib/repose/guests/g-test/console.log")
          inet.wait_until_succeeds(
              "logcli query --addr http://127.0.0.1:3100 --no-labels '{component=\"console\",guest_id=\"g-test\"}' | grep -c 'guest console smoke line' >/dev/null",
              timeout=180,
          )
          print(inet.succeed("logcli query --addr http://127.0.0.1:3100 '{component=\"console\"}' --limit 3"))
    '';
  };

  # A real Cloud Hypervisor guest in a memory cgroup shaped by hostd's
  # goldens, writing gigabytes with its RAM mostly used: buffered host I/O
  # fills the unit's overhead, direct=on does not (DECISIONS I-230). The
  # check builds the script; run ./result/bin/repose-guest-memory-repro DIR
  # on a KVM machine (a VM test would nest the guest three levels deep).
  host-guest-memory = import ./guest-memory.nix { inherit pkgs; };

  host-storage = pkgs.testers.runNixOSTest {
    name = "repose-host-storage";
    nodes.host = { ... }: {
      imports = [ stubNode ];
      virtualisation.emptyDiskImages = [ 65536 ];
    };
    testScript = ''
      host.wait_for_unit("multi-user.target")
      host.succeed("test -b /dev/vdb")

      with subtest("disko creates the PV, vg-guests and the thin pool"):
          print(host.succeed("${formatScript}"))
          print(host.succeed("lsblk"))
          host.succeed("lsblk | grep -q 'vg--guests-thin'")
          lvs = host.succeed("lvs -a -o lv_name,lv_size,lv_attr,discards,zero,lv_metadata_size --units g vg-guests")
          print(lvs)
          assert "passdown" in lvs
          size_gb = float(host.succeed("lvs --noheadings --units g --nosuffix -o lv_size vg-guests/thin").strip())
          assert 58 <= size_gb <= 62, f"pool is {size_gb} GiB, expected 95 percent of 64 GiB"
          meta_gb = float(host.succeed("lvs --noheadings --units g --nosuffix -o lv_metadata_size vg-guests/thin").strip())
          assert meta_gb >= 1.0, f"pool metadata is {meta_gb} GiB, expected at least 1 GiB"

      with subtest("autoextend is configured and monitored"):
          host.succeed("lvm dumpconfig activation/thin_pool_autoextend_threshold | grep -q '=80'")
          host.succeed("lvm dumpconfig activation/thin_pool_autoextend_percent | grep -q '=10'")
          host.succeed("lvm dumpconfig activation/monitoring | grep -q '=1'")
          host.succeed("systemctl is-active lvm2-monitor.service")

      with subtest("a thin volume can be created, used and snapshotted"):
          host.succeed("lvcreate -V 1G -T vg-guests/thin -n g-test")
          host.succeed("udevadm settle")
          # I-51: guest volumes are group hostd for the unprivileged guest@ unit;
          # anything else in the VG keeps root:disk.
          perm = host.succeed("stat -L -c '%U:%G:%a' /dev/vg-guests/g-test").strip()
          print(f"g-test node {perm}")
          assert perm == "root:hostd:660", f"g-test is {perm}, want root:hostd:660"
          # The guest@ unit's DeviceAllow names the LV symlink; systemd resolves
          # it to the dm node, and DevicePolicy=closed refuses everything else.
          host.succeed("systemd-run --wait --pipe --collect -p User=hostd -p DevicePolicy=closed -p 'DeviceAllow=/dev/vg-guests/g-test rw' -- dd if=/dev/vg-guests/g-test of=/dev/null bs=4k count=1")
          host.fail("systemd-run --wait --pipe --collect -p User=hostd -p DevicePolicy=closed -- dd if=/dev/vg-guests/g-test of=/dev/null bs=4k count=1")
          host.succeed("mkfs.ext4 -q /dev/vg-guests/g-test && mkdir -p /mnt/g && mount /dev/vg-guests/g-test /mnt/g")
          host.succeed("dd if=/dev/urandom of=/mnt/g/blob bs=1M count=64 status=none && umount /mnt/g")
          host.succeed("lvcreate -s -n snap-g-test vg-guests/g-test && lvremove -f vg-guests/snap-g-test")
          # The udev rule matches g-* only: any other volume in the VG keeps root:disk.
          host.succeed("lvcreate -V 1G -T vg-guests/thin -n x-test && udevadm settle")
          other = host.succeed("stat -L -c '%U:%G:%a' /dev/vg-guests/x-test").strip()
          print(f"x-test node {other}")
          assert other == "root:disk:660", f"x-test is {other}, want root:disk:660"
          host.succeed("lvremove -f vg-guests/x-test")
          print(host.succeed("lvs vg-guests"))

      with subtest("the pool monitor exports pool usage for node_exporter"):
          host.succeed("systemctl start repose-pool-monitor.service")
          prom = host.succeed("cat /var/lib/node_exporter/textfile/repose_lvm.prom")
          print(prom)
          assert "repose_lvm_pool_present 1" in prom
          assert "repose_lvm_volumes 1" in prom
          host.succeed("systemctl list-timers --all repose-pool-monitor.timer | grep -q repose-pool-monitor")
    '';
  };

  # The npm cache and the Docker Hub mirror (nix/hosts/caches.nix, I-202):
  # the thin volume they live on, the npm cache in front of a fake
  # registry (tarball URLs rewritten to the front, the second fetch a cache
  # hit, a stale answer when the registry is down), the front's fallback
  # to the registry when the cache itself is stopped, and the mirror up.
  host-caches = pkgs.testers.runNixOSTest {
    name = "repose-host-caches";
    nodes.host = { ... }: {
      imports = [ stubNode ];
      virtualisation.emptyDiskImages = [ 65536 ];
      repose.host.caches.npm.upstream = "http://127.0.0.1:9999";
      repose.host.caches.npm.upstreamHost = "127.0.0.1:9999";
      repose.host.caches.volumeSize = "4G";
      # The mirror asks its remote for the auth challenge at start.
      repose.host.caches.docker.remote = "http://127.0.0.1:9999";
      systemd.services.fake-registry = {
        wantedBy = [ "multi-user.target" ];
        script = "exec ${pkgs.python3}/bin/python3 ${./fixtures/fake_npm_registry.py}";
      };
    };
    testScript = ''
      host.wait_for_unit("multi-user.target")
      host.succeed("${formatScript}")
      host.succeed("systemctl restart repose-cache-volume.service")
      # A test VM boots before its data disk has the VG: the caches' units
      # skipped their start (ConditionPathIsMountPoint), as on a host whose
      # disk is not set up yet, and start now.
      host.succeed("systemctl reset-failed; systemctl restart repose-npm-cache.service docker-registry.service nginx.service")

      with subtest("the caches live on their own thin volume"):
          host.succeed("mountpoint -q /var/cache/repose")
          print(host.succeed("lvs vg-guests; df -h /var/cache/repose"))
          host.succeed("lvs vg-guests/repose-cache")
          # idempotent: a second start changes nothing
          host.succeed("systemctl restart repose-cache-volume.service && mountpoint -q /var/cache/repose")

      host.wait_for_unit("fake-registry.service")
      host.wait_for_unit("repose-npm-cache.service")
      host.wait_for_unit("nginx.service")
      host.wait_for_open_port(9999)
      host.wait_until_succeeds("curl -sf -m5 http://10.63.255.254:4873/-/ping")

      with subtest("package documents point tarballs back at the front, and tarballs are cached"):
          doc = host.succeed("curl -sf -m5 http://10.63.255.254:4873/left-pad")
          print(doc)
          assert "http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz" in doc, doc
          assert "127.0.0.1:9999" not in doc, doc
          host.succeed("curl -sf -m5 -o /tmp/a.tgz http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz")
          # every registry answer sets a cookie (Cloudflare's __cf_bm);
          # the second fetch must still be a hit, and no cookie reaches
          # a guest (I-214)
          headers = host.succeed("curl -sf -m5 -D - -o /tmp/b.tgz http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz")
          assert "set-cookie" not in headers.lower(), headers
          doc2 = host.succeed("curl -sf -m5 http://10.63.255.254:4873/left-pad")
          assert doc2 == doc, doc2
          docs = host.succeed("grep -c 'GET /left-pad$' /tmp/fake-registry.log").strip()
          assert docs == "1", f"the registry saw the package document {docs} times; the second should be a cache hit"
          host.succeed("cmp /tmp/a.tgz /tmp/b.tgz")
          hits = host.succeed("grep -c 'GET /left-pad/-/left-pad-1.0.0.tgz' /tmp/fake-registry.log").strip()
          assert hits == "1", f"the registry saw the tarball {hits} times; the second should be a cache hit"
          # nginx's syslog lines reach the journal as `<host> <tag>: ...`
          host.succeed("journalctl --no-pager -u repose-npm-cache | grep -q 'repose_npm_cache: .*cache=HIT'")
          # no URL, so no package name, in the caches' log lines
          host.fail("journalctl --no-pager | grep 'repose_npm' | grep -q left-pad")

      with subtest("package documents reach a guest that asks gzipped, rewritten; tarballs are not compressed twice"):
          # I-217: the cache rewrites tarball URLs in the plain document;
          # the front compresses the result for a client that asks.
          front = "http://10.63.255.254:4873"
          for accept in ("application/json", "application/vnd.npm.install-v1+json"):
              headers = host.succeed(
                  f"curl -sf -m5 -H 'Accept-Encoding: gzip' -H 'Accept: {accept}' -D - -o /tmp/doc.gz {front}/left-pad"
              ).lower()
              print(headers)
              assert "content-encoding: gzip" in headers, headers
              assert f"content-type: {accept}" in headers, headers
              plain = host.succeed("gzip -dc /tmp/doc.gz")
              assert f"{front}/left-pad/-/left-pad-1.0.0.tgz" in plain, plain
              assert "127.0.0.1:9999" not in plain, plain
          # without Accept-Encoding: plain, still rewritten
          headers = host.succeed(f"curl -sf -m5 -D - -o /tmp/doc.json {front}/left-pad").lower()
          assert "content-encoding" not in headers, headers
          host.succeed(f"grep -q '{front}/left-pad/-/left-pad-1.0.0.tgz' /tmp/doc.json")
          # a tarball is passed as it is, even to a client that accepts gzip
          headers = host.succeed(f"curl -sf -m5 -H 'Accept-Encoding: gzip' -D - -o /tmp/c.tgz {front}/left-pad/-/left-pad-1.0.0.tgz").lower()
          assert "content-encoding" not in headers, headers
          host.succeed("cmp /tmp/a.tgz /tmp/c.tgz")

      with subtest("an authenticated request is never cached"):
          host.succeed("curl -sf -m5 -H 'Authorization: Bearer x' -o /dev/null http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz")
          host.succeed("curl -sf -m5 -H 'Authorization: Bearer x' -o /dev/null http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz")
          hits = host.succeed("grep -c 'GET /left-pad/-/left-pad-1.0.0.tgz' /tmp/fake-registry.log").strip()
          assert hits == "3", f"authenticated fetches: registry saw {hits}, want 3"

      with subtest("with the registry down, the cache serves what it has"):
          host.succeed("systemctl stop fake-registry.service")
          host.succeed("curl -sf -m10 -o /dev/null http://10.63.255.254:4873/left-pad/-/left-pad-1.0.0.tgz")
          host.succeed("systemctl start fake-registry.service")
          host.wait_for_open_port(9999)

      with subtest("with the cache stopped, the front falls back to the registry and says so"):
          host.succeed("systemctl stop repose-npm-cache.service")
          host.succeed("curl -sf -m10 -o /dev/null http://10.63.255.254:4873/left-pad")
          host.wait_until_succeeds("journalctl --no-pager | grep -q 'repose_npm_fallback: .*status=200'")
          print(host.succeed("journalctl --no-pager | grep 'repose_npm_fallback:' | tail -2"))
          host.succeed("systemctl start repose-npm-cache.service")

      with subtest("the Docker Hub mirror listens on the services address"):
          host.wait_for_unit("docker-registry.service")
          host.wait_for_open_port(5000, "10.63.255.254")
          host.succeed("test -d /var/cache/repose/docker")
          host.succeed("curl -sf -m5 http://10.63.255.254:5000/v2/ >/dev/null")
          # no OpenTelemetry export to a collector that is not there (it
          # logged "traces export ... connection refused" ~6/min)
          host.succeed("systemctl show docker-registry -p Environment | grep -q 'OTEL_TRACES_EXPORTER=none'")
    '';
  };

  host-services = pkgs.testers.runNixOSTest {
    name = "repose-host-services";
    nodes.api = apiNode;
    nodes.host = { ... }: {
      imports = [ hostNode ];
      # The real hostd (hostModules' packages.hostd) against the hostdev in
      # the api node: `hostd register` makes a real gRPC call with a real
      # join token, and the daemon holds the stream afterwards.
      repose.host.apiAddr = "${lanIP}:8443";
      repose.host.apiCAFile = "${hostdevCA}/ca.pem";
      # The bootstrap key is accepted only until the host has a Host CA
      # (I-177); the pair is a throwaway test fixture.
      repose.host.bootstrap = {
        enable = true;
        keyUntilHostCA = true;
        authorizedKeys = [ (lib.fileContents ./fixtures/bootstrap_ed25519.pub) ];
      };
    };
    testScript = ''
      api.start()
      host.start()
      api.wait_for_unit("hostdev.service")
      api.wait_for_open_port(8443)
      host.wait_for_unit("multi-user.target")

      with subtest("hostd runs, logs the documented JSON shape, and no sudo exists"):
          host.wait_for_unit("hostd.service")
          # The unit runs the real hostd (nix/flake.nix sets
          # repose.host.hostdPackage to packages.hostd), which on a host with
          # no join token waits for one. What is asserted is the log contract
          # of docs/workstreams/10-observability.md §5: every line is JSON
          # with ts, level, component and event.
          host.wait_until_succeeds(
              "journalctl -u hostd --no-pager -o cat"
              " | grep '\"component\":\"hostd\"' | tail -1"
              " | jq -e '.ts and .level and .component == \"hostd\" and .event and .msg' >/dev/null"
          )
          print(host.succeed("journalctl -u hostd --no-pager -o cat | tail -3"))
          host.fail("command -v sudo")

      with subtest("the store export is read-only with .links masked"):
          host.wait_for_unit("repose-store-export.service")
          host.succeed("mountpoint -q /run/repose/store-export")
          host.succeed("mountpoint -q /run/repose/store-export/.links")
          assert host.succeed("ls -A /run/repose/store-export/.links").strip() == ""
          assert host.succeed("ls /run/repose/store-export | wc -l").strip() != "0"
          host.fail("touch /run/repose/store-export/x")
          host.fail("touch /run/repose/store-export/.links/x")
          # The mask must not propagate onto the real store (DECISIONS I-61):
          # nix itself needs to write /nix/store/.links.
          host.fail("mountpoint -q /nix/store/.links")
          host.succeed("nix-store --optimise >/dev/null 2>&1 || true; mount -o remount,bind,rw /nix/store; touch /nix/store/.links/probe; rm /nix/store/.links/probe; mount -o remount,bind,ro /nix/store")

      with subtest("a transient guest unit survives hostd restart and kill"):
          host.succeed("systemd-run --unit guest@test --slice guests.slice sleep infinity")
          host.succeed("systemctl is-active guest@test.service")
          host.succeed("systemctl restart hostd.service")
          host.wait_for_unit("hostd.service")
          host.succeed("systemctl is-active guest@test.service")
          host.succeed("systemctl kill --signal=SIGKILL hostd.service")
          host.wait_until_succeeds("systemctl is-active hostd.service")
          host.succeed("systemctl is-active guest@test.service")
          print(host.succeed("systemctl list-units 'guest@*' --no-pager"))
          print(host.succeed("systemctl status hostd.service --no-pager"))
          mm = host.succeed("systemctl show guests.slice -p MemoryMax --value").strip()
          print(f"guests.slice MemoryMax={mm}")
          assert mm not in ("infinity", ""), "guests.slice has no memory cap"
          host.succeed("systemctl stop guest@test.service")

      with subtest("guest@ units run as hostd inside the I-51 sandbox"):
          # The property list is internal/hostd/guest/testdata/unit.golden with
          # this test's ids substituted; the Go golden pins the list itself.
          assert "kvm" in host.succeed("id -nG hostd").split(), "hostd is not in kvm"
          host.succeed("install -d -m 0711 /var/lib/repose/guests")
          host.succeed("install -d -m 1770 -g hostd /var/lib/repose/guests/h2")
          host.succeed("install -d -m 0750 -o virtiofsd -g hostd /var/lib/repose/guests/h2/virtiofsd")
          host.succeed("install -d -m 1770 -g hostd /var/lib/repose/guests/other")
          host.succeed("ip tuntap add dev tap-h2 mode tap user hostd vnet_hdr")
          host.succeed("touch /var/lib/repose/guests/other/vsock.sock && chown hostd /var/lib/repose/guests/other/vsock.sock")
          props = " ".join("-p " + p for p in [
              "MemoryMax=512M", "CPUQuota=100%", "Restart=no", "Slice=guests.slice", "User=hostd",
              "NoNewPrivileges=yes", "CapabilityBoundingSet=", "UMask=0077", "ProtectSystem=strict",
              "ProtectHome=yes", "PrivateTmp=yes", "ProtectKernelTunables=yes", "ProtectKernelModules=yes",
              "ProtectKernelLogs=yes", "ProtectControlGroups=yes", "ProtectProc=invisible",
              "RestrictNamespaces=yes", "RestrictRealtime=yes", "RestrictSUIDSGID=yes", "LockPersonality=yes",
              "SystemCallArchitectures=native", "TemporaryFileSystem=/var/lib/repose/guests",
              "BindPaths=/var/lib/repose/guests/h2", "ReadWritePaths=/var/lib/repose/guests/h2",
              "DevicePolicy=closed", "'DeviceAllow=/dev/kvm rw'", "'DeviceAllow=/dev/net/tun rw'",
              "'RestrictAddressFamilies=AF_UNIX AF_VSOCK'",
          ])
          probe = "/var/lib/repose/guests/h2/probe.py"
          host.succeed("install -m 0644 ${./h2probe.py} " + probe)
          out = host.succeed("systemd-run --wait --pipe --collect --unit guest@h2 " + props + " -- ${pkgs.python3}/bin/python3 " + probe + " 2>&1")
          print(out)
          for line in ["uid " + host.succeed("id -u hostd").strip(), "tap ok", "unix socket ok", "AF_INET refused", "vhost-vsock refused by DevicePolicy"]:
              assert line in out, f"missing {line!r} in probe output"
          ch = host.succeed("systemd-run --wait --pipe --collect --unit guest@h2ch " + props + " -- cloud-hypervisor --version 2>&1")
          print(ch)
          assert "cloud-hypervisor" in ch
          host.succeed("ip link del tap-h2 && rm -rf /var/lib/repose/guests/h2 /var/lib/repose/guests/other")

      with subtest("the nightly snapshot timer is wired to hostd snapshot-all"):
          host.succeed("systemctl list-timers --all repose-snapshot.timer | grep -q repose-snapshot")
          host.succeed("systemctl cat repose-snapshot.service | grep -q 'snapshot-all'")
          # hostd has no join token yet, so it has not opened its control
          # socket: the unit is expected to fail, with the message the
          # runbook quotes. It runs for real once the host is registered,
          # a few subtests below.
          host.fail("systemctl start repose-snapshot.service")
          host.succeed("journalctl -u repose-snapshot --no-pager | grep -q 'hostd is not running'")

      hostdev = "hostdev --state-dir ${hostdevDir}"

      with subtest("the bootstrap key works while the host has no Host CA (I-177)"):
          # Before registration: no host.json, so no Host CA, and sshd on
          # every address behind the input chain.
          host.succeed("install -m 0600 ${./fixtures/bootstrap_ed25519} /root/bootstrap")
          host.succeed("test -s /run/repose/bootstrap_authorized_keys")
          host.fail("test -s /run/repose/host_ca.pub")
          host.fail("test -s /etc/ssh/authorized_keys.d/root")
          host.wait_until_succeeds("ss -tlnH | grep -q ':22 '")
          host.succeed(
              "ssh -n -F /dev/null -i /root/bootstrap -o IdentitiesOnly=yes -o StrictHostKeyChecking=no "
              "-o UserKnownHostsFile=/dev/null -o BatchMode=yes root@127.0.0.1 true </dev/null >/dev/null 2>&1"
          )

      with subtest("registration consumes the join token once and is idempotent"):
          # The provider NIC reaches the api through the default-drop input
          # chain: the reply to a flow the host opened, nothing inbound.
          host.wait_until_succeeds("ip -4 addr show eth1 | grep -q 'inet 203.0.113'", timeout=120)
          host.wait_until_succeeds("nc -z -w3 ${lanIP} 8443")
          host.succeed("test ! -e /var/lib/repose/hostd/host.json")
          # hostd retries registration on its own every 30 s, and the token is
          # one-shot: stopped here, the unit is its only claimant, which is
          # also why the unit is ordered before hostd on a host. That a
          # running hostd takes an identity the unit wrote is DECISIONS I-76,
          # pinned by internal/hostd/app's TestEnsureIdentityTakesTheIdentity-
          # TheUnitWrote.
          host.succeed("systemctl stop hostd.service")
          # The join token hostdev printed at init; nothing else authenticates
          # a first registration (docs/interfaces/host-conventions.md).
          token = api.succeed("jq -r .join_token ${hostdevDir}/state.json").strip()
          assert token.startswith("rjt_"), f"join token {token!r}"
          host.succeed(f"install -m 0600 /dev/null /run/repose/join-token && echo {token} > /run/repose/join-token")
          host.succeed("systemctl start repose-register.service")
          for f in ["host.json", "cert.pem", "key.pem"]:
              host.succeed(f"test -s /var/lib/repose/hostd/{f}")
          host.succeed("test ! -e /run/repose/join-token")
          print(host.succeed("journalctl -u repose-register --no-pager -o cat"))
          print(host.succeed("cat /var/lib/repose/hostd/host.json"))
          host.succeed(
              "journalctl -u repose-register --no-pager -o cat | grep '\"event\":\"register\"' | tail -1"
              " | jq -e '.msg == \"registered\" and .host_id' >/dev/null"
          )
          host_id = host.succeed("jq -r .host_id /var/lib/repose/hostd/host.json").strip()
          # The api's own record of the same registration, and its token spent.
          print(api.succeed(f"{hostdev} status"))
          # grep -c reads to EOF; grep -q closes the pipe and the writer
          # dies of SIGPIPE, which pipefail reports as a failure (141).
          api.succeed(f"{hostdev} status | grep -c {host_id} >/dev/null")
          api.succeed("jq -e .token_used ${hostdevDir}/state.json >/dev/null")

          mtime = host.succeed("stat -c %Y /var/lib/repose/hostd/host.json").strip()
          host.succeed("echo second-token > /run/repose/join-token")
          host.succeed("systemctl start repose-register.service")
          assert host.succeed("systemctl show repose-register.service -p ConditionResult --value").strip() == "no"
          host.succeed("test -e /run/repose/join-token")
          assert host.succeed("stat -c %Y /var/lib/repose/hostd/host.json").strip() == mtime
          host.succeed("rm /run/repose/join-token")

      with subtest("registration brought the Host CA, so the bootstrap key is gone (I-177)"):
          # hostdev sends its SSH CA with the registration (I-139).
          host.succeed("test -s /run/repose/host_ca.pub")
          host.fail("test -s /run/repose/bootstrap_authorized_keys")
          # A key file left in root's home (host-01's install left one) is
          # not read either.
          host.succeed("install -d -m 0700 /root/.ssh && install -m 0600 ${./fixtures/bootstrap_ed25519.pub} /root/.ssh/authorized_keys")
          host.fail(
              "ssh -n -F /dev/null -i /root/bootstrap -o IdentitiesOnly=yes -o StrictHostKeyChecking=no "
              "-o UserKnownHostsFile=/dev/null -o BatchMode=yes root@127.0.0.1 true </dev/null >/dev/null 2>&1"
          )

      with subtest("the bridge comes from the registration, and hostd holds the stream"):
          # repose-register restarts the renderer, which takes the guest /22
          # out of the host.json the api wrote: .1 of hostdev's 10.64.4.0/22.
          host.wait_until_succeeds("ip -4 addr show br-guests | grep -q '10.64.4.1/22'")
          host.succeed("grep -q GUEST_CIDR=10.64.4.0/22 /run/repose/host.env")
          # hostd starts after the unit, as on a host: it loads the identity
          # the unit was issued, holds the stream with that certificate, and
          # opens the control socket.
          host.succeed("systemctl start hostd.service")
          host.wait_for_unit("hostd.service")
          api.wait_until_succeeds(f"{hostdev} status | grep -c 'connected: true' >/dev/null", timeout=120)
          print(api.succeed(f"{hostdev} status"))
          host.wait_until_succeeds("test -S /run/repose/hostd.sock")
          print(host.succeed("hostd status"))
          host.succeed("hostd status | jq -e '.host_id == \"" + host_id + "\"' >/dev/null")

      with subtest("the nightly snapshot runs once hostd is registered"):
          # The same unit that failed before registration, with no guest to
          # snapshot: the control socket answers and the timer's command works.
          host.succeed("systemctl start repose-snapshot.service")
          print(host.succeed("journalctl -u repose-snapshot --no-pager -o cat | tail -2"))
          host.succeed("journalctl -u repose-snapshot --no-pager -o cat | grep -c 'queued 0 snapshot' >/dev/null")

      with subtest("host.json is applied after registration"):
          # hostdev registers a host without WireGuard or a Host CA (I-40);
          # the real api returns both in the same host.json. The test adds
          # what the api would have sent, with a CA whose private half it
          # holds, and re-runs the renderer the way registration did.
          host.succeed("ssh-keygen -q -t ed25519 -N \"\" -f /root/ca")
          host.succeed(
              "jq --arg ca \"$(cat /root/ca.pub)\" --slurpfile f ${fixture}"
              " '.host_ca_pub = $ca | .wg = $f[0].wg | .loki_url = $f[0].loki_url'"
              " /var/lib/repose/hostd/host.json > /root/host.json"
          )
          host.succeed("install -m 0600 /root/host.json /var/lib/repose/hostd/host.json")
          host.succeed("systemctl restart repose-host-net.service")
          host.wait_for_unit("wg-quick-wg0.service")
          host.succeed("grep -q 'ListenAddress 10.255.0.7' /run/repose/sshd.conf")
          host.succeed("cmp /run/repose/host_ca.pub /root/ca.pub")
          # The Host CA arrived, so the bootstrap key is gone (I-177).
          host.fail("test -s /run/repose/bootstrap_authorized_keys")

      with subtest("root logs in over wg0 with a Host CA certificate and the login is audited"):
          host.succeed("ssh-keygen -q -t ed25519 -N \"\" -f /root/op")
          host.succeed("ssh-keygen -q -s /root/ca -I operator-test -n root -V +1h /root/op.pub")
          host.wait_until_succeeds("ss -tlnH | grep -q '10.255.0.7:22'")
          # -n and the redirects keep ssh away from the test driver's console;
          # -F /dev/null skips the system ssh_config, which the client rejects
          # in a test VM because the writable store is not root-owned.
          status, _ = host.execute(
              "ssh -n -v -F /dev/null -i /root/op -o CertificateFile=/root/op-cert.pub -o StrictHostKeyChecking=no "
              "-o UserKnownHostsFile=/dev/null -o BatchMode=yes root@10.255.0.7 true </dev/null >/root/ssh.log 2>&1"
          )
          print(host.succeed("cat /root/ssh.log"))
          if status != 0:
              print(host.succeed("journalctl -u sshd --no-pager | tail -40"))
          assert status == 0, "certificate login over wg0 failed"
          # `hostd audit-login` from the PAM hook, logging the host id it
          # registered with; the event name is 10-observability's
          # operator_login (the stub's `audit_login` was never hostd's).
          host.wait_until_succeeds("journalctl -t hostd-audit --no-pager -o cat | grep -c '\"event\":\"operator_login\"' >/dev/null")
          host.succeed(
              "journalctl -t hostd-audit --no-pager -o cat | tail -1"
              " | jq -e '.component == \"hostd\" and .host_id == \"" + host_id + "\" and .pam_type' >/dev/null"
          )
          print(host.succeed("journalctl -t hostd-audit --no-pager"))
          host.fail(
              "ssh -n -F /dev/null -i /root/op -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "
              "-o BatchMode=yes -o CertificateFile=/dev/null root@10.255.0.7 true </dev/null >/dev/null 2>&1"
          )
          # With a Host CA the bootstrap key is refused too (I-177).
          host.fail(
              "ssh -n -F /dev/null -i /root/bootstrap -o IdentitiesOnly=yes -o StrictHostKeyChecking=no "
              "-o UserKnownHostsFile=/dev/null -o BatchMode=yes root@10.255.0.7 true </dev/null >/dev/null 2>&1"
          )
    '';
  };
}
