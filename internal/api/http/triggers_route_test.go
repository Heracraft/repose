package httpapi_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/triggers"
	"github.com/heracraft/repose/internal/triggerrun"
)

// The two run listings over the real schema and the real routes
// (DECISIONS I-1248, I-1256). The cross-project one joins `projects`
// onto `trigger_runs`, and the two tables share `id`, `state` and
// `created_at`: with an unqualified column list it answered 500 to
// every call, `column reference "id" is ambiguous` (SQLSTATE 42702),
// which no test saw because the route had none and the fake api
// answers it in Go.
func TestTriggerRunListings(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-fleet", "fleet")
	var uid uuid.UUID
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select id from users where logto_sub = $1", "sub-fleet").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uid)
	if err != nil {
		t.Fatal(err)
	}
	one := e.h.CreateRunning(u, "fleet-one")
	two := e.h.CreateRunning(u, "fleet-two")

	for _, p := range []struct {
		project *store.Project
		trigger string
	}{{one, "apply"}, {two, "triage"}} {
		if r := e.do(t, tok, "POST", "/projects/"+p.project.ID.String()+"/triggers", map[string]any{
			"name": p.trigger, "kind": "schedule", "spec": "daily 03:00", "prompt_path": "p.md",
		}); r.status != 201 {
			t.Fatalf("add %s: %d %s", p.trigger, r.status, r.raw)
		}
		if _, _, err := e.h.Triggers.Apply(e.h.Ctx, triggers.Report{
			RunID: uuid.Must(uuid.NewV7()), ProjectID: p.project.ID, Trigger: p.trigger,
			State: triggerrun.StateDone, Summary: "did it", Source: triggerrun.SourceClock,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Every project's runs, each saying which machine it ran on.
	r := e.do(t, tok, "GET", "/trigger-runs", nil)
	if r.status != 200 {
		t.Fatalf("GET /trigger-runs: %d %s", r.status, r.raw)
	}
	if len(r.list) != 2 {
		t.Fatalf("%d runs across two projects: %s", len(r.list), r.raw)
	}
	slugs := map[string]bool{}
	for _, item := range r.list {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a run is %T", item)
		}
		slugs[row["project"].(string)] = true
		if row["state"] != "done" || row["summary"] != "did it" {
			t.Errorf("the join took the project's own columns: %v", row)
		}
	}
	if !slugs["fleet-one"] || !slugs["fleet-two"] {
		t.Errorf("the slugs are %v, want both projects", slugs)
	}

	// And one project's, which is the listing the tab reads.
	r = e.do(t, tok, "GET", "/projects/"+one.ID.String()+"/trigger-runs", nil)
	if r.status != 200 || len(r.list) != 1 {
		t.Fatalf("one project's runs: %d %s", r.status, r.raw)
	}
	if row := r.list[0].(map[string]any); row["trigger"] != "apply" {
		t.Errorf("the project's own run is %v", row)
	}
	// A limit outside 1..200 is the caller's mistake, not a 500.
	if r := e.do(t, tok, "GET", "/trigger-runs?limit=0", nil); r.status != 400 {
		t.Errorf("limit=0: %d %s", r.status, r.raw)
	}
	// Another account sees none of it.
	other := e.signIn(t, "sub-mallory", "mallory")
	if r := e.do(t, other, "GET", "/trigger-runs", nil); r.status != 200 || len(r.list) != 0 {
		t.Errorf("another account's listing: %d %s", r.status, r.raw)
	}
}
