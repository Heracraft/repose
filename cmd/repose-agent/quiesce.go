package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/undo"
)

// cmdQuiesce stops everything writing in the project, which is what makes a
// rewind safe: the swap cannot reach a process that keeps its cwd in the old
// subvolume, and that process's last writes go nowhere
// (docs/research/wrapper-undo.md).
//
// It reports process names. Never a path, never an argv: a log line or a
// message carries ids, states and process names and nothing a tenant typed
// (docs/ops/OBSERVABILITY.md).
func cmdQuiesce(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "quiesce")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if _, err := os.Stat(liveDir(env.Store, *project)); err != nil {
		return errf(codeNotFound, "the store has no project %s", *project)
	}
	b, err := env.backend()
	if err != nil {
		return err
	}

	stopped := make([]string, 0, 4)
	// The session is named after the slug, as the guest's session unit names
	// it (nix/guest/base/tmux.nix); "=" makes tmux match it exactly.
	target := "=" + *project
	if env.Tmux(ctx, "has-session", "-t", target) && env.Tmux(ctx, "kill-session", "-t", target) {
		stopped = append(stopped, "tmux:"+*project)
	}

	// Whatever is left in the live area once the session is gone, which is
	// the check Rewind itself refuses on.
	holders, err := holdersOf(b, *project)
	if err != nil {
		return err
	}
	gone, busy, err := stopAll(ctx, env, b, *project, holders)
	if err != nil {
		return err
	}
	stopped = append(stopped, gone...)
	return emit(env.Stdout, wireQuiesce{Stopped: stopped, Busy: busy})
}

// holdersOf is internal/undo's quiescence check: the processes with their
// cwd or an open file in the project's live area.
func holdersOf(b undoStore, project string) ([]undo.Holder, error) {
	hs, err := b.Holders(project)
	if err != nil {
		return nil, fromBackend(err, "read the project's processes")
	}
	return hs, nil
}

func holderNames(hs []undo.Holder) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Comm)
	}
	return out
}

// stopAll sends SIGTERM to each holder, then SIGKILL to whatever still holds
// the live area after the grace period. It returns the names that went and
// the names that stayed; one that answered neither signal is reported, not
// hidden, because a rewind over it loses writes.
func stopAll(ctx context.Context, env *Env, b undoStore, project string, hs []undo.Holder) (gone, busy []string, err error) {
	gone, busy = make([]string, 0, len(hs)), make([]string, 0)
	if len(hs) == 0 {
		return gone, busy, nil
	}
	// A signal that fails is not a failure here: the process has exited
	// already, which is what was wanted, and the rescan below decides.
	for _, h := range hs {
		_ = env.Signal(h.PID, syscall.SIGTERM)
	}
	left, err := waitGone(ctx, env, b, project)
	if err != nil {
		return nil, nil, err
	}
	if len(left) > 0 {
		for _, h := range left {
			_ = env.Signal(h.PID, syscall.SIGKILL) // as above: the rescan decides
		}
		if left, err = waitGone(ctx, env, b, project); err != nil {
			return nil, nil, err
		}
	}
	stayed := make(map[int]bool, len(left))
	for _, h := range left {
		stayed[h.PID] = true
		busy = append(busy, h.Comm)
	}
	for _, h := range hs {
		if !stayed[h.PID] {
			gone = append(gone, h.Comm)
		}
	}
	return gone, busy, nil
}

// waitGone rechecks the live area until nothing holds it, the grace period
// runs out, or the call is cancelled.
func waitGone(ctx context.Context, env *Env, b undoStore, project string) ([]undo.Holder, error) {
	deadline := time.Now().Add(env.Grace)
	for {
		left, err := holdersOf(b, project)
		if err != nil {
			return nil, err
		}
		if len(left) == 0 || !time.Now().Before(deadline) {
			return left, nil
		}
		select {
		case <-ctx.Done():
			return left, nil
		case <-time.After(pollInterval):
		}
	}
}
