package guest

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func restartCmd(id string, force bool) *hostdv1.Command {
	return &hostdv1.Command{CommandId: id, Cmd: &hostdv1.Command_RestartHost{RestartHost: &hostdv1.RestartHost{Force: force}}}
}

func quickRestart(t *testing.T) {
	t.Helper()
	d, w := RestartDelay, RestartWatch
	RestartDelay, RestartWatch = time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { RestartDelay, RestartWatch = d, w })
}

// A host without a restart (Linux, every fleet host) refuses RestartHost
// and does not name the feature, so the api never offers it (I-1690).
func TestRestartHostRefusedWithoutARestart(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	if slices.Contains(fh.m.Features(), FeatureRestart) {
		t.Fatal("restart named without Deps.RestartHost")
	}
	res := fh.m.Execute(context.Background(), restartCmd("c-r0", false))
	if res.Ok || res.Error.GetCode() != CodeInvalidArgument {
		t.Fatalf("%+v", res)
	}
	if !fh.unitActive(gid1) || fh.record(gid1) != nil {
		t.Fatal("a refused restart touched the guest")
	}
}

// Every machine parked: the answer says so and the host restarts after it.
func TestRestartHostParksThenRestarts(t *testing.T) {
	quickRestart(t)
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.create(gid2)
	var restarts atomic.Int32
	fh.m.d.RestartHost = func(context.Context) error { restarts.Add(1); return nil }
	if !slices.Contains(fh.m.Features(), FeatureRestart) {
		t.Fatal("restart not named")
	}
	res := fh.m.Execute(context.Background(), restartCmd("c-r1", false))
	r := res.GetRestart()
	if !res.Ok || r.GetParked() != 2 || len(r.GetNotParked()) != 0 || !r.GetRestarting() {
		t.Fatalf("%+v", res)
	}
	if fh.unitActive(gid1) || fh.unitActive(gid2) || fh.record(gid1) == nil || fh.record(gid2) == nil {
		t.Fatal("not parked")
	}
	waitFor(t, func() bool { return restarts.Load() == 1 })
	// The test host never goes down, so RestartWatch runs them again.
	waitFor(t, func() bool { return fh.unitActive(gid1) && fh.unitActive(gid2) && fh.record(gid1) == nil })
}

// A machine that cannot be parked stops the restart: the parked ones run
// again and the answer names the one that could not be.
func TestRestartHostRefusesWhenAMachineCannotBeParked(t *testing.T) {
	quickRestart(t)
	fh := newParkHarness(t, nil)
	fh.create(gid1)
	fh.create(gid2)
	var restarts atomic.Int32
	fh.m.d.RestartHost = func(context.Context) error { restarts.Add(1); return nil }
	fh.m.d.Park.CanPark = func(_ context.Context, id string) (bool, string) {
		return id != gid2, "started with nobody logged in"
	}
	res := fh.m.Execute(context.Background(), restartCmd("c-r2", false))
	r := res.GetRestart()
	if !res.Ok || r.GetRestarting() || r.GetParked() != 1 || !slices.Equal(r.GetNotParked(), []string{gid2}) {
		t.Fatalf("%+v", res)
	}
	if !fh.unitActive(gid1) || fh.record(gid1) != nil || !fh.unitActive(gid2) {
		t.Fatal("the parked machine did not run again")
	}
	time.Sleep(100 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("restarted after a refusal")
	}
	// force restarts anyway, naming it.
	res = fh.m.Execute(context.Background(), restartCmd("c-r3", true))
	if r := res.GetRestart(); !res.Ok || !r.GetRestarting() || !slices.Equal(r.GetNotParked(), []string{gid2}) {
		t.Fatalf("force: %+v", res)
	}
	waitFor(t, func() bool { return restarts.Load() == 1 })
	waitFor(t, func() bool { return fh.unitActive(gid1) && fh.record(gid1) == nil })
}

// A park that did not run at all (the park is busy, or has no key) returns
// no results; its running guests count as not parked, never as nothing to
// lose.
func TestRestartTallyCountsGuestsLeftOut(t *testing.T) {
	parked, not := RestartTally([]string{gid1, gid2}, nil)
	if parked != 0 || !slices.Equal(not, []string{gid1, gid2}) {
		t.Fatalf("%d %v", parked, not)
	}
	parked, not = RestartTally([]string{gid1, gid2}, []ParkResult{
		{GuestID: gid1, Outcome: "parked", OK: true},
		{GuestID: gid2, Outcome: "skipped", Reason: SkipParked},
	})
	if parked != 2 || len(not) != 0 {
		t.Fatalf("%d %v", parked, not)
	}
}

// A host still up RestartWatch after it asked for the restart (an app
// cancelled it) runs the parked machines again; so does one whose
// restart could not be asked for.
func TestRestartHostThatDoesNotHappenUnparks(t *testing.T) {
	quickRestart(t)
	for _, fail := range []bool{false, true} {
		fh := newParkHarness(t, nil)
		fh.create(gid1)
		fh.m.d.RestartHost = func(context.Context) error {
			if fail {
				return context.DeadlineExceeded
			}
			return nil
		}
		res := fh.m.Execute(context.Background(), restartCmd("c-r4", false))
		if !res.GetRestart().GetRestarting() {
			t.Fatalf("%+v", res)
		}
		waitFor(t, func() bool { return fh.unitActive(gid1) && fh.record(gid1) == nil })
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in 2s")
}
