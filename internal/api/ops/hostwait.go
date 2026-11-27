package ops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// A create whose only destination is a host of the account's own that has
// not registered yet waits for it instead of being refused (ENG-131,
// DECISIONS I-1700). The owner's ruling: the minutes after `repose host
// add`, while the Mac downloads and installs, are when a customer is most
// likely to make their first project, and a refusal there was a dead end
// on the Own hardware plan, which has no other place to put it.
//
// The create op carries the host it waits for as ParamWaitHost. Each tick
// places it pinned to that host: once the host registers the machine is
// made there; while it is still installing the op waits; when the host is
// removed, or its join token expires before it joined, the create fails
// and the project goes, as a create refused at placement never exists
// (I-1648), with a sentence that says which.

// ParamWaitHost is the create op's param naming the host it waits for.
const ParamWaitHost = "wait_host_id"

// RegistrationWait is PlaceNewOn's answer for a create that waits for a
// host of the account's own to register: not a refusal, so the caller
// writes the row with no host and puts Host in the create op's params
// (ParamWaitHost).
type RegistrationWait struct {
	HostID   uuid.UUID
	Name     string
	Provider string
}

func (w *RegistrationWait) Error() string { return "waits for host " + w.Name + " to register" }

// waitable reports whether h is a host a create may wait for: one that
// has never registered and whose join token is still good, so it can
// still join. A token that expired, or none, is a host that cannot.
func waitable(h store.Host, now time.Time) bool {
	return h.State == "registering" && h.RegisteredAt == nil && h.JoinTokenHash != nil &&
		h.JoinTokenExpiresAt != nil && now.Before(*h.JoinTokenExpiresAt)
}

// registrationWaitFor is the host a refused own-host placement may wait
// for, or nil: the pinned host when the project named one, else the
// oldest of the account's hosts that is still installing. Only a refusal
// reaches here, so no host of the account's could take the machine now.
func registrationWaitFor(ctx context.Context, q store.Querier, owner, pin uuid.UUID, now time.Time) *RegistrationWait {
	hosts, err := store.ListAccountHosts(ctx, q, owner)
	if err != nil {
		return nil
	}
	for _, h := range hosts {
		if pin != uuid.Nil && h.ID != pin {
			continue
		}
		if waitable(h, now) {
			w := &RegistrationWait{HostID: h.ID, Name: h.Name}
			if h.Provider != nil {
				w.Provider = *h.Provider
			}
			return w
		}
	}
	return nil
}

// errNeverJoined is a waiting create that can no longer be placed: its
// host was removed, or its join token expired before it joined. The code
// is capacity, as every own-host placement failure's is.
func errNeverJoined(name string, removed bool) error {
	if removed {
		if name == "" {
			return &opError{code: "capacity", msg: "your host was removed before it finished installing"}
		}
		return &opError{code: "capacity", msg: "your host " + name + " was removed before it finished installing"}
	}
	return &opError{code: "capacity", msg: "your host " + name + " did not finish installing within 24 hours of `repose host add`"}
}

// waitHostOf is the host a create op waits for, or uuid.Nil.
func waitHostOf(op *store.Op) uuid.UUID {
	s, _ := op.Params[ParamWaitHost].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// registrationWaits is placeOp's answer for a create that waits for its
// host to register, after a pick pinned to that host refused: true while
// the host can still join, an error once it cannot (removed, or its
// token expired), false when it registered and refused the machine for
// another reason, which the caller reports as any own-host refusal.
func (e *Engine) registrationWaits(ctx context.Context, op *store.Op, p *store.Project, hostID uuid.UUID) (bool, error) {
	h, err := store.GetHost(ctx, e.pool, hostID)
	if errors.Is(err, db.ErrNotFound) {
		return false, errNeverJoined("", true)
	}
	if err != nil {
		return false, err
	}
	switch {
	case h.State == "retired" || h.State == "lost":
		return false, errNeverJoined(h.Name, true)
	case waitable(*h, e.now()):
		if _, logged := e.placeWaiting.LoadOrStore(op.ID, true); !logged {
			e.log.Info("create waits for the account's host to register", "event", "schedule_wait",
				"op_id", op.ID.String(), "project_id", p.ID.String(), "host_id", h.ID.String(), "reason", "host_registering")
		}
		return true, nil
	case h.RegisteredAt == nil:
		// Never joined, and its token is spent or expired: it cannot
		// join on it any more.
		return false, errNeverJoined(h.Name, false)
	}
	return false, nil
}

// CancelRegistrationWait ends the open create of a project that is still
// waiting for its host to register, inside tx, so a destroy can be
// enqueued behind it (`repose rm` on a waiting project): a create waits
// for as long as the host can join, and a destroy refused for "an
// operation is in progress" all that time would leave the user no way to
// take the project back. It answers false, changing nothing, when the
// project has no such create or the create has already placed it. The op
// row is locked before the project row, the order placeOp takes them in.
func (e *Engine) CancelRegistrationWait(ctx context.Context, tx db.Tx, projectID uuid.UUID) (bool, error) {
	var opID uuid.UUID
	var params map[string]any
	err := tx.QueryRow(ctx, `select id, params from ops where project_id = $1 and kind = $2
		and state in ('pending', 'running') order by created_at limit 1 for update`, projectID, KindCreate).Scan(&opID, &params)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s, _ := params[ParamWaitHost].(string); s == "" {
		return false, nil
	}
	var host *uuid.UUID
	if err := tx.QueryRow(ctx, "select host_id from projects where id = $1 for update", projectID).Scan(&host); err != nil {
		return false, err
	}
	if host != nil {
		return false, nil // placed: the create runs on and the destroy waits for it
	}
	if _, err := tx.Exec(ctx, `update ops set state = 'error', finished_at = now(),
		error = jsonb_build_object('code', 'cancelled', 'message', 'the project was removed while it waited for its host')
		where id = $1`, opID); err != nil {
		return false, err
	}
	e.placeWaiting.Delete(opID)
	e.m.OpsTotal.WithLabelValues(KindCreate, "error").Inc()
	e.m.OpsOpen.WithLabelValues(KindCreate).Dec()
	e.log.Info("waiting create cancelled by a destroy", "event", "op_fail", "op_id", opID.String(), "project_id", projectID.String(), "code", "cancelled")
	return true, nil
}

// pinTarget is t pinned to the host a waiting create waits for.
func pinTarget(t scheduler.Target, op *store.Op) scheduler.Target {
	if h := waitHostOf(op); h != uuid.Nil {
		t.Host = h
	}
	return t
}
