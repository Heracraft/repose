//go:build unix

package unet

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// fakePinger answers every echo for the addresses it is told are up.
type fakePinger struct {
	mu   sync.Mutex
	up   map[netip.Addr]bool
	seen []netip.Addr
}

func (f *fakePinger) Echo(_ context.Context, dst netip.Addr, _, _ uint16, payload []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, dst)
	if !f.up[dst] {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), payload...), nil
}

// echoFrame is a guest's ICMP echo request as its NIC sends it.
func echoFrame(srcMAC, dstMAC string, src, dst netip.Addr, id, seq uint16, payload []byte) []byte {
	sm, _ := net.ParseMAC(srcMAC)
	dm, _ := net.ParseMAC(dstMAC)
	b := make([]byte, header.EthernetMinimumSize+header.IPv4MinimumSize+header.ICMPv4MinimumSize+len(payload))
	header.Ethernet(b).Encode(&header.EthernetFields{SrcAddr: tcpip.LinkAddress(sm), DstAddr: tcpip.LinkAddress(dm), Type: header.IPv4ProtocolNumber})
	ip := header.IPv4(b[header.EthernetMinimumSize:])
	ip.Encode(&header.IPv4Fields{TotalLength: uint16(len(b) - header.EthernetMinimumSize), TTL: 64, Protocol: 1, SrcAddr: tcpipAddr(src), DstAddr: tcpipAddr(dst)})
	ip.SetChecksum(^ip.CalculateChecksum())
	ic := header.ICMPv4(ip.Payload())
	ic.SetType(header.ICMPv4Echo)
	ic.SetIdent(id)
	ic.SetSequence(seq)
	copy(ic.Payload(), payload)
	ic.SetChecksum(0)
	ic.SetChecksum(^checksum.Checksum(ic, 0))
	return b
}

// readEcho reads frames until an echo reply arrives or the deadline.
func readEcho(t *testing.T, c net.Conn, d time.Duration) (header.IPv4, bool) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 65536)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, false
		}
		eth := header.Ethernet(buf[:n])
		if eth.Type() != header.IPv4ProtocolNumber {
			continue
		}
		ip := header.IPv4(append([]byte(nil), buf[header.EthernetMinimumSize:n]...))
		if ip.Protocol() == 1 && header.ICMPv4(ip.Payload()).Type() == header.ICMPv4EchoReply {
			return ip, true
		}
	}
}

func TestEchoBeyondTheGatewayIsForwarded(t *testing.T) {
	in := newInternet(t)
	fp := &fakePinger{up: map[netip.Addr]bool{mustAddr("203.0.113.5"): true}}
	n, err := New(Config{Gateway: mustAddr(testGW), Dial: in.dial, Pinger: fp})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	ctx := context.Background()
	_ = n.AddTap(ctx, "tap-aaaaaaaa")
	_ = n.AddGuestRules(ctx, gA, ipA, macA, "tap-aaaaaaaa")
	hostEnd, guest := dgramPair(t)
	n.Attach("tap-aaaaaaaa", hostEnd)
	gwMAC := gatewayMAC.String()
	// Up: a reply from the address pinged, with the guest's id and seq.
	if _, err := guest.Write(echoFrame(macA, gwMAC, mustAddr(ipA), mustAddr("203.0.113.5"), 0x1234, 7, []byte("ping-payload"))); err != nil {
		t.Fatal(err)
	}
	ip, ok := readEcho(t, guest, 3*time.Second)
	if !ok {
		t.Fatal("no echo reply from 203.0.113.5")
	}
	ic := header.ICMPv4(ip.Payload())
	if netipAddr(ip.SourceAddress()) != mustAddr("203.0.113.5") || netipAddr(ip.DestinationAddress()) != mustAddr(ipA) || ic.Ident() != 0x1234 || ic.Sequence() != 7 || string(ic.Payload()) != "ping-payload" {
		t.Fatalf("reply %v -> %v id %x seq %d %q", ip.SourceAddress(), ip.DestinationAddress(), ic.Ident(), ic.Sequence(), ic.Payload())
	}
	if !ip.IsChecksumValid() || checksum.Checksum(ic, 0) != 0xffff {
		t.Fatal("bad checksum on the reply")
	}
	// Down: no reply, as the internet gives none (the stack must not
	// answer for an address itself).
	_, _ = guest.Write(echoFrame(macA, gwMAC, mustAddr(ipA), mustAddr("198.51.100.9"), 1, 1, []byte("x")))
	if _, ok := readEcho(t, guest, 1500*time.Millisecond); ok {
		t.Fatal("an echo to an address that is down was answered")
	}
	// Private destinations and other guests are never pinged.
	for _, dst := range []string{"192.168.1.1", ipB, "169.254.169.254", "127.0.0.1"} {
		_, _ = guest.Write(echoFrame(macA, gwMAC, mustAddr(ipA), mustAddr(dst), 1, 1, []byte("x")))
	}
	if _, ok := readEcho(t, guest, time.Second); ok {
		t.Fatal("a private destination answered")
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, a := range fp.seen {
		if a != mustAddr("203.0.113.5") && a != mustAddr("198.51.100.9") {
			t.Fatalf("pinged %v", a)
		}
	}
	if v, _ := n.CounterBytes(ctx, gA); v == 0 {
		t.Fatal("echo to the internet not counted as egress")
	}
}
