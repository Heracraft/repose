// Package sample implements the Sample request of
// docs/interfaces/vsock-guestd.md: the guest signals and per-process-name
// figures that metering, the idle policy and abuse detection all read.
//
// Two rules shape this package. Process command lines and environments are
// never opened, so what leaves the guest is a process *name* and its numbers
// (DECISIONS R5-3); a test runs guestd under strace and asserts it. And a
// sample must cost under 20 ms, so everything that needs a fork lives in the
// Watcher's background refresh and Sample reads its cache.
package sample

import (
	"context"
	"log/slog"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/psi"
)

// Budget is the deadline for one sample. Past it the result is returned with
// partial set rather than made to wait (docs/workstreams/04-guestd.md §6).
const Budget = time.Second

// Handler answers Sample.
type Handler struct {
	paths   sysdep.Paths
	watcher *Watcher
	log     *slog.Logger
	uid     int
	now     func() time.Time
}

// NewHandler builds the Sample handler over an already-running Watcher.
func NewHandler(p sysdep.Paths, w *Watcher, log *slog.Logger, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	uid, _ := sysdep.DevIdentity()
	return &Handler{paths: p, watcher: w, log: log, uid: uid, now: now}
}

// Sample reads the signals and the process table. It never returns an error
// for a signal it could not read: a missing signal comes back as zero with
// partial set, because a sample that fails entirely loses the metering hour.
func (h *Handler) Sample(ctx context.Context) (*guestdv1.SampleResult, error) {
	start := h.now()
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()

	// The /proc walk comes first and whatever the deadline: it takes no
	// lock and never waits, while Signals waits for the watcher's lock,
	// which a refresh holds through its tree walks. Skipped after a
	// Signals that ran past the budget, it left ssh_sessions 0 beside
	// the cached tmux_clients, and hostd and the api do not read
	// partial, so status showed `sessions 0   tmux clients 1` on an
	// attached machine and a herdr machine, whose clients are only in
	// ssh_sessions, read as unused (cli-small-fixes review, 2026-10-07).
	procs, sessions, procErr := h.watcher.procs.read(h.uid)

	signals, fresh := h.watcher.Signals()
	partial := !fresh
	if procErr != nil {
		h.log.Warn("could not read the process table",
			"event", "sample", "error_code", sysdep.CodeOf(procErr))
		partial = true
	} else {
		signals.SshSessions = sessions
	}
	if ctx.Err() != nil {
		partial = true
	}

	// A kernel without PSI has no file; the counter stays 0 and the
	// dashboard shows no pressure line rather than a false zero being a
	// reason to mark the sample partial.
	pressure, _ := psi.ReadSomeTotal(h.paths.CPUPressure())
	memUsed := memUsed(h.paths.MemInfo())

	took := h.now().Sub(start)
	h.log.Debug("sample taken",
		"event", "sample", "duration_ms", took.Milliseconds(),
		"procs", len(procs), "partial", partial)
	return &guestdv1.SampleResult{Signals: signals, Procs: procs, Partial: partial, CpuPressureUsTotal: pressure, MemUsedBytes: memUsed}, nil
}

// WindowOfPane resolves a tmux pane id to its window name, for the hook socket
// when a hook did not say which window it came from.
func (h *Handler) WindowOfPane(ctx context.Context, pane string) (string, error) {
	return h.watcher.WindowOfTmuxPane(ctx, pane)
}
