//go:build unix

package unet

import (
	"context"
	"io"
	"testing"
	"time"
)

// BenchmarkGuestUpload: bytes a guest sends to a socket of the host's.
func BenchmarkGuestUpload(b *testing.B) {
	t := &testing.T{}
	_ = t
	in := newInternet(&testing.T{})
	n, _ := New(Config{Gateway: mustAddr(testGW), Dial: in.dial})
	defer n.Close()
	g := newTestGuest(&testing.T{}, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	c, err := g.dial(context.Background(), "203.0.113.5:443")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	go func() { _, _ = io.Copy(io.Discard, c) }()
	buf := make([]byte, 256<<10)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	t0 := time.Now()
	for i := 0; i < b.N; i++ {
		if _, err := c.Write(buf); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N*len(buf))*8/time.Since(t0).Seconds()/1e9, "Gbit/s")
}
