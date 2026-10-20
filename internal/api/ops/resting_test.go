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
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// wakeKinds is kinds() with the pause and the wake, which the older
// lister leaves out.
func wakeKinds(cmds []*hostdv1.Command) string {
	var out []string
	for _, c := range cmds {
		switch {
		case c.GetWakeGuest() != nil:
			out = append(out, "WakeGuest")
		case c.GetPauseGuest() != nil:
			out = append(out, "PauseGuest")
		case c.GetStopGuest() != nil:
			out = append(out, "StopGuest")
		case c.GetStartGuest() != nil:
			out = append(out, "StartGuest")
		case c.GetApplyConfig() != nil:
			out = append(out, "ApplyConfig")
		case c.GetSnapshot() != nil:
			out = append(out, "Snapshot")
		case c.GetResizeVolume() != nil:
			out = append(out, "ResizeVolume")
		case c.GetUpdateSecrets() != nil:
			out = append(out, "UpdateSecrets")
		case c.GetDestroyGuest() != nil:
			out = append(out, "DestroyGuest")
		}
	}
	return strings.Join(out, ",")
}

func rest(t *testing.T, h *apitest.Harness, pid uuid.UUID) {
	t.Helper()
	p := h.Project(pid)
	h.Fake.SetResting(p.GuestID.String(), true)
	if _, err := h.Pool.Exec(h.Ctx, "update projects set resting_since = now() where id = $1", pid); err != nil {
		t.Fatal(err)
	}
}

// TestNoPhaseMeetsAPausedGuest is the §5 checklist item: every plan that
// reaches a guest wakes it first, as its own phase, so no phase is ever
// sent to a machine whose vCPUs are stopped (DECISIONS I-818). The wake
// is a phase and not an op on purpose: the engine allows one open op per
// project, and the op that needs the machine awake is that op.
func TestNoPhaseMeetsAPausedGuest(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ada-rest")
	p := h.CreateRunning(u, "frozen-app")
	pid := p.ID

	for _, c := range []struct {
		name  string
		kind  string
		plan  []string
		param map[string]any
		want  string
	}{
		{"apply", ops.KindApply, ops.PlanApply(), nil, "WakeGuest,ApplyConfig"},
		{"secrets", ops.KindUpdateSecrets, ops.PlanUpdateSecrets(), nil, "WakeGuest,UpdateSecrets"},
		{"snapshot", ops.KindSnapshot, ops.PlanSnapshot(), map[string]any{"reason": "manual"}, "WakeGuest,Snapshot"},
		{"resize", ops.KindResize, ops.PlanResize(), map[string]any{"volume_bytes": float64(60 << 30)}, "WakeGuest,ResizeVolume"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rest(t, h, pid)
			n := len(h.Fake.Commands())
			op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: c.kind, ProjectID: &pid, Params: c.param, Phases: c.plan}))
			if op.State != "done" {
				t.Fatalf("%s: state %s error %+v", c.name, op.State, op.Error)
			}
			if got := wakeKinds(commandsSince(h, n)); got != c.want {
				t.Fatalf("%s commands %s, want %s", c.name, got, c.want)
			}
			for _, g := range h.Fake.Guests() {
				if g.Resting {
					t.Fatalf("%s: the guest is still paused", c.name)
				}
			}
			h.WaitFor("the resting marker to be cleared", func() bool { return h.Project(pid).RestingSince == nil })
		})
	}

	// A stop wakes before it stops: hostd's stop asks guestd to shut the
	// system down, and a paused guest answers nothing.
	rest(t, h, pid)
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": false}, Phases: ops.PlanStop()}))
	if op.State != "done" {
		t.Fatalf("stop: state %s error %+v", op.State, op.Error)
	}
	if got := wakeKinds(commandsSince(h, n)); got != "WakeGuest,StopGuest" {
		t.Fatalf("stop commands %s, want WakeGuest,StopGuest", got)
	}
	// The marker goes with the state: a stopped machine is not frozen.
	if p := h.Project(pid); p.State != "stopped" || p.RestingSince != nil {
		t.Fatalf("after the stop: state %s resting_since %v", p.State, p.RestingSince)
	}
}

// TestWakePhaseIsSkippedWithNothingToWake: a plan carrying the phase
// costs nothing on a host whose Hello did not name `pause` (such a hostd
// answers a command it does not know invalid_argument, which would fail
// the op) and nothing for a machine that is not running.
func TestWakePhaseIsSkippedWithNothingToWake(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{NoPause: true}})
	u := h.NewUser("ada-old-host")
	p := h.CreateRunning(u, "on-an-old-host")
	pid := p.ID
	n := len(h.Fake.Commands())
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindApply, ProjectID: &pid, Phases: ops.PlanApply()}))
	if op.State != "done" {
		t.Fatalf("apply on a host without the feature: state %s error %+v", op.State, op.Error)
	}
	if got := wakeKinds(commandsSince(h, n)); got != "ApplyConfig" {
		t.Fatalf("commands %s, want ApplyConfig alone", got)
	}
}

// TestGuestUnresponsiveFromARestingProjectIsNotAReboot is the §5
// checklist item: a `guest_unresponsive` that comes back for a project
// the api believes resting is answered with a wake and the same phase
// again, never with PlanRestart, which is a stop and a start and would
// lose every process (DECISIONS I-818).
func TestGuestUnresponsiveFromARestingProjectIsNotAReboot(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("ada-unresp")
	p := h.CreateRunning(u, "stranded")
	pid := p.ID
	// The api believes it rests; its guestd answers nothing, which is
	// what hostd reports as guest_unresponsive for a paused guest.
	if _, err := h.Pool.Exec(h.Ctx, "update projects set resting_since = now() where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, "insert into config_revisions (id, project_id, fragment, status, system_closure) values ($1, $2, '{}', 'built', '/nix/store/new-system')", store.NewID(), pid); err != nil {
		t.Fatal(err)
	}
	h.Fake.SetGuestdDead(p.GuestID.String(), true)
	n := len(h.Fake.Commands())
	// A start's apply phase with no wake before it: the phase list an api
	// before I-818 enqueued, so the recovery is the only thing between
	// this machine and a reboot. The control is
	// TestStartWhoseApplyLosesGuestdRestarts, the same setup without the
	// marker, which still ends in the restart.
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: []string{ops.PhaseApplyConfig}}))
	// The wake and the phase again, and no reboot. The op ends `done`
	// with the apply's own warning (I-590): the machine runs what it ran,
	// with every process it had, which is the whole point.
	if got, want := wakeKinds(commandsSince(h, n)), "ApplyConfig,WakeGuest,ApplyConfig"; got != want {
		t.Fatalf("commands %s, want %s", got, want)
	}
	if rec, _ := op.Params["recovered"].(string); rec != "apply_config:guest_unresponsive" {
		t.Fatalf("recovered %q", rec)
	}
	if ph, ok := op.Params["phases"].([]any); !ok || len(ph) != 2 || ph[0] != ops.PhaseWake {
		t.Fatalf("phases after the recovery: %v, want the wake then the phase again", op.Params["phases"])
	}
	if p := h.Project(pid); p.State != "running" {
		t.Fatalf("the machine is %s; a resting project must not be stopped to recover it", p.State)
	}
	if g := h.Fake.Guests()[0]; g.State != "running" {
		t.Fatalf("the guest is %s", g.State)
	}
}

// TestWakePhaseHasItsOwnTimeout: the wake gets a budget of its own,
// between the ten-minute default (which would hide a host that is gone)
// and the thirty minutes a stop_guest waits. It has to outlast a hostd
// restart, because a wake that genuinely fails answers with an error in
// seconds and this timeout only fires when the host answers nothing at
// all -- the case the engine re-sends after the next Hello. At thirty
// seconds a hostd upgrade turned `repose stop` on a frozen machine into
// a failed op (DECISIONS I-822).
func TestWakePhaseHasItsOwnTimeout(t *testing.T) {
	d := ops.PhaseTimeoutForTest(ops.PhaseWake)
	if d != 5*time.Minute {
		t.Fatalf("wake phase timeout %s, want 5m", d)
	}
	if other := ops.PhaseTimeoutForTest("apply_config"); d >= other {
		t.Fatalf("the wake (%s) must be shorter than the default phase (%s)", d, other)
	}
	if d < time.Minute {
		t.Fatalf("the wake (%s) must outlast a hostd restart", d)
	}
}
