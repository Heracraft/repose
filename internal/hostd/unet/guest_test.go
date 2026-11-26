//go:build unix

package unet

import (
	"context"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// testGuest is a guest's kernel in miniature: a gVisor stack of its own
// with one Ethernet NIC whose frames cross a unixgram socketpair, the
// same shape as VZ's file-handle attachment.
type testGuest struct {
	s    *stack.Stack
	ip   netip.Addr
	mac  string
	conn net.Conn // the guest's end of the pair
	ep   *channel.Endpoint
	done chan struct{}
}

// dgramPair returns both ends of a unixgram socketpair as net.Conns.
func dgramPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(fd int, name string) net.Conn {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 1<<20)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4<<20)
		f := os.NewFile(uintptr(fd), name)
		c, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	return mk(fds[0], "host"), mk(fds[1], "guest")
}

// newTestGuest boots a guest at ip with mac, gateway gw, and attaches its
// NIC to tap on n.
func newTestGuest(t *testing.T, n *Net, tap, id, ip, mac, gw string) *testGuest {
	t.Helper()
	ctx := context.Background()
	if err := n.AddTap(ctx, tap); err != nil {
		t.Fatal(err)
	}
	if err := n.AddGuestRules(ctx, id, ip, mac, tap); err != nil {
		t.Fatal(err)
	}
	hostEnd, guestEnd := dgramPair(t)
	if !n.Attach(tap, hostEnd) {
		t.Fatal("attach")
	}
	return bootGuest(t, guestEnd, ip, mac, gw)
}

func bootGuest(t *testing.T, conn net.Conn, ip, mac, gw string) *testGuest {
	t.Helper()
	hw, _ := net.ParseMAC(mac)
	ep := channel.New(1024, 1500, tcpip.LinkAddress(hw))
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4},
	})
	if err := s.CreateNIC(1, ethernet.New(ep)); err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr(ip)
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpipAddr(a), PrefixLen: 22}}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, Gateway: tcpipAddr(netip.MustParseAddr(gw)), NIC: 1}})
	g := &testGuest{s: s, ip: a, mac: mac, conn: conn, ep: ep, done: make(chan struct{})}
	go func() { // frames out of the guest
		for {
			pkt := ep.ReadContext(context.Background())
			if pkt == nil {
				return
			}
			b := pkt.ToView().AsSlice()
			_, err := conn.Write(b)
			pkt.DecRef()
			if err != nil {
				return
			}
		}
	}()
	go func() { // frames into the guest
		buf := make([]byte, 65536)
		for {
			k, err := conn.Read(buf)
			if err != nil {
				return
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), buf[:k]...))})
			ep.InjectInbound(0, pkt)
			pkt.DecRef()
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); ep.Close(); s.Close() })
	return g
}

// dial opens a TCP connection from the guest.
func (g *testGuest) dial(ctx context.Context, to string) (net.Conn, error) {
	ap := netip.MustParseAddrPort(to)
	return gonet.DialContextTCP(ctx, g.s, tcpip.FullAddress{NIC: 1, Addr: tcpipAddr(ap.Addr()), Port: ap.Port()}, ipv4.ProtocolNumber)
}

// listen listens on the guest's own address.
func (g *testGuest) listen(t *testing.T, port uint16) *gonet.TCPListener {
	t.Helper()
	l, err := gonet.ListenTCP(g.s, tcpip.FullAddress{NIC: 1, Addr: tcpipAddr(g.ip), Port: port}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func (g *testGuest) udp(to string) (*gonet.UDPConn, error) {
	ap := netip.MustParseAddrPort(to)
	return gonet.DialUDP(g.s, nil, &tcpip.FullAddress{NIC: 1, Addr: tcpipAddr(ap.Addr()), Port: ap.Port()}, ipv4.ProtocolNumber)
}

// internet is a fake internet: every TCP and UDP address the network
// dials lands on one local server, which records what it saw.
type internet struct {
	tcp  net.Listener
	udp  net.PacketConn
	mu   sync.Mutex
	hits map[string]int
}

func newInternet(t *testing.T) *internet {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	in := &internet{tcp: l, udp: u, hits: map[string]int{}}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { // echo
				buf := make([]byte, 65536)
				for {
					k, err := c.Read(buf)
					if err != nil {
						_ = c.Close()
						return
					}
					if _, err := c.Write(buf[:k]); err != nil {
						_ = c.Close()
						return
					}
				}
			}()
		}
	}()
	go func() {
		buf := make([]byte, 65536)
		for {
			k, a, err := u.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = u.WriteTo(buf[:k], a)
		}
	}()
	t.Cleanup(func() { _ = l.Close(); _ = u.Close() })
	return in
}

func (in *internet) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	in.mu.Lock()
	in.hits[network+" "+addr]++
	in.mu.Unlock()
	var d net.Dialer
	if network == "udp4" {
		return d.DialContext(ctx, network, in.udp.LocalAddr().String())
	}
	return d.DialContext(ctx, network, in.tcp.Addr().String())
}

func (in *internet) hit(network, addr string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.hits[network+" "+addr]
}

const (
	testGW = "10.64.4.1"
)

func newTestNet(t *testing.T, in *internet, r *Rules) *Net {
	t.Helper()
	n, err := New(Config{Gateway: netip.MustParseAddr(testGW), Dial: in.dial, Rules: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n
}

func echoOnce(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := ioReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Fatalf("echo %q, want %q", buf, msg)
	}
}

func ioReadFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
