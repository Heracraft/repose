// Package trigger is the machine's half of a trigger
// (docs/features/triggers.md, DECISIONS I-1248..I-1262): the set the api
// pushes, the receiver the edge delivers to on 127.0.0.1:9227, the
// admission chain, one tmux window per run, the supervisor that ends a
// run, and the report back.
//
// Two doors reach the same Start: a delivery to the receiver and a
// RunTrigger command from the api. Everything after admission is the
// same for both, which is why there is one of each here and not two.
//
// A run is keyed by its id, in /run/repose/triggers/<run_id>, and never
// by its window: a trigger may be named `apply-2`, so `t-apply-2` is
// ambiguous, and `t-` is a window prefix a user may type. The registry
// is rebuilt from tmux at start, the way hostd rebuilds guests from
// systemd, so a guestd restart does not fail a run whose agent is still
// working (I-1256).
package trigger

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/agentwindow"
	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/triggerrun"
)

// ReceiverPort is the loopback port the edge delivers a trigger's events
// to (I-1250). It is platform-owned: never auto-forwarded to a laptop,
// and listening on loopback alone.
const ReceiverPort = triggerrun.ReceiverPort

// The limits of features/triggers.md §10, enforced here because this is
// where an event arrives.
const (
	// PayloadCap is the webhook body cap; the edge holds it too, and a
	// body that got past it is cut here.
	PayloadCap = 1 << 20
	// PromptCap is the prompt file's cap: it goes on the agent's own
	// command line (I-780), and this is far below any ARG_MAX.
	PromptCap = 64 << 10
	// GateTimeout bounds a gate.
	GateTimeout = 10 * time.Second
	// FiresPerHour is the most runs one trigger is admitted an hour.
	FiresPerHour = 60
	// RunsKept is how many of a trigger's run directories stay, which is
	// what the api keeps run rows.
	RunsKept = 20
	// DedupeWindow is how long a dedupe key is remembered.
	DedupeWindow = 24 * time.Hour
	// SummaryCap and ReasonCap bound the two tenant-content fields.
	SummaryCap = 1 << 10
	ReasonCap  = 200
	// SuperviseEvery is the supervisor's pass. The agent's own hooks say
	// when a turn ended, so this is the clock for the timeout and for a
	// window that went away, not a poll for the outcome.
	SuperviseEvery = 5 * time.Second
	// EndedKept is how long an ended run stays in the registry: long
	// enough that a command resent after a reconnect is answered from
	// it, and not so long that the registry grows for ever.
	EndedKept = 30 * time.Minute
	// StartGrace is how long a run may sit with no agent in its window
	// before it is failed: the wrapper loads the dev shell first, which
	// on a first run builds it.
	StartGrace = 30 * time.Minute
)

// Trigger is one of the project's triggers, as SetTriggers carries it.
type Trigger struct {
	Name        string
	Kind        string
	PromptPath  string
	Workdir     string
	Agent       string
	TimeoutS    uint32
	Concurrency uint32
	Gate        string
	Dedupe      string
	Enabled     bool
}

// Agents is what the runner needs to know about the machine's agents,
// which the sample watcher and the questions store already know.
type Agents interface {
	// WindowAgent is the agent's state in a window (`working`, `idle`,
	// `needs_input`, ...) and whether an agent is there at all.
	WindowAgent(window string) (state string, present bool)
	// QuestionOpen reports whether a question asked from that window is
	// still waiting for the owner. A run's clock stops while one is: a
	// `repose-ask` at 02:00 waits for a person, and the timeout must not
	// kill the primitive that makes an unattended run safe (I-1256).
	QuestionOpen(window string) bool
}

// Project is what the runner needs about the project, which guestd
// learns from SetupProject: the tmux session's name. The agent is not
// here — the api resolves a trigger's agent against the project's
// default before it sends the set, so the guest never guesses.
type Project struct {
	Slug string
}

// Runner owns the set, the runs and the receiver.
type Runner struct {
	paths sysdep.Paths
	run   sysdep.Runner
	log   *slog.Logger
	now   func() time.Time

	agents  Agents
	project func() Project
	emit    func(*guestdv1.TriggerRun)

	mu   sync.Mutex
	set  map[string]Trigger
	runs map[string]*run
	// fires is each trigger's recent start times, for the hourly cap.
	fires map[string][]time.Time
}

// run is one run in flight or just ended.
type run struct {
	ID      string
	Trigger string
	Window  string
	Source  string
	Started time.Time
	// Working is when the agent was last seen working, and Worked the
	// time it has spent working: the timeout counts that, not the wall
	// clock, so time waiting for an answer is not time working.
	Worked   time.Duration
	LastSeen time.Time
	// Ended is set once the run reported a terminal state, so the
	// supervisor stops looking at it.
	Ended bool
	// Summary and Link are the agent's own `--done` words, which the
	// hooks path hands over.
	Summary   string
	LinkURL   string
	LinkLabel string
	DedupeKey string
	Payload   int64
	TimeoutS  uint32
	// SawAgent is set once an agent was seen in the window, so a window
	// that never got one is told apart from one whose agent exited.
	SawAgent bool
	// State and Reason are an ended run's, so a resent command is
	// answered with what happened rather than run again.
	State  string
	Reason string
}

// New builds a runner. agents and project may not be nil; emit is how a
// run reaches hostd and the api.
func New(p sysdep.Paths, r sysdep.Runner, agents Agents, project func() Project, emit func(*guestdv1.TriggerRun), log *slog.Logger, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	return &Runner{paths: p, run: r, log: log.With("component", "guestd"), now: now,
		agents: agents, project: project, emit: emit,
		set: map[string]Trigger{}, runs: map[string]*run{}, fires: map[string][]time.Time{}}
}

// SetTriggers replaces the whole set, as WriteSecrets replaces the whole
// named set. It is kept in memory only: the api sends it again on every
// start, so a guest that lost it comes back with it (I-1248).
func (r *Runner) SetTriggers(set []Trigger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]Trigger, len(set))
	for _, t := range set {
		next[t.Name] = t
	}
	r.set = next
	r.log.Info("trigger set", "event", "trigger_run", "count", len(next))
}

// Get is one trigger of the set.
func (r *Runner) Get(name string) (Trigger, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.set[name]
	return t, ok
}

// Request is one fire, from either door.
type Request struct {
	Trigger string
	RunID   string
	Source  string
	// Payload and Headers are an event's own; both may be empty.
	Payload []byte
	Headers map[string]string
	// PayloadPath is a file in the machine to take the payload from:
	// `fire --payload` wrote it over SSH, or an earlier run's payload is
	// being replayed.
	PayloadPath string
	PayloadRun  string
	DryRun      bool
}

// Result is what a fire answers, which hostd passes to the api.
type Result struct {
	State  string
	Window string
	Reason string
}

// ErrUnknown is a trigger this guest does not know, which is an api that
// has not re-sent its set since a restart.
var ErrUnknown = errors.New("no such trigger")

// Start takes one fire through admission and, if it is admitted, opens
// the window. Every refusal is a state and a bounded reason, which the
// api turns into the run row the user reads.
func (r *Runner) Start(ctx context.Context, req Request) (Result, error) {
	t, ok := r.Get(req.Trigger)
	if !ok {
		return Result{}, ErrUnknown
	}
	if req.RunID == "" {
		req.RunID = uuid.Must(uuid.NewV7()).String()
	}
	// A repeat of a run already started is answered with that run's
	// state and starts no second agent: the command id is the run id, so
	// a re-send after a reconnect lands here (I-1252).
	r.mu.Lock()
	if existing, ok := r.runs[req.RunID]; ok {
		res := Result{State: triggerrun.StateWorking, Window: existing.Window}
		if existing.Ended {
			res.State, res.Reason = existing.State, existing.Reason
			if res.State == "" {
				res.State = triggerrun.StateDone
			}
		}
		r.mu.Unlock()
		return res, nil
	}
	r.mu.Unlock()

	// Admission, cheapest first (I-1254). A hand fire runs a trigger
	// that is off: `off` shuts the doors, and a fire is the owner asking.
	if !t.Enabled && req.Source != triggerrun.SourceHand {
		return r.skip(req, triggerrun.ReasonOff), nil
	}
	if key := r.dedupeKey(t, req); key != "" {
		seen, err := r.seenRecently(t.Name, key)
		if err != nil {
			r.log.Warn("dedupe store unreadable", "event", "trigger_run", "err", err.Error())
		}
		if seen {
			return r.skip(req, triggerrun.ReasonDuplicate), nil
		}
	}
	// The hour's cap, the concurrency limit, the window's name and the
	// run's place in the registry are taken under one lock: two
	// deliveries that arrive together would otherwise both pass a limit
	// of one and both take the name `t-apply`, and tmux would open two
	// windows with one name for the timeout to pick between.
	rn, reason := r.reserve(t, req)
	if reason != "" {
		return r.skip(req, reason), nil
	}
	// Anything that refuses the run from here gives the slot back.
	admitted := false
	defer func() {
		if !admitted {
			r.release(rn.ID)
		}
	}()

	dir := r.workdir(t)
	payload, headers, bytes, err := r.writePayload(t, req)
	if err != nil {
		r.log.Warn("payload not written", "event", "trigger_run", "run_id", req.RunID, "err", err.Error())
		return r.skip(req, triggerrun.ReasonError), nil
	}

	if t.Gate != "" {
		if reason := r.gate(ctx, t, req.RunID, dir, payload, headers); reason != "" {
			return r.skip(req, reason), nil
		}
	}
	if req.DryRun {
		return r.skip(req, triggerrun.ReasonDryRun), nil
	}

	prompt, reason := r.prompt(t, dir)
	if reason != "" {
		return r.fail(req, reason), nil
	}
	agent := t.Agent
	if agent == "" {
		// The api resolves this against the project's default, so an
		// empty one is an api older than the trigger set it sent.
		agent = "claude"
	}
	// pi reads a prompt that begins with @ as a file to include, so that
	// one pair has no argv form and a trigger must not fall back to the
	// typing path, which submits 1 in 10 (I-780).
	if _, ok := agentwindow.PromptArgv(agent, prompt); !ok {
		return r.fail(req, triggerrun.ReasonNoPermissionMode), nil
	}

	window := rn.Window
	cmd := agentwindow.NewWindow(r.project().Slug, window, agentwindow.Quote(dirPath(r.paths, dir)), agent, prompt, "")
	env := append(sysdep.DevEnv(r.paths, "dev"),
		"REPOSE_TRIGGER="+t.Name,
		"REPOSE_RUN="+req.RunID,
		"REPOSE_SOURCE="+req.Source,
	)
	if payload != nil {
		env = append(env, "REPOSE_PAYLOAD="+r.payloadPath(t.Name, req.RunID))
	}
	if headers != nil {
		env = append(env, "REPOSE_PAYLOAD_HEADERS="+r.headersPath(t.Name, req.RunID))
	}
	res, err := r.run.Run(ctx, sysdep.RunSpec{
		Argv: []string{"bash", "-lc", cmd}, User: "dev", Env: env, MaxOutput: 4 << 10,
	})
	if err != nil || res.ExitCode != 0 {
		r.log.Error("trigger window not opened", "event", "trigger_run", "run_id", req.RunID)
		return r.fail(req, triggerrun.ReasonNoAgent), nil
	}

	admitted = true
	now := r.now()
	r.mu.Lock()
	rn.Started, rn.LastSeen, rn.Payload, rn.TimeoutS = now, now, bytes, t.TimeoutS
	rn.DedupeKey = r.dedupeKey(t, req)
	r.fires[t.Name] = append(r.fires[t.Name], now)
	r.mu.Unlock()
	// The key is remembered only once the event is admitted: a gated or
	// dry run has paid for nothing, so a provider's retry of it is
	// gated again rather than read as a duplicate.
	if key := r.dedupeKey(t, req); key != "" {
		_ = r.remember(t.Name, key)
	}
	if err := r.writeMark(rn); err != nil {
		r.log.Warn("run mark not written", "event", "trigger_run", "run_id", rn.ID, "err", err.Error())
	}
	r.report(rn, triggerrun.StateWorking, "")
	r.gcRuns(t.Name)
	return Result{State: triggerrun.StateWorking, Window: window}, nil
}

func max32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// reserve takes the slot, the hour's place and the window's name under
// one lock, and puts the run in the registry before anything slow
// happens. A refusal from here on calls release.
func (r *Runner) reserve(t Trigger, req Request) (*run, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-time.Hour)
	kept := r.fires[t.Name][:0]
	for _, at := range r.fires[t.Name] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	r.fires[t.Name] = kept
	if len(kept) >= FiresPerHour {
		return nil, triggerrun.ReasonTooManyFires
	}
	working := 0
	taken := map[string]bool{}
	for _, other := range r.runs {
		if !other.Ended {
			taken[other.Window] = true
			if other.Trigger == t.Name {
				working++
			}
		}
	}
	if uint32(working) >= max32(t.Concurrency, 1) {
		return nil, triggerrun.ReasonConcurrency
	}
	rn := &run{ID: req.RunID, Trigger: t.Name, Source: req.Source,
		Window: freeWindow("t-"+t.Name, taken), Started: r.now(), LastSeen: r.now()}
	r.runs[rn.ID] = rn
	return rn, ""
}

// release gives back a reservation a refusal did not use.
func (r *Runner) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, id)
}

// freeWindow is `t-<trigger>`, and `t-<trigger>.2` for a second
// concurrent run: a dot, because a trigger may be named `apply-2` and
// `t-apply-2` would be both that trigger's window and the second run of
// `apply` (I-1256).
func freeWindow(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		if w := base + "." + strconv.Itoa(n); !taken[w] {
			return w
		}
	}
}

// skip and fail report a run that never started. Both are terminal, so
// the api writes one row and the user reads one line.
func (r *Runner) skip(req Request, reason string) Result {
	return r.ended(req, triggerrun.StateSkipped, reason)
}

func (r *Runner) fail(req Request, reason string) Result {
	return r.ended(req, triggerrun.StateFailed, reason)
}

// ended reports a run that never started and remembers it, so a command
// the api resends after a reconnect is answered from the stored result
// rather than put through admission again (CLAUDE.md's idempotency).
func (r *Runner) ended(req Request, state, reason string) Result {
	rn := &run{ID: req.RunID, Trigger: req.Trigger, Source: req.Source,
		Started: r.now(), LastSeen: r.now(), Ended: true, State: state, Reason: reason}
	if rn.ID != "" {
		r.mu.Lock()
		r.runs[rn.ID] = rn
		r.mu.Unlock()
	}
	r.report(rn, state, reason)
	return Result{State: state, Reason: reason}
}

// report sends one TriggerRun up. The summary and a gate's line are
// tenant content: they travel because the owner reads them, and no log
// line here carries either.
func (r *Runner) report(rn *run, state, reason string) {
	if r.emit == nil {
		return
	}
	n := &guestdv1.TriggerRun{
		RunId: rn.ID, Trigger: rn.Trigger, State: state, Reason: cut(reason, ReasonCap),
		Summary: cut(rn.Summary, SummaryCap), LinkUrl: rn.LinkURL, LinkLabel: rn.LinkLabel,
		Window: rn.Window, Source: rn.Source, DedupeKey: cut(rn.DedupeKey, 128),
		PayloadBytes: uint64(max64(rn.Payload, 0)),
	}
	if !rn.Started.IsZero() {
		n.StartedUnix = rn.Started.Unix()
	}
	if triggerrun.Terminal(state) {
		n.FinishedUnix = r.now().Unix()
	}
	r.emit(n)
	r.log.Info("trigger run", "event", "trigger_run", "run_id", rn.ID,
		"state", state, "reason", triggerrun.LogReason(reason), "source", rn.Source)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// workdir is the session's directory as the guest's shell spells it.
func (r *Runner) workdir(t Trigger) string {
	slug := r.project().Slug
	switch {
	case t.Workdir == "" || t.Workdir == "checkout":
		return "$HOME/" + slug
	case strings.HasPrefix(t.Workdir, "worktree-"):
		return "$HOME/" + slug + "-" + t.Workdir
	case strings.HasPrefix(t.Workdir, "~/"):
		return "$HOME/" + strings.TrimPrefix(t.Workdir, "~/")
	default:
		return t.Workdir
	}
}

// expandHome replaces a leading `~` and nothing else: a `~` in the
// middle of a path is a character, not a home.
func expandHome(p sysdep.Paths, path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(p.Home(), strings.TrimPrefix(path, "~/"))
	}
	return path
}

// dirPath turns the workdir's shell form into the path this process and
// a quoted shell word both read.
func dirPath(p sysdep.Paths, dir string) string {
	return strings.Replace(dir, "$HOME", p.Home(), 1)
}

// prompt reads the prompt file, or says why it cannot.
func (r *Runner) prompt(t Trigger, dir string) (string, string) {
	path := r.resolve(t, dir)
	st, err := os.Stat(path)
	if err != nil {
		return "", triggerrun.ReasonPromptMissing
	}
	if st.Size() > PromptCap {
		return "", triggerrun.ReasonPromptTooLarge
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", triggerrun.ReasonPromptMissing
	}
	prompt := strings.TrimSpace(string(b))
	if prompt == "" {
		return "", triggerrun.ReasonPromptMissing
	}
	return prompt, ""
}

// resolve turns the workdir's shell form and the prompt's relative path
// into a filesystem path this process can read.
func (r *Runner) resolve(t Trigger, dir string) string {
	d := dirPath(r.paths, dir)
	if filepath.IsAbs(t.PromptPath) {
		return t.PromptPath
	}
	return filepath.Join(d, t.PromptPath)
}

// triggerDir is where a trigger's own files live, on the volume and not
// a tmpfs: a dedupe window and a payload survive a stop, a reboot and a
// move to another host (I-1254).
func (r *Runner) triggerDir(name string) string {
	return filepath.Join(r.paths.Home(), ".repose", "triggers", name)
}

func (r *Runner) runDir(name, runID string) string {
	return filepath.Join(r.triggerDir(name), "runs", runID)
}

func (r *Runner) payloadPath(name, runID string) string {
	return filepath.Join(r.runDir(name, runID), "payload")
}

func (r *Runner) headersPath(name, runID string) string {
	return filepath.Join(r.runDir(name, runID), "headers.json")
}

// writePayload puts the event's bytes and headers in the run's own
// directory and answers what the gate and the session get.
func (r *Runner) writePayload(t Trigger, req Request) (payload []byte, headers map[string]string, size int64, err error) {
	payload = req.Payload
	switch {
	case req.PayloadPath != "":
		b, rerr := os.ReadFile(expandHome(r.paths, req.PayloadPath))
		if rerr != nil {
			return nil, nil, 0, rerr
		}
		payload = b
	case req.PayloadRun != "":
		b, rerr := os.ReadFile(r.payloadPath(t.Name, req.PayloadRun))
		if rerr != nil {
			return nil, nil, 0, rerr
		}
		payload = b
	}
	if len(payload) > PayloadCap {
		payload = payload[:PayloadCap]
	}
	if payload == nil && req.Headers == nil {
		return nil, nil, 0, nil
	}
	dir := r.runDir(t.Name, req.RunID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, 0, err
	}
	uid, gid := sysdep.DevIdentity()
	_ = os.Chown(dir, uid, gid)
	_ = os.Chown(filepath.Dir(dir), uid, gid)
	if payload != nil {
		if err := sysdep.WriteFileAtomic(r.payloadPath(t.Name, req.RunID), payload, 0o600, uid, gid); err != nil {
			return nil, nil, 0, err
		}
	}
	if req.Headers != nil {
		// Never Repose-Webhook-URL: it carries the route's token, and a
		// file an agent reads is no place for a credential that would let
		// anyone fire the trigger (I-1256).
		clean := make(map[string]string, len(req.Headers))
		for k, v := range req.Headers {
			if strings.EqualFold(k, "Repose-Webhook-URL") {
				continue
			}
			clean[strings.ToLower(k)] = v
		}
		b, err := json.Marshal(clean)
		if err != nil {
			return nil, nil, 0, err
		}
		if err := sysdep.WriteFileAtomic(r.headersPath(t.Name, req.RunID), b, 0o600, uid, gid); err != nil {
			return nil, nil, 0, err
		}
		headers = clean
	}
	return payload, headers, int64(len(payload)), nil
}

// gate runs the admission command: through the guest's shell, as dev, in
// the working directory, with the payload on stdin and the project's
// secrets in its environment. "" admits (I-1254).
func (r *Runner) gate(ctx context.Context, t Trigger, runID, dir string, payload []byte, headers map[string]string) string {
	gctx, cancel := context.WithTimeout(ctx, GateTimeout)
	defer cancel()
	env := append(sysdep.DevEnv(r.paths, "dev"), "REPOSE_TRIGGER="+t.Name)
	if payload != nil {
		env = append(env, "REPOSE_PAYLOAD="+r.payloadPath(t.Name, runID))
	}
	if headers != nil {
		env = append(env, "REPOSE_PAYLOAD_HEADERS="+r.headersPath(t.Name, runID))
	}
	res, err := r.run.Run(gctx, sysdep.RunSpec{
		Argv: []string{"bash", "-lc", "cd " + agentwindow.Quote(dirPath(r.paths, dir)) + " 2>/dev/null; " + t.Gate},
		User: "dev", Env: env, Stdin: payload, MaxOutput: 4 << 10,
	})
	switch {
	case gctx.Err() != nil:
		return triggerrun.ReasonGateTimeout
	case err != nil:
		return triggerrun.GateReason("the gate could not be run")
	case res.ExitCode != 0:
		// The command's own last line of stderr, which is the one piece
		// of free text a reason carries and is never logged.
		return triggerrun.GateReason(lastLine(res.Stderr, ReasonCap))
	}
	return ""
}

func lastLine(b []byte, cap int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	return cut(last, cap)
}

// dedupeKey reads the key a trigger dedupes on out of this event.
func (r *Runner) dedupeKey(t Trigger, req Request) string {
	kind, field, ok := strings.Cut(t.Dedupe, ":")
	if !ok || field == "" {
		return ""
	}
	switch kind {
	case "header":
		for k, v := range req.Headers {
			if strings.EqualFold(k, field) {
				return cut(v, 128)
			}
		}
	case "json":
		var body map[string]any
		if err := json.Unmarshal(req.Payload, &body); err != nil {
			return ""
		}
		switch v := body[field].(type) {
		case string:
			return cut(v, 128)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// seenRecently and remember are the dedupe window, kept as a file on the
// volume so it survives a stop, a reboot and a move to another host: on a
// tmpfs a provider's retry after a restart would pay twice (I-1254).
func (r *Runner) seenRecently(name, key string) (bool, error) {
	f, err := os.Open(filepath.Join(r.triggerDir(name), "seen"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	cutoff := r.now().Add(-DedupeWindow).Unix()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		at, k, ok := strings.Cut(sc.Text(), " ")
		if !ok || k != key {
			continue
		}
		if unix, err := strconv.ParseInt(at, 10, 64); err == nil && unix >= cutoff {
			return true, nil
		}
	}
	return false, sc.Err()
}

func (r *Runner) remember(name, key string) error {
	dir := r.triggerDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "seen")
	cutoff := r.now().Add(-DedupeWindow).Unix()
	var kept []string
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			at, _, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			if unix, err := strconv.ParseInt(at, 10, 64); err == nil && unix >= cutoff {
				kept = append(kept, line)
			}
		}
	}
	kept = append(kept, strconv.FormatInt(r.now().Unix(), 10)+" "+key)
	uid, gid := sysdep.DevIdentity()
	return sysdep.WriteFileAtomic(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600, uid, gid)
}

// writeMark records the run in /run/repose/triggers/<run_id>, which is
// what the reconcile reads back and what tells a trigger's window from a
// window a user named `t-something` (I-1256).
func (r *Runner) writeMark(rn *run) error {
	dir := filepath.Join(r.paths.RunDir(), "triggers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{
		"trigger": rn.Trigger, "window": rn.Window, "source": rn.Source,
		"started_unix": rn.Started.Unix(), "timeout_s": rn.TimeoutS,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, rn.ID), b, 0o600)
}

func (r *Runner) removeMark(id string) {
	_ = os.Remove(filepath.Join(r.paths.RunDir(), "triggers", id))
}

// gcRuns keeps the newest RunsKept run directories of a trigger, so the
// payloads a user can replay are the runs the api still lists (I-1262).
func (r *Runner) gcRuns(name string) {
	dir := filepath.Join(r.triggerDir(name), "runs")
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) <= RunsKept {
		return
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	// A run id is a UUIDv7, so its name sorts by time.
	sort.Strings(names)
	for _, old := range names[:len(names)-RunsKept] {
		_ = os.RemoveAll(filepath.Join(dir, old))
	}
}

// Windows is the window names of the project's tmux session.
func (r *Runner) windows(ctx context.Context) ([]string, error) {
	res, err := r.run.Run(ctx, sysdep.RunSpec{
		Argv: []string{"tmux", "list-windows", "-t", r.project().Slug, "-F", "#{window_name}"},
		User: "dev", Env: sysdep.DevEnv(r.paths, "dev"), MaxOutput: 16 << 10,
	})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("tmux list-windows exited %d", res.ExitCode)
	}
	var out []string
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// Reconcile rebuilds the runs from the marks and tmux, the way hostd
// rebuilds guests from systemd (CLAUDE.md, I-1256). A run whose window
// is alive stays working; only a run with no window ends, because
// marking a live run failed would let the next fire start a second agent
// on a checkout where the first is mid-side-effect.
func (r *Runner) Reconcile(ctx context.Context) {
	dir := filepath.Join(r.paths.RunDir(), "triggers")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	live := map[string]bool{}
	if ws, err := r.windows(ctx); err == nil {
		for _, w := range ws {
			live[w] = true
		}
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var mark struct {
			Trigger     string `json:"trigger"`
			Window      string `json:"window"`
			Source      string `json:"source"`
			StartedUnix int64  `json:"started_unix"`
			TimeoutS    uint32 `json:"timeout_s"`
		}
		if json.Unmarshal(b, &mark) != nil || mark.Window == "" {
			continue
		}
		rn := &run{ID: e.Name(), Trigger: mark.Trigger, Window: mark.Window, Source: mark.Source,
			Started: time.Unix(mark.StartedUnix, 0), LastSeen: r.now(), TimeoutS: mark.TimeoutS}
		if !live[mark.Window] {
			rn.Ended = true
			r.report(rn, triggerrun.StateFailed, triggerrun.ReasonGuestRestart)
			r.removeMark(rn.ID)
			continue
		}
		r.mu.Lock()
		r.runs[rn.ID] = rn
		r.mu.Unlock()
		// Announced again so an api that restarted learns of a run it is
		// holding a row for, as an open question is (I-1256).
		r.report(rn, triggerrun.StateWorking, "")
	}
}

// OnAgentEvent is an agent hook from one of the machine's windows. A
// `--done` in a trigger's window is that run's summary: the state is the
// machine's and the words are the agent's (I-688).
func (r *Runner) OnAgentEvent(window, kind, summary, linkURL, linkLabel string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rn := range r.runs {
		if rn.Window != window || rn.Ended {
			continue
		}
		switch kind {
		case "agent_done", "agent_message":
			if summary != "" {
				rn.Summary = cut(summary, SummaryCap)
				rn.LinkURL, rn.LinkLabel = linkURL, linkLabel
			}
		}
		return
	}
}

// Supervise ends the runs: a turn that finished, a window that went
// away, and a run past its timeout. It is the lifecycle owner the job
// agent hand-rolled as a 10-second loop.
func (r *Runner) Supervise(ctx context.Context) {
	t := time.NewTicker(SuperviseEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sweep(ctx)
		}
	}
}

// Sweep is one pass of the supervisor, which Supervise calls on its
// ticker and a test calls with a clock it controls.
func (r *Runner) Sweep(ctx context.Context) { r.sweep(ctx) }

// HandleForTest is the receiver's handler, so the answers the edge reads
// are checked without binding 9227 in a test.
func (r *Runner) HandleForTest(w http.ResponseWriter, req *http.Request) { r.handle(w, req) }

// sweep is one pass of the supervisor.
func (r *Runner) sweep(ctx context.Context) {
	live := map[string]bool{}
	if ws, err := r.windows(ctx); err == nil {
		for _, w := range ws {
			live[w] = true
		}
	} else {
		// No answer from tmux is not a reason to end every run: a run
		// ends when something says it did.
		return
	}
	type ending struct {
		rn     *run
		state  string
		reason string
	}
	var end []ending
	now := r.now()
	r.mu.Lock()
	for _, rn := range r.runs {
		if rn.Ended {
			continue
		}
		state, present := r.agents.WindowAgent(rn.Window)
		switch {
		case !live[rn.Window]:
			// The window is gone: the agent exited, or somebody closed
			// it. A run whose agent worked is done, with or without a
			// summary — the state is the machine's and the words are the
			// agent's (I-688). A window that never had an agent in it is
			// the failure.
			if rn.SawAgent {
				end = append(end, ending{rn, triggerrun.StateDone, ""})
			} else {
				end = append(end, ending{rn, triggerrun.StateFailed, triggerrun.ReasonNoAgent})
			}
		case present && state == "working":
			rn.SawAgent = true
			rn.Worked += now.Sub(rn.LastSeen)
		case present && (state == "idle" || state == "needs_input"):
			// The turn ended. `needs_input` is an agent waiting at a
			// prompt it asked for itself, which for an unattended run is
			// the end of what it was going to do.
			rn.SawAgent = true
			if r.agents.QuestionOpen(rn.Window) {
				// It is waiting for the owner, not finished: the clock
				// stops and the run stays open (I-1256).
				break
			}
			end = append(end, ending{rn, triggerrun.StateDone, ""})
		case !present && !rn.SawAgent && now.Sub(rn.Started) > StartGrace:
			// The window is there and never got an agent: the wrapper's
			// dev-shell load is the slow case and StartGrace covers it.
			end = append(end, ending{rn, triggerrun.StateFailed, triggerrun.ReasonNotLoggedIn})
		case !present && rn.SawAgent:
			end = append(end, ending{rn, triggerrun.StateDone, ""})
		}
		rn.LastSeen = now
		// The timeout counts time working, so an hour spent waiting for
		// an answer to `repose-ask` is not an hour of the run (I-1256).
		// Only for a run this pass has not already ended: a run that
		// finished past its timeout would otherwise be killed and
		// reported done in the same pass.
		ended := len(end) > 0 && end[len(end)-1].rn == rn
		if !ended && rn.TimeoutS > 0 && rn.Worked > time.Duration(rn.TimeoutS)*time.Second {
			end = append(end, ending{rn, triggerrun.StateTimeout, triggerrun.ReasonTimeout})
		}
	}
	for _, e := range end {
		e.rn.Ended, e.rn.State, e.rn.Reason = true, e.state, e.reason
	}
	// An ended run stays only long enough for a resent command to be
	// answered from it; after that the registry would grow for ever and
	// every pass would walk it.
	for id, rn := range r.runs {
		if rn.Ended && now.Sub(rn.LastSeen) > EndedKept {
			delete(r.runs, id)
		}
	}
	r.mu.Unlock()

	for _, e := range end {
		if e.state == triggerrun.StateTimeout {
			// That window and nothing else. Never the server: killing it
			// takes every agent on the machine with it.
			if _, err := r.run.Run(ctx, sysdep.RunSpec{
				Argv: []string{"tmux", "kill-window", "-t", r.project().Slug + ":" + e.rn.Window},
				User: "dev", Env: sysdep.DevEnv(r.paths, "dev"), MaxOutput: 4 << 10,
			}); err != nil {
				r.log.Warn("trigger window not killed", "event", "trigger_run", "run_id", e.rn.ID)
			}
		}
		r.report(e.rn, e.state, e.reason)
		r.removeMark(e.rn.ID)
	}
}

// Serve is the receiver the edge delivers to: loopback only, one path
// per trigger, and an answer the edge passes back to the provider
// (I-1250). It never waits for the agent: a provider's timeout is three
// seconds to ten, and an agent run is minutes.
func (r *Runner) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/t/", r.handle)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	// Both loopbacks: the edge opens its channel to 127.0.0.1 or ::1
	// depending on what the guest's sshd resolves, and the convention
	// doc names both (guest-conventions.md "Ports").
	ln, err := net.Listen("tcp", "localhost:"+strconv.Itoa(ReceiverPort))
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(ReceiverPort))
		if err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	r.log.Info("trigger receiver listening", "event", "listen", "listener", "triggers")
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// handle takes one delivery. The status is what the edge passes back to
// the sender, so it is the provider's own retry signal.
func (r *Runner) handle(w http.ResponseWriter, req *http.Request) {
	name := strings.TrimPrefix(req.URL.Path, "/t/")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if _, ok := r.Get(name); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body := make([]byte, 0, 1024)
	if req.Body != nil {
		b, err := readCapped(req.Body, PayloadCap)
		if err != nil {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	headers := map[string]string{}
	for k, v := range req.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	res, err := r.Start(req.Context(), Request{
		Trigger: name, Source: triggerrun.SourceRoute, Payload: body, Headers: headers,
	})
	switch {
	case errors.Is(err, ErrUnknown):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	case res.State == triggerrun.StateWorking:
		w.WriteHeader(http.StatusAccepted)
	case res.State == triggerrun.StateFailed:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"failed":` + strconv.Quote(triggerrun.LogReason(res.Reason)) + `}`))
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"skipped":` + strconv.Quote(triggerrun.LogReason(res.Reason)) + `}`))
	}
}

// readCapped reads at most n bytes and refuses a body longer than that,
// so a sender past the edge's own cap is answered rather than buffered.
// One byte past the cap is read on purpose: that is how a body of
// exactly n is told from one that is longer.
func readCapped(rc io.Reader, n int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(rc, int64(n)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > n {
		return nil, errors.New("payload too large")
	}
	return b, nil
}
