package app_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/app"
	"github.com/heracraft/repose/internal/api/secrets"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
)

// The api starts while Key Vault is unreachable, serves, and binds a
// name-only secret row to its project once the vault answers (DECISIONS
// I-474).
func TestStartResealsAfterKeyVaultReturns(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	vault := kv.New()
	cfg := app.Config{Mode: "all", Listen: freePort(t), GRPCListen: freePort(t), InternalListen: freePort(t), MetricsListen: "off",
		DatabaseURL: pool.Config().ConnString(), Dev: true, KeyVault: vault,
		APIResource: "https://api.test", GatewayHost: "ssh.test", GatewayPort: 22, ReplicaID: "test", GRPCServerNames: []string{"127.0.0.1"},
		Reseal: secrets.ResealOptions{Retry: 20 * time.Millisecond, MaxRetry: 50 * time.Millisecond, Again: 100 * time.Millisecond}}
	a, err := app.New(ctx, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}

	// A name-only row, as the api before I-474 wrote it.
	uid, pid := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, "insert into users (id, handle) values ($1, 'reseal')", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ($1, $2, 'p', 'reseal', 'large', 'stopped', 1)", pid, uid); err != nil {
		t.Fatal(err)
	}
	seed := secrets.New(pool, vault)
	if err := seed.Put(ctx, uid, pid, "TOKEN", []byte("value")); err != nil {
		t.Fatal(err)
	}
	dek := rowDEK(t, vault, pool, pid)
	if _, err := pool.Exec(ctx, "update secrets set ciphertext = $1 where project_id = $2", nameOnlySeal(t, dek, "TOKEN", []byte("value")), pid); err != nil {
		t.Fatal(err)
	}

	vault.SetDown(true)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("shutdown did not complete")
		}
	}()
	// Startup does not wait on the vault: /healthz answers while it is down.
	healthy := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !healthy; time.Sleep(50 * time.Millisecond) {
		if res, err := http.Get("http://" + cfg.Listen + "/healthz"); err == nil {
			healthy = res.StatusCode == 200
			_ = res.Body.Close()
		}
	}
	if !healthy {
		t.Fatal("the api did not become healthy while Key Vault was down")
	}
	time.Sleep(300 * time.Millisecond)
	if isBound(t, pool, dek, pid) {
		t.Fatal("resealed while Key Vault was down")
	}
	vault.SetDown(false)
	deadline := time.Now().Add(10 * time.Second)
	for !isBound(t, pool, dek, pid) {
		if time.Now().After(deadline) {
			t.Fatal("the row was not resealed after Key Vault returned")
		}
		time.Sleep(50 * time.Millisecond)
	}
	vals, err := secrets.New(pool, vault).DecryptForGuest(ctx, pid)
	if err != nil || len(vals) != 1 || string(vals[0].Value) != "value" {
		t.Fatalf("after reseal: %+v %v", vals, err)
	}
}

func rowDEK(t *testing.T, vault *kv.Fake, pool *db.Pool, pid string) []byte {
	t.Helper()
	var wrapped []byte
	var version string
	if err := pool.QueryRow(context.Background(), "select dek_wrapped, kv_key_version from secrets where project_id = $1", pid).Scan(&wrapped, &version); err != nil {
		t.Fatal(err)
	}
	dek, err := vault.Unwrap(context.Background(), wrapped, version)
	if err != nil {
		t.Fatal(err)
	}
	return dek
}

// isBound reports whether the project's one row opens only in the bound form.
func isBound(t *testing.T, pool *db.Pool, dek []byte, pid string) bool {
	t.Helper()
	var ct []byte
	if err := pool.QueryRow(context.Background(), "select ciphertext from secrets where project_id = $1", pid).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	gcm := newGCM(t, dek)
	_, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], []byte("TOKEN"))
	if err == nil {
		return false
	}
	if _, err := secrets.Open(dek, pid, "TOKEN", ct); err != nil {
		t.Fatalf("row opens in neither form: %v", err)
	}
	return true
}

func nameOnlySeal(t *testing.T, dek []byte, name string, value []byte) []byte {
	t.Helper()
	gcm := newGCM(t, dek)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return append(nonce, gcm.Seal(nil, nonce, value, []byte(name))...)
}

func newGCM(t *testing.T, dek []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(dek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return gcm
}
