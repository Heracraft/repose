package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/scheduler"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
)

// The hosts a user owns, of docs/interfaces/api.md "Hosts you own"
// (DECISIONS I-1366). Three routes: list them, add one (which mints the
// single-use join token the machine registers with), remove one. The
// hosts repose runs appear on none of them and are the operator's
// (`repose-admin hosts`).

// hostNameRe is the name a user may give their own host: the same shape
// as a webhook route's or an API token's name, so one rule covers every
// name a user types into the api.
var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// SettingHostInstall is the one line a user runs on their own machine to
// install repose on it, with `%s` where the join token goes
// (`repose-admin hosts install-command`). A setting and not an
// environment variable for the reason `loki_url` is one (DECISIONS
// I-95): it names something outside this deployment -- the installer
// 19f ships -- and moving it must not be a redeploy of the api. Empty
// while nothing has been recorded, and then the api sends no command and
// the client shows the token and the docs page instead of a line it made
// up itself.
const SettingHostInstall = "host_install_command"

// hostJSON is one host as GET /hosts returns it. token is empty except
// on the POST answer, which is the only answer that carries it.
func hostJSON(h *store.Host, projects, running int, token string, expires any, install map[string]any) map[string]any {
	out := map[string]any{
		"id":               h.ID.String(),
		"name":             h.Name,
		"state":            h.State,
		"mem_bytes":        h.MemBytes,
		"vcpus":            h.VCPUs,
		"cores":            h.Cores,
		"projects":         projects,
		"running_projects": running,
		"created_at":       h.CreatedAt,
	}
	if h.Draining && h.State == "ready" {
		// `repose-admin hosts drain` sets the flag before the state
		// follows; a user reading "ready" about a host that is being
		// removed would be reading the wrong word.
		out["state"] = "draining"
	}
	if h.Provider != nil {
		out["provider"] = *h.Provider
	}
	// The architecture the host reported (`x86_64`, `aarch64`), kept in
	// hosts.arch since I-1448. Before it, this sliced hosts.nixos_system
	// on `-`, assuming a Nix system tuple; hostd actually stores
	// `readlink /run/current-system` there, so owned Macs emitted
	// `/nix/store/<hash>` (ENG-89). A non-path `x86_64-linux` value in
	// nixos_system stays accepted for one release; store paths are never
	// emitted.
	if arch := hostArch(h); arch != "" {
		out["arch"] = arch
	}
	if h.LastHeartbeatAt != nil {
		out["last_heartbeat_at"] = *h.LastHeartbeatAt
	}
	if h.RegisteredAt != nil {
		out["registered_at"] = *h.RegisteredAt
	}
	if token != "" {
		out["join_token"] = token
		out["join_token_expires_at"] = expires
		out["install"] = install
	}
	return out
}

var hostArchRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// hostArch returns the host's CPU architecture ("x86_64", "aarch64") or
// "" when unknown. Prefers hosts.arch (I-1448); falls back to the first
// half of a Nix system tuple ("aarch64-linux") in nixos_system only when
// it is not a store path and matches a plain architecture token (ENG-89).
func hostArch(h *store.Host) string {
	if h.Arch != nil {
		if a := strings.TrimSpace(*h.Arch); hostArchRe.MatchString(a) {
			return a
		}
	}
	if h.NixosSystem != nil && !strings.HasPrefix(*h.NixosSystem, "/") {
		if arch, _, ok := strings.Cut(*h.NixosSystem, "-"); ok && hostArchRe.MatchString(arch) {
			return arch
		}
	}
	return ""
}

// hostCounts is the account's live projects on the host and those of
// them in a state that holds memory, which is what the list shows.
func (s *Server) hostCounts(r *http.Request, hostID uuid.UUID) (projects, running int, err error) {
	err = s.d.Pool.QueryRow(r.Context(), `select count(*),
		    count(*) filter (where state in ('creating','building','starting','running','stopping','restoring'))
		  from projects where host_id = $1 and destroyed_at is null`, hostID).Scan(&projects, &running)
	return projects, running, err
}

// hostSnapshots counts the live snapshots kept on the host's disk, which
// removing the host deletes (DECISIONS I-1542): the host rm question
// names the number.
func (s *Server) hostSnapshots(r *http.Request, hostID uuid.UUID) (n int, err error) {
	err = s.d.Pool.QueryRow(r.Context(), `select count(*) from snapshots s
		  where s.host_id = $1 and s.gone_with_host is null and `+store.RestorableSnapshotWhere, hostID).Scan(&n)
	return n, err
}

// hostRoom adds what a host whose hostd shares it has room for
// (DECISIONS I-1654): `free_mem_bytes`, the memory its hostd last said
// was free, against which a machine starts at 1 GiB (I-1630); and on a
// Mac, an aarch64 host, which runs its guests nested, `running_limit`,
// the machines its cores run at once: two vCPUs each and two of the
// host's left over, never under one (I-1640). hostd does not report the
// bound, so it is computed here from the rule it applies, and left out
// where the api cannot tell (an x86 host, nested or not). A Mac without
// the appliance (design 2, its hostd names `restart`) runs each machine
// as a VM of its own and sizes each start to what the running machines
// leave, never refusing one for cores (I-1666, I-1680), so it has no
// count to give and `running_limit` is left out (I-1692).
func (s *Server) hostRoom(r *http.Request, h *store.Host, row map[string]any) error {
	var shares, own bool
	err := s.d.Pool.QueryRow(r.Context(), "select $2 = any(features), $3 = any(features) from hosts where id = $1", h.ID, scheduler.SharedFeature, RestartFeature).Scan(&shares, &own)
	if err != nil || !shares || h.State != "ready" {
		return err
	}
	row["free_mem_bytes"] = max(h.FreeMemBytes, 0)
	if hostArch(h) == "aarch64" && h.VCPUs > 0 && !own {
		row["running_limit"] = max((h.VCPUs-2)/nestedOwnedVCPUs, 1)
	}
	return nil
}

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	hosts, err := store.ListAccountHosts(r.Context(), s.d.Pool, u.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(hosts))
	for i := range hosts {
		projects, running, err := s.hostCounts(r, hosts[i].ID)
		if err != nil {
			return err
		}
		snaps, err := s.hostSnapshots(r, hosts[i].ID)
		if err != nil {
			return err
		}
		row := hostJSON(&hosts[i], projects, running, "", nil, nil)
		row["snapshots"] = snaps
		// A host that parks its machines before a restart says so, and
		// the dashboard offers its Restart (I-1690).
		if restarts, err := s.hostRestarts(r, hosts[i].ID); err != nil {
			return err
		} else if restarts {
			row["can_restart"] = true
		}
		if err := s.hostRoom(r, &hosts[i], row); err != nil {
			return err
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// createHost is POST /hosts: it mints the single-use join token the
// machine trades for its identity, and the row that makes every
// placement of this account prefer it. The plan says how many hosts the
// account may have, and the count and the insert run in one transaction:
// two requests at once must not both see room for the last host.
func (s *Server) createHost(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	// `{name?, provider?}`: no body at all is how the dashboard's Add and
	// `repose host add` without a name ask for one. provider is what the
	// caller knows the machine is before it registers (`mac` from the CLI
	// that installs on that Mac, I-1700), so a create waiting for it can
	// say "your Mac"; registration writes what the host says it is.
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	provider := "metal"
	switch body.Provider {
	case "", "metal":
	case "mac":
		provider = "mac"
	default:
		return withDetail(errf("invalid", "provider is mac or metal"), map[string]any{"reason": "provider"})
	}
	name := strings.TrimSpace(body.Name)
	if name != "" && !hostNameRe.MatchString(name) {
		return withDetail(errf("invalid", "a host name is lower-case letters, digits and dashes, at most 32 characters, like mac-mini"),
			map[string]any{"reason": "name"})
	}
	allowed, err := s.hostsAllowed(r, u)
	if err != nil {
		return err
	}
	if allowed <= 0 {
		return withDetail(errf("payment_required", "%s", billing.OwnHardwareRequiredMessage(s.billingURL())),
			map[string]any{"reason": "own_hardware_plan_required", "limit": 0})
	}
	token, hostID := "", uuid.Nil
	var hub *hostmgr.HubBootstrap
	err = db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		// The name before the count: an account at its plan's limit
		// asking for the name it already uses is told about the name,
		// which is the mistake it made, and not about the limit. No plan
		// sold today has a limit the count reaches (I-1594), but the
		// check stays: Plan.Hosts is the one place a limit is written.
		reissue := false
		if name == "" {
			// The api names it rather than the caller inventing one, so
			// `repose host add` and the dashboard's button agree.
			var err error
			if name, err = nextHostName(r.Context(), tx, u.ID); err != nil {
				return err
			}
		} else if h, err := store.GetAccountHostByName(r.Context(), tx, u.ID, name); err == nil && h != nil {
			if h.State != "retired" && h.State != "lost" {
				return withDetail(errf("conflict", "you already have a host called %s", name),
					map[string]any{"reason": "name_taken"})
			}
			// A removed host's name is free again, and the row it kept
			// for the history is the row that takes it: the name is
			// unique per account whatever a host's state, so a new row
			// could not have it, and telling a user the name of the
			// machine they just removed is taken would be a refusal they
			// cannot act on.
			reissue = true
		} else if err != nil && !errors.Is(err, db.ErrNotFound) {
			return err
		}
		var have int
		if err := tx.QueryRow(r.Context(), "select count(*) from hosts where owner_account_id = $1 and state not in ('retired', 'lost')", u.ID).Scan(&have); err != nil {
			return err
		}
		if have >= allowed {
			return withDetail(errf("conflict", "your plan includes %s; remove one before you add another", plural(allowed, "host")),
				map[string]any{"reason": "host_limit", "limit": allowed})
		}
		// Hosts that have not joined yet are capped per account, whatever
		// the plan: each holds one of the thousand host addresses every
		// account shares until its token expires, so an account adding
		// them without limit could leave none for anyone (ENG-211).
		var waiting int
		if err := tx.QueryRow(r.Context(), `select count(*) from hosts where owner_account_id = $1 and state = 'registering'
			and registered_at is null and join_token_hash is not null and join_token_expires_at > now()`, u.ID).Scan(&waiting); err != nil {
			return err
		}
		if waiting >= hostmgr.MaxWaitingHosts {
			return withDetail(errf("conflict", "%s have not joined yet; install repose on one, or remove one, before you add another", plural(waiting, "host")),
				map[string]any{"reason": "waiting_limit", "limit": hostmgr.MaxWaitingHosts})
		}
		owner := u.ID
		var err error
		token, hostID, err = hostmgr.MintJoinTokenIn(r.Context(), tx,
			hostmgr.JoinTokenRequest{Name: name, Provider: provider, Owner: &owner, Reissue: reissue})
		if err != nil {
			return err
		}
		// A machine of the user's has no route to the api's VNet address,
		// so it gets its place on the hub now and reaches the api over
		// WireGuard from its first boot (DECISIONS I-1380).
		hub, err = hostmgr.PrepareHub(r.Context(), tx, hostID)
		return err
	})
	if err != nil {
		return err
	}
	h, err := store.GetHost(r.Context(), s.d.Pool, hostID)
	if err != nil {
		return err
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "host_add", h.Name, map[string]any{"host_id": h.ID.String()}); err != nil {
		return err
	}
	// The token is never logged: it is a credential that takes over a
	// machine (CLAUDE.md "Never log what a tenant typed").
	obs.Logger(r.Context(), s.d.Log).Info("host added", "event", "host_add", "host_id", h.ID.String())
	install, err := s.hostInstall(r, token, hub)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, hostJSON(h, 0, 0, token, h.JoinTokenExpiresAt, install))
	return nil
}

// deleteHost is DELETE /hosts/{id}: drain and retire. A project moves
// host only by restore (DESIGN.md §6), so a host still holding machines
// is refused rather than retired under them.
func (s *Server) deleteHost(w http.ResponseWriter, r *http.Request) error {
	u := userFrom(r.Context())
	h, err := s.accountHost(r, u)
	if err != nil {
		return err
	}
	if h.State == "retired" {
		// Already gone. A repeated remove is not an error: the client
		// that lost the first answer must be able to ask again.
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	projects, err := store.ListProjectsOnHost(r.Context(), s.d.Pool, h.ID)
	if err != nil {
		return err
	}
	// A host that is answering keeps the refusal: its machines are really
	// there, and a machine moves host only by restore (DESIGN §6).
	//
	// A host that is NOT answering is a different question, and refusing
	// it left the account with no way out at all: the host could not be
	// removed while it held a project, the project could not be destroyed
	// while the host was not gone (I-924 skips a destroy's phases for
	// `lost` and `retired` only), and the plan's one host slot stayed
	// held, so no replacement could be added either. Removing it is the
	// user making the statement `hosts mark-lost` makes for an operator:
	// this host is not coming back. What made I-924 exclude `unreachable`
	// does not apply to the command being asked for here, because the
	// retire below clears `cert_serial`, `prev_cert_serial` and
	// `join_token_hash`, so the host cannot come back on them and the
	// live volume it worried about cannot reappear under a destroyed
	// record (ENG-119, DECISIONS I-1420).
	//
	// The machines are left where they are rather than destroyed: the
	// user asked to remove a host, not to lose their work, and every
	// `repose rm` on them now drains to no phases and completes. What
	// they lose is said at the confirmation, in the CLI and the
	// dashboard, not decided here.
	// Named as the states that mean it is not answering, not as the ones
	// that mean it is: a host still `registering` may yet come up, and
	// a state added later should refuse until someone decides it should
	// not.
	if len(projects) > 0 && h.State != "unreachable" && h.State != "lost" {
		slugs := make([]string, 0, len(projects))
		for _, p := range projects {
			slugs = append(slugs, p.Slug)
		}
		return withDetail(errf("conflict", "%s still holds %s. A machine stays on the host it was made on, so remove them first: %s.",
			h.Name, plural(len(slugs), "machine"), strings.Join(slugs, ", ")),
			map[string]any{"reason": "host_has_projects", "projects": slugs})
	}
	// The serials go with the state, so a machine that is still dialling
	// is refused whatever certificate it holds (I-432), and the join
	// token with them, so a token minted and never used cannot register
	// a retired host.
	// The snapshots kept on its disk go with it (I-1542): marked in the
	// same transaction as the retire, by name, so a host of the same
	// name added later never brings them back.
	if err := db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		if _, err := tx.Exec(r.Context(), `update hosts set state = 'retired', draining = true,
		    cert_serial = null, prev_cert_serial = null, join_token_hash = null
		  where id = $1`, h.ID); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `update snapshots set gone_with_host = $2
		  where host_id = $1 and gone_with_host is null and deleted_at is null`, h.ID, h.Name)
		return err
	}); err != nil {
		return err
	}
	// What the host's absence wrote on the account's machines goes with
	// it (ENG-33, the customer review). A placement that found no host
	// of the account's own frees the project and leaves its sentence on
	// the row (ops/phases.go), so "your host host-1 has not registered
	// yet" outlived host-1: Settings → Hosts said the customer had no
	// hardware while five rows named it, and the only control offered
	// was a Start that could not succeed. The op keeps the history; the
	// row stops asserting something false, and the next start says
	// what is true now. Only the capacity class, which is the one a
	// host's absence causes, and `capacity:` is the api's own prefix
	// rather than anything a tenant typed.
	cleared, err := s.d.Pool.Exec(r.Context(), `update projects set last_error = null
		  where user_id = $1 and host_id is null and state = 'error'
		    and last_error like 'capacity:%' and destroyed_at is null`, u.ID)
	if err != nil {
		return err
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "host_rm", h.Name, map[string]any{"host_id": h.ID.String()}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("host removed", "event", "host_rm",
		"host_id", h.ID.String(), "cleared", cleared.RowsAffected())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// accountHost resolves the {id} of a host route against the caller's own
// hosts. A host that is not theirs is not found, so the route says
// nothing about another account's machines.
func (s *Server) accountHost(r *http.Request, u *store.User) (*store.Host, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return nil, errf("not_found", "no host %s", r.PathValue("id"))
	}
	h, err := store.GetHost(r.Context(), s.d.Pool, id)
	if err != nil || h.OwnerAccountID == nil || *h.OwnerAccountID != u.ID {
		return nil, errf("not_found", "no host %s", id)
	}
	return h, nil
}

// hostsAllowed is how many hosts of their own the account may have: the
// plan's figure for the Own hardware plan (I-1366), which lives in
// internal/billing and
// nowhere else. An api built without a gate allows one, as it lets every
// other compute gate through.
func (s *Server) hostsAllowed(r *http.Request, u *store.User) (int, error) {
	if s.d.Gate == nil {
		return 1, nil
	}
	return s.d.Gate.HostsAllowed(r.Context(), s.d.Pool, u)
}

// hostInstall is the `install` object of the POST answer: the one line to
// run on the machine with its argument already in it, the argument on its
// own, and the page that explains it. The argument is the join bundle
// (hostmgr.JoinBundle): the token and, where the deployment has a hub,
// the WireGuard material the machine needs before it can reach the api
// at all (DECISIONS I-1380). `command` is empty while no installer line
// has been recorded, and a client then shows the argument and the page.
func (s *Server) hostInstall(r *http.Request, token string, hub *hostmgr.HubBootstrap) (map[string]any, error) {
	tmpl, err := store.Setting(r.Context(), s.d.Pool, SettingHostInstall)
	if err != nil {
		return nil, err
	}
	arg, err := hostmgr.EncodeJoinBundle(hostmgr.JoinBundle{Token: token, WG: hub})
	if err != nil {
		return nil, err
	}
	command := ""
	if strings.Contains(tmpl, "%s") {
		command = strings.Replace(tmpl, "%s", arg, 1)
	}
	return map[string]any{"command": command, "argument": arg, "url": s.docsURL("own-hardware")}, nil
}

// nextHostName names a host the caller did not name: `host-1`, then the
// first number that account has not used.
func nextHostName(ctx context.Context, tx db.Tx, owner uuid.UUID) (string, error) {
	for i := 1; ; i++ {
		name := fmt.Sprintf("host-%d", i)
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from hosts where owner_account_id = $1 and name = $2", owner, name).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return name, nil
		}
	}
}

// plural is "1 host" and "2 hosts".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// billingURL and docsURL are the two addresses a host refusal names.
// Deps.DashboardURL is the deployment's own origin; the default is the
// one billing.Config falls back to.
func (s *Server) billingURL() string { return s.dashboard() + "/billing" }

func (s *Server) docsURL(page string) string { return s.dashboard() + "/docs/" + page }

func (s *Server) dashboard() string {
	if s.d.DashboardURL != "" {
		return strings.TrimRight(s.d.DashboardURL, "/")
	}
	return billing.DefaultDashboardURL
}

// checkHostedAllowed refuses a project that asked for the hosted fleet
// when the account's plan sells no memory there (DECISIONS I-1366).
// `own` is never refused: an account with no host of its own
// goes to the fleet as every account always has.
func (s *Server) checkHostedAllowed(r *http.Request, u *store.User, placement string) error {
	if placement != store.PlacementHosted || s.d.Gate == nil {
		return nil
	}
	ok, err := s.d.Gate.FleetAllowed(r.Context(), s.d.Pool, u.ID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return withDetail(errf("payment_required", "Your plan runs machines on your own host. Choose a plan at %s to run one on repose's servers.", s.billingURL()),
		map[string]any{"reason": billing.ReasonHostedPlanRequired})
}
