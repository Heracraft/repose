package webhooks_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func newProject(t *testing.T, pool *db.Pool) (userID, projectID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.Must(uuid.NewV7())
	pid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, $2)", uid, "h-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p', $3, 'large', 'running', 1)",
		pid, uid, "p-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	return uid, pid
}

// A route's token goes into the row as ciphertext under the account's
// data key and comes back out whole, and the row holds no plaintext: a
// dump of Postgres gives no working URL (DECISIONS I-1110).
func TestTokenRoundTripsAndIsNeverStoredInTheClear(t *testing.T) {
	pool := testdb.Open(t)
	store := webhooks.New(pool, secrets.New(pool, kv.New()), "hooks.test")
	ctx := context.Background()
	uid, pid := newProject(t, pool)

	route, token, created, err := store.Upsert(ctx, uid, pid, "stripe", 3000, "/api/webhooks/stripe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !created || len(token) != 22 {
		t.Fatalf("created %v, token %q", created, token)
	}
	again, err := store.Token(ctx, pid, "stripe")
	if err != nil || again != token {
		t.Fatalf("round trip: %q %v, want %q", again, err, token)
	}
	if got := store.URL(pid.String(), "stripe", token); got != "https://hooks.test/"+pid.String()+"/stripe/"+token {
		t.Fatalf("url: %s", got)
	}

	var ct, hash []byte
	if err := pool.QueryRow(ctx, `select ciphertext, token_sha256 from webhook_routes where id = $1`, route.ID).Scan(&ct, &hash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(token)) {
		t.Fatal("the stored ciphertext holds the token's characters")
	}
	if !bytes.Equal(hash, webhooks.HashToken(token)) {
		t.Fatal("the stored hash is not sha256 of the token")
	}
	// The probe: a row whose ciphertext is replaced with the token's own
	// bytes would fail the containment check above, so the check is not
	// passing on an empty column.
	if len(ct) < len(token) {
		t.Fatalf("the ciphertext is %d bytes, shorter than the token", len(ct))
	}

	// The ciphertext is bound to the route's name, as a secret is bound
	// to its own (I-433): the same bytes under another name do not open.
	if _, err := pool.Exec(ctx, `update webhook_routes set name = 'github' where id = $1`, route.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Token(ctx, pid, "github"); err == nil {
		t.Fatal("a route renamed in the database still decrypts")
	}
}

// The once-per-10-minutes rule of --start is a row, so it holds across
// two stores, which is what surviving an api restart means.
func TestClaimStartHoldsAcrossStores(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	_, pid := newProject(t, pool)
	first := webhooks.New(pool, secrets.New(pool, kv.New()), "")
	ok, err := first.ClaimStart(ctx, pid)
	if err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	second := webhooks.New(pool, secrets.New(pool, kv.New()), "")
	ok, err = second.ClaimStart(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a new store claimed a start inside the window")
	}
	// The probe: an older row is claimable again, so the false above is
	// the window and not a claim that can never be taken twice.
	if _, err := pool.Exec(ctx, `update webhook_starts set at = now() - interval '11 minutes' where project_id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	ok, err = second.ClaimStart(ctx, pid)
	if err != nil || !ok {
		t.Fatalf("after the window: %v %v", ok, err)
	}
}

// ParseTarget takes what the CLI and the dashboard send and refuses the
// rest, with the path always starting at a slash.
func TestParseTarget(t *testing.T) {
	for _, tc := range []struct {
		in   string
		port int
		path string
	}{
		{"3000", 3000, "/"},
		{":3000", 3000, "/"},
		{"3000/", 3000, "/"},
		{"3000/api/webhooks/stripe", 3000, "/api/webhooks/stripe"},
		{"65535/x", 65535, "/x"},
	} {
		port, path, err := webhooks.ParseTarget(tc.in)
		if err != nil || port != tc.port || path != tc.path {
			t.Fatalf("%q: %d %q %v, want %d %q", tc.in, port, path, err, tc.port, tc.path)
		}
	}
	for _, bad := range []string{"", "0", "70000", "nope", "-1", "3000/a b", "3000/a?b", "3000/a#b"} {
		if _, _, err := webhooks.ParseTarget(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}
