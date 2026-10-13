package questions_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/questions"
	"github.com/heracraft/repose/internal/db/testdb"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// A question posted to Google Chat gets a reply in its thread saying how
// it closed, whichever way it closed; one that never reached Chat gets
// none (DECISIONS I-692).
func TestCloseRepliesInChatThread(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := events.New(pool, metrics.NewNop(), log)
	svc := questions.New(pool, ev, nil, log)
	pid, gid := seed(t, pool, "chat")
	var uid uuid.UUID
	if err := pool.QueryRow(ctx, "update users set gchat_url = 'https://chat.googleapis.com/v1/spaces/S1/messages?key=k&token=t' where id = (select user_id from projects where id = $1) returning id", pid).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	ask := func(opts ...string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: id.String(), Agent: "claude", Text: "Ship it?", Options: opts}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	posted := func(id uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx, "update events set delivered = jsonb_build_object('gchat', '2026-10-12T10:00:00Z') where id = (select event_id from questions where id = $1)", id); err != nil {
			t.Fatal(err)
		}
	}
	replies := func(id uuid.UUID) []string {
		t.Helper()
		rows, err := pool.Query(ctx, "select e.summary from events e join events_outbox o on o.event_id = e.id and o.channel = 'gchat' where e.user_id = $1 and e.kind = 'question_closed' order by e.ts", uid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			var c events.QuestionClosed
			if err := json.Unmarshal([]byte(s), &c); err != nil {
				t.Fatal(err)
			}
			if c.QuestionID == id {
				out = append(out, c.Text)
			}
		}
		return out
	}
	user := uid

	a := ask("yes", "no")
	posted(a)
	if _, err := svc.Answer(ctx, &user, a, "YES", "dashboard"); err != nil {
		t.Fatal(err)
	}
	if got := replies(a); len(got) != 1 || got[0] != "Answered yes from the dashboard" {
		t.Fatalf("answer: %v", got)
	}

	b := ask()
	posted(b)
	if _, err := svc.Cancel(ctx, user, b); err != nil {
		t.Fatal(err)
	}
	if got := replies(b); len(got) != 1 || got[0] != "Dismissed from the dashboard" {
		t.Fatalf("dismiss: %v", got)
	}

	c := ask()
	posted(c)
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: c.String(), State: "expired"}); err != nil {
		t.Fatal(err)
	}
	if got := replies(c); len(got) != 1 || got[0] != "Expired without an answer" {
		t.Fatalf("guest expiry: %v", got)
	}

	// Never posted to Chat (the send failed): no reply.
	d := ask("yes", "no")
	if _, err := svc.Answer(ctx, &user, d, "no", "email"); err != nil {
		t.Fatal(err)
	}
	if got := replies(d); len(got) != 0 {
		t.Fatalf("reply to a question Chat never got: %v", got)
	}

	// The api's own expiry, and a stopped guest.
	e := ask()
	posted(e)
	if _, err := pool.Exec(ctx, "update questions set expires_at = now() - interval '1 hour' where id = $1", e); err != nil {
		t.Fatal(err)
	}
	if err := svc.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if got := replies(e); len(got) != 1 || got[0] != "Expired without an answer" {
		t.Fatalf("api expiry: %v", got)
	}
	f := ask()
	posted(f)
	if err := svc.GuestStopped(ctx, pid); err != nil {
		t.Fatal(err)
	}
	if got := replies(f); len(got) != 1 || got[0] != "Closed: the machine stopped" {
		t.Fatalf("guest stopped: %v", got)
	}
}

func TestCloseLine(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, tc := range []struct {
		q    questions.Question
		want string
	}{
		{questions.Question{State: "answered", Answer: s("yes"), AnsweredVia: s("email"), Options: []string{"yes"}}, "Answered yes by email"},
		{questions.Question{State: "answered", Answer: s("go"), AnsweredVia: s("gchat"), Options: []string{"go"}}, "Answered go in Google Chat"},
		{questions.Question{State: "answered", Answer: s("main, after the tests"), AnsweredVia: s("cli")}, "Answered from the CLI: main, after the tests"},
		{questions.Question{State: "answered", AnsweredVia: s("terminal")}, "Answered in the terminal"},
		{questions.Question{State: "cancelled", AnsweredVia: s("agent")}, "Withdrawn by the agent"},
		{questions.Question{State: "cancelled"}, "The agent stopped waiting"},
		{questions.Question{State: "expired"}, "Expired without an answer"},
	} {
		if got := questions.CloseLine(&tc.q); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.q, got, tc.want)
		}
	}
}

// A question whose Chat card posted after it closed gets its close reply
// when the outbox reports the post, once, and a pending one gets none.
func TestReplyIfClosed(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := events.New(pool, metrics.NewNop(), log)
	svc := questions.New(pool, ev, nil, log)
	pid, gid := seed(t, pool, "late")
	var uid uuid.UUID
	if err := pool.QueryRow(ctx, "update users set gchat_url = 'https://chat.googleapis.com/v1/spaces/S1/messages?key=k&token=t' where id = (select user_id from projects where id = $1) returning id", pid).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	ask := func() uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: id.String(), Agent: "claude", Text: "Ship it?", Options: []string{"yes", "no"}}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	posted := func(id uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx, "update events set delivered = jsonb_build_object('gchat', '2026-10-12T10:00:00Z') where id = (select event_id from questions where id = $1)", id); err != nil {
			t.Fatal(err)
		}
	}
	replies := func(id uuid.UUID) []string {
		t.Helper()
		rows, err := pool.Query(ctx, "select summary from events where user_id = $1 and kind = 'question_closed' and summary::jsonb->>'question_id' = $2", uid, id.String())
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			var c events.QuestionClosed
			_ = json.Unmarshal([]byte(s), &c)
			out = append(out, c.Text)
		}
		return out
	}

	// Pending: the card posted and nothing closed.
	a := ask()
	posted(a)
	svc.ReplyIfClosed(ctx, a)
	if got := replies(a); len(got) != 0 {
		t.Fatalf("reply to a pending question: %v", got)
	}

	// Expired in the guest while the card was in flight.
	b := ask()
	if err := svc.OnQuestion(ctx, time.Now(), &hostdv1.AgentQuestion{GuestId: gid.String(), QuestionId: b.String(), State: "expired"}); err != nil {
		t.Fatal(err)
	}
	if got := replies(b); len(got) != 0 {
		t.Fatalf("reply before the card posted: %v", got)
	}
	posted(b)
	svc.ReplyIfClosed(ctx, b)
	svc.ReplyIfClosed(ctx, b) // a second report sends no second reply
	if got := replies(b); len(got) != 1 || got[0] != "Expired without an answer" {
		t.Fatalf("late reply: %v", got)
	}

	// The machine stopped while the card was in flight.
	c := ask()
	if err := svc.GuestStopped(ctx, pid); err != nil {
		t.Fatal(err)
	}
	posted(c)
	svc.ReplyIfClosed(ctx, c)
	if got := replies(c); len(got) != 1 || got[0] != "Closed: the machine stopped" {
		t.Fatalf("machine stopped: %v", got)
	}
}
