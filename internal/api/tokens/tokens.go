// Package tokens is the api's own bearer: a token a user mints for an
// agent or a script, with the areas it may touch, an optional project it
// is bound to, and an end (DECISIONS I-1314..I-1320). The api stores the
// token's sha256 and hands the token itself back exactly once.
//
// A token is `rpt_` and 32 bytes of base64url (43 characters): the
// prefix is what tells `authed` to look here rather than at Logto, and
// what a secret scanner can match.
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Prefix opens every token.
const Prefix = "rpt_"

// secretBytes is the token's entropy: 256 bits, 43 characters of
// base64url after the prefix.
const secretBytes = 32

// Scopes are the areas a token may touch (I-1316). `read` is every GET a
// user may make; the others are the writes of one area. A token has at
// least one. There is no scope for the account itself (billing, the
// notification channels, certificates, other tokens): those stay with
// the login.
const (
	ScopeRead     = "read"
	ScopeProjects = "projects"
	ScopeConfig   = "config"
	ScopeSecrets  = "secrets"
	ScopeTriggers = "triggers"
	ScopeAgents   = "agents"
)

// AllScopes lists the scopes in the order they are shown.
var AllScopes = []string{ScopeRead, ScopeProjects, ScopeConfig, ScopeSecrets, ScopeTriggers, ScopeAgents}

// Means is the one line each scope's meaning is given in, in the CLI's
// help, the dashboard's dialog and the docs. One source, so the three
// agree.
var Means = map[string]string{
	ScopeRead:     "every project's status, events, questions, triggers and settings",
	ScopeProjects: "create, start, stop, resize, snapshot, restore and remove projects",
	ScopeConfig:   "change machine.nix and a project's config",
	ScopeSecrets:  "set and remove secrets (never read a value)",
	ScopeTriggers: "make, change, fire and remove triggers and webhook routes",
	ScopeAgents:   "message an agent and answer its questions",
}

// ValidScope says whether s is one of AllScopes.
func ValidScope(s string) bool {
	_, ok := Means[s]
	return ok
}

// NormalizeScopes sorts, dedupes and checks a scope list; the bool is
// false when a scope is unknown or the list is empty.
func NormalizeScopes(in []string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !ValidScope(s) {
			return nil, false
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	// AllScopes' order, not the alphabet's, so `read` leads.
	rank := map[string]int{}
	for i, s := range AllScopes {
		rank[s] = i
	}
	sort.Slice(out, func(i, j int) bool { return rank[out[i]] < rank[out[j]] })
	return out, true
}

// Names match webhook routes' and triggers': short, lower, and typed on
// a command line.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidName says whether a token name is one.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// MaxLive is how many live tokens an account may hold, the webhook
// routes' twenty (I-1114).
const MaxLive = 20

// Lifetime bounds (I-1320): a token lives 30 days unless asked, and a
// year at most.
const (
	DefaultTTL = 30 * 24 * time.Hour
	MaxTTL     = 365 * 24 * time.Hour
)

// Token is one row, without the secret.
type Token struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Prefix     string
	Scopes     []string
	ProjectID  *uuid.UUID
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Has says whether the token carries scope.
func (t *Token) Has(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Errors Lookup answers with. The caller turns each into
// `unauthenticated`; the words differ so the holder learns which.
var (
	ErrNotFound = errors.New("no such token")
	ErrExpired  = errors.New("token expired")
	ErrRevoked  = errors.New("token revoked")
	ErrTooMany  = errors.New("too many tokens")
	ErrName     = errors.New("a token by that name exists")
)

// New mints a token: the secret the caller shows once, and the prefix
// and hash the row keeps.
func New() (secret, prefix string, hash []byte, err error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", nil, err
	}
	secret = Prefix + base64.RawURLEncoding.EncodeToString(b)
	return secret, secret[:12], Hash(secret), nil
}

// Hash is the lookup form: the sha256 of the token's characters.
func Hash(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// IsToken says whether a bearer is one of ours rather than Logto's.
func IsToken(bearer string) bool { return strings.HasPrefix(bearer, Prefix) }

// Store is the table.
type Store struct {
	pool *db.Pool
	now  func() time.Time

	// touched throttles last_used_at: one write a minute per token, so a
	// polling agent does not turn every GET into an UPDATE.
	mu      sync.Mutex
	touched map[uuid.UUID]time.Time
}

// New builds a Store.
func NewStore(pool *db.Pool) *Store {
	return &Store{pool: pool, now: time.Now, touched: map[uuid.UUID]time.Time{}}
}

// SetNow is for tests.
func (s *Store) SetNow(f func() time.Time) { s.now = f }

const cols = `id, user_id, name, prefix, scopes, project_id, expires_at, created_at, last_used_at, revoked_at`

func scan(row pgx.Row) (*Token, error) {
	var t Token
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Scopes, &t.ProjectID, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// Create mints and stores a token. ttl is clamped to [1 minute, MaxTTL];
// 0 means DefaultTTL. ErrName when a live token has the name, ErrTooMany
// past MaxLive.
func (s *Store) Create(ctx context.Context, userID uuid.UUID, name string, scopes []string, projectID *uuid.UUID, ttl time.Duration) (*Token, string, error) {
	switch {
	case ttl == 0:
		ttl = DefaultTTL
	case ttl < time.Minute:
		ttl = time.Minute
	case ttl > MaxTTL:
		ttl = MaxTTL
	}
	secret, prefix, hash, err := New()
	if err != nil {
		return nil, "", err
	}
	var tok *Token
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the account's live rows so two creates cannot both pass the
		// count or the name check.
		rows, err := tx.Query(ctx, `select name from api_tokens where user_id = $1 and revoked_at is null for update`, userID)
		if err != nil {
			return err
		}
		live := 0
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			if n == name {
				rows.Close()
				return ErrName
			}
			live++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if live >= MaxLive {
			return ErrTooMany
		}
		now := s.now().UTC()
		t, err := scan(tx.QueryRow(ctx, `insert into api_tokens (id, user_id, name, prefix, token_sha256, scopes, project_id, expires_at, created_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning `+cols,
			store.NewID(), userID, name, prefix, hash, scopes, projectID, now.Add(ttl), now))
		if err != nil {
			return err
		}
		tok = t
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return tok, secret, nil
}

// List is the account's tokens, live first then revoked within 30 days,
// newest first within each.
func (s *Store) List(ctx context.Context, userID uuid.UUID) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from api_tokens
		where user_id = $1 and (revoked_at is null or revoked_at > now() - interval '30 days')
		order by (revoked_at is not null), created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Revoke ends the live token by name; false when the account has none.
func (s *Store) Revoke(ctx context.Context, userID uuid.UUID, name string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update api_tokens set revoked_at = now() where user_id = $1 and name = $2 and revoked_at is null`, userID, name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Lookup finds a live token by its secret. ErrNotFound, ErrExpired or
// ErrRevoked say why not.
func (s *Store) Lookup(ctx context.Context, secret string) (*Token, error) {
	t, err := scan(s.pool.QueryRow(ctx, `select `+cols+` from api_tokens where token_sha256 = $1`, Hash(secret)))
	if err != nil {
		return nil, err
	}
	switch {
	case t.RevokedAt != nil:
		return nil, ErrRevoked
	case !t.ExpiresAt.After(s.now()):
		return nil, ErrExpired
	}
	return t, nil
}

// Touch records a use, at most once a minute per token.
func (s *Store) Touch(ctx context.Context, id uuid.UUID) {
	now := s.now()
	s.mu.Lock()
	if last, ok := s.touched[id]; ok && now.Sub(last) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.touched[id] = now
	// Keep the map to the tokens seen lately.
	if len(s.touched) > 10000 {
		for k, v := range s.touched {
			if now.Sub(v) > time.Hour {
				delete(s.touched, k)
			}
		}
	}
	s.mu.Unlock()
	_, _ = s.pool.Exec(ctx, `update api_tokens set last_used_at = $2 where id = $1`, id, now.UTC())
}
