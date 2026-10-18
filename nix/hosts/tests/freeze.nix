# host-freeze: the guest freeze's host side (DECISIONS I-652) on a real
# host configuration with the real hostd. A guest cannot boot in a host VM
# test (no third level of virtualisation), so what is checked is the part
# that lives in systemd and the daemon:
#   - hostd starts with --freeze and an RSA key file, and says when it can
#     freeze;
#   - the drop-in orders every guest@ and virtiofsd@ unit, transient and
#     already running, before repose-guest-freeze.service;
#   - a restart of the freeze unit (what a switch does) freezes nothing;
#   - `hostd freeze` and `hostd thaw` reach the daemon;
#   - at a graceful reboot systemd runs the freeze (hostd logs it with
#     reason shutdown) and stops the guest units only after it.
{ pkgs, lib, hostNode, apiNode, hostdevDir, lanIP, hostdevCA }:
pkgs.testers.runNixOSTest {
  name = "repose-host-freeze";
  nodes.api = apiNode;
  nodes.host = { pkgs, ... }: {
    imports = [ hostNode ];
    repose.host.apiAddr = "${lanIP}:8443";
    repose.host.apiCAFile = "${hostdevCA}/ca.pem";
    # The key is made at test time on a tmpfs, as staging would deliver it.
    repose.host.freeze = {
      enable = true;
      keyFile = "/run/repose-freeze/key.pem";
      deadline = 30;
    };
    environment.systemPackages = [ pkgs.openssl ];
  };
  testScript = ''
    api.start()
    host.start()
    api.wait_for_unit("hostdev.service")
    api.wait_for_open_port(8443)
    host.wait_for_unit("multi-user.target")

    def register():
        token = api.succeed("jq -r .join_token ${hostdevDir}/state.json").strip()
        host.succeed(f"install -m 0600 /dev/null /run/repose/join-token && echo {token} > /run/repose/join-token")
        host.succeed("systemctl start repose-register.service")
        host.wait_until_succeeds("hostd status | jq -e .stream_connected >/dev/null", timeout=120)

    with subtest("hostd runs with the freeze and finds its key"):
        host.succeed("install -d -m 0700 /run/repose-freeze")
        host.succeed("openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out /run/repose-freeze/key.pem")
        register()
        cmdline = host.succeed("systemctl show hostd -p ExecStart --value")
        assert "--freeze " in cmdline and "--freeze-key-file /run/repose-freeze/key.pem" in cmdline, cmdline
        assert "--free-page-reporting" in cmdline, cmdline
        # The deadline and the write rate reach hostd, and the freeze
        # unit's stop timeout moves with the deadline: systemd would
        # otherwise cut the ExecStop before the deadline it was given
        # (DECISIONS I-796). 30 + 150 = 180 s.
        assert "--freeze-deadline 30s" in cmdline, cmdline
        assert "--freeze-write-rate 100" in cmdline, cmdline
        assert "--freeze-restore-timeout 180s" in cmdline, cmdline
        stop = host.succeed(
            "systemctl show repose-guest-freeze -p TimeoutStopUSec --value").strip()
        assert "3min" in stop, stop
        host.succeed("systemctl is-active repose-guest-freeze.service")
        # hostd's own path for cryptsetup and btrfs-progs.
        path = host.succeed(
            "systemctl show hostd -p Environment --value | tr ' ' '\\n'"
            " | grep '^PATH=' | cut -d= -f2-").strip()
        assert "cryptsetup" in path and "btrfs-progs" in path, path
        host.succeed("test -d /sys/module/btrfs || modinfo btrfs")

    with subtest("an image volume holds a fraction of what is written to it"):
        # The whole point of I-685: the image is btrfs with compression
        # forced on, above dm-crypt, so a freeze writes and a restore
        # reads a fraction of the guest's memory. No guest can boot here,
        # so the filesystem itself is built the way freeze.Image builds it
        # (dm-crypt plain, mkfs.btrfs -m single, compress-force=zstd:1)
        # over a loop device, and what reaches the backing file is
        # measured.
        host.succeed("truncate -s 2G /var/tmp/frz.img")
        host.succeed("head -c 64 /dev/urandom > /run/frz.key")
        loop = host.succeed(f"PATH={path}:$PATH losetup -f --show /var/tmp/frz.img").strip()
        host.succeed(
            f"PATH={path}:$PATH cryptsetup open --type plain --cipher aes-xts-plain64"
            f" --key-size 512 --key-file /run/frz.key {loop} frztest")
        host.succeed(f"PATH={path}:$PATH mkfs.btrfs -q -f --nodiscard -m single -d single -L repose-frz /dev/mapper/frztest")
        host.succeed("mkdir -p /mnt/frz")
        host.succeed(f"PATH={path}:$PATH mount -t btrfs -o nosuid,nodev,noexec,noatime,compress-force=zstd:1 /dev/mapper/frztest /mnt/frz")
        # A guest's memory image is mostly repeated and zero pages; 512
        # MiB of it stands in for one here.
        host.succeed("head -c 536870912 /dev/zero | tr '\\0' 'a' > /mnt/frz/mem")
        host.succeed("sync -f /mnt/frz/mem")
        host.succeed(f"PATH={path}:$PATH umount /mnt/frz")
        host.succeed(f"PATH={path}:$PATH cryptsetup close frztest")
        host.succeed(f"PATH={path}:$PATH losetup -d {loop}")
        on_disk = int(host.succeed("du -B1 /var/tmp/frz.img").split()[0])
        assert on_disk < 128 * 1024 * 1024, f"512 MiB of compressible pages took {on_disk} bytes on disk"
        print(f"512 MiB written, {on_disk} bytes on disk")
        host.succeed("rm -f /var/tmp/frz.img /run/frz.key")

    with subtest("no store optimise pass replaces files under running guests"):
        # `nix-store --optimise` swaps a store file for a hard link to an
        # identical one; virtiofsd then holds a deleted inode, cannot name
        # it in the snapshot, and every guest's restore fails (host-stg-01,
        # 2026-10-12, the 03:59 nix-optimise run). New paths are still
        # deduplicated as they are added (auto-optimise-store).
        host.fail("systemctl cat nix-optimise.timer")
        host.succeed("nix config show auto-optimise-store | grep -qx true")

    with subtest("guest units, transient and running, stop only after the freeze unit"):
        host.succeed("systemd-run --unit guest@t1 --slice guests.slice sleep infinity")
        host.succeed("systemd-run --unit virtiofsd@t1 --slice guests.slice sleep infinity")
        for u in ["guest@t1", "virtiofsd@t1"]:
            before = host.succeed(f"systemctl show {u} -p Before --value")
            assert "repose-guest-freeze.service" in before, f"{u}: Before={before}"
        # A reload keeps it on a unit that was already running.
        host.succeed("systemctl daemon-reload")
        assert "repose-guest-freeze.service" in host.succeed("systemctl show guest@t1 -p Before --value")

    with subtest("a switch restarting the freeze unit freezes nothing"):
        host.succeed("systemctl restart repose-guest-freeze.service")
        host.succeed("systemctl is-active guest@t1")
        out = host.succeed("journalctl -u repose-guest-freeze --no-pager -o cat")
        assert "not shutting down" in out, out

    with subtest("hostd freeze and thaw reach the daemon"):
        host.wait_until_succeeds(
            "journalctl -u hostd --no-pager -o cat | grep -q 'image key fetched'", timeout=90)
        print(host.succeed("hostd freeze"))
        print(host.succeed("hostd thaw"))
        host.succeed(
            "journalctl -u hostd --no-pager -o cat | grep '\"event\":\"guest_freeze\"'"
            " | grep 'guests frozen' | tail -1 | jq -e '.reason == \"operator\"' >/dev/null")

    with subtest("a graceful reboot runs the freeze before any guest unit stops"):
        # A graceful shutdown through systemd, then a new boot on the same
        # disk (the driver's reboot() loses the shell across the restart).
        host.shutdown()
        host.start()
        host.wait_for_unit("multi-user.target")
        prev = host.succeed("journalctl -b -1 --no-pager -o short-monotonic")
        lines = prev.splitlines()
        def last(pattern):
            for i in range(len(lines) - 1, -1, -1):
                if pattern in lines[i]:
                    return i
            raise AssertionError(f"no line with {pattern!r} in the previous boot")
        # The last of each: the earlier subtests froze and restarted too.
        froze = last("guests frozen")
        stopped_freeze = last("repose-guest-freeze.service: Deactivated")
        stopping_guest = last("guest@t1.service: Deactivated")
        assert froze < stopped_freeze < stopping_guest, (froze, stopped_freeze, stopping_guest)
        assert '"reason":"shutdown"' in lines[froze], lines[froze]
        print("\n".join(lines[froze - 2:stopping_guest + 2]))
  '';
}
