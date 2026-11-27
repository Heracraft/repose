package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/vzpark"
)

type fakeRests struct{ unsave, save, wake int }

func (f *fakeRests) UnsaveRests(context.Context) int       { f.unsave++; return 1 }
func (f *fakeRests) SaveRests(context.Context) int         { f.save++; return 1 }
func (f *fakeRests) WakeRefusedAtLock(context.Context) int { f.wake++; return 0 }

// DECISIONS I-1682: the lock is followed as it changes. At the lock every
// machine resting as a save is restored (VZ still can for a few seconds);
// at the unlock refused wakes are done; a minute into the unlock the
// machines rest as saves again; hostd starting on a locked Mac restores
// nothing (it did not see the lock happen and VZ can no longer read).
func TestLockWatchFollowsTheSession(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	open := vzpark.Session{LoggedIn: true, OnConsole: true}
	locked := vzpark.Session{LoggedIn: true, OnConsole: true, Locked: true}
	t0 := time.Unix(1_800_000_000, 0)
	var w lockWatch
	f := &fakeRests{}
	w.step(ctx, open, t0, f, log)
	w.step(ctx, open, t0.Add(30*time.Second), f, log)
	if f.save != 0 || f.unsave != 0 {
		t.Fatalf("before a minute unlocked: %+v", f)
	}
	w.step(ctx, open, t0.Add(61*time.Second), f, log)
	w.step(ctx, open, t0.Add(62*time.Second), f, log)
	if f.save != 1 {
		t.Fatalf("a minute into the unlock: saves %d, want 1", f.save)
	}
	w.step(ctx, locked, t0.Add(70*time.Second), f, log)
	w.step(ctx, locked, t0.Add(71*time.Second), f, log)
	if f.unsave != 1 {
		t.Fatalf("at the lock: unsaves %d, want 1", f.unsave)
	}
	w.step(ctx, open, t0.Add(100*time.Second), f, log)
	if f.wake != 1 || f.save != 1 {
		t.Fatalf("at the unlock: %+v", f)
	}
	w.step(ctx, open, t0.Add(161*time.Second), f, log)
	if f.save != 2 {
		t.Fatalf("a minute after the second unlock: saves %d", f.save)
	}

	g := &fakeRests{}
	var w2 lockWatch
	w2.step(ctx, locked, t0, g, log)
	if g.unsave != 0 {
		t.Fatal("hostd that started on a locked Mac restored saves it cannot read")
	}
}
