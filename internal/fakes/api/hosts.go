package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The hosts a user owns, of docs/interfaces/api.md "Hosts you own"
// (DECISIONS I-1366). The fake keeps the shapes `repose host` and the
// dashboard's Hosts page read: the list, the mint's token and install
// line, and every refusal, so a test sees what the api would answer.
//
// A fake host never registers by itself, because nothing here runs
// hostd. `SetOwnedHostState` is how a test moves one to `ready`,
// `unreachable` or anything else, and `SetOwnedHostMachines` gives it
// the project counts the list shows.

// FakeHostInstall is the install line the fake answers, with the token
// already in it. The real api's is the `host_install_command` setting
// and empty until an operator records one; the fake fills it so a test
// of the printing has something to print, and SetHostInstall("") is how
// a test takes it away again.
const FakeHostInstall = "curl -fsSL https://repose.test/host.sh | REPOSE_JOIN_TOKEN=%s sh"

// maxFakeHosts is the host count the plans allow: one on the Own
// hardware plan (DECISIONS I-1366).
const maxFakeHosts = 1

// FakeDashboard is the origin the fake builds the refusals' and the
// install's addresses on.
const FakeDashboard = "https://repose.run"

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// OwnedHost is one host the account owns, as the api returns it. Host
// is taken by the row of GET /internal/hosts, which is the edge's view
// of the fleet and a different thing entirely.
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
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	RegisteredAt    *time.Time `json:"registered_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	// The three the POST answer alone carries.
	JoinToken          string            `json:"join_token,omitempty"`
	JoinTokenExpiresAt *time.Time        `json:"join_token_expires_at,omitempty"`
	Install            *OwnedHostInstall `json:"install,omitempty"`
}

// OwnedHostInstall is OwnedHost.install: the line to run on the machine and the
// page that explains it. Command is empty when the api has no line
// recorded, and a client then shows the token and the page.
type OwnedHostInstall struct {
	Command string `json:"command"`
	URL     string `json:"url"`
}

type ownedHostRec struct {
	OwnedHost
	owner string
}

// SetOwnedHostState moves one of an account's hosts to a state, which is what
// a registration, a heartbeat or a silence would have done. The host
// gets its memory and its registration times with `ready`, so a test
// does not have to fill them in.
func (f *Fake) SetOwnedHostState(name, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name != name {
			continue
		}
		h.State = state
		if state == "ready" && h.RegisteredAt == nil {
			now := f.now()
			h.RegisteredAt = &now
			h.LastHeartbeatAt = &now
			h.MemBytes, h.VCPUs, h.Cores, h.Arch = 24<<30, 8, 8, "aarch64"
			h.JoinToken = ""
		}
	}
}

// SetOwnedHostMachines sets the project counts the list shows for a host.
func (f *Fake) SetOwnedHostMachines(name string, projects, running int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name == name {
			h.Projects, h.RunningProjects = projects, running
		}
	}
}

// SetHostInstall replaces the install line the mint answers; "" is an
// api with none recorded.
func (f *Fake) SetHostInstall(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostInstall, f.hostInstallSet = line, true
}

// SetHostLimit replaces the hosts an account may own, which is the
// plan's figure in the real api; 0 is a plan that sells none.
func (f *Fake) SetHostLimit(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostLimit = &n
}

// hostsAllowed is the plan's figure in the real api. The fake allows
// one to every account, exempt or not (billing.ExemptHosts is one too),
// and SetHostLimit is how a test asks for a plan that sells none.
func (f *Fake) hostsAllowed() int {
	if f.hostLimit != nil {
		return *f.hostLimit
	}
	return maxFakeHosts
}

func (f *Fake) listHosts(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	out := []*OwnedHost{}
	for _, h := range f.ownedHosts {
		if h.owner != u.ID {
			continue
		}
		row := h.OwnedHost
		row.JoinToken, row.JoinTokenExpiresAt, row.Install = "", nil, nil
		out = append(out, &row)
	}
	writeJSON(w, 200, out)
	return nil
}

func (f *Fake) createHost(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Name string `json:"name"`
	}
	if e := decodeBody(r, &body, true); e != nil {
		return e
	}
	name := strings.TrimSpace(body.Name)
	if name != "" && !hostNameRe.MatchString(name) {
		return invalid("a host name is lower-case letters, digits and dashes, at most 32 characters, like mac-mini").
			withDetail(map[string]any{"reason": "name"})
	}
	u := userFrom(r)
	allowed := f.hostsAllowed()
	if allowed <= 0 {
		return errf("payment_required", "Your plan does not include a host of your own. Choose the Own hardware plan at %s/billing.", FakeDashboard).
			withDetail(map[string]any{"reason": "own_hardware_plan_required", "limit": 0})
	}
	have := 0
	for _, h := range f.ownedHosts {
		if h.owner == u.ID && h.State != "retired" && h.State != "lost" {
			have++
		}
		if h.owner == u.ID && name != "" && h.Name == name {
			return errf("conflict", "you already have a host called %s", name).
				withDetail(map[string]any{"reason": "name_taken"})
		}
	}
	if have >= allowed {
		word := "hosts"
		if allowed == 1 {
			word = "host"
		}
		return errf("conflict", "your plan includes %d %s; remove one before you add another", allowed, word).
			withDetail(map[string]any{"reason": "host_limit", "limit": allowed})
	}
	if name == "" {
		name = f.nextHostName(u.ID)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return errf("internal", "%v", err)
	}
	expires := f.now().Add(24 * time.Hour)
	h := &ownedHostRec{owner: u.ID, OwnedHost: OwnedHost{
		ID: f.nextID(), Name: name, State: "registering", Provider: "metal",
		CreatedAt: f.now(), JoinToken: base64.RawURLEncoding.EncodeToString(raw), JoinTokenExpiresAt: &expires,
	}}
	line := FakeHostInstall
	if f.hostInstallSet {
		line = f.hostInstall
	}
	command := ""
	if strings.Contains(line, "%s") {
		command = strings.Replace(line, "%s", h.JoinToken, 1)
	}
	h.Install = &OwnedHostInstall{Command: command, URL: FakeDashboard + "/docs/own-hardware"}
	f.ownedHosts = append(f.ownedHosts, h)
	writeJSON(w, 201, h.OwnedHost)
	return nil
}

func (f *Fake) deleteHost(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	id := r.PathValue("id")
	for _, h := range f.ownedHosts {
		if h.ID != id || h.owner != u.ID {
			continue
		}
		if h.State == "retired" {
			w.WriteHeader(204)
			return nil
		}
		if h.Projects > 0 {
			slugs := f.slugsOnHost(h.ID, u.ID, h.Projects)
			word := "machines"
			if len(slugs) == 1 {
				word = "machine"
			}
			return errf("conflict", "%s still holds %d %s. A machine moves host only by restoring a snapshot, so remove them first: %s.",
				h.Name, len(slugs), word, strings.Join(slugs, ", ")).
				withDetail(map[string]any{"reason": "host_has_projects", "projects": slugs})
		}
		h.State = "retired"
		h.JoinToken = ""
		w.WriteHeader(204)
		return nil
	}
	return notFound("host " + id)
}

// slugsOnHost names the account's live projects the list says are on the
// host. The fake places nothing, so a test that set a count without
// making projects gets names that stand for them.
func (f *Fake) slugsOnHost(hostID, owner string, count int) []string {
	var slugs []string
	for _, p := range f.projects {
		if p.owner == owner && !p.destroyed && p.HostID == hostID {
			slugs = append(slugs, p.Slug)
		}
	}
	for i := len(slugs); i < count; i++ {
		slugs = append(slugs, fmt.Sprintf("machine-%d", i+1))
	}
	return slugs
}

func (f *Fake) nextHostName(owner string) string {
	for i := 1; ; i++ {
		name := fmt.Sprintf("host-%d", i)
		taken := false
		for _, h := range f.ownedHosts {
			if h.owner == owner && h.Name == name {
				taken = true
			}
		}
		if !taken {
			return name
		}
	}
}

// AddOwnedHost adds a host of the canned user's that has already
// registered and is answering, and returns its id. `POST /hosts` only
// ever makes a `registering` one, because in the real api a host
// becomes `ready` by running hostd, which nothing here does; a test
// about what a working host looks like starts from this.
func (f *Fake) AddOwnedHost(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	h := &ownedHostRec{owner: CannedUser.ID, OwnedHost: OwnedHost{
		ID: f.nextID(), Name: name, State: "ready", Provider: "mac", Arch: "aarch64",
		MemBytes: 24 << 30, VCPUs: 8, Cores: 8,
		LastHeartbeatAt: &now, RegisteredAt: &now, CreatedAt: now,
	}}
	f.ownedHosts = append(f.ownedHosts, h)
	return h.ID
}

// SetOwnedHostOffline marks a host unreachable and puts its last
// heartbeat quiet ago, which is what a machine switched off looks like.
// The duration is the point: a surface that says how long it has been
// quiet reads `last_heartbeat_at` and not the state.
func (f *Fake) SetOwnedHostOffline(id string, quiet time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.ID == id {
			at := f.now().Add(-quiet)
			h.State, h.LastHeartbeatAt = "unreachable", &at
		}
	}
}

// SetProjectHost places a project on a host, which the real api's
// scheduler does at the create and the fake does not do at all.
func (f *Fake) SetProjectHost(projectID, hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.projects[projectID]; p != nil {
		p.HostID = hostID
	}
	for _, h := range f.ownedHosts {
		if h.ID != hostID {
			continue
		}
		h.Projects, h.RunningProjects = 0, 0
		for _, p := range f.projects {
			if p.destroyed || p.HostID != hostID {
				continue
			}
			h.Projects++
			switch p.State {
			case "creating", "building", "starting", "running", "stopping", "restoring":
				h.RunningProjects++
			}
		}
	}
}
