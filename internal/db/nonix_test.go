package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/db/testdb"
)

// 0051_nonix_base_images (DECISIONS I-1462): an erofs base needs its
// three amd64 files, arm64 is all or nothing, the format and the runtime
// take only their two values, and the defaults keep every existing row
// what it was.
func TestNonixBaseImagesConstraints(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	h := strings.Repeat("e", 64)
	ok := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	refused := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err == nil || !strings.Contains(err.Error(), "check constraint") {
			t.Fatalf("%s was not refused by a check: %v", what, err)
		}
	}
	ok("insert into base_versions (version, nix_rev) values ('n1', 'abc')")
	var format string
	if err := pool.QueryRow(ctx, "select format from base_versions where version = 'n1'").Scan(&format); err != nil || format != "nix" {
		t.Fatalf("a nix row's format: %q %v", format, err)
	}
	refused("an unknown format", "insert into base_versions (version, nix_rev, format) values ('x1', '', 'docker')")
	refused("an erofs row without files", "insert into base_versions (version, nix_rev, format) values ('e0', '', 'erofs')")
	refused("an erofs row without its initramfs", `insert into base_versions (version, nix_rev, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256)
		values ('e0', '', 'erofs', 'u', $1, 'u', $1)`, h)
	amd := `insert into base_versions (version, nix_rev, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256, initramfs_amd64_url, initramfs_amd64_sha256`
	ok(amd+`) values ('e1', '', 'erofs', 'u', $1, 'u', $1, 'u', $1)`, h)
	refused("half an arm64 set", amd+`, image_arm64_url, image_arm64_sha256) values ('e2', '', 'erofs', 'u', $1, 'u', $1, 'u', $1, 'u', $1)`, h)
	ok(amd+`, image_arm64_url, image_arm64_sha256, kernel_arm64_url, kernel_arm64_sha256, initramfs_arm64_url, initramfs_arm64_sha256)
		values ('e3', '', 'erofs', 'u', $1, 'u', $1, 'u', $1, 'u', $1, 'u', $1, 'u', $1)`, h)
	var runtime string
	var pending *string
	if err := pool.QueryRow(ctx, "select column_default from information_schema.columns where table_name = 'projects' and column_name = 'guest_runtime'").Scan(&runtime); err != nil || !strings.Contains(runtime, "nix") {
		t.Fatalf("guest_runtime default %q %v", runtime, err)
	}
	if err := pool.QueryRow(ctx, "select null::text from information_schema.columns where table_name = 'projects' and column_name = 'pending_base_version'").Scan(&pending); err != nil {
		t.Fatalf("pending_base_version missing: %v", err)
	}
	if err := pool.QueryRow(ctx, "select null::text from information_schema.columns where table_name = 'snapshots' and column_name = 'base_version'").Scan(&pending); err != nil {
		t.Fatalf("snapshots.base_version missing: %v", err)
	}
	var check string
	if err := pool.QueryRow(ctx, "select pg_get_constraintdef(oid) from pg_constraint where conname = 'projects_guest_runtime_check'").Scan(&check); err != nil || !strings.Contains(check, "erofs") {
		t.Fatalf("guest_runtime check %q %v", check, err)
	}
}
