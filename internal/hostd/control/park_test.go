package control

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/guest"
)

// parkBackend is a fakeBackend that also parks, so the park routes exist.
type parkBackend struct {
	fakeBackend
	mu      sync.Mutex
	reasons []string
	unparks int
}

func (p *parkBackend) Park(_ context.Context, reason string) ([]guest.ParkResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reasons = append(p.reasons, reason)
	return []guest.ParkResult{{GuestID: "g1", OK: true, Outcome: "parked"}}, nil
}

func (p *parkBackend) Unpark(context.Context) ([]guest.ParkResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unparks++
	return nil, nil
}

func serveFor(t *testing.T, b Backend) (*Client, context.Context) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "hostd.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, sock, b) }()
	c := NewClient(sock)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := c.Status(ctx); err == nil {
			return c, ctx
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket never answered")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The socket answers the pre-I-1002 route names as well as the park ones,
// for one release, because host-conventions.md is this socket's contract.
// Both spellings must reach the same handler: a `hostd` binary from before
// the rename talking to a renamed daemon is the case this covers, and it
// happens during a switch.
func TestTheSocketStillAnswersTheOldFreezeRoutes(t *testing.T) {
	b := &parkBackend{}
	c, ctx := serveFor(t, b)

	var res []guest.ParkResult
	if err := c.do(ctx, "POST", "/freeze?reason=shutdown", &res); err != nil {
		t.Fatalf("POST /freeze: %v", err)
	}
	if len(res) != 1 || res[0].Outcome != "parked" {
		t.Fatalf("POST /freeze answered %+v, want the park handler's result", res)
	}
	if err := c.do(ctx, "POST", "/thaw", &res); err != nil {
		t.Fatalf("POST /thaw: %v", err)
	}

	// And the new names, so neither spelling is the only one that works.
	if _, err := c.Park(ctx, guest.ParkOperator); err != nil {
		t.Fatalf("POST /park: %v", err)
	}
	if _, err := c.Unpark(ctx); err != nil {
		t.Fatalf("POST /unpark: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if want := []string{"shutdown", guest.ParkOperator}; len(b.reasons) != 2 || b.reasons[0] != want[0] || b.reasons[1] != want[1] {
		t.Errorf("backend saw reasons %v, want %v", b.reasons, want)
	}
	if b.unparks != 2 {
		t.Errorf("backend saw %d unparks, want 2 (one per spelling)", b.unparks)
	}
}

// A backend that does not park registers none of the four routes, old
// names included: the routes exist only when the daemon has the feature.
func TestABackendThatDoesNotParkRegistersNoParkRoutes(t *testing.T) {
	c, ctx := serveFor(t, &fakeBackend{})
	for _, path := range []string{"/park", "/unpark", "/freeze", "/thaw"} {
		var res []guest.ParkResult
		if err := c.do(ctx, "POST", path, &res); err == nil {
			t.Errorf("POST %s answered on a backend with no park", path)
		}
	}
}
