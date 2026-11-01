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
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: qid.String(), Agent: "claude", Text: "Stripe dashboard", Secret: "STRIPE_KEY"}); err != nil {
		t.Fatal(err)
	}
	q, err := svc.Get(ctx, &uid, qid)
	if err != nil || q.Secret != "STRIPE_KEY" {
		t.Fatalf("%+v %v", q, err)
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
	if (e1 == nil) == (e2 == nil) || !(errors.Is(e1, questions.ErrClosed) || errors.Is(e2, questions.ErrClosed)) {
		t.Fatalf("two answers: %v, %v", e1, e2)
	}
	if puts != 1 {
		t.Fatalf("%d puts for two answers", puts)
	}
	q, _ = svc.Get(ctx, &uid, qid)
	if q.State != questions.StateAnswered || q.Answer != nil || q.AnsweredVia == nil || *q.AnsweredVia != "dashboard" {
		t.Fatalf("after: %+v", q)
	}
	if line := questions.CloseLine(q); line != "Set STRIPE_KEY from the dashboard" {
		t.Fatalf("close line %q", line)
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
	if len(got) != 1 || got[0].Status != questions.StateAnswered || got[0].Answer != "" || got[0].QuestionId != qid.String() {
		t.Fatalf("delivered %+v", got)
	}

	// A value for a question that takes a written answer is refused.
	plain := uuid.Must(uuid.NewV7())
	_ = svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: plain.String(), Text: "ok?"})
	if _, err := svc.AnswerSecret(ctx, uid, plain, "dashboard", put); !errors.Is(err, questions.ErrNoSecret) {
		t.Fatalf("value for a plain question: %v", err)
	}
}
