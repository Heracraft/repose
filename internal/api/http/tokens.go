package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/tokens"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// API tokens (DECISIONS I-1314..I-1320): a bearer the user mints for an
// agent or a script. `authed` takes one in place of a Logto token; this
// file is the three routes that make, list and end them, and the policy
// that says which route each scope opens.

// tokenRouteScope is the scope a user route needs from a token, by its
// mux pattern. "" means any token: the route is how a caller learns who
// it is. A pattern absent here and absent from tokenNever is a route the
// policy forgot, which TestEveryUserRouteHasATokenPolicy refuses.
var tokenRouteScope = map[string]string{
	"GET /v1/me":      "",
	"GET /v1/catalog": tokens.ScopeRead,
	// Reads.
	"GET /v1/me/config":                           tokens.ScopeRead,
	"GET /v1/me/config/revisions":                 tokens.ScopeRead,
	"GET /v1/me/secrets":                          tokens.ScopeRead,
	"GET /v1/hosts":                               tokens.ScopeRead,
	"GET /v1/hosts/{id}/ops/{op_id}":              tokens.ScopeRead,
	"GET /v1/projects":                            tokens.ScopeRead,
	"GET /v1/projects/destroyed":                  tokens.ScopeRead,
	"GET /v1/projects/{id}":                       tokens.ScopeRead,
	"GET /v1/projects/{id}/ops/{op_id}":           tokens.ScopeRead,
	"GET /v1/projects/{id}/ops/{op_id}/log":       tokens.ScopeRead,
	"GET /v1/projects/{id}/route":                 tokens.ScopeRead,
	"GET /v1/projects/{id}/samples":               tokens.ScopeRead,
	"GET /v1/projects/{id}/config":                tokens.ScopeRead,
	"GET /v1/projects/{id}/config/revisions":      tokens.ScopeRead,
	"GET /v1/projects/{id}/secrets":               tokens.ScopeRead,
	"GET /v1/projects/{id}/webhooks":              tokens.ScopeRead,
	"GET /v1/projects/{id}/webhooks/{name}":       tokens.ScopeRead,
	"GET /v1/projects/{id}/triggers":              tokens.ScopeRead,
	"GET /v1/projects/{id}/triggers/{name}":       tokens.ScopeRead,
	"GET /v1/trigger-runs":                        tokens.ScopeRead,
	"GET /v1/projects/{id}/trigger-runs":          tokens.ScopeRead,
	"GET /v1/projects/{id}/trigger-runs/{run_id}": tokens.ScopeRead,
	"GET /v1/snapshots":                           tokens.ScopeRead,
	"GET /v1/projects/{id}/snapshots":             tokens.ScopeRead,
	"GET /v1/projects/{id}/events":                tokens.ScopeRead,
	"GET /v1/projects/{id}/logs":                  tokens.ScopeRead,
	"GET /v1/projects/{id}/says":                  tokens.ScopeRead,
	"GET /v1/questions":                           tokens.ScopeRead,
	"GET /v1/projects/{id}/questions":             tokens.ScopeRead,
	// Projects: their life and their disks.
	"POST /v1/projects":                              tokens.ScopeProjects,
	"POST /v1/projects/restore":                      tokens.ScopeProjects,
	"PATCH /v1/projects/{id}":                        tokens.ScopeProjects,
	"DELETE /v1/projects/{id}":                       tokens.ScopeProjects,
	"POST /v1/projects/{id}/start":                   tokens.ScopeProjects,
	"POST /v1/projects/{id}/stop":                    tokens.ScopeProjects,
	"POST /v1/projects/{id}/wake":                    tokens.ScopeProjects,
	"POST /v1/projects/{id}/resize":                  tokens.ScopeProjects,
	"POST /v1/projects/{id}/snapshots":               tokens.ScopeProjects,
	"POST /v1/projects/{id}/snapshots/{sid}/restore": tokens.ScopeProjects,
	"POST /v1/projects/{id}/fork":                    tokens.ScopeProjects,
	// Config: machine.nix and a project's config.
	"PUT /v1/me/config":                                   tokens.ScopeConfig,
	"PUT /v1/projects/{id}/config":                        tokens.ScopeConfig,
	"POST /v1/projects/{id}/config/revisions/{rev}/apply": tokens.ScopeConfig,
	// Secrets: set and remove, never read.
	"PUT /v1/me/secrets/{name}":               tokens.ScopeSecrets,
	"DELETE /v1/me/secrets/{name}":            tokens.ScopeSecrets,
	"PUT /v1/projects/{id}/secrets/{name}":    tokens.ScopeSecrets,
	"DELETE /v1/projects/{id}/secrets/{name}": tokens.ScopeSecrets,
	// Triggers and the webhook routes they stand on.
	"POST /v1/projects/{id}/webhooks":               tokens.ScopeTriggers,
	"DELETE /v1/projects/{id}/webhooks/{name}":      tokens.ScopeTriggers,
	"POST /v1/projects/{id}/webhooks/{name}/rotate": tokens.ScopeTriggers,
	"POST /v1/projects/{id}/triggers":               tokens.ScopeTriggers,
	"DELETE /v1/projects/{id}/triggers/{name}":      tokens.ScopeTriggers,
	"POST /v1/projects/{id}/triggers/{name}/fire":   tokens.ScopeTriggers,
	// Agents: a message, and their questions.
	"POST /v1/projects/{id}/say":                    tokens.ScopeAgents,
	"POST /v1/projects/{id}/questions/{qid}/answer": tokens.ScopeAgents,
	"POST /v1/projects/{id}/questions/{qid}/cancel": tokens.ScopeAgents,
	"POST /v1/projects/{id}/questions/{qid}/hold":   tokens.ScopeAgents,
}

// tokenNever lists the user routes no token opens, with the reason the
// refusal prints: the account itself, money, SSH, the browser view, and
// tokens (a token that mints tokens has no end). Adding a host hands
// out a join token, which is control of a machine, and removing one
// retires it, so both take a login (I-1366); listing them is a read.
var tokenNever = map[string]string{
	"PATCH /v1/me":                        "your account's settings",
	"DELETE /v1/me":                       "your account",
	"POST /v1/me/notify-test":             "your notification channels",
	"POST /v1/certs":                      "SSH",
	"POST /v1/certs/revoke":               "SSH",
	"POST /v1/projects/{id}/browser/view": "the browser view",
	"GET /v1/usage":                       "billing",
	"GET /v1/billing":                     "billing",
	"POST /v1/billing/checkout":           "billing",
	"POST /v1/billing/plan":               "billing",
	"POST /v1/billing/cancel":             "billing",
	"POST /v1/billing/resume":             "billing",
	"POST /v1/billing/portal":             "billing",
	"GET /v1/billing/invoices":            "billing",
	"POST /v1/billing/waitlist":           "billing",
	"POST /v1/hosts":                      "your hosts",
	"DELETE /v1/hosts/{id}":               "your hosts",
	"POST /v1/hosts/{id}/restart":         "your hosts",
	"GET /v1/me/tokens":                   "API tokens",
	"POST /v1/me/tokens":                  "API tokens",
	"DELETE /v1/me/tokens/{name}":         "API tokens",
}

// tokenAccountWide lists the routes a project-bound token may still
// call although their path names no project: who am I, the catalog, and
// the project list, which listProjects narrows to the one project.
var tokenAccountWide = map[string]bool{
	"GET /v1/me":       true,
	"GET /v1/catalog":  true,
	"GET /v1/projects": true,
}

// tokenCheck is the policy: the route's scope against the token's, and
// the token's project against the path's. nil when the call may go on.
func tokenCheck(r *http.Request, t *tokens.Token) error {
	pattern := r.Pattern
	if why, ok := tokenNever[pattern]; ok {
		return withDetail(errf("forbidden", "an API token cannot reach %s; log in for that", why), map[string]any{"reason": "token_never"})
	}
	scope, ok := tokenRouteScope[pattern]
	if !ok {
		// A route the policy does not know is closed to tokens until it
		// is named: the safe side of a forgotten line.
		return withDetail(errf("forbidden", "an API token cannot reach this route"), map[string]any{"reason": "token_never"})
	}
	if scope != "" && !t.Has(scope) {
		return withDetail(errf("forbidden", "this token's scopes are %s; this call needs %s", strings.Join(t.Scopes, ", "), scope),
			map[string]any{"reason": "token_scope", "needs": scope, "scopes": t.Scopes})
	}
	if t.ProjectID == nil {
		return nil
	}
	if strings.Contains(pattern, "{id}") {
		id := r.PathValue("id")
		if id != t.ProjectID.String() {
			return withDetail(errf("forbidden", "this token is for one project, not this one"), map[string]any{"reason": "token_project", "project_id": t.ProjectID.String()})
		}
		return nil
	}
	if !tokenAccountWide[pattern] {
		return withDetail(errf("forbidden", "this token is for one project; this call is for the account"), map[string]any{"reason": "token_project", "project_id": t.ProjectID.String()})
	}
	return nil
}

// tokenFrom is the token the request authenticated with, nil for a
// login.
func tokenFrom(ctx context.Context) *tokens.Token {
	t, _ := ctx.Value(tokenKey).(*tokens.Token)
	return t
}

// authToken is `authed`'s branch for an `rpt_` bearer: the row, its
// user, and the policy. It answers the user to go on as, or the error to
// write.
func (s *Server) authToken(r *http.Request, bearer string) (*store.User, *tokens.Token, error) {
	if s.d.Tokens == nil {
		return nil, nil, errf("unauthenticated", "invalid token")
	}
	t, err := s.d.Tokens.Lookup(r.Context(), bearer)
	if err != nil {
		switch {
		case errors.Is(err, tokens.ErrExpired):
			return nil, nil, withDetail(errf("unauthenticated", "this API token expired"), map[string]any{"reason": "token_expired"})
		case errors.Is(err, tokens.ErrRevoked):
			return nil, nil, withDetail(errf("unauthenticated", "this API token was revoked"), map[string]any{"reason": "token_revoked"})
		case errors.Is(err, tokens.ErrNotFound):
			return nil, nil, errf("unauthenticated", "invalid token")
		}
		return nil, nil, err
	}
	u, err := store.GetUser(r.Context(), s.d.Pool, t.UserID)
	if err != nil {
		return nil, nil, err
	}
	if err := tokenCheck(r, t); err != nil {
		return nil, nil, err
	}
	s.d.Tokens.Touch(r.Context(), t.ID)
	return u, t, nil
}

// tokenJSON is a token as the api shows it: everything but the secret.
func (s *Server) tokenJSON(ctx context.Context, t *tokens.Token) (map[string]any, error) {
	j := map[string]any{
		"id": t.ID.String(), "name": t.Name, "prefix": t.Prefix, "scopes": t.Scopes,
		"project": nil, "expires_at": t.ExpiresAt.UTC(), "created_at": t.CreatedAt.UTC(),
		"last_used_at": nil, "revoked_at": nil,
	}
	if t.LastUsedAt != nil {
		j["last_used_at"] = t.LastUsedAt.UTC()
	}
	if t.RevokedAt != nil {
		j["revoked_at"] = t.RevokedAt.UTC()
	}
	if t.ProjectID != nil {
		p, err := store.GetUserProjectAny(ctx, s.d.Pool, t.UserID, *t.ProjectID)
		switch {
		case err == nil:
			j["project"] = map[string]any{"id": p.ID.String(), "slug": p.Slug}
		case errors.Is(err, db.ErrNotFound):
			// The project went with its row (cascade), so this is a token
			// that was read in the same instant; name the id alone.
			j["project"] = map[string]any{"id": t.ProjectID.String(), "slug": nil}
		default:
			return nil, err
		}
	}
	return j, nil
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	list, err := s.d.Tokens.List(r.Context(), u.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		j, err := s.tokenJSON(r.Context(), &list[i])
		if err != nil {
			return err
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ProjectID *string  `json:"project_id"`
		ExpiresIn *int     `json:"expires_in_days"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if !tokens.ValidName(body.Name) {
		return withDetail(errf("invalid", "a token name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit"), map[string]any{"reason": "name"})
	}
	scopes, ok := tokens.NormalizeScopes(body.Scopes)
	if !ok {
		return withDetail(errf("invalid", "scopes are one or more of %s", strings.Join(tokens.AllScopes, ", ")), map[string]any{"reason": "scopes"})
	}
	ttl := tokens.DefaultTTL
	if body.ExpiresIn != nil {
		days := *body.ExpiresIn
		if days < 1 || time.Duration(days)*24*time.Hour > tokens.MaxTTL {
			return withDetail(errf("invalid", "expires_in_days is 1 to 365"), map[string]any{"reason": "expires"})
		}
		ttl = time.Duration(days) * 24 * time.Hour
	}
	var projectID *uuid.UUID
	if body.ProjectID != nil && *body.ProjectID != "" {
		id, err := uuid.Parse(*body.ProjectID)
		if err != nil {
			return withDetail(errf("invalid", "project_id is not an id"), map[string]any{"reason": "project"})
		}
		if _, err := store.GetUserProject(r.Context(), s.d.Pool, u.ID, id); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return errf("not_found", "no project with that id")
			}
			return err
		}
		projectID = &id
	}
	t, secret, err := s.d.Tokens.Create(r.Context(), u.ID, body.Name, scopes, projectID, ttl)
	if err != nil {
		switch {
		case errors.Is(err, tokens.ErrName):
			return withDetail(errf("conflict", "you already have a token named %s", body.Name), map[string]any{"reason": "name_taken"})
		case errors.Is(err, tokens.ErrTooMany):
			return withDetail(errf("invalid", "an account holds %d tokens at most; remove one first", tokens.MaxLive), map[string]any{"reason": "token_limit", "limit": tokens.MaxLive})
		}
		return err
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "token_create", "token:"+t.ID.String(), map[string]any{"name": t.Name, "scopes": t.Scopes, "project_id": body.ProjectID, "expires_at": t.ExpiresAt}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("api token created", "event", "token_create", "user_id", u.ID.String(), "token_id", t.ID.String(), "scopes", strings.Join(t.Scopes, ","))
	j, err := s.tokenJSON(r.Context(), t)
	if err != nil {
		return err
	}
	// The one answer that carries the token.
	j["token"] = secret
	writeJSON(w, http.StatusCreated, j)
	return nil
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	name := r.PathValue("name")
	ok, err := s.d.Tokens.Revoke(r.Context(), u.ID, name)
	if err != nil {
		return err
	}
	if !ok {
		return errf("not_found", "you have no token named %s", name)
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "token_revoke", "user:"+u.ID.String(), map[string]any{"name": name}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("api token revoked", "event", "token_revoke", "user_id", u.ID.String())
	w.WriteHeader(http.StatusNoContent)
	return nil
}
