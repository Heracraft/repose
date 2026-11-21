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

// The two boot phases wait on the guest's own boot: the registration of
// the closure's paths, and home-manager's first activation, which took
// past 30 s on a busy host and failed the boot (DECISIONS I-705). Both
// deadlines are the host's, and each sits below guestd's backstop, so
// the one that fires is hostd's and the error names the phase and the
// limit it used.
//
// I-705 had the opposite rule -- hostd waited 15 s longer than guestd's
// 3 minutes, so guestd's error arrived -- and the reversal is I-1408's
// (ENG-80). Under the old rule the deadline that decided every start was
// a constant inside the guest image: it could not be raised for slower
// hardware without rebuilding every guest, and the api could not say
// which of the two phases had run out. On a Mac appliance both phases
// run for minutes and five starts in a row failed in them.
func TestTheHostOwnsTheBootPhaseDeadlines(t *testing.T) {
	// guestd's backstop, which this package must stay under. It is
	// guestd.BootDeadline; hostd does not import the guest's package,
	// so the figure is restated with its source.
	const guestdBackstop = 20 * time.Minute
	for _, c := range []struct {
		name string
		d    time.Duration
	}{{"PathsTimeout", PathsTimeout}, {"SetupTimeout", SetupTimeout}} {
		if c.d >= guestdBackstop {
			t.Errorf("%s %s is not below guestd's %s backstop, so the guest's deadline fires first and the phase is unknowable", c.name, c.d, guestdBackstop)
		}
		if c.d <= DefaultTimeout {
			t.Errorf("%s %s is not above DefaultTimeout %s", c.name, c.d, DefaultTimeout)
		}
	}
	// The default is what guestd used to enforce, so a host that sets
	// neither flag keeps the deadline it had.
	if PathsTimeout != 3*time.Minute || SetupTimeout != 3*time.Minute {
		t.Errorf("defaults changed from the 3 minutes guestd enforced: paths %s, setup %s", PathsTimeout, SetupTimeout)
	}
	// A host may raise them, which is the whole point.
	got := Timeouts{Paths: 10 * time.Minute, Setup: 10 * time.Minute}.resolve()
	if got.paths != 10*time.Minute || got.setup != 10*time.Minute {
		t.Errorf("a host's own figures were not taken: %+v", got)
	}
	if zero := (Timeouts{}).resolve(); zero.paths != PathsTimeout || zero.setup != SetupTimeout || zero.def != DefaultTimeout || zero.sw != SwitchTimeout {
		t.Errorf("the zero value does not keep the defaults: %+v", zero)
	}
}

// The same, run: a SetupProject that answers after the default bound has
// passed still succeeds, because the call carries SetupTimeout. The bounds
// are scaled down so the test takes under a second.
func TestSetupProjectOutlivesTheDefaultTimeout(t *testing.T) {
	dir := t.TempDir()
	srv, err := fakeguestd.Listen(filepath.Join(dir, "guestd.sock"), fakeguestd.Options{Delay: map[string]time.Duration{"SetupProject": 300 * time.Millisecond, "SetPrincipals": 300 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := UnixDialer{}.Dial(ctx, Target{GuestID: "g", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	s := sess.(*session)
	s.t = timeouts{def: 100 * time.Millisecond, sw: time.Second, setup: 2 * time.Second}
	bg := context.Background() // no deadline of its own, as hostd's boot calls
	if err := s.SetupProject(bg, &guestdv1.SetupProject{ProjectSlug: "todo-app"}); err != nil {
		t.Fatalf("SetupProject past the default bound: %v", err)
	}
	// The control: a default-bound call with the same delay runs out.
	if err := s.SetPrincipals(bg, []string{"dev"}); err == nil {
		t.Fatal("SetPrincipals answered past its 100 ms bound without an error")
	}
}
