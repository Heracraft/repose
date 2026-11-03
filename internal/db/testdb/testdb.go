// Package testdb gives integration tests a real Postgres. With DATABASE_URL
// set (CI) it uses that server; otherwise it starts a throwaway cluster
// with initdb and pg_ctl under a temporary directory, listening on a unix
// socket only. Each test gets its own database cloned from a migrated
// template, so tests are isolated and fast.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/db"
)

var (
	mu       sync.Mutex
	adminURL string
	dataDir  string
	started  bool
	counter  atomic.Int64
	// Per process: `go test ./...` runs packages in parallel against one
	// DATABASE_URL server, and a shared template name let one package drop
	// the template another was still cloning (CI, 2026-09-20).
	template = fmt.Sprintf("repose_template_%d", os.Getpid())
)

// Run wraps testing.M so the cluster is stopped when the package's tests
// end. Use it from TestMain: os.Exit(testdb.Run(m)).
func Run(m *testing.M) int {
	code := m.Run()
	Stop()
	return code
}

// Stop shuts the temporary cluster down, if one was started.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if started && dataDir != "" {
		_ = exec.Command("pg_ctl", "-D", dataDir, "-m", "immediate", "stop").Run() // best effort at exit
		_ = os.RemoveAll(filepath.Dir(dataDir))
		started = false
	}
}

func start(t testing.TB) string {
	mu.Lock()
	defer mu.Unlock()
	if adminURL != "" {
		return adminURL
	}
	if u := os.Getenv("DATABASE_URL"); u != "" {
		adminURL = u
		if err := makeTemplate(adminURL); err != nil {
			t.Fatalf("testdb: template on DATABASE_URL: %v", err)
		}
		return adminURL
	}
	if _, err := exec.LookPath("initdb"); err != nil {
		t.Skip("testdb: no DATABASE_URL and no initdb on PATH")
	}
	// Before paying for a new cluster, remove the ones whose owner is
	// gone (reap_unix.go, DECISIONS I-1022). Here and not in a goroutine:
	// the process about to spend a gigabyte is the one with an interest,
	// and nothing then races a suite that is shutting down.
	reap()
	base, err := os.MkdirTemp("", "repose-pg-")
	if err != nil {
		t.Fatal(err)
	}
	// Held for the life of this process, so the kernel reports our death
	// to the next run whichever way we go -- Stop() only covers the
	// orderly exit, and most of the twenty-five leaked clusters of
	// 2026-11-03 were killed rather than finished.
	claim(base)
	dataDir = filepath.Join(base, "data")
	sock := filepath.Join(base, "sock")
	if err := os.MkdirAll(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("initdb", "-D", dataDir, "-U", "postgres", "--auth=trust", "--no-sync", "-E", "UTF8").CombinedOutput()
	if err != nil {
		t.Fatalf("initdb: %v\n%s", err, out)
	}
	opts := fmt.Sprintf("-c listen_addresses='' -c unix_socket_directories=%s -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=200", sock)
	out, err = exec.Command("pg_ctl", "-D", dataDir, "-l", filepath.Join(base, "pg.log"), "-w", "-t", "60", "-o", opts, "start").CombinedOutput()
	if err != nil {
		t.Fatalf("pg_ctl start: %v\n%s", err, out)
	}
	started = true
	adminURL = "postgres://postgres@/postgres?host=" + url.QueryEscape(sock)
	if err := makeTemplate(adminURL); err != nil {
		t.Fatalf("testdb: template: %v", err)
	}
	return adminURL
}

func withDB(u, name string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func makeTemplate(admin string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, "drop database if exists "+template) // may not exist
	if _, err := conn.Exec(ctx, "create database "+template); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, withDB(admin, template))
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = db.MigrateUp(ctx, pool)
	return err
}

// Open returns a pool on a fresh, migrated database for this test. The
// database is dropped when the test ends.
func Open(t testing.TB) *db.Pool {
	t.Helper()
	admin := start(t)
	name := fmt.Sprintf("t_%d_%d", time.Now().UnixNano()%1_000_000, counter.Add(1))
	name = strings.ToLower(name)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("create database %s template %s", name, template)); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	_ = conn.Close(ctx)
	pool, err := db.Connect(ctx, withDB(admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			return
		}
		_, _ = c.Exec(ctx, "drop database if exists "+name+" with (force)") // cleanup only
		_ = c.Close(ctx)
	})
	return pool
}

// URL returns the connection string of a fresh database (for binaries under
// test that take DATABASE_URL).
func URL(t testing.TB) string {
	t.Helper()
	pool := Open(t)
	return pool.Config().ConnString()
}
