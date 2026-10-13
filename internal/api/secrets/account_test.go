package secrets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
)

// Account secrets (I-697): one row per user and name, delivered with each
// project's own set, where the project's secret of the same name wins.
func TestAccountSecretsMergeIntoEveryProject(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	pid2 := uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'q', $3, 'small', 'running', 1)", pid2, uid, "q-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	_, other := newProject(t, s, pool)

	if err := s.PutAccount(ctx, uid, "COOLIFY_BASE_URL", []byte("https://coolify.example")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAccount(ctx, uid, "STRIPE_KEY", []byte("account stripe")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, uid, pid, "STRIPE_KEY", []byte("project stripe")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, uid, pid, "ONLY_HERE", []byte("p")); err != nil {
		t.Fatal(err)
	}

	got := func(projectID string) map[string]string {
		t.Helper()
		vals, err := s.DecryptForGuest(ctx, projectID)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for i, v := range vals {
			if _, dup := m[v.Name]; dup {
				t.Fatalf("%s delivered twice: %+v", v.Name, vals)
			}
			if i > 0 && vals[i-1].Name > v.Name {
				t.Fatalf("not in name order: %+v", vals)
			}
			m[v.Name] = string(v.Value)
		}
		return m
	}
	if m := got(pid); len(m) != 3 || m["STRIPE_KEY"] != "project stripe" || m["COOLIFY_BASE_URL"] != "https://coolify.example" || m["ONLY_HERE"] != "p" {
		t.Fatalf("project with its own STRIPE_KEY: %v", m)
	}
	if m := got(pid2); len(m) != 2 || m["STRIPE_KEY"] != "account stripe" || m["COOLIFY_BASE_URL"] != "https://coolify.example" {
		t.Fatalf("project without: %v", m)
	}
	if m := got(other); len(m) != 0 {
		t.Fatalf("another user's project got %v", m)
	}

	// The project's list is its own rows; the account's list is the account's.
	if metas, err := s.List(ctx, pid2); err != nil || len(metas) != 0 {
		t.Fatalf("project list %+v %v", metas, err)
	}
	metas, err := s.ListAccount(ctx, uid)
	if err != nil || len(metas) != 2 || metas[0].Name != "COOLIFY_BASE_URL" || metas[1].Name != "STRIPE_KEY" {
		t.Fatalf("account list %+v %v", metas, err)
	}

	// One data key per user across both tables.
	var keys int
	if err := pool.QueryRow(ctx, "select count(distinct dek_wrapped) from (select dek_wrapped from secrets where project_id in ($1, $2) union all select dek_wrapped from account_secrets where user_id = $3) k", pid, pid2, uid).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if keys != 1 {
		t.Fatalf("%d data keys for one user", keys)
	}

	// The ciphertext is bound to its user and its name.
	var ct, wrapped []byte
	var version string
	if err := pool.QueryRow(ctx, "select ciphertext, dek_wrapped, kv_key_version from account_secrets where user_id = $1 and name = 'COOLIFY_BASE_URL'", uid).Scan(&ct, &wrapped, &version); err != nil {
		t.Fatal(err)
	}
	dek, err := fk.Unwrap(ctx, wrapped, version)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := secrets.OpenAccount(dek, uid, "COOLIFY_BASE_URL", ct); err != nil || string(v) != "https://coolify.example" {
		t.Fatalf("open under its own user and name: %v %q", err, v)
	}
	if _, err := secrets.OpenAccount(dek, uid, "STRIPE_KEY", ct); err == nil {
		t.Fatal("account ciphertext opened under another name")
	}
	if _, err := secrets.OpenAccount(dek, uuid.NewString(), "COOLIFY_BASE_URL", ct); err == nil {
		t.Fatal("account ciphertext opened under another user")
	}
	if _, err := secrets.Open(dek, pid, "COOLIFY_BASE_URL", ct); err == nil {
		t.Fatal("account ciphertext opened as a project row")
	}
	// A row moved to another user does not decrypt for that user's machines.
	if _, err := pool.Exec(ctx, "update account_secrets set user_id = (select user_id from projects where id = $1) where user_id = $2 and name = 'COOLIFY_BASE_URL'", other, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecryptForGuest(ctx, other); err == nil {
		t.Fatal("a row moved to another user decrypted")
	}
	if _, err := pool.Exec(ctx, "delete from account_secrets where user_id = (select user_id from projects where id = $1)", other); err != nil {
		t.Fatal(err)
	}

	// Removing the account's STRIPE_KEY leaves the project's.
	ok, err := s.DeleteAccount(ctx, uid, "STRIPE_KEY")
	if err != nil || !ok {
		t.Fatalf("delete %v %v", ok, err)
	}
	if ok, err := s.DeleteAccount(ctx, uid, "STRIPE_KEY"); err != nil || ok {
		t.Fatalf("second delete %v %v", ok, err)
	}
	if m := got(pid); m["STRIPE_KEY"] != "project stripe" {
		t.Fatalf("after the account's went: %v", m)
	}
	if m := got(pid2); len(m) != 0 {
		t.Fatalf("pid2 after delete: %v", m)
	}
}

func TestAccountSecretNames(t *testing.T) {
	pool := testdb.Open(t)
	s := secrets.New(pool, kv.New())
	ctx := context.Background()
	uid, _ := newProject(t, s, pool)
	for _, name := range []string{"lower", "1A", "ssh_host_ed25519_key", "user_ca.pub", "BASH_ENV", "ENV", "REPOSE_ENV_GEN"} {
		if err := s.PutAccount(ctx, uid, name, []byte("x")); !errors.Is(err, secrets.ErrInvalidName) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := s.PutAccount(ctx, uid, "BIG", make([]byte, secrets.MaxValueBytes+1)); !errors.Is(err, secrets.ErrTooLarge) {
		t.Fatalf("big: %v", err)
	}
	if err := s.PutAccount(ctx, secrets.PlatformUserID, "A", []byte("x")); !errors.Is(err, secrets.ErrInvalidName) {
		t.Fatalf("the platform user: %v", err)
	}
}

// A user whose first secret is an account secret gets one data key, which
// the project's first secret then reuses; Rewrap covers both tables.
func TestAccountSecretsShareTheDataKeyAndRewrap(t *testing.T) {
	pool := testdb.Open(t)
	fk := kv.New()
	s := secrets.New(pool, fk)
	ctx := context.Background()
	uid, pid := newProject(t, s, pool)
	if err := s.PutAccount(ctx, uid, "A", []byte("account")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, uid, pid, "B", []byte("project")); err != nil {
		t.Fatal(err)
	}
	var a, b []byte
	if err := pool.QueryRow(ctx, "select dek_wrapped from account_secrets where user_id = $1", uid).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select dek_wrapped from secrets where project_id = $1 and name = 'B'", pid).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("the project's first secret wrapped a second data key")
	}
	fk.BumpVersion()
	n, err := s.Rewrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rewrapped %d rows, want 2 (one per table)", n)
	}
	current, _ := fk.CurrentVersion(ctx)
	var left int
	if err := pool.QueryRow(ctx, "select count(*) from account_secrets where kv_key_version <> $1", current).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d account rows on the old key version", left)
	}
	s2 := secrets.New(pool, fk)
	vals, err := s2.DecryptForGuest(ctx, pid)
	if err != nil || len(vals) != 2 || string(vals[0].Value) != "account" || string(vals[1].Value) != "project" {
		t.Fatalf("after rewrap %+v %v", vals, err)
	}
}
