// Package basebump applies a published base version to every project not
// holding (05-control-plane-api.md §5.7, DECISIONS R4-5): a build op per
// project reusing its current fragment, failures recorded as
// base_update_failed events without stopping the sweep.
package basebump

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// Job sweeps projects behind the latest base.
type Job struct {
	pool   *db.Pool
	engine *ops.Engine
	events *events.Ingest
	log    *slog.Logger
}

// New builds the job.
func New(pool *db.Pool, engine *ops.Engine, ev *events.Ingest, log *slog.Logger) *Job {
	return &Job{pool: pool, engine: engine, events: ev, log: log.With("component", "api")}
}

// Run sweeps daily at 04:00 UTC and, checking every ten minutes, when a
// security release is newer than this process's last sweep, until ctx
// ends. "Newer than the last sweep" rather than "released in the last ten
// minutes" (I-141): api-grpc is redeployed on every push to main, and a
// restart inside a release's ten-minute window lost its sweep until 04:00
// on host-01 (2026.09.21.3, 2026-09-21 02:32Z). After a restart the first
// tick re-sweeps the newest security base; a project already on it is
// not touched.
func (j *Job) Run(ctx context.Context) {
	var lastSweep time.Time
	for {
		next := nextRun(time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		case <-time.After(10 * time.Minute):
			b, err := store.LatestBase(ctx, j.pool)
			eb, eerr := store.LatestErofsBase(ctx, j.pool, "")
			if (err != nil || !securityDue(b, lastSweep)) && (eerr != nil || !securityDue(eb, lastSweep)) {
				continue
			}
		}
		lastSweep = time.Now()
		release, ok, err := db.TryLock(ctx, j.pool, db.LockBaseBump)
		if err != nil || !ok {
			continue
		}
		if _, err := j.Sweep(ctx); err != nil && ctx.Err() == nil {
			j.log.Error("base bump sweep", "event", "base_bump_fail", "err", err.Error())
		}
		release()
	}
}

// securityDue reports whether a security release still needs a sweep from
// this process: it is newer than the last sweep this process ran.
func securityDue(b *store.BaseVersion, lastSweep time.Time) bool {
	return b != nil && b.Security && b.ReleasedAt.After(lastSweep)
}

func nextRun(now time.Time) time.Time {
	n := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.UTC)
	if !n.After(now) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// Sweep enqueues a build for every unheld running project behind the
// latest base and returns the project ids it touched. Stopped projects
// are skipped here and updated on their next start (DECISIONS I-1454). A
// project with an open op or whose last build against the latest base
// failed is skipped until its next successful apply.
func (j *Job) Sweep(ctx context.Context) ([]uuid.UUID, error) {
	erofs, err := j.sweepErofs(ctx)
	if err != nil {
		return nil, err
	}
	nix, version, err := j.sweepNix(ctx)
	if err != nil {
		return nil, err
	}
	j.log.Info("base bump sweep", "event", "base_bump", "version", version, "projects", len(nix)+len(erofs), "nix", len(nix), "erofs", len(erofs))
	return append(nix, erofs...), nil
}

// sweepErofs marks every unheld live erofs project behind the newest
// erofs base for its host's arch with that base as pending_base_version
// (DECISIONS I-1464). No op is enqueued and nothing is built: the next
// StartGuest boots the base and clears the mark. Running and stopped
// projects alike, since marking costs nothing.
func (j *Job) sweepErofs(ctx context.Context) ([]uuid.UUID, error) {
	var touched []uuid.UUID
	for _, a := range []struct{ image, host string }{{"amd64", "x86_64"}, {"arm64", "aarch64"}} {
		latest, err := store.LatestErofsBase(ctx, j.pool, a.image)
		if errors.Is(err, db.ErrNotFound) || db.IsNoRows(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows, err := j.pool.Query(ctx, `update projects p set pending_base_version = $1
			  from hosts h
			 where h.id = p.host_id and h.arch = $2
			   and p.guest_runtime = 'erofs' and p.destroyed_at is null and not p.hold_base_updates
			   and p.state not in ('destroying', 'destroyed')
			   and (p.base_version is null or p.base_version <> $1)
			   and (p.pending_base_version is null or p.pending_base_version <> $1)
			   and p.user_id <> '00000000-0000-7000-8000-000000000000'
			returning p.id`, latest.Version, a.host)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err == nil {
				touched = append(touched, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return touched, nil
}

// sweepNix is the Nix projects' bump: a build op per unheld running
// project behind the newest nix base.
func (j *Job) sweepNix(ctx context.Context) ([]uuid.UUID, string, error) {
	latest, err := store.LatestBase(ctx, j.pool)
	if err != nil {
		if db.IsNoRows(err) || err == db.ErrNotFound {
			return nil, "", nil
		}
		return nil, "", err
	}
	rows, err := j.pool.Query(ctx, `select id from projects where destroyed_at is null and state = 'running' and not hold_base_updates and guest_runtime = 'nix'
		and (base_version is null or base_version <> $1) and config_revision_id is not null and user_id <> '00000000-0000-7000-8000-000000000000' order by created_at`, latest.Version)
	if err != nil {
		return nil, "", err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var touched []uuid.UUID
	for _, pid := range ids {
		p, err := store.GetProject(ctx, j.pool, pid)
		if err != nil {
			continue
		}
		cur, err := store.GetRevision(ctx, j.pool, *p.ConfigRevisionID)
		if err != nil {
			continue
		}
		// Skip a project whose newest revision is already on the latest
		// base: built and waiting to apply at the next start, or failed
		// against it (the user, or the next base, gets it out of that). A
		// revision that failed against an older base is tried again on the
		// new one (I-146): nuru-playground's bump onto 2026.09.21.3 failed
		// in hostd's clone, not in its fragment, and the old rule would
		// have held it on 2026.09.21-m3-0208 until the owner applied
		// something by hand.
		revs, _ := store.ListRevisions(ctx, j.pool, pid)
		if len(revs) > 0 && revs[0].BaseVersion != nil && *revs[0].BaseVersion == latest.Version && revs[0].Status != "applied" {
			continue
		}
		rid := store.NewID()
		err = db.InTx(ctx, j.pool, func(tx db.Tx) error {
			// The applied revision's machine.nix rides along unchanged
			// (I-490): a base update changes the base, and a broken
			// personal file that never applied cannot hold a security
			// base back. Personal changes reach projects on their own.
			if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, menu, base_version, status, personal, personal_revision_id, personal_opt_out) values ($1, $2, $3, $4, $5, 'building', $6, $7, $8)", rid, pid, cur.Fragment, cur.Menu, latest.Version, cur.Personal, cur.PersonalRevisionID, cur.PersonalOptOut); err != nil {
				return err
			}
			_, err := j.engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Params: map[string]any{"base_bump": latest.Version}, Phases: ops.PlanBuild(p.State == "running")}, false)
			return err
		})
		if err != nil {
			j.log.Warn("base bump skipped", "event", "base_bump_skip", "project_id", pid.String(), "err", err.Error())
			continue
		}
		touched = append(touched, pid)
	}
	j.engine.Kick()
	return touched, latest.Version, nil
}

// OnOpFinished records the outcome of a bump build as an event; wired
// into the engine's OnFinished.
func (j *Job) OnOpFinished(ctx context.Context, op *store.Op) {
	v, ok := op.Params["base_bump"].(string)
	if !ok || op.ProjectID == nil || j.events == nil {
		return
	}
	if op.State == "done" {
		// The kind stays base_updated for both outcomes (13 §5 lists it;
		// interfaces keep their shape one release); the summary tells the
		// two apart (I-132): a kernel change ends built with
		// reboot_required, and "applied" then was a lie the owner's
		// projects received on 2026-09-21.
		summary := "base " + v + " applied"
		if op.RebootRequired {
			summary = "base " + v + " built; it changes the kernel, so it takes effect at the next `repose stop && repose start` (run it when the agent is idle)"
		}
		_ = j.events.Platform(ctx, *op.ProjectID, "base_updated", summary) // best effort; the revision row carries the truth
		return
	}
	// A bump op fails in its build or, the build done, in the switch of
	// the running guest; the revision row tells which (I-145: the
	// 2026.09.21.3 sweep told the owner "failed to build" for three
	// projects whose builds had succeeded and whose switch had not).
	msg := "base " + v + " failed to build"
	if op.RevisionID != nil {
		if rev, err := store.GetRevision(ctx, j.pool, *op.RevisionID); err == nil && (rev.Status == "built" || rev.Status == "failed" && revisionBootFailed(rev)) {
			// A revision that built and then did not boot is failed too
			// (I-590), and was not a build failure either.
			msg = "base " + v + " built, but switching the running guest to it failed; the guest keeps its current system"
		}
	}
	if op.Error != nil {
		if m, ok := op.Error["message"].(string); ok {
			msg += ": " + m
		}
	}
	_ = j.events.Platform(ctx, *op.ProjectID, "base_update_failed", msg) // see above
}

// revisionBootFailed reports whether a failed revision built and then
// never booted (its error is boot_failed's, DECISIONS I-590).
func revisionBootFailed(rev *store.Revision) bool {
	return rev.Error != nil && strings.HasPrefix(*rev.Error, "boot_failed: ")
}
