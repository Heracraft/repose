package shell

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// Wait does NOT capture stderr, and that is deliberate. Capturing it
// inside the primitive raced the caller's own reader: with a reader
// merely slow -- which a loaded CI box makes of any reader -- the tail
// was empty in 10 of 10 runs, so the diagnosis vanished exactly when
// something else was reading, which is every caller (I-926).
//
// The correct order is the one os/exec requires: read the pipe to EOF,
// then Wait, then wrap. Only the caller knows when its read is done.
func TestWaitDoesNotRaceTheCallersReader(t *testing.T) {
	// The wrong order, run enough times that a race would show.
	for i := 0; i < 10; i++ {
		p, err := Exec{}.Start(context.Background(), nil, "sh", "-c", "echo boom >&2; exit 1")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _, _ = io.Copy(io.Discard, p.Stderr()) }()
		var ee *ExitError
		if !errors.As(p.Wait(), &ee) || ee.Result.ExitCode != 1 {
			t.Fatalf("run %d: want exit 1", i)
		}
		if len(ee.Result.Stderr) != 0 {
			t.Fatalf("run %d: Wait captured stderr; that capture is what raced the reader", i)
		}
	}
	// The right order: read to EOF first, then Wait. Deterministic.
	p, err := Exec{}.Start(context.Background(), nil, "sh", "-c", "echo 'device-mapper: reload ioctl failed' >&2; exit 1")
	if err != nil {
		t.Fatal(err)
	}
	out, rerr := io.ReadAll(p.Stderr())
	if rerr != nil {
		t.Fatal(rerr)
	}
	var ee *ExitError
	if !errors.As(p.Wait(), &ee) {
		t.Fatal("exit 1 was not an ExitError")
	}
	if got := Tail(out, 512); !strings.Contains(got, "device-mapper: reload ioctl failed") {
		t.Errorf("stderr read before Wait = %q", got)
	}
}
