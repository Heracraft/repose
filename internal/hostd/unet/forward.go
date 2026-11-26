package unet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// GuestSSHPort is the one port the edge may open on a guest (guest_fwd:
// `iifname "wg0" oifname "br-guests" ip daddr 10.64.0.0/12 tcp dport 22
// accept`); the gateway's SSH relay and the browser view's relay both
// arrive on it.
const GuestSSHPort = 22

// udpIdle ends a UDP session nobody has used for this long (conntrack's
// stream timeout is 120 s, its unreplied one 30 s).
const udpIdle = 60 * time.Second

type udpKey struct{ src, dst netip.AddrPort }

type udpSession struct{ last time.Time }

func (n *Net) udpKnown(k udpKey) bool {
	n.udpMu.Lock()
	defer n.udpMu.Unlock()
	_, ok := n.udp[k]
	return ok
}

func (n *Net) installForwarders() {
	tf := tcp.NewForwarder(n.s, 0, 4096, n.handleTCP)
	n.s.SetTransportProtocolHandler(tcp.ProtocolNumber, tf.HandlePacket)
	uf := udp.NewForwarder(n.s, n.handleUDP)
	n.s.SetTransportProtocolHandler(udp.ProtocolNumber, uf.HandlePacket)
}

func addrPort(a tcpip.Address, p uint16) netip.AddrPort {
	return netip.AddrPortFrom(netipAddr(a), p)
}

// handleTCP is every SYN the stack has no endpoint for. A guest's has
// passed the port's rules already; the edge's is checked here.
func (n *Net) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	src, dst := netipAddr(id.RemoteAddress), addrPort(id.LocalAddress, id.LocalPort)
	if p := n.portByIP(src); p != nil && n.rules.Guests.Contains(src) {
		n.fromGuestTCP(r, p, dst)
		return
	}
	n.mu.Lock()
	e := n.edge
	n.mu.Unlock()
	if e != nil && e.fromEdge(src) {
		n.fromEdgeTCP(r, src, dst)
		return
	}
	r.Complete(true)
}

func (n *Net) fromGuestTCP(r *tcp.ForwarderRequest, p *port, dst netip.AddrPort) {
	p.mu.Lock()
	gate := p.gate
	src := p.ip
	p.mu.Unlock()
	if gate == nil {
		r.Complete(true)
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 30*time.Second)
	defer cancel()
	var out net.Conn
	var err error
	if dst.Addr() == n.rules.Edge && dst.Port() == n.rules.EdgePort {
		n.mu.Lock()
		e := n.edge
		n.mu.Unlock()
		if e == nil {
			r.Complete(true)
			return
		}
		// Hook ingest names the guest by its source address (gateway
		// hookingest.go), so the tunnel carries the guest's own.
		out, err = e.dialFrom(ctx, src, dst)
	} else if isHost(&n.rules, dst.Addr()) {
		// The port's rules drop these already; the dial is the last place
		// a guest could reach the host's own sockets.
		r.Complete(true)
		return
	} else {
		out, err = n.dial(ctx, "tcp4", dst.String())
	}
	if err != nil {
		r.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	r.Complete(false)
	if terr != nil {
		_ = out.Close() // the guest went away during the handshake
		return
	}
	gate.ConnOpened()
	go func() {
		defer gate.ConnClosed()
		splice(gonet.NewTCPConn(&wq, ep), out)
	}()
}

// fromEdgeTCP admits the gateway's SSH to a guest and nothing else, and
// dials the guest from the edge's own address, so the guest sees the
// connection as it does through wg0 today.
func (n *Net) fromEdgeTCP(r *tcp.ForwarderRequest, src netip.Addr, dst netip.AddrPort) {
	p := n.portByIP(dst.Addr())
	if p == nil || dst.Port() != GuestSSHPort || !n.rules.Guests.Contains(dst.Addr()) {
		r.Complete(true)
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
	defer cancel()
	out, err := gonet.DialTCPWithBind(ctx, n.s,
		tcpip.FullAddress{NIC: p.nic, Addr: tcpipAddr(src)},
		tcpip.FullAddress{NIC: p.nic, Addr: tcpipAddr(dst.Addr()), Port: dst.Port()}, ipv4.ProtocolNumber)
	if err != nil {
		r.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	r.Complete(false)
	if terr != nil {
		_ = out.Close() // the gateway went away during the handshake
		return
	}
	go splice(gonet.NewTCPConn(&wq, ep), out)
}

type closeWriter interface{ CloseWrite() error }

// splice copies both ways, half-closing each side when the other ends.
func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src) // ends at EOF or either side's error
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite() // the peer may be gone already
		} else {
			_ = dst.Close() // no half-close: end it
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	_ = a.Close() // both directions are done
	_ = b.Close() // same
}

// handleUDP is the first datagram of every 4-tuple: a session dialled
// through macOS sockets, ended after udpIdle of silence.
func (n *Net) handleUDP(r *udp.ForwarderRequest) {
	id := r.ID()
	src := addrPort(id.RemoteAddress, id.RemotePort)
	dst := addrPort(id.LocalAddress, id.LocalPort)
	p := n.portByIP(src.Addr())
	if p == nil || !n.rules.Guests.Contains(src.Addr()) || dst.Addr() == n.rules.Edge || isHost(&n.rules, dst.Addr()) {
		return
	}
	p.mu.Lock()
	gate := p.gate
	p.mu.Unlock()
	if gate == nil {
		return
	}
	k := udpKey{src: src, dst: dst}
	n.udpMu.Lock()
	if _, ok := n.udp[k]; ok {
		n.udpMu.Unlock()
		return
	}
	s := &udpSession{last: time.Now()}
	n.udp[k] = s
	n.udpMu.Unlock()
	var wq waiter.Queue
	ep, terr := r.CreateEndpoint(&wq)
	if terr != nil {
		n.dropUDP(k)
		return
	}
	in := gonet.NewUDPConn(&wq, ep)
	out, err := n.dial(n.ctx, "udp4", net.JoinHostPort(dst.Addr().String(), strconv.Itoa(int(dst.Port()))))
	if err != nil {
		_ = in.Close() // nothing to relay to
		n.dropUDP(k)
		return
	}
	gate.ConnOpened()
	go func() {
		defer gate.ConnClosed()
		defer n.dropUDP(k)
		relayUDP(in, out, udpIdle)
	}()
}

func (n *Net) dropUDP(k udpKey) {
	n.udpMu.Lock()
	delete(n.udp, k)
	n.udpMu.Unlock()
}

// relayUDP copies datagrams both ways until idle passes with none in
// either direction.
func relayUDP(a, b net.Conn, idle time.Duration) {
	var once sync.Once
	done := func() { once.Do(func() { _ = a.Close(); _ = b.Close() }) } // ending the session
	defer done()
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	cp := func(dst, src net.Conn) {
		defer done()
		buf := make([]byte, 65536)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle)) // both conns support deadlines
			k, err := src.Read(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() && time.Since(time.Unix(0, last.Load())) < idle {
					continue // the other direction is alive
				}
				return
			}
			last.Store(time.Now().UnixNano())
			if _, err := dst.Write(buf[:k]); err != nil {
				return
			}
		}
	}
	go cp(b, a)
	cp(a, b)
}

var _ stack.LinkEndpoint = (*port)(nil)
