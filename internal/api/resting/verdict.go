package resting

// Why a running machine did not freeze, written down at the time
// (DECISIONS I-1152).
//
// The rule publishes two numbers a tick -- machines paused and machines
// that would have been (I-844) -- and keeps no record of which of its
// terms said no to the rest. So the owner's question of 2026-11-10, "I'm
// not attached, the agent is idle, but the machine is still running and
// not frozen", had no answer after the fact: the samples are kept and the
// verdict is not, and re-deriving it means writing the rule a second time
// in SQL and hoping the two agree.
//
// This writes the verdict beside the samples, from the rule's own tick,
// once every VerdictEvery. It copies nothing from inside a guest: the
// signals stay in meter_samples and the process names in proc_samples,
// and sample_ts is the key into both.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/db"
)

// VerdictEvery is how often one verdict row per running machine is
// written. It is a multiple of TickInterval, so the rule's own tick
// writes it and nothing new has a timer: at one row per machine per five
// minutes this is a fifth of meter_samples' cadence, and a machine that
// stays warm for an hour leaves twelve rows saying so, which is enough to
// see a condition change and little enough to keep for a month.
const VerdictEvery = 5 * time.Minute

// WhyNotFor is how long hostd's last refusal word stands for a project.
// It matches the window a sent pause is remembered for (prune), because
// both answer the same question: did the host turn this machine down
// recently enough for it to be the reason it is still awake.
const WhyNotFor = 10 * time.Minute

// The values of rest_verdicts.blocked_by. lower_snake, one word for one
// condition, never a sentence: this is the column the owner reads and
// the column the launch query groups by.
const (
	// BlockedNothing is the rule saying yes with the pause on its way.
	BlockedNothing = ""
	// BlockedResting is a machine that IS frozen. Nothing blocked it; it
	// is here so that frozen minutes are countable, and the launch query
	// leaves it out of the warm ones.
	BlockedResting = "resting"

	BlockedHostUnreachable = "host_unreachable"
	BlockedHostNotReady    = "host_not_ready"
	BlockedKeepAwake       = "keep_awake"
	BlockedOp              = "op"
	BlockedQuestion        = "question"
	BlockedSession         = "session"
	BlockedTooYoung        = "too_young"
	BlockedSamplesMissing  = "samples_missing"
	BlockedAttached        = "attached"
	BlockedAgentWorking    = "agent_working"
	BlockedGuestdDown      = "guestd_down"
	BlockedCPU             = "cpu"
	BlockedNet             = "net"
	// BlockedNotQuiet is the catch-all, so a verdict never claims a term
	// it cannot name: the quiet window had a sample the rule refused and
	// none of the terms above accounts for it.
	BlockedNotQuiet = "not_quiet"
	// BlockedHostNoPause is the rule saying yes on a host that does not
	// take pauses: I-844's dry run, counted and never sent.
	BlockedHostNoPause = "host_no_pause"
	// BlockedHostPrefix is put in front of hostd's own lower_snake
	// why_not for a pause it refused (`host_timer_due`,
	// `host_command_in_flight`), so a host's word is never mistaken for
	// one of the rule's own.
	BlockedHostPrefix = "host_"
)

// agentWorkingSQL is the agent term of idle.UsedSQL on its own, so a
// verdict can say "an agent was working" rather than only "the minute
// counted as used". It is a substring of idle.UsedSQL and
// TestTheAgentTermIsIdleUsedSQLsOwn fails if it stops being one: two
// spellings of "this agent is at work" would let the verdict blame a
// term the rule did not read.
const agentWorkingSQL = `exists (select 1 from jsonb_array_elements(coalesce(agents, '[]'::jsonb)) a
	           where coalesce(a->>'state', '') not in ('idle', 'needs_input'))`

// verdictSQL reads every term of the rule for EVERY running project, one
// column per term, where candidatesSQL reads the same terms as a single
// yes. The two must agree, and they do by construction: the sample term
// is quietSQL itself, and TestAVerdictAgreesWithTheRule asserts that a
// machine the rule picks is a row with would_rest true and nothing
// blocking it.
//
// cand's `already_resting` is deliberately not called `resting`:
// quietSQL's last term reads meter_samples' own `resting` column
// unqualified, and a cand column of that name would make it ambiguous.
const verdictSQL = `with cand as (
	select p.id, p.host_id, p.keep_awake,
	       p.resting_since is not null as already_resting,
	       coalesce($3 = any(h.features), false) as pauses,
	       p.host_unreachable,
	       coalesce(h.state = 'ready', false) as host_ready,
	       coalesce(p.started_at <= $2, false) as up_for,
	       (select count(*) from ops o where o.project_id = p.id and o.state in ('pending','running')) as ops_open,
	       (select count(*) from gateway_sessions gs where gs.project_id = p.id and gs.opened_at > $4) as sessions_open,
	       (select count(*) from questions q where q.project_id = p.id and q.state = 'pending'
	                                          and q.expires_at > $1) as questions_open
	  from projects p left join hosts h on h.id = p.host_id
	 where p.state = 'running' and p.destroyed_at is null and p.guest_id is not null
), last as (
	select c.id, m.ts, ` + quietSQL + ` as quiet,
	       m.ssh_sessions + m.tmux_clients as attached,
	       (` + agentWorkingSQL + `) as agent_working,
	       m.guestd_ok, m.cpu_ns, m.net_rx + m.net_tx as net,
	       row_number() over (partition by c.id order by m.ts desc) as rn
	  from cand c join meter_samples m on m.project_id = c.id and m.ts >= $7 and m.ts <= $1
)
select c.id, c.host_id, c.keep_awake, c.already_resting, c.pauses, c.host_unreachable, c.host_ready, c.up_for,
       c.ops_open, c.sessions_open, c.questions_open,
       count(l.ts)                             as samples_seen,
       max(l.ts)                               as sample_ts,
       min(l.ts)                               as oldest_ts,
       count(*) filter (where l.quiet)         as samples_quiet,
       coalesce(max(l.attached), 0)            as attached_max,
       count(*) filter (where l.agent_working) as agents_working,
       count(*) filter (where not l.guestd_ok) as guestd_down,
       coalesce(max(l.cpu_ns), 0)              as cpu_ns_max,
       coalesce(max(l.net), 0)                 as net_bytes_max
  from cand c left join last l on l.id = c.id and l.rn <= $8::bigint
 group by c.id, c.host_id, c.keep_awake, c.already_resting, c.pauses, c.host_unreachable, c.host_ready,
          c.up_for, c.ops_open, c.sessions_open, c.questions_open`

// Verdict is one running machine at one tick: every term of the rule as
// the tick read it, and the one condition that kept the machine awake.
type Verdict struct {
	TS        time.Time
	ProjectID uuid.UUID
	HostID    *uuid.UUID
	SampleTS  *time.Time

	WouldRest bool
	BlockedBy string

	HostPauses    bool
	KeepAwake     bool
	OpsOpen       int
	SessionsOpen  int
	QuestionsOpen int

	SamplesSeen   int
	SamplesQuiet  int
	AttachedMax   int
	AgentsWorking int
	GuestdDown    int
	CPUNsMax      int64
	NetBytesMax   int64

	WhyNot string

	// alreadyResting, hostUnreachable, hostReady and upFor are terms the
	// row does not keep: `resting`, `host_unreachable`, `host_not_ready`
	// and `too_young` as blocked_by say all of them, and the project and
	// host rows are where the state itself lives.
	alreadyResting, hostUnreachable, hostReady, upFor bool
	oldestTS                                          *time.Time
}

// decide names the one condition that kept the machine awake, in the
// order the terms are written in the migration: what the user asked for
// first (a switch, an op, a question, a session), then what the machine
// was doing. Several can be true at once; the counts on the row say what
// else was.
func (v *Verdict) decide(now time.Time, whyNot string) {
	v.WhyNot = whyNot
	switch {
	case v.alreadyResting:
		v.BlockedBy = BlockedResting
		return
	case v.hostUnreachable:
		v.BlockedBy = BlockedHostUnreachable
		return
	case !v.hostReady:
		v.BlockedBy = BlockedHostNotReady
		return
	case v.KeepAwake:
		v.BlockedBy = BlockedKeepAwake
		return
	case v.OpsOpen > 0:
		v.BlockedBy = BlockedOp
		return
	case v.QuestionsOpen > 0:
		v.BlockedBy = BlockedQuestion
		return
	case v.SessionsOpen > 0:
		v.BlockedBy = BlockedSession
		return
	case !v.upFor:
		v.BlockedBy = BlockedTooYoung
		return
	case v.SamplesSeen < Samples,
		v.oldestTS == nil || v.oldestTS.Before(now.Add(-Window)),
		v.SampleTS == nil || v.SampleTS.Before(now.Add(-idle.Fresh)):
		// The sampler, not the machine: too few samples, or evidence
		// older than the rule will read. Every way this is wrong delays
		// a rest and never causes one.
		v.BlockedBy = BlockedSamplesMissing
		return
	case v.AttachedMax > 0:
		v.BlockedBy = BlockedAttached
		return
	case v.AgentsWorking > 0:
		v.BlockedBy = BlockedAgentWorking
		return
	case v.GuestdDown > 0:
		v.BlockedBy = BlockedGuestdDown
		return
	case v.CPUNsMax >= CPUNsPerSample:
		v.BlockedBy = BlockedCPU
		return
	case v.NetBytesMax >= NetBytesPerSample:
		v.BlockedBy = BlockedNet
		return
	case v.SamplesQuiet < Samples:
		v.BlockedBy = BlockedNotQuiet
		return
	}
	// Every term of the rule passed.
	v.WouldRest = true
	switch {
	case !v.HostPauses:
		v.BlockedBy = BlockedHostNoPause
	case whyNot != "":
		v.BlockedBy = BlockedHostPrefix + whyNot
	default:
		v.BlockedBy = BlockedNothing
	}
}

// Verdicts reads the rule term by term for every running machine.
func (s *Service) Verdicts(ctx context.Context, now time.Time) ([]Verdict, error) {
	rows, err := s.pool.Query(ctx, verdictSQL,
		now, now.Add(-UpFor), Feature, now.Add(-SessionWindow),
		int64(CPUNsPerSample), int64(NetBytesPerSample),
		now.Add(-2*Window), Samples)
	if err != nil {
		return nil, fmt.Errorf("read the rest rule term by term: %w", err)
	}
	defer rows.Close()
	var out []Verdict
	for rows.Next() {
		v := Verdict{TS: now.UTC().Truncate(VerdictEvery)}
		if err := rows.Scan(&v.ProjectID, &v.HostID, &v.KeepAwake, &v.alreadyResting, &v.HostPauses,
			&v.hostUnreachable, &v.hostReady, &v.upFor,
			&v.OpsOpen, &v.SessionsOpen, &v.QuestionsOpen,
			&v.SamplesSeen, &v.SampleTS, &v.oldestTS, &v.SamplesQuiet,
			&v.AttachedMax, &v.AgentsWorking, &v.GuestdDown, &v.CPUNsMax, &v.NetBytesMax); err != nil {
			return nil, err
		}
		v.decide(now, s.whyNot(v.ProjectID, now))
		out = append(out, v)
	}
	return out, rows.Err()
}

// whyNot is hostd's own refusal word for this project's most recent
// pause, while it is recent enough to be the reason the machine is still
// awake.
func (s *Service) whyNot(projectID uuid.UUID, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.refusals[projectID]
	if !ok || now.Sub(r.at) > WhyNotFor {
		return ""
	}
	return r.why
}

// maxWhyNot bounds hostd's refusal word. It is a lower_snake enum in the
// contract, and a host is still a thing that sends us bytes: anything
// longer, or with a character outside the enum's shape, is not a word we
// are going to group a column by.
const maxWhyNot = 32

// noteRefusal remembers the word hostd turned a pause down with, bounded
// to the shape the contract promises.
func (s *Service) noteRefusal(projectID uuid.UUID, why string, at time.Time) {
	if len(why) > maxWhyNot {
		why = "other"
	}
	for _, r := range why {
		if (r < 'a' || r > 'z') && r != '_' {
			why = "other"
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refusals[projectID] = refusal{why: why, at: at}
}

const insertVerdictSQL = `insert into rest_verdicts
	(ts, project_id, host_id, sample_ts, would_rest, blocked_by, host_pauses, keep_awake,
	 ops_open, sessions_open, questions_open, samples_seen, samples_quiet, attached_max,
	 agents_working, guestd_down, cpu_ns_max, net_bytes_max, why_not)
	values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	on conflict (project_id, ts) do update set
	  host_id = excluded.host_id, sample_ts = excluded.sample_ts,
	  would_rest = excluded.would_rest, blocked_by = excluded.blocked_by,
	  host_pauses = excluded.host_pauses, keep_awake = excluded.keep_awake,
	  ops_open = excluded.ops_open, sessions_open = excluded.sessions_open,
	  questions_open = excluded.questions_open, samples_seen = excluded.samples_seen,
	  samples_quiet = excluded.samples_quiet, attached_max = excluded.attached_max,
	  agents_working = excluded.agents_working, guestd_down = excluded.guestd_down,
	  cpu_ns_max = excluded.cpu_ns_max, net_bytes_max = excluded.net_bytes_max,
	  why_not = excluded.why_not`

// WriteVerdicts takes one verdict per running machine and writes it. The
// row is an upsert on (project_id, ts): a tick that runs twice, an api
// that restarts inside a bucket or a second replica that briefly holds
// the lock all rewrite the same row rather than failing or doubling the
// minutes the launch query counts.
func (s *Service) WriteVerdicts(ctx context.Context, vs []Verdict) error {
	if len(vs) == 0 {
		return nil
	}
	if err := s.ensureMonth(ctx, vs[0].TS); err != nil {
		return err
	}
	b := &pgx.Batch{}
	for _, v := range vs {
		b.Queue(insertVerdictSQL, v.TS, v.ProjectID, v.HostID, v.SampleTS, v.WouldRest, v.BlockedBy,
			v.HostPauses, v.KeepAwake, v.OpsOpen, v.SessionsOpen, v.QuestionsOpen,
			v.SamplesSeen, v.SamplesQuiet, v.AttachedMax, v.AgentsWorking, v.GuestdDown,
			v.CPUNsMax, v.NetBytesMax, v.WhyNot)
	}
	res := s.pool.SendBatch(ctx, b)
	for range vs {
		if _, err := res.Exec(); err != nil {
			// Close reports the same failure; the first one names the
			// statement, so it is the one worth returning.
			_ = res.Close()
			return fmt.Errorf("write a rest verdict: %w", err)
		}
	}
	if err := res.Close(); err != nil {
		return fmt.Errorf("write the rest verdicts: %w", err)
	}
	return nil
}

// ensureMonth creates the month's partition once per month, the way the
// sample ingest does: EnsurePartitions takes an advisory lock, and this
// tick runs every minute.
func (s *Service) ensureMonth(ctx context.Context, ts time.Time) error {
	key := ts.UTC().Format("2006-01")
	s.mu.Lock()
	ok := s.months[key]
	s.mu.Unlock()
	if ok {
		return nil
	}
	if err := db.EnsurePartitions(ctx, s.pool, ts); err != nil {
		return err
	}
	s.mu.Lock()
	s.months[key] = true
	s.mu.Unlock()
	return nil
}

// verdictDue reports whether this tick opens a new VerdictEvery bucket,
// and remembers it. The rule ticks every minute; a verdict is written on
// the first tick of each bucket, so the cadence is the rule's own timer
// and nothing new has one.
func (s *Service) verdictDue(now time.Time) bool {
	b := now.UTC().Truncate(VerdictEvery)
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.Equal(s.lastVerdict) {
		return false
	}
	s.lastVerdict = b
	return true
}

// recordVerdicts is the whole of what the tick does: read the rule term
// by term and write one row per running machine. A failure here never
// stops a pause -- the rule has already run -- so it logs and returns.
func (s *Service) recordVerdicts(ctx context.Context, now time.Time) {
	if !s.verdictDue(now) {
		return
	}
	vs, err := s.Verdicts(ctx, now)
	if err != nil {
		s.log.Error("rest verdicts not read", "event", "resting_verdict_fail", "err", err.Error())
		return
	}
	if err := s.WriteVerdicts(ctx, vs); err != nil {
		s.log.Error("rest verdicts not written", "event", "resting_verdict_fail", "count", len(vs), "err", err.Error())
	}
}
