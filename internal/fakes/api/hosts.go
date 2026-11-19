package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Hardware a user owns (DECISIONS I-1366, I-1370): `GET /hosts`,
// `POST /hosts` and `DELETE /hosts/{id}`. The fake keeps one row per
// host with its owner, mints a single-use join token per `POST /hosts`
// and hands back the install step the real api composes, so the CLI and
// the dashboard see the shape they will see in production.
//
// The producer is workstream 19a; this fake is what 19c builds against
// until that merges (docs/interfaces/README.md "Fakes").

// OwnedHost is a row of GET /hosts. The name is OwnedHost here because
// Host in this package is already a row of GET /internal/hosts, the
// edge's WireGuard peer list.
type OwnedHost struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	State           string     `json:"state"`
	Provider        string     `json:"provider,omitempty"`
	Arch            string     `json:"arch,omitempty"`
	MemBytes        int64      `json:"mem_bytes"`
	VCPUs           int        `json:"vcpus"`
	Cores           int        `json:"cores"`
	Projects        int        `json:"projects"`
	RunningProjects int        `json:"running_projects"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	RegisteredAt    *time.Time `json:"registered_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// hostInstall is the step the user runs on the machine being added.
type hostInstall struct {
	Command string `json:"command"`
	URL     string `json:"url"`
}

type ownedHost struct {
	OwnedHost
	owner        string
	token        string
	tokenExpires time.Time
}

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// hostJoinTokenValidity is hostmgr.JoinTokenValidity.
const hostJoinTokenValidity = 24 * time.Hour

// defaultHostLimit is the Own hardware plan's one host (19d's plan row
// is what makes it 1 on the real api; cited by name, not by id, until
// that entry is written).
const defaultHostLimit = 1

// hostInstallDocsURL is the page `install.url` always carries.
const hostInstallDocsURL = "https://repose.run/docs/own-hardware"

func (f *Fake) ownedHostsOf(user string) []*ownedHost {
	var out []*ownedHost
	for _, h := range f.ownedHosts {
		if h.owner == user {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (f *Fake) listOwnedHosts(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	out := []OwnedHost{}
	for _, h := range f.ownedHostsOf(u.ID) {
		row := h.OwnedHost
		row.Projects, row.RunningProjects = f.placedOn(h.ID, u.ID)
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// placedOn counts the account's live machines on a host and how many of
// them hold memory.
func (f *Fake) placedOn(hostID, user string) (placed, running int) {
	for _, p := range f.projects {
		if p.owner != user || p.destroyed || p.HostID != hostID {
			continue
		}
		placed++
		if p.State == "running" {
			running++
		}
	}
	return placed, running
}

// slugsOn is the account's live machines on a host, for the refusal to
// retire a host under them.
func (f *Fake) slugsOn(hostID, user string) []string {
	var out []string
	for _, p := range f.projects {
		if p.owner == user && !p.destroyed && p.HostID == hostID {
			out = append(out, p.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func (f *Fake) createOwnedHost(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	var body struct {
		Name string `json:"name"`
	}
	if e := decodeBody(r, &body, true); e != nil {
		return e
	}
	mine := f.ownedHostsOf(u.ID)
	name := body.Name
	if name == "" {
		// The api names it when the user does not: host-1, host-2, ...
		// never a name another account could already hold.
		for n := 1; ; n++ {
			name = fmt.Sprintf("host-%d", n)
			taken := false
			for _, h := range mine {
				if h.Name == name {
					taken = true
				}
			}
			if !taken {
				break
			}
		}
	}
	if !hostNameRe.MatchString(name) {
		return errf("invalid", "a host name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit").withDetail(map[string]any{"reason": "name"})
	}
	for _, h := range mine {
		if h.Name == name {
			return errf("conflict", "you already have a host named %s", name).withDetail(map[string]any{"reason": "name_taken"})
		}
	}
	limit := f.hostLimit
	if limit == 0 {
		limit = defaultHostLimit
	}
	if limit < 0 {
		// No Own hardware plan: the account may own none at all.
		return errf("payment_required", "hardware of your own needs the Own hardware plan").withDetail(map[string]any{"reason": "own_hardware_plan_required", "limit": 0})
	}
	if len(mine) >= limit {
		return errf("conflict", "your plan takes %d host(s)", limit).withDetail(map[string]any{"reason": "host_limit", "limit": limit})
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := "rjt_" + base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().UTC()
	h := &ownedHost{
		owner: u.ID, token: token, tokenExpires: now.Add(hostJoinTokenValidity),
		OwnedHost: OwnedHost{ID: f.nextID(), Name: name, State: "registering", CreatedAt: now},
	}
	f.ownedHosts = append(f.ownedHosts, h)
	// The install step is the `host_install_command` setting with the
	// token in place of its %s; no setting recorded means no line, and
	// then the token is all the api can hand over (19f owns the real
	// one).
	command := ""
	if f.hostInstall != "" {
		command = strings.Replace(f.hostInstall, "%s", token, 1)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": h.ID, "name": h.Name, "state": h.State,
		"mem_bytes": 0, "vcpus": 0, "cores": 0, "projects": 0, "running_projects": 0,
		"last_heartbeat_at": nil, "registered_at": nil, "created_at": h.CreatedAt,
		"join_token":            token,
		"join_token_expires_at": h.tokenExpires,
		"install":               hostInstall{Command: command, URL: hostInstallDocsURL},
	})
	return nil
}

func (f *Fake) deleteOwnedHost(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	id := r.PathValue("id")
	for i, h := range f.ownedHosts {
		if h.owner != u.ID || h.ID != id {
			continue
		}
		// A machine moves host only by restore, so a host under live
		// machines is not retired (19a): the user clears them first.
		if slugs := f.slugsOn(h.ID, u.ID); len(slugs) > 0 {
			return errf("conflict", "%s still has machines on it", h.Name).withDetail(map[string]any{"reason": "host_has_projects", "projects": slugs})
		}
		f.ownedHosts = append(f.ownedHosts[:i], f.ownedHosts[i+1:]...)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	// A second remove of the same host is not an error (19a).
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// AddOwnedHost adds a registered host of the canned user's, as one that
// has joined and is answering, and returns its id.
func (f *Fake) AddOwnedHost(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	h := &ownedHost{owner: CannedUser.ID, OwnedHost: OwnedHost{
		ID: f.nextID(), Name: name, State: "ready", Provider: "mac", Arch: "aarch64",
		MemBytes: 24 << 30, VCPUs: 8, Cores: 8,
		LastHeartbeatAt: &now, RegisteredAt: &now, CreatedAt: now,
	}}
	f.ownedHosts = append(f.ownedHosts, h)
	return h.ID
}

// SetOwnedHostOffline puts a host in `unreachable` and moves its last
// heartbeat back by quiet, as the api's sweep does for a host that has
// stopped answering.
func (f *Fake) SetOwnedHostOffline(id string, quiet time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.ID == id {
			h.State = "unreachable"
			seen := time.Now().UTC().Add(-quiet)
			h.LastHeartbeatAt = &seen
		}
	}
}

// SetHostLimit is how many hosts one account may add; 0 restores the
// plan's one, and a negative number is an account with no Own hardware
// plan, which may own none.
func (f *Fake) SetHostLimit(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostLimit = n
}

// SetHostInstallCommand is the api's `host_install_command` setting,
// with %s where the join token goes; "" is an api with none recorded,
// which is what it has until 19f sets the Mac's line.
func (f *Fake) SetHostInstallCommand(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostInstall = cmd
}

// SetProjectHost moves a project onto a host, as placement does for an
// account that owns one (I-1366).
func (f *Fake) SetProjectHost(projectID, hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.HostID = hostID
	}
}
