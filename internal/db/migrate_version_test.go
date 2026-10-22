package db

import (
	"strings"
	"testing"
	"testing/fstest"
)

// Two migrations may not claim one version (DECISIONS I-854). Without the
// guard the second file read overwrites the first's script on the same
// entry: one migration's SQL is dropped, schema_migrations records the row
// under the other's name, and nothing fails to build, vet or test. Two
// branches written in parallel produce exactly this, and two did
// (0029_resting and 0029_usage_attendance, I-852).
func TestTwoMigrationsMayNotClaimOneVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0001_init.up.sql":    {Data: []byte("create table a ();")},
		"migrations/0001_init.down.sql":  {Data: []byte("drop table a;")},
		"migrations/0002_one.up.sql":     {Data: []byte("create table b ();")},
		"migrations/0002_one.down.sql":   {Data: []byte("drop table b;")},
		"migrations/0002_other.up.sql":   {Data: []byte("create table c ();")},
		"migrations/0002_other.down.sql": {Data: []byte("drop table c;")},
	}
	_, err := migrationsFrom(fsys)
	if err == nil {
		t.Fatal("two migrations at version 2 were accepted; one of them would be silently dropped")
	}
	// The message has to name both, because the fix is to renumber one and
	// the reader needs to know which two collided.
	for _, want := range []string{"0002_one", "0002_other", "same version"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// The real directory is still read, and in version order: the guard must
// not fire on the migrations the repository actually ships.
func TestTheEmbeddedMigrationsAreAccepted(t *testing.T) {
	ms, err := Migrations()
	if err != nil {
		t.Fatalf("the embedded migrations are refused: %v", err)
	}
	if len(ms) < 2 {
		t.Fatalf("read %d migrations", len(ms))
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Version <= ms[i-1].Version {
			t.Fatalf("out of order at %d: %04d_%s then %04d_%s",
				i, ms[i-1].Version, ms[i-1].Name, ms[i].Version, ms[i].Name)
		}
	}
}
