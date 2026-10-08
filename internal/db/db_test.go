package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/db/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func TestMigrateUpDownUp(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	st, err := db.MigrateStatus(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 0 || len(st.Applied) < 2 {
		t.Fatalf("expected all applied, got %+v", st)
	}
	down, err := db.MigrateDown(ctx, pool, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(down) != 1 || down[0] != st.Applied[len(st.Applied)-1] {
		t.Fatalf("down 1 reverted %v", down)
	}
	// 0019 (projects.disk_held_bytes, I-585) is the newest: its two
	// columns go, and 0018 (meter_samples root filesystem, I-567), 0017
	// (projects.multiplexer, I-502), 0015 (the personal layer, I-490),
	// 0014 (CPU pressure, I-493), 0013 (snapshots.sha256) and 0012
	// (hosts.prev_cert_serial) stay.
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'projects' and column_name in ('disk_held_bytes', 'disk_held_at')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("projects disk held columns are still there after down 1 (0019)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'meter_samples' and column_name in ('root_used', 'root_size')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal("meter_samples root filesystem columns went with down 1 (0018 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'projects' and column_name = 'multiplexer'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("projects.multiplexer went with down 1 (0017 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.tables where table_name = 'personal_revisions'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("personal_revisions went with down 1 (0015 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'projects' and column_name = 'personal_opt_out'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("projects.personal_opt_out went with down 1 (0015 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'meter_samples' and column_name in ('cpu_pressure_us', 'host_cpu_wait_us', 'mem_used')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatal("meter_samples pressure columns went with down 1 (0014 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'snapshots' and column_name = 'sha256'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("snapshots.sha256 went with down 1 (0013 must stay)")
	}
	if err := pool.QueryRow(ctx, "select count(*) from information_schema.columns where table_name = 'hosts' and column_name = 'prev_cert_serial'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("hosts.prev_cert_serial went with down 1 (0012 must stay)")
	}
	var def string
	if err := pool.QueryRow(ctx, "select pg_get_constraintdef(oid) from pg_constraint where conname = 'subscriptions_plan_check'").Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "plus") {
		t.Fatalf("subscriptions_plan_check lost plus after down 1 (0019 reverted, 0011 kept): %s", def)
	}
	up, err := db.MigrateUp(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != 1 {
		t.Fatalf("up reapplied %v", up)
	}
	st, err = db.MigrateStatus(ctx, pool)
	if err != nil || len(st.Pending) != 0 {
		t.Fatalf("after up: %+v %v", st, err)
	}
	// 0017 stayed through the down: every existing project reads tmux,
	// and the check keeps out any other value.
	var def17, mux string
	if err := pool.QueryRow(ctx, "select column_default from information_schema.columns where table_name = 'projects' and column_name = 'multiplexer'").Scan(&def17); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def17, "tmux") {
		t.Fatalf("projects.multiplexer default %q", def17)
	}
	if _, err := pool.Exec(ctx, "insert into users (id, logto_sub, handle, email) values ('00000000-0000-7000-8000-0000000000aa', 'sub-mux', 'mux', 'mux@example.com')"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) values ('00000000-0000-7000-8000-0000000000ab', '00000000-0000-7000-8000-0000000000aa', 'm', 'm', 'small', 'stopped', 1)"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select multiplexer from projects where slug = 'm'").Scan(&mux); err != nil || mux != "tmux" {
		t.Fatalf("a project inserted without multiplexer reads %q (%v)", mux, err)
	}
	if _, err := pool.Exec(ctx, "update projects set multiplexer = 'herdr' where slug = 'm'"); err != nil {
		t.Fatalf("herdr refused: %v", err)
	}
	if _, err := pool.Exec(ctx, "update projects set multiplexer = 'screen' where slug = 'm'"); err == nil {
		t.Fatal("the check let 'screen' in")
	}
	if _, err := pool.Exec(ctx, "delete from projects; delete from users"); err != nil {
		t.Fatal(err)
	}
	// Every migration's down script reverts cleanly all the way to empty.
	all, err := db.MigrateDown(ctx, pool, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(st.Applied) {
		t.Fatalf("full down reverted %d of %d", len(all), len(st.Applied))
	}
	if _, err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
}

func TestPartitions(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now()
	if err := db.EnsurePartitions(ctx, pool, now); err != nil {
		t.Fatal(err)
	}
	old := now.AddDate(0, -5, 0)
	if err := db.EnsurePartitions(ctx, pool, old); err != nil {
		t.Fatal(err)
	}
	dropped, err := db.DropExpiredPartitions(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) < 2 {
		t.Fatalf("expected the five-month-old partitions dropped, got %v", dropped)
	}
	if _, err := pool.Exec(ctx, "insert into meter_samples (ts, project_id, state, class) values ($1, '00000000-0000-7000-8000-000000000000', 'running', 'large')", now); err != nil {
		t.Fatalf("insert into current partition: %v", err)
	}
}

func TestAdvisoryLock(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	rel, ok, err := db.TryLock(ctx, pool, db.LockRollup)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	_, ok2, err := db.TryLock(ctx, pool, db.LockRollup)
	if err != nil || ok2 {
		t.Fatalf("second lock should fail: %v %v", ok2, err)
	}
	rel()
	rel3, ok3, err := db.TryLock(ctx, pool, db.LockRollup)
	if err != nil || !ok3 {
		t.Fatalf("relock after release: %v %v", ok3, err)
	}
	rel3()
}
