package httpapi

import (
	"errors"
	"net/http"

	"github.com/heracraft/repose/internal/api/say"
)

// sayToAgent is POST /projects/:id/say (DECISIONS I-1144): a message the
// owner typed for the project's agent. The machine types it into the
// agent's own terminal the moment the agent is at its prompt, and the
// answer says which of the two happened.
//
// The message is tenant content: it is in no log line here, and a
// refusal from the machine is passed through only because it names the
// agent windows, which is what the sender needs to pick one.
func (s *Server) sayToAgent(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		Text string `json:"text"`
		// Agent is the window to speak to, as `repose ps` names it
		// (claude, claude-2). Empty means the project's single agent.
		Agent string `json:"agent"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	u := userFrom(r.Context())
	out, err := s.d.Say.Send(r.Context(), say.Request{
		ProjectID: p.ID, UserID: u.ID, Window: body.Agent, Text: body.Text,
		Sender: u.Handle, FromOwner: true,
	})
	if err != nil {
		return sayErr(err)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// sayErr maps the service's refusals to the api's codes. The machine's
// own sentence is kept where it names the agent windows.
func sayErr(err error) error {
	var agentErr *say.ErrAgent
	switch {
	case errors.Is(err, say.ErrEmpty):
		return errf("invalid", "a message with nothing in it")
	case errors.Is(err, say.ErrTooLong):
		return withDetail(errf("invalid", "a message of at most %d bytes", say.TextCap), map[string]any{"reason": "text_too_long", "limit": say.TextCap})
	case errors.Is(err, say.ErrNotRunning):
		return withDetail(errf("conflict", "the machine is not running"), map[string]any{"reason": "not_running"})
	case errors.Is(err, say.ErrNoHost), errors.Is(err, say.ErrTimeout):
		return withDetail(errf("conflict", "the machine did not take the message"), map[string]any{"reason": "machine_unreachable"})
	case errors.As(err, &agentErr):
		reason := "no_agent"
		code := "conflict"
		if agentErr.Several {
			reason, code = "several_agents", "invalid"
		}
		return withDetail(errf(code, "%s", agentErr.Detail), map[string]any{"reason": reason})
	}
	return err
}
