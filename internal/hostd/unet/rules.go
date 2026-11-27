package unet

import (
	"net/netip"
	"sync"
	"time"

	hnet "github.com/heracraft/repose/internal/hostd/net"
)

// Verdict is what the guest port does with one frame a guest sent.
type Verdict int

const (
	// Accept: into the stack, where the forwarders dial on the guest's
	// behalf.
	Accept Verdict = iota
	// Drop silently, as nftables' drop does: the guest's TCP retransmits,
	// and each retransmit is counted again (blocked.go's thresholds are
	// written in packets for this reason).
	Drop
)

// Dest classifies where an accepted packet goes, for the counters.
type Dest int

const (
	DestInternet Dest = iota // counted as egress (guest_meter, I-1063)
	DestEdge                 // hook ingest over the tunnel: not egress
	DestHost                 // the gateway address itself: ICMP echo
	DestNone                 // a stray packet the stack will answer with a reset: not counted
)

// Rules are the host's guest_fwd and guest_in chains
// (images/appliance-arm64/host/etc/repose/nftables.nft and
// nix/hosts/nftables.nix), in their order, as checks on each packet a guest
// sends. Each field is today's value; DefaultRules returns them.
type Rules struct {
	// SMTPPorts: tcp 25 (DECISIONS I-238).
	SMTPPorts []uint16
	// StratumPorts: a mining pool's ports (I-239).
	StratumPorts []uint16
	// ConnCap: a guest's tracked connections (I-453).
	ConnCap int
	// FlowRate and FlowBurst: new flows a second per guest (I-240),
	// `limit rate over 200/second burst 2000 packets`.
	FlowRate  float64
	FlowBurst float64
	// Metadata: cloud instance metadata and the Azure wire server.
	Metadata []netip.Prefix
	// Guests: every guest range, on this host or any other.
	Guests netip.Prefix
	// Edge and EdgePort: hook ingest, the one place on the mesh a guest
	// may open a connection to.
	Edge     netip.Addr
	EdgePort uint16
	// Private: the owner's LAN, the mesh, link-local.
	Private []netip.Prefix
	// HostEchoRate: ICMP echo to the gateway address, 5/second (guest_in).
	HostEchoRate float64
	// Martian: addresses no packet is forwarded to (the Linux kernel
	// never routes a guest's packet to them): this host, unspecified,
	// multicast, reserved, broadcast. On macOS the forwarder dials with
	// the host's own sockets, so 127.0.0.1 would be the Mac's own
	// loopback; these and HostAddrs are the guest_in drop (I-1668).
	Martian []netip.Prefix
	// HostAddrs returns the host's own interface addresses, which a guest
	// may not reach (guest_in: nothing a guest opens reaches the host).
	// nil: none beyond Martian.
	HostAddrs func() []netip.Addr
	// ShapeExempt: destinations the upload policer does not police and
	// sources the download shaper does not shape (hnet.ShapeExempt).
	ShapeExempt []netip.Prefix
}

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// DefaultRules are the values nftables.nft holds today.
func DefaultRules() Rules {
	return Rules{
		SMTPPorts:    []uint16{25},
		StratumPorts: []uint16{3333, 5555, 7777, 14433, 14444},
		ConnCap:      16384,
		FlowRate:     200,
		FlowBurst:    2000,
		Metadata:     mustPrefixes("169.254.169.254/32", "168.63.129.16/32"),
		Guests:       netip.MustParsePrefix("10.64.0.0/12"),
		Edge:         netip.MustParseAddr("10.255.0.1"),
		EdgePort:     8443,
		Private:      mustPrefixes("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"),
		HostEchoRate: 5,
		Martian:      mustPrefixes("0.0.0.0/8", "127.0.0.0/8", "224.0.0.0/4", "240.0.0.0/4"),
		ShapeExempt:  mustPrefixes(hnet.ShapeExempt...),
	}
}

func inAny(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func hasPort(ps []uint16, p uint16) bool {
	for _, q := range ps {
		if q == p {
			return true
		}
	}
	return false
}

// Packet is what the rules read of one IPv4 packet a guest sent.
type Packet struct {
	Proto   uint8 // 6 tcp, 17 udp, 1 icmp
	Dst     netip.Addr
	DstPort uint16 // 0 for a non-first fragment or ICMP
	// New: the packet opens a flow (`ct state new`): a TCP SYN without
	// ACK, or a UDP datagram of a 4-tuple the port tracks no session for.
	New bool
	// Echo: an ICMP echo request.
	Echo bool
}

// Gate holds one guest's rule state: its new-flow bucket, its echo
// bucket and its live connection count. The zero value is not usable;
// newGate makes one.
type Gate struct {
	r     *Rules
	host  netip.Addr // the gateway address
	mu    sync.Mutex
	flows bucket
	echo  bucket
	conns int
}

func newGate(r *Rules, host netip.Addr, now time.Time) *Gate {
	return &Gate{r: r, host: host,
		flows: bucket{rate: r.FlowRate, burst: r.FlowBurst, tokens: r.FlowBurst, at: now},
		echo:  bucket{rate: r.HostEchoRate, burst: r.HostEchoRate, tokens: r.HostEchoRate, at: now},
	}
}

// Check is the chain walk for one packet: the verdict, the blocked kind
// that dropped it (hnet.BlockedSMTP, BlockedStratum, BlockedFlows, or ""),
// and where an accepted packet goes.
//
// The order is guest_fwd's: smtp, stratum, the connection cap and the
// flow rate come before every destination rule, so a flow to a private
// address still spends a token, as it does under nftables.
func (g *Gate) Check(p Packet, now time.Time) (Verdict, string, Dest) {
	r := g.r
	if p.Dst == g.host {
		// guest_in: nothing a guest opens reaches the host, ICMP echo
		// excepted, at 5/second.
		if p.Proto == 1 && p.Echo {
			g.mu.Lock()
			ok := g.echo.take(now, 1)
			g.mu.Unlock()
			if ok {
				return Accept, "", DestHost
			}
		}
		return Drop, "", DestHost
	}
	// `ct state established,related accept`, first in guest_fwd: a TCP
	// segment that opens nothing belongs to a connection the stack holds
	// (one the rules admitted, or the edge's SSH to the guest), or to none,
	// and then the stack resets it, as conntrack's `ct state invalid drop`
	// ends it. Nothing reaches beyond the stack without a SYN that passed
	// the rules below.
	if p.Proto == 6 && !p.New {
		switch {
		case p.Dst == r.Edge:
			return Accept, "", DestEdge
		case inAny(p.Dst, r.Metadata), r.Guests.Contains(p.Dst), inAny(p.Dst, r.Private):
			return Accept, "", DestNone
		}
		return Accept, "", DestInternet
	}
	if g.IsHost(p.Dst) {
		return Drop, "", DestHost
	}
	if p.Proto == 6 && p.DstPort != 0 && hasPort(r.SMTPPorts, p.DstPort) {
		return Drop, hnet.BlockedSMTP, DestInternet
	}
	if p.Proto == 6 && p.DstPort != 0 && hasPort(r.StratumPorts, p.DstPort) {
		return Drop, hnet.BlockedStratum, DestInternet
	}
	if p.New {
		g.mu.Lock()
		over := g.conns >= r.ConnCap || !g.flows.take(now, 1)
		g.mu.Unlock()
		if over {
			return Drop, hnet.BlockedFlows, DestInternet
		}
	}
	if inAny(p.Dst, r.Metadata) || r.Guests.Contains(p.Dst) {
		return Drop, "", DestInternet
	}
	if p.Dst == r.Edge {
		if p.Proto == 6 && p.DstPort == r.EdgePort {
			return Accept, "", DestEdge
		}
		return Drop, "", DestEdge
	}
	if inAny(p.Dst, r.Private) {
		return Drop, "", DestInternet
	}
	// The stack answers ICMP echo for any address it is promiscuous for,
	// which would tell a guest the whole internet is up, so the port
	// forwards echo itself (pingOut) and drops the rest of ICMP.
	if p.Proto == 1 && !p.Echo {
		return Drop, "", DestInternet
	}
	return Accept, "", DestInternet
}

// IsHost reports whether a is this host's own (Martian or HostAddrs).
func (g *Gate) IsHost(a netip.Addr) bool { return isHost(g.r, a) }

func isHost(r *Rules, a netip.Addr) bool {
	if inAny(a, r.Martian) {
		return true
	}
	if r.HostAddrs != nil {
		for _, h := range r.HostAddrs() {
			if h == a {
				return true
			}
		}
	}
	return false
}

// ConnOpened and ConnClosed keep the connection count the cap reads.
func (g *Gate) ConnOpened() { g.mu.Lock(); g.conns++; g.mu.Unlock() }

// ConnClosed is ConnOpened's pair.
func (g *Gate) ConnClosed() {
	g.mu.Lock()
	if g.conns > 0 {
		g.conns--
	}
	g.mu.Unlock()
}

// Conns reads the count.
func (g *Gate) Conns() int { g.mu.Lock(); defer g.mu.Unlock(); return g.conns }

// bucket is a token bucket: rate tokens a second up to burst.
type bucket struct {
	rate, burst, tokens float64
	at                  time.Time
}

func (b *bucket) refill(now time.Time) {
	if dt := now.Sub(b.at).Seconds(); dt > 0 {
		b.tokens += dt * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.at = now
	}
}

// take spends n tokens if it has them.
func (b *bucket) take(now time.Time, n float64) bool {
	b.refill(now)
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// wait is how long until n tokens are there (0 when they are).
func (b *bucket) wait(now time.Time, n float64) time.Duration {
	b.refill(now)
	if b.tokens >= n {
		return 0
	}
	return time.Duration((n - b.tokens) / b.rate * float64(time.Second))
}

// Policer is a guest's upload policer (tc police on the tap's ingress,
// DECISIONS I-217): rate bytes a second with 500 ms of burst; what exceeds
// it is dropped. A nil *Policer polices nothing.
type Policer struct {
	mu sync.Mutex
	b  bucket
}

// NewPolicer polices to mbit with a burst of 500 ms at the rate.
func NewPolicer(mbit int, now time.Time) *Policer {
	rate := float64(mbit) * 1e6 / 8
	return &Policer{b: bucket{rate: rate, burst: rate / 2, tokens: rate / 2, at: now}}
}

// Allow spends n bytes or says the packet exceeds.
func (p *Policer) Allow(n int, now time.Time) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.b.take(now, float64(n))
}

// Shaper is a guest's download shaper (the tap's root HTB class 2:20,
// DECISIONS I-451): rate bytes a second with 10 ms of burst, at least two
// 64 KiB packets; what exceeds it waits in the queue.
type Shaper struct {
	mu sync.Mutex
	b  bucket
}

// NewShaper shapes to mbit.
func NewShaper(mbit int, now time.Time) *Shaper {
	rate := float64(mbit) * 1e6 / 8
	burst := max(rate/100, 2*65536)
	return &Shaper{b: bucket{rate: rate, burst: burst, tokens: burst, at: now}}
}

// Reserve spends n bytes and says how long the frame must wait first.
// The debt is taken at once, so frames leave in order at the rate.
func (s *Shaper) Reserve(n int, now time.Time) time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.b.wait(now, float64(n))
	s.b.tokens -= float64(n)
	return d
}
