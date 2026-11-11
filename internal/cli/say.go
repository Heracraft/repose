package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// `repose say` (DECISIONS I-1144): a message for the project's agent,
// from the laptop. The machine types it into the agent's own terminal,
// the way `repose run -w WINDOW -p TEXT` does, the moment the agent is
// at its prompt; while the agent is mid-turn it waits for the end of the
// turn.

// sayRefusalLine makes the machine's refusal a line a person reads: its first
// letter capital and no full stop, since the caller adds one. guestd
// phrases its errors for a log, and this is the one place they are shown
// to the owner.
func sayRefusalLine(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), ".")
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// Say is the says row POST /projects/:id/say answers with.
type Say struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Window    string    `json:"window,omitempty"`
	Text      string    `json:"text"`
	Sender    string    `json:"sender,omitempty"`
	State     string    `json:"state"`
	Replaced  bool      `json:"replaced,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SayToAgent is POST /projects/:id/say.
func (c *Client) SayToAgent(ctx context.Context, projectID, agent, text string) (*Say, error) {
	var s Say
	body := map[string]string{"text": text}
	if agent != "" {
		body["agent"] = agent
	}
	if err := c.post(ctx, "/projects/"+url.PathEscape(projectID)+"/say", body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SayCmd sends one message to the project's agent.
func SayCmd(ctx context.Context, e *Env, projectArg, agent, text string) error {
	if strings.TrimSpace(text) == "" {
		return cobraUsageError{fmt.Errorf("repose say takes the message: repose say [PROJECT] \"<message>\"")}
	}
	p, err := requireRunningProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	s, err := e.Client.SayToAgent(ctx, p.ID, agent, text)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		// The machine's own sentence names the agent windows, which is
		// what the sender needs to pick one; it is printed as it is,
		// without a code after it.
		switch apiErr.Detail["reason"] {
		case "several_agents", "no_agent", "not_running", "machine_unreachable":
			return exitf(ExitGeneric, "%s.", sayRefusalLine(apiErr.Message))
		}
	}
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, s)
	}
	who := s.Window
	if who == "" {
		who = "the agent"
	}
	switch {
	case s.State == "waiting" && s.Replaced:
		_, _ = fmt.Fprintf(e.Out, "%s is working. Your message goes in at the end of the turn, in place of the one that was waiting.\n", who)
	case s.State == "waiting":
		_, _ = fmt.Fprintf(e.Out, "%s is working. Your message goes in at the end of the turn.\n", who)
	default:
		_, _ = fmt.Fprintf(e.Out, "Sent to %s.\n", who)
	}
	return nil
}
