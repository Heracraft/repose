package httpapi_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
)

// setOwnFree makes the env's fake host report b free (0: its default,
// 200 GiB) and waits until the api has the figure from a heartbeat.
func setOwnFree(t *testing.T, e *env, b uint64) {
	t.Helper()
	e.h.Fake.SetFreeMem(b)
	want := int64(b)
	if b == 0 {
		want = 200 << 30
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got int64
		if err := e.h.Pool.QueryRow(e.h.Ctx, "select free_mem_bytes from hosts where id = $1", e.h.HostID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("free_mem_bytes %d, want %d after the fake's heartbeat", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shareHost marks the env's host as running a hostd that shares it, the
// Hello feature scheduler.SharedFeature (I-1626, I-1630). Hello alone
// writes features, and the fake sends it once, at connect.
func shareHost(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set features = array_append(features, $2) where id = $1", e.h.HostID, scheduler.SharedFeature); err != nil {
		t.Fatal(err)
	}
}

// TestCreateOnASharedOwnHost: an owned host whose hostd shares it takes
// machines of any class past its memory by class while it reports a
// gigabyte free, and refuses below that with the host's figure and no
// class (DECISIONS I-1630). The same host before its hostd shares is
// TestCreateOnAFullOwnHostNamesTheFigure: placed by class, as today's
// hostd admits.
func TestCreateOnASharedOwnHost(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-owner", "owner")
	uid := e.exempt(t, "sub-owner")
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	shareHost(t, e)
	var name string
	var mem int64
	if err := e.h.Pool.QueryRow(ctx, "select name, mem_bytes from hosts where id = $1", e.h.HostID).Scan(&name, &mem); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(mem>>34)+2; i++ {
		if _, err := e.h.Pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes)
			values ($1, $2, $3, $3, 'xl', 'running', $4, 1)`, store.NewID(), uid, fmt.Sprintf("big-%d", i), e.h.HostID); err != nil {
			t.Fatal(err)
		}
	}
	setOwnFree(t, e, 768<<20)
	want := "your host " + name + " has 0.8 GB of memory free, and a machine starts there when it has at least 1 GB free. Stopping a machine frees the memory it uses"
	for _, class := range []string{"large", "xl"} {
		r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-" + class, "class": class})
		if d := detailOf(t, r); r.status != 503 || d["reason"] != "no_own_host" || d["host"] != name {
			t.Fatalf("a %s on a full shared host: %d %s, want 503 no_own_host naming %s", class, r.status, r.raw, name)
		}
		if msg, _ := r.body["error"].(map[string]any)["message"].(string); msg != want {
			t.Fatalf("message %q, want %q", msg, want)
		}
	}
	setOwnFree(t, e, uint64(scheduler.OwnedStartFreeBytes))
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "todo-app", "class": "xl"})
	if r.status != 201 || r.body["host_id"] != e.h.HostID.String() || r.body["sized"] != false {
		t.Fatalf("an xl with 1 GiB reported free on a shared host: %d %s, want 201 unsized", r.status, r.raw)
	}
}

// TestOwnHostThatDoesNotShareKeepsItsSize: the pairing of this api with
// a hostd from before I-1626 on an owned host. The host reports 200 GiB
// free (the fake's figure) and no SharedFeature; a project there keeps
// `sized: true`, so the CLI and the dashboard show its class.
func TestOwnHostThatDoesNotShareKeepsItsSize(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-old", "old")
	uid := e.exempt(t, "sub-old")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "old-host", "class": "large"})
	if r.status != 201 || r.body["host_name"] == nil || r.body["sized"] != true {
		t.Fatalf("a machine on an owned host that does not share: %d %s, want 201 on it, sized", r.status, r.raw)
	}
}
