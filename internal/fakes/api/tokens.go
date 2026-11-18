package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// API tokens (DECISIONS I-1314..I-1320), as the real api keeps them: a
// row per token, the secret handed back once, scopes by area, an
// optional project, an end. The fake keeps the secret in memory (it is
// a test) and answers the three routes and the policy the CLI and the
// dashboard see.

// APIToken is a row of GET /me/tokens.
type APIToken struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Prefix     string        `json:"prefix"`
	Scopes     []string      `json:"scopes"`
	Project    *TokenProject `json:"project"`
	ExpiresAt  time.Time     `json:"expires_at"`
	CreatedAt  time.Time     `json:"created_at"`
	LastUsedAt *time.Time    `json:"last_used_at"`
	RevokedAt  *time.Time    `json:"revoked_at"`
}

// TokenProject names the project a token is bound to.
type TokenProject struct {
	ID   string  `json:"id"`
	Slug *string `json:"slug"`
}

type fakeToken struct {
	APIToken
	owner  string
	secret string
}

var tokenScopes = []string{"read", "projects", "config", "secrets", "triggers", "agents"}

var tokenNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (f *Fake) listTokens(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	out := []APIToken{}
	for _, t := range f.apiTokens {
		if t.owner != u.ID {
			continue
		}
		if t.RevokedAt != nil && time.Since(*t.RevokedAt) > 30*24*time.Hour {
			continue
		}
		out = append(out, t.APIToken)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].RevokedAt != nil) != (out[j].RevokedAt != nil) {
			return out[i].RevokedAt == nil
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) createToken(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ProjectID *string  `json:"project_id"`
		ExpiresIn *int     `json:"expires_in_days"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if !tokenNameRe.MatchString(body.Name) {
		return errf("invalid", "a token name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit").withDetail(map[string]any{"reason": "name"})
	}
	var scopes []string
	for _, s := range tokenScopes {
		for _, got := range body.Scopes {
			if got == s {
				scopes = append(scopes, s)
				break
			}
		}
	}
	if len(scopes) == 0 || len(scopes) != len(dedupe(body.Scopes)) {
		return errf("invalid", "scopes are one or more of %s", strings.Join(tokenScopes, ", ")).withDetail(map[string]any{"reason": "scopes"})
	}
	days := 30
	if body.ExpiresIn != nil {
		days = *body.ExpiresIn
		if days < 1 || days > 365 {
			return errf("invalid", "expires_in_days is 1 to 365").withDetail(map[string]any{"reason": "expires"})
		}
	}
	var project *TokenProject
	if body.ProjectID != nil && *body.ProjectID != "" {
		p, ok := f.projects[*body.ProjectID]
		if !ok || p.owner != u.ID || p.destroyed {
			return errf("not_found", "no project with that id")
		}
		slug := p.Slug
		project = &TokenProject{ID: p.ID, Slug: &slug}
	}
	live := 0
	for _, t := range f.apiTokens {
		if t.owner != u.ID || t.RevokedAt != nil {
			continue
		}
		if t.Name == body.Name {
			return errf("conflict", "you already have a token named %s", body.Name).withDetail(map[string]any{"reason": "name_taken"})
		}
		live++
	}
	if live >= 20 {
		return errf("invalid", "an account holds 20 tokens at most; remove one first").withDetail(map[string]any{"reason": "token_limit", "limit": 20})
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret := "rpt_" + base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().UTC()
	t := &fakeToken{owner: u.ID, secret: secret, APIToken: APIToken{
		ID: f.nextID(), Name: body.Name, Prefix: secret[:12], Scopes: scopes, Project: project,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour), CreatedAt: now,
	}}
	f.apiTokens[secret] = t
	out := map[string]any{
		"id": t.ID, "name": t.Name, "prefix": t.Prefix, "scopes": t.Scopes, "project": t.Project,
		"expires_at": t.ExpiresAt, "created_at": t.CreatedAt, "last_used_at": nil, "revoked_at": nil,
		"token": secret,
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

func (f *Fake) deleteToken(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	name := r.PathValue("name")
	for _, t := range f.apiTokens {
		if t.owner == u.ID && t.Name == name && t.RevokedAt == nil {
			now := time.Now().UTC()
			t.RevokedAt = &now
			w.WriteHeader(http.StatusNoContent)
			return nil
		}
	}
	return errf("not_found", "you have no token named %s", name)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// tokenAuth answers an `rpt_` bearer: the user, or the refusal. The
// policy is the real api's in miniature: a GET needs read, a write needs
// the area its path names, the account's own routes are never a
// token's, and a bound token sees its one project.
func (f *Fake) tokenAuth(r *http.Request, pattern, bearer string) (*userRec, *fakeToken, *apiError) {
	t, ok := f.apiTokens[bearer]
	if !ok {
		return nil, nil, errf("unauthenticated", "invalid token")
	}
	if t.RevokedAt != nil {
		return nil, nil, errf("unauthenticated", "this API token was revoked").withDetail(map[string]any{"reason": "token_revoked"})
	}
	if !t.ExpiresAt.After(time.Now()) {
		return nil, nil, errf("unauthenticated", "this API token expired").withDetail(map[string]any{"reason": "token_expired"})
	}
	u, ok := f.users[t.owner]
	if !ok {
		return nil, nil, errf("unauthenticated", "invalid token")
	}
	path := strings.TrimPrefix(strings.SplitN(pattern, " ", 2)[1], "/v1")
	never := ""
	switch {
	case path == "/me" && r.Method != http.MethodGet:
		never = "your account"
	case strings.HasPrefix(path, "/me/tokens"):
		never = "API tokens"
	case strings.HasPrefix(path, "/certs"):
		never = "SSH"
	case strings.HasPrefix(path, "/billing"), path == "/usage":
		never = "billing"
	case strings.HasSuffix(path, "/browser/view"), path == "/me/notify-test":
		never = "the browser view"
	}
	if never != "" {
		return nil, nil, errf("forbidden", "an API token cannot reach %s; log in for that", never).withDetail(map[string]any{"reason": "token_never"})
	}
	needs := ""
	if r.Method == http.MethodGet {
		if path != "/me" {
			needs = "read"
		}
	} else {
		switch {
		case strings.Contains(path, "/secrets"):
			needs = "secrets"
		case strings.Contains(path, "/config"):
			needs = "config"
		case strings.Contains(path, "/triggers"), strings.Contains(path, "/webhooks"):
			needs = "triggers"
		case strings.Contains(path, "/say"), strings.Contains(path, "/questions"):
			needs = "agents"
		default:
			needs = "projects"
		}
	}
	if needs != "" {
		has := false
		for _, s := range t.Scopes {
			if s == needs {
				has = true
			}
		}
		if !has {
			return nil, nil, errf("forbidden", "this token's scopes are %s; this call needs %s", strings.Join(t.Scopes, ", "), needs).withDetail(
				map[string]any{"reason": "token_scope", "needs": needs, "scopes": t.Scopes})
		}
	}
	if t.Project != nil {
		switch {
		case strings.HasPrefix(path, "/projects/{id}"):
			if r.PathValue("id") != t.Project.ID {
				return nil, nil, errf("forbidden", "this token is for one project, not this one").withDetail(map[string]any{"reason": "token_project", "project_id": t.Project.ID})
			}
		case path == "/me", path == "/catalog", path == "/projects" && r.Method == http.MethodGet:
		default:
			return nil, nil, errf("forbidden", "this token is for one project; this call is for the account").withDetail(map[string]any{"reason": "token_project", "project_id": t.Project.ID})
		}
	}
	now := time.Now().UTC()
	t.LastUsedAt = &now
	return u, t, nil
}

// tokenMeView is what GET /me adds when the caller is a token.
func tokenMeView(t *fakeToken) map[string]any {
	return map[string]any{"id": t.ID, "name": t.Name, "prefix": t.Prefix, "scopes": t.Scopes, "project": t.Project, "expires_at": t.ExpiresAt}
}

