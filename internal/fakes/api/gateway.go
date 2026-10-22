package api

import (
	"errors"
	"time"
)

// Test helpers for the gateway (workstream 06): direct control over what
// the internal routes answer, without going through the user routes.

// SessionReport is one POST /internal/sessions the fake received.
type SessionReport struct {
	ProjectID  string
	Event      string
	CertSerial uint64
	SessionID  string
}

// SetHosts replaces what GET /internal/hosts returns (nil restores the
// canned host).
func (f *Fake) SetHosts(hosts []Host) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = hosts
}

// CreateProject creates a running project for the canned user and returns
// it. The name must be valid per POST /projects.
func (f *Fake) CreateProject(name, class string) (Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, e := f.create(f.users[CannedUser.ID], name, "", class)
	if e != nil {
		return Project{}, errors.New(e.Message)
	}
	return p.Project, nil
}

// CreateProjectFor creates a running project owned by the given user id
// (the user must exist in Options.Users).
func (f *Fake) CreateProjectFor(userID, name, class string) (Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return Project{}, errors.New("user not found")
	}
	p, e := f.create(u, name, "", class)
	if e != nil {
		return Project{}, errors.New(e.Message)
	}
	return p.Project, nil
}

// SetState sets a project's state (and clears guest_ip when it is not
// running), as the api would after an op.
func (f *Fake) SetState(projectID, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok {
		return
	}
	p.State = state
	if state != "running" {
		p.GuestIP = ""
	} else if p.GuestIP == "" {
		f.ipSeq++
		p.GuestIP = "10.64.4." + itoa(10+f.ipSeq)
	}
}

// SetIdle marks a project idle since the given time at the given hourly
// rate, or clears it with a nil since (DECISIONS I-262).
func (f *Fake) SetIdle(projectID string, since *time.Time, hourlyCents int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok {
		return
	}
	if since == nil {
		p.Idle = nil
		return
	}
	p.Idle = &Idle{Since: *since, HourlyCents: hourlyCents}
}

// SetResting marks a running project frozen since the given time, or
// clears it with a nil since (the mock of DECISIONS I-683).
func (f *Fake) SetResting(projectID string, since *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok {
		return errors.New("no project " + projectID)
	}
	if since == nil {
		p.Resting = nil
		return nil
	}
	p.Resting = &Resting{Since: *since}
	return nil
}

// Wakes is how many POST /internal/wake the fake has answered
// (DECISIONS I-818): one window of an editor opening several connections
// must be one wake.
func (f *Fake) Wakes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wakes
}

// SetWakeFails makes every POST /internal/wake answer an error, so a
// gateway test can see what a wake that fails does to a dial and what the
// user is told (DECISIONS I-818).
func (f *Fake) SetWakeFails(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakeFails = v
}

// SetGuestIP sets a running project's guest address (tests point it at an
// in-process sshd).
func (f *Fake) SetGuestIP(projectID, ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.GuestIP = ip
	}
}

// RevokeSerial revokes a certificate by serial regardless of owner, as
// POST /certs/revoke would; it appears in GET /internal/revoked.
func (f *Fake) RevokeSerial(serial uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.certs[serial]
	if ok && c.revoked {
		return
	}
	if ok {
		c.revoked = true
	}
	f.revoked = append(f.revoked, revocation{serial: serial, at: f.now()})
}

// GatewayCertCalls is how many POST /internal/gateway-certs the fake has
// answered.
func (f *Fake) GatewayCertCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gatewayCerts
}

// SessionReports lists POST /internal/sessions calls in order.
func (f *Fake) SessionReports() []SessionReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SessionReport, len(f.sessions))
	copy(out, f.sessions)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// AddEvent records an event on a project at ts, as a guest's hook would.
func (f *Fake) AddEvent(projectID string, ts time.Time, kind, summary string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.events = append(p.events, &Event{ID: f.nextID(), TS: ts, Kind: kind, Summary: summary})
	}
}

// SetAgents sets the agents a running project's signals report.
func (f *Fake) SetAgents(projectID string, agents []AgentSignal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		if p.Signals == nil {
			p.Signals = &Signals{GuestdOK: true}
		}
		p.Signals.Agents = agents
	}
}

// SetHostUnreachable sets a project's host_unreachable flag, as the api's
// sweep does for a host silent for 90 s (a host restart, its guests
// frozen across it, I-652) and its next heartbeat clears.
func (f *Fake) SetHostUnreachable(projectID string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.HostUnreachable = on
	}
}

// SetLastError sets a project's last_error ("code: message"), or clears
// it with "".
func (f *Fake) SetLastError(projectID, lastError string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[projectID]
	if !ok {
		return
	}
	if lastError == "" {
		p.LastError = nil
		return
	}
	p.LastError = &lastError
}

// OpKinds lists the kinds of the ops sent for a project, in no order.
func (f *Fake) OpKinds(projectID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kinds []string
	for _, o := range f.ops {
		if o.projectID == projectID {
			kinds = append(kinds, o.kind)
		}
	}
	return kinds
}

// OnGet runs fn after each GET of the project is answered, so a test
// changes the fake once a command has read a state (or printed a line)
// rather than on a timer it can lose to under load. fn runs under the
// fake's lock with the project itself: it changes fields directly and
// calls no other method of f. fn returning true removes it, as does a
// nil fn.
func (f *Fake) OnGet(projectID string, fn func(p *Project) (done bool)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fn == nil {
		delete(f.onGet, projectID)
		return
	}
	if f.onGet == nil {
		f.onGet = map[string]func(*Project) bool{}
	}
	f.onGet[projectID] = fn
}
