# host-wg-watchdog: the tunnel watchdog (DECISIONS I-1068) against a real
# WireGuard interface.
#
# The one-way condition is produced rather than faked. The peer's endpoint
# is 127.0.0.1 with nothing bound to it: the host's handshake initiations
# and keepalives are really sent, so `tx` climbs, nothing ever answers, so
# `rx` stays where it is and no handshake completes. That is byte-for-byte
# the state host-02 sat in for 42 minutes on 2026-11-10. A blackholed
# public address would have read the same to WireGuard but not to the
# kernel: with no route or no ARP reply the send fails and `tx` never
# moves, which is the one thing this test needs to be true.
#
# What is asserted: the timer is enabled and not merely present; a host
# with no interface publishes nothing rather than a zero that reads as a
# healthy tunnel; the first sample never acts, having nothing to compare
# against; the second rebinds the socket to a different port; and a third
# sample in the same window does not rebind again.
{ pkgs, hostNode }:
let
  promFile = "/var/lib/node_exporter/textfile/repose_wg_watchdog.prom";
in
pkgs.testers.runNixOSTest {
  name = "repose-host-wg-watchdog";
  nodes.host = { pkgs, ... }: {
    imports = [ hostNode ];
    repose.host.wgWatchdog = {
      staleAfter = 10;
      # Long on purpose: the third sample has to be refused, and a short
      # interval would let it through and pass for the wrong reason.
      minInterval = 3600;
    };
    environment.systemPackages = [ pkgs.wireguard-tools ];
  };
  testScript = ''
    host.wait_for_unit("multi-user.target")

    def metric(name):
        return host.succeed(
            "awk '$1 == \"%s\" { print $2 }' ${promFile}" % name
        ).strip()

    def port():
        return host.succeed("wg show wg0 listen-port").strip()

    with subtest("the timer is enabled, not merely present"):
        # The failure docs/CHECKLIST.md names: a unit that ships and never
        # runs. is-enabled is the assertion; systemctl --failed would not
        # show a timer that was never wanted by anything.
        host.succeed("systemctl is-enabled repose-wg-watchdog.timer")
        host.wait_for_unit("repose-wg-watchdog.timer")

    with subtest("no interface publishes nothing, not a healthy zero"):
        host.succeed("systemctl start repose-wg-watchdog.service")
        host.fail("test -e ${promFile}")

    with subtest("a tunnel that sends and hears nothing"):
        host.succeed("ip link add wg0 type wireguard")
        host.succeed("wg genkey > /run/wg.key && chmod 600 /run/wg.key")
        host.succeed("wg set wg0 private-key /run/wg.key")
        host.succeed("wg genkey | wg pubkey > /run/peer.pub")
        host.succeed(
            "wg set wg0 peer $(cat /run/peer.pub)"
            " endpoint 127.0.0.1:51821 allowed-ips 10.255.0.0/16"
            " persistent-keepalive 1"
        )
        host.succeed("ip addr add 10.255.0.9/16 dev wg0 && ip link set wg0 up")

        # First sample: no previous counters, so nothing may be concluded
        # from them and nothing may be done.
        host.succeed("systemctl start repose-wg-watchdog.service")
        assert metric("repose_host_wg_one_way") == "0", host.succeed("cat ${promFile}")
        assert metric("repose_host_wg_rebinds_total") == "0"
        before = port()

        # Past staleAfter, with the keepalive having pushed tx up and
        # nothing having come back.
        host.sleep(12)
        host.succeed("systemctl start repose-wg-watchdog.service")
        assert metric("repose_host_wg_one_way") == "1", host.succeed("cat ${promFile}")
        assert metric("repose_host_wg_rebound") == "1"
        assert metric("repose_host_wg_rebinds_total") == "1"
        after = port()
        assert before != after, f"the socket kept its port: {before} == {after}"

    with subtest("a second rebind inside minInterval is refused"):
        # The sleep is the point, not padding. Detection needs tx to have
        # moved BETWEEN two samples, so a run that lands a fraction of a
        # second after the previous one sees no new sending and concludes
        # nothing -- which is correct, and is why this assertion failed
        # the first time it was written without the wait. In production
        # the timer is 60 s apart and the peer's PersistentKeepalive is
        # 25 s, so there is always sending to see.
        host.sleep(5)
        host.succeed("systemctl start repose-wg-watchdog.service")
        # Still one-way: the rebind changed the port, not the fact that
        # nothing is listening. This is the case where the cause is not
        # the five-tuple, and the counter staying put is what tells an
        # operator the watchdog has nothing more to offer.
        assert metric("repose_host_wg_one_way") == "1"
        assert metric("repose_host_wg_rebound") == "0"
        assert metric("repose_host_wg_rebinds_total") == "1"
        assert port() == after, "the socket was rebound inside minInterval"
  '';
}
