package unet

import (
	"context"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// pingTimeout is how long a forwarded echo waits for its reply.
const pingTimeout = 3 * time.Second

// SysPinger is the host's unprivileged ICMP socket (SOCK_DGRAM,
// IPPROTO_ICMP): macOS gives it to any user; Linux to the groups in
// net.ipv4.ping_group_range. One socket per echo, so a reply is matched
// to its request by the socket, and the kernel may rewrite the
// identifier (Linux does) without confusing guests.
type SysPinger struct{}

// Echo implements Pinger.
func (SysPinger) Echo(ctx context.Context, dst netip.Addr, id, seq uint16, payload []byte) ([]byte, error) {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }() // one echo per socket
	dl := time.Now().Add(pingTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = c.SetDeadline(dl) // a packet conn supports deadlines
	m := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: int(id), Seq: int(seq), Data: payload}}
	b, err := m.Marshal(nil)
	if err != nil {
		return nil, err
	}
	if _, err := c.WriteTo(b, &net.UDPAddr{IP: net.IP(dst.AsSlice())}); err != nil {
		return nil, err
	}
	buf := make([]byte, 65536)
	for {
		n, from, err := c.ReadFrom(buf)
		if err != nil {
			return nil, err
		}
		r := buf[:n]
		// Darwin hands the IP header up with the message; Linux does not.
		if len(r) >= header.IPv4MinimumSize && r[0]>>4 == 4 {
			if ihl := int(r[0]&0x0f) * 4; ihl <= len(r) {
				r = r[ihl:]
			}
		}
		rm, err := icmp.ParseMessage(1, r)
		if err != nil {
			continue
		}
		e, ok := rm.Body.(*icmp.Echo)
		if rm.Type != ipv4.ICMPTypeEchoReply || !ok || e.Seq != int(seq) {
			continue
		}
		if u, ok := from.(*net.UDPAddr); ok && !u.IP.Equal(net.IP(dst.AsSlice())) {
			continue
		}
		return e.Data, nil
	}
}

// pingOut forwards a guest's echo request, as the Linux host forwards
// it, and writes the reply to the guest as if it came from dst. The
// rules have admitted it; the stack never sees it, since a promiscuous
// stack would answer for any address itself.
func (p *port) pingOut(h header.IPv4) {
	ic := header.ICMPv4(h.Payload())
	if len(ic) < header.ICMPv4MinimumSize {
		return
	}
	src := netipAddr(h.SourceAddress())
	dst := netipAddr(h.DestinationAddress())
	id, seq := ic.Ident(), ic.Sequence()
	data := append([]byte(nil), ic.Payload()...)
	pg := p.n.cfg.Pinger
	if pg == nil {
		pg = SysPinger{}
	}
	go func() {
		ctx, cancel := context.WithTimeout(p.n.ctx, pingTimeout)
		defer cancel()
		reply, err := pg.Echo(ctx, dst, id, seq, data)
		if err != nil {
			return // no reply: the guest sees a timeout, as it would
		}
		p.mu.Lock()
		mac := p.mac
		p.mu.Unlock()
		p.queueFrame(echoReplyFrame(mac, src, dst, id, seq, reply), !inAny(dst, p.n.rules.ShapeExempt))
	}()
}

// echoReplyFrame builds dst's reply to the guest at src.
func echoReplyFrame(guestMAC tcpip.LinkAddress, src, dst netip.Addr, id, seq uint16, payload []byte) []byte {
	b := make([]byte, header.EthernetMinimumSize+header.IPv4MinimumSize+header.ICMPv4MinimumSize+len(payload))
	header.Ethernet(b).Encode(&header.EthernetFields{SrcAddr: gatewayMAC, DstAddr: guestMAC, Type: header.IPv4ProtocolNumber})
	ip := header.IPv4(b[header.EthernetMinimumSize:])
	ip.Encode(&header.IPv4Fields{TotalLength: uint16(len(b) - header.EthernetMinimumSize), TTL: 63, Protocol: 1, SrcAddr: tcpipAddr(dst), DstAddr: tcpipAddr(src)})
	ip.SetChecksum(^ip.CalculateChecksum())
	ic := header.ICMPv4(ip.Payload())
	ic.SetType(header.ICMPv4EchoReply)
	ic.SetCode(0)
	ic.SetIdent(id)
	ic.SetSequence(seq)
	copy(ic.Payload(), payload)
	ic.SetChecksum(0)
	ic.SetChecksum(^checksum.Checksum(ic, 0))
	return b
}
