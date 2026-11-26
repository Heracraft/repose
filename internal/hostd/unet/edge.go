package unet

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// EdgeConfig is the host's place on the mesh: host.json's `wg`, or before
// registration the join bundle's hub material (register.WG and
// hostmgr.HubBootstrap carry the same four fields). PrivateKey is
// base64, as wg(8) and host.json write it; it never reaches a log line.
type EdgeConfig struct {
	PrivateKey   string
	Address      string // the host's mesh address, "10.255.x.y/32" or bare
	EdgePubkey   string
	EdgeEndpoint string // host:port of the edge's WireGuard listener
	// AllowedIPs is what the edge peer may send from and the host routes
	// to it; empty is 10.255.0.0/16, what repose-host-net renders.
	AllowedIPs []string
	// ListenPort is the local UDP port; 0 picks one.
	ListenPort int
}

// edgeMTU is wg-quick's default for an IPv4 endpoint.
const edgeMTU = 1420

// Edge is wireguard-go on the stack's edgeNIC: the tunnel to the edge
// gateway (DESIGN §7), on the same netstack as the guests, so a gateway
// connection to a guest's port 22 arrives as a stack connection that
// handleTCP splices to the guest, and hostd's own connections to the
// mesh (the api at the hub address, I-1382) are stack dials.
type Edge struct {
	n     *Net
	ep    *channel.Endpoint
	dev   *device.Device
	tun   *edgeTun
	mu    sync.Mutex
	addr  netip.Addr
	allow []netip.Prefix
}

// StartEdge brings the tunnel up. One per Net; a second call reconfigures
// the first (a rotation's new key, a registration's new address).
func (n *Net) StartEdge(cfg EdgeConfig) (*Edge, error) {
	n.mu.Lock()
	e := n.edge
	n.mu.Unlock()
	if e != nil {
		return e, e.Reconfigure(cfg)
	}
	ep := channel.New(512, edgeMTU, "")
	if err := n.s.CreateNICWithOptions(edgeNIC, ep, stack.NICOptions{Name: "wg0"}); err != nil {
		return nil, fmt.Errorf("unet: edge nic: %s", err)
	}
	_ = n.s.SetPromiscuousMode(edgeNIC, true) // the NIC exists
	_ = n.s.SetSpoofing(edgeNIC, true)        // the NIC exists
	t := newEdgeTun(ep)
	e = &Edge{n: n, ep: ep, tun: t}
	e.dev = device.NewDevice(t, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	// Up first: setting the keepalive on a device that is up sends one
	// at once, so the host starts the handshake (the edge has no
	// endpoint for it until then, and the gateway's first SSH would wait
	// for the keepalive 25 s later).
	if err := e.dev.Up(); err != nil {
		e.dev.Close()
		_ = n.s.RemoveNIC(edgeNIC) // created above
		return nil, fmt.Errorf("unet: edge up: %w", err)
	}
	if err := e.apply(cfg, true); err != nil {
		e.dev.Close()
		_ = n.s.RemoveNIC(edgeNIC) // created above
		return nil, err
	}
	n.mu.Lock()
	n.edge = e
	n.routesLocked()
	n.mu.Unlock()
	return e, nil
}

// Reconfigure swaps the key, the address or the peer in place; the
// tunnel's UDP socket and every stack connection survive a key swap.
func (e *Edge) Reconfigure(cfg EdgeConfig) error {
	if err := e.apply(cfg, false); err != nil {
		return err
	}
	e.n.mu.Lock()
	e.n.routesLocked()
	e.n.mu.Unlock()
	return nil
}

func keyHex(b64 string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(k) != 32 {
		return "", errors.New("not a 32-byte base64 key")
	}
	return hex.EncodeToString(k), nil
}

func (e *Edge) apply(cfg EdgeConfig, first bool) error {
	priv, err := keyHex(cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("unet: edge private key: %w", err) // the error names no key material
	}
	pub, err := keyHex(cfg.EdgePubkey)
	if err != nil {
		return fmt.Errorf("unet: edge public key: %w", err)
	}
	addrS := cfg.Address
	if i := strings.IndexByte(addrS, '/'); i >= 0 {
		addrS = addrS[:i]
	}
	addr, err := netip.ParseAddr(addrS)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("unet: edge address %q", cfg.Address)
	}
	ep, err := resolveEndpoint(cfg.EdgeEndpoint)
	if err != nil {
		return err
	}
	allowS := cfg.AllowedIPs
	if len(allowS) == 0 {
		allowS = []string{"10.255.0.0/16"}
	}
	var allow []netip.Prefix
	for _, a := range allowS {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return fmt.Errorf("unet: edge allowed ip %q", a)
		}
		allow = append(allow, p)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", priv)
	if first {
		fmt.Fprintf(&b, "listen_port=%d\n", cfg.ListenPort)
	}
	fmt.Fprintf(&b, "replace_peers=true\npublic_key=%s\nendpoint=%s\npersistent_keepalive_interval=25\nreplace_allowed_ips=true\n", pub, ep)
	for _, p := range allow {
		fmt.Fprintf(&b, "allowed_ip=%s\n", p)
	}
	if err := e.dev.IpcSet(b.String()); err != nil {
		return errors.New("unet: edge configuration refused") // IpcSet's error may quote the request
	}
	e.mu.Lock()
	old := e.addr
	e.addr, e.allow = addr, allow
	e.mu.Unlock()
	if old != addr {
		if old.IsValid() {
			_ = e.n.s.RemoveAddress(edgeNIC, tcpipAddr(old)) // replaced
		}
		if err := e.n.s.AddProtocolAddress(edgeNIC, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpipAddr(addr).WithPrefix()}, stack.AddressProperties{}); err != nil {
			return fmt.Errorf("unet: edge address: %s", err)
		}
	}
	return nil
}

func resolveEndpoint(s string) (string, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("unet: edge endpoint %q: %w", s, err)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(a, mustPort(port)).String(), nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip4", host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("unet: edge endpoint %q: no address", s)
	}
	return netip.AddrPortFrom(ips[0], mustPort(port)).String(), nil
}

func mustPort(s string) uint16 {
	var p uint16
	_, _ = fmt.Sscan(s, &p) // a bad port is 0 and the handshake fails visibly
	return p
}

// Addr is the host's mesh address.
func (e *Edge) Addr() netip.Addr { e.mu.Lock(); defer e.mu.Unlock(); return e.addr }

func (e *Edge) routes() []tcpip.Route {
	e.mu.Lock()
	defer e.mu.Unlock()
	var rt []tcpip.Route
	for _, p := range e.allow {
		sub, err := tcpip.NewSubnet(tcpipAddr(p.Masked().Addr()), tcpip.MaskFromBytes(net.CIDRMask(p.Bits(), 32)))
		if err == nil {
			rt = append(rt, tcpip.Route{Destination: sub, NIC: edgeNIC})
		}
	}
	return rt
}

func (e *Edge) fromEdge(a netip.Addr) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return inAny(a, e.allow)
}

// DialContext dials the mesh from the host's own address: what hostd's
// gRPC to the api (10.255.255.1:8443, I-1382) and anything else of
// hostd's on the mesh use.
func (e *Edge) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" {
		return nil, fmt.Errorf("unet: edge dial %s: only tcp", network)
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, fmt.Errorf("unet: edge dial %q: %w", address, err)
	}
	return gonet.DialTCPWithBind(ctx, e.n.s, tcpip.FullAddress{NIC: edgeNIC, Addr: tcpipAddr(e.Addr())},
		tcpip.FullAddress{NIC: edgeNIC, Addr: tcpipAddr(ap.Addr()), Port: ap.Port()}, ipv4.ProtocolNumber)
}

// dialFrom dials the mesh with a guest's address as the source.
func (e *Edge) dialFrom(ctx context.Context, src netip.Addr, dst netip.AddrPort) (net.Conn, error) {
	return gonet.DialTCPWithBind(ctx, e.n.s, tcpip.FullAddress{NIC: edgeNIC, Addr: tcpipAddr(src)},
		tcpip.FullAddress{NIC: edgeNIC, Addr: tcpipAddr(dst.Addr()), Port: dst.Port()}, ipv4.ProtocolNumber)
}

// Listen listens on the host's mesh address (hostd's metrics, 9101).
func (e *Edge) Listen(port uint16) (net.Listener, error) {
	return gonet.ListenTCP(e.n.s, tcpip.FullAddress{NIC: edgeNIC, Addr: tcpipAddr(e.Addr()), Port: port}, ipv4.ProtocolNumber)
}

// Handshake reports the seconds since the last handshake with the edge
// (0 when none yet), from wireguard-go's own status.
func (e *Edge) Handshake() (sec int64, rx, tx uint64) {
	s, err := e.dev.IpcGet()
	if err != nil {
		return 0, 0, 0
	}
	for _, l := range strings.Split(s, "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "last_handshake_time_sec":
			_, _ = fmt.Sscan(v, &sec)
		case "rx_bytes":
			_, _ = fmt.Sscan(v, &rx)
		case "tx_bytes":
			_, _ = fmt.Sscan(v, &tx)
		}
	}
	return sec, rx, tx
}

// Close takes the tunnel down.
func (e *Edge) Close() {
	e.dev.Close()
	_ = e.n.s.RemoveNIC(edgeNIC) // the NIC exists
	e.n.mu.Lock()
	if e.n.edge == e {
		e.n.edge = nil
		e.n.routesLocked()
	}
	e.n.mu.Unlock()
}

// edgeTun is wireguard-go's tun.Device over the stack's channel endpoint:
// what tun/netstack does, on a NIC of this stack instead of a stack of
// its own.
type edgeTun struct {
	ep     *channel.Endpoint
	events chan tun.Event
	in     chan *buffer.View
	done   chan struct{}
	once   sync.Once
}

func newEdgeTun(ep *channel.Endpoint) *edgeTun {
	t := &edgeTun{ep: ep, events: make(chan tun.Event, 4), in: make(chan *buffer.View, 512), done: make(chan struct{})}
	ep.AddNotify(t)
	t.events <- tun.EventUp
	return t
}

func (t *edgeTun) WriteNotify() {
	pkt := t.ep.Read()
	if pkt == nil {
		return
	}
	v := pkt.ToView()
	pkt.DecRef()
	select {
	case t.in <- v:
	case <-t.done:
	}
}

func (t *edgeTun) File() *os.File           { return nil }
func (t *edgeTun) Name() (string, error)    { return "wg0", nil }
func (t *edgeTun) MTU() (int, error)        { return edgeMTU, nil }
func (t *edgeTun) BatchSize() int           { return 1 }
func (t *edgeTun) Events() <-chan tun.Event { return t.events }

func (t *edgeTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case v := <-t.in:
		k, err := v.Read(bufs[0][offset:])
		if err != nil {
			return 0, err
		}
		sizes[0] = k
		return 1, nil
	case <-t.done:
		return 0, os.ErrClosed
	}
}

func (t *edgeTun) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		pkt := b[offset:]
		if len(pkt) == 0 {
			continue
		}
		if pkt[0]>>4 != 4 {
			continue // IPv6 is not carried (the mesh is IPv4)
		}
		pb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), pkt...))})
		t.ep.InjectInbound(header.IPv4ProtocolNumber, pb)
		pb.DecRef()
	}
	return len(bufs), nil
}

func (t *edgeTun) Close() error {
	t.once.Do(func() { close(t.done); close(t.events) })
	return nil
}
