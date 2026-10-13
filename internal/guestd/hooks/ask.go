package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/heracraft/repose/internal/guestd/questions"
)

// KindMessage is the AgentEvent kind `repose-notify` sends (DECISIONS I-244).
const KindMessage = "agent_message"

// KindDone is the AgentEvent kind `repose-notify --done` sends: the work
// the user asked for is finished (DECISIONS I-688).
const KindDone = "agent_done"

// Shell is the agent name of a notify or an ask that no agent made: the
// user, or a script, called it from a shell.
const Shell = "shell"

// WaitMax caps one long-poll on GET /ask/{id}; the asker polls again.
const WaitMax = 25 * time.Second

// MessagePayload is POST /notify. Kind is "" (agent_message) or
// agent_done.
type MessagePayload struct {
	Agent  string `json:"agent,omitempty"`
	Window string `json:"window,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Text   string `json:"text"`
}

// AskPayload is POST /ask.
type AskPayload struct {
	Agent    string   `json:"agent,omitempty"`
	Window   string   `json:"window,omitempty"`
	Text     string   `json:"text"`
	Options  []string `json:"options,omitempty"`
	TimeoutS uint32   `json:"timeout_s,omitempty"`
}

// AskState is what POST /ask and GET /ask/{id} answer, and one entry of
// GET /ask. Reason is why the guest side closed it (I-690); Text is only
// in GET /ask.
type AskState struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Answer  string    `json:"answer,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Text    string    `json:"text,omitempty"`
	Expires time.Time `json:"expires_at"`
}

// AskList is GET /ask.
type AskList struct {
	Questions []AskState `json:"questions"`
}

// AskClosed is DELETE /ask: how many questions it closed.
type AskClosed struct {
	Closed int `json:"closed"`
}

// EnableAsk serves POST /notify and the /ask routes on the hook socket.
// Without it they answer 404, which an older repose-hook never calls. msg
// receives a notify; it is not the hook sink, because a message says
// nothing about whether the agent is working or waiting.
func (s *Server) EnableAsk(q *questions.Store, msg Sink) {
	s.questions = q
	s.msgSink = msg
	s.mux.HandleFunc("POST /notify", s.handleNotify)
	s.mux.HandleFunc("POST /ask", s.handleAsk)
	s.mux.HandleFunc("GET /ask", s.handleAskList)
	s.mux.HandleFunc("DELETE /ask", s.handleAskCancelMany)
	s.mux.HandleFunc("GET /ask/{id}", s.handleAskWait)
	s.mux.HandleFunc("DELETE /ask/{id}", s.handleAskCancel)
}

// callerAgent is the agent a notify or an ask is attributed to: the one the
// client named if it is one of the five, else "shell".
func callerAgent(name string) string {
	if agents[name] {
		return name
	}
	return Shell
}

// callerWindow is the window a notify or an ask is recorded under, and
// the one DELETE /ask matches: windowOf, else the agent's name.
func (s *Server) callerWindow(r *http.Request, given, agent string) string {
	if w := s.windowOf(r, given); w != "" {
		return w
	}
	return agent
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	var p MessagePayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		s.reject(w, r, http.StatusBadRequest, "malformed_json")
		return
	}
	text := questions.Clean(p.Text, questions.TextCap)
	if text == "" {
		s.reject(w, r, http.StatusBadRequest, "empty_message")
		return
	}
	kind := KindMessage
	switch p.Kind {
	case "", KindMessage:
	case KindDone:
		kind = KindDone
	default:
		s.reject(w, r, http.StatusBadRequest, "unknown_kind")
		return
	}
	agent := callerAgent(p.Agent)
	window := s.callerWindow(r, p.Window, agent)
	s.msgSink(agent, window, kind, text)
	s.log.Info("agent message received", "event", "agent_event", "agent", agent, "kind", kind, "summary_bytes", len(text))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var p AskPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		s.reject(w, r, http.StatusBadRequest, "malformed_json")
		return
	}
	agent := callerAgent(p.Agent)
	window := s.callerWindow(r, p.Window, agent)
	q, err := s.questions.Open(agent, window, s.agentProcOf(r, agent), p.Text, p.Options, time.Duration(p.TimeoutS)*time.Second)
	switch {
	case errors.Is(err, questions.ErrTooMany):
		s.reject(w, r, http.StatusTooManyRequests, "too_many_questions")
		return
	case errors.Is(err, questions.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, "invalid_question")
		return
	case err != nil:
		s.reject(w, r, http.StatusInternalServerError, "internal")
		return
	}
	s.log.Info("agent question opened", "event", "agent_question", "agent", agent, "question_id", q.ID,
		"options", len(q.Options), "timeout_s", q.TimeoutS, "text_bytes", len(q.Text))
	writeState(w, http.StatusCreated, q)
}

func (s *Server) handleAskWait(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	d := time.Duration(wait) * time.Second
	if d > WaitMax {
		d = WaitMax
	}
	if d < 0 {
		d = 0
	}
	// The server's write timeout is shorter than a long poll.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d + 5*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), d)
	defer cancel()
	q, err := s.questions.Wait(ctx, id)
	if errors.Is(err, questions.ErrNotFound) {
		s.reject(w, r, http.StatusNotFound, "unknown_question")
		return
	}
	if err != nil {
		s.reject(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeState(w, http.StatusOK, q)
}

// cancelReason is the reason a DELETE gives in ?reason=: withdrawn from
// `repose-ask --cancel`, interrupted (the default, what an asker that
// caught a signal sends). ok is false for another value.
func cancelReason(r *http.Request) (string, bool) {
	switch v := r.URL.Query().Get("reason"); v {
	case "":
		return questions.ReasonInterrupted, true
	case questions.ReasonInterrupted, questions.ReasonWithdrawn:
		return v, true
	default:
		return "", false
	}
}

func (s *Server) handleAskCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reason, ok := cancelReason(r)
	if !ok {
		s.reject(w, r, http.StatusBadRequest, "unknown_reason")
		return
	}
	q, err := s.questions.Get(id)
	if err == nil && !q.Open() {
		// Closed meanwhile: nothing to take back, and the caller is told.
		s.reject(w, r, http.StatusConflict, "question_closed")
		return
	}
	err = s.questions.Cancel(id, questions.StateCancelled, reason)
	if errors.Is(err, questions.ErrNotFound) {
		s.reject(w, r, http.StatusNotFound, "unknown_question")
		return
	}
	if err != nil {
		s.reject(w, r, http.StatusInternalServerError, "internal")
		return
	}
	s.log.Info("agent question cancelled by the asker", "event", "agent_question", "question_id", id, "reason", reason)
	w.WriteHeader(http.StatusNoContent)
}

// callerMatch is what ?scope= selects: every open question for "all",
// else the caller's own, those asked from its agent process or, with
// none, from its window (I-690).
func (s *Server) callerMatch(r *http.Request) (func(questions.Question) bool, bool) {
	switch r.URL.Query().Get("scope") {
	case "all":
		return func(questions.Question) bool { return true }, true
	case "", "mine":
	default:
		return nil, false
	}
	agent := callerAgent(r.URL.Query().Get("agent"))
	// The window as POST /ask stored it, the agent's name when the caller
	// is in no window, so a script outside tmux finds its own questions.
	return askedFrom(s.agentProcOf(r, agent), s.callerWindow(r, r.URL.Query().Get("window"), agent)), true
}

// handleAskList is GET /ask: the open questions in scope, oldest first.
func (s *Server) handleAskList(w http.ResponseWriter, r *http.Request) {
	match, ok := s.callerMatch(r)
	if !ok {
		s.reject(w, r, http.StatusBadRequest, "unknown_scope")
		return
	}
	out := AskList{Questions: []AskState{}}
	for _, q := range s.questions.List(match) {
		out.Questions = append(out.Questions, AskState{ID: q.ID, State: q.State, Text: q.Text, Expires: q.Expires})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out) // the caller went away
}

// handleAskCancelMany is DELETE /ask: withdraws the open questions in
// scope (`repose-ask --cancel [--all]`).
func (s *Server) handleAskCancelMany(w http.ResponseWriter, r *http.Request) {
	match, ok := s.callerMatch(r)
	if !ok {
		s.reject(w, r, http.StatusBadRequest, "unknown_scope")
		return
	}
	reason, ok := cancelReason(r)
	if !ok {
		s.reject(w, r, http.StatusBadRequest, "unknown_reason")
		return
	}
	n := s.questions.CloseWhere(match, reason)
	s.log.Info("agent questions cancelled by the asker", "event", "agent_question", "reason", reason, "count", n)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AskClosed{Closed: n}) // the caller went away
}

func writeState(w http.ResponseWriter, status int, q questions.Question) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(AskState{ID: q.ID, State: q.State, Answer: q.Answer, Reason: q.Reason, Expires: q.Expires}) // the asker went away
}
