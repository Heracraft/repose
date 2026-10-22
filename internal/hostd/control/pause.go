package control

import (
	"context"
	"net/http"
	"net/url"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// PauseBackend is the daemon's idle pause (DECISIONS I-818). It is
// separate from Backend, as FreezeBackend is, so a backend without it
// (the tests' fakes) needs nothing new.
type PauseBackend interface {
	// PauseOne stops one guest's vCPUs, its memory left resident. A
	// guest it will not pause is answered paused=false with a why_not.
	PauseOne(ctx context.Context, guestID, reason string) (*hostdv1.PauseResult, error)
	// WakeOne resumes a resting guest; one already running answers
	// was_resting=false.
	WakeOne(ctx context.Context, guestID, reason string) (*hostdv1.WakeResult, error)
}

func registerPause(mux *http.ServeMux, b Backend, writeJSON func(http.ResponseWriter, any, error)) {
	pb, ok := b.(PauseBackend)
	if !ok {
		return
	}
	mux.HandleFunc("POST /pause", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("guest")
		if id == "" {
			http.Error(w, "guest required", http.StatusBadRequest)
			return
		}
		reason := r.URL.Query().Get("reason")
		if reason == "" {
			reason = "operator"
		}
		res, err := pb.PauseOne(r.Context(), id, reason)
		writeJSON(w, res, err)
	})
	mux.HandleFunc("POST /wake", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("guest")
		if id == "" {
			http.Error(w, "guest required", http.StatusBadRequest)
			return
		}
		reason := r.URL.Query().Get("reason")
		if reason == "" {
			reason = "operator"
		}
		res, err := pb.WakeOne(r.Context(), id, reason)
		writeJSON(w, res, err)
	})
}

// Pause asks the daemon to pause one guest.
func (c *Client) Pause(ctx context.Context, guestID, reason string) (*hostdv1.PauseResult, error) {
	var res hostdv1.PauseResult
	err := c.do(ctx, http.MethodPost, "/pause?guest="+url.QueryEscape(guestID)+"&reason="+url.QueryEscape(reason), &res)
	return &res, err
}

// Wake asks the daemon to wake one guest.
func (c *Client) Wake(ctx context.Context, guestID, reason string) (*hostdv1.WakeResult, error) {
	var res hostdv1.WakeResult
	err := c.do(ctx, http.MethodPost, "/wake?guest="+url.QueryEscape(guestID)+"&reason="+url.QueryEscape(reason), &res)
	return &res, err
}
