// Package lifecycle is the one start of a project: the checks a start
// passes (billing, abuse hold, state, an unfinished restore, the host) and
// the op it enqueues, in the caller's transaction. `repose start` (the
// HTTP route) and the resume after a host reboot (internal/api/resume,
// DECISIONS I-645) both call Start, so a machine the api starts by itself
// is held to the same rules as one its owner starts.
//
// It sits above ops, billing and abuse: billing and abuse already import
// ops, so the start could not live in ops.
package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/abuse"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
)

// Refusal reasons beyond billing's, in detail.reason.
const (
	ReasonAbuseHold         = "abuse_hold"
	ReasonRestoreUnfinished = "restore_unfinished"
	ReasonHostState         = "host_state"
	ReasonState             = "state"
	ReasonNoGuest           = "no_guest"
)

// Refusal is a start the rules refuse. Code is the api error code the
// HTTP route answers with (payment_required, forbidden, conflict);
// Message is the whole sentence; Detail is the error's detail object.
type Refusal struct {
	Code    string
	Reason  string
	Message string
	Detail  map[string]any
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

// Terminal reports whether waiting does not change the answer: every
// refusal but a host that is not answering, which comes back by itself.
func (r *Refusal) Terminal() bool {
	return r.Reason != ReasonHostState || r.Detail["host_state"] != "unreachable"
}

// Deps are what a start needs.
type Deps struct {
	Pool   *db.Pool
	Engine *ops.Engine
	Gate   *billing.Gate
}

// Options say who is starting.
type Options struct {
	// Resume is the api starting a machine a host restart stopped
	// (DECISIONS I-645). The tick has already decided, from the host's
	// Hello, that the guest is down, whatever projects.state says; a
	// past_due account is let through (billing.Request.Resume); the start
	// boots what was running, never a built revision waiting to be
	// applied, since a reboot for a change is asked for first (terms.md).
	Resume bool
	// GuestdDead says whether a running project's guestd stopped
	// answering, which makes `repose start` restart it (I-157). Nil
	// treats a running project as healthy.
	GuestdDead func(ctx context.Context, p *store.Project) (bool, error)
}

// Result is what Start enqueued.
type Result struct {
	OpID    uuid.UUID
	Restart bool
	// Create is a project that never got a guest, created again (I-406).
	Create bool
}

// Start checks and enqueues a start of projectID for u inside tx, under
// the project's row lock (taken first, before its ops are counted). It
// returns a *Refusal when the rules say no, ops.ErrOpInProgress when an op
// is open, or a database error. The caller commits and kicks the engine.
func Start(ctx context.Context, tx db.Tx, d Deps, u *store.User, projectID uuid.UUID, o Options) (*Result, error) {
	p, err := store.GetProjectForUpdate(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if p.UserID != u.ID {
		return nil, db.ErrNotFound
	}
	if d.Gate == nil {
		return nil, errors.New("lifecycle: no billing gate")
	}
	// On tx: the row lock already holds a connection, and a second one
	// per start could starve the pool under many starts at once.
	if err := d.Gate.CheckIn(ctx, tx, u, billing.Request{Class: p.Class, Project: p.ID, Resume: o.Resume}); err != nil {
		var ref *billing.Refusal
		if errors.As(err, &ref) {
			return nil, &Refusal{Code: "payment_required", Reason: ref.Reason, Message: ref.Message, Detail: ref.Detail}
		}
		return nil, err
	}
	if h, err := abuse.StartHold(ctx, tx, p.ID); err != nil {
		return nil, err
	} else if h != nil {
		return nil, &Refusal{Code: "forbidden", Reason: ReasonAbuseHold, Message: abuse.HoldMessage(p.Slug, h), Detail: map[string]any{"reason": ReasonAbuseHold}}
	}
	// A project in error, or one running whose guestd stopped answering,
	// is restarted rather than started: stop the unit, boot it again on its
	// newest built revision (I-157). `repose start` is the user's recovery.
	restart := false
	if o.Resume {
		switch p.State {
		case "running", "starting", "stopped", "error":
		default:
			return nil, stateRefusal(p)
		}
	} else {
		switch p.State {
		case "running":
			dead := false
			if o.GuestdDead != nil {
				if dead, err = o.GuestdDead(ctx, p); err != nil {
					return nil, err
				}
			}
			if !dead {
				return nil, &Refusal{Code: "conflict", Reason: ReasonState, Message: fmt.Sprintf("%s is already %s", p.Slug, p.State)}
			}
			restart = true
		case "starting":
			return nil, &Refusal{Code: "conflict", Reason: ReasonState, Message: fmt.Sprintf("%s is already %s", p.Slug, p.State)}
		case "error":
			restart = true
		case "stopped":
		default:
			return nil, stateRefusal(p)
		}
	}
	// A restore that failed in its build or restore phase leaves the
	// project with no usable volume: the old one was destroyed first, the
	// new one is incomplete (one that failed in destroy_guest left the old
	// guest as it was and is not refused). A start would boot an empty or
	// half-written volume, so the way back is another restore (DECISIONS
	// I-461).
	if p.State == "error" {
		unfinished, source, err := ops.UnfinishedClone(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
		if unfinished {
			msg := fmt.Sprintf("%s has no usable volume because the restore into it did not finish; restore a snapshot into it again, or remove it with `repose rm %s`", p.Slug, p.Slug)
			// A fork's cloned copy has no snapshot of its own (I-701, I-703).
			if source != uuid.Nil {
				if src, err := store.GetProject(ctx, tx, source); err == nil && src.DestroyedAt == nil {
					msg = fmt.Sprintf("%s has no usable volume because copying %s's disk into it did not finish; remove it with `repose rm %s` and fork %s again", p.Slug, src.Slug, p.Slug, src.Slug)
				} else {
					msg = fmt.Sprintf("%s has no usable volume because copying its source's disk into it did not finish; remove it with `repose rm %s`", p.Slug, p.Slug)
				}
			}
			return nil, &Refusal{Code: "conflict", Reason: ReasonRestoreUnfinished, Message: msg,
				Detail: map[string]any{"reason": ReasonRestoreUnfinished}}
		}
	}
	if p.GuestID == nil {
		if o.Resume {
			return nil, &Refusal{Code: "conflict", Reason: ReasonNoGuest, Message: fmt.Sprintf("%s has no machine to start", p.Slug)}
		}
		return create(ctx, tx, d, p)
	}
	if p.HostID != nil {
		h, err := store.GetHost(ctx, tx, *p.HostID)
		if err == nil {
			switch h.State {
			case "unreachable":
				return nil, &Refusal{Code: "conflict", Reason: ReasonHostState, Message: UnreachableMessage(p.Slug), Detail: map[string]any{"host_state": h.State}}
			case "retired", "lost":
				return nil, &Refusal{Code: "conflict", Reason: ReasonHostState, Message: fmt.Sprintf("%s's host is %s; restore its latest snapshot onto another host", p.Slug, h.State), Detail: map[string]any{"host_state": h.State}}
			}
		}
	}
	pending := false
	if !o.Resume {
		if pending, err = ops.PendingRevision(ctx, tx, p); err != nil {
			return nil, err
		}
	}
	pid := p.ID
	n := ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(pending)}
	if restart {
		n.Phases, n.Params = ops.PlanRestart(pending), ops.RestartParams()
	}
	if o.Resume {
		// The op says why it ran, for `repose-admin ops` and the RUNBOOK.
		n.Params = map[string]any{"resume": true}
	}
	// A personal change queued a build for this stopped machine (I-490):
	// the start goes first, on what the machine has, and the build follows
	// it and switches in place.
	yielded, _, ok, err := d.Engine.YieldPersonalBuilds(ctx, tx, pid, "start")
	if err != nil {
		return nil, err
	}
	id, err := d.Engine.Enqueue(ctx, tx, n, ok)
	if err != nil {
		return nil, err
	}
	if err := d.Engine.RequeuePersonalBuilds(ctx, tx, pid, yielded); err != nil {
		return nil, err
	}
	return &Result{OpID: id, Restart: restart}, nil
}

// UnreachableMessage is what a start, `repose ssh` and `repose code` (which
// start a stopped machine, I-641) say while the project's server is not
// answering. The machine's disk is on that server, so the way back is the
// server coming back, and a machine that was running is started again by
// the api once it does (DECISIONS I-645); restoring a snapshot elsewhere
// would lose up to a day of work.
func UnreachableMessage(slug string) string {
	return fmt.Sprintf("%s's server is not answering; machines that were running on it start again by themselves when it is back, and `repose start %s` works then", slug, slug)
}

func stateRefusal(p *store.Project) *Refusal {
	return &Refusal{Code: "conflict", Reason: ReasonState, Message: fmt.Sprintf("%s is %s; wait for it to settle", p.Slug, p.State)}
}

// create runs the create again for a project whose create failed before
// CreateGuest (no host with capacity, a failed build): it has no guest,
// a restart has nothing to stop or boot, and nothing else could create it,
// so `repose start`, `repose run` and the dashboard went on answering
// "create it first" (dogfood 2026-10-01, I-406). The placement is redone
// when the host it had cannot take it.
func create(ctx context.Context, tx db.Tx, d Deps, p *store.Project) (*Result, error) {
	replace := false
	if p.HostID != nil {
		h, err := store.GetHost(ctx, tx, *p.HostID)
		replace = err != nil || h.State != "ready" || h.Draining
	}
	pid := p.ID
	id, err := d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}, false)
	if err != nil {
		return nil, err
	}
	// "creating", as a fresh create reads, so every list stops showing
	// the old error the moment the create is accepted.
	if _, err := tx.Exec(ctx, "update projects set state = 'creating', host_id = case when $2 then null else host_id end where id = $1 and guest_id is null", pid, replace); err != nil {
		return nil, err
	}
	return &Result{OpID: id, Create: true}, nil
}
