package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Question is a repose-ask question as docs/interfaces/api.md
// "Questions" shows it (DECISIONS I-245).
type Question struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Project     string     `json:"project"`
	Agent       string     `json:"agent"`
	Window      string     `json:"window,omitempty"`
	Text        string     `json:"text"`
	Options     []string   `json:"options"`
	State       string     `json:"state"`
	Answer      *string    `json:"answer"`
	AnsweredVia *string    `json:"answered_via"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AnsweredAt  *time.Time `json:"answered_at"`
}

// AddQuestion has the project's guest ask a question, as a repose-ask in
// it would: a pending question and its agent_question event. A zero
// timeout is 30 minutes.
func (f *Fake) AddQuestion(projectID, agent, text string, options []string, timeout time.Duration) (Question, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok || p.destroyed {
		return Question{}, fmt.Errorf("project %s not found", projectID)
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if agent == "" {
		agent = "shell"
	}
	if options == nil {
		options = []string{}
	}
	now := f.now()
	q := &Question{ID: f.nextID(), ProjectID: p.ID, Project: p.Slug, Agent: agent, Window: agent, Text: text, Options: options,
		State: "pending", CreatedAt: now, ExpiresAt: now.Add(timeout)}
	f.questions = append(f.questions, q)
	f.event(p, "agent_question", agent, text)
	return *q, nil
}

// Questions returns every question the fake holds, oldest first.
func (f *Fake) Questions() []Question {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Question, 0, len(f.questions))
	for _, q := range f.questions {
		out = append(out, *q)
	}
	return out
}

// ReplyToken is the fake's reply-link token for option opt of a question:
// unsigned, since only the real api signs.
func ReplyToken(questionID string, opt int) string { return fmt.Sprintf("fake.%s.%d", questionID, opt) }

func (f *Fake) pendingNow(q *Question) bool {
	if q.State == "pending" && !q.ExpiresAt.After(f.now()) {
		q.State = "expired"
	}
	return q.State == "pending"
}

func (f *Fake) listOf(u *userRec, projectID string, pendingOnly bool, limit int) []Question {
	out := []Question{}
	for i := len(f.questions) - 1; i >= 0; i-- {
		q := f.questions[i]
		p := f.projects[q.ProjectID]
		if p == nil || p.owner != u.ID || p.destroyed || (projectID != "" && q.ProjectID != projectID) {
			continue
		}
		pending := f.pendingNow(q)
		if pendingOnly && !pending {
			continue
		}
		out = append(out, *q)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].State == "pending" && out[j].State != "pending" })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (f *Fake) listQuestions(w http.ResponseWriter, r *http.Request) *apiError {
	writeJSON(w, http.StatusOK, map[string]any{"questions": f.listOf(userFrom(r), "", r.URL.Query().Get("state") != "all", 50)})
	return nil
}

func (f *Fake) listProjectQuestions(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.projectAny(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": f.listOf(userFrom(r), p.ID, r.URL.Query().Get("state") == "pending", 20)})
	return nil
}

func (f *Fake) findQuestion(u *userRec, projectID, qid string) (*Question, *apiError) {
	if _, e := f.projectAny(u, projectID); e != nil {
		return nil, e
	}
	for _, q := range f.questions {
		if q.ID == qid && q.ProjectID == projectID {
			return q, nil
		}
	}
	return nil, notFound("question")
}

// CloseQuestion closes a pending question from the guest's side, as
// guestd's close with a reason reaches the api (DECISIONS I-690): terminal
// is stored as answered with no answer and answered_via terminal;
// withdrawn, interrupted and asker_gone as cancelled with answered_via
// agent.
func (f *Fake) CloseQuestion(id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var q *Question
	for _, c := range f.questions {
		if c.ID == id {
			q = c
		}
	}
	if q == nil {
		return fmt.Errorf("question %s not found", id)
	}
	if !f.pendingNow(q) {
		return fmt.Errorf("question %s is %s", id, q.State)
	}
	now := f.now()
	switch reason {
	case "terminal":
		via := "terminal"
		q.State, q.Answer, q.AnsweredVia, q.AnsweredAt = "answered", nil, &via, &now
	case "withdrawn", "interrupted", "asker_gone":
		via := "agent"
		q.State, q.AnsweredVia, q.AnsweredAt = "cancelled", &via, &now
	default:
		return fmt.Errorf("unknown reason %q", reason)
	}
	return nil
}

func describe(q *Question) string {
	switch q.State {
	case "answered":
		if q.Answer == nil {
			return "already answered in the terminal"
		}
		return "already answered: " + *q.Answer
	case "expired":
		return "this question has expired"
	case "cancelled":
		return "this question was cancelled"
	}
	return "this question was closed"
}

// answer applies the documented rules: pending only, options enforced
// case-insensitively, first answer wins.
func (f *Fake) answer(q *Question, answer, via string) *apiError {
	if !f.pendingNow(q) {
		return errf("conflict", "%s", describe(q)).withDetail(map[string]any{"question": *q})
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return invalid("answer is required")
	}
	if len(q.Options) > 0 {
		match := ""
		for _, o := range q.Options {
			if strings.EqualFold(o, answer) {
				match = o
			}
		}
		if match == "" {
			return invalid("the answer must be one of the options").withDetail(map[string]any{"options": q.Options})
		}
		answer = match
	}
	now := f.now()
	q.State, q.Answer, q.AnsweredVia, q.AnsweredAt = "answered", &answer, &via, &now
	return nil
}

func (f *Fake) answerQuestion(w http.ResponseWriter, r *http.Request) *apiError {
	q, e := f.findQuestion(userFrom(r), r.PathValue("id"), r.PathValue("qid"))
	if e != nil {
		return e
	}
	var body struct {
		Answer string `json:"answer"`
		Via    string `json:"via"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	via := body.Via
	if via != "cli" {
		via = "dashboard"
	}
	if e := f.answer(q, body.Answer, via); e != nil {
		return e
	}
	writeJSON(w, http.StatusOK, *q)
	return nil
}

func (f *Fake) cancelQuestion(w http.ResponseWriter, r *http.Request) *apiError {
	q, e := f.findQuestion(userFrom(r), r.PathValue("id"), r.PathValue("qid"))
	if e != nil {
		return e
	}
	if !f.pendingNow(q) {
		return errf("conflict", "%s", describe(q)).withDetail(map[string]any{"question": *q})
	}
	now := f.now()
	via := "dashboard"
	q.State, q.AnsweredVia, q.AnsweredAt = "cancelled", &via, &now
	writeJSON(w, http.StatusOK, *q)
	return nil
}

// replyQuestion serves both reply-link methods with the fake's unsigned
// token (ReplyToken): GET shows a button, POST answers.
func (f *Fake) replyQuestion(w http.ResponseWriter, r *http.Request) *apiError {
	parts := strings.Split(r.FormValue("token"), ".")
	var q *Question
	opt := -1
	if len(parts) == 3 && parts[0] == "fake" {
		_, _ = fmt.Sscan(parts[2], &opt)
		for _, c := range f.questions {
			if c.ID == parts[1] {
				q = c
			}
		}
	}
	if q == nil || opt < 0 || opt >= len(q.Options) {
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "<form method=\"post\"><button>Answer %s</button></form>", q.Options[opt])
		return nil
	}
	via := r.FormValue("via")
	if via != "email" {
		via = "ntfy"
	}
	if e := f.answer(q, q.Options[opt], via); e != nil {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, describe(q))
		return nil
	}
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Answered: %s", *q.Answer)
	return nil
}
