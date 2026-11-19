package buildlog

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/metrics"
)

// TestABuildLineFromAnotherHostIsDropped (DECISIONS I-1368): a BuildLog
// line counts only from the host the build went to. A host the operator
// does not own could otherwise write its own text into another project's
// build log, which the owner's CLI streams as Nix's output. Probe: with
// the host check removed this test fails with "a stranger's line reached
// the op".
func TestABuildLineFromAnotherHostIsDropped(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Metrics = metrics.NewNop()
	opID, host, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	s.Bind("cmd-1", opID, host)
	if s.AppendFrom(other, "cmd-1", 1, "evil") {
		t.Fatal("a stranger's line was taken")
	}
	s.mu.Lock()
	n := len(s.pending[opID])
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("a stranger's line reached the op")
	}
	if !s.AppendFrom(host, "cmd-1", 1, "building") {
		t.Fatal("the build's own host was refused")
	}
	s.mu.Lock()
	n = len(s.pending[opID])
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("pending lines %d, want 1", n)
	}
	if s.AppendFrom(host, "cmd-unknown", 1, "x") {
		t.Fatal("a line for an unbound command was taken")
	}
}
