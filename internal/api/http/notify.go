package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/heracraft/repose/internal/api/notify"
)

// unsubscribeGet is the link an email carries (13-notifications.md §5.6;
// DECISIONS I-442). It changes nothing: it shows a page whose button POSTs
// the same token, so a mail scanner or link preview that fetches the link
// does not turn the user's email off. No bearer token, no session, just the
// signed token naming the user.
func (s *Server) unsubscribeGet(w http.ResponseWriter, r *http.Request) error {
	token, ok := s.unsubToken(w, r)
	if !ok {
		return nil
	}
	return s.writeReplyPage(w, http.StatusOK, replyView{Title: "Unsubscribe from repose email notifications?", Token: token, Unsubscribe: true,
		Note: "Account and billing email still arrive. You can turn notifications back on from the dashboard's notification settings."})
}

// unsubscribePost turns email notifications off: the confirmation page's
// button, or a mail client's one-click unsubscribe (RFC 8058), which POSTs
// List-Unsubscribe=One-Click to the link with the token in its query.
func (s *Server) unsubscribePost(w http.ResponseWriter, r *http.Request) error {
	token, ok := s.unsubToken(w, r)
	if !ok {
		return nil
	}
	userID, _ := s.d.Unsub.Verify(token, time.Now()) // unsubToken checked it
	if _, err := s.d.Pool.Exec(r.Context(), "update users set notify_email = false where id = $1", userID); err != nil {
		return err
	}
	// Mirror PATCH /me's behaviour (5.6's failure-mode table): a channel
	// turned off drops its already-queued rows rather than sending one
	// last batch to an inbox the user just asked to stop hearing from.
	if _, err := s.d.Pool.Exec(r.Context(),
		"delete from events_outbox where channel = 'email' and event_id in (select e.id from events e join projects p on p.id = e.project_id where p.user_id = $1)", userID); err != nil {
		return err
	}
	return s.writeReplyPage(w, http.StatusOK, replyView{Title: "You have been unsubscribed",
		Note: "repose no longer sends you agent notification email. You can turn it back on any time from the dashboard's notification settings."})
}

// unsubToken reads and verifies the token from the query or the form. A
// worn or forged token gets a plain page saying why, without echoing the
// token back.
func (s *Server) unsubToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.d.Unsub == nil {
		_ = s.writeReplyPage(w, http.StatusServiceUnavailable, replyView{Title: "Unsubscribe is not available", Note: "Turn email off from the dashboard's notification settings."})
		return "", false
	}
	token := r.FormValue("token")
	if token == "" {
		_ = s.writeReplyPage(w, http.StatusBadRequest, replyView{Title: "This link is not valid", Note: "Turn email off from the dashboard's notification settings."})
		return "", false
	}
	_, err := s.d.Unsub.Verify(token, time.Now())
	if errors.Is(err, notify.ErrUnsubExpired) {
		_ = s.writeReplyPage(w, http.StatusGone, replyView{Title: "This link has expired", Note: "Turn email off from the dashboard's notification settings."})
		return "", false
	}
	if err != nil {
		_ = s.writeReplyPage(w, http.StatusBadRequest, replyView{Title: "This link is not valid", Note: "Turn email off from the dashboard's notification settings."})
		return "", false
	}
	return token, true
}
