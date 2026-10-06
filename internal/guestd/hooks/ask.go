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

// Shell is the agent name of a notify or an ask that no agent made: the
// user, or a script, called it from a shell.
const Shell = "shell"

// WaitMax caps one long-poll on GET /ask/{id}; the asker polls again.
const WaitMax = 25 * time.Second

// MessagePayload is POST /notify.
type MessagePayload struct {
	Agent  string `json:"agent,omitempty"`
	Window string `json:"window,omitempty"`
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

// AskState is what POST /ask and GET /ask/{id} answer.
type AskState struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Answer  string    `json:"answer,omitempty"`
	Expires time.Time `json:"expires_at"`
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
	agent := callerAgent(p.Agent)
	window := s.callerWindow(r, p.Window, agent)
	s.msgSink(agent, window, KindMessage, text)
	s.log.Info("agent message received", "event", "agent_event", "agent", agent, "kind", KindMessage, "summary_bytes", len(text))
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
	q, err := s.questions.Open(agent, window, p.Text, p.Options, time.Duration(p.TimeoutS)*time.Second)
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

func (s *Server) handleAskCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.questions.Cancel(id, questions.StateCancelled)
	if errors.Is(err, questions.ErrNotFound) {
		s.reject(w, r, http.StatusNotFound, "unknown_question")
		return
	}
	if err != nil {
		s.reject(w, r, http.StatusInternalServerError, "internal")
		return
	}
	s.log.Info("agent question cancelled by the asker", "event", "agent_question", "question_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func writeState(w http.ResponseWriter, status int, q questions.Question) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(AskState{ID: q.ID, State: q.State, Answer: q.Answer, Expires: q.Expires}) // the asker went away
}
