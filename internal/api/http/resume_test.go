package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/ops"
)

// While a project's server is not answering, `repose start` (and `repose
// ssh` and `repose code`, which start a stopped machine through the same
// route, I-641) says the machine starts again by itself when the server is
// back, never to restore a snapshot elsewhere, which would lose up to a
// day of work (DECISIONS I-645). A retired or lost host keeps the restore
// advice.
func TestStartUnreachableText(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-una", "una")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "small"})
	if r.status/100 != 2 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := uuid.MustParse(r.body["id"].(string))
	var createOp uuid.UUID
	if err := e.h.Pool.QueryRow(ctx, "select id from ops where project_id = $1 and kind = 'create'", pid).Scan(&createOp); err != nil {
		t.Fatal(err)
	}
	e.h.WaitOp(createOp)
	e.h.WaitIdle(pid)
	e.h.WaitOp(e.h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	// Stop the host's stream before forcing its state, or this test races
	// the thing it is testing. A connected fake host writes its own state
	// back: measured on 2026-11-03, `update hosts set state =
	// 'unreachable'` is reverted to `ready` 50 to 75 ms later by the
	// heartbeat, so the POST below is a footrace -- it wins on an idle
	// machine and loses on a loaded one, which is why this passed here and
	// failed CI on main at 1df94164 with `202 {"restart":false}`: the
	// refusal at internal/api/lifecycle/lifecycle.go:180 read `ready` and
	// let the start through. Disconnecting first removes the race instead
	// of widening the window, and DisconnectHost waits for HostMgr to
	// notice (DECISIONS I-1010).
	e.h.DisconnectHost()
	if _, err := e.h.Pool.Exec(ctx, "update hosts set state = 'unreachable'"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid.String()+"/start", nil)
	want := "todo-app's server is not answering; machines that were running on it start again when it is back"
	if r.status != 409 || errMessage(r) != want {
		t.Fatalf("unreachable: %d %s", r.status, r.raw)
	}
	if d, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); d["host_state"] != "unreachable" {
		t.Fatalf("detail %s", r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update hosts set state = 'retired'"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid.String()+"/start", nil)
	if r.status != 409 || !strings.Contains(errMessage(r), "restore its latest snapshot onto another host") {
		t.Fatalf("retired: %d %s", r.status, r.raw)
	}
}
