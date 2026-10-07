package cli

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const opConflictRetryWindow = 10 * time.Second
const opConflictRetryInterval = 250 * time.Millisecond

// retryOnOpConflict retries fn while it fails with a 409 conflict:
// DECISIONS I-70 says a secret or principal push to a running guest
// queues an update_secrets op the caller gets no id for, and
// stop/start/resize/destroy answer 409 conflict "an operation is in
// progress" while it runs, usually well under a second — the only
// conflict these four routes ever produce without a reason. A conflict
// that names one (start's `restore_unfinished`, DECISIONS I-461) will not
// pass by waiting. Any other error returns immediately.
func retryOnOpConflict(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(opConflictRetryWindow)
	for {
		err := fn()
		var apiErr *APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Code != "conflict" || apiErr.Detail["reason"] != nil {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opConflictRetryInterval):
		}
	}
}

// StartCmd implements `repose start [PROJECT]` (07-cli.md §5.6): start,
// wait, say it is running. Does not sync. A project in `error`, or
// running with a guestd that stopped answering, is restarted by the api
// (I-157) and the progress line says so.
func StartCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if project.State == "running" && !guestdDead(project) {
		_, _ = fmt.Fprintf(e.Out, "%s is already running.\n", project.Slug)
		return nil
	}
	pr := e.newProgress()
	defer pr.Fail()
	// requireProject read the project a moment ago: acted on without a
	// second read (as run does, I-223).
	if err := ensureRunningFrom(ctx, e, project, pr, true); err != nil {
		return err
	}
	pr.Fail()
	_, _ = fmt.Fprintf(e.Out, "%s is running (%s), ready in %s.\n", project.Slug, project.Class, fmtElapsed(pr.Total()))
	return nil
}

// guestdDead reports what the newest sample says about the project's
// guestd; false when the api did not say.
func guestdDead(p *Project) bool {
	if p.Signals == nil || p.Signals.GuestdOK == nil || *p.Signals.GuestdOK {
		return false
	}
	// A sample from before the guest's current run (taken while it was
	// being stopped) says nothing about the guestd running now; an api
	// before I-225 still sends it for up to a minute after every start.
	return p.StartedAt == nil || p.Signals.SampledAt == nil || !p.Signals.SampledAt.Before(*p.StartedAt)
}

// StopCmd implements `repose stop [PROJECT] [--no-snapshot]`.
func StopCmd(ctx context.Context, e *Env, projectArg string, snapshot bool) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if project.State == "stopped" {
		_, _ = fmt.Fprintf(e.Out, "%s is already stopped.\n", project.Slug)
		return nil
	}
	busy := busyAgents(project)
	pr := e.newProgress()
	defer pr.Fail()
	var opID string
	err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.StopProject(ctx, project.ID, snapshot)
		return err
	})
	if err != nil {
		return err
	}
	closeMaster(ctx, e, project.Slug)
	if snapshot {
		pr.Phase("Snapshotting and stopping "+project.Slug, "")
	} else {
		pr.Phase("Stopping "+project.Slug, "")
	}
	op, err := waitOpPhased(ctx, e, project, opID, pr, false)
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("stop", project.Slug, op.Error, fmt.Sprintf("`repose status %s` shows its state; `repose stop %s` tries again.", project.Slug, project.Slug))
	}
	p, err := e.Client.GetProject(ctx, project.ID)
	if err != nil {
		return err
	}
	// A stopped project costs nothing; its disk counts toward the plan's
	// disk total, which `repose ls` and the Billing page show. The line
	// says what the stop did: how long it took and the snapshot's size,
	// which is what that time went on (DECISIONS I-570). The snapshot's id
	// is for `repose snapshots`, where it is used.
	snapBytes := int64(-1)
	if snapshot && projectReason(p) == "" {
		if snaps, err := e.Client.ListSnapshots(ctx, project.ID); err == nil {
			if latest := newestSnapshot(snaps); latest != nil && latest.Reason == "stop" {
				snapBytes = latest.Bytes
			}
		}
	}
	if snapBytes >= 0 {
		_, _ = fmt.Fprintf(e.Out, "Stopped %s in %s with a %s snapshot.\n", p.Slug, fmtElapsed(pr.Total()), humanBytes(snapBytes))
	} else {
		_, _ = fmt.Fprintf(e.Out, "Stopped %s in %s.\n", p.Slug, fmtElapsed(pr.Total()))
	}
	if busy != "" {
		_, _ = fmt.Fprintf(e.Out, "Interrupted %s.\n", busy)
	}
	if reason := projectReason(p); reason != "" && p.LastError != nil {
		// I-158: a stop whose snapshot failed leaves the project stopped
		// with last_error set; say so rather than implying a snapshot.
		_, _ = fmt.Fprintf(e.ErrOut, "Note: %s.\n", reason)
	}
	return nil
}

// busyAgents names the agents the project's newest sample shows in the
// middle of a turn or waiting for an answer, as "claude (working) and
// codex-2 (needs input)", or "" when none is. A stop ends them
// (features/stop-start-destroy.md); an idle agent loses nothing, so it is
// not named. The line states what the stop did and names no command
// (DECISIONS I-500, I-484): `claude --resume` is on the docs page.
func busyAgents(p *Project) string {
	if p == nil || p.State != "running" || p.Signals == nil {
		return ""
	}
	var names []string
	for _, a := range p.Signals.Agents {
		var state string
		switch a.State {
		case "working":
			state = "working"
		case "needs_input":
			state = "needs input"
		default:
			continue
		}
		name := a.Window
		if name == "" {
			name = a.Agent
		}
		names = append(names, fmt.Sprintf("%s (%s)", name, state))
	}
	return joinNames(names)
}

// destroyPrompt is the confirmation the owner asked for (2026-09-23):
// a y/N question, since the final snapshot makes a destroy recoverable
// for 30 days and typing the name added nothing.
func destroyPrompt(slug string) string {
	return fmt.Sprintf("Destroy %s? A final snapshot is kept for 30 days. [y/N] ", slug)
}

// tempDestroyPrompt is the question for a temporary project, which keeps
// no snapshot (DECISIONS I-347).
func tempDestroyPrompt(slug string) string {
	return fmt.Sprintf("Destroy %s? It is temporary: no snapshot is kept and it cannot be restored. [y/N] ", slug)
}

// DestroyCmd implements `repose rm [PROJECT] [--yes] [--wait]`.
// By default it returns as soon as the api has accepted the destroy
// (DECISIONS I-166): the project reads `destroying` in `repose ls`
// from then on, and a failure shows there, in `repose status`, and as a
// destroy_failed notification (I-165). With wait it prints "Destroyed"
// only when the destroy op is done and the project is gone (api.md:
// DELETE answers 202 {op_id}, I-156); a failed op is reported with the
// project's actual state and the command to try again.
func DestroyCmd(ctx context.Context, e *Env, projectArg string, yes, wait bool, confirm func(prompt string) (bool, error)) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if !yes {
		if confirm == nil {
			return exitf(ExitUsage, "Destroying %s needs a confirmation; pass --yes to skip it.", project.Slug)
		}
		prompt := destroyPrompt(project.Slug)
		if project.ExpiresAt != nil {
			prompt = tempDestroyPrompt(project.Slug)
		}
		ok, err := confirm(prompt)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintf(e.Out, "Nothing destroyed.\n")
			return nil
		}
	}
	pr := e.newProgress()
	defer pr.Fail()
	var opID string
	if err := retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.DestroyProject(ctx, project.ID)
		return err
	}); err != nil {
		return err
	}
	closeMaster(ctx, e, project.Slug)
	// The laptop herdr's entry for the machine goes with it (I-510).
	forgetHerdrMachine(ctx, project.Slug)
	if forgetReposeRemote(gitRepoRoot(e.Cwd), project.Slug) {
		// The machine this checkout's `repose` remote pointed at is going
		// away (I-272); what was fetched from it stays.
		_, _ = fmt.Fprintln(e.ErrOut, "Removed the git remote repose; branches already fetched from it stay as repose/*.")
	}
	temporary := project.ExpiresAt != nil
	if !wait {
		pr.Fail()
		if temporary {
			// No snapshot, nothing to restore (I-347).
			_, _ = fmt.Fprintf(e.Out, "Destroying %s.\n", project.Slug)
			return nil
		}
		_, _ = fmt.Fprintf(e.Out, "Destroying %s. Its final snapshot is kept for 30 days.\n", project.Slug)
		return nil
	}
	pr.Phase("Destroying "+project.Slug, "")
	retry := fmt.Sprintf("`repose rm %s` tries again.", project.Slug)
	if opID != "" {
		op, err := waitOpPhased(ctx, e, project, opID, pr, false)
		if err != nil {
			return err
		}
		if op.State == "error" {
			pr.Fail()
			next := retry
			if p, err := e.Client.GetProject(ctx, project.ID); err == nil {
				next = fmt.Sprintf("%s is still there, %s. %s", p.Slug, stateWords(p.State), retry)
			}
			return e.opFailed("destroy", project.Slug, op.Error, next)
		}
	}
	// Gone means GET answers 404. An older api that sent no op_id is
	// waited on this way alone.
	started := time.Now()
	deadline := started.Add(opPollTimeout)
	if opID != "" {
		deadline = time.Now().Add(5 * time.Second) // the op is done; the row follows at once
	}
	for {
		p, err := e.Client.GetProject(ctx, project.ID)
		if isNotFound(err) || err == nil && p.State == "destroyed" {
			break
		}
		if err != nil {
			return err
		}
		if opID == "" && p.State == "error" {
			pr.Fail()
			return e.opFailed("destroy", project.Slug, OpError{Message: reasonOrDefault(p)}, fmt.Sprintf("%s is still there, in state error. %s", p.Slug, retry))
		}
		if time.Now().After(deadline) {
			pr.Fail()
			return exitf(ExitGeneric, "The destroy of %s finished, but the project is still listed (%s). `repose status %s` shows it; %s", p.Slug, stateWords(p.State), p.Slug, retry)
		}
		if err := sleepOrDone(ctx, pollDelay(started)); err != nil {
			return err
		}
	}
	pr.Fail()
	if temporary {
		_, _ = fmt.Fprintf(e.Out, "Destroyed %s in %s. It was temporary, so no snapshot was kept.\n", project.Slug, fmtElapsed(pr.Total()))
		return nil
	}
	snapLine := "Its final snapshot is kept for 30 days."
	if snaps, err := e.Client.ListSnapshots(ctx, project.ID); err == nil {
		if latest := newestSnapshot(snaps); latest != nil {
			until := latest.CreatedAt.AddDate(0, 0, 30)
			if latest.ExpiresAt != nil {
				until = *latest.ExpiresAt
			}
			snapLine = fmt.Sprintf("Its last snapshot is kept until %s.", until.Local().Format("2006-01-02"))
		}
	}
	_, _ = fmt.Fprintf(e.Out, "Destroyed %s in %s. %s\n", project.Slug, fmtElapsed(pr.Total()), snapLine)
	return nil
}

// newestSnapshot is the most recent of snaps, or nil. The api lists them
// newest first and the fake api oldest first; v0.1.5 took the last one,
// which on the real api was the oldest (DECISIONS I-166).
func newestSnapshot(snaps []Snapshot) *Snapshot {
	var best *Snapshot
	for i := range snaps {
		if best == nil || snaps[i].CreatedAt.After(best.CreatedAt) {
			best = &snaps[i]
		}
	}
	return best
}

func reasonOrDefault(p *Project) string {
	if r := projectReason(p); r != "" {
		return r
	}
	return "the destroy failed"
}

// stateWords is "in state error", "stopped", "running": how a sentence
// names a state.
func stateWords(state string) string {
	switch state {
	case "running", "stopped", "stopping", "starting", "building", "creating", "restoring", "destroying":
		return state
	default:
		return "in state " + state
	}
}

// ResizeCmd implements the hidden `repose resize` alias for POST /resize
// (07-cli.md §5.6, documented only in features/config.md).
func ResizeCmd(ctx context.Context, e *Env, projectArg string, bytes int64) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	pr := e.newProgress()
	defer pr.Fail()
	var opID string
	err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.ResizeProject(ctx, project.ID, bytes)
		return err
	})
	if err != nil {
		return err
	}
	pr.Phase("Resizing "+project.Slug, "")
	op, err := waitOpPhased(ctx, e, project, opID, pr, false)
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("resize", project.Slug, op.Error, "")
	}
	_, _ = fmt.Fprintf(e.Out, "Resized %s to %s.\n", project.Slug, humanBytes(bytes))
	return nil
}

// requireProject resolves the current project and reports the exact
// not-found/no-remote errors of §5.3 for every command that is not `run`.
func requireProject(ctx context.Context, e *Env, projectArg string) (*Project, error) {
	res, err := requireProjectRes(ctx, e, projectArg)
	if err != nil {
		return nil, err
	}
	return res.Project, nil
}

// requireProjectRes is requireProject with the rest of the resolution:
// the machine's other checkout this directory is, or PROJECT:CHECKOUT
// names (DECISIONS I-480).
func requireProjectRes(ctx context.Context, e *Env, projectArg string) (*ResolveResult, error) {
	res, err := resolveProject(ctx, e.Client, e.Dir, e.Cwd, e.resolveArg(projectArg), &e.Cache, defaultResolveDeps())
	if err != nil {
		return nil, err
	}
	if res.Project == nil {
		return nil, errNoProjectFoundFor(res.Remote, e.Command)
	}
	return res, nil
}

// requireRunningProject is requireProject plus the "guest not running"
// check that attach-like commands need (07-cli.md §6: exit 5), with the
// true state and the command that fixes it (I-153).
func requireRunningProject(ctx context.Context, e *Env, projectArg string) (*Project, error) {
	p, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return nil, err
	}
	if p.State != "running" {
		return nil, notRunningError(p)
	}
	return p, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}
