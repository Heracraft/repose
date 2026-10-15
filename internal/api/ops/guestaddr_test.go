package ops_test

import (
	"testing"
	"time"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
)

// TestHelloRecordsAGuestAddressTheApiLost is the teksafari-org outage of
// 2026-10-13 (DECISIONS I-720). A create whose last step failed after the
// guest had booted left `projects.guest_ip` NULL with a guest the gateway
// could have reached, and nothing ever filled it in: the row's only
// writer was the CreateResult phase. The gateway then told the user the
// machine was "on a host the control plane cannot reach", and a
// hand-written UPDATE cleared it.
//
// Here the row is emptied under a running guest the way that failure left
// it, and the host's next Hello must put the address back.
func TestHelloRecordsAGuestAddressTheApiLost(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("addr")
	p := h.CreateRunning(u, "izma")
	if p.GuestIP == nil || p.VsockCID == nil {
		t.Fatalf("the create left no address: ip=%v cid=%v", p.GuestIP, p.VsockCID)
	}
	wantIP, wantCID := p.GuestIP.String(), *p.VsockCID

	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = null, vsock_cid = null where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("the address back from Hello", func() bool { return h.Project(p.ID).GuestIP != nil })

	got := h.Project(p.ID)
	if got.GuestIP == nil || got.GuestIP.String() != wantIP {
		t.Fatalf("guest_ip %v, want %s", got.GuestIP, wantIP)
	}
	if got.VsockCID == nil || *got.VsockCID != wantCID {
		t.Fatalf("vsock_cid %v, want %d", got.VsockCID, wantCID)
	}
	if got.State != "running" {
		t.Fatalf("state %s, want running", got.State)
	}
}

// TestHelloCorrectsAnAddressThatDisagrees: the host allocates its guests'
// addresses inside the disjoint /22 it was handed at registration and
// keeps the allocation across a stop, so a project row that disagrees
// with Hello is a stale row and the host's word wins (I-720).
func TestHelloCorrectsAnAddressThatDisagrees(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("stale")
	p := h.CreateRunning(u, "izma")
	want := p.GuestIP.String()

	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = '10.64.3.254', vsock_cid = 9 where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("the stale address corrected", func() bool {
		q := h.Project(p.ID)
		return q.GuestIP != nil && q.GuestIP.String() == want
	})
	if cid := h.Project(p.ID).VsockCID; cid == nil || *cid == 9 {
		t.Fatalf("vsock_cid %v: the stale value survived", cid)
	}
}

// TestStartRecordsTheGuestAddress is the half of I-720 that closes the
// outage in seconds instead of days: prod host-01 sent exactly two
// Hellos in the 72 hours to 2026-10-13, so a reconcile that only runs on
// Hello would have left teksafari-org unroutable until the next hostd
// restart. StartResult now carries the started guest's address, and the
// start the user runs next writes it.
func TestStartRecordsTheGuestAddress(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("onstart")
	p := h.CreateRunning(u, "izma")
	wantIP, wantCID := p.GuestIP.String(), *p.VsockCID
	pid := p.ID

	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	// What the failed create left behind: a guest the host holds and an
	// api row with no address for it.
	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = null, vsock_cid = null where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)})); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}

	got := h.Project(pid)
	if got.GuestIP == nil || got.GuestIP.String() != wantIP {
		t.Fatalf("guest_ip %v after a start, want %s", got.GuestIP, wantIP)
	}
	if got.VsockCID == nil || *got.VsockCID != wantCID {
		t.Fatalf("vsock_cid %v after a start, want %d", got.VsockCID, wantCID)
	}
}

// TestTwoLiveProjectsCannotHoldOneGuestAddress: migration 0026's partial
// unique index (I-723). Guest addresses are unique across the fleet by
// construction, so two live rows holding one would relay two users'
// logins to one guest. The write fails and the row stays as it was; a
// destroyed row keeps its history and is outside the index.
func TestTwoLiveProjectsCannotHoldOneGuestAddress(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("dup")
	a := h.CreateRunning(u, "izma")
	b := h.NewProject(u, "nuru", "large")

	_, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = $2 where id = $1", b.ID, a.GuestIP.String())
	if err == nil {
		t.Fatal("two live projects took one guest address")
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set destroyed_at = now(), state = 'destroyed' where id = $1", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = $2 where id = $1", b.ID, a.GuestIP.String()); err != nil {
		t.Fatalf("a destroyed row may repeat a live address: %v", err)
	}
	if got := h.Project(a.ID); got.GuestIP == nil || got.GuestIP.String() != a.GuestIP.String() {
		t.Fatalf("the live row's address changed: %v", got.GuestIP)
	}
}

// TestHelloLeavesAProjectWithAnOpenOpAlone: the Hello reconcile does not
// touch a project whose op is still open — that op's result settles the
// address as it settles the state (I-720). Without this, a Hello arriving
// in the middle of a restore would write the address of the guest the
// restore is about to destroy.
func TestHelloLeavesAProjectWithAnOpenOpAlone(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("openop")
	p := h.CreateRunning(u, "izma")
	pid := p.ID

	// An op nothing will finish, and no address on the row.
	if _, err := h.Pool.Exec(h.Ctx,
		"insert into ops (id, project_id, kind, state, step, host_id, params) values ($1, $2, 'restore', 'running', 0, $3, '{}'::jsonb)",
		store.NewID(), pid, p.HostID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = null, vsock_cid = null where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	// The Hello has been handled once the host is connected again; give
	// the reconcile the same room the other tests' WaitFor gives it.
	h.WaitFor("the host connected again", func() bool { return h.HostMgr.Connected(h.HostID) })
	time.Sleep(300 * time.Millisecond)
	if got := h.Project(pid); got.GuestIP != nil {
		t.Fatalf("the reconcile wrote %v while an op was open", got.GuestIP)
	}
}

// TestHelloRecordsTheAddressOfAStoppedGuestButNotADestroyedProject:
// guest_ip is the host's allocation, not a liveness signal, so a stopped
// guest's address is recorded and the gateway still refuses the login on
// the project's state. A destroyed project is never written: the row
// keeps its history, and reviving its address would hand it to whatever
// the host gives the next guest (I-720).
func TestHelloRecordsTheAddressOfAStoppedGuestButNotADestroyedProject(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("stoppedaddr")
	p := h.CreateRunning(u, "izma")
	pid := p.ID
	want := p.GuestIP.String()
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_ip = null, vsock_cid = null where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("a stopped guest's address recorded", func() bool { return h.Project(pid).GuestIP != nil })
	got := h.Project(pid)
	if got.GuestIP.String() != want {
		t.Fatalf("guest_ip %v, want %s", got.GuestIP, want)
	}
	if got.State != "stopped" {
		t.Fatalf("state %s, want stopped: recording an address must not start anything", got.State)
	}

	// The same guest, with the project destroyed: nothing is written.
	if _, err := h.Pool.Exec(h.Ctx,
		"update projects set state = 'destroyed', destroyed_at = now(), guest_ip = null, vsock_cid = null where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("the host connected again", func() bool { return h.HostMgr.Connected(h.HostID) })
	time.Sleep(300 * time.Millisecond)
	if d := h.Project(pid); d.GuestIP != nil || d.State != "destroyed" {
		t.Fatalf("a destroyed project was written: ip=%v state=%s", d.GuestIP, d.State)
	}
}
