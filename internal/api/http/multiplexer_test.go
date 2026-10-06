package httpapi_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	httpapi "github.com/heracraft/repose/internal/api/http"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// gateDetail is the 409's detail, or nil when r is not the gate's refusal.
func gateDetail(r resp) map[string]any {
	if r.status != 409 || errCode(r) != "conflict" {
		return nil
	}
	d, _ := r.body["error"].(map[string]any)["detail"].(map[string]any)
	if d["reason"] != "base_update_needed" {
		return nil
	}
	return d
}

// lastProjectJSON is the project_json of the newest CreateGuest,
// StartGuest or Restore the fake host received for a project.
func (e *env) lastProjectJSON(t *testing.T, pid string) map[string]any {
	t.Helper()
	p := e.h.Project(uuid.MustParse(pid))
	var raw []byte
	for _, c := range e.h.Fake.Commands() {
		switch x := c.Cmd.(type) {
		case *hostdv1.Command_CreateGuest:
			if x.CreateGuest.ProjectId == pid {
				raw = x.CreateGuest.ProjectJson
			}
		case *hostdv1.Command_StartGuest:
			if p.GuestID != nil && x.StartGuest.GuestId == p.GuestID.String() && len(x.StartGuest.ProjectJson) > 0 {
				raw = x.StartGuest.ProjectJson
			}
		case *hostdv1.Command_Restore:
			if x.Restore.ProjectId == pid {
				raw = x.Restore.ProjectJson
			}
		}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("project_json %q: %v", raw, err)
	}
	return out
}

// TestMultiplexerField covers DECISIONS I-502 at the api: the field on
// POST and PATCH, its default, the 400 outside tmux and herdr, the base
// gate's 409 (empty min base, a base older than it, a null base, a min
// base with no row), tmux never refused, and the value reaching the guest
// in project_json at the next start and not before.
func TestMultiplexerField(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-mux", "mux")

	// Old shape: no field gives tmux, and a client that knows nothing of
	// the field still decodes the Project.
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "ma", "class": "small"})
	if r.status != 201 || r.body["multiplexer"] != "tmux" {
		t.Fatalf("create without multiplexer: %d %s", r.status, r.raw)
	}
	var old struct {
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		State string `json:"state"`
	}
	dec := json.NewDecoder(bytes.NewReader(r.raw))
	if err := dec.Decode(&old); err != nil || old.Slug != "ma" {
		t.Fatalf("an old client's decode: %v %+v", err, old)
	}
	ma := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create ma: %+v", op.Error)
	}
	if pj := e.lastProjectJSON(t, ma); pj["multiplexer"] != "tmux" {
		t.Fatalf("create project_json: %v", pj)
	}

	// Outside the two names: 400 on both routes, nothing written.
	for _, v := range []any{"screen", "", "HERDR", 3} {
		if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "mx", "class": "small", "multiplexer": v}); r.status != 400 || errCode(r) != "invalid" {
			t.Fatalf("POST multiplexer %v: %d %s", v, r.status, r.raw)
		}
		if r := e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": v}); r.status != 400 {
			t.Fatalf("PATCH multiplexer %v: %d %s", v, r.status, r.raw)
		}
	}

	// herdrMinBase empty, as shipped until the CLI release: every herdr
	// request is refused with needs "", and tmux goes through.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "mh", "class": "small", "multiplexer": "herdr"})
	if d := gateDetail(r); d == nil || d["needs"] != "" || r.body["error"].(map[string]any)["message"] != "herdr is not available yet." {
		t.Fatalf("POST herdr with no min base: %d %s", r.status, r.raw)
	}
	if l := e.do(t, tok, "GET", "/projects", nil); len(l.list) != 1 {
		t.Fatalf("a refused create made a project: %s", l.raw)
	}
	r = e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "herdr", "tz": "Europe/Paris"})
	if d := gateDetail(r); d == nil || d["needs"] != "" {
		t.Fatalf("PATCH herdr with no min base: %d %s", r.status, r.raw)
	}
	if g := e.do(t, tok, "GET", "/projects/"+ma, nil); g.body["multiplexer"] != "tmux" || g.body["tz"] == "Europe/Paris" {
		t.Fatalf("a refused PATCH changed the project: %s", g.raw)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "tmux"}); r.status != 200 || r.body["multiplexer"] != "tmux" {
		t.Fatalf("PATCH tmux: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "mt", "class": "small", "multiplexer": "tmux"})
	if r.status != 201 || r.body["multiplexer"] != "tmux" {
		t.Fatalf("POST tmux: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)

	// Two published bases; herdr arrives in the newer one.
	now := time.Now()
	if _, err := e.h.Pool.Exec(ctx, "insert into base_versions (version, nix_rev, released_at) values ('2026.10.01', 'a1', $1), ('2026.10.06', 'b2', $2)", now.Add(-48*time.Hour), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A min base that names no row still refuses with needs "".
	restore := httpapi.SetHerdrMinBase("2099.01.01")
	if d := gateDetail(e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "herdr"})); d == nil || d["needs"] != "" {
		t.Fatal("a min base with no row let herdr through")
	}
	restore()
	defer httpapi.SetHerdrMinBase("2026.10.06")()

	// A project on the older base: 409 naming both versions; tmux is
	// still never refused there.
	if _, err := e.h.Pool.Exec(ctx, "update projects set base_version = '2026.10.01' where id = $1", ma); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "herdr"})
	d := gateDetail(r)
	if d == nil || d["base_version"] != "2026.10.01" || d["needs"] != "2026.10.06" ||
		r.body["error"].(map[string]any)["message"] != "ma runs base 2026.10.01; herdr needs 2026.10.06 or newer." {
		t.Fatalf("PATCH herdr on an old base: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "tmux"}); r.status != 200 {
		t.Fatalf("PATCH tmux on an old base: %d %s", r.status, r.raw)
	}
	// A null base counts as older.
	if _, err := e.h.Pool.Exec(ctx, "update projects set base_version = null where id = $1", ma); err != nil {
		t.Fatal(err)
	}
	if d := gateDetail(e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "herdr"})); d == nil || d["base_version"] != nil || d["needs"] != "2026.10.06" {
		t.Fatal("PATCH herdr on a null base was not refused")
	}

	// On the base with herdr: the PATCH answers the new value and GET
	// shows it, while the running guest keeps what it started with.
	if _, err := e.h.Pool.Exec(ctx, "update projects set base_version = '2026.10.06' where id = $1", ma); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "PATCH", "/projects/"+ma, map[string]any{"multiplexer": "herdr"})
	if r.status != 200 || r.body["multiplexer"] != "herdr" {
		t.Fatalf("PATCH herdr: %d %s", r.status, r.raw)
	}
	if g := e.do(t, tok, "GET", "/projects/"+ma, nil); g.body["multiplexer"] != "herdr" {
		t.Fatalf("GET after PATCH: %s", g.raw)
	}
	if pj := e.lastProjectJSON(t, ma); pj["multiplexer"] != "tmux" {
		t.Fatalf("a running guest got the switch before its next start: %v", pj)
	}
	// The next start carries it.
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+ma+"/stop", nil)); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+ma+"/start", nil)); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	if pj := e.lastProjectJSON(t, ma); pj["multiplexer"] != "herdr" || pj["slug"] != "ma" {
		t.Fatalf("StartGuest project_json: %v", pj)
	}

	// A POST for herdr gates on the newest base, which has it.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "mh", "class": "small", "multiplexer": "herdr"})
	if r.status != 201 || r.body["multiplexer"] != "herdr" {
		t.Fatalf("POST herdr: %d %s", r.status, r.raw)
	}
	mh := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create mh: %+v", op.Error)
	}
	if pj := e.lastProjectJSON(t, mh); pj["multiplexer"] != "herdr" {
		t.Fatalf("CreateGuest project_json: %v", pj)
	}
	// "Older" compares released_at, not the version string: a min base
	// whose name sorts after the newest base but which was released
	// before it lets herdr through.
	if _, err := e.h.Pool.Exec(ctx, "insert into base_versions (version, nix_rev, released_at) values ('2026.10.07', 'c3', $1)", now.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	defer httpapi.SetHerdrMinBase("2026.10.07")()
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "mz", "class": "small", "multiplexer": "herdr"})
	if r.status != 201 {
		t.Fatalf("POST herdr on a base released after the min base: %d %s", r.status, r.raw)
	}
	e.waitOp(t, r)
	if !strings.Contains(e.logs.String(), `"event":"multiplexer_set"`) {
		t.Fatal("no multiplexer_set log line")
	}
}

// TestMultiplexerForkCopies: a fork and a restore as new copy the
// source's multiplexer, and fall back to tmux when the gate would refuse
// herdr for the copy's base, which is the source's (api.md "The base
// gate").
func TestMultiplexerForkCopies(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-muxf", "muxf")
	now := time.Now()
	if _, err := e.h.Pool.Exec(ctx, "insert into base_versions (version, nix_rev, released_at) values ('2026.10.06', 'b2', $1)", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	restore := httpapi.SetHerdrMinBase("2026.10.06")
	defer restore()
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "src", "class": "small", "multiplexer": "herdr"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set base_version = '2026.10.06' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots", nil)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("snapshot op: %+v", op.Error)
	}
	g := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+r.body["op_id"].(string), nil)
	sid := g.body["result"].(map[string]any)["snapshot_id"].(string)

	fork := func(name string) map[string]any {
		t.Helper()
		r := e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid, "count": 1, "name": name, "start": false})
		if r.status != 202 {
			t.Fatalf("fork %s: %d %s", name, r.status, r.raw)
		}
		np := r.body["projects"].([]any)[0].(map[string]any)
		return e.do(t, tok, "GET", "/projects/"+np["project_id"].(string), nil).body
	}
	restoreNew := func(name string) map[string]any {
		t.Helper()
		r := e.do(t, tok, "POST", "/projects/"+pid+"/snapshots/"+sid+"/restore", map[string]any{"as_new_project": name, "start": false})
		if r.status != 202 {
			t.Fatalf("restore as new %s: %d %s", name, r.status, r.raw)
		}
		return e.do(t, tok, "GET", "/projects/"+r.body["project_id"].(string), nil).body
	}
	if p := fork("fa"); p["multiplexer"] != "herdr" {
		t.Fatalf("fork of a herdr project: %v", p["multiplexer"])
	}
	if p := restoreNew("ra"); p["multiplexer"] != "herdr" || p["base_version"] != "2026.10.06" {
		t.Fatalf("restore as new of a herdr project: %v on %v", p["multiplexer"], p["base_version"])
	}
	// The source held on a base released before the min base: the copies
	// run the source's base, so the gate refuses herdr and they get tmux,
	// though the newest base would pass.
	if _, err := e.h.Pool.Exec(ctx, "insert into base_versions (version, nix_rev, released_at) values ('2026.10.01', 'b1', $1)", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set base_version = '2026.10.01' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if p := fork("fo"); p["multiplexer"] != "tmux" || p["base_version"] != "2026.10.01" {
		t.Fatalf("fork of a herdr source behind the min base: %v on %v", p["multiplexer"], p["base_version"])
	}
	if p := restoreNew("ro"); p["multiplexer"] != "tmux" || p["base_version"] != "2026.10.01" {
		t.Fatalf("restore as new of a herdr source behind the min base: %v on %v", p["multiplexer"], p["base_version"])
	}
	// The gate would refuse herdr now: the copy gets tmux, and the fork
	// still succeeds.
	defer httpapi.SetHerdrMinBase("")()
	if p := fork("fb"); p["multiplexer"] != "tmux" {
		t.Fatalf("fork under a refusing gate: %v", p["multiplexer"])
	}
}

// TestMultiplexerPatchRefusalWritesNothing: the guarded multiplexer
// update is the PATCH's first write, so when it matches no row (a DELETE
// accepted after the handler read the project) the 409 leaves the
// request's other fields unwritten. A trigger that skips the multiplexer
// update stands in for that DELETE.
func TestMultiplexerPatchRefusalWritesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-muxr", "muxr")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "mr", "class": "small"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	e.waitOp(t, r)
	if _, err := e.h.Pool.Exec(ctx, "update projects set multiplexer = 'herdr', tz = 'UTC', hold_base_updates = false where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"create function skip_mux() returns trigger language plpgsql as $$ begin return null; end $$",
		"create trigger skip_mux before update on projects for each row when (new.multiplexer is distinct from old.multiplexer) execute function skip_mux()",
	} {
		if _, err := e.h.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer e.h.Pool.Exec(ctx, "drop trigger skip_mux on projects; drop function skip_mux()")
	r = e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"multiplexer": "tmux", "tz": "Europe/Paris", "hold_base_updates": true})
	if r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("PATCH racing a destroy: %d %s", r.status, r.raw)
	}
	var tz string
	var hold bool
	if err := e.h.Pool.QueryRow(ctx, "select tz, hold_base_updates from projects where id = $1", pid).Scan(&tz, &hold); err != nil {
		t.Fatal(err)
	}
	if tz != "UTC" || hold {
		t.Fatalf("refused PATCH wrote tz=%q hold_base_updates=%v", tz, hold)
	}
}
