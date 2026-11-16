package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/app"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
	"github.com/heracraft/repose/internal/fakes/kv"
)

// TestOnlyTheGrpcProcessDeliversSays (DECISIONS I-1162): two api
// processes on one database, as production runs them. A `repose say`
// row is stored by the http process, which holds no host's stream, and
// is claimed and finished by the grpc process, which does. The defect
// this covers shipped because every test ran `API_MODE=all`, where one
// process is both halves.
func TestOnlyTheGrpcProcessDeliversSays(t *testing.T) {
	pool := testdb.Open(t)
	url := pool.Config().ConnString()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One key vault for both: in production they share Azure Key Vault,
	// and without that the second process cannot unwrap the CA the first
	// wrote.
	vault := kv.New()
	base := app.Config{DatabaseURL: url, Dev: true, KeyVault: vault, APIResource: "https://api.test",
		GatewayHost: "ssh.test", GatewayPort: 22, GRPCServerNames: []string{"127.0.0.1"}, MetricsListen: "off"}
	httpCfg := base
	httpCfg.Mode, httpCfg.Listen, httpCfg.ReplicaID = "http", freePort(t), "http-1"
	grpcCfg := base
	grpcCfg.Mode, grpcCfg.GRPCListen, grpcCfg.InternalListen, grpcCfg.ReplicaID = "grpc", freePort(t), freePort(t), "grpc-1"

	// The http process alone first: nothing in it picks the row up.
	runApp(t, ctx, httpCfg)
	sayID := storePendingSay(t, ctx, pool)
	time.Sleep(2 * time.Second) // four of the worker's one-second passes
	if got := sayState(t, ctx, pool, sayID); got != "pending" {
		t.Fatalf("the http process moved the row to %q on its own", got)
	}

	// The grpc process joins and its worker finishes the row. The
	// project has no host, so the answer is a failure; the point is that
	// the row left `pending` in a process that never saw the request.
	runApp(t, ctx, grpcCfg)
	deadline := time.Now().Add(20 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if got = sayState(t, ctx, pool, sayID); got == "failed" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the row is %q; the grpc process never delivered it", got)
}

// runApp starts one api process and stops it with the test.
func runApp(t *testing.T, ctx context.Context, cfg app.Config) {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(ctx)
	a, err := app.New(actx, cfg, "test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(actx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("api did not shut down")
		}
	})
}

// storePendingSay inserts the user, the running project and the says
// row the http process's POST /projects/:id/say would leave behind.
func storePendingSay(t *testing.T, ctx context.Context, pool *db.Pool) uuid.UUID {
	t.Helper()
	userID, projectID, revID, sayID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	err := db.InTx(ctx, pool, func(tx db.Tx) error {
		if _, err := tx.Exec(ctx, "insert into users (id, logto_sub, handle, email, billing_status, has_card) values ($1, 'sub-sayworker', 'sayworker', 'sayworker@example.com', 'trial', true)", userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes, config_revision_id, guest_id) values ($1, $2, 'sayworker', 'sayworker', 'large', 'running', $3, $4, $5)",
			projectID, userID, int64(40)<<30, revID, uuid.New()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, '{ pkgs, ... }: { }', 'applied')", revID, projectID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into says (id, project_id, user_id, tmux_window, text, sender, from_owner, state, command_id)
			values ($1, $2, $3, '', 'run the tests before you commit', 'sayworker', true, 'pending', $4)`, sayID, projectID, userID, uuid.New())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return sayID
}

func sayState(t *testing.T, ctx context.Context, pool *db.Pool, id uuid.UUID) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(ctx, "select state from says where id = $1", id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
