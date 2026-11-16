package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"
)

// The browser view's two routes of docs/interfaces/api.md (DECISIONS
// I-1216). The fake mints a ticket of the real length into a cookie with
// the real attributes, so a test can check what the dashboard does with
// the answer; it serves no desktop, so the stream route answers
// `unavailable`, which is also what a real api with no VIEW_RELAY_ADDR
// answers.

// FakeViewTTL is the ticket's life, as the api's own viewTicketTTL.
const FakeViewTTL = 15 * time.Minute

func (f *Fake) mintBrowserView(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if p.State != "running" {
		return errf("conflict", "%s is %s", p.Slug, p.State)
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return errf("internal", "%v", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "repose_view",
		Value:    base64.RawURLEncoding.EncodeToString(b[:]),
		Path:     "/v1/browser/stream",
		MaxAge:   int(FakeViewTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_at":  time.Now().Add(FakeViewTTL).UTC(),
		"stream_path": "/v1/browser/stream",
		"resting":     false,
	})
	return nil
}

func (f *Fake) browserStream(http.ResponseWriter, *http.Request) *apiError {
	return errf("unavailable", "the fake api serves no desktop")
}
