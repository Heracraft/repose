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
	// The architecture is the first half of the system the host reported
	// (`x86_64-linux`), so it is known from the first registration and
	// absent before it. No column: the machine tells us what it is.
	if h.NixosSystem != nil {
		if arch, _, ok := strings.Cut(*h.NixosSystem, "-"); ok && arch != "" {
			out["arch"] = arch
		}
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

// hostCounts is the account's live projects on the host and those of
// them in a state that holds memory, which is what the list shows.
func (s *Server) hostCounts(r *http.Request, hostID uuid.UUID) (projects, running int, err error) {
	err = s.d.Pool.QueryRow(r.Context(), `select count(*),
		    count(*) filter (where state in ('creating','building','starting','running','stopping','restoring'))
		  from projects where host_id = $1 and destroyed_at is null`, hostID).Scan(&projects, &running)
	return projects, running, err
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
		out = append(out, hostJSON(&hosts[i], projects, running, "", nil, nil))
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
	// `{name?}`: no body at all is how the dashboard's Add and
	// `repose host add` without a name ask for one.
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
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
		return withDetail(errf("payment_required", "Your plan does not include a host of your own. Choose the Own hardware plan at %s.", s.billingURL()),
			map[string]any{"reason": "own_hardware_plan_required", "limit": 0})
	}
	token, hostID := "", uuid.Nil
	err = db.InTx(r.Context(), s.d.Pool, func(tx db.Tx) error {
		// The name before the count: an account at its one-host limit
		// asking for the name it already uses is told about the name,
		// which is the mistake it made, and not about the limit.
		if name == "" {
			// The api names it rather than the caller inventing one, so
			// `repose host add` and the dashboard's button agree.
			var err error
			if name, err = nextHostName(r.Context(), tx, u.ID); err != nil {
				return err
			}
		} else if h, err := store.GetAccountHostByName(r.Context(), tx, u.ID, name); err == nil && h != nil {
			return withDetail(errf("conflict", "you already have a host called %s", name),
				map[string]any{"reason": "name_taken"})
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
		owner := u.ID
		var err error
		token, hostID, err = hostmgr.MintJoinTokenIn(r.Context(), tx, hostmgr.JoinTokenRequest{Name: name, Provider: "metal", Owner: &owner})
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
	install, err := s.hostInstall(r, token)
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
	if len(projects) > 0 {
		slugs := make([]string, 0, len(projects))
		for _, p := range projects {
			slugs = append(slugs, p.Slug)
		}
		return withDetail(errf("conflict", "%s still holds %s. A machine moves host only by restoring a snapshot, so remove them first: %s.",
			h.Name, plural(len(slugs), "machine"), strings.Join(slugs, ", ")),
			map[string]any{"reason": "host_has_projects", "projects": slugs})
	}
	// The serials go with the state, so a machine that is still dialling
	// is refused whatever certificate it holds (I-432), and the join
	// token with them, so a token minted and never used cannot register
	// a retired host.
	if _, err := s.d.Pool.Exec(r.Context(), `update hosts set state = 'retired', draining = true,
		    cert_serial = null, prev_cert_serial = null, join_token_hash = null
		  where id = $1`, h.ID); err != nil {
		return err
	}
	if _, err := store.Audit(r.Context(), s.d.Pool, "user:"+u.ID.String(), "host_rm", h.Name, map[string]any{"host_id": h.ID.String()}); err != nil {
		return err
	}
	obs.Logger(r.Context(), s.d.Log).Info("host removed", "event", "host_rm", "host_id", h.ID.String())
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
// run on the machine with the token already in it, and the page that
// explains it. `command` is empty while no installer line has been
// recorded, and a client then shows the token and the page.
func (s *Server) hostInstall(r *http.Request, token string) (map[string]any, error) {
	tmpl, err := store.Setting(r.Context(), s.d.Pool, SettingHostInstall)
	if err != nil {
		return nil, err
	}
	command := ""
	if strings.Contains(tmpl, "%s") {
		command = strings.Replace(tmpl, "%s", token, 1)
	}
	return map[string]any{"command": command, "url": s.docsURL("own-hardware")}, nil
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
