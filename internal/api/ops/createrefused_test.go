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
