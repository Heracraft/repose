package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/store"
)

// I-592: `repose logs --kind console` reads what the machine printed in a
// boot that never reached Ready: hostd's console tail, one line per line,
// with the time of its op. Before, it gave one op-error sentence per
// failed op. The op's own read does not carry the console.
func TestConsoleLogsServeTheFailedBoot(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-kan", "kan")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "kanali", "class": "small"})
	if r.status/100 != 2 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	var createOp string
	if err := e.h.Pool.QueryRow(ctx, "select id::text from ops where project_id = $1 and kind = 'create'", pid).Scan(&createOp); err != nil {
		t.Fatal(err)
	}
	e.h.WaitOp(uuid.MustParse(createOp))
	e.h.WaitIdle(uuid.MustParse(pid))
	if r := e.do(t, tok, "GET", "/projects/"+pid+"/logs?kind=console", nil); r.status != 200 || strings.TrimSpace(string(r.raw)) != "" {
		t.Fatalf("console of a machine that booted cleanly: %d %s", r.status, r.raw)
	}
	p := e.h.Project(uuid.MustParse(pid))
	bad := "/nix/store/1kyz-nixos-system-bad"
	if _, err := e.h.Pool.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure) values ($1, $2, '{}', 'built', $3)", store.NewID(), pid, bad); err != nil {
		t.Fatal(err)
	}
	e.h.Fake.SetBadClosure(bad, "<<< NixOS Stage 1 >>>\nstage 2 init script (/mnt-root/nix/store/1kyz-nixos-system-bad/init) not found")
	e.h.Fake.SetGuestdDead(p.GuestID.String(), true)
	if _, err := e.h.Pool.Exec(ctx, "update projects set state = 'error' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/start", nil)
	if r.status != 202 || r.body["restart"] != true {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	opID := r.body["op_id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("restart: %s %+v", op.State, op.Error)
	}
	got := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+opID, nil)
	res, _ := got.body["result"].(map[string]any)
	if _, has := res["console"]; has {
		t.Fatalf("the op read carries the console: %s", got.raw)
	}
	if w, _ := res["warning"].(map[string]any); w["code"] != "boot_failed" {
		t.Fatalf("op result %s", got.raw)
	}
	r = e.do(t, tok, "GET", "/projects/"+pid+"/logs?kind=console", nil)
	lines := strings.Split(strings.TrimSpace(string(r.raw)), "\n")
	if r.status != 200 || len(lines) != 2 || !strings.Contains(lines[0], `"line":"<<< NixOS Stage 1`) && !strings.Contains(lines[0], "NixOS Stage 1") || !strings.Contains(lines[1], "stage 2 init script") || !strings.Contains(lines[1], `"kind":"console"`) {
		t.Fatalf("console log: %d %s", r.status, r.raw)
	}
	if pr := e.do(t, tok, "GET", "/projects/"+pid, nil); pr.body["state"] != "running" || !strings.HasPrefix(pr.body["last_error"].(string), "boot_failed: ") {
		t.Fatalf("project after the fallback: %s", pr.raw)
	}
}
