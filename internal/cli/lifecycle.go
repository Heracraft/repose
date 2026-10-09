package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// StopCmd stops one project without a question, as `repose stop
// PROJECT --yes` does.
func StopCmd(ctx context.Context, e *Env, projectArg string, snapshot bool) error {
	return StopProjectsCmd(ctx, e, StopOptions{Projects: []string{projectArg}, Snapshot: snapshot, Yes: true})
}

// StopOptions is `repose stop [PROJECT...] [--unused] [--no-snapshot] [--yes]`.
type StopOptions struct {
	Projects []string // as typed; none is this checkout's
	Idle     bool     // every running project the api reports idle (I-262)
	Snapshot bool
	Yes      bool
	// Confirm asks the question; nil when there is no terminal to ask on.
	Confirm func(prompt string) (bool, error)
	// Acted, when set, gets the projects the command resolved.
	Acted *[]*Project
	// asked: the caller's question named the busy agents already
	// (run's plan question, I-637), so no Ended line repeats them.
	asked bool
}

// stopResult is how one project's stop ended.
type stopResult struct {
	line     string // the stop line, on success
	note     string // what went wrong with a stop that stopped it (I-158)
	accepted bool   // the api took the stop, so it goes on without the CLI
	err      error
}

// StopProjectsCmd implements `repose stop`. A stop ends every process on
// the machine, so when the newest sample shows an agent in the middle of
// a turn or waiting for an answer it asks first, and without a terminal
// it refuses unless --yes (DECISIONS I-614, revising I-500). Several
// projects, or --unused, stop in parallel after one question (I-615). In a
// checkout whose `repose` remote is one of them, the agent's commits are
// fetched before the machine stops (I-615).
func StopProjectsCmd(ctx context.Context, e *Env, o StopOptions) error {
	started := time.Now()
	var projects []*Project
	var err error
	if o.Idle {
		projects, err = idleProjects(ctx, e)
		if err == nil && len(projects) == 0 {
			_, _ = fmt.Fprintln(e.Out, "No machine is unused.")
			return nil
		}
	} else {
		projects, err = resolveProjects(ctx, e, o.Projects)
	}
	if err != nil {
		return err
	}
	if o.Acted != nil {
		*o.Acted = projects
	}
	var running []*Project
	for _, p := range projects {
		if p.State == "stopped" {
			_, _ = fmt.Fprintf(e.Out, "%s is already stopped.\n", p.Slug)
			continue
		}
		running = append(running, p)
	}
	if len(running) == 0 {
		return nil
	}
	busy := make([]string, len(running))
	var clauses []string
	agents := 0
	for i, p := range running {
		names := busyAgentList(p)
		agents += len(names)
		busy[i] = joinNames(names)
		if busy[i] != "" {
			clauses = append(clauses, p.Slug+" has "+busy[i])
		}
	}
	asked := o.asked
	if agents > 0 && !o.Yes {
		clause := strings.Join(clauses, "; ") + "."
		if o.Confirm == nil {
			return exitf(ExitUsage, "%s No terminal to confirm stopping on; pass --yes.", clause)
		}
		ends := "it"
		if agents > 1 {
			ends = "them"
		}
		prompt := fmt.Sprintf("%s Stopping ends %s. Stop %s? [y/N] ", clause, ends, joinNames(slugsOf(running)))
		if err := confirmOr(o.Confirm, prompt, "Nothing stopped."); err != nil {
			return err
		}
		asked = true
	}
	if err := fetchBeforeStop(ctx, e, running); err != nil {
		return err
	}
	if len(running) == 1 {
		p := running[0]
		pr := e.newProgress()
		defer pr.Fail()
		r := stopOne(ctx, e, p, o.Snapshot, pr, started)
		pr.Fail()
		if r.err != nil {
			if r.accepted && interrupted(ctx, r.err) {
				return exitf(ExitInterrupted, "Interrupted. The stop of %s goes on.", p.Slug)
			}
			return r.err
		}
		_, _ = fmt.Fprintln(e.Out, r.line)
		if busy[0] != "" && !asked {
			_, _ = fmt.Fprintf(e.Out, "Ended %s.\n", busy[0])
		}
		if r.note != "" {
			_, _ = fmt.Fprintln(e.ErrOut, r.note)
		}
		return nil
	}
	pr := e.newProgress()
	defer pr.Fail()
	pr.Phase("Stopping "+joinNames(slugsOf(running)), "")
	results := make([]stopResult, len(running))
	var wg sync.WaitGroup
	for i, p := range running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = stopOne(ctx, e, p, o.Snapshot, nil, time.Now())
		}()
	}
	wg.Wait()
	pr.Fail()
	var goesOn []string
	failed, code := 0, ExitOK
	for i, r := range results {
		p := running[i]
		switch {
		case r.err == nil:
			_, _ = fmt.Fprintln(e.Out, r.line)
			if busy[i] != "" && !asked {
				_, _ = fmt.Fprintf(e.Out, "Ended %s on %s.\n", busy[i], p.Slug)
			}
			if r.note != "" {
				_, _ = fmt.Fprintln(e.ErrOut, r.note)
			}
		case interrupted(ctx, r.err):
			if r.accepted {
				goesOn = append(goesOn, p.Slug)
			}
		default:
			failed++
			code = exitCodeFor(r.err, e.ErrOut)
		}
	}
	if ctx.Err() != nil {
		switch len(goesOn) {
		case 0:
			return ctx.Err()
		case 1:
			return exitf(ExitInterrupted, "Interrupted. The stop of %s goes on.", goesOn[0])
		}
		return exitf(ExitInterrupted, "Interrupted. The stops of %s go on.", joinNames(goesOn))
	}
	switch {
	case failed == 1:
		return silent(code)
	case failed > 1:
		return silent(ExitGeneric)
	}
	return nil
}

// stopOne stops project and says how it went; pr nil draws nothing.
func stopOne(ctx context.Context, e *Env, project *Project, snapshot bool, pr *progress, started time.Time) stopResult {
	var r stopResult
	var opID string
	// What the stop leaves on the machine, read while it still answers
	// (I-634).
	left := probeBeforeStop(ctx, e, project)
	r.err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.StopProject(ctx, project.ID, snapshot)
		return err
	})
	if r.err != nil {
		return r
	}
	r.accepted = true
	closeMaster(ctx, e, project.Slug)
	if snapshot {
		pr.Phase("Snapshotting and stopping "+project.Slug, "")
	} else {
		pr.Phase("Stopping "+project.Slug, "")
	}
	op, err := waitOpPhased(ctx, e, project, opID, pr, false)
	if err != nil {
		r.err = err
		return r
	}
	pr.Fail()
	if op.State == "error" {
		r.err = e.opFailed("stop", project.Slug, op.Error, fmt.Sprintf("`repose status %s` shows its state; `repose stop %s` tries again.", project.Slug, project.Slug))
		return r
	}
	p, err := e.Client.GetProject(ctx, project.ID)
	if err != nil {
		r.err = err
		return r
	}
	// A stopped project costs nothing; what its disk holds counts toward
	// the plan's disk total (I-585), which the Billing page shows. The line
	// says what the stop did: how long it took and the snapshot's size,
	// which is what that time went on (DECISIONS I-570). The snapshot's id
	// is for `repose snapshots`, where it is used.
	// The stop op's result names the snapshot it recorded; an api that
	// does not say leaves the newest snapshot, when it is a stop's and the
	// project carries no error (a failed snapshot sets one, I-158).
	snapBytes := int64(-1)
	if snapshot {
		snapID, _ := op.Result["snapshot_id"].(string)
		if snapID != "" || projectReason(p) == "" {
			if snaps, err := e.Client.ListSnapshots(ctx, project.ID); err == nil {
				if s := stopSnapshot(snaps, snapID); s != nil {
					snapBytes = s.Bytes
				}
			}
		}
	}
	took := fmtElapsed(time.Since(started))
	if snapBytes >= 0 {
		r.line = fmt.Sprintf("Stopped %s in %s with a %s snapshot.", p.Slug, took, humanBytes(snapBytes))
	} else {
		r.line = fmt.Sprintf("Stopped %s in %s.", p.Slug, took)
	}
	r.line = withLeft(r.line, leftClause(left))
	saveStopLeft(e.Dir, p.ID, left, time.Now())
	if reason := projectReason(p); reason != "" && p.LastError != nil {
		// I-158: a stop whose snapshot failed leaves the project stopped
		// with last_error set; say so rather than implying a snapshot.
		r.note = fmt.Sprintf("Note: %s.", reason)
	}
	return r
}

// interrupted reports an error that is the command's Ctrl-C.
func interrupted(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, context.Canceled)
}

// resolveProjects resolves each PROJECT argument as requireProject does,
// all before anything is done, so a typo in the third name acts on none.
// No arguments is this checkout's project. A project named twice is
// acted on once.
func resolveProjects(ctx context.Context, e *Env, args []string) ([]*Project, error) {
	if len(args) == 0 {
		args = []string{""}
	}
	var out []*Project
	seen := map[string]bool{}
	for _, a := range args {
		p, err := requireProject(ctx, e, a)
		if err != nil {
			return nil, err
		}
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// idleProjects is every running project the api reports idle: running a
// day with no SSH session and no agent working (DECISIONS I-262), the
// projects `repose ls` prints the idle line for.
func idleProjects(ctx context.Context, e *Env) ([]*Project, error) {
	all, err := e.Client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Project
	for i := range all {
		if all[i].State == "running" && all[i].Idle != nil {
			out = append(out, &all[i])
		}
	}
	return out, nil
}

func slugsOf(projects []*Project) []string {
	s := make([]string, len(projects))
	for i, p := range projects {
		s[i] = p.Slug
	}
	return s
}

// busyAgents names the agents the project's newest sample shows in the
// middle of a turn or waiting for an answer, as "claude (working) and
// codex-2 (needs input)", or "" when none is. A stop ends them
// (features/stop-start-destroy.md), so stop asks first (I-614); an idle
// agent loses nothing, so it is not named. The sample can be up to a
// minute old.
func busyAgents(p *Project) string { return joinNames(busyAgentList(p)) }

func busyAgentList(p *Project) []string {
	if p == nil || p.State != "running" || p.Signals == nil {
		return nil
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
	return names
}

// destroyPrompt is the confirmation the owner asked for (2026-09-23): a
// y/N question, since the final snapshot makes a destroy recoverable for
// 30 days and typing the name added nothing. It names the busy agents a
// destroy ends (I-614), and a temporary project's lost snapshot comes
// before the question, since that one cannot be undone (DECISIONS I-347,
// I-614). Several projects are one question (I-615).
func destroyPrompt(projects []*Project) string {
	var b strings.Builder
	for _, p := range projects {
		if a := busyAgents(p); a != "" {
			fmt.Fprintf(&b, "%s has %s. ", p.Slug, a)
		}
	}
	var keep, temp []string
	for _, p := range projects {
		if p.ExpiresAt != nil {
			temp = append(temp, p.Slug)
		} else {
			keep = append(keep, p.Slug)
		}
	}
	all := joinNames(slugsOf(projects))
	if len(projects) == 1 {
		if len(temp) == 1 {
			fmt.Fprintf(&b, "%s is temporary: destroying it keeps no snapshot and it cannot be restored. Destroy %s? [y/N] ", all, all)
		} else {
			fmt.Fprintf(&b, "Destroy %s? A final snapshot is kept for 30 days. [y/N] ", all)
		}
		return b.String()
	}
	switch len(temp) {
	case 0:
	case 1:
		fmt.Fprintf(&b, "%s is temporary: it keeps no snapshot and cannot be restored. ", temp[0])
	default:
		fmt.Fprintf(&b, "%s are temporary: they keep no snapshot and cannot be restored. ", joinNames(temp))
	}
	fmt.Fprintf(&b, "Destroy %s? ", all)
	switch {
	case len(temp) == 0:
		b.WriteString("Final snapshots are kept for 30 days. ")
	case len(keep) == 1:
		fmt.Fprintf(&b, "%s's final snapshot is kept for 30 days. ", keep[0])
	case len(keep) > 1:
		fmt.Fprintf(&b, "The final snapshots of %s are kept for 30 days. ", joinNames(keep))
	}
	b.WriteString("[y/N] ")
	return b.String()
}

// DestroyCmd is `repose rm [PROJECT] [--yes] [--wait]` for one project.
func DestroyCmd(ctx context.Context, e *Env, projectArg string, yes, wait bool, confirm func(prompt string) (bool, error)) error {
	return DestroyProjectsCmd(ctx, e, []string{projectArg}, yes, wait, confirm)
}

// laptopFiles serializes destroyOne's edits of projects.json, the laptop
// herdr's config and the checkout's git config.
var laptopFiles sync.Mutex

// forgetDestroyedOnLaptop drops what the laptop keeps for a machine being
// destroyed, and returns the checkout's git remotes that went with it:
// its `repose` remote, and the one `repose fork` added for a copy
// (I-622). Several destroys run at once and git refuses a second writer
// of .git/config, so the files are edited one at a time (I-631).
func forgetDestroyedOnLaptop(ctx context.Context, e *Env, project *Project) []string {
	laptopFiles.Lock()
	defer laptopFiles.Unlock()
	// Folders linked to the machine forget it, so a plain run there makes
	// a new one instead of landing on this while it is destroyed.
	e.forgetProjectOnDisk(project.ID)
	// The laptop herdr's entry for the machine goes with it (I-510).
	forgetHerdrMachine(ctx, project.Slug)
	root := gitRepoRoot(e.Cwd)
	var removed []string
	if forgetReposeRemote(root, project.Slug) {
		removed = append(removed, "repose")
	}
	if forgetForkRemote(root, project.Slug) {
		removed = append(removed, project.Slug)
	}
	return removed
}

// destroyResult is how one project's destroy ended.
type destroyResult struct {
	out, errOut []string
	accepted    bool
	err         error
}

// DestroyProjectsCmd implements `repose rm [PROJECT...] [--yes] [--wait]`.
// By default it returns as soon as the api has accepted the destroy
// (DECISIONS I-166): the project reads `destroying` in `repose ls`
// from then on, and a failure shows there, in `repose status`, and as a
// destroy_failed notification (I-165). With wait it prints "Destroyed"
// only when the destroy op is done and the project is gone (api.md:
// DELETE answers 202 {op_id}, I-156); a failed op is reported with the
// project's actual state and the command to try again. Several projects
// are resolved first and asked about once (I-615); a no ends the command
// with exit 1 (I-614). One PROJECT:CHECKOUT removes that checkout, never
// the machine (I-618).
func DestroyProjectsCmd(ctx context.Context, e *Env, args []string, yes, wait bool, confirm func(prompt string) (bool, error)) error {
	for _, a := range args {
		if !strings.Contains(e.resolveArg(a), ":") {
			continue
		}
		if len(args) > 1 {
			return exitf(ExitUsage, "`repose rm %s` removes one checkout; pass it alone.", a)
		}
		return RemoveCheckoutCmd(ctx, e, a, yes, confirm)
	}
	projects, err := resolveProjects(ctx, e, args)
	if err != nil {
		return err
	}
	if !yes {
		if confirm == nil {
			return exitf(ExitUsage, "Destroying %s needs a confirmation; pass --yes to skip it.", joinNames(slugsOf(projects)))
		}
		if err := confirmOr(confirm, destroyPrompt(projects), "Nothing destroyed."); err != nil {
			return err
		}
	}
	// After a yes the question has said what is kept; with --yes the
	// line says it, with the date (A8).
	asked := !yes
	started := time.Now()
	if len(projects) == 1 {
		pr := e.newProgress()
		defer pr.Fail()
		r := destroyOne(ctx, e, projects[0], wait, asked, pr, started)
		pr.Fail()
		printLines(e, r)
		if r.err != nil && r.accepted && interrupted(ctx, r.err) {
			return exitf(ExitInterrupted, "Interrupted. The destroy of %s goes on.", projects[0].Slug)
		}
		return r.err
	}
	pr := e.newProgress()
	defer pr.Fail()
	if wait {
		pr.Phase("Destroying "+joinNames(slugsOf(projects)), "")
	}
	results := make([]destroyResult, len(projects))
	var wg sync.WaitGroup
	for i, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = destroyOne(ctx, e, p, wait, asked, nil, started)
		}()
	}
	wg.Wait()
	pr.Fail()
	var goesOn []string
	failed, code := 0, ExitOK
	for i, r := range results {
		printLines(e, r)
		switch {
		case r.err == nil:
		case interrupted(ctx, r.err):
			if r.accepted {
				goesOn = append(goesOn, projects[i].Slug)
			}
		default:
			failed++
			code = exitCodeFor(r.err, e.ErrOut)
		}
	}
	if ctx.Err() != nil {
		switch len(goesOn) {
		case 0:
			return ctx.Err()
		case 1:
			return exitf(ExitInterrupted, "Interrupted. The destroy of %s goes on.", goesOn[0])
		}
		return exitf(ExitInterrupted, "Interrupted. The destroys of %s go on.", joinNames(goesOn))
	}
	switch {
	case failed == 1:
		return silent(code)
	case failed > 1:
		return silent(ExitGeneric)
	}
	return nil
}

func printLines(e *Env, r destroyResult) {
	for _, l := range r.errOut {
		_, _ = fmt.Fprintln(e.ErrOut, l)
	}
	for _, l := range r.out {
		_, _ = fmt.Fprintln(e.Out, l)
	}
}

// destroyOne destroys project; pr nil draws nothing. asked is whether the
// user just answered a question that said what is kept.
func destroyOne(ctx context.Context, e *Env, project *Project, wait, asked bool, pr *progress, started time.Time) destroyResult {
	var r destroyResult
	var opID string
	if r.err = retryOnOpConflict(ctx, func() error {
		var err error
		opID, err = e.Client.DestroyProject(ctx, project.ID)
		return err
	}); r.err != nil {
		return r
	}
	r.accepted = true
	closeMaster(ctx, e, project.Slug)
	// What was fetched from a removed remote stays (I-272).
	for _, remote := range forgetDestroyedOnLaptop(ctx, e, project) {
		r.errOut = append(r.errOut, "Removed the git remote "+remote+".")
	}
	temporary := project.ExpiresAt != nil
	if !wait {
		pr.Fail()
		switch {
		case temporary || asked:
			// No snapshot, nothing to restore (I-347); or the question
			// just said what is kept (A8).
			r.out = append(r.out, fmt.Sprintf("Destroying %s.", project.Slug))
		default:
			// The snapshot is taken in the next minute or so; its 30 days
			// run from then.
			until := time.Now().AddDate(0, 0, 30).Local().Format("2006-01-02")
			r.out = append(r.out, fmt.Sprintf("Destroying %s. Its final snapshot is kept until %s.", project.Slug, until))
		}
		return r
	}
	pr.Phase("Destroying "+project.Slug, "")
	retry := fmt.Sprintf("`repose rm %s` tries again.", project.Slug)
	if opID != "" {
		op, err := waitOpPhased(ctx, e, project, opID, pr, false)
		if err != nil {
			r.err = err
			return r
		}
		if op.State == "error" {
			pr.Fail()
			next := retry
			if p, err := e.Client.GetProject(ctx, project.ID); err == nil {
				next = fmt.Sprintf("%s is still there, %s. %s", p.Slug, stateWords(p.State), retry)
			}
			r.err = e.opFailed("destroy", project.Slug, op.Error, next)
			return r
		}
	}
	// Gone means GET answers 404. An older api that sent no op_id is
	// waited on this way alone.
	polled := time.Now()
	deadline := polled.Add(opPollTimeout)
	if opID != "" {
		deadline = time.Now().Add(5 * time.Second) // the op is done; the row follows at once
	}
	for {
		p, err := e.Client.GetProject(ctx, project.ID)
		if isNotFound(err) || err == nil && p.State == "destroyed" {
			break
		}
		if err != nil {
			r.err = err
			return r
		}
		if opID == "" && p.State == "error" {
			pr.Fail()
			r.err = e.opFailed("destroy", project.Slug, OpError{Message: reasonOrDefault(p)}, fmt.Sprintf("%s is still there, in state error. %s", p.Slug, retry))
			return r
		}
		if time.Now().After(deadline) {
			pr.Fail()
			r.err = exitf(ExitGeneric, "The destroy of %s finished, but the project is still listed (%s). `repose status %s` shows it; %s", p.Slug, stateWords(p.State), p.Slug, retry)
			return r
		}
		if err := sleepOrDone(ctx, pollDelay(polled)); err != nil {
			r.err = err
			return r
		}
	}
	pr.Fail()
	took := fmtElapsed(time.Since(started))
	if temporary {
		r.out = append(r.out, fmt.Sprintf("Destroyed %s in %s. It was temporary, so no snapshot was kept.", project.Slug, took))
		return r
	}
	snapLine := "Its final snapshot is kept for 30 days."
	if snaps, err := e.Client.ListSnapshots(ctx, project.ID); err == nil {
		if latest := newestSnapshot(snaps); latest != nil {
			until := latest.CreatedAt.AddDate(0, 0, 30)
			if latest.ExpiresAt != nil {
				until = *latest.ExpiresAt
			}
			snapLine = fmt.Sprintf("Its final snapshot is kept until %s.", until.Local().Format("2006-01-02"))
		}
	}
	r.out = append(r.out, fmt.Sprintf("Destroyed %s in %s. %s", project.Slug, took, snapLine))
	return r
}

// newestSnapshot is the most recent of snaps, or nil. The api lists them
// newest first and the fake api oldest first; v0.1.5 took the last one,
// which on the real api was the oldest (DECISIONS I-166).
// stopSnapshot is the snapshot a stop took: the one with id, or, with
// no id, the newest when a stop took it.
func stopSnapshot(snaps []Snapshot, id string) *Snapshot {
	if id != "" {
		for i := range snaps {
			if snaps[i].ID == id {
				return &snaps[i]
			}
		}
		return nil
	}
	if s := newestSnapshot(snaps); s != nil && s.Reason == "stop" {
		return s
	}
	return nil
}

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
	// The api refuses a shrink with its field name; the CLI knows the
	// size already (I-613).
	if cur := project.VolumeBytes; cur > 0 {
		switch {
		case bytes == cur:
			_, _ = fmt.Fprintf(e.Out, "%s's disk is already %s.\n", project.Slug, diskSize(cur))
			return nil
		case bytes < cur:
			return exitf(ExitUsage, "%s's disk is %s and can only grow.", project.Slug, diskSize(cur))
		}
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
	_, _ = fmt.Fprintf(e.Out, "Resized %s's disk to %s.\n", project.Slug, diskSize(bytes))
	return nil
}

// requireProject resolves the current project and reports the exact
// not-found/no-remote errors of §5.3 for every command that is not `run`.
func requireProject(ctx context.Context, e *Env, projectArg string) (*Project, error) {
	if err := wholeMachineOnly(e, projectArg); err != nil {
		return nil, err
	}
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
		return nil, errNoProject(res, e.Command)
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
