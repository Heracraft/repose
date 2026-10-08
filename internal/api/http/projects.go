package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/heracraft/repose/internal/api/abuse"
	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/temp"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/multiplexer"
	"github.com/heracraft/repose/internal/obs"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var slugClean = regexp.MustCompile(`[^a-z0-9-]+`)
var slugDashes = regexp.MustCompile(`-{2,}`)

// Slug derives the SSH login half from a project name (docs/features/
// projects.md): lowercase, [a-z0-9-], runs collapsed, 1 to 40 characters.
func Slug(name string) string {
	s := strings.ToLower(name)
	s = slugClean.ReplaceAllString(s, "-")
	s = slugDashes.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// DefaultFragment is the empty user fragment a new project starts with.
const DefaultFragment = "{ pkgs, ... }:\n{\n  home.packages = [ ];\n}\n"

type projectExtras struct {
	runningToday, runningMonth int64
	lastSnapshot               *time.Time
	latest                     *meter.Latest
	idleSince                  *time.Time
}

func (s *Server) extras(ctx context.Context, p *store.Project, tz string) (projectExtras, error) {
	var x projectExtras
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	// Running seconds today and this period (I-289); the period is the
	// owner's subscription's, else the calendar month.
	periodStart := monthStart
	if sub, err := billing.LiveSubscription(ctx, s.d.Pool, p.UserID); err != nil {
		return x, err
	} else if sub != nil {
		periodStart = sub.Period(now.UTC()).Start
	}
	if err := s.d.Pool.QueryRow(ctx, `select coalesce(sum(running_seconds) filter (where hour >= $2), 0), coalesce(sum(running_seconds) filter (where hour >= $3), 0) from usage_hours where project_id = $1`, p.ID, dayStart, periodStart).Scan(&x.runningToday, &x.runningMonth); err != nil {
		return x, err
	}
	if err := s.d.Pool.QueryRow(ctx, "select max(taken_at) from snapshots where project_id = $1 and deleted_at is null", p.ID).Scan(&x.lastSnapshot); err != nil {
		return x, err
	}
	l, ok, err := meter.LatestSample(ctx, s.d.Pool, p.ID)
	if err != nil {
		return x, err
	}
	if ok {
		x.latest = l
	}
	// Only a machine up longer than idle.After can be idle; the others
	// skip the query.
	if p.State == "running" && p.StartedAt != nil && time.Since(*p.StartedAt) >= idle.After {
		since, isIdle, err := idle.Project(ctx, s.d.Pool, p, time.Now())
		if err != nil {
			return x, err
		}
		if isIdle {
			x.idleSince = &since
		}
	}
	return x, nil
}

func (s *Server) projectJSON(ctx context.Context, p *store.Project, u *store.User) (map[string]any, error) {
	tz := ""
	if u.TZ != nil {
		tz = *u.TZ
	}
	x, err := s.extras(ctx, p, tz)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id": p.ID, "name": p.Name, "slug": p.Slug, "remote_url": p.RemoteURL, "class": p.Class, "state": p.State,
		"host_id": p.HostID, "guest_ip": nil, "agent_default": p.AgentDefault, "hold_base_updates": p.HoldBaseUpdates,
		"base_version": p.BaseVersion, "config_revision_id": p.ConfigRevisionID, "volume_bytes": p.VolumeBytes,
		"created_at": p.CreatedAt, "started_at": p.StartedAt, "cost_today_cents": 0, "cost_month_cents": 0,
		"running_seconds_today": x.runningToday, "running_seconds_month": x.runningMonth,
		"last_snapshot_at": x.lastSnapshot, "host_unreachable": p.HostUnreachable, "last_error": p.LastError, "tz": p.TZ,
		"personal_opt_out": p.PersonalOptOut,
		// What the next start runs (I-502); a running machine may still
		// run the other one until it stops.
		"multiplexer": multiplexer.Normalize(p.Multiplexer),
	}
	if p.ExpiresAt != nil {
		// A temporary machine (DECISIONS I-347): destroyed with no
		// snapshot once this has passed, unless `repose keep` clears it.
		out["expires_at"] = *p.ExpiresAt
	}
	if p.GuestIP != nil {
		out["guest_ip"] = p.GuestIP.String()
	}
	if x.idleSince != nil {
		// A running machine unused for a day, still holding the plan's
		// memory (I-262). hourly_cents is 0 since I-289, kept one release.
		out["idle"] = map[string]any{"since": *x.idleSince, "hourly_cents": 0, "memory_gb": billing.ClassMemoryGB(p.Class)}
	}
	if x.latest != nil {
		// disk_used_bytes is the thin volume's allocated blocks, which keep
		// a deleted file's blocks until the guest's daily fstrim (I-585),
		// and are what the plan's disk counts; root_* is the
		// guest's root filesystem, what its writes run out of (I-567).
		out["disk_used_bytes"] = x.latest.DiskUsed
		if x.latest.RootSize > 0 {
			out["root_used_bytes"] = x.latest.RootUsed
			out["root_size_bytes"] = x.latest.RootSize
		}
		agents := []map[string]string{}
		for _, a := range x.latest.Agents {
			agents = append(agents, map[string]string{"agent": a["agent"], "window": a["window"], "state": a["state"]})
		}
		var guestdOK any = x.latest.GuestdOK
		if sampleBeforeStart(p, x.latest) {
			guestdOK = nil // says nothing about the guestd running now (I-225)
		}
		out["signals"] = map[string]any{"ssh_sessions": x.latest.SSHSessions, "tmux_clients": x.latest.TmuxClients, "agents": agents,
			"docker_containers": x.latest.DockerContainers, "guestd_ok": guestdOK, "sampled_at": x.latest.TS, "gateway_sessions": s.sessions.Count(ctx, p.ID)}
	}
	return out, nil
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	projects, err := store.ListUserProjects(r.Context(), s.d.Pool, u.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(projects))
	for i := range projects {
		j, err := s.projectJSON(r.Context(), &projects[i], u)
		if err != nil {
			return err
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// countsTowardLimit is the SQL condition for a project that counts toward
// the account's project cap (billing.ProjectCap, I-569): running or
// stopped, not destroyed, and not being destroyed either, so `repose rm`
// frees the slot at once (DECISIONS I-300). A project in error after a
// failed destroy still counts: its volume is still on the host. The name
// and remote stay taken until the destroy finishes (the unique indexes on
// live rows).
const countsTowardLimit = "destroyed_at is null and state <> 'destroying'"

func (s *Server) userProject(r *http.Request) (*store.Project, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return store.GetUserProject(r.Context(), s.d.Pool, userFrom(r.Context()).ID, id)
}

// projectLimitError is the 400 a create, restore or fork past the
// account's project cap answers (api.md POST /projects/:id/fork, I-569).
// detail.reason lets the CLI say it in its own words; the message is the
// whole sentence for a client that prints it as it is.
func projectLimitError(have, limit, requested int) error {
	detail := map[string]any{"reason": "project_limit", "limit": limit, "projects": have}
	if requested <= 1 {
		return withDetail(errf("invalid", "you have %d of the %d projects an account can have, running or stopped; destroy one first", have, limit), detail)
	}
	detail["requested"] = requested
	return withDetail(errf("invalid", "you have %d of the %d projects an account can have, running or stopped, and %d more would make %d; destroy some first", have, limit, requested, have+requested), detail)
}

// abuseGate refuses to start a project on hold after three miner stops in
// 24 hours, until an operator clears it (DECISIONS I-239).
func (s *Server) abuseGate(ctx context.Context, p *store.Project) error {
	h, err := abuse.StartHold(ctx, s.d.Pool, p.ID)
	if err != nil {
		return err
	}
	if h == nil {
		return nil
	}
	return withDetail(errf("forbidden", "%s", abuse.HoldMessage(p.Slug, h)), map[string]any{"reason": "abuse_hold"})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	ctx := r.Context()
	var body struct {
		Name      string  `json:"name"`
		RemoteURL *string `json:"remote_url"`
		Class     string  `json:"class"`
		TZ        *string `json:"tz"`
		Agent     *string `json:"agent_default"`
		// ExpiresIn makes the project temporary (DECISIONS I-347).
		ExpiresIn *int64 `json:"expires_in_s"`
		// PersonalOptOut keeps the account's machine.nix off this
		// machine (repose run --no-personal, DECISIONS I-490).
		PersonalOptOut bool `json:"personal_opt_out"`
		// Multiplexer is tmux (the default) or herdr (DECISIONS I-502).
		Multiplexer *string `json:"multiplexer"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	mux := multiplexer.Tmux
	if body.Multiplexer != nil {
		if err := checkMultiplexer(*body.Multiplexer); err != nil {
			return err
		}
		mux = *body.Multiplexer
	}
	var expiresAt *time.Time
	if body.ExpiresIn != nil {
		d := time.Duration(*body.ExpiresIn) * time.Second
		if d < temp.MinLifetime || d > temp.MaxLifetime {
			return errf("invalid", "expires_in_s must be between %d and %d", int64(temp.MinLifetime/time.Second), int64(temp.MaxLifetime/time.Second))
		}
		if body.RemoteURL != nil && *body.RemoteURL != "" {
			return errf("invalid", "a temporary project has no remote_url")
		}
		body.RemoteURL = nil
		t := time.Now().Add(d)
		expiresAt = &t
	}
	if !nameRe.MatchString(body.Name) {
		return errf("invalid", "name must match [A-Za-z0-9._-]{1,64}")
	}
	if body.Class == "" {
		body.Class = "large"
	}
	if !scheduler.ValidClass(body.Class) {
		return errf("invalid", "class must be small, large or xl")
	}
	slug := Slug(body.Name)
	if slug == "" {
		return errf("invalid", "name has no usable characters for a slug")
	}
	if body.TZ != nil {
		if _, err := time.LoadLocation(*body.TZ); err != nil {
			return errf("invalid", "tz is not an IANA zone name")
		}
	}
	agent := "claude"
	if body.Agent != nil {
		agent = *body.Agent
	}
	if mux == multiplexer.Herdr {
		// The new machine's first build takes the newest base.
		if err := herdrGate(ctx, s.d.Pool, slug, nil, true); err != nil {
			return err
		}
	}
	// The compute gate (I-289): a plan, its memory for this class and its
	// disk for the new volume. No waitlist gate here since I-290: checkout
	// is where the seats question is answered.
	if err := s.gate(r, u, billing.Request{Class: body.Class, Disk: true, AddHeldBytes: billing.NewProjectHeldBytes, VolumeBytes: scheduler.DefaultVolume(body.Class)}); err != nil {
		return err
	}
	if u.CancelledAt != nil {
		return errf("forbidden", "account is cancelled")
	}
	limits, err := s.limits(r, u)
	if err != nil {
		return err
	}
	pid := store.NewID()
	rid := store.NewID()
	var (
		opID uuid.UUID
		p    *store.Project
	)
	err = db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		// The project count is checked under a row lock on the user so two
		// creates cannot both pass.
		var count int
		if err := tx.QueryRow(ctx, "select count(*) from projects where user_id = (select id from users where id = $1 for update) and "+countsTowardLimit, u.ID).Scan(&count); err != nil {
			return err
		}
		if count >= limits.Projects {
			return projectLimitError(count, limits.Projects, 1)
		}
		_, err := tx.Exec(ctx, `insert into projects (id, user_id, name, slug, remote_url, class, state, volume_bytes, tz, agent_default, config_revision_id, expires_at, personal_opt_out, multiplexer, disk_held_bytes) values ($1, $2, $3, $4, $5, $6, 'creating', $7, $8, $9, $10, $11, $12, $13, $14)`,
			pid, u.ID, body.Name, slug, body.RemoteURL, body.Class, scheduler.DefaultVolume(body.Class), body.TZ, agent, rid, expiresAt, body.PersonalOptOut, mux, billing.NewProjectHeldBytes)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				if strings.Contains(pgErr.ConstraintName, "remote") {
					return errf("conflict", "a project for this remote already exists")
				}
				return errf("conflict", "a project named %s already exists", slug)
			}
			return err
		}
		// The first revision carries the account's machine.nix (I-490);
		// a create the host has no closure for comes up on the project
		// layer first and applies this one right after (deferPersonal).
		layer := store.PersonalLayer{OptOut: body.PersonalOptOut}
		if !body.PersonalOptOut {
			if layer, err = store.PersonalFor(ctx, tx, &store.Project{UserID: u.ID}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status, personal, personal_revision_id, personal_opt_out) values ($1, $2, $3, 'building', $4, $5, $6)", rid, pid, DefaultFragment, layer.Text, layer.RevisionID, layer.OptOut); err != nil {
			return err
		}
		if opID, err = s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}, false); err != nil {
			return err
		}
		// Read the row inside the transaction: once it commits and the
		// engine is kicked, the op can move the project to "building"
		// before a read on the pool sees it, and the 201 body would show a
		// state the caller never asked for (CI, 2026-09-20).
		p, err = store.GetProject(ctx, tx, pid)
		return err
	})
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	obs.Logger(ctx, s.d.Log).Info("project created", "event", "project_create", "project_id", pid.String(), "class", body.Class, "temporary", expiresAt != nil, "multiplexer", mux)
	j, err := s.projectJSON(ctx, p, u)
	if err != nil {
		return err
	}
	j["op_id"] = opID
	writeJSON(w, http.StatusCreated, j)
	return nil
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	j, err := s.projectJSON(r.Context(), p, userFrom(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, j)
	return nil
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		Class *string `json:"class"`
		Hold  *bool   `json:"hold_base_updates"`
		Agent *string `json:"agent_default"`
		// TZ is the laptop's zone, sent by the CLI when it differs from
		// the project's, so the next start's SetupProject writes the zone
		// the CLI already put in the running guest (I-198).
		TZ *string `json:"tz"`
		// ExpiresAt may only be null: `repose keep` makes a temporary
		// project a normal one (DECISIONS I-347). Raw, so an absent field
		// and an explicit null differ.
		ExpiresAt json.RawMessage `json:"expires_at"`
		// PersonalOptOut turns the account's machine.nix off (true) or
		// back on (false) for this machine, with a rebuild (I-490).
		PersonalOptOut *bool `json:"personal_opt_out"`
		// Multiplexer changes what the next start runs (I-502).
		Multiplexer *string `json:"multiplexer"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	ctx := r.Context()
	if body.Multiplexer != nil {
		// Checked before any field is written, so a refusal here
		// changes nothing.
		if err := checkMultiplexer(*body.Multiplexer); err != nil {
			return err
		}
		if p.State == "destroying" {
			return errf("conflict", "%s is being destroyed", p.Slug)
		}
		if *body.Multiplexer == multiplexer.Herdr {
			if err := herdrGate(ctx, s.d.Pool, p.Slug, p.BaseVersion, false); err != nil {
				return err
			}
		}
	}
	keep := false
	if len(body.ExpiresAt) > 0 {
		if strings.TrimSpace(string(body.ExpiresAt)) != "null" {
			return errf("invalid", "expires_at can only be set to null, which keeps a temporary project")
		}
		keep = true
	}
	if body.TZ != nil {
		if _, err := time.LoadLocation(*body.TZ); err != nil || *body.TZ == "" || strings.ContainsAny(*body.TZ, "\n\r") {
			return errf("invalid", "tz is not an IANA zone name")
		}
	}
	if body.Class != nil {
		if !scheduler.ValidClass(*body.Class) {
			return errf("invalid", "class must be small, large or xl")
		}
		if p.State != "stopped" {
			return errf("conflict", "changing the class requires the project to be stopped")
		}
		if billing.ClassMemoryGB(*body.Class) > billing.ClassMemoryGB(p.Class) {
			// A bigger class has to fit the plan's memory beside what runs
			// now (an xl needs Pro); the stopped project itself holds none.
			if err := s.gate(r, userFrom(ctx), billing.Request{Class: *body.Class, Project: p.ID}); err != nil {
				return err
			}
		}
	}
	// Every check is above and the multiplexer is the first write: its
	// update is guarded by state, so a destroy accepted since p was read
	// refuses the request before any field of it is stored.
	if body.Multiplexer != nil && *body.Multiplexer != multiplexer.Normalize(p.Multiplexer) {
		// Stored now; project_json carries it to the guest at the next
		// start, never sooner (I-502).
		tag, err := s.d.Pool.Exec(ctx, "update projects set multiplexer = $2 where id = $1 and state <> 'destroying' and destroyed_at is null", p.ID, *body.Multiplexer)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf("conflict", "%s is being destroyed", p.Slug)
		}
		obs.Logger(ctx, s.d.Log).Info("multiplexer changed", "event", "multiplexer_set", "project_id", p.ID.String(), "multiplexer", *body.Multiplexer)
	}
	if body.Class != nil {
		if _, err := s.d.Pool.Exec(ctx, "update projects set class = $2 where id = $1 and state = 'stopped'", p.ID, *body.Class); err != nil {
			return err
		}
	}
	if body.Hold != nil {
		if _, err := s.d.Pool.Exec(ctx, "update projects set hold_base_updates = $2 where id = $1", p.ID, *body.Hold); err != nil {
			return err
		}
	}
	if body.Agent != nil {
		if _, err := s.d.Pool.Exec(ctx, "update projects set agent_default = $2 where id = $1", p.ID, *body.Agent); err != nil {
			return err
		}
	}
	if body.TZ != nil {
		if _, err := s.d.Pool.Exec(ctx, "update projects set tz = $2 where id = $1", p.ID, *body.TZ); err != nil {
			return err
		}
	}
	if body.PersonalOptOut != nil {
		if err := s.setPersonalOptOut(ctx, p, *body.PersonalOptOut); err != nil {
			return err
		}
	}
	if keep && p.ExpiresAt != nil {
		// Under the row's state: once the reaper (or a DELETE) has marked
		// it destroying, the destroy is under way and keep is too late.
		tag, err := s.d.Pool.Exec(ctx, "update projects set expires_at = null where id = $1 and state <> 'destroying' and destroyed_at is null", p.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf("conflict", "%s is already being destroyed", p.Slug)
		}
		obs.Logger(ctx, s.d.Log).Info("temporary project kept", "event", "temp_keep", "project_id", p.ID.String())
	}
	fresh, err := store.GetProject(ctx, s.d.Pool, p.ID)
	if err != nil {
		return err
	}
	j, err := s.projectJSON(ctx, fresh, userFrom(ctx))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, j)
	return nil
}

func (s *Server) enqueue(ctx context.Context, n ops.NewOp, allowQueue bool) (uuid.UUID, error) {
	var id uuid.UUID
	err := db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		var err error
		id, err = s.d.Engine.Enqueue(ctx, tx, n, allowQueue)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	s.d.Engine.Kick()
	return id, nil
}

func (s *Server) destroyProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	pid := p.ID
	// A repeated DELETE while the destroy runs answers with that op, so a
	// client that lost the first response can still wait on it (I-156).
	open, err := store.OpenOpsForProject(r.Context(), s.d.Pool, pid)
	if err != nil {
		return err
	}
	for _, o := range open {
		if o.Kind == ops.KindDestroy {
			writeJSON(w, http.StatusAccepted, map[string]any{"op_id": o.ID, "state": o.State})
			return nil
		}
	}
	// The project reads `destroying` from the moment the destroy is
	// accepted, in the same transaction, because the CLI returns right
	// away (I-166) and the next `repose ls` must not show it running.
	var id uuid.UUID
	ctx := r.Context()
	err = db.InTx(ctx, s.d.Pool, func(tx db.Tx) error {
		var err error
		if id, err = s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindDestroy, ProjectID: &pid, Phases: ops.PlanDestroy(p)}, false); err != nil {
			return err
		}
		return store.SetProjectState(ctx, tx, pid, "destroying")
	})
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	obs.Logger(ctx, s.d.Log).Info("project destroy accepted", "event", "guest_destroy", "project_id", pid.String(), "op_id", id.String())
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "state": "pending"})
	return nil
}

func (s *Server) startProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	u := userFrom(r.Context())
	if err := s.gate(r, u, billing.Request{Class: p.Class, Project: p.ID}); err != nil {
		return err
	}
	if err := s.abuseGate(r.Context(), p); err != nil {
		return err
	}
	// A project in error, or one running whose guestd stopped answering,
	// is restarted rather than started: stop the unit, boot it again on its
	// newest built revision (I-157). `repose start` is the user's recovery.
	restart := false
	switch p.State {
	case "running":
		dead, err := s.guestdDead(r.Context(), p)
		if err != nil {
			return err
		}
		if !dead {
			return errf("conflict", "%s is already %s", p.Slug, p.State)
		}
		restart = true
	case "starting":
		return errf("conflict", "%s is already %s", p.Slug, p.State)
	case "error":
		restart = true
	case "stopped":
	default:
		return errf("conflict", "%s is %s; wait for it to settle", p.Slug, p.State)
	}
	// A restore that failed in its build or restore phase leaves the
	// project with no usable volume: the old one was destroyed first, the
	// new one is incomplete (one that failed in destroy_guest left the old
	// guest as it was and is not refused). A start would boot an empty or half-written volume,
	// so the way back is another restore (DECISIONS I-461).
	if p.State == "error" {
		unfinished, err := ops.UnfinishedRestore(r.Context(), s.d.Pool, p.ID)
		if err != nil {
			return err
		}
		if unfinished {
			return withDetail(errf("conflict", "%s has no usable volume because the restore into it did not finish; restore a snapshot into it again, or remove it with `repose rm %s`", p.Slug, p.Slug), map[string]any{"reason": "restore_unfinished"})
		}
	}
	if p.GuestID == nil {
		// A create that failed before CreateGuest (no host with capacity,
		// a failed build) left the project in error with no guest; a
		// restart has nothing to stop or boot, and nothing else could
		// create it, so `repose start`, `repose run` and the dashboard
		// went on answering "create it first" (dogfood 2026-10-01,
		// I-406). The create runs again; the placement is redone when
		// the host it had cannot take it.
		replace := false
		if p.HostID != nil {
			h, err := store.GetHost(r.Context(), s.d.Pool, *p.HostID)
			replace = err != nil || h.State != "ready" || h.Draining
		}
		pid := p.ID
		var id uuid.UUID
		err := db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
			var err error
			if id, err = s.d.Engine.Enqueue(r.Context(), tx, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()}, false); err != nil {
				return err
			}
			// "creating", as a fresh create reads, so every list stops
			// showing the old error the moment the create is accepted.
			_, err = tx.Exec(r.Context(), "update projects set state = 'creating', host_id = case when $2 then null else host_id end where id = $1 and guest_id is null", pid, replace)
			return err
		})
		if err != nil {
			return err
		}
		s.d.Engine.Kick()
		writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "restart": false, "create": true})
		return nil
	}
	if p.HostID != nil {
		h, err := store.GetHost(r.Context(), s.d.Pool, *p.HostID)
		if err == nil && (h.State == "unreachable" || h.State == "retired" || h.State == "lost") {
			return withDetail(errf("conflict", "%s's host is %s; restore its latest snapshot onto another host", p.Slug, h.State), map[string]any{"host_state": h.State})
		}
	}
	pending, err := ops.PendingRevision(r.Context(), s.d.Pool, p)
	if err != nil {
		return err
	}
	pid := p.ID
	n := ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStart(pending)}
	if restart {
		n.Phases, n.Params = ops.PlanRestart(pending), ops.RestartParams()
	}
	var id uuid.UUID
	err = db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		// A personal change queued a build for this stopped machine
		// (I-490): the start goes first, on what the machine has, and the
		// build follows it and switches in place.
		yielded, _, ok, err := s.d.Engine.YieldPersonalBuilds(r.Context(), tx, pid, "start")
		if err != nil {
			return err
		}
		if id, err = s.d.Engine.Enqueue(r.Context(), tx, n, ok); err != nil {
			return err
		}
		return s.d.Engine.RequeuePersonalBuilds(r.Context(), tx, pid, yielded)
	})
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "restart": restart})
	return nil
}

// guestdStaleAfter bounds how old a guestd_ok=false sample may be and still
// count: hosts sample every minute, so five minutes is several samples.
const guestdStaleAfter = 5 * time.Minute

// guestdDead reports whether the newest sample of a running project says
// its guestd is not answering.
func (s *Server) guestdDead(ctx context.Context, p *store.Project) (bool, error) {
	l, ok, err := meter.LatestSample(ctx, s.d.Pool, p.ID)
	if err != nil || !ok {
		return false, err
	}
	return l.State == "running" && !l.GuestdOK && time.Since(l.TS) < guestdStaleAfter && !sampleBeforeStart(p, l), nil
}

// sampleBeforeStart reports whether l was taken before the project's
// guest last became running: a sample from a guest being stopped (its
// guestd already shut down, the state not yet stopping) is the newest
// until the host's next minute tick, and read as the new guest's word it
// said guestd was dead right after every start, so `repose run` asked
// for a restart and `repose status` warned (I-225).
func sampleBeforeStart(p *store.Project, l *meter.Latest) bool {
	return p.StartedAt != nil && l.TS.Before(*p.StartedAt)
}

func (s *Server) stopProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	body := struct {
		Snapshot *bool `json:"snapshot"`
	}{}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	snapshot := body.Snapshot == nil || *body.Snapshot
	switch p.State {
	case "running", "starting", "error":
	case "stopped":
		return errf("conflict", "%s is already stopped", p.Slug)
	default:
		return errf("conflict", "%s is %s; wait for it to settle", p.Slug, p.State)
	}
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Params: map[string]any{"snapshot": snapshot}, Phases: ops.PlanStop()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id})
	return nil
}

func opJSON(r *http.Request, op *store.Op) map[string]any {
	out := map[string]any{"op_id": op.ID, "kind": op.Kind, "state": op.State, "created_at": op.CreatedAt, "finished_at": op.FinishedAt, "reboot_required": op.RebootRequired}
	if op.Error != nil {
		out["error"] = op.Error
	}
	if op.Result != nil {
		out["result"] = op.Result
	}
	if op.Kind == ops.KindBuild || op.Kind == ops.KindCreate {
		out["log_url"] = "/v1/projects/" + r.PathValue("id") + "/ops/" + op.ID.String() + "/log"
	}
	return out
}

func (s *Server) userOp(r *http.Request) (*store.Op, error) {
	_, op, err := s.userOpProject(r)
	return op, err
}

// userOpProject is userOp plus the project it belongs to.
func (s *Server) userOpProject(r *http.Request) (*store.Project, *store.Op, error) {
	p, err := s.userProjectAny(r)
	if err != nil {
		return nil, nil, err
	}
	opID, err := pathID(r, "op_id")
	if err != nil {
		return nil, nil, err
	}
	op, err := store.GetOp(r.Context(), s.d.Pool, opID)
	if err != nil {
		return nil, nil, err
	}
	if op.ProjectID == nil || *op.ProjectID != p.ID {
		return nil, nil, db.ErrNotFound
	}
	return p, op, nil
}

func (s *Server) userProjectAny(r *http.Request) (*store.Project, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return store.GetUserProjectAny(r.Context(), s.d.Pool, userFrom(r.Context()).ID, id)
}

func (s *Server) resizeProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	var body struct {
		VolumeBytes int64 `json:"volume_bytes"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.VolumeBytes <= p.VolumeBytes {
		return withDetail(errf("invalid", "volumes only grow; current size is %d bytes", p.VolumeBytes), map[string]any{"volume_bytes": p.VolumeBytes})
	}
	if body.VolumeBytes > 2<<40 {
		return errf("invalid", "volume may not exceed 2 TB")
	}
	if p.GuestID == nil {
		return errf("conflict", "%s has no guest yet", p.Slug)
	}
	// Growing a volume raises its ceiling and holds nothing more at once;
	// it is refused past the plan's disk, or while the projects already
	// hold more than it (I-585).
	if err := s.gate(r, userFrom(r.Context()), billing.Request{Disk: true, VolumeBytes: body.VolumeBytes, Project: p.ID}); err != nil {
		return err
	}
	pid := p.ID
	id, err := s.enqueue(r.Context(), ops.NewOp{Kind: ops.KindResize, ProjectID: &pid, Params: map[string]any{"volume_bytes": float64(body.VolumeBytes)}, Phases: ops.PlanResize()}, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id})
	return nil
}

func (s *Server) projectRoute(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	out := map[string]any{"host_id": p.HostID, "guest_ip": nil, "state": p.State, "host_unreachable": p.HostUnreachable}
	if p.GuestIP != nil {
		out["guest_ip"] = p.GuestIP.String()
	}
	if p.HostID != nil {
		if h, err := store.GetHost(r.Context(), s.d.Pool, *p.HostID); err == nil {
			out["host_name"] = h.Name
			out["host_state"] = h.State
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
