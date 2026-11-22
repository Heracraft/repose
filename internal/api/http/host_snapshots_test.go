package httpapi_test

import (
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/store"
)

// A destroyed machine's final snapshot on the account's own host lives on
// that host's disk; removing the host deletes it (DECISIONS I-1542). The
// api then lists it as gone with the host, never as restorable, refuses a
// restore of it naming the host, and writes no project for the refusal.
func TestRemovingAnOwnHostTakesItsSnapshots(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-own-snap", "ownsnap")
	uid := e.exempt(t, "sub-own-snap")
	r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	hostID, _ := r.body["id"].(string)
	pid := store.NewID()
	destroyed := time.Now().Add(-time.Hour)
	if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, destroyed_at)
		values ($1, $2, 'app', 'app', 'small', 'destroyed', 1, $3)`, pid, uid, destroyed); err != nil {
		t.Fatal(err)
	}
	sid := store.NewID()
	if _, err := e.h.Pool.Exec(e.h.Ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason, taken_at, expires_at)
		values ($1, $2, $3, 'local/app.zst', 10, 'stop', $4, $5)`, sid, pid, hostID, destroyed, time.Now().Add(29*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	list := e.do(t, tok, "GET", "/hosts", nil)
	if row := list.list[0].(map[string]any); row["snapshots"] != float64(1) {
		t.Fatalf("GET /hosts snapshots = %v, want 1", row["snapshots"])
	}
	if r := e.do(t, tok, "DELETE", "/hosts/"+hostID, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}

	d := e.do(t, tok, "GET", "/projects/destroyed", nil)
	if d.status != 200 || len(d.list) != 1 {
		t.Fatalf("destroyed: %d %s", d.status, d.raw)
	}
	row := d.list[0].(map[string]any)
	if row["gone_with_host"] != "mac-mini" || row["restorable_until"] != nil {
		t.Fatalf("destroyed row: gone_with_host %v, restorable_until %v", row["gone_with_host"], row["restorable_until"])
	}
	snaps := e.do(t, tok, "GET", "/snapshots", nil)
	if s := snaps.list[0].(map[string]any); s["gone_with_host"] != "mac-mini" {
		t.Fatalf("GET /snapshots: %v", s)
	}

	var before int
	_ = e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from projects where user_id = $1", uid).Scan(&before)
	for _, body := range []map[string]any{{"slug": "app", "name": "app-back"}, {"snapshot_id": sid.String(), "name": "app-back"}} {
		rr := e.do(t, tok, "POST", "/projects/restore", body)
		if rr.status != 409 || detailOf(t, rr)["reason"] != "snapshot_gone" || detailOf(t, rr)["host"] != "mac-mini" {
			t.Fatalf("restore %v: %d %s", body, rr.status, rr.raw)
		}
		if msg, _ := rr.body["error"].(map[string]any)["message"].(string); msg != "That snapshot was kept on your host mac-mini, which was removed, so it is gone; repose had no copy" {
			t.Fatalf("message %q", msg)
		}
	}
	rr := e.do(t, tok, "POST", "/projects/"+pid.String()+"/snapshots/"+sid.String()+"/restore", map[string]any{"as_new_project": "app-back"})
	if rr.status != 409 || detailOf(t, rr)["reason"] != "snapshot_gone" {
		t.Fatalf("restore by snapshot route: %d %s", rr.status, rr.raw)
	}
	var after int
	_ = e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from projects where user_id = $1", uid).Scan(&after)
	if after != before {
		t.Fatalf("a refused restore wrote %d projects", after-before)
	}

	// A host added again under the same name does not bring them back.
	if r := e.do(t, tok, "POST", "/hosts", map[string]any{"name": "mac-mini"}); r.status != 201 {
		t.Fatalf("re-add: %d %s", r.status, r.raw)
	}
	d = e.do(t, tok, "GET", "/projects/destroyed", nil)
	if row := d.list[0].(map[string]any); row["gone_with_host"] != "mac-mini" {
		t.Fatalf("after re-add: %v", row)
	}
}
