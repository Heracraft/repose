//go:build lab

// d2netlab: hostd's darwin network (internal/hostd/unet) alone, on the lab
// Mac, with real VZ guests (d2-arch's vzx2, fh=DIR/<tap>.sock) and a stand-in
// edge: wireguard-go on a netstack of its own in this process, peered with
// the host over UDP on loopback, so every edge connection crosses a real
// WireGuard tunnel into unet's edge NIC.
//
//	d2netlab -dir DIR -gw 10.64.4.1 -guest tap,id,ip,mac ... [-up M -down M]
//	         [-fwd 127.0.0.1:2201=10.64.4.2:22 ...] [-ctl 127.0.0.1:9900]
//
// -fwd listens on the Mac and dials the guest THROUGH the edge tunnel (the
// gateway's path). The edge serves 10.255.0.1:8443 (hook ingest stand-in:
// answers "src=<remote>") and 10.255.255.1:8443 is not served.
// ctl: GET /status (counters JSON), GET /shape?tap=&up=&down=.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/heracraft/repose/internal/hostd/unet"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, " ") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func key() (priv, pub [32]byte) {
	_, _ = rand.Read(priv[:])
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	p, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
	copy(pub[:], p)
	return
}

func main() {
	dir := flag.String("dir", "", "socket dir")
	gw := flag.String("gw", "10.64.4.1", "gateway")
	cidr := flag.String("cidr", "10.64.4.0/22", "host guest range (edge allowed ips)")
	up := flag.Int("up", 0, "upload Mbit (0 none)")
	down := flag.Int("down", 0, "download Mbit (0 none)")
	ctl := flag.String("ctl", "127.0.0.1:9900", "control listen")
	mtu := flag.Int("mtu", 1500, "guest link MTU")
	fifo := flag.Bool("fifo", false, "plain FIFO download queue (the shape before I-1668's amendment)")
	var guests, fwds, maps multi
	flag.Var(&maps, "map", "dst=real: the lab's dialer sends a guest's dial of dst to real (a benchmark server on the Mac, which the rules rightly refuse to a guest)")
	flag.Var(&guests, "guest", "tap,id,ip,mac")
	flag.Var(&fwds, "fwd", "local=guestip:port through the edge")
	flag.Parse()
	mapped := map[string]string{}
	for _, m := range maps {
		a, b, _ := strings.Cut(m, "=")
		mapped[a] = b
	}
	var d net.Dialer
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if r, ok := mapped[addr]; ok {
			addr = r
		}
		return d.DialContext(ctx, network, addr)
	}
	n, err := unet.New(unet.Config{Gateway: netip.MustParseAddr(*gw), SocketDir: *dir, Dial: dial, MTU: uint32(*mtu), PlainFIFO: *fifo})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	for _, g := range guests {
		f := strings.Split(g, ",")
		if err := n.AddTap(ctx, f[0]); err != nil {
			log.Fatal(err)
		}
		if err := n.AddGuestRules(ctx, f[1], f[2], f[3], f[0]); err != nil {
			log.Fatal(err)
		}
		if *up > 0 || *down > 0 {
			_ = n.Shape(ctx, f[0], *up, *down)
		}
		log.Printf("guest %s %s %s on %s/%s.sock", f[1], f[2], f[3], *dir, f[0])
	}
	// The stand-in edge.
	hpriv, hpub := key()
	epriv, epub := key()
	tn, en, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.255.0.1"), netip.MustParseAddr("10.255.255.1")}, nil, 1420)
	if err != nil {
		log.Fatal(err)
	}
	ed := device.NewDevice(tn, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "edge "))
	if err := ed.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=51820\npublic_key=%s\nallowed_ip=%s\nallowed_ip=10.255.1.7/32\n", hex.EncodeToString(epriv[:]), hex.EncodeToString(hpub[:]), *cidr)); err != nil {
		log.Fatal(err)
	}
	_ = ed.Up()
	e, err := n.StartEdge(unet.EdgeConfig{PrivateKey: base64.StdEncoding.EncodeToString(hpriv[:]), Address: "10.255.1.7/32", EdgePubkey: base64.StdEncoding.EncodeToString(epub[:]), EdgeEndpoint: "127.0.0.1:51820"})
	if err != nil {
		log.Fatal(err)
	}
	hl, err := en.ListenTCPAddrPort(netip.MustParseAddrPort("10.255.0.1:8443"))
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		_ = http.Serve(hl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "src=%s\n", r.RemoteAddr)
		}))
	}()
	for _, f := range fwds {
		l, r, _ := strings.Cut(f, "=")
		ln, err := net.Listen("tcp4", l)
		if err != nil {
			log.Fatal(err)
		}
		dst := netip.MustParseAddrPort(r)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					o, err := en.DialContextTCPAddrPort(ctx, dst)
					if err != nil {
						log.Printf("fwd %s: %v", dst, err)
						_ = c.Close()
						return
					}
					go func() { _, _ = io.Copy(o, c); _ = o.CloseWrite() }()
					_, _ = io.Copy(c, o)
					_ = c.Close()
					_ = o.Close()
				}()
			}
		}()
		log.Printf("fwd %s -> edge -> %s", l, dst)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		eg, _ := n.EgressBytes(ctx)
		bl, _ := n.BlockedPackets(ctx)
		taps, _ := n.ListTaps(ctx)
		st := map[string]any{}
		for _, t := range taps {
			rx, tx, _ := n.TapStats(t)
			st[t] = map[string]any{"rx": rx, "tx": tx, "drops": n.Drops(t), "attached": n.Attached(t)}
		}
		hs, erx, etx := e.Handshake()
		_ = json.NewEncoder(w).Encode(map[string]any{"egress": eg, "blocked": bl, "taps": st, "edge": map[string]any{"handshake": hs, "rx": erx, "tx": etx}})
	})
	mux.HandleFunc("/shape", func(w http.ResponseWriter, r *http.Request) {
		u, _ := strconv.Atoi(r.URL.Query().Get("up"))
		d, _ := strconv.Atoi(r.URL.Query().Get("down"))
		t := r.URL.Query().Get("tap")
		if u == 0 && d == 0 {
			_ = n.Unshape(ctx, t)
		} else {
			_ = n.Shape(ctx, t, u, d)
		}
		_, _ = fmt.Fprintf(w, "ok %s %d %d\n", t, u, d)
	})
	go func() { _ = http.ListenAndServe(*ctl, mux) }() // lab only
	log.Printf("ready")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	n.Close()
	ed.Close()
}
