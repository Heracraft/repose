package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
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
const FakeHostInstall = "repose host add --join %s"

// maxFakeHosts is billing.ExemptHosts: the hosts a billing-exempt
// account, or an api with billing off, may own (DECISIONS I-1366,
// I-1582, and since I-1594 the Own hardware plan's any number).
// TestExemptHostsIsTheApis holds the two equal.
const maxFakeHosts = unlimitedHosts

// maxWaitingHosts is hostmgr.MaxWaitingHosts: the hosts one account may
// have added that have not joined yet (ENG-211).
const maxWaitingHosts = 10

// ownHardwareRequired is billing.OwnHardwareRequiredMessage's sentence,
// word for word, built from the fake's own plan table so that a change
// to the real one is a deliberate change here too
// (TestOwnHardwareRefusalIsTheApis holds them equal). It names the
// price: the refusal is the one place in the product that tells a
// buyer what a host costs while the public copy is held (ENG-39).
func ownHardwareRequired() string {
	return fmt.Sprintf("Your plan does not include a host of your own. The %s plan is %s a month for %s: choose it at %s/billing.",
		OwnPlan.Name, dollars(OwnPlan.PriceCents), hostsPhrase(OwnPlan.Hosts), FakeDashboard)
}

// hostsPhrase is billing.HostsPhrase: "one host", "3 hosts", or "any
// number of hosts" for unlimitedHosts.
func hostsPhrase(n int) string {
	switch {
	case n >= unlimitedHosts:
		return "any number of hosts"
	case n == 1:
		return "one host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// dollars is billing.Dollars: "$10", or "$10.50" when the cents are not
// whole.
func dollars(cents int64) string {
	if cents%100 == 0 {
		return fmt.Sprintf("$%d", cents/100)
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// FakeDashboard is the origin the fake builds the refusals' and the
// install's addresses on.
const FakeDashboard = "https://repose.run"

// fakeWGKey is a canned WireGuard public key: 32 zero bytes in base64,
// which is the right shape and the right length and opens nothing.
const fakeWGKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// OwnedHost is one host the account owns, as the api returns it. Host
// is taken by the row of GET /internal/hosts, which is the edge's view
// of the fleet and a different thing entirely.
type OwnedHost struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	State           string `json:"state"`
	Provider        string `json:"provider,omitempty"`
	Arch            string `json:"arch,omitempty"`
	MemBytes        int64  `json:"mem_bytes"`
	VCPUs           int    `json:"vcpus"`
	Cores           int    `json:"cores"`
	Projects        int    `json:"projects"`
	RunningProjects int    `json:"running_projects"`
	// FreeMemBytes and RunningLimit are a sharing host's room (I-1654):
	// what it reports free, and on a Mac the machines its cores run at
	// once. The listing fills them in.
	FreeMemBytes *int64 `json:"free_mem_bytes,omitempty"`
	RunningLimit int    `json:"running_limit,omitempty"`
	// Snapshots counts the live snapshots kept on the host's disk, which
	// removing it deletes (I-1542).
	Snapshots int `json:"snapshots"`
	// CanRestart is a host that parks its machines before a restart, so
	// the dashboard offers Restart (I-1690).
	CanRestart      bool       `json:"can_restart,omitempty"`
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
	Command  string `json:"command"`
	Argument string `json:"argument,omitempty"`
	URL      string `json:"url"`
}

type ownedHostRec struct {
	OwnedHost
	owner string
	// freeMem is the free memory this host reports, which is what the api
	// places a machine on an owned host against (DECISIONS I-1630); -1
	// is the fake's default, half the host's memory.
	freeMem int64
	// oldHostd is a host whose hostd does not share (no "shared" Hello
	// feature): the api places there by class, as before I-1630.
	oldHostd bool
	// restarts is a host whose hostd names `restart` (I-1690), and
	// notParked the slugs its next restart cannot park.
	restarts  bool
	notParked []string
}

// SetOwnedHostOldHostd makes the account's live host called name one
// whose hostd does not share, or one that does again.
func (f *Fake) SetOwnedHostOldHostd(name string, old bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name == name && h.State != "retired" && h.State != "lost" {
			h.oldHostd = old
		}
	}
}

// room is what placement counts free on h and what a machine of class
// needs there: the reported free against a gigabyte on a host that
// shares, the class arithmetic on one that does not (I-1630).
func (f *Fake) room(h *ownedHostRec, class string) (free, need int64) {
	if h.oldHostd {
		return f.ownedHostFree(h), int64(classGB[class]) << 30
	}
	return h.reportedFree(), ownedStartFreeBytes
}

// ownedHostFree is the memory the api's placement counts free on an
// owned host whose hostd does not share: its memory, less the eighth it
// keeps (never under 2 GiB, never over 16), less the class memory of
// every machine on it that holds memory.
func (f *Fake) ownedHostFree(h *ownedHostRec) int64 {
	reserve := min(max(h.MemBytes/8, 2<<30), 16<<30)
	free := h.MemBytes - reserve
	for _, p := range f.projects {
		if p.destroyed || p.HostID != h.ID {
			continue
		}
		switch p.State {
		case "creating", "building", "starting", "running", "stopping", "restoring":
			free -= int64(classGB[p.Class]) << 30
		}
	}
	return max(free, 0)
}

// reportedFree is what the host reports free: freeMem once a test has
// set it, else half its memory, which takes any number of machines.
func (h *ownedHostRec) reportedFree() int64 {
	if h.freeMem >= 0 {
		return h.freeMem
	}
	return h.MemBytes / 2
}

// SetOwnedHostFree sets the free memory the account's live host called
// name reports, as its hostd's heartbeat would (DECISIONS I-1630).
func (f *Fake) SetOwnedHostFree(name string, b int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name == name && h.State != "retired" && h.State != "lost" {
			h.freeMem = b
		}
	}
}

// SetOwnedHostState moves one of an account's hosts to a state, which is what
// a registration, a heartbeat or a silence would have done. The host
// gets its memory and its registration times with `ready`, so a test
// does not have to fill them in. A name is the live host's, as in the
// api, where a removed host's name is free again: a retired or lost
// host keeps its state. Moving every row of that name brought the
// hosts earlier tests had removed back as `registering`, and a create
// then named six waiting mac-minis (main 49b140b0, I-1582).
func (f *Fake) SetOwnedHostState(name, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name != name || h.State == "retired" || h.State == "lost" {
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

// SetHostInstall replaces the install line the mint answers; "" is an
// api with none recorded.
func (f *Fake) SetHostInstall(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostInstall, f.hostInstallSet = line, true
}

// SetHostLimit replaces the hosts an account may own, which is the
// plan's figure in the real api; 0 is a plan that sells none, and a
// figure below 0 puts the default back (the billing mode's figure, any
// number of hosts with billing off, and memory on repose's servers too).
// No plan the api sells has a figure above 0 that a count reaches
// (I-1594); a test sets one to reach the host_limit refusal an older
// plan answered. A figure a test set above 0 is read as the
// Own hardware plan, which sells no fleet memory, so `hosted` is
// refused on it (createProject).
func (f *Fake) SetHostLimit(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 0 {
		f.hostLimit = nil
		return
	}
	f.hostLimit = &n
}

// hostsAllowed is the plan's figure in the real api: maxFakeHosts for
// an exempt account or billing off, the plan's own figure otherwise, and
// SetHostLimit is how a test asks for a figure of its own.
func (f *Fake) hostsAllowed() int {
	if f.hostLimit != nil {
		return *f.hostLimit
	}
	// The api's answer, in the fake's terms (billing.Gate.HostsAllowed):
	// a billing-exempt account and an api with billing off get any
	// number, an account with no live subscription gets none, and
	// otherwise the plan's own figure decides -- any number on Own
	// hardware (I-1594), none on the plans that sell memory on repose's
	// fleet.
	//
	// It used to hand one to every account whatever its plan, which is a
	// fake that says yes where the api says no: a reviewer running
	// against it reports "I added a Mac without ever paying and nothing
	// stopped me" about a contract that does stop them (ENG-36), and no
	// test of ours disagrees, because the tests that care set the limit
	// by hand.
	switch f.bill.mode {
	case BillingOff, BillingExempt:
		return maxFakeHosts
	}
	plan := f.currentPlan()
	if plan == nil {
		return 0
	}
	return plan.Hosts
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
		row.Projects, row.RunningProjects = f.machinesOn(h.ID)
		row.Snapshots = f.snapshotsOn(h.ID)
		if h.State == "ready" && !h.oldHostd {
			// The api's hostRoom: a sharing host's free memory, and the
			// I-1640 bound on a Mac.
			free := h.reportedFree()
			row.FreeMemBytes = &free
			if row.Arch == "aarch64" && row.VCPUs > 0 {
				row.RunningLimit = max((row.VCPUs-2)/2, 1)
			}
		}
		row.CanRestart = h.restarts
		out = append(out, &row)
	}
	writeJSON(w, 200, out)
	return nil
}

// machinesOn counts the live projects placed on a host and those of them
// in a state that holds memory, as the api's own query does. Counted at
// every read rather than kept on the row: a project destroyed after it
// was placed must stop counting, and a stored count said a host still
// held a machine that was gone.
func (f *Fake) machinesOn(hostID string) (projects, running int) {
	for _, p := range f.projects {
		if p.destroyed || p.HostID != hostID {
			continue
		}
		projects++
		switch p.State {
		case "creating", "building", "starting", "running", "stopping", "restoring":
			running++
		}
	}
	return projects, running
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
		return errf("payment_required", "%s", ownHardwareRequired()).
			withDetail(map[string]any{"reason": "own_hardware_plan_required", "limit": 0})
	}
	have, waiting := 0, 0
	var revive *ownedHostRec
	for _, h := range f.ownedHosts {
		gone := h.State == "retired" || h.State == "lost"
		if h.owner == u.ID && !gone {
			have++
		}
		if h.owner == u.ID && h.State == "registering" && h.JoinTokenExpiresAt != nil && h.JoinTokenExpiresAt.After(f.now()) {
			waiting++
		}
		if h.owner == u.ID && name != "" && h.Name == name {
			if !gone {
				return errf("conflict", "you already have a host called %s", name).
					withDetail(map[string]any{"reason": "name_taken"})
			}
			// A removed host's name is free again and its own row takes
			// it back, as the api does.
			revive = h
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
	// The api's per-account cap on hosts that have not joined (ENG-211),
	// checked after the plan's as there.
	if waiting >= maxWaitingHosts {
		return errf("conflict", "%d hosts have not joined yet; install repose on one, or remove one, before you add another", waiting).
			withDetail(map[string]any{"reason": "waiting_limit", "limit": maxWaitingHosts})
	}
	if name == "" {
		name = f.nextHostName(u.ID)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return errf("internal", "%v", err)
	}
	expires := f.now().Add(24 * time.Hour)
	h := &ownedHostRec{owner: u.ID, freeMem: -1, OwnedHost: OwnedHost{
		ID: f.nextID(), Name: name, State: "registering", Provider: "metal",
		CreatedAt: f.now(), JoinToken: base64.RawURLEncoding.EncodeToString(raw), JoinTokenExpiresAt: &expires,
	}}
	if revive != nil {
		// Same row, same id, nothing of the machine it was.
		h.ID, h.CreatedAt = revive.ID, revive.CreatedAt
	}
	line := FakeHostInstall
	if f.hostInstallSet {
		line = f.hostInstall
	}
	// The step's argument is the join bundle (DECISIONS I-1380): the
	// token and the machine's place on the hub, which it needs before it
	// can reach the api at all. The fake runs no hub, so the WireGuard
	// half is canned -- but it is there, and the same size it is in
	// production (a bundle is about 340 characters, not the 60 a token
	// alone makes). A client's layout and its copy button are judged
	// against what a user will actually be handed.
	argument := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"token":"` + h.JoinToken +
		`","wg":{"private_key":"` + fakeWGKey + `","address":"10.255.0.9/16","edge_pubkey":"` + fakeWGKey +
		`","edge_endpoint":"edge.repose.test:51820"}}`))
	command := ""
	if strings.Contains(line, "%s") {
		command = strings.Replace(line, "%s", argument, 1)
	}
	h.Install = &OwnedHostInstall{Command: command, Argument: argument, URL: FakeDashboard + "/docs/own-hardware"}
	if revive != nil {
		*revive = *h
	} else {
		f.ownedHosts = append(f.ownedHosts, h)
	}
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
		if slugs := f.slugsOnHost(h.ID, u.ID); len(slugs) > 0 {
			word := "machines"
			if len(slugs) == 1 {
				word = "machine"
			}
			return errf("conflict", "%s still holds %d %s. A machine stays on the host it was made on, so remove them first: %s.",
				h.Name, len(slugs), word, strings.Join(slugs, ", ")).
				withDetail(map[string]any{"reason": "host_has_projects", "projects": slugs})
		}
		h.State = "retired"
		h.JoinToken = ""
		// Its snapshots were on its disk and go with it (I-1542).
		name := h.Name
		for _, p := range f.projects {
			for _, s := range p.snapshots {
				if s.hostID == h.ID && s.GoneWithHost == nil {
					s.GoneWithHost = &name
				}
			}
		}
		w.WriteHeader(204)
		return nil
	}
	return notFound("host " + id)
}

// snapshotsOn counts the live snapshots kept on a host's disk.
func (f *Fake) snapshotsOn(hostID string) int {
	n := 0
	for _, p := range f.projects {
		for _, s := range p.snapshots {
			if s.hostID == hostID && s.GoneWithHost == nil && (s.ExpiresAt == nil || s.ExpiresAt.After(f.now())) {
				n++
			}
		}
	}
	return n
}

// slugsOnHost names the account's live projects placed on the host, in
// name order so the refusal reads the same twice.
func (f *Fake) slugsOnHost(hostID, owner string) []string {
	var slugs []string
	for _, p := range f.projects {
		if p.owner == owner && !p.destroyed && p.HostID == hostID {
			slugs = append(slugs, p.Slug)
		}
	}
	sort.Strings(slugs)
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
	h := &ownedHostRec{owner: CannedUser.ID, freeMem: -1, OwnedHost: OwnedHost{
		ID: f.nextID(), Name: name, State: "ready", Provider: "mac", Arch: "aarch64",
		MemBytes: 24 << 30, VCPUs: 8, Cores: 8,
		LastHeartbeatAt: &now, RegisteredAt: &now, CreatedAt: now,
	}}
	f.ownedHosts = append(f.ownedHosts, h)
	return h.ID
}

// SetOwnedHostArch replaces a host's reported arch, so a layout test can
// verify that even a long unbroken arch never pushes Remove off a phone.
func (f *Fake) SetOwnedHostArch(id, arch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.ID == id {
			h.Arch = arch
		}
	}
}

// SetOwnedHostOffline marks a host unreachable and puts its last
// heartbeat quiet ago, which is what a machine switched off looks like.
// The duration is the point: a surface that says how long it has been
// quiet reads `last_heartbeat_at` and not the state.
func (f *Fake) SetOwnedHostOffline(id string, quiet time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.ID != id {
			continue
		}
		at := f.now().Add(-quiet)
		h.State, h.LastHeartbeatAt = "unreachable", &at
		// A host that stops answering takes every machine on it with
		// it: the api's sweep sets `host_unreachable` on each, which is
		// the field `repose ls`, `repose status` and the dashboard read
		// (I-1370). Without this a test of an offline host would have to
		// set the flag by hand and could set it on a project that is not
		// even on the host.
		for _, p := range f.projects {
			if !p.destroyed && p.HostID == id {
				p.HostUnreachable = true
			}
		}
	}
}

// SetProjectHost places a project on a host, which the real api's
// scheduler does at the create and the fake does not do at all.
func (f *Fake) SetProjectHost(projectID, hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.projects[projectID]; p != nil {
		p.HostID, p.HostName = hostID, f.ownedHostName(hostID)
	}
}

// ownedHostName is the name of an owned host by id, "" for a host
// repose runs (I-1392): what Project.host_name carries.
func (f *Fake) ownedHostName(hostID string) string {
	for _, h := range f.ownedHosts {
		if h.ID == hostID {
			return h.Name
		}
	}
	return ""
}

// placeNew is where an `own` project goes (DECISIONS I-1366, I-1390),
// answered at the create as the api answers it: a host of the account's
// own that is online; a `capacity` refusal that names the host and its
// state when none it owns is online, and every host when it owns several
// (I-1582); the fleet (no host yet, f.run picks the fleet's) when it owns
// none. `hosted` is the fleet whatever it owns. The refusal's detail is
// the api's: `reason` no_own_host, `host` and `host_state` (the oldest
// host's), and `hosted_allowed`, whether the plan sells memory on
// repose's servers.
//
// pin is the host the create named (`host`, I-1584): that host when it
// is online, else a refusal about it alone, never another host.
func (f *Fake) placeNew(u *userRec, class, placement string, pin *ownedHostRec) (string, *apiError) {
	if placement == "hosted" {
		return "", nil
	}
	// A plan that sells a host sells no fleet memory (I-1364), so `hosted`
	// is refused on it. De Morgan of !(set && above zero); the nil check
	// still short-circuits the deref.
	hostedAllowed := f.hostLimit == nil || *f.hostLimit <= 0
	if pin != nil {
		// The pinned host alone, under the same memory term (I-1586,
		// I-1630).
		if free, need := f.room(pin, class); pin.State == "ready" && free >= need {
			return pin.ID, nil
		}
		return "", errf("capacity", "your host %s %s", pin.Name, f.ownHostWhy(pin, class, false)).withDetail(map[string]any{
			"reason": "no_own_host", "host": pin.Name, "host_state": pin.State, "hosted_allowed": hostedAllowed})
	}
	var own []*ownedHostRec
	best, bestFree := "", int64(-1)
	for _, h := range f.ownedHosts {
		if h.owner == u.ID && h.State != "retired" && h.State != "lost" {
			// The api's memory term on a host the user owns, the memory
			// it reports free against a gigabyte and no class (I-1630),
			// and no CPU term (I-1586): the most free first, as the
			// scheduler orders.
			if free, need := f.room(h, class); h.State == "ready" && free >= need && free > bestFree {
				best, bestFree = h.ID, free
			}
			own = append(own, h)
		}
	}
	if best != "" {
		return best, nil
	}
	if len(own) == 0 {
		return "", nil
	}
	msg := "your host " + own[0].Name + " " + f.ownHostWhy(own[0], class, false)
	if len(own) > 1 {
		why := make([]string, len(own))
		for i, h := range own {
			why[i] = h.Name + " " + f.ownHostWhy(h, class, true)
		}
		msg = "none of your hosts can start a machine: " + strings.Join(why, "; ")
		for _, h := range own {
			if h.oldHostd {
				msg = "none of your hosts can take " + aClass(class) + " machine (" + gbText(int64(classGB[class])<<30) + "): " + strings.Join(why, "; ")
				break
			}
		}
	}
	return "", errf("capacity", "%s", msg).withDetail(map[string]any{
		"reason": "no_own_host", "host": own[0].Name, "host_state": own[0].State, "hosted_allowed": hostedAllowed})
}

// hostPin is the api's createHostPin (I-1584): POST /projects' `host`,
// one live host the account owns by id or name, refused as `invalid`
// with reason `host` beside `hosted` or when the account has no such
// host. nil is no pin.
func (f *Fake) hostPin(u *userRec, host *string, placement string) (*ownedHostRec, *apiError) {
	if host == nil || strings.TrimSpace(*host) == "" {
		return nil, nil
	}
	want := strings.TrimSpace(*host)
	if placement == "hosted" {
		return nil, invalid("host names one of your hosts and placement hosted is repose's servers; send one of them").withDetail(map[string]any{"reason": "host"})
	}
	for _, h := range f.ownedHosts {
		if h.owner == u.ID && (h.ID == want || h.Name == want) && h.State != "retired" && h.State != "lost" {
			return h, nil
		}
	}
	return nil, invalid("you have no host called %s", want).withDetail(map[string]any{"reason": "host", "host": want})
}

// ownHostWhy is the api's ops.Engine.ownHostWhy: why one host took no
// placement, as the words after its name.
func (f *Fake) ownHostWhy(h *ownedHostRec, class string, short bool) string {
	switch h.State {
	case "registering":
		return "has not registered yet"
	case "unreachable":
		quiet := "90s"
		if h.LastHeartbeatAt != nil {
			quiet = quietFor(f.now().Sub(*h.LastHeartbeatAt))
		}
		return "has not answered for " + quiet
	case "draining":
		if short {
			return "is being removed"
		}
		return "is being removed, so nothing can be placed on it"
	}
	// The api names the figure that refused (I-1586); the fake has no
	// disk, so memory is the only one it can. Since I-1630 that is what
	// the host reports free against a gigabyte, with no class.
	free, need := f.room(h, class)
	if free >= need {
		if short {
			return "has no room"
		}
		if h.oldHostd {
			return "has no room for " + aClass(class) + " machine"
		}
		return "has no room for a machine"
	}
	if short {
		return "has " + gbText(free) + " free"
	}
	if h.oldHostd {
		return "has " + gbText(free) + " of memory free for machines, and " + aClass(class) + " machine needs " + gbText(need) +
			". A machine holds its memory while it runs, so stopping one frees room for it"
	}
	return "has " + gbText(max(free, 0)) + " of memory free, and a machine starts there when it has at least " + gbText(ownedStartFreeBytes) +
		" free. Stopping a machine frees the memory it uses"
}

// ownedStartFreeBytes is scheduler.OwnedStartFreeBytes, the free memory
// an owned host must report for a machine to go there (I-1630).
const ownedStartFreeBytes int64 = 1 << 30

// aClass is a size class with its article, as the api's: "a small", "an xl".
func aClass(class string) string {
	if class == "xl" {
		return "an xl"
	}
	return "a " + class
}

// gbText is the api's ops.gbText: GiB, whole or to one decimal.
func gbText(b int64) string {
	t := strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64)
	return strings.TrimSuffix(t, ".0") + " GB"
}

// quietFor is how long a host has been silent, in the api's shortest
// true unit: "14m", "3h", "2d".
func quietFor(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
