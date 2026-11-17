package triggers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/events"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/db"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/triggerrun"
	"github.com/heracraft/repose/internal/triggerspec"
)

// DefaultPromptPath is where `-p TEXT` writes the prompt and what
// `--prompt-file` defaults to.
func DefaultPromptPath(name string) string { return ".repose/triggers/" + name + ".md" }

// Put creates a trigger or changes the fields the Input names on one that
// exists, keeping the rest and the route's token, which is `repose
// webhooks add` on an existing name in the same words (I-1110).
//
// class is the project's class, for the concurrency clamp. zone is the
// caller's zone for a spec that names none. A route trigger's route is
// created if it is missing and pointed at the guest's receiver (I-1250).
func (s *Store) Put(ctx context.Context, userID, projectID uuid.UUID, class string, in Input) (t Trigger, created bool, err error) {
	spec, err := in.Validate(class)
	if err != nil {
		return Trigger{}, false, err
	}
	now := s.now().UTC()

	var routeID *uuid.UUID
	routeMade := false
	if in.Kind == KindRoute {
		if s.hooks == nil {
			return Trigger{}, false, errors.New("webhook routes are not configured on this api")
		}
		// The route's target is the receiver, not a port of the user's.
		// Upsert keeps the token of a route that exists, so a URL already
		// in a provider's dashboard goes on working.
		r, _, made, err := s.hooks.Upsert(ctx, userID, projectID, in.Route,
			webhooks.ReceiverPort, "/t/"+in.Name, in.AllowStart)
		if err != nil {
			return Trigger{}, false, err
		}
		id := r.ID
		routeID, routeMade = &id, made
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Trigger{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cur Trigger
	curT, err := s.Get(ctx, projectID, in.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		created = true
	case err != nil:
		return Trigger{}, false, err
	default:
		cur = curT
	}

	if created {
		if in.Kind == "" {
			// Nothing to fire on, so there is nothing to create. The CLI
			// turns this into exit 1 for a name the project does not
			// have, which is what every other noun does.
			return Trigger{}, false, ErrNotFound
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from triggers where project_id = $1`, projectID).Scan(&n); err != nil {
			return Trigger{}, false, err
		}
		if n >= MaxTriggers {
			if routeMade {
				// The route was made before this transaction, so a refusal
				// here would leave a live URL on the receiver with no
				// trigger behind it.
				_ = s.hooks.Delete(ctx, projectID, in.Route)
			}
			return Trigger{}, false, ErrTooMany
		}
		prompt := DefaultPromptPath(in.Name)
		if in.PromptPath != nil && *in.PromptPath != "" {
			prompt = *in.PromptPath
		}
		var anchor, next *time.Time
		var specText, zone *string
		if in.Kind == KindSchedule {
			a := now
			nx := spec.Next(now, a)
			anchor, next = &a, &nx
			st, zn := spec.Text, spec.Zone
			specText, zone = &st, &zn
		}
		id := uuid.Must(uuid.NewV7())
		// Every column in order, one placeholder each: the first draft
		// renumbered these by hand after a column moved and dropped
		// prompt_path, which Postgres answered with "INSERT has more
		// target columns than expressions".
		_, err = tx.Exec(ctx, `insert into triggers (
			id, project_id, name, kind, route_id, route_made,
			spec, zone, anchor_at, next_at,
			prompt_path, workdir, agent, timeout_s, concurrency,
			gate, dedupe, notify, enabled, allow_start, stop_after, catch_up,
			created_at, updated_at)
			values ($1,$2,$3,$4,$5,$6,
				$7,$8,$9,$10,
				$11,coalesce($12, 'checkout'),$13,coalesce($14::int, $23::int),coalesce($15, 1),
				$16,$17,coalesce($18, 'failed'),coalesce($19, true),coalesce($20, false),coalesce($21, false),coalesce($22, false),
				$24,$24)`,
			id, projectID, in.Name, in.Kind, routeID, routeMade,
			specText, zone, anchor, next,
			prompt, in.Workdir, nilIfEmpty(in.Agent), in.TimeoutS, in.Concurrency,
			nilIfEmpty(in.Gate), nilIfEmpty(in.Dedupe), in.Notify, in.Enabled,
			in.AllowStart, in.StopAfter, in.CatchUp,
			int(DefaultTimeout/time.Second), now)
		if err != nil {
			return Trigger{}, false, err
		}
	} else {
		// Only the fields the caller named change. A schedule's spec
		// changing re-anchors it, because `every 15m` counts from the
		// anchor and a new interval from an old anchor fires at a time
		// the user did not ask for.
		set := `update triggers set updated_at = $3`
		args := []any{projectID, in.Name, now}
		add := func(frag string, v any) {
			args = append(args, v)
			set += fmt.Sprintf(`, %s = $%d`, frag, len(args))
		}
		if in.Kind == KindRoute && routeID != nil {
			add("route_id", *routeID)
			if routeMade {
				add("route_made", true)
			}
			add("kind", KindRoute)
			add("spec", nil)
			add("zone", nil)
			add("next_at", nil)
			add("anchor_at", nil)
		}
		if in.Kind == KindSchedule {
			add("kind", KindSchedule)
			add("spec", spec.Text)
			add("zone", spec.Zone)
			add("anchor_at", now)
			add("next_at", spec.Next(now, now))
			add("route_id", nil)
		}
		// Turning a schedule on re-arms its clock from now, and so does a
		// zone named on its own. Without this a trigger off for a week
		// has a fire a week in the past and runs an agent the moment it is
		// enabled (I-1252). `repose triggers on` is this route, which is
		// why the re-arm lives here and not only in SetEnabled.
		if in.Kind != KindSchedule && cur.Kind == KindSchedule {
			zone := cur.Zone
			if in.Zone != "" {
				zone = in.Zone
			}
			reArm := in.Zone != "" && in.Zone != cur.Zone
			if in.Enabled != nil && *in.Enabled && !cur.Enabled {
				reArm = true
			}
			if reArm {
				cs, err := triggerspec.Parse(cur.Spec, zone)
				if err != nil {
					return Trigger{}, false, err
				}
				if in.Zone != "" {
					add("zone", cs.Zone)
				}
				add("anchor_at", now)
				add("next_at", cs.Next(now, now))
			}
		}
		if in.PromptPath != nil {
			add("prompt_path", *in.PromptPath)
		}
		if in.Workdir != nil {
			add("workdir", *in.Workdir)
		}
		if in.Agent != nil {
			add("agent", nilIfEmpty(in.Agent))
		}
		if in.TimeoutS != nil {
			add("timeout_s", *in.TimeoutS)
		}
		if in.Concurrency != nil {
			add("concurrency", *in.Concurrency)
		}
		if in.Gate != nil {
			add("gate", nilIfEmpty(in.Gate))
		}
		if in.Dedupe != nil {
			add("dedupe", nilIfEmpty(in.Dedupe))
		}
		if in.Notify != nil {
			add("notify", *in.Notify)
		}
		if in.Enabled != nil {
			add("enabled", *in.Enabled)
		}
		if in.AllowStart != nil {
			add("allow_start", *in.AllowStart)
		}
		if in.StopAfter != nil {
			add("stop_after", *in.StopAfter)
		}
		if in.CatchUp != nil {
			add("catch_up", *in.CatchUp)
		}
		set += ` where project_id = $1 and name = $2`
		if _, err := tx.Exec(ctx, set, args...); err != nil {
			return Trigger{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Trigger{}, false, err
	}
	s.notifySet(ctx, projectID)
	t, err = s.Get(ctx, projectID, in.Name)
	return t, created, err
}

// notifySet tells the process that holds the host's stream that this
// project's set changed (I-1162). A lost notify costs nothing: the set
// rides the machine's next start.
func (s *Store) notifySet(ctx context.Context, projectID uuid.UUID) {
	if _, err := s.pool.Exec(ctx, "select pg_notify($1, $2)", SetChannel, projectID.String()); err != nil {
		return
	}
}

func nilIfEmpty(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

// Delete removes a trigger. Its route goes with it unless another trigger
// uses that route or the user made it before the trigger did; the caller
// says which with keepRoute.
func (s *Store) Delete(ctx context.Context, projectID uuid.UUID, name string) (routeToRemove string, err error) {
	t, err := s.Get(ctx, projectID, name)
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `delete from triggers where id = $1`, t.ID); err != nil {
		return "", err
	}
	if t.RouteID != nil {
		// Another trigger on the same route keeps it. The route's own
		// target still names this trigger's path, so the api repoints it
		// at the surviving trigger rather than leaving a URL that
		// delivers to a trigger that is gone.
		var other string
		err := tx.QueryRow(ctx, `select name from triggers where route_id = $1 limit 1`, *t.RouteID).Scan(&other)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Only a route this trigger made goes with it. A route the
			// user made first stays: its token is tombstoned for 30 days
			// by a removal, and that is not undoable (I-1114).
			if t.RouteMade {
				routeToRemove = t.RouteName
			}
		case err != nil:
			return "", err
		default:
			if _, err := tx.Exec(ctx, `update webhook_routes set path = $2, updated_at = now() where id = $1`,
				*t.RouteID, "/t/"+other); err != nil {
				return "", err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.notifySet(ctx, projectID)
	return routeToRemove, nil
}

// FireOpts is `repose triggers fire`.
type FireOpts struct {
	Source     string // SourceClock or SourceHand
	PayloadRun *uuid.UUID
	DryRun     bool
	// PayloadPath is the file the CLI wrote over SSH, under the
	// trigger's own folder. The bytes never pass through the api.
	PayloadPath string
}

// Fire writes the `pending` run a worker in the grpc process picks up. It
// is the hand-off, not a call: only that process holds the host's stream
// (I-1162).
//
// A trigger that is off still fires by hand: `off` shuts the doors, and a
// fire is the owner asking for this run.
func (s *Store) Fire(ctx context.Context, projectID uuid.UUID, name string, opts FireOpts) (Run, error) {
	t, err := s.Get(ctx, projectID, name)
	if err != nil {
		return Run{}, err
	}
	if opts.PayloadRun != nil {
		var bytes int64
		err := s.pool.QueryRow(ctx, `select payload_bytes from trigger_runs where project_id = $1 and id = $2`,
			projectID, *opts.PayloadRun).Scan(&bytes)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// A run the api pruned is a payload the guest pruned with it,
			// so there is nothing to replay and nothing to look up.
			return Run{}, ErrRunNotFound
		case err != nil:
			return Run{}, err
		case bytes == 0:
			return Run{}, ErrPayloadGone
		}
	}
	if opts.PayloadPath != "" && !ValidPayloadPath(t.Name, opts.PayloadPath) {
		return Run{}, ErrPayloadPath
	}
	source := opts.Source
	if source == "" {
		source = SourceHand
	}
	run, err := s.insertRun(ctx, nil, t, source, StatePending, "", opts)
	if err != nil {
		return Run{}, err
	}
	// The worker is in the other process, so a fire that waited for the
	// minute tick would make the build loop cost a minute an iteration
	// (I-1162's hand-off, say's Kick).
	if _, err := s.pool.Exec(ctx, "select pg_notify($1, '')", NotifyChannel); err != nil {
		return run, nil
	}
	return run, nil
}

func (s *Store) insertRun(ctx context.Context, tx pgx.Tx, t Trigger, source, state, reason string, opts FireOpts) (Run, error) {
	id := uuid.Must(uuid.NewV7())
	now := s.now().UTC()
	var finished *time.Time
	if Terminal(state) {
		finished = &now
	}
	q := `insert into trigger_runs
		(id, project_id, trigger_id, trigger_name, source, state, reason,
		 payload_path, dry_run, payload_run, command_id, created_at, handoff_at, finished_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$1,$11,$11,$12)`
	args := []any{id, t.ProjectID, t.ID, t.Name, source, state, nilStr(reason),
		nilStr(opts.PayloadPath), opts.DryRun, opts.PayloadRun, now, finished}
	var err error
	if tx != nil {
		_, err = tx.Exec(ctx, q, args...)
	} else {
		_, err = s.pool.Exec(ctx, q, args...)
	}
	if err != nil {
		return Run{}, err
	}
	if tx != nil {
		if err := pruneRuns(ctx, tx, t.ID); err != nil {
			return Run{}, err
		}
	} else if err := pruneRunsPool(ctx, s.pool, t.ID); err != nil {
		return Run{}, err
	}
	return Run{ID: id, ProjectID: t.ProjectID, TriggerID: &t.ID, TriggerName: t.Name,
		Source: source, State: state, Reason: reason, CommandID: &id,
		PayloadPath: opts.PayloadPath, DryRun: opts.DryRun, PayloadRun: opts.PayloadRun,
		FinishedAt: finished, CreatedAt: now}, nil
}

func nilStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Due is one trigger the clock claimed, with the run row to send.
type Due struct {
	Trigger Trigger
	Run     Run
}

// Claim is the clock: it takes every schedule trigger whose next fire has
// passed, writes the following fire in the same transaction, and inserts
// the run row. One replica runs it, under db.LockTriggers, and the claim
// is what makes a fire happen once across replicas and once across a
// restart.
//
// A fire the api is late for writes a `skipped` run unless the trigger
// says --catch-up and the delay is inside its own interval.
func (s *Store) Claim(ctx context.Context, limit int) ([]Due, error) {
	now := s.now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, selectTrigger+` where t.enabled and t.kind = 'schedule'
			and t.next_at is not null and t.next_at <= $1
		order by t.next_at limit $2 for update of t skip locked`, now, limit)
	if err != nil {
		return nil, err
	}
	var claimed []Trigger
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Due
	for _, t := range claimed {
		spec, err := triggerspec.Parse(t.Spec, t.Zone)
		if err != nil {
			// A row whose spec no longer parses would be claimed every
			// minute for ever. Disable it and say why in the run.
			if _, err := tx.Exec(ctx, `update triggers set enabled = false, next_at = null where id = $1`, t.ID); err != nil {
				return nil, err
			}
			if _, err := s.insertRun(ctx, tx, t, SourceClock, StateSkipped, triggerrun.ReasonError, FireOpts{}); err != nil {
				return nil, err
			}
			continue
		}
		anchor := now
		if t.AnchorAt != nil {
			anchor = *t.AnchorAt
		}
		next := spec.Next(now, anchor)
		if _, err := tx.Exec(ctx, `update triggers set next_at = $2, updated_at = now() where id = $1`, t.ID, next); err != nil {
			return nil, err
		}
		late := now.Sub(*t.NextAt)
		missed := late > time.Minute+2*time.Second
		// A fire the clock is late for: the machine was stopped, its host
		// was away, or the api was down. `machine_stopped` would blame a
		// machine that was never asked, so the reason says what happened.
		if missed {
			interval := time.Hour
			if spec.Kind == triggerspec.KindEvery && spec.Every < interval {
				interval = spec.Every
			}
			if !t.CatchUp || late > interval {
				if _, err := s.insertRun(ctx, tx, t, SourceClock, StateSkipped, triggerrun.ReasonClockLate, FireOpts{}); err != nil {
					return nil, err
				}
				continue
			}
		}
		run, err := s.insertRun(ctx, tx, t, SourceClock, StatePending, "", FireOpts{})
		if err != nil {
			return nil, err
		}
		out = append(out, Due{Trigger: t, Run: run})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// TakePending claims up to limit hand-off rows for the worker in the grpc
// process, as say's worker claims a says row: `for update skip locked`,
// so two replicas never send the same RunTrigger.
func (s *Store) TakePending(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `with taken as (
			select id from trigger_runs where state = $1
			order by created_at limit $2 for update skip locked)
		update trigger_runs r set state = $3, handoff_at = now()
		where r.id in (select id from taken)
		returning `+runCols, StatePending, limit, StateSending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// raise turns an ending the api wrote itself into the event (and the
// notification) a user sees. The machine's own endings come through
// OnRun; these do not, and each of them used to write a terminal row
// and tell nobody: the fire the machine never took, the runs a stopped
// machine left, the hand-off nobody delivered and the report that never
// came.
func (s *Store) raise(ctx context.Context, ids ...uuid.UUID) {
	if s.events == nil || len(ids) == 0 {
		return
	}
	rows, err := s.pool.Query(ctx, `select `+runCols+`,
			coalesce((select t.notify from triggers t where t.id = r.trigger_id), 'failed')
		from trigger_runs r where r.id = any($1)`, ids)
	if err != nil {
		return
	}
	type ending struct {
		run    Run
		notify string
	}
	var ends []ending
	for rows.Next() {
		var run Run
		var notify string
		if err := scanRunWith(rows, &run, &notify); err != nil {
			rows.Close()
			return
		}
		ends = append(ends, ending{run, notify})
	}
	rows.Close()
	for _, e := range ends {
		if !Terminal(e.run.State) {
			continue
		}
		s.counted(e.run)
		kind := "trigger_run"
		if e.run.State == StateFailed || e.run.State == StateTimeout {
			kind = "trigger_failed"
		}
		_, _, _ = s.events.Insert(ctx, events.Incoming{
			ProjectID: e.run.ProjectID, TS: s.now().UTC(), Kind: kind,
			Window: e.run.Window, Summary: Sentence(e.run), Source: "api",
			NoNotify: kind == "trigger_failed" && e.notify != "failed",
		})
		// `--stop-after` holds for an ending the api wrote itself too:
		// a fire that started a machine and was then refused, or whose
		// report never came, left that machine running and billing.
		if s.done != nil {
			s.done(ctx, e.run.ID)
		}
	}
}

// coalesceSkip folds one refusal into the single row its (trigger,
// reason, hour) keeps and reports whether that row is new, which is the
// one event a user sees for the hour. It is the whole of §10's promise
// that a week of a stopped machine's refusals costs one row an hour, and
// all three doors go through it: the edge's, the guest's and the api's.
func (s *Store) coalesceSkip(ctx context.Context, tx pgx.Tx, runID, projectID, triggerID uuid.UUID,
	name, source, reason, dedupeKey string) (uuid.UUID, bool, error) {
	hour := s.now().UTC().Truncate(time.Hour)
	var id uuid.UUID
	var skips int
	err := tx.QueryRow(ctx, `insert into trigger_runs
		(id, project_id, trigger_id, trigger_name, source, state, reason,
		 dedupe_key, skip_hour, created_at, finished_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		on conflict (trigger_id, reason, skip_hour) where skip_hour is not null
		do update set skips = trigger_runs.skips + 1,
			finished_at = excluded.finished_at,
			dedupe_key = coalesce(excluded.dedupe_key, trigger_runs.dedupe_key)
		returning id, skips`,
		runID, projectID, triggerID, name, source, StateSkipped, reason,
		nilStr(dedupeKey), hour, s.now().UTC()).Scan(&id, &skips)
	if err != nil {
		return uuid.UUID{}, false, err
	}
	if err := pruneRuns(ctx, tx, triggerID); err != nil {
		return uuid.UUID{}, false, err
	}
	return id, skips == 1, nil
}

// Answered writes the machine's answer to a RunTrigger back on the row,
// which is what a `fire --wait` is waiting for.
func (s *Store) Answered(ctx context.Context, runID uuid.UUID, state, window, reason string) error {
	if !Terminal(state) && state != StateWorking {
		return fmt.Errorf("%q is not an answer to a fire", state)
	}
	if state == StateSkipped {
		return s.answeredSkip(ctx, runID, reason)
	}
	var finished any
	if Terminal(state) {
		finished = s.now().UTC()
	}
	var started any
	if state == StateWorking {
		started = s.now().UTC()
	}
	tag, err := s.pool.Exec(ctx, `update trigger_runs
		set state = $2, tmux_window = coalesce(nullif($3, ''), tmux_window),
		    reason = coalesce(nullif($4, ''), reason),
		    started_at = coalesce(started_at, $5), finished_at = coalesce($6, finished_at)
		where id = $1 and state in ($7, $8)`,
		runID, state, window, reason, started, finished, StatePending, StateSending)
	// Only an update that moved the row raises: a Result the host
	// resends after a reconnect matched nothing, and raising on it gave
	// the run a second ending event and a second notification.
	if err == nil && Terminal(state) && tag.RowsAffected() > 0 {
		s.raise(ctx, runID)
	}
	return err
}

// answeredSkip is the api's own refusal of a fire: the machine is
// stopped, busy, in error, or the plan's memory is in use.
//
// A clock fire folds into its hour's row like every other refusal
// (coalesceSkip). It has to: `every 15m` against a stopped machine wrote
// four rows an hour, and with 20 rows kept per trigger that evicted
// every run that ever ran inside five hours. A hand fire keeps its own
// row, because `repose triggers fire --wait` is waiting on that id.
func (s *Store) answeredSkip(ctx context.Context, runID uuid.UUID, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var projectID uuid.UUID
	var triggerID *uuid.UUID
	var name, source string
	var dedupe *string
	err = tx.QueryRow(ctx, `select project_id, trigger_id, trigger_name, source, dedupe_key
		from trigger_runs where id = $1 and state in ($2, $3) for update`,
		runID, StatePending, StateSending).Scan(&projectID, &triggerID, &name, &source, &dedupe)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already ended, and the first ending wins
	}
	if err != nil {
		return err
	}

	if triggerID == nil || source != SourceClock {
		if _, err := tx.Exec(ctx, `update trigger_runs
			set state = $2, reason = coalesce(nullif($3, ''), reason), finished_at = $4
			where id = $1`, runID, StateSkipped, reason, s.now().UTC()); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		s.raise(ctx, runID)
		return nil
	}

	if _, err := tx.Exec(ctx, `delete from trigger_runs where id = $1`, runID); err != nil {
		return err
	}
	word := reason
	if !triggerrun.Reason(word) {
		word = triggerrun.ReasonError
	}
	id, fresh, err := s.coalesceSkip(ctx, tx, runID, projectID, *triggerID, name, SourceClock, word, strVal(dedupe))
	if err != nil {
		return err
	}
	run, err := s.runTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// The hour's first refusal is the one event; the rest are the count
	// on that row, which is what keeps a week of them quiet.
	if fresh {
		s.raise(ctx, id)
	} else {
		s.counted(run)
	}
	return nil
}

// strVal is a nullable text column as a string.
func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// FailStale fails a hand-off row the worker never finished, past every
// waiter's window, so a fire cannot reach a machine long after the sender
// was told it did not (say's StaleAfter).
func (s *Store) FailStale(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `update trigger_runs
		set state = $1, reason = $6, finished_at = now()
		where state in ($2, $3)
		  and (coalesce(handoff_at, created_at) < $4 or created_at < $5)
		returning id`,
		StateFailed, StatePending, StateSending,
		s.now().UTC().Add(-Stale), s.now().UTC().Add(-MaxWait),
		triggerrun.ReasonUnreachable)
	if err != nil {
		return 0, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return 0, err
	}
	s.raise(ctx, ids...)
	return len(ids), nil
}

// scanIDs reads the ids an update returned.
func scanIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FailWorking ends a run the machine stopped reporting on: it stopped
// mid-run, or the guest's notify budget dropped the report (I-1256).
// Without it a row stays `working` for ever, `fire --wait` burns its
// whole window, and UsedAt counts that row as use for ever, so the
// unused notice never fires on the project again.
//
// The reason is `unreachable` and not `timeout`: the guest owns the
// timeout and kills the window itself, and its clock stops while a
// question waits. A row this sweep ends is one nobody reported on.
func (s *Store) FailWorking(ctx context.Context) (int, error) {
	now := s.now().UTC()
	rows, err := s.pool.Query(ctx, `update trigger_runs r
		set state = $1, reason = $2, finished_at = $3
		from triggers t
		where t.id = r.trigger_id and r.state = $4
		  and r.started_at < $3::timestamptz - make_interval(secs => greatest(t.timeout_s, 1) + $5::int)
		returning r.id`,
		StateFailed, triggerrun.ReasonUnreachable, now, StateWorking, int(WorkingGrace.Seconds()))
	if err != nil {
		return 0, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return 0, err
	}
	// A run whose trigger is gone has no timeout to measure, so it is
	// swept at the ceiling a run can have.
	rows2, err := s.pool.Query(ctx, `update trigger_runs
		set state = $1, reason = $2, finished_at = $3
		where state = $4 and trigger_id is null
		  and started_at < $3::timestamptz - make_interval(secs => $5::int)
		returning id`,
		StateFailed, triggerrun.ReasonUnreachable, now, StateWorking,
		int((MaxTimeout + WorkingGrace).Seconds()))
	if err != nil {
		s.raise(ctx, ids...)
		return len(ids), err
	}
	ids2, err := scanIDs(rows2)
	if err != nil {
		s.raise(ctx, ids...)
		return len(ids), err
	}
	ids = append(ids, ids2...)
	s.raise(ctx, ids...)
	return len(ids), nil
}

// MachineStopped ends every run of a project that was still working when
// its machine stopped, which is §8.3's `failed: the machine stopped`. The
// events ingest calls it on the guest's state change, as it cancels a
// project's questions there.
func (s *Store) MachineStopped(ctx context.Context, projectID uuid.UUID) error {
	rows, err := s.pool.Query(ctx, `update trigger_runs
		set state = $1, reason = $2, finished_at = now()
		where project_id = $3 and state in ($4, $5, $6)
		returning id`,
		StateFailed, triggerrun.ReasonMachineStopped, projectID,
		StateWorking, StatePending, StateSending)
	if err != nil {
		return err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	s.raise(ctx, ids...)
	return nil
}

// StartedMachine records that this run's own fire started the machine,
// which is the only run `--stop-after` may stop it after (I-1252).
func (s *Store) StartedMachine(ctx context.Context, runID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `update trigger_runs set started_machine = true where id = $1`, runID)
	return err
}

// StopAfter is the project and run of a finished run whose trigger says
// `--stop-after` and whose own fire started the machine. The caller
// stops the machine; nothing here does, because stopping is an op.
func (s *Store) StopAfter(ctx context.Context, runID uuid.UUID) (uuid.UUID, bool, error) {
	var projectID uuid.UUID
	err := s.pool.QueryRow(ctx, `select r.project_id from trigger_runs r
		join triggers t on t.id = r.trigger_id
		where r.id = $1 and r.started_machine and t.stop_after`, runID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, false, nil
	}
	return projectID, err == nil, err
}

// Report is a TriggerRun the machine sent: a run it started itself (a
// delivery), or a change of state on one the api asked for. It inserts
// by run id, so a repeat on a new hostd connection is harmless, and it
// never moves a run backwards out of a terminal state.
type Report struct {
	RunID          uuid.UUID
	ProjectID      uuid.UUID
	Trigger        string
	State          string
	Reason         string
	Summary        string
	LinkURL        string
	LinkLabel      string
	Window         string
	DedupeKey      string
	Source         string
	PayloadBytes   int64
	StartedMachine bool
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// Apply writes a machine's report. It returns the stored run and whether
// this report ended it, which is what the notification path reads.
func (s *Store) Apply(ctx context.Context, rep Report) (Run, bool, error) {
	if !triggerrun.GuestStates[rep.State] {
		// A state nobody defined is a report to drop, as a run id that is
		// not a uuid is: the insert would fail the check constraint, the
		// host event would never be acked, and it would come back on
		// every reconnect for ever.
		return Run{}, false, nil
	}
	if !triggerrun.Sources[rep.Source] && rep.Source != "" {
		rep.Source = SourceRoute
	}
	if rep.Reason != "" && !triggerrun.Reason(rep.Reason) && triggerrun.LogReason(rep.Reason) == "" {
		rep.Reason = triggerrun.ReasonError
	}
	if rep.PayloadBytes < 0 || rep.PayloadBytes > PayloadCap {
		rep.PayloadBytes = PayloadCap
	}
	if len(rep.Summary) > SummaryCap {
		rep.Summary = rep.Summary[:SummaryCap]
	}
	if len(rep.Reason) > ReasonCap {
		rep.Reason = rep.Reason[:ReasonCap]
	}
	source := rep.Source
	if source == "" {
		source = SourceRoute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var triggerID *uuid.UUID
	var id uuid.UUID
	err = tx.QueryRow(ctx, `select id from triggers where project_id = $1 and name = $2`, rep.ProjectID, rep.Trigger).Scan(&id)
	if err == nil {
		triggerID = &id
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, err
	}

	// Does the api already hold this run — a fire or a clock run it
	// wrote and sent? That decides what a skip means. A skip of a row
	// the api has is that row's ending; a skip of a run the machine
	// started itself has no row yet and folds into its hour's.
	var was string
	errWas := tx.QueryRow(ctx, `select state from trigger_runs where id = $1 for update`, rep.RunID).Scan(&was)
	known := errWas == nil
	if !known && !errors.Is(errWas, pgx.ErrNoRows) {
		return Run{}, false, errWas
	}

	// A skip the guest reported for a run it started coalesces exactly
	// as an edge-side one does: one row per trigger, reason and hour,
	// with the count. A provider retrying 600 times is the case
	// `--dedupe` exists for, and a row each would evict every run that
	// ran (I-1254, I-1262).
	//
	// Only for a run the api does not already hold: the first draft
	// took this branch for every skip, and a `fire --dry-run` or a
	// gated fire then inserted a second row with a run id that was
	// already a row. The conflict was on the primary key, which this
	// statement's target does not cover, so the insert raised 23505,
	// the event was never acked, and hostd resent it for ever.
	if rep.State == StateSkipped && triggerID != nil && !known {
		reason := rep.Reason
		if !triggerrun.Reason(reason) {
			reason = triggerrun.ReasonGated // a gate's line is one reason, not many
		}
		id, fresh, err := s.coalesceSkip(ctx, tx, rep.RunID, rep.ProjectID, *triggerID,
			rep.Trigger, source, reason, rep.DedupeKey)
		if err != nil {
			return Run{}, false, err
		}
		run, err := s.runTx(ctx, tx, id)
		if err != nil {
			return Run{}, false, err
		}
		s.counted(run)
		return run, fresh, tx.Commit(ctx)
	}

	switch {
	case !known:
		// A run the machine started: a delivery came in the front door
		// and the api hears of it here first.
		if _, err := tx.Exec(ctx, `insert into trigger_runs
			(id, project_id, trigger_id, trigger_name, source, state, reason, summary,
			 link_url, link_label, tmux_window, dedupe_key, payload_bytes, started_machine,
			 started_at, finished_at, created_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,coalesce($15, now()))`,
			rep.RunID, rep.ProjectID, triggerID, rep.Trigger, source, rep.State,
			nilStr(rep.Reason), nilStr(rep.Summary), nilStr(rep.LinkURL), nilStr(rep.LinkLabel),
			nilStr(rep.Window), nilStr(rep.DedupeKey), rep.PayloadBytes, rep.StartedMachine,
			rep.StartedAt, rep.FinishedAt); err != nil {
			return Run{}, false, err
		}
	case Terminal(was):
		// The first terminal state wins, as the first answer to a
		// question does: a repeat on a reconnect changes nothing.
		run, err := s.runTx(ctx, tx, rep.RunID)
		if err != nil {
			return Run{}, false, err
		}
		return run, false, tx.Commit(ctx)
	default:
		if _, err := tx.Exec(ctx, `update trigger_runs set state = $2,
				reason = coalesce(nullif($3, ''), reason),
				summary = coalesce(nullif($4, ''), summary),
				link_url = coalesce(nullif($5, ''), link_url),
				link_label = coalesce(nullif($6, ''), link_label),
				tmux_window = coalesce(nullif($7, ''), tmux_window),
				dedupe_key = coalesce(nullif($8, ''), dedupe_key),
				payload_bytes = greatest(payload_bytes, $9),
				started_machine = started_machine or $10,
				started_at = coalesce(started_at, $11),
				finished_at = coalesce($12, finished_at)
			where id = $1`,
			rep.RunID, rep.State, rep.Reason, rep.Summary, rep.LinkURL, rep.LinkLabel,
			rep.Window, rep.DedupeKey, rep.PayloadBytes, rep.StartedMachine,
			rep.StartedAt, rep.FinishedAt); err != nil {
			return Run{}, false, err
		}
	}
	if triggerID != nil {
		if err := pruneRuns(ctx, tx, *triggerID); err != nil {
			return Run{}, false, err
		}
	}
	run, err := s.runTx(ctx, tx, rep.RunID)
	if err != nil {
		return Run{}, false, err
	}
	if Terminal(rep.State) {
		s.counted(run)
	}
	return run, Terminal(rep.State), tx.Commit(ctx)
}

func (s *Store) runTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Run, error) {
	rows, err := tx.Query(ctx, `select `+runCols+` from trigger_runs where id = $1`, id)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Run{}, ErrRunNotFound
	}
	return scanRun(rows)
}

// pruneRuns keeps the newest RunsKept of a trigger. Per trigger and not
// per project: one route trigger at the hour's cap would otherwise erase
// every other trigger's history in a morning (I-1262).
func pruneRuns(ctx context.Context, tx pgx.Tx, triggerID uuid.UUID) error {
	_, err := tx.Exec(ctx, pruneSQL, triggerID, RunsKept)
	return err
}

func pruneRunsPool(ctx context.Context, pool *db.Pool, triggerID uuid.UUID) error {
	_, err := pool.Exec(ctx, pruneSQL, triggerID, RunsKept)
	return err
}

// A run still in the hand-off or working is never pruned: it is the row
// a waiter is reading and the row the machine will report on.
const pruneSQL = `delete from trigger_runs where trigger_id = $1
	and state not in ('pending', 'sending', 'working')
	and id not in (
		select id from trigger_runs where trigger_id = $1 order by created_at desc limit $2)`

// SkipFromDelivery turns an edge-side refusal into the run row the user
// reads (I-1250). The guest never heard that delivery, so nothing in the
// guest can write it down, and without this a trigger whose machine has
// been stopped for a week appears on no screen.
func (s *Store) SkipFromDelivery(ctx context.Context, routeID uuid.UUID, outcome string, at time.Time) error {
	reason := deliveryReason(outcome)
	if reason == "" {
		return nil
	}
	rows, err := s.pool.Query(ctx, selectTrigger+` where t.route_id = $1`, routeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ts []Trigger
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return err
		}
		ts = append(ts, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range ts {
		// One row per trigger, reason and hour, with the count on it. A
		// machine stopped for a week takes 600 refusals an hour, and a
		// row each would evict every run that ran from the 20 a trigger
		// keeps (I-1254, I-1262).
		// One statement: the insert carries the hour in its conflict
		// target, so two deliveries in one tick add to one row rather
		// than racing to create two.
		hour := at.UTC().Truncate(time.Hour)
		id := uuid.Must(uuid.NewV7())
		if _, err := s.pool.Exec(ctx, `insert into trigger_runs
			(id, project_id, trigger_id, trigger_name, source, state, reason,
			 skip_hour, created_at, finished_at)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			on conflict (trigger_id, reason, skip_hour) where skip_hour is not null
			do update set skips = trigger_runs.skips + 1,
				finished_at = excluded.finished_at`,
			id, t.ProjectID, t.ID, t.Name, SourceRoute, StateSkipped, reason,
			hour, at); err != nil {
			return err
		}
		if err := pruneRunsPool(ctx, s.pool, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// deliveryReason maps an edge outcome to a run's reason. An outcome that
// reached the guest is not a skip: the guest reports those itself.
func deliveryReason(outcome string) string {
	switch outcome {
	case "stopped", "start_refused":
		// `started` is not here: the api started the machine and the
		// sender will retry into a run, so there is nothing to record.
		return "stopped"
	case "wake_failed", "unavailable":
		return "busy"
	case "too_large":
		return "payload_too_large"
	case "rate_limited":
		return "too_many_fires"
	case "no_listener", "timeout":
		// The receiver is guestd's own: no listener means guestd is not
		// up, which is the machine's trouble and not the trigger's.
		return "error"
	case "not_found", "gone":
		return "route_gone"
	case "body_dropped":
		// The app's reply was dropped, which cannot happen to a trigger:
		// the receiver answers JSON the edge passes.
		return ""
	}
	return ""
}

// ForGuest is the SetTriggers payload: the project's whole set, as
// UpdateSecrets sends the whole named set. The prompt is a path; the api
// has never held its text.
func (s *Store) ForGuest(ctx context.Context, projectID uuid.UUID) ([]*hostdv1.Trigger, error) {
	ts, err := s.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	// The agent is resolved here, where the project's default is known:
	// the guest has no copy of it, and a guest guessing would be a
	// second answer to one question.
	var dflt string
	if err := s.pool.QueryRow(ctx, `select coalesce(agent_default, '') from projects where id = $1`,
		projectID).Scan(&dflt); err != nil {
		return nil, err
	}
	out := make([]*hostdv1.Trigger, 0, len(ts))
	for _, t := range ts {
		agent := t.Agent
		if agent == "" {
			agent = dflt
		}
		out = append(out, &hostdv1.Trigger{
			Name:        t.Name,
			Kind:        t.Kind,
			PromptPath:  t.PromptPath,
			Workdir:     t.Workdir,
			Agent:       agent,
			TimeoutS:    uint32(t.TimeoutS),
			Concurrency: uint32(t.Concurrency),
			Gate:        t.Gate,
			Dedupe:      t.Dedupe,
			Enabled:     t.Enabled,
		})
	}
	return out, nil
}

// UsedAt is the newest run that counts as use for the unused notice
// (I-1260). It reads the rows and not the sampler: I-262 decides idleness
// from minute samples, and a 14-second run falls between two of them.
func (s *Store) UsedAt(ctx context.Context, projectID uuid.UUID) (time.Time, error) {
	var at *time.Time
	err := s.pool.QueryRow(ctx, `select max(coalesce(finished_at, created_at))
		from trigger_runs where project_id = $1 and state <> $2`, projectID, StateSkipped).Scan(&at)
	if err != nil || at == nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		return time.Time{}, err
	}
	return *at, nil
}

// Lock is the advisory lock the clock tick holds, so one replica fires.
const Lock = db.LockTriggers

// OnRun records a machine's TriggerRun notification (I-1256). It is the
// events package's TriggerHandler: the run row is this store's, the
// event row is that package's, and the sentence it returns is what the
// owner reads on the Events tab and in a notification.
func (s *Store) OnRun(ctx context.Context, projectID uuid.UUID, ev *hostdv1.TriggerRunEvent) (string, bool, bool, error) {
	runID, err := uuid.Parse(ev.GetRunId())
	if err != nil {
		// A run id that is not a uuid is a guest sending nonsense; drop
		// it rather than leave the host event unacked for ever.
		return "", false, false, nil
	}
	rep := Report{
		RunID: runID, ProjectID: projectID, Trigger: ev.GetTrigger(),
		State: ev.GetState(), Reason: ev.GetReason(), Summary: ev.GetSummary(),
		LinkURL: ev.GetLinkUrl(), LinkLabel: ev.GetLinkLabel(), Window: ev.GetWindow(),
		DedupeKey: ev.GetDedupeKey(), Source: ev.GetSource(),
		PayloadBytes: int64(ev.GetPayloadBytes()),
	}
	if ev.GetStartedUnix() > 0 {
		at := time.Unix(ev.GetStartedUnix(), 0).UTC()
		rep.StartedAt = &at
	}
	if ev.GetFinishedUnix() > 0 {
		at := time.Unix(ev.GetFinishedUnix(), 0).UTC()
		rep.FinishedAt = &at
	}
	run, ended, err := s.Apply(ctx, rep)
	if err != nil {
		return "", false, false, err
	}
	if !ended {
		return "", false, false, nil
	}
	notify := false
	if run.State == StateFailed || run.State == StateTimeout {
		var want string
		err := s.pool.QueryRow(ctx, `select notify from triggers where project_id = $1 and name = $2`,
			projectID, run.TriggerName).Scan(&want)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", false, false, err
		}
		notify = want == "failed"
	}
	return Sentence(run), true, notify, nil
}

// Sentence is a finished run in one line, for the event row and the
// notification. The agent's own words win where it sent any, which is
// I-688's rule that the machine says the state and the agent says what
// happened.
func Sentence(r Run) string {
	switch {
	case r.Summary != "":
		return r.Summary
	case r.State == StateDone:
		return r.TriggerName + " ran"
	case r.Reason != "":
		return r.TriggerName + " " + r.State + ": " + r.Reason
	default:
		return r.TriggerName + " " + r.State
	}
}
