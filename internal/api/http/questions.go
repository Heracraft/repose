package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/notify"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/questions"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// Questions an agent asked with repose-ask (DECISIONS I-245): listed for the
// dashboard and the CLI, answered or dismissed by the owner, or answered
// by a signed one-click reply link from ntfy or email that needs no login.

func (s *Server) questionsOff() error {
	if s.d.Questions == nil {
		return errf("internal", "questions are not configured")
	}
	return nil
}

// listQuestions is GET /questions: the user's pending questions across
// projects, or ?state=all for the latest of every state.
func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) error {
	if err := s.questionsOff(); err != nil {
		return err
	}
	all := r.URL.Query().Get("state") == "all"
	qs, err := s.d.Questions.List(r.Context(), userFrom(r.Context()).ID, nil, !all, 50)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": qs})
	return nil
}

// listProjectQuestions is GET /projects/:id/questions: pending first, then
// the latest answered or closed ones.
func (s *Server) listProjectQuestions(w http.ResponseWriter, r *http.Request) error {
	if err := s.questionsOff(); err != nil {
		return err
	}
	p, err := s.userProjectAny(r)
	if err != nil {
		return err
	}
	qs, err := s.d.Questions.List(r.Context(), userFrom(r.Context()).ID, &p.ID, r.URL.Query().Get("state") == "pending", 20)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": qs})
	return nil
}

// projectQuestion resolves {id} and {qid} to a question of the user's.
func (s *Server) projectQuestion(r *http.Request) (uuid.UUID, error) {
	if err := s.questionsOff(); err != nil {
		return uuid.Nil, err
	}
	p, err := s.userProjectAny(r)
	if err != nil {
		return uuid.Nil, err
	}
	qid, err := pathID(r, "qid")
	if err != nil {
		return uuid.Nil, err
	}
	uid := userFrom(r.Context()).ID
	q, err := s.d.Questions.Get(r.Context(), &uid, qid)
	if err != nil {
		return uuid.Nil, err
	}
	if q.ProjectID != p.ID {
		return uuid.Nil, db.ErrNotFound
	}
	return qid, nil
}

func questionErr(q *questions.Question, err error) error {
	switch {
	case errors.Is(err, questions.ErrClosed):
		return withDetail(errf("conflict", "%s", questions.Describe(q)), map[string]any{"question": q})
	case errors.Is(err, questions.ErrNotOption):
		return withDetail(errf("invalid", "the answer must be one of the options"), map[string]any{"options": q.Options})
	case errors.Is(err, questions.ErrEmpty):
		return errf("invalid", "answer is required")
	case errors.Is(err, questions.ErrSecret):
		return withDetail(errf("invalid", "this question asks for the secret %s: send its value", q.Secret), map[string]any{"secret": q.Secret})
	case errors.Is(err, questions.ErrBusy):
		return errf("conflict", "another answer to this question is being saved")
	case errors.Is(err, questions.ErrNoSecret):
		return errf("invalid", "this question takes a written answer, not a secret")
	}
	return err
}

// answerQuestion is POST /projects/:id/questions/:qid/answer.
func (s *Server) answerQuestion(w http.ResponseWriter, r *http.Request) error {
	qid, err := s.projectQuestion(r)
	if err != nil {
		return err
	}
	var body struct {
		Answer string `json:"answer"`
		// Value answers a question that asks for a secret (I-929):
		// base64, as PUT /projects/:id/secrets/:name takes it.
		Value *string `json:"value"`
		Via   string  `json:"via"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	via := body.Via
	if via != "cli" {
		via = "dashboard"
	}
	uid := userFrom(r.Context()).ID
	if body.Value != nil {
		return s.answerSecret(w, r, qid, *body.Value, via)
	}
	q, err := s.d.Questions.Answer(r.Context(), &uid, qid, body.Answer, via)
	if err != nil {
		return questionErr(q, err)
	}
	writeJSON(w, http.StatusOK, q)
	return nil
}

// answerSecret answers a question that asks for a secret (DECISIONS
// I-929): the value is checked and stored exactly as PUT
// /projects/:id/secrets/:name stores it, audited and pushed to a running
// machine, and the question closes with no answer text. The value is in
// no response, no log line and no question row.
func (s *Server) answerSecret(w http.ResponseWriter, r *http.Request, qid uuid.UUID, encoded, via string) error {
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errf("invalid", "value must be base64")
	}
	if len(value) == 0 {
		return errf("invalid", "value is required")
	}
	if len(value) > secrets.MaxValueBytes {
		return errf("invalid", "value exceeds 64 KB")
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	u := userFrom(r.Context())
	queued := false
	put := func(ctx context.Context, tx db.Tx, projectID uuid.UUID, name string) (*uuid.UUID, error) {
		if projectID != p.ID {
			return nil, db.ErrNotFound
		}
		if err := s.d.Secrets.PutTx(ctx, tx, u.ID.String(), p.ID.String(), name, value); err != nil {
			if errors.Is(err, secrets.ErrInvalidName) || errors.Is(err, secrets.ErrTooLarge) {
				return nil, errf("invalid", "%v", err)
			}
			return nil, err
		}
		s.d.Metrics.SecretsOpsTotal.WithLabelValues("put").Inc()
		if _, err := store.Audit(ctx, tx, "user:"+u.ID.String(), "secret_put", p.ID.String(), map[string]any{"name": name, "question_id": qid.String()}); err != nil {
			return nil, err
		}
		if p.State != "running" {
			return nil, nil
		}
		pid := p.ID
		id, err := s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()}, true)
		if err != nil {
			return nil, err
		}
		queued = true
		return &id, nil
	}
	q, err := s.d.Questions.AnswerSecret(r.Context(), u.ID, qid, via, put)
	if queued {
		s.d.Engine.Kick()
	}
	if err != nil {
		return questionErr(q, err)
	}
	obs.Logger(r.Context(), s.d.Log).Info("secret stored", "event", "secret_put", "project_id", p.ID.String(), "question_id", qid.String())
	writeJSON(w, http.StatusOK, q)
	return nil
}

// cancelQuestion is POST /projects/:id/questions/:qid/cancel: the owner
// dismisses a question; the waiting repose-ask exits cancelled. The body
// {via} is optional: a client that sends none is the dashboard.
func (s *Server) cancelQuestion(w http.ResponseWriter, r *http.Request) error {
	qid, err := s.projectQuestion(r)
	if err != nil {
		return err
	}
	var body struct {
		Via string `json:"via"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	q, err := s.d.Questions.Cancel(r.Context(), userFrom(r.Context()).ID, qid, body.Via)
	if err != nil {
		return questionErr(q, err)
	}
	writeJSON(w, http.StatusOK, q)
	return nil
}

// replyPage is the page a reply link shows. A GET only shows it, with a
// button that POSTs: mail scanners open links, and a GET that answered
// would let one answer for the owner. ntfy's http button POSTs directly.
var replyPage = template.Must(template.New("reply").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>repose</title>
<style>
body{margin:0;background:#fafaf9;color:#1c1917;font:16px/1.5 system-ui,sans-serif}
main{max-width:34rem;margin:0 auto;padding:3rem 16px}
h1{font:600 1.35rem/1.3 "Noto Serif",Georgia,serif;margin:0 0 1rem}
p{margin:0 0 1rem}.q{white-space:pre-wrap;border-left:3px solid #d6d3d1;padding-left:.75rem}
button{font:inherit;padding:.5rem 1.1rem;border:1px solid #1c1917;background:#1c1917;color:#fafaf9;border-radius:4px;cursor:pointer}
.muted{color:#57534e;font-size:.9rem}
@media (prefers-color-scheme:dark){body{background:#1c1917;color:#f5f5f4}.q{border-color:#57534e}button{background:#f5f5f4;color:#1c1917;border-color:#f5f5f4}.muted{color:#a8a29e}}
</style></head><body><main>
<h1>{{.Title}}</h1>
{{if .Question}}<p class="q">{{.Question}}</p>{{end}}
{{if .Confirm}}<form method="post" action="/v1/questions/reply"><input type="hidden" name="token" value="{{.Token}}"><input type="hidden" name="via" value="{{.Via}}">
<p><button type="submit">Answer “{{.Answer}}”</button></p></form>{{end}}
{{if .Unsubscribe}}<form method="post" action="/v1/notify/unsubscribe"><input type="hidden" name="token" value="{{.Token}}">
<p><button type="submit">Unsubscribe</button></p></form>{{end}}
<p class="muted">{{.Note}}</p>
</main></body></html>
`))

type replyView struct {
	Title, Question, Answer, Token, Via, Note string
	Confirm                                   bool
	// Unsubscribe shows the unsubscribe page's button instead of an answer.
	Unsubscribe bool
}

func (s *Server) writeReplyPage(w http.ResponseWriter, status int, v replyView) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	return replyPage.Execute(w, v)
}

// replyToken verifies a reply link's token and rate-limits it per question.
func (s *Server) replyToken(w http.ResponseWriter, r *http.Request) (uuid.UUID, int, string, bool) {
	token := r.FormValue("token")
	via := r.FormValue("via")
	if via != "email" && via != "gchat" {
		via = "ntfy"
	}
	if s.d.Unsub == nil || s.d.Questions == nil {
		_ = s.writeReplyPage(w, http.StatusServiceUnavailable, replyView{Title: "Replies are not available", Note: "Answer on the dashboard or with repose reply."})
		return uuid.Nil, 0, "", false
	}
	id, opt, err := s.d.Unsub.VerifyReply(token, time.Now())
	if errors.Is(err, notify.ErrReplyExpired) {
		_ = s.writeReplyPage(w, http.StatusGone, replyView{Title: "This question has expired", Note: "The agent stopped waiting for an answer."})
		return uuid.Nil, 0, "", false
	}
	if err != nil {
		_ = s.writeReplyPage(w, http.StatusBadRequest, replyView{Title: "This link is not valid", Note: "Answer on the dashboard or with repose reply."})
		return uuid.Nil, 0, "", false
	}
	if ok, retry := s.replies.Allow(id.String()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
		_ = s.writeReplyPage(w, http.StatusTooManyRequests, replyView{Title: "Too many tries", Note: "Wait a minute and try again."})
		return uuid.Nil, 0, "", false
	}
	return id, opt, via, true
}

// replyGet is GET /questions/reply?token=: shows the question and a button.
func (s *Server) replyGet(w http.ResponseWriter, r *http.Request) error {
	id, opt, via, ok := s.replyToken(w, r)
	if !ok {
		return nil
	}
	q, err := s.d.Questions.Get(r.Context(), nil, id)
	if errors.Is(err, db.ErrNotFound) {
		return s.writeReplyPage(w, http.StatusNotFound, replyView{Title: "This question is gone"})
	}
	if err != nil {
		return err
	}
	title := q.Agent + " asks, in " + q.Project
	if q.State != questions.StatePending || !q.ExpiresAt.After(time.Now()) {
		return s.writeReplyPage(w, http.StatusOK, replyView{Title: title, Question: q.Text, Note: capitalise(questions.Describe(q)) + "."})
	}
	if opt < 0 || opt >= len(q.Options) {
		return s.writeReplyPage(w, http.StatusBadRequest, replyView{Title: "This link is not valid"})
	}
	return s.writeReplyPage(w, http.StatusOK, replyView{Title: title, Question: q.Text, Answer: q.Options[opt], Token: r.FormValue("token"), Via: via, Confirm: true,
		Note: "The link works once, until the question expires."})
}

// replyPost is POST /questions/reply: answers with the link's option. The
// first answer wins; a repeat says what the answer was.
func (s *Server) replyPost(w http.ResponseWriter, r *http.Request) error {
	id, opt, via, ok := s.replyToken(w, r)
	if !ok {
		return nil
	}
	q, err := s.d.Questions.AnswerOption(r.Context(), id, opt, via)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return s.writeReplyPage(w, http.StatusNotFound, replyView{Title: "This question is gone"})
	case errors.Is(err, questions.ErrClosed):
		return s.writeReplyPage(w, http.StatusConflict, replyView{Title: capitalise(questions.Describe(q)), Question: q.Text})
	case errors.Is(err, questions.ErrNotOption):
		return s.writeReplyPage(w, http.StatusBadRequest, replyView{Title: "This link is not valid"})
	case err != nil:
		return err
	}
	return s.writeReplyPage(w, http.StatusOK, replyView{Title: "Answered: " + *q.Answer, Question: q.Text, Note: q.Agent + " has the answer. You can close this page."})
}

func capitalise(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
