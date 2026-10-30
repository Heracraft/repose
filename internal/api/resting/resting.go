// Package resting decides which running machines rest and wakes one
// again (DECISIONS I-818, I-820).
//
// A machine that rests has its vCPUs stopped and its memory left resident
// on its host: every process, socket and open file survives, the state
// stays `running`, and a wake is a resume measured in milliseconds. The
// api decides, hostd executes, and either may wake (the brief's S1): two
// of the rule's terms live only in Postgres -- an op in flight (`ops`)
// and an open gateway session (`gateway_sessions`) -- and a third,
// keep_awake, is a column, so hostd cannot evaluate the rule from its own
// samples. hostd keeps the safeties only it can see and refuses a pause
// it must not take.
//
// A wake is deliberately not an op. The engine allows one open op per
// project and a wake is needed precisely when that op is open, so a wake
// modelled as its own op would wait for itself. It is sent straight to
// the host and its result awaited, the shape repose-ask's delivery
// already uses (internal/api/questions): hostmgr.Send plus an OnResult
// hook wired in app.go. Inside an op, the wake is a phase of the plan
// (ops.PhaseWake), which is not a second op either.
//
// Nothing here logs anything from inside a guest: ids, states, reasons
// and durations only.
package resting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// The rule of the workstream's §3.
const (
	// Samples is how many one-minute samples in a row must be quiet. At
	// one sample a minute this is the quiet window.
	Samples = 15
	// Window bounds how far back those samples may reach: 15 samples
	// spread over an hour are a sampler that skipped, not a quiet
	// quarter of an hour. It carries slack for a few minutes the sampler
	// did not send, because with no slack at all one dropped minute puts
	// the 15th sample outside the window and the machine waits another
	// Window before it can rest -- and a minute goes missing for every
	// guest on a host whenever the api cannot write that minute's
	// partition, or whenever hostd's own 60 s tick slips (it samples
	// guests in series). Five minutes of slack tolerates four such
	// minutes in a quarter of an hour.
	Window = (Samples + 5) * time.Minute
	// CPUNsPerSample is the host-measured CPU a quiet minute may use,
	// 6 s: 0.10 of a core. `.claude-wrapped` at its prompt burns about
	// 2 s a minute, so the threshold leaves room for a machine doing
	// nothing and none for one doing work.
	CPUNsPerSample = 6_000_000_000
	// NetBytesPerSample is the network a quiet minute may move, in and
	// out together: 64 KiB covers keepalives and leaves a bot polling an
	// API out of the rest.
	NetBytesPerSample = 64 << 10
	// UpFor is how long a machine must have been running before it may
	// rest: without it a machine whose samples began before its start
	// could rest inside its first minutes.
	UpFor = Samples * time.Minute
	// SessionWindow is how long a gateway_sessions row counts as an open
	// session. It is sessionTracker.Count's own window (http/server.go):
	// one definition of "a session is open", so the rule and
	// signals.gateway_sessions cannot disagree. A gateway that died
	// without its closes therefore holds its projects awake until its
	// rows age out, which is bounded by this window.
	SessionWindow = 24 * time.Hour
	// Feature is the Hello feature name a host must carry before the api
	// sends it a pause or a wake. A hostd older than I-818 answers a
	// command it does not know `invalid_argument`, which would fail the
	// op the wake is a phase of.
	Feature = "pause"
	// TickInterval is how often the rule runs.
	TickInterval = time.Minute
	// WakeTimeout is the api's own deadline for a wake: short, because
	// everything waiting on one is a user waiting -- an SSH dial, an
	// answer being delivered, an op's first guest-bound phase. It is
	// under the gateway's 10 s api client timeout on purpose, so a wake
	// the gateway asked for fails with this deadline's reason rather
	// than with its HTTP client's.
	WakeTimeout = 8 * time.Second
)

// Reasons a pause or a wake carries, for the host's log line and the
// metric label. lower_snake, never a sentence.
const (
	ReasonIdle      = "idle"
	ReasonConnect   = "connect"
	ReasonOp        = "op"
	ReasonAnswer    = "answer"
	ReasonKeepAwake = "keep_awake"
)

// Sender is hostmgr's Send.
type Sender interface {
	Send(ctx context.Context, hostID uuid.UUID, cmd *hostdv1.Command) error
}

// ErrNoHost is a wake for a project whose host is not connected.
var ErrNoHost = errors.New("resting: the project's host is not connected")

// ErrTimeout is a wake the host did not answer within WakeTimeout.
var ErrTimeout = errors.New("resting: the host did not answer the wake")

// Service runs the rule and serves wakes.
type Service struct {
	pool *db.Pool
	send Sender
	log  *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time

	mu sync.Mutex
	// waiting maps a wake's command_id to the channel its result lands
	// on; pauses maps a pause's command_id to the project it was for.
	waiting map[uuid.UUID]chan *hostdv1.Result
	pauses  map[uuid.UUID]pauseRec
	// flight shares one in-flight wake per project, so an editor opening
	// several connections at once is one wake.
	flight map[uuid.UUID]*wake
	// lastFail is the most recent failed wake per project, so a caller
	// that retries does not send another until WakeFailCooldown has
	// passed (I-914). flight covers wakes that overlap; this covers the
	// ones that follow each other, which is what a retrying ssh client
	// produces.
	lastFail map[uuid.UUID]failedWake
	// CountWake, when set, counts one wake by its reason and result
	// (`woken`, `awake`, `failed`). It lives here rather than at the one
	// HTTP route because the api asks for a wake from four places -- the
	// gateway's route, an op's wake phase, a question being delivered and
	// the keep_awake PATCH -- and the failures hostd never sees are
	// exactly the ones ApiWakeFailed exists for.
	CountWake func(reason, result string)
	// CountCandidates, when set, is given each tick's counts: the
	// machines paused and the machines the rule picked whose host does
	// not take pauses (DECISIONS I-844).
	CountCandidates func(paused, would int)

	// lastSent and lastWould are the previous tick's counts, so the log
	// line is written when the picture changes rather than every minute.
	lastSent, lastWould int
}

type pauseRec struct {
	project uuid.UUID
	at      time.Time
}

// wake is one shared wake: every caller waits on done and reads res/err.
type wake struct {
	done chan struct{}
	res  *hostdv1.WakeResult
	err  error
}

// New builds the service.
func New(pool *db.Pool, send Sender, log *slog.Logger) *Service {
	return &Service{pool: pool, send: send, log: log.With("component", "api"), Now: time.Now,
		waiting: map[uuid.UUID]chan *hostdv1.Result{}, pauses: map[uuid.UUID]pauseRec{}, flight: map[uuid.UUID]*wake{},
		lastFail: map[uuid.UUID]failedWake{}}
}

// Run evaluates the rule every TickInterval on the one replica that holds
// the lock, until ctx ends. It is its own lock and its own tick: the
// hourly jobs' lock is gated on the first five minutes of the hour, which
// would run this rule twelve times a day.
func (s *Service) Run(ctx context.Context) {
	for ctx.Err() == nil {
		release, ok, err := db.TryLock(ctx, s.pool, db.LockResting)
		if err != nil {
			s.log.Error("resting lock", "event", "resting_lock_fail", "err", err.Error())
		}
		if ok {
			t := time.NewTicker(TickInterval)
			for {
				if n, err := s.Once(ctx); err != nil && ctx.Err() == nil {
					s.log.Error("resting pass", "event", "guest_resting_fail", "err", err.Error())
				} else if n > 0 {
					s.log.Info("machines asked to rest", "event", "guest_resting", "count", n)
				}
				select {
				case <-ctx.Done():
					t.Stop()
					release()
					return
				case <-t.C:
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// Candidate is a project the rule says may rest.
type Candidate struct {
	ProjectID uuid.UUID
	HostID    uuid.UUID
	GuestID   uuid.UUID
	// HostPauses is whether this project's host named the `pause`
	// feature. False means the rule picked it and nothing will be sent:
	// it is counted, and that count is the only way to know what the rule
	// would do to a fleet before it does it (DECISIONS I-844).
	HostPauses bool
}

// quietSQL is one sample that counts toward a rest: nobody and no agent
// at work (idle.UsedSQL, the same text the unused notice reads), under
// the CPU and network thresholds, and not already resting.
const quietSQL = `(not ` + idle.UsedSQL + ` and cpu_ns < $5 and net_rx + net_tx < $6 and not resting)`

// candidatesSQL is the whole rule in one read: the row's own terms, the
// op, session and question terms that exist only here, and the last
// Samples samples of each candidate.
//
// An open `repose-ask` question holds a machine awake (DECISIONS I-822).
// A question is stored with a wall-clock expiry inside the guest
// (internal/guestd/questions), and the guest's clock keeps running while
// it rests -- measured on host-stg-01 across a 35 minute pause -- while
// the sweep that expires questions cannot, because the vCPUs are
// stopped. So a machine that rested an hour comes back with every open
// question already past its expiry, and the first sweep after the wake
// closes them: the agent waiting on one is told nobody answered. The
// agent is in `needs_input`, which the rule otherwise lets rest, so this
// term is what makes that safe.
const candidatesSQL = `with cand as (
	select p.id, p.host_id, p.guest_id, coalesce($3 = any(h.features), false) as pauses
	  from projects p join hosts h on h.id = p.host_id
	 where p.state = 'running' and p.destroyed_at is null and not p.host_unreachable
	   and not p.keep_awake and p.resting_since is null and p.guest_id is not null
	   and p.started_at is not null and p.started_at <= $2
	   and h.state = 'ready'
	   and not exists (select 1 from ops o where o.project_id = p.id and o.state in ('pending','running'))
	   and not exists (select 1 from gateway_sessions gs where gs.project_id = p.id and gs.opened_at > $4)
	   and not exists (select 1 from questions q where q.project_id = p.id and q.state = 'pending'
	                     and q.expires_at > $1)
), last as (
	select c.id, m.ts, ` + quietSQL + ` as quiet,
	       row_number() over (partition by c.id order by m.ts desc) as rn
	  from cand c join meter_samples m on m.project_id = c.id and m.ts >= $7 and m.ts <= $1
)
select c.id, c.host_id, c.guest_id, c.pauses from cand c join last l on l.id = c.id and l.rn <= $8::bigint
 group by c.id, c.host_id, c.guest_id, c.pauses
having count(*) = $8::bigint and min(l.ts) >= $9 and max(l.ts) >= $10
   and count(*) filter (where l.quiet) = $8::bigint`

// Candidates reads the rule for EVERY running project, and says of each
// whether its host takes pauses (DECISIONS I-844). The feature gates the
// pause, not the rule: a host with `pauseIdle` off yields candidates that
// are counted and sent nowhere, which is the only way to learn what this
// rule would do to a fleet before it does it. The gate used to sit in the
// WHERE clause, so a host with the switch off produced no rows and the
// days before a release said nothing at all.
//
// Two terms bound how old the evidence may be, and the tighter one is
// the oldest-sample term, not the freshness one: with samples a minute
// apart the 15th is 14 minutes behind the newest, so `min(ts) >= now -
// Window` already requires the NEWEST to be within Window - 14m. That is
// the real freshness budget, and it is what the slack in Window buys.
// The `max(ts) >= now - idle.Fresh` term is kept as the floor it is named
// for -- a host that stopped reporting says nothing about who is attached
// -- and it binds only if Window ever grows past idle.Fresh + 14m.
// Every way this arithmetic can be wrong returns FEWER candidates: a
// rest delayed, never a pause on a machine somebody is using.
func (s *Service) Candidates(ctx context.Context, now time.Time) ([]Candidate, error) {
	rows, err := s.pool.Query(ctx, candidatesSQL,
		now, now.Add(-UpFor), Feature, now.Add(-SessionWindow),
		int64(CPUNsPerSample), int64(NetBytesPerSample),
		now.Add(-2*Window), Samples, now.Add(-Window), now.Add(-idle.Fresh))
	if err != nil {
		return nil, fmt.Errorf("read the rest rule: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ProjectID, &c.HostID, &c.GuestID, &c.HostPauses); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Once sends one pause per project the rule passes and returns how many
// it sent. A pause is a request: hostd refuses one it must not take (a
// command in flight for that guest, a host going down, a user timer due)
// and says why, and the next tick asks again.
func (s *Service) Once(ctx context.Context) (int, error) {
	now := s.Now()
	s.prune(now)
	cs, err := s.Candidates(ctx, now)
	if err != nil {
		return 0, err
	}
	n, would := 0, 0
	for _, c := range cs {
		if !c.HostPauses {
			// The rule picked it and its host does not take pauses. This
			// is the dry run (DECISIONS I-844): counted, logged, and
			// nothing is sent. It is what the days before a release are
			// for -- the rule's own judgement on a real fleet, with no
			// machine touched.
			would++
			continue
		}
		if err := s.sendPause(ctx, c, now); err != nil {
			continue // not connected: the rule holds next minute and asks again
		}
		n++
	}
	s.report(n, would, len(cs))
	return n, nil
}

// report publishes what this tick found. The counters are per tick, so a
// counter of candidate-minutes integrates over days into the machine-hours
// a fleet would have rested, which is the figure the proposal's section 5
// estimated from three accounts and nothing has measured since
// (DECISIONS I-844).
func (s *Service) report(sent, would, total int) {
	if s.CountCandidates != nil {
		s.CountCandidates(sent, would)
	}
	// One line when the set changes, not one a minute: at a minute a tick
	// an unchanging fleet would write 1,440 identical lines a day.
	if sent == s.lastSent && would == s.lastWould {
		return
	}
	s.lastSent, s.lastWould = sent, would
	s.log.Info("the rest rule picked machines", "event", "resting_rule",
		"paused", sent, "would_rest", would, "candidates", total)
}

// sendPause sends one pause and remembers its command_id, so the result
// can be read against the project it was for.
func (s *Service) sendPause(ctx context.Context, c Candidate, now time.Time) error {
	cmdID := store.NewID()
	s.mu.Lock()
	s.pauses[cmdID] = pauseRec{project: c.ProjectID, at: now}
	s.mu.Unlock()
	cmd := &hostdv1.Command{CommandId: cmdID.String(), Cmd: &hostdv1.Command_PauseGuest{
		PauseGuest: &hostdv1.PauseGuest{GuestId: c.GuestID.String(), Reason: ReasonIdle}}}
	if err := s.send.Send(ctx, c.HostID, cmd); err != nil {
		s.mu.Lock()
		delete(s.pauses, cmdID)
		s.mu.Unlock()
		return err
	}
	return nil
}

// prune drops the records of pauses whose result never came.
func (s *Service) prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.pauses {
		if now.Sub(p.at) > 10*time.Minute {
			delete(s.pauses, id)
		}
	}
}

// Wake resumes a project's guest and waits for the host's answer, under
// WakeTimeout or the caller's deadline, whichever is sooner. It is
// idempotent and safe to ask for on a machine that is already awake: the
// answer then says so (was_resting false) and costs one round trip, which
// is what makes the gateway's 5-second route cache harmless in both
// directions.
//
// reason is one of the Reason constants, for the host's log line.
func (s *Service) Wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error) {
	res, err := s.wakeShared(ctx, projectID, reason)
	if s.CountWake != nil {
		switch {
		case err != nil:
			s.CountWake(reason, "failed")
		case res.GetWasResting():
			s.CountWake(reason, "woken")
		default:
			s.CountWake(reason, "awake")
		}
	}
	return res, err
}

// WakeFailCooldown is how long a failed wake stands for the project
// before another is sent (DECISIONS I-914).
//
// A wake that fails is almost never fixed by sending the same command
// again a second later, and sending it again can be what keeps it
// broken: on 2026-10-29 a guest's Cloud Hypervisor had accepted its cap
// of API connections and reaped none, so every wake got `503 Too many
// open connections` -- and every retry took another slot at a cap that
// never drains. One `repose attach` fires an ssh connection every two
// seconds for a minute, and the gateway asks for a wake on each, so a
// single user command sent about thirty wakes into a guest that could
// not take them.
//
// It is deliberately shorter than the ssh wait a client gives up after,
// so a wake that fails once still gets several real attempts within one
// attach; it only stops them being back to back.
const WakeFailCooldown = 10 * time.Second

// failedWake is a wake that failed, and when.
type failedWake struct {
	at  time.Time
	err error
}

// wakeShared is Wake without the counting: one in-flight wake per
// project, and no new one while a recent failure still stands.
func (s *Service) wakeShared(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error) {
	s.mu.Lock()
	if w, ok := s.flight[projectID]; ok {
		s.mu.Unlock()
		select {
		case <-w.done:
			return w.res, w.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f, ok := s.lastFail[projectID]; ok {
		if s.Now().Sub(f.at) < WakeFailCooldown {
			s.mu.Unlock()
			return nil, f.err
		}
		delete(s.lastFail, projectID)
	}
	w := &wake{done: make(chan struct{})}
	s.flight[projectID] = w
	s.mu.Unlock()
	w.res, w.err = s.wake(ctx, projectID, reason)
	close(w.done)
	s.mu.Lock()
	delete(s.flight, projectID)
	// A wake that reached the guest clears the mark; ErrNoHost does not
	// set one, since the host coming back is exactly the retry that
	// should go straight through.
	switch {
	case w.err == nil:
		delete(s.lastFail, projectID)
	case !errors.Is(w.err, ErrNoHost):
		s.lastFail[projectID] = failedWake{at: s.Now(), err: w.err}
	}
	s.mu.Unlock()
	return w.res, w.err
}

func (s *Service) wake(ctx context.Context, projectID uuid.UUID, reason string) (*hostdv1.WakeResult, error) {
	var hostID, guestID *uuid.UUID
	var state string
	var since *time.Time
	var pauses bool
	err := s.pool.QueryRow(ctx, `select p.host_id, p.guest_id, p.state, p.resting_since, coalesce($2 = any(h.features), false)
		from projects p left join hosts h on h.id = p.host_id where p.id = $1`, projectID, Feature).
		Scan(&hostID, &guestID, &state, &since, &pauses)
	if err != nil {
		return nil, err
	}
	// Nothing to wake: a machine that is not running, or has no guest.
	if state != "running" || hostID == nil || guestID == nil {
		return &hostdv1.WakeResult{}, nil
	}
	// The `pause` feature gates the PAUSE, never the wake. A host that
	// stops naming it -- `repose.host.pauseIdle` turned off, a hostd
	// rolled back, which is what the RUNBOOK tells an operator to do
	// about a wake that fails -- still has the guests it froze before,
	// and they stay paused until something resumes them. Refusing the
	// wake there would strand exactly the machines the operator is
	// trying to rescue. A guest we believe rests is therefore always
	// sent its WakeGuest; a hostd too old to know the command answers
	// `invalid_argument`, which is a failed wake and says so, rather
	// than a machine nobody can reach.
	if !pauses && since == nil {
		return &hostdv1.WakeResult{}, nil
	}
	cmdID := store.NewID()
	ch := make(chan *hostdv1.Result, 1)
	s.mu.Lock()
	s.waiting[cmdID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiting, cmdID)
		s.mu.Unlock()
	}()
	cmd := &hostdv1.Command{CommandId: cmdID.String(), Cmd: &hostdv1.Command_WakeGuest{
		WakeGuest: &hostdv1.WakeGuest{GuestId: guestID.String(), Reason: reason}}}
	started := s.Now()
	if err := s.send.Send(ctx, *hostID, cmd); err != nil {
		s.log.Warn("wake not sent", "event", "guest_wake_fail", "project_id", projectID.String(), "reason", reason, "cause", "host_not_connected")
		return nil, ErrNoHost
	}
	wctx, cancel := context.WithTimeout(ctx, WakeTimeout)
	defer cancel()
	var res *hostdv1.Result
	select {
	case res = <-ch:
	case <-wctx.Done():
		s.log.Warn("wake not answered", "event", "guest_wake_fail", "project_id", projectID.String(), "reason", reason,
			"cause", "timeout", "waited_ms", s.Now().Sub(started).Milliseconds(), "was_resting", since != nil)
		return nil, ErrTimeout
	}
	if !res.Ok {
		code := res.GetError().GetCode()
		s.log.Warn("wake refused", "event", "guest_wake_fail", "project_id", projectID.String(), "reason", reason, "cause", code)
		return nil, fmt.Errorf("resting: wake refused: %s", code)
	}
	wr := res.GetWake()
	if wr == nil {
		wr = &hostdv1.WakeResult{} // a host that answered ok without the payload
	}
	s.clear(ctx, projectID)
	if wr.WasResting {
		s.log.Info("machine woken", "event", "guest_wake", "project_id", projectID.String(), "reason", reason,
			"wake_ms", int(wr.WakeMs), "waited_ms", s.Now().Sub(started).Milliseconds())
	}
	return wr, nil
}

// OnResult takes the result of a pause or a wake this service sent and
// reports whether it was one. Wired in app.go before the questions and
// the ops engine, which ignore a command_id they do not know anyway.
func (s *Service) OnResult(ctx context.Context, hostID uuid.UUID, r *hostdv1.Result) bool {
	cmdID, err := uuid.Parse(r.CommandId)
	if err != nil {
		return false
	}
	s.mu.Lock()
	ch, waiting := s.waiting[cmdID]
	rec, paused := s.pauses[cmdID]
	if paused {
		delete(s.pauses, cmdID)
	}
	s.mu.Unlock()
	switch {
	case waiting:
		select {
		case ch <- r:
		default:
		}
		return true
	case paused:
		s.onPauseResult(ctx, hostID, rec, r)
		return true
	}
	return false
}

// onPauseResult records what the host did with a pause. hostd raises
// GuestResting for a pause it took, which is what writes the row; this
// writes it too, idempotently, so the marker is right even if that event
// is lost, and logs a refusal with the host's own lower_snake word.
func (s *Service) onPauseResult(ctx context.Context, hostID uuid.UUID, rec pauseRec, r *hostdv1.Result) {
	if !r.Ok {
		s.log.Warn("pause failed", "event", "guest_resting_fail", "project_id", rec.project.String(),
			"host_id", hostID.String(), "code", r.GetError().GetCode())
		return
	}
	p := r.GetPause()
	if p == nil || !p.Paused {
		why := "unknown"
		if p != nil && p.WhyNot != "" {
			why = p.WhyNot
		}
		s.log.Info("pause refused by the host", "event", "guest_resting", "project_id", rec.project.String(), "why_not", why)
		return
	}
	at := time.Unix(p.At, 0).UTC()
	if p.At == 0 {
		at = s.Now().UTC()
	}
	if err := s.mark(ctx, rec.project, at); err != nil {
		s.log.Error("resting marker not written", "event", "guest_resting_fail", "project_id", rec.project.String(), "err", err.Error())
	}
}

// mark writes the marker for a machine that is now resting. Idempotent:
// it never moves a marker already set, so a repeated event or a Hello
// reconcile does not make a rest look younger than it is.
func (s *Service) mark(ctx context.Context, projectID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, "update projects set resting_since = $2 where id = $1 and state = 'running' and resting_since is null", projectID, at)
	return err
}

// clear takes the marker off a machine that is awake.
func (s *Service) clear(ctx context.Context, projectID uuid.UUID) {
	if _, err := s.pool.Exec(ctx, "update projects set resting_since = null where id = $1 and resting_since is not null", projectID); err != nil {
		s.log.Error("resting marker not cleared", "event", "guest_resting_fail", "project_id", projectID.String(), "err", err.Error())
	}
}

// OnEvent takes hostd's GuestResting and writes the row. It raises no
// user event and no notification: a pause is invisible, and the marker in
// the state cell is the whole surface (the brief's S6, DECISIONS I-484,
// I-485). It is deliberately not routed through the event ingest's
// state-change path, which would cancel the project's open questions --
// the very questions an answer is supposed to wake the machine for -- and
// write two rows a user reads for every pause and wake.
func (s *Service) OnEvent(ctx context.Context, hostID uuid.UUID, e *hostdv1.GuestResting) {
	gid, err := uuid.Parse(e.GuestId)
	if err != nil {
		return
	}
	p, err := store.GetProjectOnHost(ctx, s.pool, gid, hostID)
	if err != nil {
		if !errors.Is(err, store.ErrOtherHost) {
			s.log.Warn("resting event for a guest the api does not know", "event", "guest_resting", "host_id", hostID.String(), "guest_id", e.GuestId)
		}
		return
	}
	if !e.Resting {
		s.clear(ctx, p.ID)
		s.log.Info("machine awake", "event", "guest_resting", "project_id", p.ID.String(), "resting", false, "reason", e.Reason)
		return
	}
	at := time.Unix(e.At, 0).UTC()
	if e.At == 0 {
		at = s.Now().UTC()
	}
	if err := s.mark(ctx, p.ID, at); err != nil {
		s.log.Error("resting marker not written", "event", "guest_resting_fail", "project_id", p.ID.String(), "err", err.Error())
		return
	}
	s.log.Info("machine resting", "event", "guest_resting", "project_id", p.ID.String(), "resting", true, "reason", e.Reason)
}

// OnHello reconciles the marker from the host's own view of its guests,
// so an api or hostd restart, or a lost event, does not leave a machine
// marked resting when it runs or running when it rests. hostd reads its
// bbolt state and the hypervisor's vm.info at start, so every Hello
// carries the truth.
func (s *Service) OnHello(ctx context.Context, hostID uuid.UUID, h *hostdv1.Hello) {
	for _, g := range h.Guests {
		gid, err := uuid.Parse(g.GuestId)
		if err != nil {
			continue
		}
		p, err := store.GetProjectOnHost(ctx, s.pool, gid, hostID)
		if err != nil {
			continue // not ours, or unknown: the engine's reconcile logs it
		}
		resting := g.RestingAt != 0
		if resting == (p.RestingSince != nil) {
			continue
		}
		if !resting {
			s.log.Info("resting marker cleared from Hello", "event", "guest_resting", "project_id", p.ID.String(), "resting", false)
			s.clear(ctx, p.ID)
			continue
		}
		if err := s.mark(ctx, p.ID, time.Unix(g.RestingAt, 0).UTC()); err != nil {
			s.log.Error("resting marker not written", "event", "guest_resting_fail", "project_id", p.ID.String(), "err", err.Error())
			continue
		}
		s.log.Info("resting marker written from Hello", "event", "guest_resting", "project_id", p.ID.String(), "resting", true)
	}
}
