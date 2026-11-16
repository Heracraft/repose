package httpapi_test

import (
	"strings"
	"testing"
)

// `repose fork` was removed (I-1082). POST /projects/:id/fork stays for
// one release and answers 400 invalid with detail.reason fork_removed,
// in a message an old CLI prints after its own "Could not fork X:" and
// that its fork-snapshot fallback does not match.
func TestForkRouteRefuses(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-fern", "fern")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "izma", "class": "large"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	for _, body := range []map[string]any{
		nil,
		{"count": 3},
		{"snapshot_id": "00000000-0000-0000-0000-000000000000", "count": 1},
	} {
		r := e.do(t, tok, "POST", "/projects/"+pid+"/fork", body)
		if r.status != 400 || errCode(r) != "invalid" {
			t.Fatalf("fork %v: %d %s", body, r.status, r.raw)
		}
		detail, _ := r.body["error"].(map[string]any)["detail"].(map[string]any)
		if detail["reason"] != "fork_removed" {
			t.Fatalf("fork %v detail: %s", body, r.raw)
		}
		msg, _ := r.body["error"].(map[string]any)["message"].(string)
		if msg != "repose fork was removed; `repose run --worktree -p PROMPT` runs another agent on its own branch on the same machine" {
			t.Fatalf("message %q", msg)
		}
		// An old CLI prints it as a clause after a colon, so no full
		// stop; and it takes a fork snapshot when the message names
		// snapshot_id, which this must not.
		if strings.HasSuffix(msg, ".") || strings.Contains(msg, "snapshot_id") {
			t.Fatalf("an old CLI reads this wrong: %q", msg)
		}
	}
	// Nothing was created.
	if r := e.do(t, tok, "GET", "/projects", nil); r.status != 200 || len(r.list) != 1 {
		t.Fatalf("projects after the refusals: %d %s", r.status, r.raw)
	}
}
