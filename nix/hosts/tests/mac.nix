# The Mac appliance's host modules, on x86 (DECISIONS I-1376, I-1380):
# the same `provider = "mac"` configuration the aarch64 image is built
# from, booted as a test VM with KVM of the builder's own architecture.
# What it proves, which the Mac day then repeats on the real thing:
#
#   the bootstrap order   The join bundle on the launcher's share becomes
#                         /run/repose/join-token and /run/repose/hub.json;
#                         wg0 comes up on the bootstrap key before any
#                         registration; `hostd register` reaches a hostdev
#                         at the control plane's hub address 10.255.255.1
#                         over that tunnel; the answer's key replaces the
#                         bootstrap key, hub.json is gone, and once the
#                         "edge" (this test, in wgsync's role) swaps the
#                         peer, hostd's stream connects.
#   the one disk          disko-layout.nix `oneDisk` formats a small
#                         image; copied onto a larger disk, repose-mac-grow
#                         gives the PV partition, the PV, the pool's
#                         metadata and the pool the rest, and does
#                         nothing the second time.
{ pkgs, nixpkgs, disko, hostModules, hostdev }:
let
  lib = pkgs.lib;
  system = pkgs.stdenv.hostPlatform.system;
  lanIP = "203.0.113.9";
  hostWG = "10.255.0.7";
  controlWG = "10.255.255.1";

  # The hub's key pair, the host's bootstrap key pair, a hostdev that
  # answers at the hub address with that edge, and the join bundle the
  # api would have printed (hostmgr.JoinBundle). Built, not evaluated:
  # nothing here is read at evaluation time.
  lab = pkgs.runCommand "repose-mac-test-lab" { nativeBuildInputs = [ pkgs.wireguard-tools pkgs.jq pkgs.coreutils hostdev ]; } ''
    mkdir -p $out/hostdev
    wg genkey > $out/edge.key
    wg pubkey < $out/edge.key > $out/edge.pub
    wg genkey > $out/host.key
    wg pubkey < $out/host.key > $out/host.pub
    hostdev --state-dir $out/hostdev init \
      --listen 0.0.0.0:8443 --names ${lanIP},${controlWG} --guest-cidr 10.64.4.0/22 \
      --edge-pubkey "$(cat $out/edge.pub)" --edge-endpoint ${lanIP}:51820 --wg-address ${hostWG}/16 > $out/hostdev/init.log
    cp $out/hostdev/ca.pem $out/ca.pem
    token=$(jq -r .join_token $out/hostdev/state.json)
    jq -cn --arg t "$token" --arg k "$(cat $out/host.key)" --arg e "$(cat $out/edge.pub)" \
      '{v: 1, token: $t, wg: {private_key: $k, address: "${hostWG}/16", edge_pubkey: $e, edge_endpoint: "${lanIP}:51820"}}' \
      | basenc --base64url -w0 | tr -d '=' > $out/join
  '';
  hostdevDir = "/var/lib/repose-hostdev";

  # The one-disk layout on a loop device, formatted inside the test; the
  # grow script then runs on the disk it is copied to.
  oneDisk = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      disko.nixosModules.disko
      {
        disko.devices = import ../disko-layout.nix {
          inherit lib;
          osDevice = "/dev/loop0";
          oneDisk = true;
          rootSize = "2G";
          poolMetadataFloor = 64 * 1024 * 1024;
        };
        disko.enableConfig = false;
      }
    ];
  };
  formatScript = (oneDisk.config.disko.devices._scripts { inherit pkgs; }).formatScript;
in
pkgs.testers.runNixOSTest {
  name = "repose-host-mac";

  nodes.api = { pkgs, ... }: {
    networking.useDHCP = false;
    networking.firewall.enable = false;
    virtualisation.interfaces.eth1 = { vlan = 1; assignIP = false; };
    networking.interfaces.eth1.ipv4.addresses = [{ address = lanIP; prefixLength = 24; }];
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
    # The hub: the control plane's address on wg0, the host peered at
    # runtime by the test (wgsync's role).
    networking.wireguard.interfaces.wg0 = {
      ips = [ "${controlWG}/16" ];
      listenPort = 51820;
      privateKeyFile = "${lab}/edge.key";
    };
    systemd.services.hostdev = {
      description = "hostdev at the hub address";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];
      serviceConfig = {
        ExecStartPre = pkgs.writeShellScript "hostdev-state" ''
          if [ ! -e ${hostdevDir}/state.json ]; then
            mkdir -p ${hostdevDir}
            cp -r ${lab}/hostdev/. ${hostdevDir}/
            chmod 0700 ${hostdevDir} && chmod 0600 ${hostdevDir}/*
          fi
        '';
        ExecStart = "${hostdev}/bin/hostdev --state-dir ${hostdevDir} serve";
        Restart = "always";
        RestartSec = 2;
      };
    };
    environment.systemPackages = [ hostdev pkgs.jq pkgs.wireguard-tools ];
  };

  nodes.host = { lib, pkgs, ... }: {
    imports = hostModules;
    repose.host = {
      hostName = "repose-mac";
      provider = "mac";
      # The test VM's NIC, not the framework's: no rename, DHCP on eth1.
      uplinkInterface = lib.mkForce "eth1";
      mac = {
        renameUplink = false;
        launcherMount = false;
        growDisk = false;
        # The test driver's backdoor shell is on hvc0.
        console = "";
      };
      dataDisk.enable = false;
      # hostdev's CA instead of the platform's; both set is an error. The
      # server name is the address's host part, the IP SAN hostdev
      # issues, not the fleet's internal name.
      apiCA = lib.mkForce "";
      apiCAFile = "${lab}/ca.pem";
      apiServerName = lib.mkForce "";
    };
    disko.enableConfig = false;
    users.users.root.hashedPassword = lib.mkForce null;
    boot.loader.systemd-boot.enable = lib.mkForce false;
    boot.loader.efi.canTouchEfiVariables = lib.mkForce false;
    virtualisation = {
      memorySize = 3072;
      cores = 2;
      writableStore = true;
      interfaces.eth1 = { vlan = 1; assignIP = false; };
      emptyDiskImages = [ 16384 ];
    };
    systemd.network.wait-online.enable = false;
    # What the launcher's share would hold at the first boot.
    systemd.tmpfiles.rules = [
      "d /run/repose/launcher 0700 root root -"
      "C /run/repose/launcher/join 0600 root root - ${lab}/join"
    ];
    environment.systemPackages = with pkgs; [ jq wireguard-tools util-linux gptfdisk e2fsprogs parted python3 ];
  };

  testScript = ''
    import json

    api.start()
    host.start()
    api.wait_for_unit("dnsmasq.service")
    api.wait_for_unit("hostdev.service")
    api.wait_for_unit("wireguard-wg0.service")
    api.wait_for_open_port(8443)
    bootstrap_pub = open("${lab}/host.pub").read().strip()
    # The edge peers the host from GET /internal/hosts within 30 s of
    # `host add`, before the machine boots: here, before it registers.
    api.succeed(f"wg set wg0 peer {bootstrap_pub} allowed-ips ${hostWG}/32")

    host.wait_for_unit("multi-user.target")

    with subtest("the join bundle becomes the token and the hub bootstrap"):
        host.wait_for_unit("repose-launcher-join.service")
        joinlog = host.succeed("journalctl -u repose-launcher-join --no-pager -o cat")
        print(joinlog)
        assert "event=join_token_written" in joinlog and "event=hub_bootstrap_written" in joinlog, joinlog
        host.succeed("test ! -e /run/repose/launcher/join")
        bundle = json.loads(host.succeed("cat ${lab}/join | tr -d '\\n' | python3 -c 'import sys,base64; s=sys.stdin.read(); print(base64.urlsafe_b64decode(s + \"=\" * (-len(s) % 4)).decode())'"))
        assert bundle["token"] not in joinlog and bundle["wg"]["private_key"] not in joinlog, "the bundle reached the journal"

    with subtest("wg0 comes up on the bootstrap key before registration, and hostd registers over it"):
        host.wait_for_unit("repose-host-net.service")
        netlog = host.succeed("journalctl -u repose-host-net --no-pager -o cat")
        print(netlog)
        assert "hub.json applied: wg0 configured for registration" in netlog, netlog
        host.wait_until_succeeds("test -s /var/lib/repose/hostd/host.json", timeout=240)
        # A handshake under the bootstrap key on the hub: the Register
        # travelled over the tunnel, since nothing else of the host's dials
        # the hub address.
        # -F: a base64 key carries `+`, which grep -E would read as a quantifier.
        api.succeed(f"wg show wg0 latest-handshakes | grep -F '{bootstrap_pub}' | awk '$2 > 0' | grep -q .")
        reg = host.succeed("journalctl -u repose-register --no-pager -o cat")
        print(reg)
        host.succeed("journalctl -u repose-register --no-pager -o cat | grep '\"event\":\"register\"' | tail -1 | jq -e '.msg == \"registered\" and .host_id' >/dev/null")
        host.succeed("test ! -e /run/repose/join-token")

    with subtest("the registration's key replaces the bootstrap key and the bootstrap material is gone"):
        hj = json.loads(host.succeed("cat /var/lib/repose/hostd/host.json"))
        assert hj["wg"]["address"] == "${hostWG}/16", hj["wg"]
        assert hj["wg"]["private_key"] != bundle["wg"]["private_key"], "host.json kept the bootstrap key"
        host.wait_until_succeeds("test ! -e /run/repose/hub.json")
        host.wait_until_succeeds("wg show wg0 public-key", timeout=60)
        new_pub = api.succeed("jq -r .host_wg_pubkey ${hostdevDir}/state.json").strip()
        assert new_pub and new_pub != bootstrap_pub, new_pub
        host.wait_until_succeeds(f"wg show wg0 public-key | grep -qxF '{new_pub}'", timeout=60)
        # A second bundle on the share, after registration, is ignored.
        host.succeed("cp ${lab}/join /run/repose/launcher/join && systemctl restart repose-launcher-join.service")
        assert "event=join_ignored reason=registered" in host.succeed("journalctl -u repose-launcher-join --no-pager -o cat")
        host.succeed("test ! -e /run/repose/launcher/join && test ! -e /run/repose/hub.json")

    with subtest("once the edge swaps the peer, hostd's stream connects over the tunnel"):
        api.succeed(f"wg set wg0 peer {new_pub} allowed-ips ${hostWG}/32,10.64.4.0/22 && wg set wg0 peer {bootstrap_pub} remove")
        api.wait_until_succeeds("hostdev --state-dir ${hostdevDir} status | grep -q 'connected: true'", timeout=120)
        print(api.succeed("hostdev --state-dir ${hostdevDir} status"))

    with subtest("the one-disk layout formats, and repose-mac-grow takes the rest of a larger disk"):
        host.succeed("truncate -s 6G /tmp/img && losetup -P /dev/loop0 /tmp/img && udevadm settle")
        host.succeed("${formatScript} >/tmp/format.log 2>&1 || { cat /tmp/format.log; exit 1; }")
        print(host.succeed("sgdisk -p /dev/loop0"))
        host.succeed("sgdisk -p /dev/loop0 | grep -q disk-os-guests")
        host.succeed("vgchange -an vg-guests && losetup -d /dev/loop0")
        host.succeed("dd if=/tmp/img of=/dev/vdb bs=4M conv=sparse && partprobe /dev/vdb && udevadm settle")
        host.succeed("vgscan && lvchange -ay vg-guests/thin")
        small = int(host.succeed("lvs --noheadings --units b --nosuffix -o lv_size vg-guests/thin").strip())
        out = host.succeed("repose-mac-grow /dev/vdb 3")
        print(out)
        assert "event=partition_grown" in out and "event=pv_grown" in out and "event=pool_grown" in out, out
        disk = int(host.succeed("blockdev --getsize64 /dev/vdb").strip())
        pv = int(host.succeed("pvs --noheadings --units b --nosuffix -o pv_size /dev/vdb3").strip())
        pool = int(host.succeed("lvs --noheadings --units b --nosuffix -o lv_size vg-guests/thin").strip())
        vg = int(host.succeed("vgs --noheadings --units b --nosuffix -o vg_size vg-guests").strip())
        meta = int(host.succeed("lvs --noheadings --units b --nosuffix -o lv_metadata_size vg-guests/thin").strip())
        assert pv > disk - 4 * 1024**3, (pv, disk)
        # The metadata rose to its 1 GiB floor, and the pool took the rest
        # less the metadata and its spare, under 95 percent of the VG.
        assert meta == 1024**3, meta
        assert pool > small and vg - 2 * meta - 256 * 1024**2 <= pool <= vg * 95 // 100, (pool, vg, meta)
        again = host.succeed("repose-mac-grow /dev/vdb 3")
        print(again)
        assert "event=partition_unchanged" in again and "event=pool_unchanged" in again, again
        assert int(host.succeed("lvs --noheadings --units b --nosuffix -o lv_size vg-guests/thin").strip()) == pool
  '';
}
