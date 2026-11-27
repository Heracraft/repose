package guest

import (
	"context"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// review-cli 2026-11-27 R2: `repose rm` of a running machine during a
// hostd restart failed "systemctl kill: launchctl: context canceled
// (internal)" and left the machine in error. A stop or destroy cut off by
// hostd's own shutdown is not stored, so the api's resend after the
// restart runs it again and it completes.
func TestDestroyCutOffByAHostdRestartResends(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	// The guest does not power off by itself, so the stop reaches the
	// kill, as a VM that is slow to go down does.
	h.mu.Lock()
	srv := h.guestds[gid1]
	h.mu.Unlock()
	srv.OnShutdown(func() {})
	h.m.d.SystemStopping = func(context.Context) bool { return false }
	d := cmd(&hostdv1.DestroyGuest{GuestId: gid1})
	ctx, cancel := context.WithCancel(context.Background())
	// app.Run's order: SetShuttingDown, then the root context ends.
	h.m.SetShuttingDown()
	cancel()
	if res := h.m.Execute(ctx, d); res != nil {
		t.Fatalf("a destroy cut off by a hostd restart answered %v; want nothing stored or sent", res)
	}
	h.m.Close()
	// The next hostd's stop finds a guest that powers off when asked.
	srv.OnShutdown(func() { h.sd.Exit(GuestUnit(gid1), 0) })
	m2, err := New(h.cfg, h.m.d)
	if err != nil {
		t.Fatal(err)
	}
	m2.Run()
	defer m2.Close()
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := m2.Execute(context.Background(), clone(d)); !r2.Ok {
		t.Fatalf("the resent destroy after the restart: %v", r2.Error)
	}
}
