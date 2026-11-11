// Package webhooks owns a project's public webhook routes
// (docs/features/webhooks.md, DECISIONS I-1110): the rows the CLI and the
// dashboard write, the token each route is reached by, the table the edge
// syncs, and the delivery records the edge reports back.
//
// A route is per-project server state, as keep_awake is (I-820). The
// token lives as ciphertext under the account's data key, with the same
// envelope as a named secret, and as its sha256 for the edge's lookup;
// nothing holds it in plain text, so the two homes of "Secrets have three
// homes" are the only ones it has.
package webhooks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/db"
)

// MaxRoutes is the routes one project may have (the proposal's §3.9).
const MaxRoutes = 20

// TombstoneWindow is how long a rotated or removed token answers 410
// before it answers 404.
const TombstoneWindow = 30 * 24 * time.Hour

// StartInterval is the least time between two deliveries starting one
// project's stopped machine (the `--start` rule, §3.6).
const StartInterval = 10 * time.Minute

// DeliveriesKept is how many deliveries a route keeps.
const DeliveriesKept = 20

// ErrInvalidName, ErrInvalidTarget and ErrTooMany are the refusals a
// caller turns into its own message.
var (
	ErrInvalidName   = errors.New("a route name matches [a-z0-9-]{1,32}")
	ErrInvalidTarget = errors.New("a target is PORT or PORT/PATH")
	ErrTooMany       = fmt.Errorf("a project may have %d webhook routes", MaxRoutes)
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidName reports whether name is a usable route name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Outcomes are the words a delivery ends with (§3.10). The api refuses
// anything else, so no word the edge was handed by a sender can reach a
// row or a log line.
var Outcomes = map[string]bool{
	"delivered": true, "woke": true, "stopped": true, "started": true,
	"start_refused": true, "wake_failed": true, "no_listener": true,
	"timeout": true, "too_large": true, "rate_limited": true,
	"body_dropped": true, "unavailable": true,
}

// ParseTarget reads `3000` or `3000/api/webhooks/stripe` into a port and
// a path. The path always starts with a slash.
func ParseTarget(s string) (port int, path string, err error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, ":")
	portPart, rest, hasPath := strings.Cut(s, "/")
	port, err = strconv.Atoi(portPart)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", ErrInvalidTarget
	}
	if !hasPath {
		return port, "/", nil
	}
	path = "/" + rest
	if strings.ContainsAny(path, " \t\r\n?#") {
		return 0, "", ErrInvalidTarget
	}
	return port, path, nil
}

// Target renders a route's port and path the way the CLI prints it.
func Target(port int, path string) string {
	if path == "" || path == "/" {
		return ":" + strconv.Itoa(port) + "/"
	}
	return ":" + strconv.Itoa(port) + path
}

// Route is one row of webhook_routes with its newest delivery.
type Route struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	Name       string
	Port       int
	Path       string
	AllowStart bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// Last is the newest delivery, or nil for a route nothing has used.
	Last *Delivery
}

// Delivery is one record the edge reported.
type Delivery struct {
	At         time.Time
	Status     int
	Outcome    string
	DurationMs int
	BytesIn    int64
	BytesOut   int64
}

// EdgeRoute is one row of the table the edge holds in memory. It carries
// the token's hash, never the token: a copy of the edge's memory gives no
// working URL.
type EdgeRoute struct {
	RouteID    string `json:"route_id"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	AllowStart bool   `json:"allow_start"`
	TokenHash  string `json:"token_sha256"`
}

// EdgeTable is GET /internal/webhook-routes.
type EdgeTable struct {
	Version    string      `json:"version"`
	Routes     []EdgeRoute `json:"routes"`
	Tombstones []string    `json:"tombstones"`
	// Unchanged is true when the caller's since= matches Version; Routes
	// and Tombstones are then empty.
	Unchanged bool `json:"unchanged,omitempty"`
}

// TokenBytes is the token's entropy: 128 bits, 22 characters of base64url.
const TokenBytes = 16

// NewToken mints an unguessable token.
func NewToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is the lookup form: the hex sha256 of the token's characters.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Store is the service behind the webhook routes.
type Store struct {
	pool *db.Pool
	sec  *secrets.Store
	// Hostname is the public name the URLs are built on; empty means the
	// documented default.
	hostname string
	now      func() time.Time
}

// DefaultHostname is the one public name webhook routes answer on. It is
// kept for good: a URL pasted into a provider's dashboard outlives a
// change of product domain (R4-12, DECISIONS I-1110).
const DefaultHostname = "hooks.repose.herakraft.co"

// New builds a store. hostname empty takes DefaultHostname.
func New(pool *db.Pool, sec *secrets.Store, hostname string) *Store {
	if hostname == "" {
		hostname = DefaultHostname
	}
	return &Store{pool: pool, sec: sec, hostname: hostname, now: time.Now}
}

// Hostname is the public name URLs are built on.
func (s *Store) Hostname() string { return s.hostname }

// URL is the public URL of a route with a known token.
func (s *Store) URL(projectID, name, token string) string {
	return "https://" + s.hostname + "/" + projectID + "/" + name + "/" + token
}

// List returns a project's routes, oldest first, each with its newest
// delivery.
func (s *Store) List(ctx context.Context, projectID uuid.UUID) ([]Route, error) {
	rows, err := s.pool.Query(ctx, `select r.id, r.project_id, r.name, r.port, r.path, r.allow_start, r.created_at, r.updated_at,
			d.at, d.status, d.outcome, d.duration_ms, d.bytes_in, d.bytes_out
		from webhook_routes r
		left join lateral (
			select at, status, outcome, duration_ms, bytes_in, bytes_out
			from webhook_deliveries where route_id = r.id order by at desc limit 1
		) d on true
		where r.project_id = $1 order by r.created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var r Route
		var at *time.Time
		var status, durMs *int
		var outcome *string
		var in, bout *int64
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Port, &r.Path, &r.AllowStart, &r.CreatedAt, &r.UpdatedAt,
			&at, &status, &outcome, &durMs, &in, &bout); err != nil {
			return nil, err
		}
		if at != nil {
			r.Last = &Delivery{At: *at, Status: deref(status), Outcome: derefStr(outcome), DurationMs: deref(durMs), BytesIn: deref64(in), BytesOut: deref64(bout)}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Deliveries returns a route's kept deliveries, newest first.
func (s *Store) Deliveries(ctx context.Context, routeID uuid.UUID) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx, `select at, status, outcome, duration_ms, bytes_in, bytes_out
		from webhook_deliveries where route_id = $1 order by at desc limit $2`, routeID, DeliveriesKept)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.At, &d.Status, &d.Outcome, &d.DurationMs, &d.BytesIn, &d.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Get returns one route by name.
func (s *Store) Get(ctx context.Context, projectID uuid.UUID, name string) (*Route, error) {
	var r Route
	err := s.pool.QueryRow(ctx, `select id, project_id, name, port, path, allow_start, created_at, updated_at
		from webhook_routes where project_id = $1 and name = $2`, projectID, name).
		Scan(&r.ID, &r.ProjectID, &r.Name, &r.Port, &r.Path, &r.AllowStart, &r.CreatedAt, &r.UpdatedAt)
	if db.IsNoRows(err) {
		return nil, db.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Token decrypts a route's token so `repose webhooks url` can print it.
func (s *Store) Token(ctx context.Context, projectID uuid.UUID, name string) (string, error) {
	var ct, wrapped []byte
	var version string
	err := s.pool.QueryRow(ctx, `select ciphertext, dek_wrapped, kv_key_version from webhook_routes where project_id = $1 and name = $2`,
		projectID, name).Scan(&ct, &wrapped, &version)
	if db.IsNoRows(err) {
		return "", db.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	v, err := s.sec.OpenValue(ctx, wrapped, version, projectID.String(), tokenAAD(name), ct)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// tokenAAD binds a token's ciphertext to its route name, in a namespace
// no secret name can collide with: a secret name is upper-case.
func tokenAAD(name string) string { return "webhook:" + name }

// Upsert adds a route or changes an existing one's target. An existing
// route keeps its token, so a URL already pasted into a provider's
// dashboard goes on working. start is nil to leave --start as it is.
func (s *Store) Upsert(ctx context.Context, userID, projectID uuid.UUID, name string, port int, path string, start *bool) (route *Route, token string, created bool, err error) {
	if !ValidName(name) {
		return nil, "", false, ErrInvalidName
	}
	if port < 1 || port > 65535 {
		return nil, "", false, ErrInvalidTarget
	}
	if path == "" {
		path = "/"
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var existing uuid.UUID
		var allow bool
		qerr := tx.QueryRow(ctx, `select id, allow_start from webhook_routes where project_id = $1 and name = $2 for update`, projectID, name).Scan(&existing, &allow)
		switch {
		case qerr == nil:
			if start != nil {
				allow = *start
			}
			var r Route
			if err := tx.QueryRow(ctx, `update webhook_routes set port = $2, path = $3, allow_start = $4 where id = $1
				returning id, project_id, name, port, path, allow_start, created_at, updated_at`, existing, port, path, allow).
				Scan(&r.ID, &r.ProjectID, &r.Name, &r.Port, &r.Path, &r.AllowStart, &r.CreatedAt, &r.UpdatedAt); err != nil {
				return err
			}
			route = &r
			return nil
		case !db.IsNoRows(qerr):
			return qerr
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from webhook_routes where project_id = $1`, projectID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxRoutes {
			return ErrTooMany
		}
		t, err := NewToken()
		if err != nil {
			return err
		}
		ct, wrapped, version, err := s.sec.SealValue(ctx, tx, userID.String(), projectID.String(), tokenAAD(name), []byte(t))
		if err != nil {
			return err
		}
		allow = start != nil && *start
		var r Route
		if err := tx.QueryRow(ctx, `insert into webhook_routes (id, project_id, name, port, path, allow_start, token_sha256, ciphertext, dek_wrapped, kv_key_version)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			returning id, project_id, name, port, path, allow_start, created_at, updated_at`,
			uuid.Must(uuid.NewV7()), projectID, name, port, path, allow, HashToken(t), ct, wrapped, version).
			Scan(&r.ID, &r.ProjectID, &r.Name, &r.Port, &r.Path, &r.AllowStart, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return err
		}
		route, token, created = &r, t, true
		return nil
	})
	if err != nil {
		return nil, "", false, err
	}
	if !created {
		token, err = s.Token(ctx, projectID, name)
		if err != nil {
			return nil, "", false, err
		}
	}
	return route, token, created, nil
}

// Rotate replaces a route's token at once. The old hash becomes a
// tombstone, so the old URL answers 410 for TombstoneWindow.
func (s *Store) Rotate(ctx context.Context, userID, projectID uuid.UUID, name string) (*Route, string, error) {
	var route *Route
	var token string
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		var oldHash []byte
		qerr := tx.QueryRow(ctx, `select id, token_sha256 from webhook_routes where project_id = $1 and name = $2 for update`, projectID, name).Scan(&id, &oldHash)
		if db.IsNoRows(qerr) {
			return db.ErrNotFound
		}
		if qerr != nil {
			return qerr
		}
		t, err := NewToken()
		if err != nil {
			return err
		}
		ct, wrapped, version, err := s.sec.SealValue(ctx, tx, userID.String(), projectID.String(), tokenAAD(name), []byte(t))
		if err != nil {
			return err
		}
		var r Route
		if err := tx.QueryRow(ctx, `update webhook_routes set token_sha256 = $2, ciphertext = $3, dek_wrapped = $4, kv_key_version = $5 where id = $1
			returning id, project_id, name, port, path, allow_start, created_at, updated_at`, id, HashToken(t), ct, wrapped, version).
			Scan(&r.ID, &r.ProjectID, &r.Name, &r.Port, &r.Path, &r.AllowStart, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into webhook_tombstones (token_sha256, project_id, at) values ($1, $2, now())
			on conflict (token_sha256) do update set at = now()`, oldHash, projectID); err != nil {
			return err
		}
		route, token = &r, t
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return route, token, nil
}

// Delete removes a route and tombstones its token.
func (s *Store) Delete(ctx context.Context, projectID uuid.UUID, name string) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var hash []byte
		qerr := tx.QueryRow(ctx, `delete from webhook_routes where project_id = $1 and name = $2 returning token_sha256`, projectID, name).Scan(&hash)
		if db.IsNoRows(qerr) {
			return db.ErrNotFound
		}
		if qerr != nil {
			return qerr
		}
		_, err := tx.Exec(ctx, `insert into webhook_tombstones (token_sha256, project_id, at) values ($1, $2, now())
			on conflict (token_sha256) do update set at = now()`, hash, projectID)
		return err
	})
}

// TombstoneProject removes every route of a project and tombstones each
// token. The destroy calls it in its own transaction: a destroyed
// project's URLs answer 410 for the window and 404 after, and `repose
// restore` does not bring them back, because a sender that obeyed the
// 410 has already let the old URL go (DECISIONS I-1114).
func TombstoneProject(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) error {
	_, err := tx.Exec(ctx, `with gone as (delete from webhook_routes where project_id = $1 returning token_sha256)
		insert into webhook_tombstones (token_sha256, project_id, at) select token_sha256, $1, now() from gone
		on conflict (token_sha256) do update set at = now()`, projectID)
	return err
}

// EdgeTable is the whole route table and the live tombstones. since is
// the version the caller holds; an unchanged table is answered with
// Unchanged and no rows, which is what makes a 5 s poll cheap.
func (s *Store) EdgeTable(ctx context.Context, since string) (*EdgeTable, error) {
	version, err := s.version(ctx)
	if err != nil {
		return nil, err
	}
	if since != "" && since == version {
		return &EdgeTable{Version: version, Routes: []EdgeRoute{}, Tombstones: []string{}, Unchanged: true}, nil
	}
	out := &EdgeTable{Version: version, Routes: []EdgeRoute{}, Tombstones: []string{}}
	rows, err := s.pool.Query(ctx, `select r.id, r.project_id, r.name, r.port, r.path, r.allow_start, r.token_sha256
		from webhook_routes r join projects p on p.id = r.project_id where p.destroyed_at is null`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e EdgeRoute
		var hash []byte
		if err := rows.Scan(&e.RouteID, &e.ProjectID, &e.Name, &e.Port, &e.Path, &e.AllowStart, &hash); err != nil {
			rows.Close()
			return nil, err
		}
		e.TokenHash = hexOf(hash)
		out.Routes = append(out.Routes, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trows, err := s.pool.Query(ctx, `select token_sha256 from webhook_tombstones where at > now() - $1::interval`, intervalOf(TombstoneWindow))
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var hash []byte
		if err := trows.Scan(&hash); err != nil {
			return nil, err
		}
		out.Tombstones = append(out.Tombstones, hexOf(hash))
	}
	return out, trows.Err()
}

// version changes whenever a route or a tombstone does. It is the two
// tables' newest timestamps and row counts, which moves on an insert, an
// update and a delete alike.
func (s *Store) version(ctx context.Context) (string, error) {
	var routes, tombs int64
	var rAt, tAt *time.Time
	if err := s.pool.QueryRow(ctx, `select count(*), max(updated_at) from webhook_routes`).Scan(&routes, &rAt); err != nil {
		return "", err
	}
	if err := s.pool.QueryRow(ctx, `select count(*), max(at) from webhook_tombstones where at > now() - $1::interval`, intervalOf(TombstoneWindow)).Scan(&tombs, &tAt); err != nil {
		return "", err
	}
	stamp := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%d.%s.%d.%s", routes, stamp(rAt), tombs, stamp(tAt)), nil
}

func intervalOf(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10) + " seconds"
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

// Report records one delivery against a route and drops all but the
// newest DeliveriesKept. An outcome the api does not know is refused by
// the caller, so nothing a sender chose reaches the row.
func (s *Store) Report(ctx context.Context, routeID uuid.UUID, d Delivery) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		at := d.At
		if at.IsZero() {
			at = s.now()
		}
		if _, err := tx.Exec(ctx, `insert into webhook_deliveries (id, route_id, at, status, outcome, duration_ms, bytes_in, bytes_out)
			values ($1, $2, $3, $4, $5, $6, $7, $8)`, uuid.Must(uuid.NewV7()), routeID, at, d.Status, d.Outcome, d.DurationMs, d.BytesIn, d.BytesOut); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `delete from webhook_deliveries where route_id = $1 and id not in (
			select id from webhook_deliveries where route_id = $1 order by at desc limit $2)`, routeID, DeliveriesKept)
		return err
	})
}

// ProjectOf returns the project a route id belongs to, or ErrNotFound.
func (s *Store) ProjectOf(ctx context.Context, routeID uuid.UUID) (uuid.UUID, error) {
	var pid uuid.UUID
	err := s.pool.QueryRow(ctx, `select project_id from webhook_routes where id = $1`, routeID).Scan(&pid)
	if db.IsNoRows(err) {
		return uuid.Nil, db.ErrNotFound
	}
	return pid, err
}

// ClaimStart reserves this project's one delivery-started start for the
// next StartInterval and reports whether the caller got it. A row and not
// a counter in memory: the rule has to survive an api restart.
func (s *Store) ClaimStart(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var claimed bool
	err := s.pool.QueryRow(ctx, `insert into webhook_starts (project_id, at) values ($1, now())
		on conflict (project_id) do update set at = now() where webhook_starts.at < now() - $2::interval
		returning true`, projectID, intervalOf(StartInterval)).Scan(&claimed)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return claimed, nil
}
