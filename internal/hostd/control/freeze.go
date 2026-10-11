package control

import (
	"context"
	"net/http"
	"net/url"

	"github.com/heracraft/repose/internal/hostd/guest"
)

// FreezeBackend is the daemon's guest freeze (DECISIONS I-652). It is
// separate from Backend so a backend without it (the tests' fakes) needs
// nothing new; the routes exist only when the daemon has it.
type FreezeBackend interface {
	// Freeze freezes every running guest; reason is shutdown, operator or
	// scheduled_event.
	Freeze(ctx context.Context, reason string) ([]guest.FreezeResult, error)
	// Thaw restores every frozen guest now.
	Thaw(ctx context.Context) ([]guest.FreezeResult, error)
}

func registerFreeze(mux *http.ServeMux, b Backend, writeJSON func(http.ResponseWriter, any, error)) {
	fb, ok := b.(FreezeBackend)
	if !ok {
		return
	}
	mux.HandleFunc("POST /freeze", func(w http.ResponseWriter, r *http.Request) {
		reason := r.URL.Query().Get("reason")
		switch reason {
		case "":
			reason = guest.FreezeOperator
		case guest.FreezeOperator, guest.FreezeShutdown, guest.FreezeScheduledEvent:
		default:
			http.Error(w, "reason must be operator, shutdown or scheduled_event", http.StatusBadRequest)
			return
		}
		res, err := fb.Freeze(r.Context(), reason)
		writeJSON(w, res, err)
	})
	mux.HandleFunc("POST /thaw", func(w http.ResponseWriter, r *http.Request) {
		res, err := fb.Thaw(r.Context())
		writeJSON(w, res, err)
	})
}

// Freeze asks the daemon to freeze every running guest.
func (c *Client) Freeze(ctx context.Context, reason string) ([]guest.FreezeResult, error) {
	var res []guest.FreezeResult
	return res, c.do(ctx, http.MethodPost, "/freeze?reason="+url.QueryEscape(reason), &res)
}

// Thaw asks the daemon to restore every frozen guest now.
func (c *Client) Thaw(ctx context.Context) ([]guest.FreezeResult, error) {
	var res []guest.FreezeResult
	return res, c.do(ctx, http.MethodPost, "/thaw", &res)
}
