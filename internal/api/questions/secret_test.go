package questions_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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

type capture struct {
	mu   sync.Mutex
	sent []*hostdv1.AnswerQuestion
}

func (c *capture) Send(_ context.Context, _ uuid.UUID, cmd *hostdv1.Command) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, cmd.GetAnswerQuestion())
	return nil
}

func (c *capture) all() []*hostdv1.AnswerQuestion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*hostdv1.AnswerQuestion(nil), c.sent...)
}

// TestSecretQuestion (DECISIONS I-929): a question for a named secret
// keeps the name and never a value, refuses a written answer, stores
// one value of two answers at once, and reaches the guest only once the
// UpdateSecrets op its answer queued has finished, with no answer text.
func TestSecretQuestion(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	send := &capture{}
	svc := questions.New(pool, events.New(pool, metrics.NewNop(), log), send, log)
	pid, gid := seed(t, pool, "delta")
	hid := store.NewID()
	if _, err := pool.Exec(ctx, "insert into hosts (id, name, state) values ($1, $2, 'ready')", hid, "h-"+hid.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update projects set host_id = $2 where id = $1", pid, hid); err != nil {
		t.Fatal(err)
	}
	var uid uuid.UUID
	_ = pool.QueryRow(ctx, "select user_id from projects where id = $1", pid).Scan(&uid)

	// A name the api would not store, or options beside one, is dropped.
	for _, bad := range []*hostdv1.AgentQuestion{
		{GuestId: gid.String(), QuestionId: uuid.Must(uuid.NewV7()).String(), Text: "x", Secret: "lower"},
		{GuestId: gid.String(), QuestionId: uuid.Must(uuid.NewV7()).String(), Text: "x", Secret: "BASH_ENV"},
		{GuestId: gid.String(), QuestionId: uuid.Must(uuid.NewV7()).String(), Text: "x", Secret: "KEY", Options: []string{"a"}},
	} {
		if err := svc.OnQuestion(ctx, time.Now(), bad); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Get(ctx, nil, uuid.MustParse(bad.QuestionId)); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("%+v was stored: %v", bad, err)
		}
	}

	qid := uuid.Must(uuid.NewV7())
	// A bidi override in the reason would show the owner one text and
	// say another; it is dropped.
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: qid.String(), Agent: "claude", TmuxWindow: "w\u202e1", Text: "Stripe\u202e dash\u200bboard", Secret: "STRIPE_KEY"}); err != nil {
		t.Fatal(err)
	}
	q, err := svc.Get(ctx, &uid, qid)
	if err != nil || q.Secret != "STRIPE_KEY" || q.Text != "Stripe dashboard" || q.Window != "w1" {
		t.Fatalf("%+v %v", q, err)
	}
	// A second agent waiting on the same name closes with the first.
	twin := uuid.Must(uuid.NewV7())
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: twin.String(), Agent: "codex", Text: "me too", Secret: "STRIPE_KEY"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Answer(ctx, &uid, qid, "sk_live_NEVER", "dashboard"); !errors.Is(err, questions.ErrSecret) {
		t.Fatalf("a written answer: %v", err)
	}

	// Two answers at once: the first holds the row while it stores.
	opID := store.NewID()
	var puts int
	var mu sync.Mutex
	release := make(chan struct{})
	put := func(ctx context.Context, tx db.Tx, projectID uuid.UUID, name string) (*uuid.UUID, error) {
		mu.Lock()
		puts++
		first := puts == 1
		mu.Unlock()
		if projectID != pid || name != "STRIPE_KEY" {
			t.Errorf("put %s %s", projectID, name)
		}
		if first {
			<-release
		}
		_, err := tx.Exec(ctx, `insert into ops (id, project_id, kind, state, params) values ($1, $2, 'update_secrets', 'pending', '{}')`, opID, projectID)
		return &opID, err
	}
	errs := make(chan error, 2)
	go func() { _, err := svc.AnswerSecret(ctx, uid, qid, "dashboard", put); errs <- err }()
	time.Sleep(200 * time.Millisecond)
	go func() { _, err := svc.AnswerSecret(ctx, uid, qid, "cli", put); errs <- err }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	e1, e2 := <-errs, <-errs
	lost := func(err error) bool { return errors.Is(err, questions.ErrBusy) || errors.Is(err, questions.ErrClosed) }
	if (e1 == nil) == (e2 == nil) || (!lost(e1) && !lost(e2)) {
		t.Fatalf("two answers: %v, %v", e1, e2)
	}
	if puts != 1 {
		t.Fatalf("%d puts for two answers", puts)
	}
	q, _ = svc.Get(ctx, &uid, qid)
	if q.State != questions.StateAnswered || q.Answer != nil || q.AnsweredVia == nil || *q.AnsweredVia != "dashboard" {
		t.Fatalf("after: %+v", q)
	}
	if line := questions.CloseLine(q); line != "Saved STRIPE_KEY from the dashboard" {
		t.Fatalf("close line %q", line)
	}
	if tq, _ := svc.Get(ctx, &uid, twin); tq.State != questions.StateAnswered || tq.Answer != nil {
		t.Fatalf("twin: %+v", tq)
	}

	// Not delivered while the op is pending; delivered once it is done.
	if err := svc.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(send.all()); n != 0 {
		t.Fatalf("delivered before the push finished: %d", n)
	}
	if _, err := pool.Exec(ctx, "update ops set state = 'done' where id = $1", opID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Once(ctx); err != nil {
		t.Fatal(err)
	}
	got := send.all()
	if len(got) != 2 {
		t.Fatalf("delivered %+v", got)
	}
	for _, d := range got {
		if d.Status != questions.StateAnswered || d.Answer != "" || (d.QuestionId != qid.String() && d.QuestionId != twin.String()) {
			t.Fatalf("delivered %+v", d)
		}
	}

	// A push that ended in error tells the guest so.
	failed := uuid.Must(uuid.NewV7())
	_ = svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: failed.String(), Text: "x", Secret: "OTHER_KEY"})
	failOp := store.NewID()
	if _, err := svc.AnswerSecret(ctx, uid, failed, "dashboard", func(ctx context.Context, tx db.Tx, projectID uuid.UUID, _ string) (*uuid.UUID, error) {
		_, err := tx.Exec(ctx, `insert into ops (id, project_id, kind, state, params) values ($1, $2, 'update_secrets', 'error', '{}')`, failOp, projectID)
		return &failOp, err
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if got := send.all(); len(got) != 3 || got[2].QuestionId != failed.String() || got[2].Answer != questions.PushFailed {
		t.Fatalf("push failed: %+v", got)
	}

	// A value for a question that takes a written answer is refused.
	plain := uuid.Must(uuid.NewV7())
	_ = svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: plain.String(), Text: "ok?"})
	if _, err := svc.AnswerSecret(ctx, uid, plain, "dashboard", put); !errors.Is(err, questions.ErrNoSecret) {
		t.Fatalf("value for a plain question: %v", err)
	}
}
