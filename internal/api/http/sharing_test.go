package httpapi_test

import (
	"fmt"
	"strings"
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
	want := "your host " + name + " is full. Stop a machine there"
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

// TestOwnedCreateTakesNoSizeFromTheClass: on a host the account owns whose
// hostd shares it, a class asked for changes nothing, the disk included
// (DECISIONS I-1654). review-cli 2026-11-26 03:04Z: `--size xl` said it
// was ignored and gave the machine an 80G disk.
func TestOwnedCreateTakesNoSizeFromTheClass(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-disk", "disk")
	uid := e.exempt(t, "sub-disk")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	shareHost(t, e)
	for _, class := range []string{"tiny", "xl"} {
		r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "disk-" + class, "class": class})
		if r.status != 201 {
			t.Fatalf("create %s: %d %s", class, r.status, r.raw)
		}
		if vb, _ := r.body["volume_bytes"].(float64); int64(vb) != scheduler.OwnedVolumeBytes || r.body["class"] != "large" {
			t.Fatalf("asked for %s on a shared owned host: class %v volume %v, want large and %d", class, r.body["class"], r.body["volume_bytes"], scheduler.OwnedVolumeBytes)
		}
	}
}

// TestDestroyedOwnedMachineHasNoSize: GET /projects/destroyed says
// `sized: false` for a project whose snapshot is on a host the account
// owns that shares, so `repose ls --destroyed` and Recently destroyed name
// no class for it (I-1654, review-dash 5); one on repose's servers stays
// sized.
func TestDestroyedOwnedMachineHasNoSize(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-gone", "gone")
	uid := e.exempt(t, "sub-gone")
	for _, owned := range []bool{true, false} {
		pid := store.NewID()
		slug := fmt.Sprintf("gone-%v", owned)
		if _, err := e.h.Pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, destroyed_at)
			values ($1, $2, $3, $3, 'large', 'destroyed', 1, now())`, pid, uid, slug); err != nil {
			t.Fatal(err)
		}
		if _, err := e.h.Pool.Exec(ctx, `insert into snapshots (id, project_id, host_id, blob_path, bytes, reason, taken_at, expires_at)
			values ($1, $2, $3, $4, 1, 'scheduled', now(), now() + interval '1 day')`, store.NewID(), pid, map[bool]any{true: e.h.HostID, false: nil}[owned], "p/"+slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.h.Pool.Exec(ctx, "update hosts set owner_account_id = $1 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	shareHost(t, e)
	r := e.do(t, tok, "GET", "/projects/destroyed", nil)
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	if strings.Count(string(r.raw), `"sized":false`) != 1 || strings.Count(string(r.raw), `"sized":true`) != 1 {
		t.Fatalf("destroyed list: %s, want one sized false (owned) and one sized true", r.raw)
	}
}

// TestOwnedHostSaysItsRoom: GET /hosts carries what a host of the
// account's own that shares has free and, on a Mac (an aarch64 host,
// I-1640), how many machines its cores run at once, so the New project
// form can say before Create that the host is full (I-1654,
// review-dash P1-2).
func TestOwnedHostSaysItsRoom(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-room", "room")
	uid := e.exempt(t, "sub-room")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set owner_account_id = $1, arch = 'aarch64', vcpus = 8 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	shareHost(t, e)
	setOwnFree(t, e, 3<<30)
	r := e.do(t, tok, "GET", "/hosts", nil)
	if r.status != 200 {
		t.Fatalf("hosts: %d %s", r.status, r.raw)
	}
	raw := string(r.raw)
	if !strings.Contains(raw, `"free_mem_bytes":3221225472`) || !strings.Contains(raw, `"running_limit":3`) {
		t.Fatalf("hosts: %s, want free_mem_bytes 3 GiB and running_limit 3", raw)
	}
}

// A Mac without the appliance (its hostd names `restart`, design 2) sizes
// each start to what the running machines leave and never refuses one for
// cores, so GET /hosts gives no running_limit and the New project form
// does not hold Create at D1's count (I-1692); free_mem_bytes stays.
func TestDesignTwoMacHasNoRunningLimit(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-room2", "room2")
	uid := e.exempt(t, "sub-room2")
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set owner_account_id = $1, arch = 'aarch64', vcpus = 10 where id = $2", uid, e.h.HostID); err != nil {
		t.Fatal(err)
	}
	shareHost(t, e)
	setOwnFree(t, e, 3<<30)
	if _, err := e.h.Pool.Exec(e.h.Ctx, "update hosts set features = array_append(features, 'restart') where id = $1", e.h.HostID); err != nil {
		t.Fatal(err)
	}
	raw := string(e.do(t, tok, "GET", "/hosts", nil).raw)
	if !strings.Contains(raw, `"free_mem_bytes":3221225472`) || strings.Contains(raw, `running_limit`) {
		t.Fatalf("hosts: %s, want free_mem_bytes and no running_limit", raw)
	}
}
