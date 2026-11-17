package api

import (
	"testing"
	"time"
)

// The cross-project run listing (GET /v1/trigger-runs), which
// `repose triggers runs --all` reads. It is tested because the first
// version took f.mu, which ServeHTTP already holds for the whole
// dispatch: the route deadlocked, and no test called it, so the suite
// stayed green.
func TestFakeListsEveryProjectsTriggerRuns(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	one, err := f.CreateProject("fleet-one", "large")
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.CreateProject("fleet-two", "large")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		id, name string
		ago      time.Duration
	}{{one.ID, "apply", 2 * time.Hour}, {two.ID, "triage", time.Hour}} {
		r := call(t, f, "POST", "/v1/projects/"+p.id+"/triggers", tok,
			map[string]any{"name": p.name, "kind": "schedule", "spec": "daily 03:00"})
		want(t, r, 201)
		f.AddTriggerRun(p.id, p.name, FakeTriggerRun{
			State: "done", Summary: "did it", Source: "clock",
			CreatedAt: time.Now().UTC().Add(-p.ago),
		})
	}

	var runs []FakeTriggerRun
	r := call(t, f, "GET", "/v1/trigger-runs", tok, nil)
	want(t, r, 200)
	r.json(t, &runs)
	if len(runs) != 2 {
		t.Fatalf("%d runs across two projects, want 2", len(runs))
	}
	// Newest first, each saying which machine it ran on.
	if runs[0].Project != "fleet-two" || runs[1].Project != "fleet-one" {
		t.Errorf("projects %q then %q, want fleet-two then fleet-one", runs[0].Project, runs[1].Project)
	}
	// The per-project listing says nothing about another project's.
	var mine []FakeTriggerRun
	r = call(t, f, "GET", "/v1/projects/"+one.ID+"/trigger-runs", tok, nil)
	want(t, r, 200)
	r.json(t, &mine)
	if len(mine) != 1 || mine[0].Trigger != "apply" {
		t.Errorf("one project's runs: %+v", mine)
	}

	r = call(t, f, "GET", "/v1/trigger-runs?limit=0", tok, nil)
	want(t, r, 400)
}
