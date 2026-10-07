package ops_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
)

const badConsole = "<<< NixOS Stage 1 >>>\nstage 2 init script (/mnt-root/nix/store/x-nixos-system/init) not found\nAn error occurred in stage 1 of the boot process\n"

func addBuiltRevision(t *testing.T, h *apitest.Harness, pid uuid.UUID, closure string) uuid.UUID {
	t.Helper()
	rid := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure, base_version) values ($1, $2, '{}', 'built', $3, 'next')", rid, pid, closure); err != nil {
		t.Fatal(err)
	}
	return rid
}

func revisionStatus(t *testing.T, h *apitest.Harness, rid uuid.UUID) (string, string) {
	t.Helper()
	var status string
	var errText *string
	if err := h.Pool.QueryRow(h.Ctx, "select status, error from config_revisions where id = $1", rid).Scan(&status, &errText); err != nil {
		t.Fatal(err)
	}
	if errText == nil {
		return status, ""
	}
	return status, *errText
}

// The kanali restart of 2026-10-07 17:40Z (I-590): the restart adopts the
// pending revision on the stopped guest, its boot never reaches Ready,
// and hostd boots the closure the guest ran. The op ends done, the
// project runs, the revision that did not boot is failed (so no start or
// base sweep sends it again), the one that booted is applied again, and
// the user reads why in the op's warning, last_error and an event.
func TestRestartWhoseNewSystemDoesNotBootRunsThePreviousOne(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("kan")
	p := h.CreateRunning(u, "kanali")
	pid := p.ID
	applied := *p.ConfigRevisionID
	bad := "/nix/store/1kyz-nixos-system-repose-guest-2026.10.05.1"
	rid := addBuiltRevision(t, h, pid, bad)
	h.Fake.SetBadClosure(bad, badConsole)
	killGuestd(t, h, pid, "error")
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Params: ops.RestartParams(), Phases: ops.PlanRestart(true)}))
	if op.State != "done" {
		t.Fatalf("restart: %s %+v", op.State, op.Error)
	}
	if got := kinds(commandsSince(h, n)); got != "StopGuest,ApplyConfig,StartGuest" {
		t.Fatalf("commands %s", got)
	}
	w, _ := op.Result["warning"].(map[string]any)
	if w["code"] != "boot_failed" || !strings.Contains(w["message"].(string), "runs its previous one") || !strings.Contains(w["message"].(string), fakehostd.BadBootMessage) {
		t.Fatalf("warning %v", op.Result["warning"])
	}
	if c, _ := op.Result["console"].(string); !strings.Contains(c, "stage 2 init script") {
		t.Fatalf("console tail not kept: %q", c)
	}
	p = h.Project(pid)
	if p.State != "running" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "boot_failed: its new system did not boot") {
		t.Fatalf("project %s last_error %v", p.State, p.LastError)
	}
	if p.ConfigRevisionID == nil || *p.ConfigRevisionID != applied {
		t.Fatalf("config revision %v, want the one that booted %s", p.ConfigRevisionID, applied)
	}
	if st, e := revisionStatus(t, h, rid); st != "failed" || !strings.HasPrefix(e, "boot_failed: ") {
		t.Fatalf("revision that did not boot: %s %q", st, e)
	}
	if st, _ := revisionStatus(t, h, applied); st != "applied" {
		t.Fatalf("revision that booted: %s", st)
	}
	if pending, err := ops.PendingRevision(h.Ctx, h.Pool, p); err != nil || pending {
		t.Fatalf("the revision that did not boot is still pending: %v %v", pending, err)
	}
	evs, _ := store.ListEvents(h.Ctx, h.Pool, pid, time.Time{}, 50)
	found := false
	for _, e := range evs {
		found = found || e.Kind == ops.EventBootFailed && strings.HasPrefix(e.Summary, "kanali: its new system did not boot")
	}
	if !found {
		t.Fatalf("no boot_failed event: %+v", evs)
	}
	// The next start is a plain one: nothing pending, no reboot onto the
	// closure that failed.
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": false}, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatal(op.Error)
	}
	n = len(h.Fake.Commands())
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(false)}))
	if op.State != "done" || op.Result["warning"] != nil {
		t.Fatalf("plain start: %s %v", op.State, op.Result)
	}
	if got := kinds(commandsSince(h, n)); got != "StartGuest" {
		t.Fatalf("commands %s", got)
	}
	if p := h.Project(pid); p.LastError != nil {
		t.Fatalf("a clean start left last_error %q", *p.LastError)
	}
}

// A start whose switch of the running guest fails (the nightly's
// not_found of 2026-10-06, I-593) leaves the project running with the
// failure in a warning and last_error; before I-590 it put a running
// project in error, and the next `repose start` rebooted it.
func TestStartWhoseSwitchFailsStaysRunning(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("res")
	p := h.CreateRunning(u, "resized")
	pid := p.ID
	rid := addBuiltRevision(t, h, pid, "/nix/store/hidden-system")
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": false}, Phases: ops.PlanStop()}))
	if op.State != "done" {
		t.Fatal(op.Error)
	}
	h.Fake.SetFail("ApplyConfig", "not_found")
	h.Fake.SetFailMessage("ApplyConfig", "switch failed: not_found: switch: /nix/store/hidden-system is not in the store share")
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(true)}))
	if op.State != "done" {
		t.Fatalf("start: %s %+v", op.State, op.Error)
	}
	w, _ := op.Result["warning"].(map[string]any)
	if w["code"] != "not_found" || !strings.Contains(w["message"].(string), "not in the machine's store") {
		t.Fatalf("warning %v", op.Result["warning"])
	}
	p = h.Project(pid)
	if p.State != "running" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "not_found: the new system is not in the machine's store") {
		t.Fatalf("project %s last_error %v", p.State, p.LastError)
	}
	if st, _ := revisionStatus(t, h, rid); st != "built" {
		t.Fatalf("a revision whose switch failed is %s, want built (tried again at the next start)", st)
	}
}

// A restart whose offline apply is refused still boots what the guest
// has: the apply is skipped, not the start.
func TestRestartWhoseApplyIsRefusedStillBoots(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ref")
	p := h.CreateRunning(u, "refused")
	pid := p.ID
	addBuiltRevision(t, h, pid, "/nix/store/no-init-system")
	killGuestd(t, h, pid, "error")
	h.Fake.SetFail("ApplyConfig", "not_found")
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Params: ops.RestartParams(), Phases: ops.PlanRestart(true)}))
	if op.State != "done" {
		t.Fatalf("restart: %s %+v", op.State, op.Error)
	}
	if got := kinds(commandsSince(h, n)); got != "StopGuest,ApplyConfig,StartGuest" {
		t.Fatalf("commands %s", got)
	}
	if p := h.Project(pid); p.State != "running" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "not_found: ") {
		t.Fatalf("after restart %s %v", p.State, p.LastError)
	}
}

// A first boot that fails (nothing to fall back to) is an error whose
// message says how it failed (I-592), with the console kept for the logs
// route and the host's wording in detail.
func TestCreateWhoseBootFailsSaysHow(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("fir")
	p := h.NewProject(u, "first", "large")
	pid := p.ID
	h.Fake.SetBadClosure("/nix/store/00000000000000000000000000000000-nixos-system-fake-"+p.ConfigRevisionID.String(), badConsole)
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}))
	if op.State != "error" || op.Error["code"] != "boot_failed" {
		t.Fatalf("create: %s %v", op.State, op.Error)
	}
	msg, _ := op.Error["message"].(string)
	if !strings.HasPrefix(msg, "the environment did not boot: "+fakehostd.BadBootMessage) || !strings.Contains(msg, "`repose logs --kind console`") {
		t.Fatalf("message %q", msg)
	}
	if strings.Contains(msg, "NixOS Stage 1") {
		t.Fatal("console text in the message")
	}
	if c, _ := op.Result["console"].(string); !strings.Contains(c, "NixOS Stage 1") {
		t.Fatalf("console not kept: %q", c)
	}
	if p := h.Project(pid); p.State != "error" || p.LastError == nil || !strings.HasPrefix(*p.LastError, "boot_failed: the environment did not boot") {
		t.Fatalf("project %s %v", p.State, p.LastError)
	}
}
