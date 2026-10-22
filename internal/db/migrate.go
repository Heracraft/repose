package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one numbered pair of up and down scripts.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// Migrations returns the embedded migrations in version order.
func Migrations() ([]Migration, error) { return migrationsFrom(migrationFS) }

// migrationsFrom is Migrations over any filesystem, so a test can hand it
// a pair of files the embedded directory cannot hold (DECISIONS I-854).
func migrationsFrom(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".sql")
		var dir string
		switch {
		case strings.HasSuffix(base, ".up"):
			dir, base = "up", strings.TrimSuffix(base, ".up")
		case strings.HasSuffix(base, ".down"):
			dir, base = "down", strings.TrimSuffix(base, ".down")
		default:
			return nil, fmt.Errorf("migration %s: expected .up.sql or .down.sql", name)
		}
		num, rest, ok := strings.Cut(base, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected NNNN_name", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		body, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		m := byVersion[v]
		if m == nil {
			m = &Migration{Version: v, Name: rest}
			byVersion[v] = m
		} else if m.Name != rest {
			// Two different migrations claiming one version, which is
			// what two branches written in parallel produce (DECISIONS
			// I-854). Without this the second file read simply overwrites
			// the first's Up or Down on the same entry: one migration's
			// SQL is silently dropped, `schema_migrations` records the
			// row under the other one's name, and nothing fails to build,
			// vet or test. The pair that found it was 0029_resting and
			// 0029_usage_attendance in the batch of 2026-10-22 (I-852);
			// ReadDir is sorted, so the alphabetically later name wins
			// and which migration is lost depends on its own name.
			return nil, fmt.Errorf("migrations %04d_%s and %04d_%s claim the same version; renumber one",
				m.Version, m.Name, v, rest)
		}
		if dir == "up" {
			m.Up = string(body)
		} else {
			m.Down = string(body)
		}
	}
	var out []Migration
	for _, m := range byVersion {
		if m.Up == "" || m.Down == "" {
			return nil, fmt.Errorf("migration %04d_%s: missing up or down script", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Status is the migration state of a database.
type Status struct {
	Applied []int
	Pending []int
}

// MigrateStatus reports applied and pending versions.
func MigrateStatus(ctx context.Context, pool *Pool) (Status, error) {
	var st Status
	if _, err := pool.Exec(ctx, "create table if not exists schema_migrations (version integer primary key, name text not null, applied_at timestamptz not null default now())"); err != nil {
		return st, err
	}
	ms, err := Migrations()
	if err != nil {
		return st, err
	}
	rows, err := pool.Query(ctx, "select version from schema_migrations order by version")
	if err != nil {
		return st, err
	}
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return st, err
		}
		applied[v] = true
		st.Applied = append(st.Applied, v)
	}
	rows.Close()
	for _, m := range ms {
		if !applied[m.Version] {
			st.Pending = append(st.Pending, m.Version)
		}
	}
	return st, nil
}

// errAlreadyApplied is returned from inside a migration's transaction when
// another session applied that version first; the caller skips it.
var errAlreadyApplied = errors.New("migration already applied")

// MigrateUp applies every pending migration in order, each in its own
// transaction, and returns the versions applied. Safe to run from several
// processes at once: each version is applied under an advisory lock and
// re-checked there.
func MigrateUp(ctx context.Context, pool *Pool) ([]int, error) {
	st, err := MigrateStatus(ctx, pool)
	if err != nil {
		return nil, err
	}
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	pending := map[int]bool{}
	for _, v := range st.Pending {
		pending[v] = true
	}
	var done []int
	for _, m := range ms {
		if !pending[m.Version] {
			continue
		}
		err := InTx(ctx, pool, func(tx Tx) error {
			if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(9000)); err != nil {
				return err
			}
			// Re-check under the lock: the pending set above was read
			// before it, and a replica starting at the same moment may have
			// applied this version in between (two api replicas against an
			// empty database, 2026-09-20). Idempotent by construction.
			var n int
			if err := tx.QueryRow(ctx, "select count(*) from schema_migrations where version = $1", m.Version).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return errAlreadyApplied
			}
			if _, err := tx.Exec(ctx, m.Up); err != nil {
				return fmt.Errorf("migration %04d_%s up: %w", m.Version, m.Name, err)
			}
			_, err := tx.Exec(ctx, "insert into schema_migrations (version, name) values ($1, $2)", m.Version, m.Name)
			return err
		})
		if errors.Is(err, errAlreadyApplied) {
			continue
		}
		if err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	if len(done) > 0 || len(st.Applied) > 0 {
		if err := EnsurePartitions(ctx, pool, nowFunc()); err != nil {
			return done, err
		}
	}
	return done, nil
}

// MigrateDown reverts the newest n applied migrations and returns the
// versions reverted.
func MigrateDown(ctx context.Context, pool *Pool, n int) ([]int, error) {
	st, err := MigrateStatus(ctx, pool)
	if err != nil {
		return nil, err
	}
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	byVersion := map[int]Migration{}
	for _, m := range ms {
		byVersion[m.Version] = m
	}
	var done []int
	for i := len(st.Applied) - 1; i >= 0 && n > 0; i, n = i-1, n-1 {
		m, ok := byVersion[st.Applied[i]]
		if !ok {
			return done, fmt.Errorf("migration %d is applied but not embedded in this binary", st.Applied[i])
		}
		err := InTx(ctx, pool, func(tx Tx) error {
			if _, err := tx.Exec(ctx, m.Down); err != nil {
				return fmt.Errorf("migration %04d_%s down: %w", m.Version, m.Name, err)
			}
			_, err := tx.Exec(ctx, "delete from schema_migrations where version = $1", m.Version)
			return err
		})
		if err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// MigrateTo reverts until version v is the newest applied.
func MigrateTo(ctx context.Context, pool *Pool, v int) ([]int, error) {
	st, err := MigrateStatus(ctx, pool)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, a := range st.Applied {
		if a > v {
			n++
		}
	}
	return MigrateDown(ctx, pool, n)
}
