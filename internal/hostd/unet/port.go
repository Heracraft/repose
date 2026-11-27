package unet

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// FrameConn carries whole Ethernet frames, one per datagram: the hostd
// end of the socketpair whose other end is the VM's
// VZFileHandleNetworkDeviceAttachment (I-1668).
type FrameConn interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Close() error
}

// port is one guest's NIC on the stack: its own link, so nothing a guest
// sends can reach another guest's link (the bridge's `isolated on` and
// `flood off`), and an admission check on every frame: the source MAC and
// address must be the guest's (the bridge's static FDB entry and its
// `guests` set), then the rules.
type port struct {
	n   *Net
	tap string
	nic tcpip.NICID

	mu      sync.Mutex
	guestID string
	ip      netip.Addr
	mac     tcpip.LinkAddress
	gate    *Gate
	police  *Policer
	shape   *Shaper
	conn    FrameConn
	dispat  stack.NetworkDispatcher
	closed  bool
	txq     *txQueue

	rx, tx  atomic.Uint64 // guest view: rx what it received, tx what it sent
	drops   atomic.Uint64 // frames refused before the rules: spoofed, not IPv4/ARP
	policed atomic.Uint64 // packets the upload policer dropped
}

// queueFrames bounds each of a port's two transmit queues: frames beyond
// it are dropped, as a full qdisc does. The shaped queue is bounded in
// bytes too (shapedQueueBytes).
const queueFrames = 16384

// shapedQueueBytes bounds what waits in the shaped queue: 100 ms at the
// rate, at least 2 MiB. A shorter queue drops the stack's segments in
// bursts and the stack's own TCP to the guest stalls on its retransmit
// timer (200 ms minimum): 2048 frames held a 1000 Mbit download to
// 564-578 Mbit/s on the lab Mac, 2026-11-26.
func shapedQueueBytes(s *Shaper) int64 {
	if s == nil {
		return 1 << 62
	}
	return max(int64(s.b.rate/10), 2<<20)
}

func newPort(n *Net, tap string, nic tcpip.NICID) *port {
	p := &port{n: n, tap: tap, nic: nic}
	p.txq = newTxQueue(n.cfg.PlainFIFO, func() int64 {
		p.mu.Lock()
		defer p.mu.Unlock()
		return shapedQueueBytes(p.shape)
	})
	return p
}

// gatewayMAC is the stack's MAC on every guest link. Each guest sees only
// its own link, so one address serves all.
var gatewayMAC = tcpip.LinkAddress([]byte{0x02, 0x72, 0x70, 0x00, 0x00, 0x01})

func (p *port) MTU() uint32                             { return p.n.mtu }
func (p *port) SetMTU(uint32)                           {}
func (p *port) MaxHeaderLength() uint16                 { return header.EthernetMinimumSize }
func (p *port) LinkAddress() tcpip.LinkAddress          { return gatewayMAC }
func (p *port) SetLinkAddress(tcpip.LinkAddress)        {}
func (p *port) ARPHardwareType() header.ARPHardwareType { return header.ARPHardwareEther }
func (p *port) Wait()                                   {}
func (p *port) SetOnCloseAction(func())                 {}
func (p *port) AddHeader(*stack.PacketBuffer)           {}
func (p *port) ParseHeader(*stack.PacketBuffer) bool    { return true }
func (p *port) Capabilities() stack.LinkEndpointCapabilities {
	// The guest's virtio-net checksums what it sends.
	return stack.CapabilityResolutionRequired | stack.CapabilityRXChecksumOffload
}
func (p *port) Attach(d stack.NetworkDispatcher) {
	p.mu.Lock()
	p.dispat = d
	p.mu.Unlock()
}
func (p *port) IsAttached() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.dispat != nil }
func (p *port) Close()           {}

// WritePackets is the stack sending to the guest: framed, then queued
// in the free FIFO (from ShapeExempt, and ARP) or the fair queue the
// shaper drains. ToView is already the caller's own copy, written as it
// is (I-1668: the second copy of each frame cost a quarter of the rate).
func (p *port) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	n := 0
	now := time.Now()
	for _, pkt := range pkts.AsSlice() {
		r := pkt.EgressRoute
		src := r.LocalLinkAddress
		if src == "" {
			src = gatewayMAC
		}
		eth := header.Ethernet(pkt.LinkHeader().Push(header.EthernetMinimumSize))
		eth.Encode(&header.EthernetFields{Type: pkt.NetworkProtocolNumber, SrcAddr: src, DstAddr: r.RemoteLinkAddress})
		v := pkt.ToView()
		shaped := false
		if pkt.NetworkProtocolNumber == header.IPv4ProtocolNumber {
			if a, ok := netip.AddrFromSlice(r.LocalAddress.AsSlice()); ok && !inAny(a, p.n.rules.ShapeExempt) {
				shaped = true
			}
		}
		p.txq.put(txItem{b: v.AsSlice(), v: v, at: now}, shaped)
		n++
	}
	return n, nil
}

// queueFrame queues a frame hostd built (an echo reply).
func (p *port) queueFrame(b []byte, shaped bool) {
	p.txq.put(txItem{b: b, at: time.Now()}, shaped)
}

// attach gives the port its frame connection and starts its two loops.
// A port already attached closes the old connection first: a restored VM
// brings a new socketpair.
func (p *port) attach(c FrameConn) {
	p.mu.Lock()
	old := p.conn
	p.conn = c
	p.mu.Unlock()
	if old != nil {
		_ = old.Close() // replaced; its loops end on the error
	}
	go p.readLoop(c)
	go p.writeLoop(c)
}

// ready: a VM is connected (a DgramConn has heard from its peer).
func (p *port) ready() bool {
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	if c == nil {
		return false
	}
	if r, ok := c.(interface{ Ready() bool }); ok {
		return r.Ready()
	}
	return true
}

func (p *port) attached() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.conn != nil && !p.closed }

func (p *port) current(c FrameConn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn == c && !p.closed
}

func (p *port) writeLoop(c FrameConn) {
	for p.current(c) {
		it, shaped, ok := p.txq.next(time.Now())
		if !ok {
			select {
			case <-p.txq.wake:
			case <-time.After(time.Second):
			}
			continue
		}
		if shaped {
			p.mu.Lock()
			sh := p.shape
			p.mu.Unlock()
			if d := sh.Reserve(len(it.b), time.Now()); d > 0 {
				time.Sleep(d)
			}
		}
		p.send(c, it.b)
		it.release()
	}
}

func (p *port) send(c FrameConn, f []byte) {
	if _, err := c.Write(f); err == nil {
		p.rx.Add(uint64(len(f)))
	}
}

func (p *port) readLoop(c FrameConn) {
	buf := make([]byte, 65536+header.EthernetMinimumSize)
	for {
		n, err := c.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || !p.current(c) {
				p.detach(c)
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			p.detach(c)
			return
		}
		if n < header.EthernetMinimumSize {
			continue
		}
		p.tx.Add(uint64(n))
		p.inbound(buf[:n])
	}
}

func (p *port) detach(c FrameConn) {
	p.mu.Lock()
	if p.conn == c {
		p.conn = nil
	}
	p.mu.Unlock()
	_ = c.Close() // ended
	p.n.kick()
}

// inbound admits one frame from the guest.
func (p *port) inbound(frame []byte) {
	eth := header.Ethernet(frame)
	p.mu.Lock()
	ip, mac, gate, police, d, gid := p.ip, p.mac, p.gate, p.police, p.dispat, p.guestID
	p.mu.Unlock()
	if gate == nil || d == nil || eth.SourceAddress() != mac {
		p.drops.Add(1)
		return
	}
	payload := frame[header.EthernetMinimumSize:]
	switch eth.Type() {
	case header.ARPProtocolNumber:
		a := header.ARP(payload)
		if !a.IsValid() || tcpip.LinkAddress(a.HardwareAddressSender()) != mac || netip.AddrFrom4([4]byte(a.ProtocolAddressSender())) != ip {
			p.drops.Add(1)
			return
		}
	case header.IPv4ProtocolNumber:
		h := header.IPv4(payload)
		if !h.IsValid(len(payload)) {
			p.drops.Add(1)
			return
		}
		src := h.SourceAddress().As4()
		if netip.AddrFrom4(src) != ip {
			p.drops.Add(1)
			return
		}
		pk := p.parse(h)
		now := time.Now()
		// tc's ingress policer runs before netfilter (I-217).
		if !inAny(pk.Dst, p.n.rules.ShapeExempt) && !police.Allow(len(payload), now) {
			p.policed.Add(1)
			return
		}
		v, kind, dest := gate.Check(pk, now)
		if v == Drop {
			if kind != "" {
				p.n.count(gid, kind, 1)
			}
			return
		}
		if dest == DestInternet {
			p.n.addEgress(gid, uint64(len(h)))
			if pk.Proto == 1 {
				p.pingOut(h)
				return
			}
		}
	default:
		// IPv6 and the rest: dropped (`meta nfproto ipv6 drop`).
		p.drops.Add(1)
		return
	}
	// One copy, into the stack's pooled chunks: the read buffer is reused.
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithView(buffer.NewViewWithData(payload))})
	d.DeliverNetworkPacket(eth.Type(), pkt)
	pkt.DecRef()
}

func (p *port) guestIDLocked() string { p.mu.Lock(); defer p.mu.Unlock(); return p.guestID }

// parse reads the fields the rules need. A UDP datagram is new when the
// port's UDP table has no session for its 4-tuple.
func (p *port) parse(h header.IPv4) Packet {
	pk := Packet{Proto: h.Protocol(), Dst: netip.AddrFrom4(h.DestinationAddress().As4())}
	if h.FragmentOffset() != 0 {
		return pk
	}
	body := h.Payload()
	switch pk.Proto {
	case 6:
		if len(body) >= header.TCPMinimumSize {
			t := header.TCP(body)
			pk.DstPort = t.DestinationPort()
			f := t.Flags()
			pk.New = f.Contains(header.TCPFlagSyn) && !f.Contains(header.TCPFlagAck)
		}
	case 17:
		if len(body) >= header.UDPMinimumSize {
			u := header.UDP(body)
			pk.DstPort = u.DestinationPort()
			pk.New = !p.n.udpKnown(udpKey{src: netip.AddrPortFrom(netip.AddrFrom4(h.SourceAddress().As4()), u.SourcePort()), dst: netip.AddrPortFrom(pk.Dst, u.DestinationPort())})
		}
	case 1:
		if len(body) >= header.ICMPv4MinimumSize {
			pk.Echo = header.ICMPv4(body).Type() == header.ICMPv4Echo
			pk.New = pk.Echo
		}
	}
	return pk
}

func (p *port) close() {
	p.mu.Lock()
	c := p.conn
	p.conn, p.closed = nil, true
	p.mu.Unlock()
	p.txq.drain()
	if c != nil {
		_ = c.Close() // the port is going away
	}
}
