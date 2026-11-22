package ops_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
)

// A machine on a host that is lost or retired is destroyed without the
// host: nothing is sent, the row ends destroyed, its snapshots kept on
// that host are marked gone_with_host, and the op is done at once.
func TestDestroyOnLostHostFinalises(t *testing.T) {
	for _, state := range []string{"lost", "retired"} {
		t.Run(state, func(t *testing.T) {
			h := apitest.New(t, apitest.Options{})
			u := h.NewUser("lou")
			p := h.CreateRunning(u, "pk2")
			ctx := h.Ctx
			// A snapshot the host kept, and the row as production has it.
			if _, err := h.Pool.Exec(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason) values (gen_random_uuid(), $1, $2, 'local/pk2/1.img.zst', 1, 'stop')`, p.ID, h.HostID); err != nil {
				t.Fatal(err)
			}
			// The account's own host, whose snapshots live on its disk.
			if _, err := h.Pool.Exec(ctx, "update hosts set state = $2, owner_account_id = $3 where id = $1", h.HostID, state, u.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Pool.Exec(ctx, "update projects set state = 'error', last_error = 'host_lost: the host is gone' where id = $1", p.ID); err != nil {
				t.Fatal(err)
			}
			// The host is gone: no stream, no heartbeat.
			h.DisconnectHost()
			before := len(h.Fake.Commands())
			p = h.Project(p.ID)
			pid := p.ID
			op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}))
			if op.State != "done" {
				t.Fatalf("destroy on a %s host: %s %+v", state, op.State, op.Error)
			}
			if got := h.Project(pid); got.State != "destroyed" || got.DestroyedAt == nil {
				t.Fatalf("row after destroy: state %s destroyed_at %v", got.State, got.DestroyedAt)
			}
			if n := len(h.Fake.Commands()); n != before {
				t.Fatalf("%d commands sent to a %s host", n-before, state)
			}
			var gone *string
			if err := h.Pool.QueryRow(ctx, "select gone_with_host from snapshots where project_id = $1", pid).Scan(&gone); err != nil || gone == nil || *gone == "" {
				t.Fatalf("snapshot on the %s host: gone_with_host %v %v", state, gone, err)
			}
		})
	}
}

// On a host repose runs the snapshot is in repose's store, so a destroy
// on that host lost keeps it as the final snapshot (I-1548).
func TestDestroyOnLostFleetHostKeepsStoreSnapshot(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("fleet")
	p := h.CreateRunning(u, "live1g")
	ctx := h.Ctx
	if _, err := h.Pool.Exec(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason) values (gen_random_uuid(), $1, $2, 'u/live1g/1.img.zst', 1, 'stop')`, p.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, "update hosts set state = 'lost' where id = $1", h.HostID); err != nil {
		t.Fatal(err)
	}
	h.DisconnectHost()
	p = h.Project(p.ID)
	pid := p.ID
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)})); op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
	var gone *string
	var expires any
	if err := h.Pool.QueryRow(ctx, "select gone_with_host, expires_at from snapshots where project_id = $1", pid).Scan(&gone, &expires); err != nil || gone != nil || expires == nil {
		t.Fatalf("store snapshot: gone_with_host %v expires %v %v", gone, expires, err)
	}
}

// Production's shape (live1g, pk2): a destroy enqueued while the host was
// only unreachable failed with host_unreachable, and the host was then
// marked lost. The engine destroys it again, without the host (I-1548).
func TestFailedDestroyRerunsWhenHostGone(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("prod")
	p := h.CreateRunning(u, "live1g")
	ctx := h.Ctx
	pid := p.ID
	if _, err := h.Pool.Exec(ctx, `insert into ops (id, project_id, kind, state, step, host_id, params, error, finished_at)
		values (gen_random_uuid(), $1, 'destroy', 'error', 1, $2, '{"phases": ["wake", "stop_guest", "snapshot", "destroy_guest"]}',
		        '{"code": "host_unreachable", "message": "host unreachable"}', now())`, pid, h.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, "update projects set state = 'error', last_error = 'host_unreachable: the host stopped answering' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	h.DisconnectHost()
	if _, err := h.Pool.Exec(ctx, "update hosts set state = 'lost' where id = $1", h.HostID); err != nil {
		t.Fatal(err)
	}
	h.Engine.Kick()
	// The re-run is enqueued on a tick, and the row reads destroyed a
	// moment before its op is marked done (markDestroyed runs inside the
	// last phase's result), so wait for the op itself, then read both.
	var rerun uuid.UUID
	h.WaitFor("a fresh destroy op", func() bool {
		return h.Pool.QueryRow(ctx, "select id from ops where project_id = $1 and kind = 'destroy' and state <> 'error' order by created_at desc limit 1", pid).Scan(&rerun) == nil
	})
	if op := h.WaitOp(rerun); op.State != "done" {
		t.Fatalf("re-run destroy: %s %+v", op.State, op.Error)
	}
	if got := h.Project(pid); got.State != "destroyed" {
		t.Fatalf("row after the re-run: %s", got.State)
	}
	var n int
	if err := h.Pool.QueryRow(ctx, "select count(*) from ops where project_id = $1 and kind = 'destroy' and state = 'done'", pid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("done destroy ops %d %v", n, err)
	}
}

// A destroy whose command waits on a host that becomes lost goes on
// without it at once, instead of failing host_unreachable after the grace.
func TestInFlightDestroyFinishesWhenHostGone(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("flight")
	p := h.CreateRunning(u, "pk2")
	ctx := h.Ctx
	pid := p.ID
	h.DisconnectHost()
	if _, err := h.Pool.Exec(ctx, "update hosts set state = 'unreachable' where id = $1", h.HostID); err != nil {
		t.Fatal(err)
	}
	id := h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(h.Project(pid))})
	h.WaitFor("the destroy's command sent", func() bool {
		var sent bool
		_ = h.Pool.QueryRow(ctx, "select sent_at is not null from ops where id = $1", id).Scan(&sent)
		return sent
	})
	if _, err := h.Pool.Exec(ctx, "update hosts set state = 'lost' where id = $1", h.HostID); err != nil {
		t.Fatal(err)
	}
	h.Engine.Kick()
	if op := h.WaitOp(id); op.State != "done" {
		t.Fatalf("in-flight destroy: %s %+v", op.State, op.Error)
	}
	if got := h.Project(pid); got.State != "destroyed" {
		t.Fatalf("row %s", got.State)
	}
}
