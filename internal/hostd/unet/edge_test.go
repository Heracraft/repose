//go:build unix

package unet

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type wgKey [32]byte

func newKey(t *testing.T) (priv, pub wgKey) {
	t.Helper()
	if _, err := rand.Read(priv[:]); err != nil {
		t.Fatal(err)
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	copy(pub[:], p)
	return
}

func (k wgKey) b64() string { return base64.StdEncoding.EncodeToString(k[:]) }
func (k wgKey) hex() string { return hex.EncodeToString(k[:]) }

// testEdge is the edge gateway in miniature: wireguard-go on a netstack
// of its own holding the gateway's address (10.255.0.1) and the control
// plane's hub address (10.255.255.1), peered with the host.
type testEdge struct {
	net  *netstack.Net
	dev  *device.Device
	port int
	pub  wgKey
}

func newTestEdge(t *testing.T, hostPub wgKey, hostAllowed ...string) *testEdge {
	t.Helper()
	priv, pub := newKey(t)
	tn, nn, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.255.0.1"), netip.MustParseAddr("10.255.255.1")}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tn, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	// A free UDP port for the edge's listener.
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\npublic_key=%s\n", priv.hex(), port, hostPub.hex())
	for _, a := range hostAllowed {
		fmt.Fprintf(&b, "allowed_ip=%s\n", a)
	}
	if err := dev.IpcSet(b.String()); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	return &testEdge{net: nn, dev: dev, port: port, pub: pub}
}

func startTestEdge(t *testing.T, n *Net) (*testEdge, *Edge) {
	t.Helper()
	hpriv, hpub := newKey(t)
	te := newTestEdge(t, hpub, "10.64.4.0/22", "10.255.1.7/32")
	e, err := n.StartEdge(EdgeConfig{PrivateKey: hpriv.b64(), Address: "10.255.1.7/32", EdgePubkey: te.pub.b64(), EdgeEndpoint: fmt.Sprintf("127.0.0.1:%d", te.port)})
	if err != nil {
		t.Fatal(err)
	}
	return te, e
}

func TestEdgeSSHReachesTheGuestFromTheGatewayAddress(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	te, _ := startTestEdge(t, n)
	l := g.listen(t, 22)
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		got <- c.RemoteAddr().String()
		buf := make([]byte, 64)
		k, _ := c.Read(buf)
		_, _ = c.Write(buf[:k])
		_ = c.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := te.net.DialContextTCPAddrPort(ctx, netip.MustParseAddrPort(ipA+":22"))
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "SSH-2.0-gateway")
	_ = c.Close()
	if r := <-got; !strings.HasPrefix(r, "10.255.0.1:") {
		t.Fatalf("guest saw the gateway as %s; want 10.255.0.1", r)
	}
	if v, _ := n.CounterBytes(ctx, gA); v != 0 {
		t.Fatalf("edge traffic counted as egress: %d", v)
	}
	// Only port 22.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	g.listen(t, 8080)
	if c, err := te.net.DialContextTCPAddrPort(ctx2, netip.MustParseAddrPort(ipA+":8080")); err == nil {
		_ = c.Close()
		t.Fatal("the edge opened a guest port other than 22")
	}
	// No guest at the address: refused.
	if c, err := te.net.DialContextTCPAddrPort(ctx2, netip.MustParseAddrPort("10.64.4.9:22")); err == nil {
		_ = c.Close()
		t.Fatal("the edge reached a guest that does not exist")
	}
}

func TestGuestReachesHookIngestWithItsOwnAddress(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	te, _ := startTestEdge(t, n)
	l, err := te.net.ListenTCPAddrPort(netip.MustParseAddrPort("10.255.0.1:8443"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		got <- c.RemoteAddr().String()
		buf := make([]byte, 64)
		k, _ := c.Read(buf)
		_, _ = c.Write(buf[:k])
		_ = c.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := g.dial(ctx, "10.255.0.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "POST /hooks")
	_ = c.Close()
	if r := <-got; !strings.HasPrefix(r, ipA+":") {
		t.Fatalf("hook ingest saw %s; want the guest's %s", r, ipA)
	}
	if in.hit("tcp4", "10.255.0.1:8443") != 0 {
		t.Fatal("hook ingest went to the internet")
	}
}

func TestHostdDialsTheAPIOverTheEdgeAndServesMetrics(t *testing.T) {
	in := newInternet(t)
	n := newTestNet(t, in, nil)
	te, e := startTestEdge(t, n)
	l, err := te.net.ListenTCPAddrPort(netip.MustParseAddrPort("10.255.255.1:8443"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		got <- c.RemoteAddr().String()
		buf := make([]byte, 64)
		k, _ := c.Read(buf)
		_, _ = c.Write(buf[:k])
		_ = c.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := e.DialContext(ctx, "tcp", "10.255.255.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "grpc")
	_ = c.Close()
	if r := <-got; !strings.HasPrefix(r, "10.255.1.7:") {
		t.Fatalf("api saw %s; want the host's mesh address", r)
	}
	ml, err := e.Listen(9101)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ml.Close() }()
	go func() {
		c, err := ml.Accept()
		if err == nil {
			_, _ = c.Write([]byte("metrics"))
			_ = c.Close()
		}
	}()
	mc, err := te.net.DialContextTCPAddrPort(ctx, netip.MustParseAddrPort("10.255.1.7:9101"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	_ = mc.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := ioReadFull(mc, buf); err != nil || string(buf) != "metrics" {
		t.Fatalf("metrics over the edge: %q %v", buf, err)
	}
	if sec, _, _ := e.Handshake(); sec == 0 {
		t.Fatal("no handshake recorded")
	}
	// A key rotation keeps the tunnel.
	hpriv2, hpub2 := newKey(t)
	if err := te.dev.IpcSet("public_key=" + hpub2.hex() + "\nallowed_ip=10.64.4.0/22\nallowed_ip=10.255.1.7/32\n"); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconfigure(EdgeConfig{PrivateKey: hpriv2.b64(), Address: "10.255.1.7", EdgePubkey: te.pub.b64(), EdgeEndpoint: fmt.Sprintf("127.0.0.1:%d", te.port)}); err != nil {
		t.Fatal(err)
	}
	go func() {
		if c, err := l.Accept(); err == nil {
			_, _ = c.Write([]byte("rotated"))
			_ = c.Close()
		}
	}()
	c2, err := e.DialContext(ctx, "tcp", "10.255.255.1:8443")
	if err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	buf = make([]byte, 7)
	_ = c2.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := ioReadFull(c2, buf); err != nil || string(buf) != "rotated" {
		t.Fatalf("after rotation: %q %v", buf, err)
	}
}

func TestEdgeErrorsNameNoKey(t *testing.T) {
	n := newTestNet(t, newInternet(t), nil)
	secret := "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0LXNlY3I=" // 32 bytes
	_, err := n.StartEdge(EdgeConfig{PrivateKey: secret, Address: "bad", EdgePubkey: secret, EdgeEndpoint: "127.0.0.1:1"})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err %v", err)
	}
	_, err = n.StartEdge(EdgeConfig{PrivateKey: "short", Address: "10.255.1.1", EdgePubkey: secret, EdgeEndpoint: "127.0.0.1:1"})
	if err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("err %v", err)
	}
}
