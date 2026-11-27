package guest

import (
	"context"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// FeatureRestart is the Hello feature that says this hostd takes
// RestartHost: it parks every machine, then restarts the machine it runs
// on (DECISIONS I-1690). Only a hostd with Deps.RestartHost and the park
// turned on names it, which today is a Mac without the appliance.
const FeatureRestart = "restart"

// RestartDelay is how long hostd waits after answering a RestartHost
// before it restarts the host, so the answer reaches the api first. A
// var for tests.
var RestartDelay = 2 * time.Second

// RestartWatch is how long hostd waits for the host to go down after it
// asked for the restart. A host still up after it did not restart (an
// app on the Mac with unsaved work can cancel a restart), so the parked
// machines run again rather than wait for a restart that is not coming.
// A var for tests.
var RestartWatch = 3 * time.Minute

// RestartOn reports whether this host takes RestartHost.
func (m *Manager) RestartOn() bool { return m.d.RestartHost != nil && m.ParkEnabled() }

// RestartTally reads a ParkAll's results against the guests that were
// running before it: parked counts the guests whose memory is saved (parked
// now, or parked already), and notParked names every running guest that
// is not, including one ParkAll left out of its results (a park that did
// not run at all returns none, and its guests would lose their memory as
// surely as a failed one).
func RestartTally(running []string, res []ParkResult) (parked int, notParked []string) {
	parked, notParked, _ = restartTally(running, res)
	return parked, notParked
}

// ReasonNotParked is the reason given for a running guest the park did
// not report on at all.
const ReasonNotParked = "the park did not reach it"

func restartTally(running []string, res []ParkResult) (parked int, notParked, reasons []string) {
	saved := map[string]bool{}
	why := map[string]string{}
	for _, r := range res {
		if r.Reason != "" {
			why[r.GuestID] = r.Reason
		} else if r.Outcome != "" {
			why[r.GuestID] = "the park " + r.Outcome
		}
		switch {
		case r.Outcome == "parked", r.Outcome == "skipped" && r.Reason == SkipParked:
			saved[r.GuestID] = true
			parked++
		case r.Outcome == "skipped" && r.Reason == SkipNotRunning:
			saved[r.GuestID] = true
		}
	}
	for _, id := range running {
		if !saved[id] {
			notParked = append(notParked, id)
			r := why[id]
			if r == "" {
				r = ReasonNotParked
			}
			reasons = append(reasons, r)
		}
	}
	return parked, notParked, reasons
}

// runningGuests are the guests a park would take: state running and the
// unit up. A machine resting by save has no unit and is a file already.
func (m *Manager) runningGuests(ctx context.Context) ([]string, error) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, g := range gs {
		if g.State == StateRunning && m.unitRunning(ctx, g) {
			ids = append(ids, g.GuestID)
		}
	}
	return ids, nil
}

// restartHost is RestartHost (DECISIONS I-1690): park every running guest,
// and restart the host once the answer is on its way. A guest that could
// not be parked stops the restart unless Force, and the parked ones are
// restored; the answer names it either way. Idempotent through the
// command record: a resent RestartHost gets the stored answer, so a host
// that came back from the restart does not restart again.
func (m *Manager) restartHost(ctx context.Context, c *hostdv1.RestartHost) (*hostdv1.RestartResult, *Error) {
	if !m.RestartOn() {
		return nil, errf(CodeInvalidArgument, "this host does not restart through repose")
	}
	running, err := m.runningGuests(ctx)
	if err != nil {
		return nil, errf(CodeInternal, "list guests: %v", err)
	}
	t0 := m.d.Now()
	res := m.ParkAll(ctx, ParkOperator)
	parked, notParked, reasons := restartTally(running, res)
	out := &hostdv1.RestartResult{Parked: uint32(parked), NotParked: notParked, NotParkedReasons: reasons, ParkMs: uint32(m.d.Now().Sub(t0).Milliseconds())}
	if len(notParked) > 0 && !c.GetForce() {
		back := m.UnparkAll(ctx)
		m.d.Log.Warn("restart refused: machines could not be parked", "event", "host_restart", "parked", parked, "not_parked", len(notParked), "unparked", len(back))
		return out, nil
	}
	out.Restarting = true
	m.d.Log.Info("restarting the host", "event", "host_restart", "parked", parked, "not_parked", len(notParked), "park_ms", out.ParkMs)
	go m.restartAfterAnswer()
	return out, nil
}

// restartAfterAnswer restarts the host after RestartDelay, and runs the
// parked machines again if the host is still up RestartWatch later or the
// restart could not be asked for.
func (m *Manager) restartAfterAnswer() {
	time.Sleep(RestartDelay)
	ctx := context.Background()
	if err := m.d.RestartHost(ctx); err != nil {
		m.d.Log.Error("host restart failed; running the parked machines again", "event", "host_restart", "err", err.Error())
		m.UnparkAll(ctx)
		return
	}
	time.Sleep(RestartWatch)
	m.d.Log.Warn("the host did not restart; running the parked machines again", "event", "host_restart", "waited_s", int(RestartWatch.Seconds()))
	m.UnparkAll(ctx)
}
