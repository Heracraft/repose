package control

import (
	"context"
	"net/http"
	"net/url"

	"github.com/heracraft/repose/internal/hostd/guest"
)

// ParkBackend is the daemon's guest park (DECISIONS I-652). It is
// separate from Backend so a backend without it (the tests' fakes) needs
// nothing new; the routes exist only when the daemon has it.
type ParkBackend interface {
	// Park parks every running guest; reason is shutdown, operator or
	// scheduled_event.
	Park(ctx context.Context, reason string) ([]guest.ParkResult, error)
	// Unpark restores every parked guest now.
	Unpark(ctx context.Context) ([]guest.ParkResult, error)
}

func registerPark(mux *http.ServeMux, b Backend, writeJSON func(http.ResponseWriter, any, error)) {
	fb, ok := b.(ParkBackend)
	if !ok {
		return
	}
	park := func(w http.ResponseWriter, r *http.Request) {
		reason := r.URL.Query().Get("reason")
		switch reason {
		case "":
			reason = guest.ParkOperator
		case guest.ParkOperator, guest.ParkShutdown, guest.ParkScheduledEvent:
		default:
			http.Error(w, "reason must be operator, shutdown or scheduled_event", http.StatusBadRequest)
			return
		}
		res, err := fb.Park(r.Context(), reason)
		writeJSON(w, res, err)
	}
	unpark := func(w http.ResponseWriter, r *http.Request) {
		res, err := fb.Unpark(r.Context())
		writeJSON(w, res, err)
	}
	mux.HandleFunc("POST /park", park)
	mux.HandleFunc("POST /unpark", unpark)
	// The names before I-1002, kept for one release because
	// docs/interfaces/host-conventions.md is the contract for this socket
	// and the rule is that the old shape stays accepted for one release.
	// Nothing in the tree calls them: the only client is this package's
	// Client, which a switch replaces together with the daemon. Drop both
	// in the release after I-1002 ships.
	mux.HandleFunc("POST /freeze", park)
	mux.HandleFunc("POST /thaw", unpark)
}

// Park asks the daemon to park every running guest.
func (c *Client) Park(ctx context.Context, reason string) ([]guest.ParkResult, error) {
	var res []guest.ParkResult
	return res, c.do(ctx, http.MethodPost, "/park?reason="+url.QueryEscape(reason), &res)
}

// Unpark asks the daemon to restore every parked guest now.
func (c *Client) Unpark(ctx context.Context) ([]guest.ParkResult, error) {
	var res []guest.ParkResult
	return res, c.do(ctx, http.MethodPost, "/unpark", &res)
}
