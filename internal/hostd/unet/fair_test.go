//go:build unix

package unet

import (
	"context"
	"io"
	"net"
	"sort"
	"testing"
	"time"
)

// chargen streams bytes to whoever connects, as a download does.
func chargen(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		buf := make([]byte, 64<<10)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				for {
					if _, err := c.Write(buf); err != nil {
						_ = c.Close()
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}

// keystrokeRTT measures 1-byte round trips on an interactive connection
// while a download saturates the guest's shaped link.
func keystrokeRTT(t *testing.T, fifo bool) (p50, p99 time.Duration, mbit float64) {
	in := newInternet(t)
	cg := chargen(t)
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "203.0.113.19:19" {
			var d net.Dialer
			return d.DialContext(ctx, network, cg)
		}
		return in.dial(ctx, network, addr)
	}
	n, err := New(Config{Gateway: mustAddr(testGW), Dial: dial, PlainFIFO: fifo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	if err := n.Shape(context.Background(), "tap-aaaaaaaa", 0, 50); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bulk, err := g.dial(ctx, "203.0.113.19:19")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bulk.Close() }()
	got := make(chan int64, 1)
	go func() {
		k, _ := io.Copy(io.Discard, bulk)
		got <- k
	}()
	time.Sleep(1500 * time.Millisecond) // the queue fills
	ks, err := g.dial(ctx, "203.0.113.5:22")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ks.Close() }()
	var ds []time.Duration
	b := []byte{'k'}
	t0 := time.Now()
	for i := 0; i < 40; i++ {
		s := time.Now()
		_ = ks.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := ks.Write(b); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(ks, b); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(s))
		time.Sleep(20 * time.Millisecond) // a typist
	}
	el := time.Since(t0)
	_ = bulk.Close()
	k := <-got
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[len(ds)/2], ds[len(ds)*99/100], float64(k) * 8 / (el.Seconds() + 1.5) / 1e6
}

func TestKeystrokeDoesNotWaitBehindADownload(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	f50, f99, fm := keystrokeRTT(t, true)
	q50, q99, qm := keystrokeRTT(t, false)
	t.Logf("keystroke RTT under a 50 Mbit-shaped download: FIFO p50 %v p99 %v (%.0f Mbit/s); fair p50 %v p99 %v (%.0f Mbit/s)", f50, f99, fm, q50, q99, qm)
	if q50 > 20*time.Millisecond {
		t.Fatalf("fair queue keystroke p50 %v; want under 20 ms", q50)
	}
	if qm < 35 {
		t.Fatalf("download under the fair queue %.0f Mbit/s; want near 50", qm)
	}
}
