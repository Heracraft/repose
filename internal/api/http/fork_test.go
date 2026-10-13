package httpapi_test

import (
	"encoding/base64"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/billing"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// TestFork is I-254: POST /projects/:id/fork restores one snapshot of a
// live project into N new projects, named <slug>-fork-<k>, without the
// source's remote, with its named secrets and not its sshd material; the
// account's project cap is checked for all N before anything is created;
// a resent request_id answers with the same projects.
func TestFork(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-fern", "fern")
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "izma", "class": "large", "remote_url": "github.com/fern/izma"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	// A snapshot now; its op's result names it.
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots", nil)
	if r.status != 202 {
		t.Fatalf("snapshot: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("snapshot op: %+v", op.Error)
	}
	g := e.do(t, tok, "GET", "/projects/"+pid+"/ops/"+r.body["op_id"].(string), nil)
	res, _ := g.body["result"].(map[string]any)
	sid, _ := res["snapshot_id"].(string)
	if sid == "" {
		t.Fatalf("snapshot op has no result.snapshot_id: %s", g.raw)
	}
	// Named secrets live in Postgres, not in the snapshot; the fork copies them.
	val := base64.StdEncoding.EncodeToString([]byte("postgres://fork-secret"))
	if r := e.do(t, tok, "PUT", "/projects/"+pid+"/secrets/DATABASE_URL", map[string]any{"value": val}); r.status != 200 {
		t.Fatalf("secret: %d %s", r.status, r.raw)
	}

	// Refusals before anything exists.
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid, "count": 0}); r.status != 400 {
		t.Fatalf("count 0: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": uuid.NewString(), "count": 1}); r.status != 404 {
		t.Fatalf("unknown snapshot: %d %s", r.status, r.raw)
	}
	// The account's project cap (I-569), running or stopped: an exempt
	// account whose users.project_limit is below the cap has the cap.
	// With 97 stopped projects beside izma, 3 forks would make 101, so the
	// fork is refused and nothing is created.
	e.subscribe(t, "sub-fern", "")
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt', project_limit = 3, xl_limit = 1 where logto_sub = 'sub-fern'"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < billing.ProjectCap-3; i++ {
		if _, err := e.h.Pool.Exec(ctx, "insert into projects (id, user_id, name, slug, class, state, volume_bytes) select gen_random_uuid(), id, $1, $1, 'small', 'stopped', 1073741824 from users where logto_sub = 'sub-fern'", fmt.Sprintf("filler-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	live := func() int {
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects p join users u on u.id = p.user_id where u.logto_sub = 'sub-fern' and p.destroyed_at is null").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid, "count": 3})
	if r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("over the limit: %d %s", r.status, r.raw)
	}
	if det, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); det["reason"] != "project_limit" || det["limit"] != float64(billing.ProjectCap) || det["projects"] != float64(billing.ProjectCap-2) || det["requested"] != float64(3) {
		t.Fatalf("limit detail: %s", r.raw)
	}
	if n := live(); n != billing.ProjectCap-2 {
		t.Fatalf("a refused fork created projects: %d live", n)
	}

	// A new project is counted at a new volume's first bytes until its
	// samples say more, and a fork at what its source holds (I-585).
	heldOf := func(id string) int64 {
		t.Helper()
		var b int64
		if err := e.h.Pool.QueryRow(ctx, "select disk_held_bytes from projects where id = $1", id).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if b := heldOf(pid); b != billing.NewProjectHeldBytes {
		t.Fatalf("a new project holds %d", b)
	}
	if _, err := e.h.Pool.Exec(ctx, "update projects set disk_held_bytes = 7::bigint<<30 where id = $1", pid); err != nil {
		t.Fatal(err)
	}

	// Two forks.
	req := uuid.NewString()
	body := map[string]any{"snapshot_id": sid, "count": 2, "request_id": req}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", body)
	if r.status != 202 || r.body["snapshot_id"] != sid || r.body["from_project_id"] != pid {
		t.Fatalf("fork: %d %s", r.status, r.raw)
	}
	forks, _ := r.body["projects"].([]any)
	if len(forks) != 2 {
		t.Fatalf("fork answer: %s", r.raw)
	}
	var ids []string
	for i, f := range forks {
		m := f.(map[string]any)
		want := []string{"izma-fork-1", "izma-fork-2"}[i]
		if m["slug"] != want || m["class"] != "large" {
			t.Fatalf("fork %d: %v", i, m)
		}
		ids = append(ids, m["project_id"].(string))
		if op := e.h.WaitOp(uuid.MustParse(m["op_id"].(string))); op.State != "done" {
			t.Fatalf("fork %d restore op: %+v", i, op.Error)
		}
		p := e.do(t, tok, "GET", "/projects/"+m["project_id"].(string), nil)
		if p.status != 200 || p.body["state"] != "running" || p.body["remote_url"] != nil {
			t.Fatalf("fork %d: %d %s", i, p.status, p.raw)
		}
		// The named secret came along; the sshd material did not.
		s := e.do(t, tok, "GET", "/projects/"+m["project_id"].(string)+"/secrets", nil)
		if len(s.list) != 1 || s.list[0].(map[string]any)["name"] != "DATABASE_URL" {
			t.Fatalf("fork %d secrets: %s", i, s.raw)
		}
		// It opens in the fork, sealed for the fork (I-433).
		vals, err := e.h.Secrets.DecryptForGuest(ctx, m["project_id"].(string))
		found := false
		for _, v := range vals {
			found = found || (v.Name == "DATABASE_URL" && string(v.Value) == "postgres://fork-secret")
		}
		if err != nil || !found {
			t.Fatalf("fork %d: the copied secret does not open there: %v", i, err)
		}
		var lower int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from secrets where project_id = $1 and name !~ '^[A-Z]'", m["project_id"]).Scan(&lower); err != nil {
			t.Fatal(err)
		}
		var srcLower int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from secrets where project_id = $1 and name !~ '^[A-Z]'", pid).Scan(&srcLower); err != nil {
			t.Fatal(err)
		}
		var same int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from secrets a join secrets b on a.name = b.name and a.ciphertext = b.ciphertext where a.project_id = $1 and b.project_id = $2 and a.name !~ '^[A-Z]'", pid, m["project_id"]).Scan(&same); err != nil {
			t.Fatal(err)
		}
		if srcLower == 0 || same != 0 {
			t.Fatalf("fork %d: the source has %d sshd rows, %d of them shared with the fork (%d sshd rows of its own)", i, srcLower, same, lower)
		}
	}
	for i, id := range ids {
		if b := heldOf(id); b != 7<<30 {
			t.Fatalf("fork %d holds %d, want the source's 7 GB", i, b)
		}
	}
	// The source keeps its remote and keeps running.
	if p := e.do(t, tok, "GET", "/projects/"+pid, nil); p.body["remote_url"] != "github.com/fern/izma" || p.body["state"] != "running" {
		t.Fatalf("source after the fork: %s", p.raw)
	}

	// The same request again answers with the same projects and creates none.
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", body)
	if r.status != 202 {
		t.Fatalf("resend: %d %s", r.status, r.raw)
	}
	again, _ := r.body["projects"].([]any)
	if len(again) != 2 || again[0].(map[string]any)["project_id"] != ids[0] || again[1].(map[string]any)["project_id"] != ids[1] {
		t.Fatalf("resend answered other projects: %s", r.raw)
	}
	if n := live(); n != billing.ProjectCap {
		t.Fatalf("resend created projects: %d live", n)
	}

	// At the limit now; a fork of a fork is refused the same way.
	if r := e.do(t, tok, "POST", "/projects/"+ids[0]+"/fork", map[string]any{"snapshot_id": sid, "count": 1}); r.status != 404 {
		t.Fatalf("another project's snapshot: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("fork at the limit: %d %s", r.status, r.raw)
	}

	// Numbering takes the lowest free k, and a long name is trimmed to
	// fit a slug's 40 characters. users.project_limit above the cap raises
	// it for this account.
	if _, err := e.h.Pool.Exec(ctx, "update users set project_limit = $1 where logto_sub = 'sub-fern'", billing.ProjectCap+10); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid, "count": 1, "name": "a-very-long-name-for-an-experiment-with-the-parser"})
	if r.status != 202 {
		t.Fatalf("named fork: %d %s", r.status, r.raw)
	}
	if m := r.body["projects"].([]any)[0].(map[string]any); m["slug"] != "a-very-long-name-for-an-experiment-wit-1" {
		t.Fatalf("long name: %v", m["slug"])
	}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid, "count": 1, "start": false})
	if r.status != 202 {
		t.Fatalf("third fork: %d %s", r.status, r.raw)
	}
	m := r.body["projects"].([]any)[0].(map[string]any)
	if m["slug"] != "izma-fork-3" {
		t.Fatalf("third fork name: %v", m["slug"])
	}
	if op := e.h.WaitOp(uuid.MustParse(m["op_id"].(string))); op.State != "done" {
		t.Fatalf("unstarted fork op: %+v", op.Error)
	}
	if p := e.do(t, tok, "GET", "/projects/"+m["project_id"].(string), nil); p.body["state"] != "stopped" {
		t.Fatalf("start:false fork: %s", p.raw)
	}

	// Someone else cannot fork it.
	other := e.signIn(t, "sub-gus", "gus")
	if r := e.do(t, other, "POST", "/projects/"+pid+"/fork", map[string]any{"snapshot_id": sid}); r.status != 404 {
		t.Fatalf("someone else's project: %d %s", r.status, r.raw)
	}
}

// TestForkClonesOnTheSourcesHost is I-701: a fork without snapshot_id
// copies the project as it is now, as thin clones on its own host: no
// snapshot row, each copy placed on the source's host, every copy's
// Restore naming the same fork point and all the copies, no host_moved
// notice for a new project; a host whose hostd cannot clone answers 409
// snapshot_needed and creates nothing.
func TestForkClonesOnTheSourcesHost(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-oak", "oak")
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt' where logto_sub = 'sub-oak'"); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "izma", "class": "large"})
	if r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create op: %+v", op.Error)
	}
	var srcHost, srcGuest string
	if err := e.h.Pool.QueryRow(ctx, "select host_id::text, guest_id::text from projects where id = $1", pid).Scan(&srcHost, &srcGuest); err != nil {
		t.Fatal(err)
	}
	e.h.WaitFor("the host's Hello names local_clone", func() bool {
		var ok bool
		_ = e.h.Pool.QueryRow(ctx, "select 'local_clone' = any(features) from hosts where id = $1", srcHost).Scan(&ok)
		return ok
	})

	req := uuid.NewString()
	body := map[string]any{"count": 2, "request_id": req}
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", body)
	if r.status != 202 || r.body["snapshot_id"] != nil || r.body["snapshot_created_at"] == nil || r.body["from_project_id"] != pid {
		t.Fatalf("fork: %d %s", r.status, r.raw)
	}
	forks, _ := r.body["projects"].([]any)
	if len(forks) != 2 {
		t.Fatalf("fork answer: %s", r.raw)
	}
	var ids []string
	for i, f := range forks {
		m := f.(map[string]any)
		ids = append(ids, m["project_id"].(string))
		if op := e.h.WaitOp(uuid.MustParse(m["op_id"].(string))); op.State != "done" {
			t.Fatalf("fork %d restore op: %+v", i, op.Error)
		}
		var host, state string
		if err := e.h.Pool.QueryRow(ctx, "select host_id::text, state from projects where id = $1", m["project_id"]).Scan(&host, &state); err != nil {
			t.Fatal(err)
		}
		if host != srcHost || state != "running" {
			t.Fatalf("fork %d is %s on %s, want running on the source's host %s", i, state, host, srcHost)
		}
		var moved int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from events where project_id = $1 and kind = 'host_moved'", m["project_id"]).Scan(&moved); err != nil {
			t.Fatal(err)
		}
		if moved != 0 {
			t.Fatalf("fork %d was told it moved host", i)
		}
	}
	var snaps int
	if err := e.h.Pool.QueryRow(ctx, "select count(*) from snapshots where project_id = $1", pid).Scan(&snaps); err != nil {
		t.Fatal(err)
	}
	if snaps != 0 {
		t.Fatalf("a cloned fork left %d snapshots", snaps)
	}
	// Both Restores clone the source at one point and name both copies.
	var restores []*hostdv1.Restore
	for _, c := range e.h.Fake.Commands() {
		if rs := c.GetRestore(); rs != nil {
			restores = append(restores, rs)
		}
	}
	if len(restores) != 2 {
		t.Fatalf("%d Restores sent, want 2", len(restores))
	}
	for _, rs := range restores {
		if rs.BlobPath != "" || rs.Clone == nil || rs.Clone.SourceGuestId != srcGuest || rs.Clone.PointId != restores[0].Clone.PointId ||
			len(rs.Clone.PointId) != 32 || !slices.Contains(rs.Clone.SiblingGuestIds, rs.GuestId) || len(rs.Clone.SiblingGuestIds) != 2 {
			t.Fatalf("restore %+v", rs)
		}
	}
	cloned := 0
	for _, g := range e.h.Fake.Guests() {
		if g.ClonedFrom == srcGuest {
			cloned++
		}
	}
	if cloned != 2 {
		t.Fatalf("the host holds %d clones of the source, want 2", cloned)
	}
	// The same request again answers with the same projects.
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", body)
	again, _ := r.body["projects"].([]any)
	if r.status != 202 || len(again) != 2 || again[0].(map[string]any)["project_id"] != ids[0] {
		t.Fatalf("resend: %d %s", r.status, r.raw)
	}

	// A host whose hostd cannot clone: 409 snapshot_needed, nothing made.
	if _, err := e.h.Pool.Exec(ctx, "update hosts set features = '{}' where id = $1", srcHost); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := e.h.Pool.QueryRow(ctx, "select count(*) from projects where destroyed_at is null").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	r = e.do(t, tok, "POST", "/projects/"+pid+"/fork", map[string]any{"count": 1})
	if r.status != 409 || errCode(r) != "conflict" {
		t.Fatalf("no local_clone: %d %s", r.status, r.raw)
	}
	if det, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); det["reason"] != "snapshot_needed" {
		t.Fatalf("detail: %s", r.raw)
	}
	if count() != before {
		t.Fatal("a fork refused for a snapshot created projects")
	}

	// The snapshot a fork takes then is reason fork and lasts a day (I-703).
	r = e.do(t, tok, "POST", "/projects/"+pid+"/snapshots", map[string]any{"reason": "fork"})
	if r.status != 202 {
		t.Fatalf("fork snapshot: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("fork snapshot op: %+v", op.Error)
	}
	var reason string
	var left float64
	if err := e.h.Pool.QueryRow(ctx, "select reason, extract(epoch from expires_at - taken_at) from snapshots where project_id = $1", pid).Scan(&reason, &left); err != nil {
		t.Fatal(err)
	}
	if reason != "fork" || left < 23*3600 || left > 25*3600 {
		t.Fatalf("fork snapshot reason %s, expires %.0f s after it was taken", reason, left)
	}
	var sent string
	for _, c := range e.h.Fake.Commands() {
		if sn := c.GetSnapshot(); sn != nil {
			sent = sn.Reason
		}
	}
	if sent != "manual" {
		t.Fatalf("hostd was sent reason %q, which a hostd before I-703 refuses", sent)
	}
}
