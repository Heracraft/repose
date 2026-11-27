package ops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
)

// DECISIONS I-1648: a create whose CreateGuest the host refuses for
// capacity leaves nothing behind, as a refusal at placement does: no
// project in error that `repose start` cannot start (the lab's F2,
// 2026-11-25 18:09Z and again 2026-11-26 01:15Z). The host made nothing:
// each of these refusals comes before hostd writes a record or a volume.
// The op still fails with the same code and sentence, so the CLI's exit
// 8 and message are unchanged, and the name is free for the next try.
func TestCreateRefusedByTheHostLeavesNoProject(t *testing.T) {
	for name, msg := range map[string]string{
		// internal/hostd/guest/owned.go memRoom (fleet, a class that no
		// longer fits by the time the command lands)
		"class": "create: not enough free memory for a large guest",
		// internal/hostd/guest/owned.go ownedCPURoom (I-1640)
		"cpu bound": "not enough free CPU: 8 of 8 vCPUs taken by running machines on this host, a machine starts with 2",
		// internal/hostd/guest/owned.go ownedRoom (owned, memory gone
		// between placement and the command)
		"memory race": "not enough free memory: 800 MiB free on this host, a machine starts with 1024 MiB",
	} {
		t.Run(name, func(t *testing.T) {
			h := apitest.New(t, apitest.Options{})
			u := h.NewUser("ines")
			p := h.NewProject(u, "pocketmail", "small")
			pid := p.ID
			h.Fake.SetFail("CreateGuest", "insufficient_capacity")
			h.Fake.SetFailMessage("CreateGuest", msg)
			op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
			if op.State != "error" || op.Error["code"] != "insufficient_capacity" {
				t.Fatalf("op %s %+v, want error insufficient_capacity", op.State, op.Error)
			}
			p = h.Project(pid)
			if p.DestroyedAt == nil || p.State != "destroyed" || p.HostID != nil {
				t.Fatalf("project %s destroyed_at %v host %v, want destroyed with no host", p.State, p.DestroyedAt, p.HostID)
			}
			if p.LastError == nil || !strings.HasPrefix(*p.LastError, ops.CreateRefusedPrefix) {
				t.Fatalf("last_error %v, want %q and the sentence", p.LastError, ops.CreateRefusedPrefix)
			}
			list, err := store.ListUserProjects(h.Ctx, h.Pool, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 0 {
				t.Fatalf("listed %d projects, want none: %+v", len(list), list[0].Slug)
			}
			evs, _ := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
			for _, e := range evs {
				if strings.Contains(e.Kind, "fail") || strings.Contains(e.Kind, "stopped") || strings.Contains(e.Kind, "destroy") {
					t.Fatalf("event %s %q: the refusal is the CLI's and the dashboard's to show", e.Kind, e.Summary)
				}
			}
			// The name is free, and the next create, with room, runs.
			h.Fake.SetFail("CreateGuest", "")
			again := h.NewProject(u, "pocketmail", "small")
			aid := again.ID
			if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &aid, Phases: ops.PlanCreate()})); op.State != "done" {
				t.Fatalf("second create: %+v", op.Error)
			}
		})
	}
}

// Any other create failure keeps its project in error, as before: the
// host may hold a record or a volume of it.
func TestCreateFailedOtherwiseKeepsItsProject(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ines")
	p := h.NewProject(u, "pocketmail", "small")
	pid := p.ID
	h.Fake.SetFail("CreateGuest", "boot_failed")
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})); op.State != "error" {
		t.Fatalf("op %s", op.State)
	}
	if p = h.Project(pid); p.State != "error" || p.DestroyedAt != nil {
		t.Fatalf("project %s destroyed_at %v, want error and kept", p.State, p.DestroyedAt)
	}
}

// DECISIONS I-1654: on a host the account owns, a create or start the host
// refuses for its cores names the host, the machines running there and
// both ways out: stopping one of them, or, for a create, repose's
// servers. review-cli 7 and review-mac 03:42Z: the sentence named
// neither the host nor the machines, one of which was hung.
func TestOwnedCPURefusalNamesTheHostAndTheWayOut(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ines")
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set name = 'mac-mini', owner_account_id = $1 where id = $2", u.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"beta", "alpha"} {
		if _, err := h.Pool.Exec(h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes, started_at)
			values ($1, $2, $3, $3, 'large', 'running', $4, 1, now())`, store.NewID(), u.ID, slug, h.HostID); err != nil {
			t.Fatal(err)
		}
	}
	h.Fake.SetFail("CreateGuest", "insufficient_capacity")
	h.Fake.SetFailMessage("CreateGuest", "not enough free CPU: 6 of 6 vCPUs taken by running machines on this host, a machine starts with 2")
	p := h.NewProject(u, "pocketmail", "small")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	want := "your host mac-mini is running as many machines as it can. Stop alpha or beta there, or run it on repose's servers: repose run --host repose"
	if op.Error["code"] != "insufficient_capacity" || op.Error["message"] != want {
		t.Fatalf("create refused for cores: %+v\nwant message %q", op.Error, want)
	}

	h.Fake.SetFail("CreateGuest", "")
	q := h.NewProject(u, "folio", "small")
	qid := q.ID
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &qid, Phases: ops.PlanCreate()})); op.State != "done" {
		t.Fatalf("create folio: %+v", op.Error)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &qid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop folio: %+v", op.Error)
	}
	h.Fake.SetFail("StartGuest", "insufficient_capacity")
	h.Fake.SetFailMessage("StartGuest", "not enough free CPU: 6 of 6 vCPUs taken by running machines on this host, a machine starts with 2")
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &qid, Phases: ops.PlanStart(false)}))
	want = "your host mac-mini is running as many machines as it can. Stop alpha or beta there"
	if op.Error["message"] != want {
		t.Fatalf("start refused for cores: %+v\nwant message %q", op.Error, want)
	}
}

// The owner's own run, 2026-11-27: "Could not start muqsit-labs: the
// machines running on this host hold the memory it gives its machines;
// stopping a running machine frees room (insufficient_capacity)." On a Mac
// the refusal names the Mac, says it is full and names what to stop
// (I-1724).
func TestMacFullRefusalIsPlain(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("owner")
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set name = 'e2e-d2lab', arch = 'aarch64', owner_account_id = $1 where id = $2", u.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes, started_at)
		values ($1, $2, 'api', 'api', 'large', 'running', $3, 1, now())`, store.NewID(), u.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	h.Fake.SetFail("CreateGuest", "insufficient_capacity")
	h.Fake.SetFailMessage("CreateGuest", "the machines running on this host hold 22528 of the 24576 MiB it gives its machines, and one starts with at least 2048 MiB: stop one first")
	p := h.NewProject(u, "muqsit-labs", "small")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	want := "your Mac e2e-d2lab is full. Stop api there, or run it on repose's servers: repose run --host repose"
	if op.Error["code"] != "insufficient_capacity" || op.Error["message"] != want {
		t.Fatalf("Mac full: %+v\nwant message %q", op.Error, want)
	}
}

// Review pass 2, P1-2: on a locked Mac the machines that hold its memory
// rest (paused, not saved). The refusal says they are resting, by name,
// and that stopping one makes room.
func TestMacFullOfRestingMachinesSaysSo(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("owner")
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set name = 'e2e-d2lab', arch = 'aarch64', owner_account_id = $1 where id = $2", u.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"beta", "alpha"} {
		if _, err := h.Pool.Exec(h.Ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes, started_at, resting_since)
			values ($1, $2, $3, $3, 'large', 'running', $4, 1, now(), now())`, store.NewID(), u.ID, slug, h.HostID); err != nil {
			t.Fatal(err)
		}
	}
	h.Fake.SetFail("CreateGuest", "insufficient_capacity")
	h.Fake.SetFailMessage("CreateGuest", "the machines running on this host hold 16384 of the 17408 MiB it gives its machines, and one starts with at least 2048 MiB: stop one first")
	p := h.NewProject(u, "e2e-p2d", "small")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	want := "your Mac e2e-d2lab is full: alpha and beta are resting there and hold its memory. Stop one of them, or run it on repose's servers: repose run --host repose"
	if op.Error["message"] != want {
		t.Fatalf("Mac full of resting machines: %+v\nwant message %q", op.Error, want)
	}
}
