package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// I-746: when the request's BootDeadline (3 min, I-705) runs out inside
// --check-validity, RegisterPaths says so and stops. Going on ran
// --load-db with a dead context, which failed at once and named
// --load-db for the time the validity pass had spent — the error the
// owner saw on the creates that failed at step 10.
func TestRegisterPathsNamesTheCommandThatRanOutOfTime(t *testing.T) {
	lib := storeName('a', "glibc")
	p, _ := overlayRoot(t, "/mnt-root", lib)
	run := sysdep.NewFakeRunner()
	run.Errs["nix-store"] = fmt.Errorf("run /run/current-system/sw/bin/nix-store: %w", context.DeadlineExceeded)
	h := New(p, run, 0, quietLog())

	err := h.RegisterPaths(context.Background(), registration("/nix/store/"+lib))
	if err == nil || !strings.Contains(err.Error(), "nix-store --check-validity") {
		t.Fatalf("err %v, want one naming nix-store --check-validity", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v does not carry the deadline", err)
	}
	if n := countCalls(run, "nix-store --load-db"); n != 0 {
		t.Fatalf("%d loads after the budget was gone, want none", n)
	}
}

// Every other failure of the validity pass stays skipped: the load still
// runs, because what the pass feeds (the load's skip, the view's roots) is
// best-effort.
func TestRegisterPathsLoadsWhenTheValidityPassFailsOtherwise(t *testing.T) {
	lib := storeName('a', "glibc")
	p, _ := overlayRoot(t, "/mnt-root", lib)
	run := sysdep.NewFakeRunner()
	run.Match["nix-store --check-validity"] = sysdep.RunResult{ExitCode: 1}
	h := New(p, run, 0, quietLog())

	if err := h.RegisterPaths(context.Background(), registration("/nix/store/"+lib)); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(run, "nix-store --load-db"); n != 1 {
		t.Fatalf("%d loads, want 1", n)
	}
}
