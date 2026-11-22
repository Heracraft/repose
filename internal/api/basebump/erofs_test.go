package basebump_test

import (
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/basebump"
	"github.com/heracraft/repose/internal/api/store"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
)

const hex64 = "abababababababababababababababababababababababababababababababab"

// A bump (DECISIONS I-1464): an unheld erofs project gets the newest
// erofs base as pending_base_version and no op at all; a held one is left
// alone; a Nix project still gets its build, against the newest Nix base
// and never the erofs one.
func TestSweepErofsMarksPendingNixBuilds(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Erofs: true, Arch: "x86_64"}})
	ctx := h.Ctx
	h.WaitFor("host arch", func() bool {
		hr, err := store.GetHost(ctx, h.Pool, h.HostID)
		return err == nil && hr.Arch != nil
	})
	u := h.NewUser("ida")
	nix := h.CreateRunning(u, "nixy")
	ero := h.CreateRunning(u, "ero")
	held := h.CreateRunning(u, "held")
	stopped := h.CreateRunning(u, "parked")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"update projects set guest_runtime = 'erofs', base_version = 'old' where id = any($1)", []any{[]any{ero.ID, held.ID, stopped.ID}}},
		{"update projects set hold_base_updates = true where id = $1", []any{held.ID}},
		{"update projects set state = 'stopped' where id = $1", []any{stopped.ID}},
		{"insert into base_versions (version, nix_rev, changelog, released_at) values ('2026.11.22', 'abc123', 'nix', now() - interval '1 minute')", nil},
		{`insert into base_versions (version, nix_rev, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256,
			initramfs_amd64_url, initramfs_amd64_sha256, kernel_version, manifest_url)
			values ('2026.11.22.e1', '', 'erofs', 'https://x.test/r', $1, 'https://x.test/k', $1, 'https://x.test/i', $1, '6.8.0', 'https://x.test/m')`, []any{hex64}},
	} {
		if _, err := h.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	opsBefore := func(id any) int {
		var n int
		_ = h.Pool.QueryRow(ctx, "select count(*) from ops where project_id = $1", id).Scan(&n)
		return n
	}
	eroOps, heldOps, stoppedOps := opsBefore(ero.ID), opsBefore(held.ID), opsBefore(stopped.ID)
	job := basebump.New(h.Pool, h.Engine, h.Events, h.Log)
	touched, err := job.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{nix.ID.String(): true, ero.ID.String(): true, stopped.ID.String(): true}
	if len(touched) != len(want) {
		t.Fatalf("touched %v", touched)
	}
	for _, id := range touched {
		if !want[id.String()] {
			t.Fatalf("touched %s, want %v", id, want)
		}
	}
	for _, p := range []*store.Project{ero, stopped} {
		got := h.Project(p.ID)
		if got.PendingBaseVersion == nil || *got.PendingBaseVersion != "2026.11.22.e1" {
			t.Fatalf("%s pending %v", p.Slug, got.PendingBaseVersion)
		}
	}
	if n := opsBefore(ero.ID); n != eroOps {
		t.Fatalf("erofs project got %d ops from the bump", n-eroOps)
	}
	if n := opsBefore(stopped.ID); n != stoppedOps {
		t.Fatalf("stopped erofs project got %d ops from the bump", n-stoppedOps)
	}
	if got := h.Project(held.ID); got.PendingBaseVersion != nil || opsBefore(held.ID) != heldOps {
		t.Fatalf("held erofs project touched: pending %v", got.PendingBaseVersion)
	}
	nixOps, _ := store.ListProjectOps(ctx, h.Pool, nix.ID, 5)
	if len(nixOps) == 0 || nixOps[0].Kind != "build" || nixOps[0].Params["base_bump"] != "2026.11.22" {
		t.Fatalf("nix project's bump op: %+v", nixOps)
	}
	if got := h.WaitOp(nixOps[0].ID); got.State != "done" {
		t.Fatalf("nix bump: %+v", got.Error)
	}
	if got := h.Project(nix.ID); got.PendingBaseVersion != nil {
		t.Fatal("a nix project got a pending base")
	}
	// A second sweep changes nothing.
	if again, _ := job.Sweep(ctx); len(again) != 0 {
		t.Fatalf("second sweep touched %v", again)
	}
}
