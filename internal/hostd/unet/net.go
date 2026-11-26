// Package unet is hostd's network on a host with no Linux kernel to
// lean on (macOS, design 2, DECISIONS I-1668): a gVisor netstack in
// hostd's own process, with one NIC per guest fed by the VM's
// file-handle attachment, today's egress rules as checks on every frame a
// guest sends, its upload policer and download shaper as token buckets,
// and its counters under today's names. It implements hnet.Net, so the
// guest manager drives it exactly as it drives the Linux bridge, taps,
// nftables and tc.
//
// Nothing is forwarded at the IP layer: the stack's IP forwarding is off,
// every guest connection ends in the stack, and hostd dials again on the
// guest's behalf (the internet through macOS sockets, hook ingest through
// the edge tunnel, edge.go). Isolation between guests is therefore by
// construction: no path exists from one guest's NIC to another's, and the
// rules drop the guest range before anything is dialled.
package unet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	hnet "github.com/heracraft/repose/internal/hostd/net"
)

// Reconnect asks whatever runs a guest's VM to connect its NIC to the
// guest's socket again (the launcher's `PUT /api/v1/vm.net-reconnect` on
// darwin). A new hostd binds a new socket at the same path, and the VM's
// socket is still connected to the old one.
type Reconnect func(ctx context.Context, guestID, tap string) error

// Config is what the network needs from hostd.
type Config struct {
	// Gateway is the guests' gateway address, .1 of the host's range.
	Gateway netip.Addr
	// MTU of every guest link; 1500 unless the guest raises its own.
	MTU uint32
	// Rules; zero means DefaultRules.
	Rules *Rules
	// Dial dials the internet on a guest's behalf; nil is net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// SocketDir: each guest's NIC socket is SocketDir/<tap>.sock. Empty:
	// ports get their connection from Attach only (the tests).
	SocketDir string
	// Reconnect: nil leaves a port with no VM connected waiting for the
	// VM to connect on its own.
	Reconnect Reconnect
	Log       *slog.Logger
}

// Net is the network: one stack, a port per guest, the counters.
type Net struct {
	cfg   Config
	rules Rules
	mtu   uint32
	s     *stack.Stack
	log   *slog.Logger
	dial  func(ctx context.Context, network, addr string) (net.Conn, error)

	mu      sync.Mutex
	ports   map[string]*port // by tap
	byIP    map[netip.Addr]*port
	nextNIC tcpip.NICID
	egress  map[string]uint64            // guest -> bytes to the internet
	blocked map[string]map[string]uint64 // guest -> kind -> packets
	edge    *Edge

	udpMu sync.Mutex
	udp   map[udpKey]*udpSession

	kickc  chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// ifaceAddrs caches the host's interface addresses for five seconds: a
// new address (a DHCP renewal, a VPN coming up) is refused within that.
type ifaceAddrs struct {
	mu   sync.Mutex
	at   time.Time
	list []netip.Addr
}

func (c *ifaceAddrs) get() []netip.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < 5*time.Second {
		return c.list
	}
	c.at = time.Now()
	as, err := net.InterfaceAddrs()
	if err != nil {
		return c.list // the last list we had
	}
	c.list = c.list[:0]
	for _, a := range as {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
			c.list = append(c.list, p.Addr())
		}
	}
	return c.list
}

// edgeNIC is the tunnel's NIC; guests' NICs start after it.
const edgeNIC tcpip.NICID = 1

// New builds the stack and starts the attach loop.
func New(cfg Config) (*Net, error) {
	if cfg.Gateway.IsValid() && !cfg.Gateway.Is4() {
		return nil, errors.New("unet: gateway must be an IPv4 address")
	}
	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}
	r := DefaultRules()
	if cfg.Rules != nil {
		r = *cfg.Rules
	}
	if r.HostAddrs == nil {
		r.HostAddrs = (&ifaceAddrs{}).get
	}
	lg := cfg.Log
	if lg == nil {
		lg = slog.New(slog.DiscardHandler)
	}
	dial := cfg.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second}
		dial = d.DialContext
	}
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4},
		HandleLocal:        false,
	})
	// SACK and a large window: the stack is both ends of every guest
	// connection's first leg.
	sack := tcpip.TCPSACKEnabled(true)
	_ = s.SetTransportProtocolOption(tcp.ProtocolNumber, &sack) // a supported option; the default is the fallback
	ctx, cancel := context.WithCancel(context.Background())
	n := &Net{cfg: cfg, rules: r, mtu: cfg.MTU, s: s, log: lg, dial: dial,
		ports: map[string]*port{}, byIP: map[netip.Addr]*port{}, nextNIC: edgeNIC + 1,
		egress: map[string]uint64{}, blocked: map[string]map[string]uint64{},
		udp: map[udpKey]*udpSession{}, kickc: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	n.installForwarders()
	go n.attachLoop()
	return n, nil
}

// Close ends every port and the edge.
func (n *Net) Close() {
	n.cancel()
	n.mu.Lock()
	ps := make([]*port, 0, len(n.ports))
	for _, p := range n.ports {
		ps = append(ps, p)
	}
	e := n.edge
	n.mu.Unlock()
	for _, p := range ps {
		p.close()
	}
	if e != nil {
		e.Close()
	}
	n.s.Close()
}

// Stack is the netstack, for the edge and the tests.
func (n *Net) Stack() *stack.Stack { return n.s }

func (n *Net) kick() {
	select {
	case n.kickc <- struct{}{}:
	default:
	}
}

// attachLoop asks Reconnect, every two seconds, for each port that has
// rules and no VM connected.
func (n *Net) attachLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
		case <-n.kickc:
		}
		if n.cfg.Reconnect == nil {
			continue
		}
		n.mu.Lock()
		var want []*port
		for _, p := range n.ports {
			if !p.ready() && p.guestIDLocked() != "" {
				want = append(want, p)
			}
		}
		n.mu.Unlock()
		for _, p := range want {
			ctx, cancel := context.WithTimeout(n.ctx, 2*time.Second)
			_ = n.cfg.Reconnect(ctx, p.guestIDLocked(), p.tap) // the VM is not up yet: the next tick asks again
			cancel()
		}
	}
}

// Attach gives a guest's port its frame connection; false when the tap
// has no port.
func (n *Net) Attach(tap string, c FrameConn) bool {
	n.mu.Lock()
	p := n.ports[tap]
	n.mu.Unlock()
	if p == nil {
		return false
	}
	p.attach(c)
	return true
}

// Attached reports whether a tap's port has a frame connection.
func (n *Net) Attached(tap string) bool {
	n.mu.Lock()
	p := n.ports[tap]
	n.mu.Unlock()
	return p != nil && p.attached()
}

func (n *Net) count(guestID, kind string, v uint64) {
	if guestID == "" || kind == "policed" {
		return
	}
	n.mu.Lock()
	if m := n.blocked[guestID]; m != nil {
		m[kind] += v
	}
	n.mu.Unlock()
}

func (n *Net) addEgress(guestID string, v uint64) {
	if guestID == "" {
		return
	}
	n.mu.Lock()
	if _, ok := n.egress[guestID]; ok {
		n.egress[guestID] += v
	}
	n.mu.Unlock()
}

func (n *Net) portByIP(a netip.Addr) *port {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.byIP[a]
}

func tcpipAddr(a netip.Addr) tcpip.Address { return tcpip.AddrFrom4(a.As4()) }

func netipAddr(a tcpip.Address) netip.Addr {
	b, _ := netip.AddrFromSlice(a.AsSlice())
	return b
}

// routesLocked rebuilds the route table: each guest's /32 to its NIC,
// the mesh to the edge's.
func (n *Net) routesLocked() {
	var rt []tcpip.Route
	for a, p := range n.byIP {
		sub, _ := tcpip.NewSubnet(tcpipAddr(a), tcpip.MaskFromBytes([]byte{255, 255, 255, 255}))
		rt = append(rt, tcpip.Route{Destination: sub, NIC: p.nic})
	}
	sort.Slice(rt, func(i, j int) bool { return rt[i].NIC < rt[j].NIC })
	if n.edge != nil {
		rt = append(rt, n.edge.routes()...)
	}
	n.s.SetRouteTable(rt)
}

// ---- hnet.Net ----

var _ hnet.Net = (*Net)(nil)

// TapExists implements hnet.Net: the guest has a port.
func (n *Net) TapExists(_ context.Context, tap string) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.ports[tap]
	return ok, nil
}

// AddTap implements hnet.Net: a NIC on the stack with the gateway's
// address. Idempotent.
func (n *Net) AddTap(_ context.Context, tap string) error {
	_, err := n.ensurePort(tap)
	return err
}

// SetGateway sets the guests' gateway when New had none (on macOS it is
// known only once the host has registered and host.json names its
// range). It cannot change once a port exists.
func (n *Net) SetGateway(a netip.Addr) error {
	if !a.Is4() {
		return errors.New("unet: gateway must be an IPv4 address")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cfg.Gateway == a {
		return nil
	}
	if len(n.ports) > 0 {
		return errors.New("unet: gateway changed with guests attached")
	}
	n.cfg.Gateway = a
	return nil
}

func (n *Net) ensurePort(tap string) (*port, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p := n.ports[tap]; p != nil {
		return p, nil
	}
	if !n.cfg.Gateway.IsValid() {
		return nil, errors.New("unet: no gateway yet")
	}
	id := n.nextNIC
	n.nextNIC++
	p := newPort(n, tap, id)
	if err := n.s.CreateNICWithOptions(id, p, stack.NICOptions{Name: tap}); err != nil {
		return nil, fmt.Errorf("unet: create nic for %s: %s", tap, err)
	}
	if err := n.s.AddProtocolAddress(id, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpipAddr(n.cfg.Gateway).WithPrefix()}, stack.AddressProperties{}); err != nil {
		_ = n.s.RemoveNIC(id)
		return nil, fmt.Errorf("unet: address nic for %s: %s", tap, err)
	}
	// Promiscuous: every destination a guest names reaches the
	// forwarders. Spoofing: the stack may answer as that destination, and
	// the edge's splice may dial the guest from the edge's address.
	_ = n.s.SetPromiscuousMode(id, true) // the NIC exists
	_ = n.s.SetSpoofing(id, true)        // the NIC exists
	if n.cfg.SocketDir != "" {
		c, err := ListenDgram(filepath.Join(n.cfg.SocketDir, tap+".sock"))
		if err != nil {
			_ = n.s.RemoveNIC(id) // created above
			return nil, fmt.Errorf("unet: socket for %s: %w", tap, err)
		}
		p.attach(c)
	}
	n.ports[tap] = p
	return p, nil
}

// DelTap implements hnet.Net.
func (n *Net) DelTap(_ context.Context, tap string) error {
	n.mu.Lock()
	p := n.ports[tap]
	if p != nil {
		delete(n.ports, tap)
		for a, q := range n.byIP {
			if q == p {
				delete(n.byIP, a)
			}
		}
		n.routesLocked()
	}
	n.mu.Unlock()
	if p == nil {
		return nil
	}
	p.close()
	_ = n.s.RemoveNIC(p.nic) // the NIC exists; nothing else holds it
	return nil
}

// AddGuestRules implements hnet.Net: the port admits the guest's MAC and
// address from now on, its counters exist, and its route is in the table.
// Idempotent; a port that does not exist yet (hostd restarted under a
// running guest, reconcile's reshape) is made.
func (n *Net) AddGuestRules(_ context.Context, guestID, ip, mac, tap string) error {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return fmt.Errorf("unet: guest address %q", ip)
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return fmt.Errorf("unet: guest mac %q: %w", mac, err)
	}
	p, err := n.ensurePort(tap)
	if err != nil {
		return err
	}
	n.mu.Lock()
	gw := n.cfg.Gateway
	n.mu.Unlock()
	p.mu.Lock()
	p.guestID, p.ip, p.mac = guestID, a, tcpip.LinkAddress(hw)
	if p.gate == nil {
		p.gate = newGate(&n.rules, gw, time.Now())
	}
	p.mu.Unlock()
	_ = n.s.AddStaticNeighbor(p.nic, ipv4.ProtocolNumber, tcpipAddr(a), tcpip.LinkAddress(hw)) // the NIC exists
	n.mu.Lock()
	n.byIP[a] = p
	if _, ok := n.egress[guestID]; !ok {
		n.egress[guestID] = 0
	}
	if n.blocked[guestID] == nil {
		n.blocked[guestID] = map[string]uint64{}
		for _, k := range hnet.BlockedKinds {
			n.blocked[guestID][k] = 0
		}
	}
	n.routesLocked()
	n.mu.Unlock()
	n.kick()
	return nil
}

// DelGuestRules implements hnet.Net: the port admits nothing more; the
// counters stay until DelCounter.
func (n *Net) DelGuestRules(_ context.Context, guestID, ip, _, tap string) error {
	n.mu.Lock()
	p := n.ports[tap]
	if a, err := netip.ParseAddr(ip); err == nil && n.byIP[a] == p {
		delete(n.byIP, a)
	}
	n.routesLocked()
	n.mu.Unlock()
	if p == nil {
		return nil
	}
	p.mu.Lock()
	ip0 := p.ip
	p.gate, p.guestID = nil, ""
	p.mu.Unlock()
	if ip0.IsValid() {
		_ = n.s.RemoveNeighbor(p.nic, ipv4.ProtocolNumber, tcpipAddr(ip0)) // absent is fine
	}
	return nil
}

// Shape implements hnet.Net: the policer and the shaper, replaced in
// place.
func (n *Net) Shape(_ context.Context, tap string, upMbit, downMbit int) error {
	p, err := n.ensurePort(tap)
	if err != nil {
		return err
	}
	now := time.Now()
	p.mu.Lock()
	p.police, p.shape = nil, nil
	if upMbit > 0 {
		p.police = NewPolicer(upMbit, now)
	}
	if downMbit > 0 {
		p.shape = NewShaper(downMbit, now)
	}
	p.mu.Unlock()
	return nil
}

// Unshape implements hnet.Net.
func (n *Net) Unshape(_ context.Context, tap string) error {
	n.mu.Lock()
	p := n.ports[tap]
	n.mu.Unlock()
	if p != nil {
		p.mu.Lock()
		p.police, p.shape = nil, nil
		p.mu.Unlock()
	}
	return nil
}

// CounterBytes implements hnet.Net.
func (n *Net) CounterBytes(_ context.Context, guestID string) (uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.egress[guestID]
	if !ok {
		return 0, fmt.Errorf("unet: no counter %s", hnet.CounterName(guestID))
	}
	return v, nil
}

// EgressBytes implements hnet.Net.
func (n *Net) EgressBytes(context.Context) (map[string]uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]uint64, len(n.egress))
	for g, v := range n.egress {
		out[g] = v
	}
	return out, nil
}

// BlockedPackets implements hnet.Net.
func (n *Net) BlockedPackets(context.Context) (map[string]map[string]uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := map[string]map[string]uint64{}
	for g, m := range n.blocked {
		out[g] = map[string]uint64{}
		for k, v := range m {
			out[g][k] = v
		}
	}
	return out, nil
}

// DelCounter implements hnet.Net.
func (n *Net) DelCounter(_ context.Context, guestID string) error {
	n.mu.Lock()
	delete(n.egress, guestID)
	delete(n.blocked, guestID)
	n.mu.Unlock()
	return nil
}

// TapStats implements hnet.Net: the guest's view, frame bytes.
func (n *Net) TapStats(tap string) (uint64, uint64, error) {
	n.mu.Lock()
	p := n.ports[tap]
	n.mu.Unlock()
	if p == nil {
		return 0, 0, fmt.Errorf("unet: no port %s", tap)
	}
	return p.rx.Load(), p.tx.Load(), nil
}

// ListTaps implements hnet.Net.
func (n *Net) ListTaps(context.Context) ([]string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.ports))
	for t := range n.ports {
		if strings.HasPrefix(t, "tap-") {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FlushNeigh implements hnet.Net. Every guest has its own link and the
// stack's entries are the static ones AddGuestRules writes, so a stale
// entry from a destroyed guest cannot answer for the next one (the cause
// of I-1530 does not exist here); nothing to flush.
func (n *Net) FlushNeigh(context.Context, string) error { return nil }

// Drops is how many frames a tap's port refused before the rules
// (spoofed source, not IPv4 or ARP), for the tests and the lab.
func (n *Net) Drops(tap string) uint64 {
	n.mu.Lock()
	p := n.ports[tap]
	n.mu.Unlock()
	if p == nil {
		return 0
	}
	return p.drops.Load()
}
