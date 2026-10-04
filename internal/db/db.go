// Package db owns the Postgres connection, the embedded migrations and the
// helpers every api package shares: transactions, advisory locks and the
// monthly partitions of the sample tables. Schema reference:
// docs/interfaces/db-schema.md.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the shared connection pool.
type Pool = pgxpool.Pool

// Tx is a transaction handle.
type Tx = pgx.Tx

// ErrNotFound is returned by lookups that found no row.
var ErrNotFound = errors.New("not found")

// Connect opens a pool and pings it.
func Connect(ctx context.Context, url string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// InTx runs fn inside a transaction, committing when it returns nil.
func InTx(ctx context.Context, pool *Pool, fn func(tx Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx) // the caller's error is the one to report; a rollback failure is subsumed by it
		return err
	}
	return tx.Commit(ctx)
}

// Lock ids for pg_try_advisory_lock so two replicas do not double-run a
// background loop (05-control-plane-api.md §5.1).
const (
	LockRollup      int64 = 1001
	LockOutbox      int64 = 1002
	LockSnapshots   int64 = 1003
	LockBaseBump    int64 = 1004
	LockCertCleanup int64 = 1005
	LockOps         int64 = 1006
	LockSweeper     int64 = 1007
	LockPartitions  int64 = 1008
	LockCAInit      int64 = 1009
	LockQuestions   int64 = 1010
	LockWaitlist    int64 = 1011
	// LockWaitlistTick picks the one replica that runs the waitlist's
	// minute tick. It is not LockWaitlist: the tick's transactions take
	// that one themselves on other connections, and a session lock held
	// by the loop blocked them forever (DECISIONS I-357).
	LockWaitlistTick int64 = 1012
	// LockSecretsReseal serialises the secrets reseal passes the api runs at
	// start (DECISIONS I-474).
	LockSecretsReseal int64 = 1013
)

// TryLock takes a session-level advisory lock on a dedicated connection
// and returns a release function, or ok=false when another session holds it.
func TryLock(ctx context.Context, pool *Pool, id int64) (release func(), ok bool, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, "select pg_try_advisory_lock($1)", id).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		_, _ = conn.Exec(context.Background(), "select pg_advisory_unlock($1)", id) // releasing the connection drops the lock anyway
		conn.Release()
	}, true, nil
}

// IsNoRows reports whether err is pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// EnsurePartitions creates the monthly partitions of meter_samples and
// proc_samples covering ts and the following month, idempotently.
func EnsurePartitions(ctx context.Context, pool *Pool, ts time.Time) error {
	ts = ts.UTC()
	// One transaction under LockPartitions: "create table if not exists"
	// for a partition is not atomic across sessions, and two api replicas
	// starting together both tried to create the same month (2026-09-20).
	return InTx(ctx, pool, func(tx Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", LockPartitions); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			start := time.Date(ts.Year(), ts.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, 0)
			for _, t := range []string{"meter_samples", "proc_samples"} {
				name := fmt.Sprintf("%s_%s", t, start.Format("2006_01"))
				q := fmt.Sprintf("create table if not exists %s partition of %s for values from ('%s') to ('%s')",
					name, t, start.Format("2006-01-02"), end.Format("2006-01-02"))
				if _, err := tx.Exec(ctx, q); err != nil {
					return fmt.Errorf("partition %s: %w", name, err)
				}
			}
		}
		return nil
	})
}

// DropExpiredPartitions drops sample partitions entirely older than the
// retention (docs/ops/OBSERVABILITY.md: meter_samples 90 days, proc_samples
// 30 days) and returns the names dropped.
func DropExpiredPartitions(ctx context.Context, pool *Pool, now time.Time) ([]string, error) {
	var dropped []string
	for _, t := range []struct {
		table string
		keep  time.Duration
	}{{"meter_samples", 90 * 24 * time.Hour}, {"proc_samples", 30 * 24 * time.Hour}} {
		rows, err := pool.Query(ctx, `select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid join pg_class p on p.oid = i.inhparent where p.relname = $1`, t.table)
		if err != nil {
			return dropped, err
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return dropped, err
			}
			names = append(names, n)
		}
		rows.Close()
		for _, n := range names {
			var y, m int
			if _, err := fmt.Sscanf(n[len(t.table)+1:], "%d_%d", &y, &m); err != nil {
				continue
			}
			end := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
			if now.Sub(end) > t.keep {
				if _, err := pool.Exec(ctx, "drop table if exists "+n); err != nil {
					return dropped, fmt.Errorf("drop %s: %w", n, err)
				}
				dropped = append(dropped, n)
			}
		}
	}
	return dropped, nil
}
