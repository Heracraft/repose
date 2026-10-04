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
