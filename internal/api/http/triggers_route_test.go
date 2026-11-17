package httpapi_test

import (
	"strings"
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

// The prompt a browser types, over the real schema and the real route
// (DECISIONS I-1266). It goes in on `prompt`, it never comes back, and
// it is refused for the same reasons `repose triggers add -p` refuses
// it: a machine that is not running has nothing to write to, and a
// prompt over the cap is one the runner would not read.
func TestAPromptOnPostTriggers(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-prompt", "prompter")
	var uid uuid.UUID
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select id from users where logto_sub = $1", "sub-prompt").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUser(e.h.Ctx, e.h.Pool, uid)
	if err != nil {
		t.Fatal(err)
	}
	p := e.h.CreateRunning(u, "prompt-one")
	const text = "Triage the new issues; tell me what needs me"

	r := e.do(t, tok, "POST", "/projects/"+p.ID.String()+"/triggers", map[string]any{
		"name": "triage", "kind": "schedule", "spec": "weekdays 07:00",
		"zone": "Europe/Berlin", "prompt": text,
	})
	if r.status != 201 {
		t.Fatalf("add with a prompt: %d %s", r.status, r.raw)
	}
	// The answer carries a path and never the text, and nor does the row.
	if r.body["prompt_path"] != ".repose/triggers/triage.md" {
		t.Errorf("prompt_path is %v", r.body["prompt_path"])
	}
	if strings.Contains(string(r.raw), "Triage the new issues") {
		t.Fatalf("the answer carries the prompt's text: %s", r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+p.ID.String()+"/triggers", nil); strings.Contains(string(r.raw), "Triage") {
		t.Fatalf("the listing carries the prompt's text: %s", r.raw)
	}
	// It is waiting for the machine, which is the whole of where it is.
	pending, err := e.h.Triggers.PendingPrompts(e.h.Ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != text {
		t.Fatalf("the hand-off holds %+v", pending)
	}

	// Over the cap: `invalid`, with the reason a surface prints.
	big := e.do(t, tok, "POST", "/projects/"+p.ID.String()+"/triggers", map[string]any{
		"name": "big", "kind": "schedule", "spec": "daily 03:00",
		"prompt": strings.Repeat("x", triggers.PromptCap+1),
	})
	if big.status != 400 || errCode(big) != "invalid" {
		t.Fatalf("a prompt over the cap: %d %s", big.status, big.raw)
	}
	if errDetail(big, "reason") != "prompt_too_large" {
		t.Errorf("a long prompt's reason is %v", errDetail(big, "reason"))
	}

	// A machine that is not running has nothing to write to, and the
	// refusal says what to do instead, as the CLI's does.
	stopped := e.h.NewProject(u, "prompt-stopped", "large")
	bad := e.do(t, tok, "POST", "/projects/"+stopped.ID.String()+"/triggers", map[string]any{
		"name": "apply", "kind": "route", "route": "recruiting", "prompt": text,
	})
	if bad.status != 409 || errCode(bad) != "conflict" {
		t.Fatalf("a prompt on a machine that is not running: %d %s", bad.status, bad.raw)
	}
	if errDetail(bad, "reason") != "machine_not_running" {
		t.Errorf("the reason is %v", errDetail(bad, "reason"))
	}
	if !strings.Contains(string(bad.raw), "a prompt is written into the machine") {
		t.Errorf("the refusal reads %s", bad.raw)
	}
	// And the same trigger with a path instead is taken, because a file
	// already in the machine is not read until the first fire.
	ok := e.do(t, tok, "POST", "/projects/"+stopped.ID.String()+"/triggers", map[string]any{
		"name": "apply", "kind": "route", "route": "recruiting", "prompt_path": "apply.md",
	})
	if ok.status != 201 {
		t.Fatalf("add with a path on a stopped machine: %d %s", ok.status, ok.raw)
	}
}
