package httpapi_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/config"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// currentRev is the project's active revision.
func (e *env) currentRev(t *testing.T, pid string) *store.Revision {
	t.Helper()
	p := e.h.Project(uuid.MustParse(pid))
	if p.ConfigRevisionID == nil {
		t.Fatalf("%s has no revision", pid)
	}
	rev, err := store.GetRevision(e.h.Ctx, e.h.Pool, *p.ConfigRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

// builds lists the Build commands the fake host received for a project.
func (e *env) builds(pid string) []*hostdv1.Build {
	var out []*hostdv1.Build
	for _, c := range e.h.Fake.Commands() {
		if b := c.GetBuild(); b != nil && b.ProjectId == pid {
			out = append(out, b)
		}
	}
	return out
}

// TestPersonalLayer covers DECISIONS I-490 at the api: the account's
// machine.nix is stored with revisions, reaches every project that has
// not opted out (a running one in place, a stopped one at its next
// start), a new machine comes up on the project layer and applies the
// combined revision right after, the CLI's base revision check refuses a
// copy edited elsewhere, and a broken file leaves machines as they were
// with a notification naming machine.nix.
func TestPersonalLayer(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-pers", "pers")
	r := e.do(t, tok, "GET", "/me/config", nil)
	if r.status != 200 || r.body["revision_id"] != nil || r.body["fragment"] != "" {
		t.Fatalf("empty personal: %d %s", r.status, r.raw)
	}

	// Without a personal layer a create is exactly what it was.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pa", "class": "small"})
	pa := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create pa: %+v", op.Error)
	}
	if b := e.builds(pa); len(b) != 1 || len(b[0].Personal) != 0 {
		t.Fatalf("pa builds: %v", b)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pb", "class": "small"})
	pb := r.body["id"].(string)
	e.waitOp(t, r)
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pb+"/stop", nil)); op.State != "done" {
		t.Fatalf("stop pb: %+v", op.Error)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pc", "class": "small", "personal_opt_out": true})
	pc := r.body["id"].(string)
	e.waitOp(t, r)
	if r.body["personal_opt_out"] != true {
		t.Fatalf("pc opt-out not in the project: %s", r.raw)
	}

	// The first save names no base: the account has none.
	f1 := "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }"
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f1, "base_revision_id": ""})
	if r.status != 200 || r.body["revision_id"] == nil {
		t.Fatalf("put personal: %d %s", r.status, r.raw)
	}
	r1 := r.body["revision_id"].(string)
	projects := r.body["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("fan-out reached %d projects, want pa and pb: %s", len(projects), r.raw)
	}
	for _, x := range projects {
		if x.(map[string]any)["project_id"] == pc {
			t.Fatal("an opted-out project got the personal layer")
		}
	}
	e.h.WaitIdle(uuid.MustParse(pa))
	e.h.WaitIdle(uuid.MustParse(pb))
	if rev := e.currentRev(t, pa); rev.Personal != f1 || rev.Status != "applied" || rev.PersonalRevisionID == nil || rev.PersonalRevisionID.String() != r1 {
		t.Fatalf("running pa: %+v", rev)
	}
	if b := e.builds(pa); string(b[len(b)-1].Personal) != f1 {
		t.Fatal("Build did not carry the personal layer")
	}
	// Stopped: built now, applied by the next start.
	if rev := e.currentRev(t, pb); rev.Personal != "" {
		t.Fatalf("stopped pb switched while stopped: %+v", rev)
	}
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pb+"/start", nil)); op.State != "done" {
		t.Fatalf("start pb: %+v", op.Error)
	}
	if rev := e.currentRev(t, pb); rev.Personal != f1 || rev.Status != "applied" {
		t.Fatalf("pb after start: %+v", rev)
	}
	if rev := e.currentRev(t, pc); rev.Personal != "" || !rev.PersonalOptOut {
		t.Fatalf("pc: %+v", rev)
	}

	// A copy pushed from an older revision is refused; the same text is
	// unchanged and rebuilds nothing.
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f1 + " ", "base_revision_id": ""})
	if r.status != 409 || errCode(r) != "conflict" || r.body["error"].(map[string]any)["detail"].(map[string]any)["current_revision_id"] != r1 {
		t.Fatalf("stale base: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f1, "base_revision_id": r1})
	if r.status != 200 || r.body["unchanged"] != true || len(r.body["projects"].([]any)) != 0 {
		t.Fatalf("unchanged: %d %s", r.status, r.raw)
	}
	if _, ok := config.NewParser(); ok {
		r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": "{\n  home.packages = [ ;\n}"})
		if r.status != 400 || !strings.Contains(string(r.raw), "machine.nix:2:") || r.body["error"].(map[string]any)["detail"].(map[string]any)["personal_line"].(float64) != 2 {
			t.Fatalf("parse error: %d %s", r.status, r.raw)
		}
	}
	r = e.do(t, tok, "GET", "/me/config", nil)
	if r.body["revision_id"] != r1 || r.body["fragment"] != f1 || len(r.body["opted_out"].([]any)) != 1 {
		t.Fatalf("get personal: %s", r.raw)
	}

	// Never block: a new machine comes up on the project layer (the
	// combined closure is on no host) and applies the combined revision
	// right after, without the caller asking.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pd", "class": "small"})
	pd := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create pd: %+v", op.Error)
	}
	e.h.WaitIdle(uuid.MustParse(pd))
	bs := e.builds(pd)
	if len(bs) != 2 || len(bs[0].Personal) != 0 || string(bs[1].Personal) != f1 {
		t.Fatalf("pd builds: first should be the project layer alone, second the combined one: %v", bs)
	}
	if rev := e.currentRev(t, pd); rev.Personal != f1 || rev.Status != "applied" {
		t.Fatalf("pd after create: %+v", rev)
	}
	var creates, pcmd int
	for _, c := range e.h.Fake.Commands() {
		if cg := c.GetCreateGuest(); cg != nil && cg.ProjectId == pd {
			creates++
		}
		if ac := c.GetApplyConfig(); ac != nil && strings.HasSuffix(ac.SystemClosure, bs[1].RevisionId) {
			pcmd++
		}
	}
	if creates != 1 || pcmd != 1 {
		t.Fatalf("pd: %d creates, %d applies of the combined revision", creates, pcmd)
	}

	// Opting out rebuilds without the layer; opting in brings it back.
	r = e.do(t, tok, "PATCH", "/projects/"+pd, map[string]any{"personal_opt_out": true})
	if r.status != 200 || r.body["personal_opt_out"] != true {
		t.Fatalf("opt out: %d %s", r.status, r.raw)
	}
	e.h.WaitIdle(uuid.MustParse(pd))
	if rev := e.currentRev(t, pd); rev.Personal != "" || !rev.PersonalOptOut || rev.Status != "applied" {
		t.Fatalf("pd opted out: %+v", rev)
	}
	e.do(t, tok, "PATCH", "/projects/"+pd, map[string]any{"personal_opt_out": false})
	e.h.WaitIdle(uuid.MustParse(pd))
	if rev := e.currentRev(t, pd); rev.Personal != f1 {
		t.Fatalf("pd opted back in: %+v", rev)
	}

	// A project fragment change keeps the layer.
	r = e.do(t, tok, "PUT", "/projects/"+pa+"/config", map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.deno ]; }"})
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("pa fragment: %+v", op.Error)
	}
	if rev := e.currentRev(t, pa); rev.Personal != f1 || !strings.Contains(rev.Fragment, "deno") {
		t.Fatalf("pa after a fragment change: %+v", rev)
	}

	// A broken machine.nix leaves every machine on what it runs and says
	// which file to fix.
	before := e.currentRev(t, pa).ID
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": "{ fake-eval-error = 1; }", "source": "dashboard"})
	if r.status != 200 {
		t.Fatalf("broken put: %d %s", r.status, r.raw)
	}
	e.h.WaitIdle(uuid.MustParse(pa))
	if e.currentRev(t, pa).ID != before {
		t.Fatal("a failed personal build changed pa's active revision")
	}
	var failedLine *int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select personal_line from config_revisions where project_id = $1 and status = 'failed' order by created_at desc limit 1", pa).Scan(&failedLine); err != nil || failedLine == nil || *failedLine != 1 {
		t.Fatalf("failed revision's personal_line: %v %v", failedLine, err)
	}
	var n int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from events where project_id = $1 and kind = 'personal_failed' and summary like 'machine.nix did not apply to pa%'", pa).Scan(&n); err != nil || n != 1 {
		t.Fatalf("personal_failed events: %d %v", n, err)
	}
	if r := e.do(t, tok, "GET", "/me/config/revisions", nil); r.status != 200 || len(r.list) != 2 || r.list[0].(map[string]any)["source"] != "dashboard" {
		t.Fatalf("revisions: %d %s", r.status, r.raw)
	}
}

// TestPersonalCreateReuse: with a published base, the first machine after
// a personal save is deferred (no closure of the combination on the
// host), and the next one finds the combined closure the first applied
// and boots straight onto it with no Build at all (DECISIONS I-160,
// I-490). A secret's value in machine.nix fails the build naming the file.
func TestPersonalCreateReuse(t *testing.T) {
	e := newEnv(t)
	if _, err := e.h.Pool.Exec(e.h.Ctx, "insert into base_versions (version, nix_rev) values ('2026.10.04', 'abc123')"); err != nil {
		t.Fatal(err)
	}
	tok := e.signIn(t, "sub-reuse", "reuse")
	f := "{ pkgs, ... }: { home.packages = [ pkgs.fd ]; }"
	if r := e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.raw)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "r1", "class": "small"})
	p1 := r.body["id"].(string)
	e.waitOp(t, r)
	e.h.WaitIdle(uuid.MustParse(p1))
	if b := e.builds(p1); len(b) != 2 {
		t.Fatalf("r1: %d builds, want the project layer then the combination", len(b))
	}
	if rev := e.currentRev(t, p1); rev.Personal != f || rev.Status != "applied" {
		t.Fatalf("r1: %+v", rev)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "r2", "class": "small"})
	p2 := r.body["id"].(string)
	e.waitOp(t, r)
	e.h.WaitIdle(uuid.MustParse(p2))
	if b := e.builds(p2); len(b) != 0 {
		t.Fatalf("r2: %d builds, want the reused combination", len(b))
	}
	if rev := e.currentRev(t, p2); rev.Personal != f || rev.Status != "applied" {
		t.Fatalf("r2: %+v", rev)
	}

	const val = "sk-personal-secret-0123456789"
	if r := e.do(t, tok, "PUT", "/projects/"+p1+"/secrets/API_KEY", map[string]any{"value": base64.StdEncoding.EncodeToString([]byte(val))}); r.status != 200 {
		t.Fatalf("secret: %d %s", r.status, r.raw)
	}
	e.h.WaitIdle(uuid.MustParse(p1))
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": "{ home.sessionVariables.K = \"" + val + "\"; }"})
	if r.status != 200 {
		t.Fatalf("put with secret: %d %s", r.status, r.raw)
	}
	var opID string
	for _, x := range r.body["projects"].([]any) {
		if m := x.(map[string]any); m["project_id"] == p1 {
			opID, _ = m["op_id"].(string)
		}
	}
	op := e.h.WaitOp(uuid.MustParse(opID))
	if op.State != "error" || op.Error["message"] != "machine.nix contains the value of secret API_KEY" {
		t.Fatalf("secret in machine.nix: %s %+v", op.State, op.Error)
	}
	if strings.Contains(string(e.logs.Bytes()), val) {
		t.Fatal("the secret's value reached a log line")
	}
}

// TestPersonalStartYields: a start right after a personal save is not
// refused because the save queued a build for the stopped machine; the
// machine boots and the new layer reaches it (DECISIONS I-490).
func TestPersonalStartYields(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-yield", "yield")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "y1", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil))
	for i, f := range []string{"{ home.packages = [ ]; }", "{ home.sessionVariables.A = \"1\"; }"} {
		e.h.StopEngine()
		if r := e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f}); r.status != 200 {
			t.Fatalf("put %d: %d %s", i, r.status, r.raw)
		}
		r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
		if r.status != 202 {
			t.Fatalf("start %d after a personal save: %d %s", i, r.status, r.raw)
		}
		e.h.StartEngine(ops.Config{BaseRef: "deadbeef"})
		if op := e.waitOp(t, r); op.State != "done" {
			t.Fatalf("start %d: %+v", i, op.Error)
		}
		e.h.WaitIdle(uuid.MustParse(pid))
		if rev := e.currentRev(t, pid); rev.Personal != f || rev.Status != "applied" {
			rows, _ := e.h.Pool.Query(e.h.Ctx, "select kind, state, coalesce(revision_id::text,''), params::text, coalesce(error::text,'') from ops where project_id = $1 order by created_at", pid)
			for rows.Next() {
				var a, b, c, d, ee string
				_ = rows.Scan(&a, &b, &c, &d, &ee)
				t.Logf("op %s %s %s %s %s", a, b, c, d, ee)
			}
			rows.Close()
			t.Fatalf("after start %d: %+v", i, rev.Personal)
		}
		e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil))
	}
}

// TestPersonalConfigChangeSupersedes: a project configuration change
// right after a machine.nix save (a run that pushes machine.nix and then
// sends the checkout's repose.nix) is not refused because the save queued
// a build; the queued build is superseded by the change, which carries
// both (DECISIONS I-490).
func TestPersonalConfigChangeSupersedes(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-super", "super")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "s1", "class": "small"})
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	e.h.StopEngine()
	f := "{ home.sessionVariables.B = \"2\"; }"
	r = e.do(t, tok, "PUT", "/me/config", map[string]any{"fragment": f})
	if r.status != 200 {
		t.Fatalf("put personal: %d %s", r.status, r.raw)
	}
	stale := r.body["projects"].([]any)[0].(map[string]any)["revision_id"].(string)
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ pkgs, ... }: { home.packages = [ pkgs.zig ]; }"})
	if r.status != 202 {
		t.Fatalf("config change after a personal save: %d %s", r.status, r.raw)
	}
	e.h.StartEngine(ops.Config{BaseRef: "deadbeef"})
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("config op: %+v", op.Error)
	}
	e.h.WaitIdle(uuid.MustParse(pid))
	if rev := e.currentRev(t, pid); rev.Personal != f || !strings.Contains(rev.Fragment, "zig") || rev.Status != "applied" {
		t.Fatalf("after: %+v", rev)
	}
	old, err := store.GetRevision(e.h.Ctx, e.h.Pool, uuid.MustParse(stale))
	if err != nil || old.Status != "failed" || old.Error == nil || !strings.HasPrefix(*old.Error, "superseded by revision ") {
		t.Fatalf("superseded revision: %+v %v", old, err)
	}
}
