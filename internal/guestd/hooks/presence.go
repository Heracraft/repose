package hooks

import (
	"context"
	"encoding/json"
	"net/http"
)

// Presence answers whether the owner is at the machine, by the definition
// the presence hold uses (DECISIONS I-689): a tmux client or an SSH
// session, a laptop herdr bridge counting as an SSH session (I-511).
type Presence func(ctx context.Context) bool

// PresenceState is GET /presence. State is "attached" or "away", the two
// words `repose-presence` prints.
type PresenceState struct {
	State    string `json:"state"`
	Attached bool   `json:"attached"`
}

// The two states, as the route answers them and the guest command prints
// them (DECISIONS I-1098).
const (
	StateAttached = "attached"
	StateAway     = "away"
)

// SetPresence serves GET /presence from p. Without it the hook socket's
// catch-all refuses the request, and `repose-presence` exits 1 rather
// than guessing.
func (s *Server) SetPresence(p Presence) {
	s.presence = p
	s.mux.HandleFunc("GET /presence", s.handlePresence)
}

// handlePresence reads the owner's presence now. It takes no payload and
// returns no tenant content: two numbers the sample already counts decide
// one word.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	if s.presence == nil {
		s.reject(w, r, http.StatusServiceUnavailable, "no_presence_source")
		return
	}
	out := PresenceState{State: StateAway}
	if out.Attached = s.presence(r.Context()); out.Attached {
		out.State = StateAttached
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out) // the caller went away
}
