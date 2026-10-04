package ops_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db/testdb"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func TestMain(m *testing.M) { os.Exit(testdb.Run(m)) }

func TestLifecycle(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("alice")
	ctx := h.Ctx
	if err := h.Secrets.Put(ctx, u.ID.String(), "", "X", nil); err == nil {
		t.Fatal("put without a project should fail")
	}
	p := h.NewProject(u, "todo", "large")
	pid := p.ID
	if err := h.Secrets.Put(ctx, u.ID.String(), pid.String(), "DATABASE_URL", []byte("postgres://planted")); err != nil {
		t.Fatal(err)
	}
	// create: build then create_guest
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	if op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	p = h.Project(pid)
	if p.State != "running" || p.GuestIP == nil || p.HostID == nil || p.GuestID == nil || p.StartedAt == nil {
		t.Fatalf("after create: %+v", p)
	}
	rev, _ := store.GetRevision(ctx, h.Pool, *p.ConfigRevisionID)
	if rev.Status != "applied" || rev.SystemClosure == nil {
		t.Fatalf("revision after create: %+v", rev)
	}
	// The CreateGuest command carried the secrets and the sshd material.
	var create *hostdv1.CreateGuest
	for _, c := range h.Fake.Commands() {
		if cg := c.GetCreateGuest(); cg != nil {
			create = cg
		}
	}
	if create == nil {
		t.Fatal("no CreateGuest sent")
	}
	if len(create.HostKey) == 0 || len(create.HostCert) == 0 || create.SshCaPub == "" || create.Principals[0] != pid.String() || create.UserId != u.ID.String() || create.ProjectSlug != "todo" {
		t.Fatalf("CreateGuest fields: key=%d cert=%d ca=%q principals=%v", len(create.HostKey), len(create.HostCert), create.SshCaPub, create.Principals)
	}
	if len(create.Secrets) != 1 || create.Secrets[0].Name != "DATABASE_URL" || string(create.Secrets[0].Value) != "postgres://planted" {
		t.Fatalf("secrets on the wire: %+v", create.Secrets)
	}
	if create.Env["TZ"] != "UTC" || create.Env["LANG"] != "C.UTF-8" {
		t.Fatalf("env %v", create.Env)
	}
	if len(create.ProjectJson) == 0 {
		t.Fatal("project_json empty")
	}
	// The host certificate now carries the guest ip principal.
	vals, _ := h.Secrets.DecryptForGuest(ctx, pid.String())
	var certLine string
	for _, v := range vals {
		if v.Name == "ssh_host_ed25519_key-cert.pub" {
			certLine = string(v.Value)
		}
	}
	if certLine == "" {
		t.Fatal("host cert not stored")
	}
	// update_secrets pushes the whole set.
	if err := h.Secrets.Put(ctx, u.ID.String(), pid.String(), "TOKEN", []byte("t")); err != nil {
		t.Fatal(err)
	}
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()}))
	if op.State != "done" {
		t.Fatalf("update_secrets: %+v", op.Error)
	}
	for _, g := range h.Fake.Guests() {
		if string(g.Secrets["TOKEN"]) != "t" || string(g.Secrets["DATABASE_URL"]) != "postgres://planted" {
			t.Fatalf("guest secrets %v", g.Secrets)
		}
		if _, ok := g.Secrets["ssh_host_ed25519_key"]; ok {
			t.Fatal("reserved name pushed through UpdateSecrets")
		}
	}
	// stop with snapshot
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": true}, Phases: ops.PlanStop()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("stop: %+v %+v", op.Error, op)
	}
	if p = h.Project(pid); p.State != "stopped" || p.StoppedAt == nil {
		t.Fatalf("after stop: %s", p.State)
	}
	snaps, _ := store.ListSnapshots(ctx, h.Pool, pid)
	if len(snaps) != 1 || snaps[0].Reason != "stop" || snaps[0].Bytes == 0 {
		t.Fatalf("snapshots after stop: %+v", snaps)
	}
	// start, after a class change made while stopped (PATCH, I-260)
	if _, err := h.Pool.Exec(ctx, "update projects set class = 'small' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}))
	if op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	if p = h.Project(pid); p.State != "running" {
		t.Fatalf("after start: %s", p.State)
	}
	var start *hostdv1.StartGuest
	for _, c := range h.Fake.Commands() {
		if sg := c.GetStartGuest(); sg != nil {
			start = sg
		}
	}
	if start == nil || len(start.HostKey) == 0 || len(start.Secrets) != 2 || start.SshCaPub == "" || start.Class != "small" {
		t.Fatalf("StartGuest delivery fields: %+v", start)
	}
	// manual snapshot
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Params: map[string]any{"reason": "manual"}, Phases: ops.PlanSnapshot()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("snapshot: %+v", op.Error)
	}
	snaps, _ = store.ListSnapshots(ctx, h.Pool, pid)
	if len(snaps) != 2 || snaps[0].Reason != "manual" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	// resize
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindResize, ProjectID: &pid, Params: map[string]any{"volume_bytes": float64(80 << 30)}, Phases: ops.PlanResize()}))
	if op.State != "done" {
		t.Fatalf("resize: %+v", op.Error)
	}
	if p = h.Project(pid); p.VolumeBytes != 80<<30 {
		t.Fatalf("volume after resize %d", p.VolumeBytes)
	}
	// exec requires an audit id
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindExec, ProjectID: &pid, Params: map[string]any{"argv": []any{"uptime"}}, Phases: ops.PlanExec()}))
	if op.State != "error" || op.Error["code"] != "invalid" {
		t.Fatalf("exec without audit: %+v", op)
	}
	aid, _ := store.Audit(ctx, h.Pool, "admin", "exec", pid.String(), map[string]any{"argv": []string{"uptime"}})
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindExec, ProjectID: &pid, AuditID: &aid, Params: map[string]any{"argv": []any{"uptime"}}, Phases: ops.PlanExec()}))
	if op.State != "done" {
		t.Fatalf("exec: %+v", op.Error)
	}
	out, _ := base64.StdEncoding.DecodeString(op.Result["stdout"].(string))
	if string(out) != "fake\n" {
		t.Fatalf("exec stdout %q", out)
	}
	// destroy: stop with snapshot, destroy guest, keep the snapshot 30 days
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(h.Project(pid))}))
	if op.State != "done" {
		t.Fatalf("destroy: %+v", op.Error)
	}
	p = h.Project(pid)
	if p.State != "destroyed" || p.DestroyedAt == nil || p.HostID != nil || p.GuestID != nil {
		t.Fatalf("after destroy: %+v", p)
	}
	snaps, _ = store.ListSnapshots(ctx, h.Pool, pid)
	if len(snaps) != 3 {
		t.Fatalf("snapshots after destroy: %d", len(snaps))
	}
	for _, s := range snaps {
		if s.ExpiresAt == nil || time.Until(*s.ExpiresAt) < 29*24*time.Hour {
			t.Fatalf("snapshot %s expires %v", s.ID, s.ExpiresAt)
		}
	}
	if len(h.Fake.Guests()) != 0 {
		t.Fatal("fake still holds the guest")
	}
	var reserved int64
	_ = h.Pool.QueryRow(ctx, "select coalesce(sum(reserved_bytes),0) from host_reservations").Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("reservation not released: %d", reserved)
	}
}

func TestCapacityError(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set mem_bytes = 8::bigint<<30, free_mem_bytes = 8::bigint<<30"); err != nil {
		t.Fatal(err)
	}
	u := h.NewUser("bob")
	p := h.NewProject(u, "big", "xl")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	if op.State != "error" || op.Error["code"] != "capacity" {
		t.Fatalf("expected capacity error, got %+v", op)
	}
	p = h.Project(pid)
	if p.State != "error" || p.HostID != nil {
		t.Fatalf("after capacity error: %+v", p)
	}
}

func TestBuildFailureAndRebootRequired(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("carol")
	p := h.CreateRunning(u, "app")
	pid := p.ID
	// A failing build on a running project leaves it running and marks the revision failed.
	h.Fake.SetFail("Build", "eval_failed")
	rid := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, 'bad', 'building')", rid, pid); err != nil {
		t.Fatal(err)
	}
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Phases: ops.PlanBuild(true)}))
	if op.State != "error" || op.Error["code"] != "eval_failed" {
		t.Fatalf("build fail: %+v", op)
	}
	rev, _ := store.GetRevision(h.Ctx, h.Pool, rid)
	if rev.Status != "failed" || rev.Error == nil {
		t.Fatalf("revision: %+v", rev)
	}
	if h.Project(pid).State != "running" {
		t.Fatal("a failed build changed the project state")
	}
	h.Fake.SetFail("Build", "")
	// A kernel-changing build applies nothing until confirmed with reboot.
	h.Fake.SetKernelChanged(true)
	rid2 := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, 'kernel', 'building')", rid2, pid); err != nil {
		t.Fatal(err)
	}
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid2, Phases: ops.PlanBuild(true)}))
	if op.State != "done" || !op.RebootRequired {
		t.Fatalf("kernel build: state=%s reboot_required=%v err=%+v", op.State, op.RebootRequired, op.Error)
	}
	rev, _ = store.GetRevision(h.Ctx, h.Pool, rid2)
	if rev.Status != "built" || !rev.RebootRequired || !rev.KernelChanged {
		t.Fatalf("revision after kernel build: %+v", rev)
	}
	if *h.Project(pid).ConfigRevisionID == rid2 {
		t.Fatal("kernel revision applied without confirmation")
	}
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindApply, ProjectID: &pid, RevisionID: &rid2, Params: map[string]any{"reboot": true}, Phases: ops.PlanApply()}))
	if op.State != "done" || op.RebootRequired {
		t.Fatalf("apply with reboot: %+v", op)
	}
	rev, _ = store.GetRevision(h.Ctx, h.Pool, rid2)
	if rev.Status != "applied" || *h.Project(pid).ConfigRevisionID != rid2 {
		t.Fatalf("after confirmed apply: %+v", rev)
	}
}

func waitSent(h *apitest.Harness, opID uuid.UUID) {
	h.WaitFor("command sent", func() bool {
		op, err := store.GetOp(h.Ctx, h.Pool, opID)
		return err == nil && op.State == "running" && op.CommandID != nil
	})
}

// Kill the api mid-build, restart it, the build completes.
func TestOpSurvivesApiRestart(t *testing.T) {
	h := apitest.New(t, apitest.Options{BuildDelay: 1500 * time.Millisecond})
	u := h.NewUser("dave")
	p := h.NewProject(u, "restart", "large")
	pid := p.ID
	opID := h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})
	waitSent(h, opID)
	h.StopEngine()
	time.Sleep(200 * time.Millisecond)
	h.StartEngine(ops.Config{BaseRef: "deadbeef"})
	op := h.WaitOp(opID)
	if op.State != "done" {
		t.Fatalf("after restart: %+v", op.Error)
	}
	if h.Project(pid).State != "running" {
		t.Fatal("project not running after the restarted op")
	}
}

// Drop the host stream mid-build; the command is re-sent with the same
// command_id after Hello and the build completes.
func TestOpSurvivesStreamDrop(t *testing.T) {
	h := apitest.New(t, apitest.Options{BuildDelay: 1500 * time.Millisecond})
	u := h.NewUser("erin")
	p := h.NewProject(u, "drop", "large")
	pid := p.ID
	opID := h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})
	waitSent(h, opID)
	op0, _ := store.GetOp(h.Ctx, h.Pool, opID)
	h.ReconnectHost(t)
	op := h.WaitOp(opID)
	if op.State != "done" {
		t.Fatalf("after stream drop: %+v", op.Error)
	}
	builds := 0
	for _, c := range h.Fake.Commands() {
		if c.GetBuild() != nil && c.CommandId == op0.CommandID.String() {
			builds++
		}
	}
	if builds < 2 {
		t.Fatalf("expected the build re-sent with the same command_id, saw %d", builds)
	}
	lines, err := h.Logs.Read(h.Ctx, opID, 0, 0)
	if err != nil || len(lines) < 3 {
		t.Fatalf("build log lines %d %v", len(lines), err)
	}
}

func TestRestoreOntoHostAndSecretValueInFragmentRefused(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("frank")
	p := h.CreateRunning(u, "res")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("stop: %+v", op.Error)
	}
	oldGuest := *h.Project(pid).GuestID
	sid := *op.SnapshotID
	p = h.Project(pid)
	rop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(p, true, true)}))
	if rop.State != "done" {
		t.Fatalf("restore: %+v", rop.Error)
	}
	p = h.Project(pid)
	if p.State != "running" || *p.GuestID == oldGuest || p.GuestIP == nil {
		t.Fatalf("after restore: %+v", p)
	}
	snap, _ := store.GetSnapshot(h.Ctx, h.Pool, sid)
	if snap.RestoringOpID != nil {
		t.Fatal("restore guard not released")
	}
	var restore *hostdv1.Restore
	for _, c := range h.Fake.Commands() {
		if r := c.GetRestore(); r != nil {
			restore = r
		}
	}
	if restore == nil || restore.BlobPath != snap.BlobPath || restore.SystemClosure == "" || len(restore.HostKey) == 0 {
		t.Fatalf("Restore command: %+v", restore)
	}
	// I-462: the stop's digest is in the row, and the Restore carries it
	// for hostd to check the blob against.
	if snap.Sha256 == nil || len(*snap.Sha256) != 64 || restore.Sha256 != *snap.Sha256 {
		t.Fatalf("snapshot digest %v, Restore's %q", snap.Sha256, restore.Sha256)
	}
	// A digest the store's blob does not match fails the restore; a row
	// from before I-462 has none and restores unchecked.
	if _, err := h.Pool.Exec(h.Ctx, "update snapshots set sha256 = $2 where id = $1", sid, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if rop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: []string{ops.PhaseRestore}})); rop.State != "error" || rop.Error == nil || !strings.Contains(fmt.Sprint(rop.Error), "checksum") {
		t.Fatalf("restore against a wrong digest: %s %v", rop.State, rop.Error)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update snapshots set sha256 = null where id = $1", sid); err != nil {
		t.Fatal(err)
	}
	if rop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(h.Project(pid), true, false)})); rop.State != "done" {
		t.Fatalf("restore of a snapshot without a digest: %+v", rop.Error)
	}
	// A fragment containing a current secret value is refused before Build.
	if err := h.Secrets.Put(h.Ctx, u.ID.String(), pid.String(), "API_KEY", []byte("sk-verysecret")); err != nil {
		t.Fatal(err)
	}
	rid := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status) values ($1, $2, $3, 'building')", rid, pid, `{ home.sessionVariables.K = "sk-verysecret"; }`); err != nil {
		t.Fatal(err)
	}
	before := len(h.Fake.Commands())
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Phases: ops.PlanBuild(true)}))
	if op.State != "error" || op.Error["code"] != "invalid" {
		t.Fatalf("fragment with secret: %+v", op)
	}
	if len(h.Fake.Commands()) != before {
		t.Fatal("a Build was sent despite the secret in the fragment")
	}
}

func TestEnqueueRefusesConcurrentOps(t *testing.T) {
	h := apitest.New(t, apitest.Options{BuildDelay: 2 * time.Second})
	u := h.NewUser("gina")
	p := h.NewProject(u, "busy", "large")
	pid := p.ID
	opID := h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})
	if _, err := h.Engine.Enqueue(context.Background(), h.Pool, ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}, false); err != ops.ErrOpInProgress {
		t.Fatalf("expected op in progress, got %v", err)
	}
	h.WaitOp(opID)
}

// TestSnapshotFailureNotifies is 13-notifications.md §9's "Platform events
// ... flow through the same pipeline" for snapshot_failed: a failed
// Snapshot command must reach the events table and its outbox, not just
// projects.last_error.
func TestSnapshotFailureNotifies(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ivy")
	p := h.CreateRunning(u, "snap")
	pid := p.ID
	h.Fake.SetFail("Snapshot", "internal")
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindSnapshot, ProjectID: &pid, Phases: ops.PlanSnapshot()}))
	if op.State != "error" {
		t.Fatalf("snapshot: %+v", op)
	}
	if h.Project(pid).LastError == nil {
		t.Fatal("last_error not set")
	}
	evs, err := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Event
	for i := range evs {
		if evs[i].Kind == "snapshot_failed" {
			found = &evs[i]
		}
	}
	if found == nil {
		t.Fatalf("no snapshot_failed event among %+v", evs)
	}
	var channels int
	if err := h.Pool.QueryRow(h.Ctx, "select count(*) from events_outbox where event_id = $1", found.ID).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if channels == 0 {
		t.Fatal("snapshot_failed was not queued for delivery (user has an email and notify_email defaults true)")
	}
}

// TestRestoreEmitsHostMovedEvent is the host_moved half of the same
// checklist item: a restore over the same host's volume is not a move
// (I-142: on host-01 every restore had said "restored onto a new host"),
// a restore that comes up on another host is.
func TestRestoreEmitsHostMovedEvent(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("jack")
	p := h.CreateRunning(u, "mov")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("stop: %+v", op.Error)
	}
	sid := *op.SnapshotID
	hostMoved := func() int {
		evs, err := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range evs {
			if e.Kind == "host_moved" {
				n++
			}
		}
		return n
	}
	// Same host: the project's host is ready, the restore lands on it.
	p = h.Project(pid)
	rop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(p, true, false)}))
	if rop.State != "done" {
		t.Fatalf("restore: %+v", rop.Error)
	}
	if moved, _ := rop.Params["host_moved"].(bool); moved || hostMoved() != 0 {
		t.Fatalf("a restore over the same host raised host_moved (params %v)", rop.Params)
	}
	// Another host: the project's host is gone (its row unknown, the guest
	// with it), the scheduler places the restore on the one that is left.
	lost := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into hosts (id, name, state, draining) values ($1, 'host-lost', 'unreachable', true)", lost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set host_id = $2, guest_id = null where id = $1", pid, lost); err != nil {
		t.Fatal(err)
	}
	p = h.Project(pid)
	rop = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(p, true, true)}))
	if rop.State != "done" {
		t.Fatalf("restore onto another host: %+v", rop.Error)
	}
	if moved, _ := rop.Params["host_moved"].(bool); !moved || hostMoved() != 1 {
		t.Fatalf("a restore onto another host must raise host_moved once (params %v)", rop.Params)
	}
	if np := h.Project(pid); np.HostID == nil || *np.HostID == lost {
		t.Fatal("project still on the lost host")
	}
}

func TestDrainAndHelloReconcile(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("hank")
	p := h.CreateRunning(u, "rec")
	hid := h.HostID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDrain, HostID: &hid, Phases: ops.PlanDrain()}))
	if op.State != "done" {
		t.Fatalf("drain: %+v", op.Error)
	}
	h.WaitFor("host draining", func() bool {
		hr, _ := store.GetHost(h.Ctx, h.Pool, hid)
		return hr != nil && hr.State == "draining" && hr.Draining
	})
	// The host's view wins over a stale api state when no op is open.
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'stopped' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("state reconciled from Hello", func() bool { return h.Project(p.ID).State == "running" })
	_ = fakehostd.Options{}
}

// I-461: a restore that fails after its destroy_guest phase, before the
// Restore is sent (here: the snapshot was deleted), leaves the project
// with no guest, no address and no vsock cid, and UnfinishedRestore says
// so until a restore succeeds.
func TestRestoreFailingAfterDestroyReleasesTheGuest(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("rory")
	p := h.CreateRunning(u, "res")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	if op.State != "done" || op.SnapshotID == nil {
		t.Fatalf("stop: %+v", op.Error)
	}
	sid := *op.SnapshotID
	if _, err := h.Pool.Exec(h.Ctx, "update snapshots set deleted_at = now() where id = $1", sid); err != nil {
		t.Fatal(err)
	}
	p = h.Project(pid)
	rop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(p, true, true)}))
	if rop.State != "error" {
		t.Fatalf("restore of a deleted snapshot: %s", rop.State)
	}
	p = h.Project(pid)
	if p.State != "error" || p.GuestID != nil || p.GuestIP != nil || p.VsockCID != nil {
		t.Fatalf("after the failed restore: state %s guest %v ip %v cid %v", p.State, p.GuestID, p.GuestIP, p.VsockCID)
	}
	if un, err := ops.UnfinishedRestore(h.Ctx, h.Pool, pid); err != nil || !un {
		t.Fatalf("UnfinishedRestore after a failed restore: %v %v", un, err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update snapshots set deleted_at = null where id = $1", sid); err != nil {
		t.Fatal(err)
	}
	rop = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindRestore, ProjectID: &pid, SnapshotID: &sid, Phases: ops.PlanRestore(p, true, true)}))
	if rop.State != "done" {
		t.Fatalf("restore again: %+v", rop.Error)
	}
	if un, err := ops.UnfinishedRestore(h.Ctx, h.Pool, pid); err != nil || un {
		t.Fatalf("UnfinishedRestore after a restore that succeeded: %v %v", un, err)
	}
	if p = h.Project(pid); p.State != "running" || p.GuestIP == nil {
		t.Fatalf("after the restore again: %+v", p)
	}
}

// I-461: a project in error whose guest the host does not report (left
// by an in-place restore that failed before the fix) lets go of its
// address on Hello; a project whose guest the host has keeps it.
func TestHelloReleasesAddressOfAMissingGuest(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("hedy")
	kept := h.CreateRunning(u, "kept")
	stale := h.CreateRunning(u, "stale")
	keptIP := h.Project(kept.ID).GuestIP
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'error', guest_id = $2 where id = $1", stale.ID, store.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'error' where id = $1", kept.ID); err != nil {
		t.Fatal(err)
	}
	h.ReconnectHost(t)
	h.WaitFor("missing guest's address released", func() bool {
		p := h.Project(stale.ID)
		return p.GuestIP == nil && p.VsockCID == nil
	})
	if p := h.Project(stale.ID); p.GuestID == nil || p.State != "error" {
		t.Fatalf("the stale project lost its guest id or state: %+v", p)
	}
	if p := h.Project(kept.ID); p.GuestIP == nil || *p.GuestIP != *keptIP {
		t.Fatalf("a project whose guest the host has lost its address: %+v", p)
	}
}

// A project whose create failed before CreateGuest has no guest, so its
// destroy plan is empty; the op must still end with the project destroyed
// (I-124: the stale smoke row on host-01, and a user's DELETE of such a
// project, stayed in `error` for ever).
func TestDestroyWithoutAGuestMarksTheProjectDestroyed(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("dora")
	p := h.NewProject(u, "dead", "small")
	pid := p.ID
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'error', host_id = (select id from hosts limit 1) where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	p = h.Project(pid)
	if phases := ops.PlanDestroy(p); len(phases) != 0 {
		t.Fatalf("plan for a guestless project = %v, want empty", phases)
	}
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}))
	if op.State != "done" {
		t.Fatalf("destroy op: %+v", op)
	}
	var state string
	var destroyedAt *time.Time
	if err := h.Pool.QueryRow(h.Ctx, "select state, destroyed_at from projects where id = $1", pid).Scan(&state, &destroyedAt); err != nil {
		t.Fatal(err)
	}
	if state != "destroyed" || destroyedAt == nil {
		t.Fatalf("after an empty-plan destroy: state %q destroyed_at %v", state, destroyedAt)
	}
}

// A start applies only a built revision newer than the one the guest runs
// (I-147): m3-held's start on 2026-09-21 re-applied a superseded `built`
// row from an older base over the applied newer one.
func TestStartAppliesOnlyANewerBuiltRevision(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("nia")
	p := h.CreateRunning(u, "st")
	pid := p.ID
	cur := h.Project(pid)
	if cur.ConfigRevisionID == nil {
		t.Fatal("no current revision after create")
	}
	// An older built row (a bump that was never applied), created before
	// the current revision.
	old := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure, created_at) values ($1, $2, '{}', 'built', '/nix/store/old-system', now() - interval '1 hour')", old, pid); err != nil {
		t.Fatal(err)
	}
	if pending, err := ops.PendingRevision(h.Ctx, h.Pool, cur); err != nil || pending {
		t.Fatalf("a superseded built row must not be pending (pending=%v err=%v)", pending, err)
	}
	h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	sop := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}))
	if sop.State != "done" {
		t.Fatalf("start: %+v", sop.Error)
	}
	for _, c := range h.Fake.Commands() {
		if a := c.GetApplyConfig(); a != nil && a.SystemClosure == "/nix/store/old-system" {
			t.Fatal("the start applied the superseded revision")
		}
	}
	// A newer built row is pending and gets applied.
	newer := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure) values ($1, $2, '{}', 'built', '/nix/store/new-system')", newer, pid); err != nil {
		t.Fatal(err)
	}
	if pending, err := ops.PendingRevision(h.Ctx, h.Pool, h.Project(pid)); err != nil || !pending {
		t.Fatalf("a newer built row must be pending (pending=%v err=%v)", pending, err)
	}
	h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()}))
	sop = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}))
	if sop.State != "done" || sop.RevisionID == nil || *sop.RevisionID != newer {
		t.Fatalf("start with a newer built row: state=%s revision=%v err=%+v", sop.State, sop.RevisionID, sop.Error)
	}
}

// An op enqueued by an engine that does not drive (the api, while api-grpc
// holds the ops lock) starts at once through NOTIFY, not at the driver's
// next tick (I-163). The driver's tick is an hour here, so only the
// notification can have woken it.
func TestEnqueueInAnotherProcessWakesTheDriver(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	h.StartEngine(ops.Config{BaseRef: "dev", TickInterval: time.Hour})
	time.Sleep(200 * time.Millisecond) // the driver takes the lock and listens
	other := ops.New(h.Pool, h.HostMgr, h.CA, h.Secrets, h.Logs, h.Events, h.Metrics, h.Log, ops.Config{BaseRef: "dev"})
	u := h.NewUser("ned")
	p := h.NewProject(u, "woken", "small")
	pid := p.ID
	id, err := other.Enqueue(h.Ctx, h.Pool, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}, false)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	other.Kick()
	deadline := time.Now().Add(10 * time.Second)
	for {
		op, err := store.GetOp(h.Ctx, h.Pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == "done" {
			t.Logf("create done %s after the other process's kick", time.Since(start).Round(time.Millisecond))
			return
		}
		if op.State == "error" {
			t.Fatalf("create: %+v", op.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("op still %s 10 s after a kick from another process; the driver was not woken", op.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A create whose fragment and base version match a closure already applied
// on the same host sends no Build (I-160): the closure depends on nothing
// else, and the other guest's GC root keeps it on the host.
func TestCreateReusesAClosureOnTheSameHost(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	if _, err := h.Pool.Exec(h.Ctx, "insert into base_versions (version, nix_rev) values ('2026.09.23', 'abc123')"); err != nil {
		t.Fatal(err)
	}
	u := h.NewUser("rey")
	a := h.CreateRunning(u, "first")
	aRev, err := store.GetRevision(h.Ctx, h.Pool, *h.Project(a.ID).ConfigRevisionID)
	if err != nil || aRev.SystemClosure == nil {
		t.Fatalf("first revision: %+v %v", aRev, err)
	}
	builds := func(pid uuid.UUID) int {
		n := 0
		for _, c := range h.Fake.Commands() {
			if b := c.GetBuild(); b != nil && b.ProjectId == pid.String() {
				n++
			}
		}
		return n
	}

	// Same fragment, same base: no Build, the first project's closure.
	b := h.NewProject(u, "second", "small")
	bid := b.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &bid, Phases: ops.PlanCreate()}))
	if op.State != "done" {
		t.Fatalf("reused create: %+v", op.Error)
	}
	if n := builds(bid); n != 0 {
		t.Fatalf("reused create sent %d Build commands", n)
	}
	bp := h.Project(bid)
	bRev, _ := store.GetRevision(h.Ctx, h.Pool, *bp.ConfigRevisionID)
	if bp.State != "running" || bRev.Status != "applied" || bRev.SystemClosure == nil || *bRev.SystemClosure != *aRev.SystemClosure ||
		bRev.BaseVersion == nil || aRev.BaseVersion == nil || *bRev.BaseVersion != *aRev.BaseVersion || bRev.BuiltAt == nil || bRev.KernelChanged {
		t.Fatalf("reused revision: state %s %+v", bp.State, bRev)
	}
	var created *hostdv1.CreateGuest
	for _, c := range h.Fake.Commands() {
		if cg := c.GetCreateGuest(); cg != nil && cg.ProjectId == bid.String() {
			created = cg
		}
	}
	if created == nil || created.SystemClosure != *aRev.SystemClosure {
		t.Fatalf("CreateGuest for the reused closure: %+v", created)
	}

	// A different fragment builds.
	c := h.NewProject(u, "third", "small")
	cid := c.ID
	if _, err := h.Pool.Exec(h.Ctx, "update config_revisions set fragment = $2 where project_id = $1", cid, "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }"); err != nil {
		t.Fatal(err)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &cid, Phases: ops.PlanCreate()})); op.State != "done" {
		t.Fatalf("third create: %+v", op.Error)
	}
	if n := builds(cid); n != 1 {
		t.Fatalf("a different fragment sent %d Build commands, want 1", n)
	}

	// With every guest of that closure destroyed, nothing holds it on the
	// host any more and the next create builds.
	for _, pid := range []uuid.UUID{a.ID, bid} {
		pid := pid
		if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(h.Project(pid))})); op.State != "done" {
			t.Fatalf("destroy: %+v", op.Error)
		}
	}
	d := h.NewProject(u, "fourth", "small")
	did := d.ID
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &did, Phases: ops.PlanCreate()})); op.State != "done" {
		t.Fatalf("fourth create: %+v", op.Error)
	}
	if n := builds(did); n != 1 {
		t.Fatalf("a create after the destroys sent %d Build commands, want 1", n)
	}
}

// TestPlacementWaitsForAGuestBeingStopped is I-408: a create on a host
// whose memory is held by a guest being stopped (`repose rm` a moment
// before `repose run`) waits for that guest instead of failing with
// capacity at once, and fails as before once PlacementWait has passed.
func TestPlacementWaitsForAGuestBeingStopped(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	// Room for one large guest below the 8 GiB host reserve.
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set mem_bytes = 16::bigint<<30"); err != nil {
		t.Fatal(err)
	}
	u := h.NewUser("dora")
	old := h.CreateRunning(u, "recruiting")
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'stopping' where id = $1", old.ID); err != nil {
		t.Fatal(err)
	}
	p := h.NewProject(u, "recruiting-2", "large")
	pid := p.ID
	id := h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})
	time.Sleep(1500 * time.Millisecond)
	if op, _ := store.GetOp(h.Ctx, h.Pool, id); op.State == "error" || op.State == "done" {
		t.Fatalf("create while the old guest stops: %s %+v", op.State, op.Error)
	}
	// The old guest is down: its reservation goes, the create places.
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'stopped' where id = $1", old.ID); err != nil {
		t.Fatal(err)
	}
	h.Engine.Kick()
	if op := h.WaitOp(id); op.State != "done" {
		t.Fatalf("create after the stop: %+v", op.Error)
	}
	if p := h.Project(pid); p.State != "running" {
		t.Fatalf("after create: %s", p.State)
	}

	// A guest that stays stopping: the wait ends and the create fails
	// with capacity, as it did before I-408.
	h.StartEngine(ops.Config{BaseRef: "deadbeef", PlacementWait: time.Second})
	if _, err := h.Pool.Exec(h.Ctx, "update projects set state = 'stopping' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	q := h.NewProject(u, "recruiting-3", "large")
	qid := q.ID
	start := time.Now()
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &qid, Phases: ops.PlanCreate()}))
	if op.State != "error" || op.Error["code"] != "capacity" {
		t.Fatalf("create past the wait: %s %+v", op.State, op.Error)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Fatalf("failed after %s, before the wait ended", d)
	}
}
