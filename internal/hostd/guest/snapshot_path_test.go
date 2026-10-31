package guest

import (
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/state"
)

// A snapshot's blob path is the whole of its uniqueness, so two
// snapshots of one project that are close together must not share one.
// At milliseconds they did, and the second silently overwrote the
// first: a stop that snapshots followed at once by a manual snapshot
// left the api holding two rows against one blob, so retiring either
// took the other's bytes with it (I-910).
func TestBlobPathSeparatesSnapshotsInOneMillisecond(t *testing.T) {
	g := &state.Guest{UserID: "u1", ProjectID: "p1"}
	base := time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)
	first := BlobPath(g, base.Add(1500*time.Microsecond))
	second := BlobPath(g, base.Add(1900*time.Microsecond))
	if first == second {
		t.Fatalf("two snapshots 400us apart share the blob path %q", first)
	}
	for _, p := range []string{first, second} {
		if got := len(p); got == 0 {
			t.Fatal("empty blob path")
		}
	}
	// A guest with no user id still falls under its project alone.
	if got := BlobPath(&state.Guest{ProjectID: "p1"}, base); got[:3] != "p1/" {
		t.Fatalf("no user id: %q", got)
	}
	// The same instant is the same path, so a retry of one snapshot
	// overwrites its own bytes rather than leaving a second blob.
	// Through variables, because staticcheck reads the two calls as one
	// expression (SA4000) and the point here is that they are not: a
	// BlobPath made unique with a counter or a random suffix would pass
	// the collision test above and fail this one.
	again, andAgain := BlobPath(g, base), BlobPath(g, base)
	if again != andAgain {
		t.Fatalf("BlobPath is not a function of its arguments: %q then %q", again, andAgain)
	}
}
