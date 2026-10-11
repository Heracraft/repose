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
        host.succeed("systemctl is-active repose-guest-freeze.service")
        # hostd's own path for cryptsetup.
        host.succeed("systemctl show hostd -p Environment --value | tr ' ' '\\n' | grep '^PATH=' | grep -q cryptsetup")

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
