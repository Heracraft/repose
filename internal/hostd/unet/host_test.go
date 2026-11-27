//go:build unix

package unet

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEdgeConfigPrefersHostJSON(t *testing.T) {
	d := t.TempDir()
	host, hub := filepath.Join(d, "host.json"), filepath.Join(d, "hub.json")
	if _, ok, err := LoadEdgeConfig(host, hub); ok || err != nil {
		t.Fatalf("nothing: ok %v err %v", ok, err)
	}
	_ = os.WriteFile(hub, []byte(`{"private_key":"a","address":"10.255.1.2/32","edge_pubkey":"b","edge_endpoint":"e:51820"}`), 0o600)
	c, ok, err := LoadEdgeConfig(host, hub)
	if !ok || err != nil || c.Address != "10.255.1.2/32" {
		t.Fatalf("hub: %+v %v %v", c, ok, err)
	}
	_ = os.WriteFile(host, []byte(`{"host_id":"h","wg":{"private_key":"k","address":"10.255.1.3/32","edge_pubkey":"b","edge_endpoint":"e:51820"}}`), 0o600)
	c, ok, err = LoadEdgeConfig(host, hub)
	if !ok || err != nil || c.Address != "10.255.1.3/32" || c.PrivateKey != "k" {
		t.Fatalf("host: %+v %v %v", c, ok, err)
	}
}

func TestLauncherReconnect(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ch.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- r.Method + " " + r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
	}()
	defer func() { _ = l.Close() }()
	rc := LauncherReconnect(func(string) string { return sock })
	if err := rc(context.Background(), "g", "tap-x"); err != nil {
		t.Fatal(err)
	}
	if g := <-got; g != "PUT /api/v1/vm.net-reconnect" {
		t.Fatal(g)
	}
}

// The launcher's side of the agreed contract (design2-program.log
// 14:58Z): bind its own unixgram socket, connect to DIR/<tap>.sock, send
// VFKT, then frames.
func TestSocketDirPortTakesTheLauncherAfterVFKT(t *testing.T) {
	in := newInternet(t)
	dir, err := os.MkdirTemp("", "un") // short: a unix path is at most 104 bytes on macOS
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	n, err := New(Config{Gateway: mustAddr(testGW), Dial: in.dial, SocketDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	ctx := context.Background()
	if err := n.AddTap(ctx, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := n.AddGuestRules(ctx, gA, ipA, macA, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	vm := func() net.Conn {
		local := filepath.Join(dir, "net0.dgram")
		_ = os.Remove(local)
		c, err := net.DialUnix("unixgram", &net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: filepath.Join(dir, "tap-aaaaaaaa.sock"), Net: "unixgram"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write([]byte("VFKT")); err != nil {
			t.Fatal(err)
		}
		return c
	}
	g := bootGuest(t, vm(), ipA, macA, testGW)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := g.dial(cctx, "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "over the launcher's socket")
	_ = c.Close()
	if !n.Attached("tap-aaaaaaaa") {
		t.Fatal("not attached")
	}
	if err := n.DelTap(ctx, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tap-aaaaaaaa.sock")); !os.IsNotExist(err) {
		t.Fatalf("socket outlived DelTap: %v", err)
	}
}

func TestEdgeWakesAParkedGuest(t *testing.T) {
	in := newInternet(t)
	dir, err := os.MkdirTemp("", "un")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	woke := make(chan string, 1)
	var n *Net
	vmUp := func() net.Conn {
		local := filepath.Join(dir, "net0.dgram")
		_ = os.Remove(local)
		c, err := net.DialUnix("unixgram", &net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: filepath.Join(dir, "tap-aaaaaaaa.sock"), Net: "unixgram"})
		if err != nil {
			t.Error(err)
			return nil
		}
		_, _ = c.Write([]byte("VFKT"))
		return c
	}
	var g *testGuest
	n, err = New(Config{Gateway: mustAddr(testGW), Dial: in.dial, SocketDir: dir, OnWake: func(tap string) {
		woke <- tap
		go func() { // the VM restores and its NIC connects again
			time.Sleep(200 * time.Millisecond)
			g2 := bootGuest(t, vmUp(), ipA, macA, testGW)
			l := g2.listen(t, 22)
			go func() {
				if c, err := l.Accept(); err == nil {
					_, _ = c.Write([]byte("SSH-2.0-woken"))
					_ = c.Close()
				}
			}()
		}()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	ctx := context.Background()
	_ = n.AddTap(ctx, "tap-aaaaaaaa")
	_ = n.AddGuestRules(ctx, gA, ipA, macA, "tap-aaaaaaaa")
	g = bootGuest(t, vmUp(), ipA, macA, testGW)
	te, _ := startTestEdge(t, n)
	_ = g.conn.Close() // the VM parks
	n.Detach("tap-aaaaaaaa")
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := te.net.DialContextTCPAddrPort(cctx, mustAddrPort(ipA+":22"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	buf := make([]byte, 13)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := ioReadFull(c, buf); err != nil || string(buf) != "SSH-2.0-woken" {
		t.Fatalf("%q %v", buf, err)
	}
	if tap := <-woke; tap != "tap-aaaaaaaa" {
		t.Fatal(tap)
	}
}
