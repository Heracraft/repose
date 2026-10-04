package secrets_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	// Put writes the project-bound form (I-474): it does not open under
	// another project or in the name-only form an older api read.
	if _, err := secrets.Open(dek, uuid.NewString(), "DATABASE_URL", ct); err == nil {
		t.Fatal("ciphertext decrypted under another project")
	}
	if _, err := nameOnlyOpen(dek, "DATABASE_URL", ct); err == nil {
		t.Fatal("Put wrote the name-only form")
	}
	// The name-only form still opens under its own name for this release.
	legacy := nameOnlySeal(t, dek, "DATABASE_URL", []byte("postgres://secret"))
	if v, err := secrets.Open(dek, pid, "DATABASE_URL", legacy); err != nil || string(v) != "postgres://secret" {
		t.Fatalf("name-only row under its own name: %v %q", err, v)
	}
	if _, err := secrets.Open(dek, pid, "OTHER", legacy); err == nil {
		t.Fatal("name-only ciphertext decrypted under another name")
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

// legacyPut stores name with value in the name-only form an api from
// before I-474 wrote: a Put for the user's data key, then the ciphertext
// replaced with one whose additional data is the name alone.
func legacyPut(t *testing.T, s *secrets.Store, fk *kv.Fake, pool *dbPool, userID, projectID, name, value string) {
	t.Helper()
	ctx := context.Background()
	if err := s.Put(ctx, userID, projectID, name, []byte(value)); err != nil {
		t.Fatal(err)
	}
	var wrapped []byte
	var version string
	if err := pool.QueryRow(ctx, "select dek_wrapped, kv_key_version from secrets where project_id = $1 and name = $2", projectID, name).Scan(&wrapped, &version); err != nil {
		t.Fatal(err)
	}
	dek, err := fk.Unwrap(ctx, wrapped, version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update secrets set ciphertext = $1 where project_id = $2 and name = $3", nameOnlySeal(t, dek, name, []byte(value)), projectID, name); err != nil {
		t.Fatal(err)
	}
}

// rowForm opens one row with its own data key and reports whether it is
// in the project-bound form.
func rowForm(t *testing.T, fk *kv.Fake, pool *dbPool, projectID, name string) (value string, bound bool) {
	t.Helper()
	ctx := context.Background()
	var ct, wrapped []byte
	var version string
	if err := pool.QueryRow(ctx, "select ciphertext, dek_wrapped, kv_key_version from secrets where project_id = $1 and name = $2", projectID, name).Scan(&ct, &wrapped, &version); err != nil {
		t.Fatal(err)
	}
	dek, err := fk.Unwrap(ctx, wrapped, version)
	if err != nil {
		t.Fatal(err)
	}
	v, err := secrets.Open(dek, projectID, name, ct)
	if err != nil {
		t.Fatalf("%s/%s does not open in its own project: %v", projectID, name, err)
	}
	_, nameOnlyErr := nameOnlyOpen(dek, name, ct)
	return string(v), nameOnlyErr != nil
}

// I-433 and I-474: a row Put writes does not open in another project; a
// name-only row, which an older api wrote, does until Reseal binds it; a
// name-only row under the platform's data key does not open outside the
// platform project, before or after Reseal.
func TestCiphertextIsBoundToItsProject(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	platformProject(t, pool)
	legacyPut(t, s, fk, pool, secrets.PlatformUserID, secrets.PlatformProjectID, "SSH_USER_CA", "ca private key")
	uid, victim := newProject(t, s, pool)
	if err := s.Put(ctx, uid, victim, "AWS_SECRET", []byte("victim value")); err != nil {
		t.Fatal(err)
	}
	legacyPut(t, s, fk, pool, uid, victim, "OLD_TOKEN", "old value")

	_, platformCopy := newProject(t, s, pool)
	copyRows(t, pool, secrets.PlatformProjectID, platformCopy)
	if _, err := s.DecryptForGuest(ctx, platformCopy); !errors.Is(err, secrets.ErrPlatformKey) {
		t.Fatalf("platform row in a user project: %v", err)
	}

	// Before Reseal: the bound row does not open in the copy, the
	// name-only row still does.
	_, before := newProject(t, s, pool)
	copyRows(t, pool, victim, before)
	if _, err := s.DecryptForGuest(ctx, before); err == nil {
		t.Fatal("a copied project decrypted with a bound row in it")
	}
	if _, err := pool.Exec(ctx, "delete from secrets where project_id = $1 and name = 'AWS_SECRET'", before); err != nil {
		t.Fatal(err)
	}
	if vals, err := s.DecryptForGuest(ctx, before); err != nil || len(vals) != 1 {
		t.Fatalf("a name-only row opens anywhere until Reseal: %+v %v", vals, err)
	}
	if _, err := pool.Exec(ctx, "delete from secrets where project_id = $1", before); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.Reseal(ctx); err != nil {
		t.Fatal(err)
	}
	_, after := newProject(t, s, pool)
	copyRows(t, pool, victim, after)
	if vals, err := s.DecryptForGuest(ctx, after); err == nil {
		t.Fatalf("a resealed row decrypted in another project: %d values", len(vals))
	}
	if vals, err := s.DecryptForGuest(ctx, victim); err != nil || len(vals) != 2 || string(vals[0].Value) != "victim value" || string(vals[1].Value) != "old value" {
		t.Fatalf("own project after reseal: %+v %v", vals, err)
	}
	if _, err := s.DecryptForGuest(ctx, platformCopy); !errors.Is(err, secrets.ErrPlatformKey) {
		t.Fatalf("platform row in a user project after reseal: %v", err)
	}
	if v, err := s.GetPlatform(ctx, "SSH_USER_CA"); err != nil || string(v) != "ca private key" {
		t.Fatalf("platform: %q %v", v, err)
	}
}

// Reseal binds the name-only rows an older api wrote, platform rows
// included, leaves a platform-key row outside the platform project alone,
// and a second run changes nothing.
func TestResealLegacyRows(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	platformProject(t, pool)
	legacyPut(t, s, fk, pool, secrets.PlatformUserID, secrets.PlatformProjectID, "SSH_USER_CA", "ca private key")
	uid, pid := newProject(t, s, pool)
	legacyPut(t, s, fk, pool, uid, pid, "TOKEN", "v")
	if err := s.Put(ctx, uid, pid, "NEW", []byte("n")); err != nil {
		t.Fatal(err)
	}
	if _, bound := rowForm(t, fk, pool, pid, "TOKEN"); bound {
		t.Fatal("legacyPut wrote the bound form")
	}
	_, other := newProject(t, s, pool)
	copyRows(t, pool, secrets.PlatformProjectID, other)
	resealed, refused, err := s.Reseal(ctx)
	if err != nil || resealed != 2 || refused != 1 {
		t.Fatalf("reseal: %d resealed, %d refused, %v", resealed, refused, err)
	}
	var before []byte
	if err := pool.QueryRow(ctx, "select ciphertext from secrets where project_id = $1 and name = 'TOKEN'", pid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if again, refusedAgain, err := s.Reseal(ctx); err != nil || again != 0 || refusedAgain != 1 {
		t.Fatalf("second reseal: %d %d %v", again, refusedAgain, err)
	}
	var after []byte
	if err := pool.QueryRow(ctx, "select ciphertext from secrets where project_id = $1 and name = 'TOKEN'", pid).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a second reseal rewrote a bound row")
	}
	for _, n := range []string{"TOKEN", "NEW"} {
		if _, bound := rowForm(t, fk, pool, pid, n); !bound {
			t.Fatalf("%s is still name-only", n)
		}
	}
	if v, bound := rowForm(t, fk, pool, secrets.PlatformProjectID, "SSH_USER_CA"); !bound || v != "ca private key" {
		t.Fatalf("platform row: %q bound=%v", v, bound)
	}
	if _, err := s.DecryptForGuest(ctx, other); !errors.Is(err, secrets.ErrPlatformKey) {
		t.Fatalf("platform copy after reseal: %v", err)
	}
}

// Two passes at once, as the api and api-grpc containers (or the old and
// new container of a rolling deploy) run them: every row ends bound and
// opens in its own project with its own value, and each row is counted
// once between the two.
func TestConcurrentResealKeepsEveryRow(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	type key struct{ project, name string }
	want := map[key]string{}
	for p := 0; p < 4; p++ {
		uid, pid := newProject(t, s, pool)
		for i := 0; i < 15; i++ {
			name := fmt.Sprintf("K%02d", i)
			value := fmt.Sprintf("value-%d-%d", p, i)
			legacyPut(t, s, fk, pool, uid, pid, name, value)
			want[key{pid, name}] = value
		}
	}
	var wg sync.WaitGroup
	results := make([][3]any, 3)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, f, err := secrets.New(pool, fk).Reseal(ctx)
			results[i] = [3]any{r, f, err}
		}(i)
	}
	wg.Wait()
	total := 0
	for i, r := range results {
		if r[2] != nil {
			t.Fatalf("pass %d: %v", i, r[2])
		}
		if r[1].(int) != 0 {
			t.Fatalf("pass %d refused %d rows", i, r[1])
		}
		total += r[0].(int)
	}
	if total != len(want) {
		t.Fatalf("passes resealed %d rows between them, want %d", total, len(want))
	}
	for k, v := range want {
		got, bound := rowForm(t, fk, pool, k.project, k.name)
		if !bound || got != v {
			t.Fatalf("%v: %q bound=%v, want %q", k, got, bound, v)
		}
	}
	if again, _, err := s.Reseal(ctx); err != nil || again != 0 {
		t.Fatalf("after the concurrent passes: %d %v", again, err)
	}
}

// A fork copies a bound row and a name-only row; both arrive bound to the
// fork and open there, and the fork's rows do not open back in the source.
func TestCopyNamedBindsToTheFork(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, src := newProject(t, s, pool)
	if err := s.Put(ctx, uid, src, "BOUND", []byte("b")); err != nil {
		t.Fatal(err)
	}
	legacyPut(t, s, fk, pool, uid, src, "LEGACY", "l")
	fork := uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'f', $3, 'large', 'stopped', 1)", fork, uid, "f-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CopyNamed(ctx, tx, src, fork); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]string{"BOUND": "b", "LEGACY": "l"} {
		if v, bound := rowForm(t, fk, pool, fork, n); !bound || v != want {
			t.Fatalf("fork %s: %q bound=%v", n, v, bound)
		}
	}
	_, back := newProject(t, s, pool)
	copyRows(t, pool, fork, back)
	if _, err := s.DecryptForGuest(ctx, back); err == nil {
		t.Fatal("the fork's rows opened in another project")
	}
	// Put on the fork after the copy replaces only the fork's value.
	if err := s.Put(ctx, uid, fork, "BOUND", []byte("changed")); err != nil {
		t.Fatal(err)
	}
	if v, _ := rowForm(t, fk, pool, src, "BOUND"); v != "b" {
		t.Fatalf("source changed with the fork: %q", v)
	}
}

// hookKV runs hook on the first Unwrap, which Reseal calls after it has
// read the rows and before it writes them.
type hookKV struct {
	*kv.Fake
	once sync.Once
	hook func()
}

func (h *hookKV) Unwrap(ctx context.Context, wrapped []byte, version string) ([]byte, error) {
	h.once.Do(h.hook)
	return h.Fake.Unwrap(ctx, wrapped, version)
}

// A Put that lands between Reseal's read and its write is kept: the
// UPDATE matches the ciphertext Reseal read, so it changes nothing.
func TestResealDoesNotOverwriteANewerPut(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	legacyPut(t, s, fk, pool, uid, pid, "A", "old")
	hk := &hookKV{Fake: fk, hook: func() {
		if err := s.Put(ctx, uid, pid, "A", []byte("new")); err != nil {
			t.Error(err)
		}
	}}
	resealed, _, err := secrets.New(pool, hk).Reseal(ctx)
	if err != nil || resealed != 0 {
		t.Fatalf("reseal after a concurrent put: %d %v", resealed, err)
	}
	if v, bound := rowForm(t, fk, pool, pid, "A"); !bound || v != "new" {
		t.Fatalf("got %q bound=%v, want the newer put", v, bound)
	}
}

// ResealLoop at start with Key Vault down: it retries, does nothing to the
// rows while the vault is down, and binds them once it answers.
func TestResealLoopRetriesWhileKeyVaultIsDown(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	seed := secrets.New(pool, fk)
	uid, pid := newProject(t, seed, pool)
	legacyPut(t, seed, fk, pool, uid, pid, "A", "a")
	fk.SetDown(true)
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		secrets.New(pool, fk).ResealLoop(ctx, log, secrets.ResealOptions{Retry: 20 * time.Millisecond, MaxRetry: 50 * time.Millisecond, Again: 100 * time.Millisecond})
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "key_service_unavailable") {
		if time.Now().After(deadline) {
			t.Fatalf("no failed pass logged: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	fk.SetDown(false)
	if _, bound := rowForm(t, fk, pool, pid, "A"); bound {
		t.Fatal("row resealed while Key Vault was down")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("loop did not finish: %s", logs.String())
	}
	if v, bound := rowForm(t, fk, pool, pid, "A"); !bound || v != "a" {
		t.Fatalf("after the vault returned: %q bound=%v", v, bound)
	}
	out := logs.String()
	for _, w := range []string{"event=secrets_reseal resealed=1", "event=secrets_name_only_none"} {
		if !strings.Contains(out, w) {
			t.Fatalf("log lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "\"A\"") || strings.Contains(out, "name=A") || strings.Contains(out, "=a ") {
		t.Fatalf("log carries a name or value:\n%s", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// nameOnlySeal is how an api from before I-474 sealed a row.
func nameOnlySeal(t *testing.T, dek []byte, name string, value []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(dek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return append(nonce, gcm.Seal(nil, nonce, value, []byte(name))...)
}

// nameOnlyOpen is how an api from before I-433 opens a row.
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
