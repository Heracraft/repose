package httpapi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// verdict reads the one rest_verdicts row the tick wrote for this
// project, with every column the owner reads.
type verdictRow struct {
	sampleTS                                  *time.Time
	wouldRest                                 bool
	blockedBy, whyNot                         string
	hostPauses, keepAwake                     bool
	ops, sessions, questions                  int
	seen, quiet, attached, agents, guestdDown int
	cpuNsMax, netMax                          int64
}

func (r *restingEnv) verdict(t *testing.T) verdictRow {
	t.Helper()
	var v verdictRow
	err := r.e.h.Pool.QueryRow(r.e.h.Ctx, `select sample_ts, would_rest, blocked_by, why_not,
		host_pauses, keep_awake, ops_open, sessions_open, questions_open,
		samples_seen, samples_quiet, attached_max, agents_working, guestd_down,
		cpu_ns_max, net_bytes_max
		from rest_verdicts where project_id = $1 order by ts desc limit 1`, r.p.ID).
		Scan(&v.sampleTS, &v.wouldRest, &v.blockedBy, &v.whyNot, &v.hostPauses, &v.keepAwake,
			&v.ops, &v.sessions, &v.questions, &v.seen, &v.quiet, &v.attached, &v.agents,
			&v.guestdDown, &v.cpuNsMax, &v.netMax)
	if err != nil {
		t.Fatalf("no rest_verdicts row for the machine: %v", err)
	}
	return v
}

// TestAVerdictNamesTheConditionThatKeptTheMachineAwake is the column the
// owner reads (DECISIONS I-1152), one case per branch of the rest rule.
// Each case is the quiet stretch with one thing changed, the same shape
// TestRestRuleHoldsEveryTerm uses for the rule itself -- so a term the
// rule holds and a term the verdict names are the same list, read twice.
func TestAVerdictNamesTheConditionThatKeptTheMachineAwake(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		rows  sampleRow
		setup func(t *testing.T, r *restingEnv)
	}{
		{"an ssh session", resting.BlockedAttached, sampleRow{agent: "idle", ssh: 1}, nil},
		{"a tmux client", resting.BlockedAttached, sampleRow{agent: "idle", tmux: 1}, nil},
		{"an agent at work", resting.BlockedAgentWorking, sampleRow{agent: "working"}, nil},
		{"an agent in an unknown state", resting.BlockedAgentWorking, sampleRow{agent: "unknown"}, nil},
		{"guestd not answering", resting.BlockedGuestdDown, sampleRow{agent: "idle", noGuestd: true}, nil},
		{"CPU over the 6 s line", resting.BlockedCPU, sampleRow{agent: "idle", cpuNs: resting.CPUNsPerSample}, nil},
		{"network over 64 KiB", resting.BlockedNet, sampleRow{agent: "idle", netRx: resting.NetBytesPerSample / 2, netTx: resting.NetBytesPerSample / 2}, nil},
		{"the never-freeze switch", resting.BlockedKeepAwake, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set keep_awake = true where id = $1", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		// A held forward and a browser viewer both reach the machine
		// over a gateway session, so this one term covers all three.
		{"a gateway session, held forward or viewer", resting.BlockedSession, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "insert into gateway_sessions (project_id, cert_serial, session_id) values ($1, 9, 'fwd')", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"an op in flight", resting.BlockedOp, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "insert into ops (id, project_id, kind, state, params, created_at) values ($1, $2, 'snapshot', 'pending', '{}'::jsonb, now())", store.NewID(), r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"an open repose-ask question", resting.BlockedQuestion, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx,
				`insert into questions (id, project_id, guest_id, event_id, agent, text, state, expires_at)
				 values ($1, $2, $3, $4, 'claude', 'which branch?', 'pending', now() + interval '20 minutes')`,
				store.NewID(), r.p.ID, store.NewID(), store.NewID()); err != nil {
				t.Fatal(err)
			}
		}},
		{"up for less than the window", resting.BlockedTooYoung, quiet, func(t *testing.T, r *restingEnv) {
			r.upFor(t, time.Minute)
		}},
		{"the host unreachable", resting.BlockedHostUnreachable, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update projects set host_unreachable = true where id = $1", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"the host not ready", resting.BlockedHostNotReady, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "update hosts set state = 'draining' where id = (select host_id from projects where id = $1)", r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
		// Nothing was wrong with the machine: the sampler did not report.
		{"too few samples", resting.BlockedSamplesMissing, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx, "delete from meter_samples where project_id = $1 and ts >= $2", r.p.ID, time.Now().Add(-3*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}},
		// The rule said yes. The machine is still warm because its host
		// does not take pauses (I-844's dry run).
		{"a host that does not pause", resting.BlockedHostNoPause, quiet, func(t *testing.T, r *restingEnv) {
			if _, err := r.e.h.Pool.Exec(r.e.h.Ctx,
				"update hosts set features = array_remove(features, $1) where id = (select host_id from projects where id = $2)",
				resting.Feature, r.p.ID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRestingEnv(t, fmt.Sprintf("vrd%d", i))
			r.upFor(t, time.Hour)
			r.quietStretch(t, c.rows)
			if c.setup != nil {
				c.setup(t, r)
			}
			if _, err := r.e.h.Resting.Once(r.e.h.Ctx); err != nil {
				t.Fatal(err)
			}
			v := r.verdict(t)
			if v.blockedBy != c.want {
				t.Fatalf("blocked_by = %q, want %q (would_rest %v, seen %d quiet %d attached %d agents %d guestd_down %d cpu %d net %d)",
					v.blockedBy, c.want, v.wouldRest, v.seen, v.quiet, v.attached, v.agents, v.guestdDown, v.cpuNsMax, v.netMax)
			}
			// would_rest is the rule's own answer, and only the last two
			// cases are a machine the rule said yes to.
			wantWould := c.want == resting.BlockedHostNoPause
			if v.wouldRest != wantWould {
				t.Fatalf("would_rest = %v, want %v for %s", v.wouldRest, wantWould, c.want)
			}
		})
	}
}

// TestAVerdictAgreesWithTheRule: the verdict is a second read of the same
// terms, so a machine the rule picks must be a row that blames nothing,
// and a machine it does not pick must be a row that names something. Two
// spellings of one rule that drift apart is the failure this catches.
func TestAVerdictAgreesWithTheRule(t *testing.T) {
	r := newRestingEnv(t, "agree")
	ctx := r.e.h.Ctx
	r.upFor(t, time.Hour)
	r.quietStretch(t, quiet)

	now := time.Now().UTC()
	cs, err := r.e.h.Resting.Candidates(ctx, now)
	if err != nil || len(cs) != 1 {
		t.Fatalf("candidates %+v %v", cs, err)
	}
	vs, err := r.e.h.Resting.Verdicts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("verdicts %+v, want one per running machine", vs)
	}
	v := vs[0]
	if !v.WouldRest || v.BlockedBy != resting.BlockedNothing {
		t.Fatalf("the rule picked the machine and the verdict says would_rest=%v blocked_by=%q", v.WouldRest, v.BlockedBy)
	}
	if v.SamplesSeen != resting.Samples || v.SamplesQuiet != resting.Samples {
		t.Fatalf("seen %d quiet %d, want %d of each", v.SamplesSeen, v.SamplesQuiet, resting.Samples)
	}
	if v.SampleTS == nil {
		t.Fatal("sample_ts is null; it is the join key to meter_samples and proc_samples")
	}
}

// TestAVerdictRowIsWrittenOnceAVerdictBucket: the rule ticks every
// minute and the verdict is written every VerdictEvery, from that same
// tick. A second timer, or a row a minute, is what this pins against:
// one is a thing to get wrong and the other is five times the rows for
// nothing.
func TestAVerdictRowIsWrittenOnceAVerdictBucket(t *testing.T) {
	r := newRestingEnv(t, "bucket")
	ctx := r.e.h.Ctx
	r.upFor(t, time.Hour)
	r.quietStretch(t, sampleRow{agent: "working"}) // no pause, so Once only writes verdicts

	// One clock for the three ticks, so the test does not depend on
	// where the wall clock sits inside a bucket.
	now := time.Now().UTC()
	r.e.h.Resting.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, err := r.e.h.Resting.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := r.e.h.Pool.QueryRow(ctx, "select count(*) from rest_verdicts where project_id = $1", r.p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("three ticks inside one bucket wrote %d rows, want 1", n)
	}
	// The row is an upsert, so the same bucket read twice corrects it
	// rather than failing or doubling the minutes the launch query counts.
	vs, err := r.e.h.Resting.Verdicts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.e.h.Resting.WriteVerdicts(ctx, vs); err != nil {
		t.Fatalf("rewriting the same bucket: %v", err)
	}
	if err := r.e.h.Pool.QueryRow(ctx, "select count(*) from rest_verdicts where project_id = $1", r.p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the same bucket written twice made %d rows", n)
	}
}

// TestAVerdictCarriesNothingATenantTyped: the row is columns of ids,
// booleans, counts and bounded lower_snake words, and nothing else. A
// process name, a prompt, a path or an argument has no column to sit in.
// The rule's own log lines are already covered by obslint; this covers
// the table, which obslint cannot see.
func TestAVerdictCarriesNothingATenantTyped(t *testing.T) {
	r := newRestingEnv(t, "privacy")
	allowed := map[string]bool{
		"ts": true, "project_id": true, "host_id": true, "sample_ts": true,
		"would_rest": true, "blocked_by": true, "host_pauses": true, "keep_awake": true,
		"ops_open": true, "sessions_open": true, "questions_open": true,
		"samples_seen": true, "samples_quiet": true, "attached_max": true,
		"agents_working": true, "guestd_down": true, "cpu_ns_max": true,
		"net_bytes_max": true, "why_not": true,
	}
	rows, err := r.e.h.Pool.Query(r.e.h.Ctx,
		"select column_name from information_schema.columns where table_name = 'rest_verdicts'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
		if !allowed[c] {
			t.Errorf("rest_verdicts.%s is not on the list a verdict may carry; a new column needs a reviewer, not a default", c)
		}
	}
	sort.Strings(got)
	if len(got) != len(allowed) {
		t.Fatalf("rest_verdicts has %d columns and the list has %d: %v", len(got), len(allowed), got)
	}
}

// TestTheVerdictsWhyNotIsTheHostsOwnWord: the rule said yes, the pause
// went, and hostd turned it down. Without this the row would read
// blocked_by = ” for a machine that is never going to freeze, which is
// exactly the case the owner reported (I-930's "not changed here": a
// machine that will not freeze gives its owner nothing to go on).
func TestTheVerdictsWhyNotIsTheHostsOwnWord(t *testing.T) {
	r := newRestingEnv(t, "whynot")
	ctx := r.e.h.Ctx
	r.upFor(t, time.Hour)
	r.quietStretch(t, quiet)
	r.e.h.Fake.RefusePause("timer_due")

	if _, err := r.e.h.Resting.Once(ctx); err != nil {
		t.Fatal(err)
	}
	// The refusal comes back on the host's stream, so the next bucket's
	// verdict is the one that carries it.
	var v resting.Verdict
	r.e.h.WaitFor("the host's refusal in a verdict", func() bool {
		vs, err := r.e.h.Resting.Verdicts(ctx, time.Now().UTC())
		if err != nil || len(vs) != 1 {
			return false
		}
		v = vs[0]
		return v.WhyNot != ""
	})
	if !v.WouldRest {
		t.Fatal("the rule said no; this case is the rule saying yes and the host refusing")
	}
	if v.WhyNot != "timer_due" || v.BlockedBy != "host_timer_due" {
		t.Fatalf("why_not %q blocked_by %q, want timer_due and host_timer_due", v.WhyNot, v.BlockedBy)
	}
	// And the machine is not marked frozen, which is the whole point:
	// the rule said yes and nothing happened.
	if r.e.h.Project(r.p.ID).RestingSince != nil {
		t.Fatal("a refused pause marked the machine resting")
	}
}

// q13 is the launch question as it is committed, so the test runs the
// text an operator runs and not a copy of it.
func q13(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "ops", "sql", "launch.sql"))
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(b), "-- Q13.")
	if i < 0 {
		t.Fatal("ops/sql/launch.sql has no Q13")
	}
	return string(b)[i:]
}

// TestLaunchQ13NamesTheConditionAndTheProcess runs the launch question
// of DECISIONS I-1152 against a real database with fixture rows: a
// machine that would not freeze because of CPU, and what was spending
// it. A query that does not run is the failure this catches -- a group
// by that does not cover the select list, a window function in the wrong
// place -- and only Postgres catches those.
func TestLaunchQ13NamesTheConditionAndTheProcess(t *testing.T) {
	r := newRestingEnv(t, "q13")
	ctx := r.e.h.Ctx
	now := time.Now().UTC()

	// Six verdicts over half an hour: four blocked on CPU, one frozen
	// (which the question leaves out) and one on an attached session.
	for i, c := range []struct {
		blocked string
		would   bool
	}{{"cpu", false}, {"cpu", false}, {"cpu", false}, {"cpu", false}, {"resting", false}, {"attached", false}} {
		ts := now.Add(-time.Duration(i+1) * resting.VerdictEvery).Truncate(resting.VerdictEvery)
		sampleTS := ts.Add(-30 * time.Second)
		if err := db.EnsurePartitions(ctx, r.e.h.Pool, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := r.e.h.Pool.Exec(ctx, `insert into rest_verdicts
			(ts, project_id, host_id, sample_ts, would_rest, blocked_by, host_pauses, keep_awake, cpu_ns_max)
			values ($1,$2,$3,$4,$5,$6,true,false,9000000000)`,
			ts, r.p.ID, r.p.HostID, sampleTS, c.would, c.blocked); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsurePartitions(ctx, r.e.h.Pool, sampleTS); err != nil {
			t.Fatal(err)
		}
		for comm, cpu := range map[string]int64{"chromium": 8_000_000_000, "claude": 2_000_000_000} {
			if _, err := r.e.h.Pool.Exec(ctx,
				"insert into proc_samples (ts, project_id, comm, cpu_ns, rss) values ($1,$2,$3,$4,0)",
				sampleTS, r.p.ID, comm, cpu); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows, err := r.e.h.Pool.Query(ctx, q13(t))
	if err != nil {
		t.Fatalf("Q13 does not run: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var slug, blocked string
		var minutes int
		var hours float64
		var top *string
		if err := rows.Scan(&slug, &blocked, &minutes, &hours, &top); err != nil {
			t.Fatal(err)
		}
		if slug != r.p.Slug {
			continue
		}
		got[blocked] = fmt.Sprintf("%d minutes, top %v", minutes, derefOr(top, "-"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["resting"]; ok {
		t.Fatal("a frozen machine's minutes are counted as warm")
	}
	// The process CPU is summed across the minutes those verdicts read:
	// four CPU-blocked ticks of 8 s of chromium each.
	if want := "20 minutes, top chromium 32s, claude 8s"; got["cpu"] != want {
		t.Fatalf("cpu row = %q, want %q (all rows: %v)", got["cpu"], want, got)
	}
	if want := "5 minutes, top chromium 8s, claude 2s"; got["attached"] != want {
		t.Fatalf("attached row = %q, want %q", got["attached"], want)
	}
}

func derefOr(s *string, alt string) string {
	if s == nil {
		return alt
	}
	return *s
}
