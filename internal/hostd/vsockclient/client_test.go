package vsockclient

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	fakeguestd "github.com/heracraft/repose/internal/hostd/fakeguestd"
)

func TestUnixDialerAgainstFake(t *testing.T) {
	dir := t.TempDir()
	srv, err := fakeguestd.Listen(filepath.Join(dir, "guestd.sock"), fakeguestd.Options{FreezeWatchdog: 200 * time.Millisecond, NeedsReboot: map[string]bool{"/nix/store/k": true}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := UnixDialer{}.Dial(ctx, Target{GuestID: "g", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	select {
	case n := <-s.Notifications():
		if n.GetReady() == nil {
			t.Fatalf("expected Ready, got %v", n)
		}
	case <-ctx.Done():
		t.Fatal("no Ready")
	}
	if p, err := s.Ping(ctx); err != nil || p.BootId != "boot-1" {
		t.Fatalf("ping: %v %v", p, err)
	}
	if err := s.WriteSecrets(ctx, []*guestdv1.Secret{{Name: "A", Value: []byte("1")}}); err != nil {
		t.Fatal(err)
	}
	if string(srv.Secrets()["A"]) != "1" {
		t.Fatal("secret not delivered")
	}
	sw, err := s.Switch(ctx, "/nix/store/k", false, nil)
	if err != nil || !sw.NeedsReboot {
		t.Fatalf("switch: %v %v", sw, err)
	}
	if err := s.Freeze(ctx); err != nil {
		t.Fatal(err)
	}
	// Watchdog thaws and warns.
	select {
	case n := <-s.Notifications():
		if n.GetWarning().GetKind() != "freeze_timeout" {
			t.Fatalf("expected freeze_timeout, got %v", n)
		}
	case <-ctx.Done():
		t.Fatal("no watchdog warning")
	}
	if srv.Frozen() {
		t.Fatal("watchdog should have thawed")
	}
	if err := s.Shutdown(ctx, 5); err != nil {
		t.Fatal(err)
	}
	<-srv.ShutdownCalled()
	kinds := srv.Kinds()
	if kinds[0] != "Ping" || kinds[len(kinds)-1] != "Shutdown" {
		t.Fatalf("kinds: %v", kinds)
	}
}

// SetupProject waits for home-manager's first activation in the guest,
// which took past 30 s on a busy host and failed the boot (DECISIONS
// I-705). hostd waits a little longer than guestd's own 3-minute bound,
// so the guest's error, which says which step ran out, is what arrives.
func TestSetupProjectWaitsPastGuestdsBootDeadline(t *testing.T) {
	if SetupTimeout <= 3*time.Minute || SetupTimeout > 4*time.Minute {
		t.Fatalf("SetupTimeout %s, want just over guestd's 3-minute BootDeadline", SetupTimeout)
	}
	if SetupTimeout <= DefaultTimeout {
		t.Fatalf("SetupTimeout %s is not above DefaultTimeout %s", SetupTimeout, DefaultTimeout)
	}
}
