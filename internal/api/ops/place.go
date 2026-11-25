package ops

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// PlacementRefusal is PlaceNew's answer when no host can take the
// project: the code and sentence the create op would have failed with,
// answered to the request instead, so nothing is written for it
// (DECISIONS I-1390). Detail carries `reason` (`no_own_host` when the
// account's own hardware is what refused, with `host` and `host_state`,
// the named host's when the project named one, I-1584;
// `no_capacity` or `no_cpu` for the fleet) and `hosted_allowed`, whether
// the account's plan lets it ask for repose's servers instead.
type PlacementRefusal struct {
	Code    string
	Message string
	Detail  map[string]any
}

func (r *PlacementRefusal) Error() string { return r.Code + ": " + r.Message }

// PlaceNew picks the host for a project about to be inserted, inside the
// create's transaction, and answers before the row exists (DECISIONS
// I-1390). Until this the create wrote the row as `creating` and the op
// placed it afterwards, so a refusal left a project in `error` behind,
// and a customer whose own host had not registered yet stranded one
// project per attempt, with no command that cleared them. The caller
// records the pick as `projects.host_id` in the same transaction, which
// is PickHost's contract: the placement lock is held until the commit,
// so the next placement sees this one.
//
// A nil host with a nil error is the one case the op still places: no
// host fits now but a guest being stopped is about to free the room
// (I-408), and the row is written so the op can wait for it as before. A
// PlacementRefusal is the create refused, with the same code (`capacity`,
// 503) and the same sentence the op would have failed with, so a client
// that already handled the failed op reads the same words one step
// earlier.
func (e *Engine) PlaceNew(ctx context.Context, tx db.Tx, p *store.Project) (*uuid.UUID, error) {
	return e.PlaceNewOn(ctx, tx, p, uuid.Nil)
}

// PlaceNewOn is PlaceNew for a project that named one host of its
// account's own (POST /projects `host`, DECISIONS I-1584): that host or a
// refusal that names it and why, never another host. uuid.Nil is no pin,
// which is PlaceNew. The caller has checked that the account owns host.
func (e *Engine) PlaceNewOn(ctx context.Context, tx db.Tx, p *store.Project, host uuid.UUID) (*uuid.UUID, error) {
	t, err := e.target(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	t.Host = host
	pk, err := scheduler.PickHost(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now())
	if errors.Is(err, scheduler.ErrNoOwnHost) {
		if e.afterOwnRefusal != nil {
			e.afterOwnRefusal(ctx)
		}
		// The sentence below reads the account's hosts after the pick
		// refused, and a heartbeat can land in between: a host that was
		// full at the pick reports room by the time its figure is read,
		// and the sentence fell back to "has no room", which named no
		// figure and was no longer true (ENG-214). Room read now is room
		// the pick can take, so it is asked once more; a second refusal
		// is answered with the figures that refused it.
		if roomAppeared(ctx, tx, p, host) {
			pk, err = scheduler.PickHost(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now())
		}
	}
	if err == nil {
		e.m.ScheduleTotal.WithLabelValues("ok").Inc()
		e.log.Info("placed project", "event", "schedule", "project_id", p.ID.String(), "host_id", pk.HostID.String(), "class", p.Class)
		return &pk.HostID, nil
	}
	if errors.Is(err, scheduler.ErrNoOwnHost) {
		// A host of the account's own that is full but for a guest being
		// stopped waits for it, as a fleet host does (I-408): every
		// refusal on an owned host comes here since I-1586, the full
		// ones included.
		if ok, ferr := scheduler.Freeing(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now()); ferr == nil && ok {
			return nil, nil
		}
		e.m.ScheduleTotal.WithLabelValues("no_own_host").Inc()
		e.log.Warn("no host of the account's own has capacity", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", "no_own_host")
		msg, detail := e.ownHostRefusalDetail(ctx, p, host)
		detail["hosted_allowed"] = t.Fleet
		return nil, &PlacementRefusal{Code: "capacity", Message: msg, Detail: detail}
	}
	if errors.Is(err, scheduler.ErrNoRuntimeHost) {
		// No host runs this guest runtime, or none has its image for its
		// arch (DECISIONS I-1464): no stopping guest frees that.
		e.m.ScheduleTotal.WithLabelValues("no_runtime_host").Inc()
		e.log.Warn("no host runs the project's guest runtime", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", "no_runtime_host", "guest_runtime", store.NormalizeRuntime(p.GuestRuntime))
		return nil, &PlacementRefusal{Code: "capacity", Message: "no host with capacity",
			Detail: map[string]any{"reason": "no_runtime_host", "hosted_allowed": t.Fleet}}
	}
	if errors.Is(err, scheduler.ErrNoCapacity) {
		// The room is coming (a guest on a host that would fit is being
		// stopped): the row is written and the op waits for it, as it
		// has since I-408. Only a refusal that would be final is given
		// now.
		if ok, ferr := scheduler.Freeing(ctx, tx, t, p.Class, p.HeldBytes(), p.VolumeBytes, e.now()); ferr == nil && ok {
			return nil, nil
		}
		reason := "no_capacity"
		if errors.Is(err, scheduler.ErrNoCPUCapacity) {
			reason = "no_cpu"
		}
		e.m.ScheduleTotal.WithLabelValues(reason).Inc()
		e.log.Warn("no host has capacity", "event", "schedule_fail",
			"project_id", p.ID.String(), "class", p.Class, "reason", reason)
		return nil, &PlacementRefusal{Code: "capacity", Message: "no host with capacity",
			Detail: map[string]any{"reason": reason, "hosted_allowed": t.Fleet}}
	}
	e.m.ScheduleTotal.WithLabelValues("error").Inc()
	e.log.Error("placement failed", "event", "schedule_fail",
		"project_id", p.ID.String(), "class", p.Class, "reason", "error", "err", err.Error())
	return nil, err
}

// roomAppeared reports whether a ready host of the account's own that
// the placement could use (pin's alone when the project named one) has
// room for p by placement's own memory and disk figures, read in tx. It
// is asked after a pick refused, so true means the figures changed in
// between. A failed read answers false: the refusal stands.
func roomAppeared(ctx context.Context, tx db.Tx, p *store.Project, pin uuid.UUID) bool {
	hosts, err := store.ListAccountHosts(ctx, tx, p.UserID)
	if err != nil {
		return false
	}
	rooms, err := scheduler.OwnedRooms(ctx, tx, p.UserID, p.HeldBytes(), p.VolumeBytes)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if h.State != "ready" || h.Draining || (pin != uuid.Nil && h.ID != pin) {
			continue
		}
		if r, ok := rooms[h.ID]; ok && roomWhy(r, false) == "" {
			return true
		}
	}
	return false
}
