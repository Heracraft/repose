//go:build unix

package unet

import (
	"bytes"
	"context"
	"io"
	"net/netip"
	"runtime"
	"testing"
	"time"

	hnet "github.com/heracraft/repose/internal/hostd/net"
)

const (
	gA, ipA, macA = "g-a", "10.64.4.2", "52:54:aa:00:00:02"
	gB, ipB, macB = "g-b", "10.64.4.3", "52:54:bb:00:00:03"
)

func blocked(t *testing.T, n *Net, guest, kind string) uint64 {
	t.Helper()
	m, err := n.BlockedPackets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m[guest][kind]
}

// blockedSoon is blocked once it is above zero, or after 10 s: a dropped
// SYN is counted when the switch sees it, which under the race detector on
// a slow runner can be after the guest's dial has given up (macOS CI,
// TestSMTPIsDroppedAndCounted 1 of 5, d2-int 2026-11-27).
func blockedSoon(t *testing.T, n *Net, guest, kind string) uint64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if v := blocked(t, n, guest, kind); v > 0 || time.Now().After(deadline) {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mustNotConnect: the dial must fail by timing out, as a silent drop
// does, and never reach the fake internet.
func mustNotConnect(t *testing.T, g *testGuest, in *internet, to string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	c, err := g.dial(ctx, to)
	if err == nil {
		_ = c.Close()
		t.Fatalf("dial %s connected; want it dropped", to)
	}
	if in != nil && in.hit("tcp4", to) != 0 {
		t.Fatalf("dial %s reached the internet", to)
	}
}

func TestGuestReachesTheInternetAndIsCounted(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := g.dial(ctx, "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "hello internet")
	_ = c.Close()
	if in.hit("tcp4", "203.0.113.5:443") != 1 {
		t.Fatalf("internet saw %v", in.hits)
	}
	v, err := n.CounterBytes(ctx, gA)
	if err != nil || v == 0 {
		t.Fatalf("egress counter %d, %v; want > 0", v, err)
	}
	rx, tx, err := n.TapStats("tap-aaaaaaaa")
	if err != nil || rx == 0 || tx == 0 {
		t.Fatalf("tap stats %d %d %v", rx, tx, err)
	}
}

func TestSMTPIsDroppedAndCounted(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	mustNotConnect(t, g, in, "203.0.113.5:25")
	if v := blockedSoon(t, n, gA, hnet.BlockedSMTP); v == 0 {
		t.Fatalf("smtp counter %d; want > 0", v)
	}
	if v, _ := n.CounterBytes(context.Background(), gA); v != 0 {
		t.Fatalf("a dropped flow was counted as egress: %d", v)
	}
	// 587 is not blocked.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := g.dial(ctx, "203.0.113.5:587")
	if err != nil {
		t.Fatalf("587: %v", err)
	}
	echoOnce(t, c, "ehlo")
	_ = c.Close()
}

func TestStratumPortsAreDroppedAndCounted(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	for _, p := range []string{"3333", "5555", "7777", "14433", "14444"} {
		mustNotConnect(t, g, in, "198.51.100.7:"+p)
	}
	if v := blocked(t, n, gA, hnet.BlockedStratum); v < 5 {
		t.Fatalf("stratum counter %d; want >= 5", v)
	}
}

func TestPrivateAndMetadataDestinationsAreDropped(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	for _, to := range []string{"192.168.1.10:80", "10.0.0.5:80", "172.16.0.1:443", "100.64.0.1:22", "169.254.169.254:80", "168.63.129.16:80", "10.255.255.1:8443", "10.255.0.1:22"} {
		mustNotConnect(t, g, in, to)
	}
	// The gateway itself answers nothing a guest opens.
	mustNotConnect(t, g, nil, testGW+":22")
	// Nor does the host's own loopback, or any of its addresses: the
	// forwarder dials with the host's sockets, so these would be the
	// Mac's own services.
	for _, to := range []string{"127.0.0.1:22", "127.1.2.3:9101", "0.0.0.0:22", "224.0.0.1:80", "255.255.255.255:80"} {
		mustNotConnect(t, g, in, to)
	}
}

func TestHostAddressesAreRefused(t *testing.T) {
	in := newInternet(t)
	r := DefaultRules()
	r.HostAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("203.0.113.77")} }
	n := newTestNet(t, in, &r)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	mustNotConnect(t, g, in, "203.0.113.77:22")
	u, err := g.udp("203.0.113.77:53")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	_, _ = u.Write([]byte("q"))
	_ = u.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := u.Read(make([]byte, 8)); err == nil {
		t.Fatal("udp to the host's own address was answered")
	}
	if in.hit("udp4", "203.0.113.77:53") != 0 {
		t.Fatal("udp to the host's own address was dialled")
	}
	// The real interface list includes loopback at least.
	def := DefaultRules()
	n2 := newTestNet(t, in, &def)
	if !isHost(&n2.rules, netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("loopback not refused")
	}
}

func TestGuestsCannotReachEachOther(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	a := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	b := newTestGuest(t, n, "tap-bbbbbbbb", gB, ipB, macB, testGW)
	l := b.listen(t, 22)
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			_ = c.Close()
			accepted <- struct{}{}
		}
	}()
	mustNotConnect(t, a, in, ipB+":22")
	select {
	case <-accepted:
		t.Fatal("guest b accepted a connection from guest a")
	default:
	}
	// And on another host's range.
	mustNotConnect(t, a, in, "10.64.8.2:22")
}

func TestSpoofedSourceIsDropped(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	// A guest whose kernel claims b's address on a's port.
	if err := n.AddTap(context.Background(), "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := n.AddGuestRules(context.Background(), gA, ipA, macA, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	hostEnd, guestEnd := dgramPair(t)
	n.Attach("tap-aaaaaaaa", hostEnd)
	liar := bootGuest(t, guestEnd, ipB, macA, testGW)
	mustNotConnect(t, liar, in, "203.0.113.5:443")
	if n.Drops("tap-aaaaaaaa") == 0 {
		t.Fatal("no frame refused for the spoofed source")
	}
	// And a guest with the wrong MAC.
	if err := n.AddTap(context.Background(), "tap-bbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if err := n.AddGuestRules(context.Background(), gB, ipB, macB, "tap-bbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	h2, g2 := dgramPair(t)
	n.Attach("tap-bbbbbbbb", h2)
	wrongMAC := bootGuest(t, g2, ipB, "52:54:cc:00:00:09", testGW)
	mustNotConnect(t, wrongMAC, in, "203.0.113.5:443")
	if n.Drops("tap-bbbbbbbb") == 0 {
		t.Fatal("no frame refused for the wrong MAC")
	}
}

func TestNewFlowsOverTheRateAreDroppedAndCounted(t *testing.T) {
	if raceOn && runtime.GOOS == "darwin" {
		// Its 400 ms dial is a timing bound: under -race on a 3-core
		// macOS runner no dial made it, the burst included (2 of 4 runs,
		// d2-int 2026-11-27). It runs under -race on Linux and without it
		// on a Mac.
		t.Skip("timing under the race detector on macOS")
	}
	in := newInternet(t)
	r := DefaultRules()
	r.FlowRate, r.FlowBurst = 1, 3
	n := newTestNet(t, in, &r)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	ok, failed := 0, 0
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		c, err := g.dial(ctx, "203.0.113.5:443")
		cancel()
		if err == nil {
			ok++
			_ = c.Close()
		} else {
			failed++
		}
	}
	if ok == 0 || failed == 0 {
		t.Fatalf("ok %d failed %d; want some of each", ok, failed)
	}
	if v := blockedSoon(t, n, gA, hnet.BlockedFlows); v == 0 {
		t.Fatal("flows counter 0")
	}
}

func TestConnectionCap(t *testing.T) {
	in := newInternet(t)
	r := DefaultRules()
	r.ConnCap = 2
	n := newTestNet(t, in, &r)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	ctx := context.Background()
	var open []io.Closer
	for i := 0; i < 2; i++ {
		c, err := g.dial(ctx, "203.0.113.5:443")
		if err != nil {
			t.Fatal(err)
		}
		echoOnce(t, c, "x")
		open = append(open, c)
	}
	mustNotConnect(t, g, nil, "203.0.113.5:443")
	if v := blockedSoon(t, n, gA, hnet.BlockedFlows); v == 0 {
		t.Fatal("flows counter 0 at the cap")
	}
	for _, c := range open {
		_ = c.Close()
	}
}

func TestUDPIsForwarded(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	u, err := g.udp("1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	// Three round trips. Under the race detector on a 3-core macOS CI
	// runner the first one took more than 3 s (3 of 3 runs, d2-int
	// 2026-11-27) and passed 3 of 3 there without it, and on the lab Mac.
	wait := 3 * time.Second
	if raceOn {
		wait = 30 * time.Second
	}
	_ = u.SetDeadline(time.Now().Add(wait))
	for i := 0; i < 3; i++ {
		if _, err := u.Write([]byte("query")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		k, err := u.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(buf[:k]) != "query" {
			t.Fatalf("reply %q", buf[:k])
		}
	}
	if in.hit("udp4", "1.1.1.1:53") != 1 {
		t.Fatalf("udp dials %v; want one session", in.hits)
	}
}

func TestRemovedRulesAdmitNothing(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	ctx := context.Background()
	if err := n.DelGuestRules(ctx, gA, ipA, macA, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	mustNotConnect(t, g, in, "203.0.113.5:443")
	// Counters outlive the rules until DelCounter.
	if _, err := n.CounterBytes(ctx, gA); err != nil {
		t.Fatal(err)
	}
	if err := n.DelCounter(ctx, gA); err != nil {
		t.Fatal(err)
	}
	if _, err := n.CounterBytes(ctx, gA); err == nil {
		t.Fatal("counter outlived DelCounter")
	}
	if err := n.DelTap(ctx, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := n.TapExists(ctx, "tap-aaaaaaaa"); ok {
		t.Fatal("tap outlived DelTap")
	}
}

// transfer sends size bytes from the guest to the echo server and reads
// them back, returning the time it took.
func transfer(t *testing.T, g *testGuest, size int) time.Duration {
	t.Helper()
	c, err := g.dial(context.Background(), "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	t0 := time.Now()
	go func() { _, _ = c.Write(bytes.Repeat([]byte{'a'}, size)) }()
	if _, err := io.CopyN(io.Discard, c, int64(size)); err != nil {
		t.Fatal(err)
	}
	return time.Since(t0)
}

func TestPolicerAndShaperLimitTheGuest(t *testing.T) {
	if testing.Short() || raceOn {
		t.Skip("timing")
	}
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	free := transfer(t, g, 4<<20)
	// The shaper: what the guest receives, at 20 Mbit (the echo's return
	// leg). 4 MiB is about 1.7 s at the rate, less the burst.
	if err := n.Shape(context.Background(), "tap-aaaaaaaa", 0, 20); err != nil {
		t.Fatal(err)
	}
	shaped := transfer(t, g, 4<<20)
	mbit := float64(4<<20) * 8 / shaped.Seconds() / 1e6
	t.Logf("4 MiB echo: unshaped %v, shaped down to 20 Mbit %v (%.1f Mbit/s)", free, shaped, mbit)
	if mbit > 24 || mbit < 12 {
		t.Fatalf("shaped rate %.1f Mbit/s; want about 20", mbit)
	}
	// The policer: what the guest sends beyond the burst is dropped. The
	// test guest is gVisor's TCP, which recovers from a policer's drops
	// far worse than Linux does (the lab Mac's Linux guests held 0.19-0.205
	// and 0.048-0.051 Gbit/s at 200 and 50), so only the bound and the
	// drops are asserted here, not the rate a Linux guest reaches.
	if err := n.Shape(context.Background(), "tap-aaaaaaaa", 20, 0); err != nil {
		t.Fatal(err)
	}
	c, err := g.dial(context.Background(), "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	go func() { _, _ = io.Copy(io.Discard, c) }()
	sent := 0
	buf := make([]byte, 64<<10)
	_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	for {
		k, err := c.Write(buf)
		sent += k
		if err != nil {
			break
		}
	}
	_ = c.Close()
	up := float64(sent) * 8 / time.Since(t0).Seconds() / 1e6
	t.Logf("policed at 20 Mbit: %.1f Mbit/s accepted by the guest's socket in 3 s, %d packets policed", up, n.Policed("tap-aaaaaaaa"))
	if n.Policed("tap-aaaaaaaa") == 0 {
		t.Fatal("nothing policed")
	}
	if err := n.Unshape(context.Background(), "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if again := transfer(t, g, 4<<20); again > shaped/2 {
		t.Fatalf("unshaped again %v vs shaped %v", again, shaped)
	}
}

func TestGateOrderMatchesNftables(t *testing.T) {
	r := DefaultRules()
	now := time.Now()
	g := newGate(&r, netip.MustParseAddr(testGW), now)
	cases := []struct {
		p    Packet
		v    Verdict
		kind string
	}{
		{Packet{Proto: 6, Dst: netip.MustParseAddr("192.168.1.1"), DstPort: 25, New: true}, Drop, hnet.BlockedSMTP},
		{Packet{Proto: 17, Dst: netip.MustParseAddr("8.8.8.8"), DstPort: 25, New: true}, Accept, ""},
		{Packet{Proto: 6, Dst: netip.MustParseAddr("8.8.8.8"), DstPort: 3333, New: true}, Drop, hnet.BlockedStratum},
		{Packet{Proto: 6, Dst: netip.MustParseAddr("8.8.8.8"), DstPort: 25}, Accept, ""}, // no SYN: the stack holds it or resets it, uncounted, as ct invalid is
		{Packet{Proto: 6, Dst: netip.MustParseAddr("10.255.0.1"), DstPort: 8443, New: true}, Accept, ""},
		{Packet{Proto: 6, Dst: netip.MustParseAddr("10.255.0.1"), DstPort: 22, New: true}, Drop, ""},
		{Packet{Proto: 1, Dst: netip.MustParseAddr(testGW), Echo: true}, Accept, ""},
		{Packet{Proto: 1, Dst: netip.MustParseAddr("8.8.8.8"), Echo: true, New: true}, Accept, ""}, // forwarded by pingOut (I-1668 amended)
		{Packet{Proto: 1, Dst: netip.MustParseAddr("8.8.8.8"), New: false}, Drop, ""},
	}
	for i, c := range cases {
		v, k, _ := g.Check(c.p, now)
		if v != c.v || k != c.kind {
			t.Errorf("case %d %+v: %v %q, want %v %q", i, c.p, v, k, c.v, c.kind)
		}
	}
	// Echo to the host: 5 a second.
	acc := 0
	for i := 0; i < 20; i++ {
		if v, _, _ := g.Check(Packet{Proto: 1, Dst: netip.MustParseAddr(testGW), Echo: true}, now); v == Accept {
			acc++
		}
	}
	if acc > 5 {
		t.Fatalf("host echo accepted %d in one instant; want <= 5", acc)
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func mustAddrPort(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }
