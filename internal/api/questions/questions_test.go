package questions_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/questions"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func seed(t *testing.T, pool *db.Pool, slug string) (pid, gid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := store.NewID()
	pid, gid = store.NewID(), store.NewID()
	if _, err := pool.Exec(ctx, "insert into users (id, handle, email, ntfy_url) values ($1, $2, 'e@example.com', 'https://ntfy.example/t')", uid, "u"+uid.String()[24:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, guest_id) values ($1, $2, $3, $3, 'large', 'running', 1, $4)", pid, uid, slug, gid); err != nil {
		t.Fatal(err)
	}
	return pid, gid
}

// TestQuestionCloseAndReuseStayInTheSendersProject: a guest that names
// another project's question id can neither close it nor announce it as
// its own; the owning guest still can.
func TestQuestionCloseAndReuseStayInTheSendersProject(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := events.New(pool, metrics.NewNop(), log)
	svc := questions.New(pool, ev, nil, log)
	pidA, gidA := seed(t, pool, "alpha")
	pidB, gidB := seed(t, pool, "bravo")
	qid := uuid.Must(uuid.NewV7()).String()
	now := time.Now()

	if err := svc.OnQuestion(ctx, now, &hostdv1.AgentQuestion{GuestId: gidA.String(), QuestionId: qid, Agent: "claude", Text: "Drop it?"}); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		var st string
		if err := pool.QueryRow(ctx, "select state from questions where id = $1", qid).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	events := func(pid uuid.UUID) int {
		var n int
		_ = pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'agent_question'", pid).Scan(&n)
		return n
	}
	if state() != "pending" || events(pidA) != 1 {
		t.Fatalf("open: state %s, events %d", state(), events(pidA))
	}

	// Guest B closes A's question: nothing happens.
	for _, st := range []string{"cancelled", "expired"} {
		if err := svc.OnQuestion(ctx, now, &hostdv1.AgentQuestion{GuestId: gidB.String(), QuestionId: qid, State: st}); err != nil {
			t.Fatal(err)
		}
	}
	if state() != "pending" {
		t.Fatalf("another project's guest closed the question: %s", state())
	}
	// Guest B announces the same id: no event in B, no text read back.
	if err := svc.OnQuestion(ctx, now, &hostdv1.AgentQuestion{GuestId: gidB.String(), QuestionId: qid, Text: "mine now"}); err != nil {
		t.Fatal(err)
	}
	if events(pidB) != 0 || events(pidA) != 1 {
		t.Fatalf("reused id: events A %d, B %d", events(pidA), events(pidB))
	}
	// The owner's guest closes it.
	if err := svc.OnQuestion(ctx, now, &hostdv1.AgentQuestion{GuestId: gidA.String(), QuestionId: qid, State: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if state() != "cancelled" {
		t.Fatalf("owner's close: %s", state())
	}
}

// TestQuestionWithNULIsStored: text, agent and window a guest sends with a
// NUL are stored cleaned rather than failing the insert, which would leave
// the host event unacked and resent forever.
func TestQuestionWithNULIsStored(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := questions.New(pool, events.New(pool, metrics.NewNop(), log), nil, log)
	_, gid := seed(t, pool, "charlie")
	qid := uuid.Must(uuid.NewV7()).String()
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: qid, Agent: "cl\x00aude", TmuxWindow: "w\x00", Text: "ok\x00?", Options: []string{"y\x00es"}}); err != nil {
		t.Fatal(err)
	}
	var agent, window, text string
	if err := pool.QueryRow(ctx, "select agent, tmux_window, text from questions where id = $1", qid).Scan(&agent, &window, &text); err != nil {
		t.Fatal(err)
	}
	if agent != "claude" || window != "w" || text != "ok?" {
		t.Fatalf("stored %q %q %q", agent, window, text)
	}
}

// TestGuestCloseReasons (DECISIONS I-690): a close the guest sends with
// reason terminal is an answer with no text, via terminal; withdrawn is a
// cancel via agent; any other reason, or none (an older hostd), is the
// plain close of before. Nothing is delivered back down for any of them.
func TestGuestCloseReasons(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := questions.New(pool, events.New(pool, metrics.NewNop(), log), nil, log)
	_, gid := seed(t, pool, "delta")
	for _, tc := range []struct {
		state, reason, wantState, wantVia, describe string
	}{
		{"cancelled", "terminal", "answered", "terminal", "answered in the terminal"},
		{"cancelled", "withdrawn", "cancelled", "agent", "withdrawn by the agent"},
		{"cancelled", "asker_gone", "cancelled", "", "this question was cancelled"},
		{"cancelled", "", "cancelled", "", "this question was cancelled"},
		{"expired", "", "expired", "", "this question has expired"},
	} {
		qid := uuid.Must(uuid.NewV7())
		if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: qid.String(), Agent: "claude", Text: "Go?"}); err != nil {
			t.Fatal(err)
		}
		if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: qid.String(), State: tc.state, Reason: tc.reason}); err != nil {
			t.Fatal(err)
		}
		q, err := svc.Get(ctx, nil, qid)
		if err != nil {
			t.Fatal(err)
		}
		via := ""
		if q.AnsweredVia != nil {
			via = *q.AnsweredVia
		}
		var delivery string
		_ = pool.QueryRow(ctx, "select coalesce(delivery, '') from questions where id = $1", qid).Scan(&delivery)
		if q.State != tc.wantState || via != tc.wantVia || q.Answer != nil || delivery != "guest" || questions.Describe(q) != tc.describe {
			t.Errorf("%s/%s: state %s via %q answer %v delivery %q describe %q", tc.state, tc.reason, q.State, via, q.Answer, delivery, questions.Describe(q))
		}
		if tc.wantState == "answered" && q.AnsweredAt == nil {
			t.Errorf("%s: no answered_at", tc.reason)
		}
	}
}
