package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/heracraft/repose/internal/api/abuse"
	"github.com/heracraft/repose/internal/api/answering"
	"github.com/heracraft/repose/internal/api/idle"
	"github.com/heracraft/repose/internal/api/lifecycle"
	"github.com/heracraft/repose/internal/api/meter"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/api/temp"
	"github.com/heracraft/repose/internal/api/webhooks"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/checkoutname"
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
		// Whether this machine is kept out of the rest rule (I-820).
		"keep_awake": p.KeepAwake,
		// What the next start runs (I-502); a running machine may still
		// run the other one until it stops.
		"multiplexer": multiplexer.Normalize(p.Multiplexer),
		"placement":   store.NormalizePlacement(p.Placement),
		// What the machine boots (I-1462), and the erofs base its next
		// start boots, null when none.
		"guest_runtime":        store.NormalizeRuntime(p.GuestRuntime),
		"pending_base_version": p.PendingBaseVersion,
		// The checkout's name under /home/dev (I-1610): "" for none,
		// null until a CLI has said.
		"checkout": p.Checkout,
	}
	// onShared: the project's host is one of the account's own whose
	// hostd shares it (I-1630).
	onShared := false
	if p.HostID != nil {
		// The name the account gave its host, beside the id (I-1392): a
		// script reading --json got an opaque uuid and never the name
		// its owner typed, which nothing could work around. A host
		// repose runs stays an id: that one is a platform internal
		// (I-616), and its name is not the user's.
		var name string
		err := s.d.Pool.QueryRow(ctx, "select name, $3 = any(features) from hosts where id = $1 and owner_account_id = $2", *p.HostID, u.ID, scheduler.SharedFeature).Scan(&name, &onShared)
		if err == nil {
			out["host_name"] = name
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	sized, err := s.projectSized(ctx, p, onShared)
	if err != nil {
		return nil, err
	}
	out["sized"] = sized
	if p.ExpiresAt != nil {
		// A temporary machine (DECISIONS I-347): destroyed with no
		// snapshot once this has passed, unless `repose keep` clears it.
		out["expires_at"] = *p.ExpiresAt
	}
	if p.GuestIP != nil {
		out["guest_ip"] = p.GuestIP.String()
	}
	if p.State == "running" && p.RestingSince != nil {
		// A machine whose vCPUs are stopped and whose memory is resident:
		// every process survives and the first thing that reaches it
		// wakes it (I-818). Read from the projects row, not from a
		// samples query, so a hundred-project list stays one query per
		// project; and with no 24-hour gate like `idle`'s, because a
		// pause happens about fifteen minutes in and the marker must not
		// hide for a machine's first day.
		out["resting"] = map[string]any{"since": *p.RestingSince}
	}
	if p.State == "running" && p.VMSize != nil {
		// What the machine's VM got and what it gets with its host to
		// itself, from a host that sizes each start (I-1680): a start
		// sized below the full size is a slower machine, and the clients
		// say so.
		v := p.VMSize
		out["vm"] = map[string]any{"vcpus": v.VCPUs, "mem_bytes": v.MemMiB << 20, "full_vcpus": v.FullVCPUs, "full_mem_bytes": v.FullMemMiB << 20, "sized_down": v.SizedDown()}
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
		if p.State == "running" && x.latest.MemUsed > 0 && !sampleBeforeStart(p, x.latest) {
			// What the machine uses now, as the guest sees it (I-1632):
			// on a host the user owns it is what the clients show in
			// place of a size.
			out["mem_used_bytes"] = x.latest.MemUsed
		}
		agents := []map[string]string{}
		for _, a := range x.latest.Agents {
			// agent_id and terminal (I-1676); a row from an older guestd
			// gets them derived from its window.
			a = meter.AgentRow(a)
			agents = append(agents, map[string]string{"agent": a["agent"], "window": a["window"], "state": a["state"], "agent_id": a["agent_id"], "terminal": a["terminal"]})
		}
		var guestdOK any = x.latest.GuestdOK
		if sampleBeforeStart(p, x.latest) {
			guestdOK = nil // says nothing about the guestd running now (I-225)
		}
		out["signals"] = map[string]any{"ssh_sessions": x.latest.SSHSessions, "tmux_clients": x.latest.TmuxClients, "agents": agents,
			"docker_containers": x.latest.DockerContainers, "guestd_ok": guestdOK, "sampled_at": x.latest.TS, "gateway_sessions": s.sessions.Count(ctx, p.ID)}
		if r, ok := out["resting"].(map[string]any); ok && x.latest.Resting && x.latest.WaitsFor != "" && !sampleBeforeStart(p, x.latest) {
			// A machine resting as a save on a Mac whose screen is locked
			// (or with nobody logged in) wakes once the user unlocks it or
			// logs in (I-1682): its memory is kept.
			r["waits_for"] = x.latest.WaitsFor
		}
		parked := p.State == "running" && x.latest.ParkedWaitingSince != nil && !sampleBeforeStart(p, x.latest)
		if parked {
			// Parked on a Mac, waiting for its user to log in (I-1672):
			// its memory is kept and it runs again at the login. Silent
			// on purpose, so never not_answering.
			// waits_for is "unlock" when the Mac's user is logged in with
			// the screen locked (I-1682), else "login" as before.
			waits := "login"
			if x.latest.WaitsFor == "unlock" {
				waits = "unlock"
			}
			out["parked"] = map[string]any{"since": x.latest.ParkedWaitingSince.UTC(), "waits_for": waits}
		}
		if !parked && !x.latest.GuestdOK && !x.latest.Resting && !sampleBeforeStart(p, x.latest) {
			// Its guestd has not answered for about three minutes
			// (I-1652): read only when the newest sample already says
			// so, so a list of answering machines costs nothing more.
			since, silent, err := answering.Project(ctx, s.d.Pool, p, time.Now())
			if err != nil {
				return nil, err
			}
			if silent {
				out["not_answering"] = map[string]any{"since": since}
			}
		}
	}
	return out, nil
}

// projectSized is the Project's `sized` (DECISIONS I-1632): whether its
// class is a size. A machine on a host its account owns whose hostd
// shares it (scheduler.SharedFeature) has no size (I-1630); so does one
// not placed yet that will be placed there, a project asking for `own`
// in an account that owns such a host that is not retired or lost. An
// owned host whose hostd does not share yet boots each machine at its
// class, so a machine there keeps its size. onShared is whether the
// project's host is one of the account's that shares.
func (s *Server) projectSized(ctx context.Context, p *store.Project, onShared bool) (bool, error) {
	if onShared {
		return false, nil
	}
	if p.HostID != nil || store.NormalizePlacement(p.Placement) == "hosted" {
		return true, nil
	}
	var shares bool
	err := s.d.Pool.QueryRow(ctx, `select exists (select 1 from hosts
		 where owner_account_id = $1 and state not in ('retired', 'lost') and $2 = any(features))`, p.UserID, scheduler.SharedFeature).Scan(&shares)
	return !shares, err
}

// createsUnsized reports whether a create with this placement and pin
// lands on a host the account owns whose hostd shares it (I-1654): the
// pinned host when one is named, else any live one of the account's,
// which is where `own` placement puts it.
func (s *Server) createsUnsized(ctx context.Context, userID uuid.UUID, placement string, pin uuid.UUID) (bool, error) {
	if store.NormalizePlacement(placement) == store.PlacementHosted {
		return false, nil
	}
	var shares bool
	err := s.d.Pool.QueryRow(ctx, `select exists (select 1 from hosts
		 where owner_account_id = $1 and state not in ('retired', 'lost') and $2 = any(features)
		   and ($3::uuid is null or id = $3))`, userID, scheduler.SharedFeature, nullUUID(pin)).Scan(&shares)
	return shares, err
}

// createsOnOwnHost reports whether a create with this placement and pin
// lands on a host the account owns (DECISIONS I-1704): `own` placement
// takes such a host whenever the account has one that is not retired or
// lost (scheduler.ownedSQL), and a pin names one. The billing gate asks
// it, because such a machine counts nothing against the plan.
func (s *Server) createsOnOwnHost(ctx context.Context, userID uuid.UUID, placement string, pin uuid.UUID) (bool, error) {
	if store.NormalizePlacement(placement) == store.PlacementHosted {
		return false, nil
	}
	if pin != uuid.Nil {
		return true, nil
	}
	var owns bool
	err := s.d.Pool.QueryRow(ctx, `select exists (select 1 from hosts
		 where owner_account_id = $1 and state not in ('retired', 'lost'))`, userID).Scan(&owns)
	return owns, err
}

// hostOwnedBy reports whether host is one the account owns; nil is a
// host of repose's or none.
func (s *Server) hostOwnedBy(ctx context.Context, host *uuid.UUID, userID uuid.UUID) (bool, error) {
	if host == nil {
		return false, nil
	}
	var own bool
	err := s.d.Pool.QueryRow(ctx, "select exists (select 1 from hosts where id = $1 and owner_account_id = $2)", *host, userID).Scan(&own)
	return own, err
}

// nullUUID is id, or nil for uuid.Nil, as a query argument.
func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	projects, err := store.ListUserProjects(r.Context(), s.d.Pool, u.ID)
	if err != nil {
		return err
	}
	// A project-bound token sees its one project (I-1318): the list is
	// how a CLI turns a name into an id, so it is not refused.
	if t := tokenFrom(r.Context()); t != nil && t.ProjectID != nil {
		kept := projects[:0]
		for i := range projects {
			if projects[i].ID == *t.ProjectID {
				kept = append(kept, projects[i])
			}
		}
		projects = kept
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

// notCreated is the 404 for a project whose create the host refused for
// room (DECISIONS I-1648): it was removed at once, and a page opened on it
// says so and why, in the op's own sentence, rather than "not found".
// Any other missing project keeps err.
func (s *Server) notCreated(r *http.Request, err error) error {
	p, aerr := s.userProjectAny(r)
	if aerr != nil || p.DestroyedAt == nil || p.LastError == nil {
		return err
	}
	why, ok := strings.CutPrefix(*p.LastError, ops.CreateRefusedPrefix)
	if !ok {
		return err
	}
	return withDetail(errf("not_found", "%s was not created: %s", p.Slug, why), map[string]any{"reason": "create_refused"})
}

func (s *Server) userProject(r *http.Request) (*store.Project, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return store.GetUserProject(r.Context(), s.d.Pool, userFrom(r.Context()).ID, id)
}

// projectLimitError is the 400 a create or a restore past the account's
// project cap answers (api.md POST /projects, I-569).
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

// createRuntime is the guest_runtime a create gets (DECISIONS I-1462):
// the environment's default, or what an exempt account asked for. Any
// other account asking for the other runtime is refused.
func (s *Server) createRuntime(ctx context.Context, u *store.User, asked *string) (string, error) {
	def, err := store.DefaultGuestRuntime(ctx, s.d.Pool)
	if err != nil {
		return "", err
	}
	if asked == nil {
		return def, nil
	}
	if *asked != store.RuntimeNix && *asked != store.RuntimeErofs {
		return "", withDetail(errf("invalid", "guest_runtime must be nix or erofs"), map[string]any{"reason": "guest_runtime"})
	}
	if *asked != def && u.BillingStatus != "exempt" {
		return "", withDetail(errf("forbidden", "this account cannot choose the guest runtime; new machines run %s", def), map[string]any{"reason": "guest_runtime"})
	}
	return *asked, nil
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
		// Placement is own (the default) or hosted (DECISIONS I-1366).
		Placement *string `json:"placement"`
		// Host names one host the account owns, by id or by name
		// (DECISIONS I-1584): that host or a refusal, never another.
		Host *string `json:"host"`
		// GuestRuntime is nix or erofs (DECISIONS I-1462); absent is the
		// environment's default.
		GuestRuntime *string `json:"guest_runtime"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	runtime, err := s.createRuntime(ctx, u, body.GuestRuntime)
	if err != nil {
		return err
	}
	placement := store.PlacementOwn
	if body.Placement != nil {
		if *body.Placement != store.PlacementOwn && *body.Placement != store.PlacementHosted {
			return withDetail(errf("invalid", "placement must be own or hosted"), map[string]any{"reason": "placement"})
		}
		placement = *body.Placement
	}
	pin, err := s.createHostPin(ctx, u, body.Host, placement)
	if err != nil {
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
		return errf("invalid", "class must be tiny, small, large or xl")
	}
	slug := Slug(body.Name)
	if slug == "" {
		return errf("invalid", "name has no usable characters for a slug")
	}
	// A machine on a host the account owns whose hostd shares it has no
	// size (I-1630, I-1632), and the class asked for changes nothing
	// there, its disk included (I-1654): it is made as `repose run`
	// without --size makes it. The class stays a field for older
	// clients, and means nothing on such a host.
	volume := scheduler.DefaultVolume(body.Class)
	if owned, err := s.createsUnsized(ctx, u.ID, placement, pin); err != nil {
		return err
	} else if owned {
		body.Class, volume = "large", scheduler.OwnedVolumeBytes
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
	if mux == multiplexer.Herdr && runtime == store.RuntimeNix {
		// The new machine's first build takes the newest base. An erofs
		// base ships herdr (I-1462), so the gate is the Nix bases'.
		if err := herdrGate(ctx, s.d.Pool, slug, nil, true); err != nil {
			return err
		}
	}
	// The compute gate (I-289): a plan, its memory for this class and its
	// disk for the new volume. No waitlist gate here since I-290: checkout
	// is where the seats question is answered.
	onOwn, err := s.createsOnOwnHost(ctx, u.ID, placement, pin)
	if err != nil {
		return err
	}
	if err := s.gate(r, u, billing.Request{Class: body.Class, Disk: true, AddHeldBytes: billing.NewProjectHeldBytes, VolumeBytes: volume, OwnHost: onOwn}); err != nil {
		return err
	}
	// A plan that sells no memory on repose's own hosts cannot ask for
	// one (DECISIONS I-1366): placement would refuse it as `capacity`,
	// which is a sentence about the fleet and no answer to a user who
	// asked for the fleet on purpose.
	if err := s.checkHostedAllowed(r, u, placement); err != nil {
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
	fragment := DefaultFragment
	if runtime == store.RuntimeErofs {
		fragment = "" // nothing is built from it (I-1462)
	}
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
		// Placement answers before the row is written (DECISIONS
		// I-1390): a refusal is this request's 503 and leaves nothing
		// behind. Until this the op placed the row afterwards, and a
		// customer whose own host had not registered yet stranded one
		// project in `error` per attempt. The pick is recorded on the
		// row in this transaction, which is PickHost's contract.
		held := billing.NewProjectHeldBytes
		hostID, err := s.d.Engine.PlaceNewOn(ctx, tx, &store.Project{ID: pid, UserID: u.ID, Class: body.Class,
			VolumeBytes: volume, Placement: placement, DiskHeldBytes: &held, GuestRuntime: runtime}, pin)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into projects (id, user_id, name, slug, remote_url, class, state, volume_bytes, tz, agent_default, config_revision_id, expires_at, personal_opt_out, multiplexer, placement, disk_held_bytes, host_id, guest_runtime) values ($1, $2, $3, $4, $5, $6, 'creating', $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			pid, u.ID, body.Name, slug, body.RemoteURL, body.Class, volume, body.TZ, agent, rid, expiresAt, body.PersonalOptOut, mux, placement, held, hostID, runtime)
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
		if !body.PersonalOptOut && runtime == store.RuntimeNix {
			// An erofs project never carries machine.nix (I-1462).
			if layer, err = store.PersonalFor(ctx, tx, &store.Project{UserID: u.ID}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "insert into config_revisions (id, project_id, fragment, status, personal, personal_revision_id, personal_opt_out) values ($1, $2, $3, 'building', $4, $5, $6)", rid, pid, fragment, layer.Text, layer.RevisionID, layer.OptOut); err != nil {
			return err
		}
		if opID, err = s.d.Engine.Enqueue(ctx, tx, ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreateFor(runtime)}, false); err != nil {
			return err
		}
		// Read the row inside the transaction: once it commits and the
		// engine is kicked, the op can move the project to "building"
		// before a read on the pool sees it, and the 201 body would show a
		// state the caller never asked for (CI, 2026-09-20).
		p, err = store.GetProject(ctx, tx, pid)
		return err
	})
	var refused *ops.PlacementRefusal
	if errors.As(err, &refused) {
		return withDetail(errf(refused.Code, "%s", refused.Message), refused.Detail)
	}
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

// createHostPin resolves POST /projects' `host` (DECISIONS I-1584): one
// host the account owns, by its id or by the name the account gave it,
// that is not retired or lost. Absent or empty is no pin (uuid.Nil),
// which is placement as it was. A host is the account's own hardware, so
// it goes with `own` only; with `hosted` it is a contradiction and is
// refused rather than one of the two being ignored. A name the account
// has no live host under is refused before anything is written, in the
// words the CLI used to print for itself.
func (s *Server) createHostPin(ctx context.Context, u *store.User, host *string, placement string) (uuid.UUID, error) {
	if host == nil || strings.TrimSpace(*host) == "" {
		return uuid.Nil, nil
	}
	want := strings.TrimSpace(*host)
	if placement == store.PlacementHosted {
		return uuid.Nil, withDetail(errf("invalid", "host names one of your hosts and placement hosted is repose's servers; send one of them"), map[string]any{"reason": "host"})
	}
	var h *store.Host
	if id, perr := uuid.Parse(want); perr == nil {
		got, err := store.GetHost(ctx, s.d.Pool, id)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return uuid.Nil, err
		}
		if got != nil && got.OwnerAccountID != nil && *got.OwnerAccountID == u.ID {
			h = got
		}
	}
	if h == nil {
		got, err := store.GetAccountHostByName(ctx, s.d.Pool, u.ID, want)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return uuid.Nil, err
		}
		h = got
	}
	if h == nil || h.State == "retired" || h.State == "lost" {
		return uuid.Nil, withDetail(errf("invalid", "you have no host called %s", want), map[string]any{"reason": "host", "host": want})
	}
	return h.ID, nil
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if errors.Is(err, db.ErrNotFound) {
		return s.notCreated(r, err)
	}
	if err != nil {
		return err
	}
	j, err := s.projectJSON(r.Context(), p, userFrom(r.Context()))
	if err != nil {
		return err
	}
	// The op in flight, on the single-project read only (DECISIONS
	// I-722). Until this, only the create's own 201 carried an op id, so
	// `repose run` on a checkout whose project already existed fell back
	// to polling states: `waitState` returns on any settled state, `error`
	// included, and the next statement started the machine — one user
	// action, two ops, and the create's error never read (teksafari-org,
	// 2026-10-13). With the id here the wait follows the op and reports
	// what it said. Ops are serialised per project, so the oldest open one
	// is the one running. The list path does not carry it: a query per
	// project is an N+1.
	if open, oerr := store.OpenOpsForProject(r.Context(), s.d.Pool, p.ID); oerr == nil && len(open) > 0 {
		j["op_id"] = open[0].ID
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
		// ExpiresIn moves a temporary project's expiry to now plus this
		// many seconds, within create's bounds: `repose keep PROJECT 3h`
		// (DECISIONS I-612).
		ExpiresIn *int64 `json:"expires_in_s"`
		// PersonalOptOut turns the account's machine.nix off (true) or
		// back on (false) for this machine, with a rebuild (I-490).
		PersonalOptOut *bool `json:"personal_opt_out"`
		// Multiplexer changes what the next start runs (I-502).
		Multiplexer *string `json:"multiplexer"`
		// KeepAwake keeps this machine out of the rest rule (I-820). It
		// takes effect at once: the api decides what rests, so nothing
		// has to be pushed to the machine, and a machine already resting
		// is woken here.
		KeepAwake *bool `json:"keep_awake"`
		// Checkout is the checkout's name under /home/dev, which the CLI
		// sends when it finds a different one on the machine (I-1610).
		Checkout *string `json:"checkout"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	ctx := r.Context()
	if body.Checkout != nil && !checkoutname.Valid(*body.Checkout) {
		return errf("invalid", "checkout must be a folder name of letters, digits, '.', '_' and '-', at most 100 characters, or empty")
	}
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
	if body.ExpiresIn != nil {
		if keep {
			return errf("invalid", "expires_at: null and expires_in_s cannot both be sent")
		}
		if d := time.Duration(*body.ExpiresIn) * time.Second; d < temp.MinLifetime || d > temp.MaxLifetime {
			return errf("invalid", "expires_in_s must be between %d and %d", int64(temp.MinLifetime/time.Second), int64(temp.MaxLifetime/time.Second))
		}
		if p.ExpiresAt == nil {
			return errf("invalid", "expires_in_s applies only to a temporary project")
		}
	}
	if body.TZ != nil {
		if _, err := time.LoadLocation(*body.TZ); err != nil || *body.TZ == "" || strings.ContainsAny(*body.TZ, "\n\r") {
			return errf("invalid", "tz is not an IANA zone name")
		}
	}
	if body.Class != nil {
		if !scheduler.ValidClass(*body.Class) {
			return errf("invalid", "class must be tiny, small, large or xl")
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
	if body.KeepAwake != nil {
		if _, err := s.d.Pool.Exec(ctx, "update projects set keep_awake = $2 where id = $1", p.ID, *body.KeepAwake); err != nil {
			return err
		}
		// Effective at once in both directions: the rule reads the column
		// on its next pass, and a machine already frozen when the user
		// turns the switch on is woken now rather than at whatever next
		// reaches it (I-820).
		if waker := s.userWake(); *body.KeepAwake && p.RestingSince != nil && waker != nil {
			if _, err := waker.Wake(ctx, p.ID, resting.ReasonKeepAwake); err != nil {
				obs.Logger(ctx, s.d.Log).Warn("keep-awake wake failed", "event", "guest_wake_fail", "project_id", p.ID.String(), "err", err.Error())
			}
		}
	}
	if body.Checkout != nil {
		// Never logged: the name comes from the user's laptop folder.
		if _, err := s.d.Pool.Exec(ctx, "update projects set checkout = $2 where id = $1", p.ID, *body.Checkout); err != nil {
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
	if body.ExpiresIn != nil {
		// Under the row's state, as keep: a project the reaper has marked
		// destroying is past saving.
		tag, err := s.d.Pool.Exec(ctx, "update projects set expires_at = now() + $2 * interval '1 second' where id = $1 and expires_at is not null and state <> 'destroying' and destroyed_at is null", p.ID, *body.ExpiresIn)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf("conflict", "%s is already being destroyed", p.Slug)
		}
		obs.Logger(ctx, s.d.Log).Info("temporary project extended", "event", "temp_extend", "project_id", p.ID.String(), "expires_in_s", *body.ExpiresIn)
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
			writeJSON(w, http.StatusAccepted, map[string]any{"op_id": o.ID, "state": o.State, "final_snapshot": s.finalSnapshot(r.Context(), p)})
			return nil
		}
	}
	// Taken before the state moves to `destroying`, because PlanDestroy
	// reads the project's state and the answer must describe the plan
	// that was enqueued (DECISIONS I-1418).
	snap := s.finalSnapshot(r.Context(), p)
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
		// The project's webhook routes go with it, and each token
		// becomes a tombstone: the URL answers 410 for 30 days and 404
		// after, and `repose restore` does not bring it back, because a
		// sender that obeyed the 410 has already let it go (I-1114).
		if err := webhooks.TombstoneProject(ctx, tx, pid); err != nil {
			return err
		}
		return store.SetProjectState(ctx, tx, pid, "destroying")
	})
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	obs.Logger(ctx, s.d.Log).Info("project destroy accepted", "event", "guest_destroy", "project_id", pid.String(), "op_id", id.String())
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": id, "state": "pending", "final_snapshot": snap})
	return nil
}

// finalSnapshot is whether this destroy will take one, which is what the
// CLI needs to know before it says so: it prints its line the moment
// this route answers, before any phase has run, and it promised a final
// snapshot for a machine that had none and could never get one (ENG-119,
// DECISIONS I-1418). Two reasons there may be no snapshot, and the
// answer is the same for both: a temporary project's plan does not carry
// the phase (I-347), and a host nobody expects back has every phase
// skipped (I-924).
func (s *Server) finalSnapshot(ctx context.Context, p *store.Project) bool {
	if !slices.Contains(ops.PlanDestroy(p), ops.PhaseSnapshot) {
		return false
	}
	if p.HostID == nil {
		return false
	}
	h, err := store.GetHost(ctx, s.d.Pool, *p.HostID)
	if err != nil || h == nil {
		// The host row is gone, so the host is (ops.Engine.hostGone reads
		// a missing row the same way).
		return err == nil && h != nil
	}
	return h.State != "lost" && h.State != "retired"
}

// wakeProject is POST /projects/:id/wake (DECISIONS I-1682):
// a resting machine is woken now, as the gateway's first connection would
// wake it. The CLI sends it the moment it reads a resting machine, so the
// wake runs beside the ssh handshake instead of after it. A machine that
// is not resting answers was_resting false at once.
func (s *Server) wakeProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	if p.State != "running" || p.RestingSince == nil {
		writeJSON(w, http.StatusOK, map[string]any{"was_resting": false, "wake_ms": 0})
		return nil
	}
	waker := s.userWake()
	if waker == nil {
		return errf("internal", "waking is not configured on this api")
	}
	res, err := waker.Wake(r.Context(), p.ID, "cli")
	if err != nil {
		return wakeFailed(p.Slug, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"was_resting": res.GetWasResting(), "wake_ms": res.GetWakeMs()})
	return nil
}

func (s *Server) startProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	u := userFrom(r.Context())
	// The checks and the op are lifecycle.Start's, which the resume after
	// a host reboot shares (DECISIONS I-645); it takes the project's row
	// lock before it counts the open ops.
	var res *lifecycle.Result
	err = db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		var err error
		res, err = lifecycle.Start(r.Context(), tx, lifecycle.Deps{Pool: s.d.Pool, Engine: s.d.Engine, Gate: s.d.Gate}, u, p.ID, lifecycle.Options{GuestdDead: s.guestdDead})
		return err
	})
	var ref *lifecycle.Refusal
	if errors.As(err, &ref) {
		e := errf(ref.Code, "%s", ref.Message)
		if ref.Detail != nil {
			e = withDetail(e, ref.Detail)
		}
		return e
	}
	if err != nil {
		return err
	}
	s.d.Engine.Kick()
	if res.Create {
		writeJSON(w, http.StatusAccepted, map[string]any{"op_id": res.OpID, "restart": false, "create": true})
		return nil
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": res.OpID, "restart": res.Restart})
	return nil
}

// guestdStaleAfter bounds how old a guestd_ok=false sample may be and still
// count: hosts sample every minute, so five minutes is several samples.
const guestdStaleAfter = 5 * time.Minute

// guestdDead reports whether the newest sample of a running project says
// its guestd is not answering.
//
// A resting sample never says so (DECISIONS I-818). hostd keeps
// guestd_ok true for a paused guest, because guestd is paused with its
// machine and not lost; this is the second guard, and it is the one that
// matters: guestdDead is what lifecycle.Start turns into restart=true,
// which is stop_guest plus start_guest. On a frozen machine `repose
// start` and the dashboard's Start would then reboot it and lose every
// process the pause was taken to keep.
func (s *Server) guestdDead(ctx context.Context, p *store.Project) (bool, error) {
	l, ok, err := meter.LatestSample(ctx, s.d.Pool, p.ID)
	if err != nil || !ok {
		return false, err
	}
	if l.Resting {
		return false, nil
	}
	if l.ParkedWaitingSince != nil {
		// Parked on a Mac, waiting for its user's login (I-1672): a
		// restart would throw away the memory it is parked to keep.
		return false, nil
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
		// The console tail of a failed boot is served by the logs route
		// (I-592), not with every read of the op; the boot's timings are
		// the operators' (I-707).
		res := make(map[string]any, len(op.Result))
		for k, v := range op.Result {
			if k != "console" && k != "boot" {
				res[k] = v
			}
		}
		out["result"] = res
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
