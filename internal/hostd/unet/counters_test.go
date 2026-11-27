//go:build unix

package unet

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	hnet "github.com/heracraft/repose/internal/hostd/net"
)

func TestCountersOutliveARestart(t *testing.T) {
	in := newInternet(t)
	file := filepath.Join(t.TempDir(), "c.json")
	n, err := New(Config{Gateway: mustAddr(testGW), Dial: in.dial, CounterFile: file})
	if err != nil {
		t.Fatal(err)
	}
	g := newTestGuest(t, n, "tap-aaaaaaaa", gA, ipA, macA, testGW)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := g.dial(ctx, "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "before the restart")
	_ = c.Close()
	mustNotConnect(t, g, in, "203.0.113.5:25")
	eg, _ := n.CounterBytes(ctx, gA)
	sm := blocked(t, n, gA, hnet.BlockedSMTP)
	rx, tx, _ := n.TapStats("tap-aaaaaaaa")
	n.Close() // hostd stops
	n2, err := New(Config{Gateway: mustAddr(testGW), Dial: in.dial, CounterFile: file})
	if err != nil {
		t.Fatal(err)
	}
	defer n2.Close()
	// Reconcile re-adds the running guest's rules and shape.
	if err := n2.AddGuestRules(ctx, gA, ipA, macA, "tap-aaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	eg2, err := n2.CounterBytes(ctx, gA)
	if err != nil || eg2 < eg || eg == 0 {
		t.Fatalf("egress %d -> %d (%v)", eg, eg2, err)
	}
	if sm2 := blocked(t, n2, gA, hnet.BlockedSMTP); sm2 < sm || sm == 0 {
		t.Fatalf("smtp %d -> %d", sm, sm2)
	}
	rx2, tx2, _ := n2.TapStats("tap-aaaaaaaa")
	if rx2 < rx || tx2 < tx || rx == 0 {
		t.Fatalf("tap %d/%d -> %d/%d", rx, tx, rx2, tx2)
	}
}
