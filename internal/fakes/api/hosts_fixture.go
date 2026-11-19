package api

import "time"

// Fixtures for a host that has already joined and for one that has
// stopped answering (DECISIONS I-1370). hosts.go has the routes and the
// setters a test of the listing needs; these three are what a test of
// `repose ls`, `repose status` and the dashboard needs, because they
// read a machine's own host and not the hosts page.

// AddOwnedHost adds a host of the canned user's that has registered and
// is answering, and returns its id. `POST /hosts` only ever makes a
// `registering` one, and a test of an offline Mac starts from a Mac
// that was online.
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

// SetOwnedHostOffline puts a host in `unreachable` and moves its last
// heartbeat back by quiet, as the api's sweep does for a host that has
// stopped answering. How long it has been quiet is the point: the line
// a user reads carries the figure.
func (f *Fake) SetOwnedHostOffline(id string, quiet time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.ID == id {
			h.State = "unreachable"
			seen := f.now().Add(-quiet)
			h.LastHeartbeatAt = &seen
		}
	}
}

// SetProjectHost moves a project onto a host, as placement does for an
// account that owns one. The fake's create places nothing.
func (f *Fake) SetProjectHost(projectID, hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.HostID = hostID
	}
	for _, h := range f.ownedHosts {
		if h.ID != hostID {
			continue
		}
		h.Projects, h.RunningProjects = 0, 0
		for _, p := range f.projects {
			if p.owner != h.owner || p.destroyed || p.HostID != h.ID {
				continue
			}
			h.Projects++
			if p.State == "running" {
				h.RunningProjects++
			}
		}
	}
}
