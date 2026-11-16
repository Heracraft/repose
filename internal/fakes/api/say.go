package api

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// `repose say` against the fake api (DECISIONS I-1144): the fake has no
// guest, so it answers the way a machine with one idle agent window
// would, and records what arrived. SetSayAnswer makes it answer
// `waiting`, or refuse with the windows it has, so the CLI's two other
// lines can be tested.

// Say is a says row as docs/interfaces/api.md "Messages for an agent"
// shows it.
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

// SetSayAgents sets the agent windows the fake's machines run. One
// window is used without `agent`; several refuse a message that names
// none; none refuses every message. The default is one, `claude`.
func (f *Fake) SetSayAgents(windows ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sayAgents = append([]string(nil), windows...)
	f.sayAgentsSet = true
}

// SetSayState sets the state the fake answers a message with
// (`delivered` or `waiting`) and whether it replaced a waiting one.
func (f *Fake) SetSayState(state string, replaced bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sayState, f.sayReplaced = state, replaced
}

// Says are the messages the fake took, oldest first.
func (f *Fake) Says() []Say {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Say(nil), f.says...)
}

// sayToAgent is POST /v1/projects/{id}/say.
func (f *Fake) sayToAgent(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	var body struct {
		Text  string `json:"text"`
		Agent string `json:"agent"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	text := strings.TrimSpace(body.Text)
	switch {
	case text == "":
		return invalid("a message with nothing in it")
	case len(text) > 4<<10:
		return invalid("a message of at most %d bytes", 4<<10).withDetail(map[string]any{"reason": "text_too_long", "limit": 4 << 10})
	}

	// The handler already runs under f.mu (Fake.ServeHTTP).
	if p.State != "running" {
		return errf("conflict", "the machine is not running").withDetail(map[string]any{"reason": "not_running"})
	}
	windows := f.sayAgents
	if !f.sayAgentsSet {
		windows = []string{"claude"}
	}
	sort.Strings(windows)
	window := body.Agent
	switch {
	case window != "" && !slices.Contains(windows, window):
		if len(windows) == 0 {
			return errf("conflict", "no agent is running on this machine").withDetail(map[string]any{"reason": "no_agent"})
		}
		return errf("conflict", "no agent in window %s; the agents here are %s", window, strings.Join(windows, ", ")).
			withDetail(map[string]any{"reason": "no_agent"})
	case window == "" && len(windows) == 0:
		return errf("conflict", "no agent is running on this machine").withDetail(map[string]any{"reason": "no_agent"})
	case window == "" && len(windows) > 1:
		return invalid("%d agents are running: %s. Name one with --agent", len(windows), strings.Join(windows, ", ")).
			withDetail(map[string]any{"reason": "several_agents"})
	case window == "":
		window = windows[0]
	}

	state := f.sayState
	if state == "" {
		state = "delivered"
	}
	s := Say{
		ID: f.nextID(), ProjectID: p.ID, Window: window, Text: text,
		Sender: userFrom(r).Handle, State: state, Replaced: f.sayReplaced,
		CreatedAt: f.now().UTC(),
	}
	f.says = append(f.says, s)
	writeJSON(w, http.StatusOK, s)
	return nil
}

