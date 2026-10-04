package httpapi_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/abuse"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// DECISIONS I-239, end to end against the real engine and a fake host: a
// sample naming a miner stops the guest through the normal stop op (with
// a snapshot), records the stop, tells the user why on the project and in
// a notification, and counts it for the operator's alert. Resent and
// repeated samples are one stop. The third stop in 24 hours holds the
// project: start and restore are refused until `repose-admin abuse
// clear`, and the user is never suspended.
func TestMinerStopsGuestAndThirdStrikeHoldsStarts(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-miner", "miner")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "hashy", "class": "small"})
	if r.status/100 != 2 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := uuid.MustParse(r.body["id"].(string))
	e.h.WaitFor("created", func() bool { return e.h.Project(pid).State == "running" })
	e.h.WaitIdle(pid)
	g := abuse.New(e.h.Pool, e.h.Engine, e.h.Events, e.h.Metrics, e.h.Log)

	sample := func(ts time.Time, comms ...string) *hostdv1.Samples {
		p := e.h.Project(pid)
		gs := &hostdv1.GuestSample{GuestId: p.GuestID.String(), State: "running", Class: "small"}
		for _, c := range comms {
			gs.Procs = append(gs.Procs, &hostdv1.ProcSample{Comm: c, CpuNsDelta: 59e9})
		}
		return &hostdv1.Samples{Ts: ts.Unix(), Guests: []*hostdv1.GuestSample{gs}}
	}
	stops := func() int {
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from abuse_events where project_id = $1", pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Developer processes stop nothing.
	g.OnSamples(ctx, e.h.HostID, sample(time.Now(), "node", "cargo", "minikube", "claude"))
	if n := stops(); n != 0 || e.h.Project(pid).State != "running" {
		t.Fatalf("a sample without a miner stopped the guest (%d stops, %s)", n, e.h.Project(pid).State)
	}
	// A sample from before this run (resent after a reconnect) is not this run's.
	g.OnSamples(ctx, e.h.HostID, sample(e.h.Project(pid).StartedAt.Add(-time.Minute), "xmrig"))
	if n := stops(); n != 0 {
		t.Fatalf("a sample older than the start stopped the guest")
	}

	for strike := 1; strike <= abuse.Strikes; strike++ {
		// Two samples in a row, as a running miner produces: one stop.
		g.OnSamples(ctx, e.h.HostID, sample(time.Now(), "node", "XMRig"))
		g.OnSamples(ctx, e.h.HostID, sample(time.Now(), "node", "XMRig"))
		e.h.WaitFor("stopped by the miner", func() bool { return e.h.Project(pid).State == "stopped" })
		e.h.WaitIdle(pid)
		if n := stops(); n != strike {
			t.Fatalf("strike %d: %d abuse_events rows", strike, n)
		}
		// Once stopped, more samples (a late one from the stopping guest) do nothing.
		g.OnSamples(ctx, e.h.HostID, sample(time.Now(), "xmrig"))
		if n := stops(); n != strike {
			t.Fatalf("strike %d: a sample of a stopped project added a stop", strike)
		}
		var snaps int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from ops where project_id = $1 and kind = 'stop' and params->>'snapshot' = 'true' and params->>'reason' = 'abuse' and state = 'done'", pid).Scan(&snaps); err != nil || snaps != strike {
			t.Fatalf("strike %d: %d done abuse stop ops with a snapshot (%v)", strike, snaps, err)
		}
		pj := e.do(t, tok, "GET", "/projects/"+pid.String(), nil)
		le, _ := pj.body["last_error"].(string)
		if !strings.HasPrefix(le, "abuse_stopped: stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose") {
			t.Fatalf("strike %d: last_error %q", strike, le)
		}
		st := e.do(t, tok, "POST", "/projects/"+pid.String()+"/start", nil)
		if strike < abuse.Strikes {
			if st.status != 202 {
				t.Fatalf("strike %d: start refused before the hold: %d %s", strike, st.status, st.raw)
			}
			e.h.WaitFor("started again", func() bool { return e.h.Project(pid).State == "running" })
			e.h.WaitIdle(pid)
			if e.h.Project(pid).LastError != nil {
				t.Fatalf("strike %d: a successful start left last_error %q", strike, *e.h.Project(pid).LastError)
			}
			continue
		}
		// The third stop in 24 hours: held.
		errObj, _ := st.body["error"].(map[string]any)
		detail, _ := errObj["detail"].(map[string]any)
		msg, _ := errObj["message"].(string)
		if st.status != 403 || errObj["code"] != "forbidden" || detail["reason"] != "abuse_hold" || !strings.Contains(msg, "hashy is on hold") || !strings.Contains(msg, "(xmrig)") {
			t.Fatalf("start on hold: %d %s", st.status, st.raw)
		}
		rs := e.do(t, tok, "POST", "/projects/restore", map[string]any{"slug": "hashy", "name": "hashy-2"})
		if rs.status != 403 {
			t.Fatalf("restore of a held project as a new one: %d %s", rs.status, rs.raw)
		}
		// A copy that is not started is refused too: started later, it
		// would be a project with no hold of its own (I-460).
		var sid uuid.UUID
		if err := e.h.Pool.QueryRow(ctx, "select id from snapshots where project_id = $1 order by taken_at desc limit 1", pid).Scan(&sid); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			path string
			body map[string]any
		}{
			{"/projects/restore", map[string]any{"slug": "hashy", "name": "hashy-3", "start": false}},
			{"/projects/restore", map[string]any{"snapshot_id": sid.String(), "name": "hashy-4", "start": false}},
			{"/projects/" + pid.String() + "/snapshots/" + sid.String() + "/restore", map[string]any{"as_new_project": "hashy-5", "start": false}},
			{"/projects/" + pid.String() + "/fork", map[string]any{"snapshot_id": sid.String(), "start": false}},
		} {
			rs := e.do(t, tok, "POST", c.path, c.body)
			errObj, _ := rs.body["error"].(map[string]any)
			detail, _ := errObj["detail"].(map[string]any)
			if rs.status != 403 || detail["reason"] != "abuse_hold" {
				t.Fatalf("unstarted copy of a held project (%s %v): %d %s", c.path, c.body, rs.status, rs.raw)
			}
		}
		var copies int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects where slug like 'hashy%' and id <> $1", pid).Scan(&copies); err != nil || copies != 0 {
			t.Fatalf("a refused copy left %d projects (%v)", copies, err)
		}
		// An in-place restore that does not start keeps the project's own
		// hold, so it is not refused; one that starts is.
		if rs := e.do(t, tok, "POST", "/projects/"+pid.String()+"/snapshots/"+sid.String()+"/restore", map[string]any{"start": true}); rs.status != 403 {
			t.Fatalf("started in-place restore of a held project: %d %s", rs.status, rs.raw)
		}
	}
	if got := testutil.ToFloat64(e.h.Metrics.AbuseStopsTotal.WithLabelValues("miner")); got != abuse.Strikes {
		t.Fatalf("repose_api_abuse_stops_total{kind=miner} = %v", got)
	}
	// Stops within the events' dedupe window collapse into one
	// notification whose summary gains the holding stop's words.
	var summary string
	if err := e.h.Pool.QueryRow(ctx, "select summary from events where project_id = $1 and kind = 'abuse_stopped' order by ts desc limit 1", pid).Scan(&summary); err != nil || !strings.Contains(summary, "cannot be started again") {
		t.Fatalf("the holding stop's notification: %q %v", summary, err)
	}
	var suspended *time.Time
	if err := e.h.Pool.QueryRow(ctx, "select suspended_at from users where handle = 'miner'").Scan(&suspended); err != nil || suspended != nil {
		t.Fatalf("the user was suspended automatically: %v %v", suspended, err)
	}
	if err := g.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(e.h.Metrics.AbuseHeldProjects); got != 1 {
		t.Fatalf("repose_api_abuse_held_projects = %v", got)
	}

	// The operator sees it and clears it; the project starts again.
	ae := &admin.Env{KV: e.h.KV, Actor: "admin:test"}
	ae.SetPool(e.h.Pool)
	var out bytes.Buffer
	ae.Stdout, ae.Stderr = &out, &out
	if err := admin.Run(context.Background(), ae, []string{"abuse", "list"}); err != nil || !strings.Contains(out.String(), "hashy") || !strings.Contains(out.String(), "xmrig") {
		t.Fatalf("abuse list: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := admin.Run(context.Background(), ae, []string{"abuse", "clear", "hashy.miner"}); err != nil || !strings.Contains(out.String(), "3 abuse stop(s) cleared") {
		t.Fatalf("abuse clear: %v\n%s", err, out.String())
	}
	var audited int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from audit_log where action = 'abuse_clear' and target = $1", pid.String()).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("abuse clear audit rows: %d %v", audited, err)
	}
	if st := e.do(t, tok, "POST", "/projects/"+pid.String()+"/start", nil); st.status != 202 {
		t.Fatalf("start after clear: %d %s", st.status, st.raw)
	}
	e.h.WaitFor("started after clear", func() bool { return e.h.Project(pid).State == "running" })
	e.h.WaitIdle(pid)
	// The strikes were cleared too: the next miner is strike one, not a hold.
	g.OnSamples(ctx, e.h.HostID, sample(time.Now(), "lolMiner"))
	e.h.WaitFor("stopped again", func() bool { return e.h.Project(pid).State == "stopped" })
	e.h.WaitIdle(pid)
	if st := e.do(t, tok, "POST", "/projects/"+pid.String()+"/start", nil); st.status != 202 {
		t.Fatalf("start after one new strike: %d %s", st.status, st.raw)
	}
}

// BusyUnattended: six hours at full CPU with nobody there counts; the same
// with an agent present (the product: agents run for hours unattended), a
// tmux client, or a shorter run does not.
func TestBusyUnattendedGauge(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	u := e.h.NewUser("busy")
	now := time.Now().UTC()
	fill := func(name, class string, minutes int, cpuPerMin int64, agents string, tmux int) {
		p := e.h.NewProject(u, name, class)
		for i := 0; i < minutes; i++ {
			if _, err := e.h.Pool.Exec(ctx, `insert into meter_samples (ts, project_id, host_id, state, class, cpu_ns, ssh_sessions, tmux_clients, agents, guestd_ok)
				values ($1, $2, $3, 'running', $4, $5, 0, $6, $7::jsonb, true)`, now.Add(-time.Duration(i)*time.Minute), p.ID, e.h.HostID, class, cpuPerMin, tmux, agents); err != nil {
				t.Fatal(err)
			}
		}
	}
	full := func(vcpus int64) int64 { return vcpus * 60e9 * 98 / 100 }
	fill("miner-like", "small", 370, full(2), "[]", 0)
	fill("agent-at-work", "large", 370, full(4), `[{"agent":"claude","window":"0","state":"working"}]`, 0)
	fill("someone-attached", "xl", 370, full(8), "[]", 1)
	fill("half-busy", "large", 370, full(4)/2, "[]", 0)
	fill("only-two-hours", "small", 120, full(2), "[]", 0)
	g := abuse.New(e.h.Pool, e.h.Engine, e.h.Events, e.h.Metrics, e.h.Log)
	if err := g.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(e.h.Metrics.AbuseBusyUnattendedProjects); got != 1 {
		t.Fatalf("repose_api_abuse_busy_unattended_projects = %v, want 1 (miner-like only)", got)
	}
	if got := testutil.ToFloat64(e.h.Metrics.EgressAlertProjects); got != 0 {
		t.Fatalf("repose_api_egress_alert_projects = %v", got)
	}
}
