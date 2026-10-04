package secrets_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func newProject(t *testing.T, s *secrets.Store, pool *dbPool) (userID, projectID string) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.Must(uuid.NewV7()).String()
	pid := uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, $2)", uid, "h-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p', $3, 'large', 'stopped', 1)", pid, uid, "p-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	return uid, pid
}

type dbPool = testdbPool

func TestRoundTripAndAAD(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	if err := s.Put(ctx, uid, pid, "DATABASE_URL", []byte("postgres://secret")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, uid, pid, "OTHER", []byte("x")); err != nil {
		t.Fatal(err)
	}
	vals, err := s.DecryptForGuest(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 || vals[0].Name != "DATABASE_URL" || string(vals[0].Value) != "postgres://secret" {
		t.Fatalf("got %+v", vals)
	}
	// Ciphertext for name A must not decrypt under name B.
	var ct, wrapped []byte
	var version string
	if err := pool.QueryRow(ctx, "select ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1 and name = 'DATABASE_URL'", pid).Scan(&ct, &wrapped, &version); err != nil {
		t.Fatal(err)
	}
	dek, err := fk.Unwrap(ctx, wrapped, version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Open(dek, pid, "OTHER", ct); err == nil {
		t.Fatal("ciphertext decrypted under another name")
	}
	// The project-bound form (written from the release after I-433) does
	// not open under another project or another name.
	bound, err := secrets.SealBound(dek, pid, "DATABASE_URL", []byte("postgres://secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Open(dek, uuid.NewString(), "DATABASE_URL", bound); err == nil {
		t.Fatal("bound ciphertext decrypted under another project")
	}
	if _, err := secrets.Open(dek, pid, "OTHER", bound); err == nil {
		t.Fatal("bound ciphertext decrypted under another name")
	}
	if v, err := secrets.Open(dek, pid, "DATABASE_URL", bound); err != nil || string(v) != "postgres://secret" {
		t.Fatalf("bound ciphertext under its own project: %v %q", err, v)
	}
	if v, err := secrets.Open(dek, pid, "DATABASE_URL", ct); err != nil || string(v) != "postgres://secret" {
		t.Fatalf("open under own name: %v %q", err, v)
	}
	metas, err := s.List(ctx, pid)
	if err != nil || len(metas) != 2 || metas[0].Name != "DATABASE_URL" {
		t.Fatalf("list %+v %v", metas, err)
	}
	ok, err := s.Delete(ctx, pid, "OTHER")
	if err != nil || !ok {
		t.Fatal(err)
	}
	vals, _ = s.DecryptForGuest(ctx, pid)
	if len(vals) != 1 {
		t.Fatalf("after delete: %d", len(vals))
	}
}

func TestNames(t *testing.T) {
	for name, want := range map[string]bool{"A": true, "DATABASE_URL": true, "a": false, "1A": false, "ssh_host_ed25519_key": false, "user_ca.pub": false, "ssh_host_ed25519_key-cert.pub": false} {
		if got := secrets.ValidName(name); got != want {
			t.Errorf("%q: got %v want %v", name, got, want)
		}
	}
	pool := testdb.Open(t)
	s := secrets.New(pool, kv.New())
	uid, pid := newProject(t, s, pool)
	if err := s.Put(context.Background(), uid, pid, "bad", []byte("x")); !errors.Is(err, secrets.ErrInvalidName) {
		t.Fatalf("expected invalid name, got %v", err)
	}
	for _, n := range []string{"BASH_ENV", "ENV", "REPOSE_ENV_GEN"} {
		if !secrets.IsShellName(n) || !secrets.ValidName(n) {
			t.Fatalf("%s: shell name %v, valid (deletable) %v", n, secrets.IsShellName(n), secrets.ValidName(n))
		}
		if err := s.Put(context.Background(), uid, pid, n, []byte("x")); !errors.Is(err, secrets.ErrInvalidName) {
			t.Fatalf("%s: expected invalid name, got %v", n, err)
		}
	}
	if err := s.Put(context.Background(), uid, pid, "BIG", bytes.Repeat([]byte("x"), secrets.MaxValueBytes+1)); !errors.Is(err, secrets.ErrTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
}

func TestRewrapAfterKeyVersionBump(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	_, pid2 := newProject(t, s, pool)
	for _, n := range []string{"A", "B", "C"} {
		if err := s.Put(ctx, uid, pid, n, []byte("value-"+n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, uid, pid2, "D", []byte("value-D")); err != nil {
		t.Fatal(err)
	}
	var before [][]byte
	rows, _ := pool.Query(ctx, "select ciphertext from secrets order by name")
	for rows.Next() {
		var ct []byte
		_ = rows.Scan(&ct)
		before = append(before, ct)
	}
	rows.Close()
	v2 := fk.BumpVersion()
	n, err := s.Rewrap(ctx)
	if err != nil || n != 4 {
		t.Fatalf("rewrap n=%d err=%v", n, err)
	}
	var after [][]byte
	var versions []string
	rows, _ = pool.Query(ctx, "select ciphertext, kv_key_version from secrets order by name")
	for rows.Next() {
		var ct []byte
		var v string
		_ = rows.Scan(&ct, &v)
		after = append(after, ct)
		versions = append(versions, v)
	}
	rows.Close()
	for i := range before {
		if !bytes.Equal(before[i], after[i]) {
			t.Fatal("rewrap touched ciphertext")
		}
		if versions[i] != v2 {
			t.Fatalf("row %d still on %s", i, versions[i])
		}
	}
	// A fresh store (empty cache) decrypts with the new wrapping.
	s2 := secrets.New(pool, fk)
	vals, err := s2.DecryptForGuest(ctx, pid)
	if err != nil || len(vals) != 3 || string(vals[2].Value) != "value-C" {
		t.Fatalf("after rewrap: %+v %v", vals, err)
	}
	n, err = s.Rewrap(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second rewrap should be a no-op: %d %v", n, err)
	}
}

func TestKeyVaultDownFailsClosed(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	if err := s.Put(ctx, uid, pid, "A", []byte("v")); err != nil {
		t.Fatal(err)
	}
	fk.Down = true
	s2 := secrets.New(pool, fk) // empty cache
	if _, err := s2.DecryptForGuest(ctx, pid); !errors.Is(err, secrets.ErrKeyServiceUnavailable) {
		t.Fatalf("expected key service unavailable, got %v", err)
	}
	if err := s2.Put(ctx, uid, pid, "B", []byte("v")); !errors.Is(err, secrets.ErrKeyServiceUnavailable) {
		t.Fatalf("expected key service unavailable on put, got %v", err)
	}
	// The warm cache still serves reads for the ttl.
	if _, err := s.DecryptForGuest(ctx, pid); err != nil {
		t.Fatalf("cached dek should still decrypt: %v", err)
	}
}

func TestDEKCacheLimitsKeyVaultCalls(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	for i := 0; i < 5; i++ {
		if err := s.Put(ctx, uid, pid, "K"+string(rune('A'+i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, err := s.DecryptForGuest(ctx, pid); err != nil {
			t.Fatal(err)
		}
	}
	wraps, unwraps := fk.Counts()
	if wraps != 1 || unwraps != 0 {
		t.Fatalf("wraps=%d unwraps=%d; one DEK per user and no unwrap while cached", wraps, unwraps)
	}
}

func platformProject(t *testing.T, pool *dbPool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'platform') on conflict do nothing", secrets.PlatformUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $1, 'platform', 'platform', 'large', 'stopped', 1) on conflict do nothing", secrets.PlatformProjectID); err != nil {
		t.Fatal(err)
	}
}

func copyRows(t *testing.T, pool *dbPool, from, to string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into secrets (id, project_id, name, ciphertext, dek_wrapped, kv_key_version)
		select gen_random_uuid(), $2, name, ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1`, from, to); err != nil {
		t.Fatal(err)
	}
}

// What this release binds (I-433): a row in the name-only form under the
// platform's data key does not open outside the platform project, and a
// project-bound row does not open in another project. A name-only user
// row still does, until the release that writes the bound form.
func TestCiphertextIsBoundToItsProject(t *testing.T) {
	pool := testdb.Open(t)
	s := secrets.New(pool, kv.New())
	ctx := context.Background()
	platformProject(t, pool)
	if err := s.PutPlatform(ctx, "SSH_USER_CA", []byte("ca private key")); err != nil {
		t.Fatal(err)
	}
	uid, victim := newProject(t, s, pool)
	if err := s.Put(ctx, uid, victim, "AWS_SECRET", []byte("victim value")); err != nil {
		t.Fatal(err)
	}

	_, platformCopy := newProject(t, s, pool)
	copyRows(t, pool, secrets.PlatformProjectID, platformCopy)
	if _, err := s.DecryptForGuest(ctx, platformCopy); !errors.Is(err, secrets.ErrPlatformKey) {
		t.Fatalf("platform row in a user project: %v", err)
	}

	_, nameOnly := newProject(t, s, pool)
	copyRows(t, pool, victim, nameOnly)
	if vals, err := s.DecryptForGuest(ctx, nameOnly); err != nil || len(vals) != 1 {
		t.Fatalf("name-only rows are not bound to a project in this release: %+v %v", vals, err)
	}

	if _, _, err := s.Reseal(ctx); err != nil {
		t.Fatal(err)
	}
	_, boundCopy := newProject(t, s, pool)
	copyRows(t, pool, victim, boundCopy)
	if vals, err := s.DecryptForGuest(ctx, boundCopy); err == nil {
		t.Fatalf("a bound row decrypted in another project: %d values", len(vals))
	}
	if vals, err := s.DecryptForGuest(ctx, victim); err != nil || len(vals) != 1 || string(vals[0].Value) != "victim value" {
		t.Fatalf("own project after reseal: %+v %v", vals, err)
	}
	if v, err := s.GetPlatform(ctx, "SSH_USER_CA"); err != nil || string(v) != "ca private key" {
		t.Fatalf("platform: %q %v", v, err)
	}
}

// This release writes the name-only form an older api can open; Reseal,
// for the next release, binds those rows to their project and leaves a
// platform-key row outside the platform project alone.
func TestResealLegacyRows(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	platformProject(t, pool)
	if err := s.PutPlatform(ctx, "SSH_USER_CA", []byte("ca private key")); err != nil {
		t.Fatal(err)
	}
	uid, pid := newProject(t, s, pool)
	if err := s.Put(ctx, uid, pid, "TOKEN", []byte("v")); err != nil {
		t.Fatal(err)
	}
	rowDEK := func() (ct, dek []byte) {
		var wrapped []byte
		var version string
		if err := pool.QueryRow(ctx, "select ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1", pid).Scan(&ct, &wrapped, &version); err != nil {
			t.Fatal(err)
		}
		dek, err := fk.Unwrap(ctx, wrapped, version)
		if err != nil {
			t.Fatal(err)
		}
		return ct, dek
	}
	ct, dek := rowDEK()
	if v, err := nameOnlyOpen(dek, "TOKEN", ct); err != nil || string(v) != "v" {
		t.Fatalf("Put did not write the form an older api opens: %v", err)
	}
	_, other := newProject(t, s, pool)
	copyRows(t, pool, secrets.PlatformProjectID, other)
	resealed, refused, err := s.Reseal(ctx)
	if err != nil || resealed != 2 || refused != 1 {
		t.Fatalf("reseal: %d resealed, %d refused, %v", resealed, refused, err)
	}
	if again, _, err := s.Reseal(ctx); err != nil || again != 0 {
		t.Fatalf("second reseal: %d %v", again, err)
	}
	ct, dek = rowDEK()
	if _, err := secrets.Open(dek, uuid.NewString(), "TOKEN", ct); err == nil {
		t.Fatal("a resealed row opens under another project")
	}
	if v, err := secrets.Open(dek, pid, "TOKEN", ct); err != nil || string(v) != "v" {
		t.Fatalf("resealed row under its project: %q %v", v, err)
	}
	if v, err := s.GetPlatform(ctx, "SSH_USER_CA"); err != nil || string(v) != "ca private key" {
		t.Fatalf("platform after reseal: %q %v", v, err)
	}
}

// nameOnlyOpen is how an api image from before I-433 opens a row.
func nameOnlyOpen(dek []byte, name string, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], []byte(name))
}
