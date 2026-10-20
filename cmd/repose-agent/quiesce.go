package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	holders, err := holdersOf(ctx, env, b, *project)
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
// cwd, an open file or a mapping in the project's live area.
func holdersOf(ctx context.Context, env *Env, b undoStore, project string) ([]undo.Holder, error) {
	hs, err := b.Holders(ctx, project)
	if err != nil {
		return nil, env.fromBackend(err, "read the project's processes")
	}
	return hs, nil
}

// holderLabels name the processes for a user who has to act on them.
// The pid is half of that: "claude" says nothing a user can do, and
// `kill -9 1234` is the whole of what a refusal asks for. A pid and a
// process name are allowed in a message; a path, an argv and an
// environment are not (docs/ops/OBSERVABILITY.md).
func holderLabels(hs []undo.Holder) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, holderLabel(h))
	}
	return out
}

func holderLabel(h undo.Holder) string {
	comm := h.Comm
	if comm == "" {
		// A process that exited between the two reads of /proc has no
		// name left, and an unnamed holder still holds the live area.
		comm = "a process"
	}
	return fmt.Sprintf("%s (pid %d)", comm, h.PID)
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
		busy = append(busy, holderLabel(h))
	}
	for _, h := range hs {
		if !stayed[h.PID] {
			gone = append(gone, holderLabel(h))
		}
	}
	return gone, busy, nil
}

// waitGone rechecks the live area until nothing holds it, the grace period
// runs out, or the call is cancelled.
func waitGone(ctx context.Context, env *Env, b undoStore, project string) ([]undo.Holder, error) {
	deadline := time.Now().Add(env.Grace)
	for {
		left, err := holdersOf(ctx, env, b, project)
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

// cmdHolders is the scan of one live area, printed as JSON. The agent
// runs it on itself through sudo (sudoScan): /proc hides another user's
// cwd, fds and mappings from the login user, so without it a root-owned
// writer in the live area was not refused but missed, and the rewind
// over it lost that writer's last writes.
func cmdHolders(env *Env, args []string) error {
	fs := flagSet(env, "holders")
	root := fs.String("root", "", "the live area to scan: an absolute path with no symlinks in it")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if *root == "" {
		return errf(codeInvalidArgument, "no live area given; pass --root")
	}
	if !filepath.IsAbs(*root) || filepath.Clean(*root) != *root {
		return errf(codeInvalidArgument, "--root is an absolute path with no symlinks in it")
	}
	hs, err := undo.HoldersOf(*root)
	if err != nil {
		return errf(codeInternal, "read the processes of the live area: %v", err)
	}
	out := wireHolders{Holders: make([]wireHolder, 0, len(hs))}
	for _, h := range hs {
		out.Holders = append(out.Holders, wireHolder{PID: h.PID, Comm: h.Comm})
	}
	return emit(env.Stdout, out)
}
