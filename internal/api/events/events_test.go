package events_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

// seedOn makes a user and a running project placed on a host, and returns
// the project, guest and host ids.
func seedOn(t *testing.T, pool *db.Pool) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid, pid, gid, hid := store.NewID(), store.NewID(), store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into hosts (id, name, state) values ($1, $2, 'ready')", hid, "h-"+hid.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into users (id, handle, email, ntfy_url) values ($1, $2, 'e@example.com', 'https://ntfy.example/t')", uid, "u"+uid.String()[24:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id, guest_ip, host_id) values ($1, $2, 'todo', 'todo', 'large', 'running', 1, $3, '10.64.4.9', $4)", pid, uid, gid, hid); err != nil {
		t.Fatal(err)
	}
	return pid, gid, hid
}

func TestDedupeOutboxAndRateCap(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	ing := events.New(pool, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	pid, gid, host := seedOn(t, pool)
	now := time.Now()
	// Claude's Stop and agent_completed within a minute collapse to one
	// event with both summaries.
	id1, ins1, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now, Kind: "completed", Agent: "claude", Summary: "ran tests"})
	if err != nil || !ins1 {
		t.Fatal(err)
	}
	id2, ins2, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now.Add(20 * time.Second), Kind: "completed", Agent: "claude", Summary: "3 failures fixed"})
	if err != nil || ins2 || id2 != id1 {
		t.Fatalf("second within window: inserted=%v id=%s (first %s) err=%v", ins2, id2, id1, err)
	}
	var summary string
	var n int
	_ = pool.QueryRow(ctx, "select summary from events where id = $1", id1).Scan(&summary)
	if summary != "ran tests\n3 failures fixed" {
		t.Fatalf("merged summary %q", summary)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox where event_id = $1", id1).Scan(&n)
	if n != 2 {
		t.Fatalf("outbox rows %d", n)
	}
	// A different kind is its own event; a repeat past the window too.
	if _, ins, _ := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now.Add(30 * time.Second), Kind: "needs_input", Agent: "claude", Summary: "?"}); !ins {
		t.Fatal("different kind collapsed")
	}
	if _, ins, _ := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now.Add(2 * time.Minute), Kind: "completed", Agent: "claude", Summary: "later"}); !ins {
		t.Fatal("event past the window collapsed")
	}
	// The hostd event path: same event id twice is one row; a state change
	// updates the project when no op is open.
	ev := &hostdv1.Event{EventId: "ev-1", Ts: now.Unix(), Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: gid.String(), Agent: "codex", Kind: "error", Summary: "exit 1"}}}
	first := ing.OnEvent(ctx, host, ev)
	second := ing.OnEvent(ctx, host, ev)
	if !first || !second {
		t.Fatal("events not acked")
	}
	_ = pool.QueryRow(ctx, "select count(*) from events where host_event_id = 'ev-1'").Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate host event stored %d times", n)
	}
	st := &hostdv1.Event{EventId: "ev-2", Ts: now.Unix(), Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: gid.String(), State: "stopped", Reason: "hypervisor exited"}}}
	ing.OnEvent(ctx, host, st)
	p, _ := store.GetProject(ctx, pool, pid)
	if p.State != "stopped" {
		t.Fatalf("state after host event: %s", p.State)
	}
	// Two state changes in the same second both land (no dedupe on them).
	st2 := &hostdv1.Event{EventId: "ev-3", Ts: now.Unix(), Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: gid.String(), State: "running"}}}
	ing.OnEvent(ctx, host, st2)
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'guest_state_changed'").Scan(&n)
	if n != 2 {
		t.Fatalf("state change events %d", n)
	}
	// A guest that dies after a failed config build shows why it died,
	// not the build's old error.
	_, _ = pool.Exec(ctx, "update projects set last_error = 'build_failed: nixpkgs has no package \"x\"' where id = $1", pid)
	st3 := &hostdv1.Event{EventId: "ev-3b", Ts: now.Unix(), Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: gid.String(), State: "error", Reason: "hypervisor exited 0"}}}
	ing.OnEvent(ctx, host, st3)
	p, _ = store.GetProject(ctx, pool, pid)
	if p.State != "error" || p.LastError == nil || *p.LastError != "internal: the environment stopped unexpectedly (hypervisor exited 0)" {
		t.Fatalf("after a guest error: state %s, last_error %v", p.State, derefStr(p.LastError))
	}
	// snapshot_done inserts a row once.
	blob := p.UserID.String() + "/" + pid.String() + "/1.img.zst"
	sd := &hostdv1.Event{EventId: "ev-4", Ts: now.Unix(), Ev: &hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: gid.String(), BlobPath: blob, Bytes: 5}}}
	ing.OnEvent(ctx, host, sd)
	ing.OnEvent(ctx, host, sd)
	_ = pool.QueryRow(ctx, "select count(*) from snapshots where blob_path = $1", blob).Scan(&n)
	if n != 1 {
		t.Fatalf("snapshot rows %d", n)
	}
	// The edge's HTTP path maps the source ip and dedupes with the vsock path.
	if _, err := ing.FromEdge(ctx, "10.64.4.9", "codex", "error", "exit 1"); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'error' and agent = 'codex'").Scan(&n)
	if n != 1 {
		t.Fatalf("http path duplicated the event: %d", n)
	}
	if _, err := ing.FromEdge(ctx, "10.64.9.9", "codex", "error", "x"); err == nil {
		t.Fatal("unknown source ip accepted")
	}
	// Rate cap: past 30 notified events in an hour, one digest and no more
	// outbox rows.
	base := now.Add(10 * time.Minute)
	for i := 0; i < 40; i++ {
		ts := base.Add(time.Duration(i) * 61 * time.Second)
		ing.SetNow(func() time.Time { return ts })
		if _, _, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: ts, Kind: "completed", Agent: "pi", Summary: fmt.Sprint("loop ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	ing.SetNow(time.Now)
	var outboxEvents, paused int
	_ = pool.QueryRow(ctx, "select count(distinct event_id) from events_outbox").Scan(&outboxEvents)
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'notifications_paused'").Scan(&paused)
	if paused != 1 {
		t.Fatalf("digest events %d", paused)
	}
	if outboxEvents > 36 {
		t.Fatalf("outbox kept growing past the cap: %d events", outboxEvents)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events where agent = 'pi'").Scan(&n)
	if n != 40 {
		t.Fatalf("capped events were dropped: %d stored", n)
	}
	// Clock skew over five minutes is replaced and recorded.
	id, _, _ := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now.Add(-time.Hour), Kind: "error", Agent: "gemini", Summary: "old clock"})
	var skew *int
	var ts time.Time
	_ = pool.QueryRow(ctx, "select ts, skew_seconds from events where id = $1", id).Scan(&ts, &skew)
	if skew == nil || *skew < 3500 || time.Since(ts) > time.Minute {
		t.Fatalf("skew handling: ts=%v skew=%v", ts, skew)
	}
}

// An operator's SSH login on a host becomes exactly one audit_log row,
// keyed by the certificate's key id, however often the host re-sends the
// event (I-140; 14 §5 "every operator SSH login to a host").
func TestOperatorLoginWritesOneAuditRow(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	ing := events.New(pool, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	hostID := store.NewID()
	ev := &hostdv1.Event{EventId: store.NewID().String(), Ts: time.Now().Unix(), Ev: &hostdv1.Event_OperatorLogin{OperatorLogin: &hostdv1.OperatorLogin{PamType: "open_session", UserPresent: true, KeyId: "operator:alice", Serial: 42, KeyFingerprint: "SHA256:abc"}}}
	first := ing.OnEvent(ctx, hostID, ev)
	resent := ing.OnEvent(ctx, hostID, ev) // the host re-sends when an ack is lost
	if !first || !resent {
		t.Fatalf("operator login acked: first %v, re-sent %v", first, resent)
	}
	var n int
	var actor, target string
	var detail map[string]any
	if err := pool.QueryRow(ctx, "select count(*) from audit_log where action = 'operator_login'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, "select actor, target, detail from audit_log where action = 'operator_login'").Scan(&actor, &target, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != "operator:alice" || target != hostID.String() || detail["serial"] != float64(42) || detail["key_id"] != "operator:alice" || detail["host_event_id"] != ev.EventId {
		t.Fatalf("row %s %s %v", actor, target, detail)
	}
	// A plain key (the bootstrap key) is identified by its fingerprint.
	ev2 := &hostdv1.Event{EventId: store.NewID().String(), Ts: time.Now().Unix(), Ev: &hostdv1.Event_OperatorLogin{OperatorLogin: &hostdv1.OperatorLogin{PamType: "open_session", UserPresent: true, KeyFingerprint: "SHA256:boot"}}}
	if !ing.OnEvent(ctx, hostID, ev2) {
		t.Fatal("second login not acked")
	}
	if err := pool.QueryRow(ctx, "select actor from audit_log where detail->>'host_event_id' = $1", ev2.EventId).Scan(&actor); err != nil || actor != "operator:key:SHA256:boot" {
		t.Fatalf("plain-key actor %q %v", actor, err)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestAgentMessagesAreNotCollapsedAndShareTheCap: repose-notify messages
// (DECISIONS I-244) are one event each, even within the dedupe window, and
// count against the project's notification cap like any other event.
func TestAgentMessagesAreNotCollapsedAndShareTheCap(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	ing := events.New(pool, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	pid, gid, host := seedOn(t, pool)
	now := time.Now()
	for i := 0; i < 3; i++ {
		ev := &hostdv1.Event{EventId: fmt.Sprint("msg-", i), Ts: now.Unix(), Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: gid.String(), Agent: "shell", Kind: "agent_message", Summary: fmt.Sprint("step ", i)}}}
		if !ing.OnEvent(ctx, host, ev) {
			t.Fatal("not acked")
		}
	}
	var n int
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'agent_message' and project_id = $1", pid).Scan(&n)
	if n != 3 {
		t.Fatalf("three messages in a second became %d events", n)
	}
	_ = pool.QueryRow(ctx, "select count(distinct event_id) from events_outbox").Scan(&n)
	if n != 3 {
		t.Fatalf("outbox events %d, want 3", n)
	}
	for i := 3; i < 40; i++ {
		if _, _, err := ing.Insert(ctx, events.Incoming{ProjectID: pid, TS: now, Kind: "agent_message", Agent: "shell", Summary: fmt.Sprint("step ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var outboxEvents, paused int
	_ = pool.QueryRow(ctx, "select count(distinct event_id) from events_outbox o join events e on e.id = o.event_id where e.kind = 'agent_message'").Scan(&outboxEvents)
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'notifications_paused'").Scan(&paused)
	if outboxEvents != events.RatePerHour || paused != 1 {
		t.Fatalf("past the cap: %d messages queued, %d pause notices", outboxEvents, paused)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events where kind = 'agent_message'").Scan(&n)
	if n != 40 {
		t.Fatalf("capped messages were dropped: %d stored", n)
	}
}

// A guest names only guest kinds and guest agents: a platform kind from the
// edge is refused, one over vsock is stored as an error with no outbox row
// worded as a platform notice, and notifications_paused (which skips the
// dedupe and the cap) is never reachable from a guest.
func TestGuestCannotSendPlatformKinds(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	ing := events.New(pool, metrics.NewNop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	pid, gid, host := seedOn(t, pool)
	now := time.Now()
	for _, kind := range []string{"billing_stopped", "abuse_stopped", "notifications_paused", "destroy_failed", "temp_destroyed", "agent_question", "welcome", "guest_state_changed"} {
		if _, err := ing.FromEdge(ctx, "10.64.4.9", "claude", kind, "x"); !errors.Is(err, events.ErrNotGuestKind) {
			t.Errorf("edge %s: %v, want ErrNotGuestKind", kind, err)
		}
	}
	for i := 0; i < 60; i++ {
		ev := &hostdv1.Event{EventId: fmt.Sprint("plat-", i), Ts: now.Unix(), Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: gid.String(), Agent: "claude\r\nBcc: x", Kind: "notifications_paused", Summary: "x"}}}
		if !ing.OnEvent(ctx, host, ev) {
			t.Fatal("not acked")
		}
	}
	var n int
	_ = pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind <> 'error' and source <> 'api'", pid).Scan(&n)
	if n != 0 {
		t.Fatalf("%d guest events stored with a non-guest kind", n)
	}
	// The 60 collapse into one error event (dedupe applies), which has no
	// agent because the name was not one guestd sends.
	var agents []string
	rows, _ := pool.Query(ctx, "select coalesce(agent, '') from events where project_id = $1 and kind = 'error'", pid)
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		agents = append(agents, a)
	}
	rows.Close()
	if len(agents) != 1 || agents[0] != "" {
		t.Fatalf("error events %q, want one with no agent", agents)
	}
	_ = pool.QueryRow(ctx, "select count(*) from events_outbox o join events e on e.id = o.event_id where e.project_id = $1", pid).Scan(&n)
	if n != 2 {
		t.Fatalf("outbox rows %d, want 2 (one event, two channels)", n)
	}
	if got := events.GuestAgent("codex"); got != "codex" {
		t.Fatalf("GuestAgent(codex) = %q", got)
	}
}

// TestHostReportsCountOnlyForItsOwnGuests: a host reporting a guest of a
// project placed on another host changes nothing (state, agent events,
// snapshots, questions), and the drop is counted.
func TestHostReportsCountOnlyForItsOwnGuests(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	m := metrics.NewNop()
	ing := events.New(pool, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pid, gid, _ := seedOn(t, pool)
	other := store.NewID()
	p, _ := store.GetProject(ctx, pool, pid)
	now := time.Now()
	for i, ev := range []*hostdv1.Event{
		{Ev: &hostdv1.Event_GuestStateChanged{GuestStateChanged: &hostdv1.GuestStateChanged{GuestId: gid.String(), State: "error"}}},
		{Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: gid.String(), Agent: "claude", Kind: "needs_input", Summary: "spoof"}}},
		{Ev: &hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: gid.String(), BlobPath: p.UserID.String() + "/" + pid.String() + "/x.img.zst", Bytes: 1}}},
	} {
		ev.EventId, ev.Ts = fmt.Sprintf("foreign-%d", i), now.Unix()
		if !ing.OnEvent(ctx, other, ev) {
			t.Fatalf("event %d not acked", i)
		}
	}
	var n int
	_ = pool.QueryRow(ctx, "select (select count(*) from events where project_id = $1) + (select count(*) from snapshots where project_id = $1)", pid).Scan(&n)
	p2, _ := store.GetProject(ctx, pool, pid)
	if n != 0 || p2.State != "running" {
		t.Fatalf("foreign host changed the project: %d rows, state %s", n, p2.State)
	}
	if got := testutil.ToFloat64(m.HostReportsRefused.WithLabelValues("foreign_guest")); got != 3 {
		t.Fatalf("foreign_guest refusals %v", got)
	}
}

// TestGuestKindsTextAndCaps: a guest-raised event cannot carry a platform
// kind, text with a NUL is stored cleaned, a snapshot outside the
// project's prefix is refused, and past the hourly cap nothing is stored.
func TestGuestKindsTextAndCaps(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	m := metrics.NewNop()
	ing := events.New(pool, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pid, gid, host := seedOn(t, pool)
	now := time.Now()
	agentEv := func(id, kind, agent, summary string) *hostdv1.Event {
		return &hostdv1.Event{EventId: id, Ts: now.Unix(), Ev: &hostdv1.Event_AgentEvent{AgentEvent: &hostdv1.AgentEvent{GuestId: gid.String(), Agent: agent, Kind: kind, Summary: summary, TmuxWindow: strings.Repeat("w", 1000)}}}
	}
	if !ing.OnEvent(ctx, host, agentEv("k1", "billing_stopped", "claude", "pay up")) {
		t.Fatal("not acked")
	}
	if !ing.OnEvent(ctx, host, agentEv("k2", "agent_message", "sh\x00ell", "hi\x00there")) {
		t.Fatal("NUL text not acked")
	}
	var kind, agent, window, summary string
	_ = pool.QueryRow(ctx, "select kind from events where host_event_id = 'k1'").Scan(&kind)
	if kind != "error" {
		t.Fatalf("platform kind from a guest stored as %q", kind)
	}
	if err := pool.QueryRow(ctx, "select agent, tmux_window, summary from events where host_event_id = 'k2'").Scan(&agent, &window, &summary); err != nil {
		t.Fatal(err)
	}
	if agent != "shell" || summary != "hithere" || len(window) != events.MaxWindow {
		t.Fatalf("stored %q %d-byte window %q", agent, len(window), summary)
	}

	bad := &hostdv1.Event{EventId: "snap-bad", Ts: now.Unix(), Ev: &hostdv1.Event_SnapshotDone{SnapshotDone: &hostdv1.SnapshotDone{GuestId: gid.String(), BlobPath: store.NewID().String() + "/" + store.NewID().String() + "/x.img.zst"}}}
	ing.OnEvent(ctx, host, bad)
	var n int
	_ = pool.QueryRow(ctx, "select count(*) from snapshots where project_id = $1", pid).Scan(&n)
	if n != 0 || testutil.ToFloat64(m.HostReportsRefused.WithLabelValues("bad_snapshot")) != 1 {
		t.Fatalf("snapshot outside the prefix: %d rows", n)
	}

	if _, err := pool.Exec(ctx, `insert into events (id, project_id, ts, ts_second, kind, summary, source)
		select gen_random_uuid(), $1, $2, $3, 'agent_message', 'x', 'host' from generate_series(1, $4)`, pid, now, now.Unix(), events.GuestEventsPerHour); err != nil {
		t.Fatal(err)
	}
	ing.OnEvent(ctx, host, agentEv("k3", "agent_message", "shell", "one too many"))
	_ = pool.QueryRow(ctx, "select count(*) from events where host_event_id = 'k3'").Scan(&n)
	if n != 0 || testutil.ToFloat64(m.HostReportsRefused.WithLabelValues("project_cap")) != 1 {
		t.Fatalf("past the cap: stored %d", n)
	}
}

// TestHostWarningKindsAreAFixedSet: kinds outside the known set share one
// series, and the logged detail is one capped line.
func TestHostWarningKindsAreAFixedSet(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	m := metrics.NewNop()
	var logBuf strings.Builder
	ing := events.New(pool, m, slog.New(slog.NewTextHandler(&logBuf, nil)))
	for i := 0; i < 500; i++ {
		ing.OnEvent(ctx, store.NewID(), &hostdv1.Event{EventId: fmt.Sprintf("w%d", i), Ev: &hostdv1.Event_HostWarning{HostWarning: &hostdv1.HostWarning{Kind: fmt.Sprintf("kind-%d", i), Detail: "line one\nline two" + strings.Repeat("x", 5000)}}})
	}
	ing.OnEvent(ctx, store.NewID(), &hostdv1.Event{EventId: "w-pool", Ev: &hostdv1.Event_HostWarning{HostWarning: &hostdv1.HostWarning{Kind: "pool_high", Detail: "91% of the thin pool is used"}}})
	if n := testutil.CollectAndCount(m.HostWarningsTotal); n != 2 {
		t.Fatalf("%d warning series, want 2 (other, pool_high)", n)
	}
	if got := testutil.ToFloat64(m.HostWarningsTotal.WithLabelValues("other")); got != 500 {
		t.Fatalf("other %v", got)
	}
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if len(line) > events.MaxWarningDetail+300 {
			t.Fatalf("log line of %d bytes", len(line))
		}
	}
}
