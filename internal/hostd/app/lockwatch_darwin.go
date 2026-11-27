package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/vzpark"
)

// sessionReader reads the Mac user's session; set by adaptMac for the
// loop.
type sessionReader struct {
	read func(ctx context.Context) vzpark.Session
}

var macSession atomic.Value // *sessionReader

// resaveAfter is how long the session stays unlocked before the machines
// held by pause through a lock rest as save files again: a user who
// unlocks for a moment does not pay a save and a restore per lock.
const resaveAfter = time.Minute

// restManager is what lockWatch drives (guest.Manager).
type restManager interface {
	UnsaveRests(ctx context.Context) int
	SaveRests(ctx context.Context) int
	WakeRefusedAtLock(ctx context.Context) int
}

// lockWatch follows the Mac user's session from one second to the next
// (DECISIONS I-1682). VZ reads a save file only in the unlocked session,
// and for a few seconds after the screen locks (n=3 on the lab Mac:
// restored up to +7.6 s, refused from +11.4 s), so the moment it sees the
// lock every machine resting as a save is restored and rests by pause,
// reachable by the first ssh; once the session has been unlocked for
// resaveAfter they rest as saves again, giving their memory back while the
// user is at the Mac; and a wake the lock refused is done at the unlock.
type lockWatch struct {
	seen       bool
	unlocked   bool
	unlockedAt time.Time
	resaved    bool
}

func (w *lockWatch) step(ctx context.Context, s vzpark.Session, now time.Time, m restManager, log *slog.Logger) {
	open := s.Unlocked()
	was, seen := w.unlocked, w.seen
	w.seen, w.unlocked = true, open
	switch {
	case seen && was && !open:
		t0 := time.Now()
		n := m.UnsaveRests(ctx)
		if n > 0 {
			log.Info("screen locked: machines resting as saves restored; they rest paused", "event", "guest_resting", "guests", n, "duration_ms", time.Since(t0).Milliseconds())
		}
	case open && (!seen || !was):
		w.unlockedAt, w.resaved = now, false
		if seen {
			if n := m.WakeRefusedAtLock(ctx); n > 0 {
				log.Info("screen unlocked: machines whose wake the lock refused woken", "event", "guest_resting", "guests", n)
			}
		}
	}
	if open && !w.resaved && now.Sub(w.unlockedAt) >= resaveAfter {
		w.resaved = true
		if n := m.SaveRests(ctx); n > 0 {
			log.Info("machines resting paused rest as saves again", "event", "guest_resting", "guests", n)
		}
	}
}

var _ restManager = (*guest.Manager)(nil)
