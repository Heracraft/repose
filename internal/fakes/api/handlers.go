package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // PATCH /me validates tz without depending on the host's zoneinfo

	"github.com/heracraft/repose/internal/ca/sshca"
	"github.com/heracraft/repose/internal/menu"
	"github.com/heracraft/repose/internal/multiplexer"
)

var (
	nameRe       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	secretNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	slugStripRe  = regexp.MustCompile(`[^a-z0-9-]+`)
	slugRunRe    = regexp.MustCompile(`-+`)
)

const (
	maxFragmentBytes = 256 << 10
	maxSecretBytes   = 64 << 10
	retentionDays    = 30

	// forceEvalErrorMarker in a fragment makes putConfig answer the exact
	// first canonical eval_failed message from nix-build-contract.md
	// instead of applying, so a consumer can test the error path without a
	// real Nix evaluation.
	forceEvalErrorMarker = "repose-force-eval-error"
)

var reservedSecretNames = map[string]bool{
	"ssh_host_ed25519_key":          true,
	"ssh_host_ed25519_key-cert.pub": true,
	"user_ca.pub":                   true,
}

var classes = map[string]int64{
	"small": 20 << 30,
	"large": 40 << 30,
	"xl":    80 << 30,
}

// menuCatalog is the real catalog (internal/menu): the fake validates and
// renders a selection exactly as the api does, {package} items included
// (DECISIONS I-220), so the CLI and dashboard tests see the real shape.
var menuCatalog = sync.OnceValues(menu.Load)

var buildLines = []string{"evaluating configuration", "building", "built"}

// Users.

type meView struct {
	User
	TZ        string      `json:"tz"`
	CreatedAt time.Time   `json:"created_at"`
	Billing   billingView `json:"billing"`
	Limits    limitsView  `json:"limits"`
	Notify    notifyView  `json:"notify"`
	// Waitlist is the place on the waitlist, null when not waiting
	// (DECISIONS I-269, I-290).
	Waitlist *WaitlistPlace `json:"waitlist"`
}

// billingView is GET /me's billing block: a projection of the subscription
// (I-289). trial_credit_cents is always 0 and has_card means a
// subscription exists; both are kept one release.
type billingView struct {
	Status           string     `json:"status"`
	Plan             *string    `json:"plan"`
	Seats            int        `json:"seats"`
	PeriodEnd        *time.Time `json:"period_end"`
	TrialEnd         *time.Time `json:"trial_end"`
	CancelAt         *time.Time `json:"cancel_at"`
	HasCard          bool       `json:"has_card"`
	TrialCreditCents int64      `json:"trial_credit_cents"`
}

type limitsView struct {
	Projects int `json:"projects"`
	XL       int `json:"xl"`
	MemoryGB int `json:"memory_gb"`
	DiskGB   int `json:"disk_gb"`
	EgressGB int `json:"egress_gb"`
}

type notifyView struct {
	Email   bool    `json:"email"`
	NtfyURL *string `json:"ntfy_url"`
}

func (f *Fake) meOf(u *userRec) meView {
	v := meView{User: u.User, TZ: u.TZ, CreatedAt: u.CreatedAt,
		Billing:  f.meBilling(),
		Limits:   f.meLimits(),
		Notify:   notifyView{Email: u.NotifyEmail},
		Waitlist: f.bill.waitlist}
	if u.NtfyURL != "" {
		url := u.NtfyURL
		v.Notify.NtfyURL = &url
	}
	return v
}

func (f *Fake) getMe(w http.ResponseWriter, r *http.Request) *apiError {
	writeJSON(w, http.StatusOK, f.meOf(userFrom(r)))
	return nil
}

func (f *Fake) patchMe(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		TZ     *string `json:"tz"`
		Notify *struct {
			Email   *bool           `json:"email"`
			NtfyURL json.RawMessage `json:"ntfy_url"`
		} `json:"notify"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	u := userFrom(r)
	if body.TZ != nil {
		if _, err := time.LoadLocation(*body.TZ); err != nil || *body.TZ == "" {
			return invalid("tz: unknown time zone")
		}
		u.TZ = *body.TZ
	}
	if body.Notify != nil {
		if body.Notify.Email != nil {
			u.NotifyEmail = *body.Notify.Email
		}
		if len(body.Notify.NtfyURL) > 0 {
			var url *string
			if err := json.Unmarshal(body.Notify.NtfyURL, &url); err != nil {
				return invalid("notify.ntfy_url: string or null")
			}
			if url == nil {
				u.NtfyURL = ""
			} else if !strings.HasPrefix(*url, "https://") && !strings.HasPrefix(*url, "http://") {
				return invalid("notify.ntfy_url: must be an http(s) URL")
			} else {
				u.NtfyURL = *url
			}
		}
	}
	writeJSON(w, http.StatusOK, f.meOf(u))
	return nil
}

func (f *Fake) deleteMe(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	u.Cancelling = true
	for _, p := range f.projects {
		if p.owner == u.ID && !p.destroyed && p.State == "running" {
			f.stop(p, true)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelling", "retention_days": retentionDays})
	return nil
}

// notifyUnsubscribe fakes the signed-token check with the token being the
// user id itself: this fixture has no HMAC key to sign against, and no CLI
// or dashboard code ever calls this route (a user's browser does, from an
// email), so a real signature has nothing to prove here.
func (f *Fake) notifyUnsubscribe(w http.ResponseWriter, r *http.Request) *apiError {
	// The token is the user id here. GET only shows the confirmation, as
	// the api does (I-442); POST turns email off.
	u, ok := f.users[r.FormValue("token")]
	if !ok {
		return errf("invalid", "this unsubscribe link is not valid")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodPost {
		_, _ = w.Write([]byte("Unsubscribe from repose email notifications? POST to this URL to confirm.\n"))
		return nil
	}
	u.NotifyEmail = false
	_, _ = w.Write([]byte("You have been unsubscribed from repose email notifications.\n"))
	return nil
}

func (f *Fake) notifyTest(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	res := map[string]string{"email": "error", "ntfy": "error"}
	if u.NotifyEmail {
		res["email"] = "ok"
	}
	if u.NtfyURL != "" {
		res["ntfy"] = "ok"
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// Projects.

func slugOf(name string) string {
	s := slugStripRe.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(slugRunRe.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// project finds a live project the user owns; anything else is not_found
// so a stranger cannot tell a foreign id from a missing one.
func (f *Fake) project(u *userRec, id string) (*project, *apiError) {
	p, ok := f.projects[id]
	if !ok || p.owner != u.ID || p.destroyed {
		return nil, notFound("project")
	}
	return p, nil
}

// projectAny is project, but a destroyed one is still visible (its last
// snapshot is kept for retentionDays).
func (f *Fake) projectAny(u *userRec, id string) (*project, *apiError) {
	p, ok := f.projects[id]
	if !ok || p.owner != u.ID {
		return nil, notFound("project")
	}
	return p, nil
}

func (f *Fake) userProjects(u *userRec) []*project {
	var out []*project
	for _, p := range f.projects {
		if p.owner == u.ID && !p.destroyed {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *Fake) event(p *project, kind, agent, summary string) *Event {
	ev := &Event{ID: f.nextID(), TS: f.now(), Kind: kind, Agent: agent, Summary: summary}
	p.events = append(p.events, ev)
	return ev
}

func (f *Fake) newOp(p *project, kind string) *op {
	o := &op{id: f.nextID(), projectID: p.ID, kind: kind}
	o.State = "done"
	o.LogURL = fmt.Sprintf("%s/v1/projects/%s/ops/%s/log", f.Server.URL, p.ID, o.id)
	f.ops[o.id] = o
	return o
}

func opResult(w http.ResponseWriter, o *op) *apiError {
	writeJSON(w, http.StatusAccepted, map[string]string{"op_id": o.id})
	return nil
}

func (f *Fake) run(p *project) {
	now := f.now()
	p.State = "running"
	p.HostID = hostID
	if p.GuestIP == "" {
		f.ipSeq++
		p.GuestIP = fmt.Sprintf("10.64.4.%d", 10+f.ipSeq)
	}
	p.StartedAt = &now
	p.Signals = &Signals{Agents: []AgentSignal{}, GuestdOK: true}
	f.event(p, "guest.started", "", "guest started on "+hostID)
}

func (f *Fake) snapshot(p *project, reason string) *Snapshot {
	s := &Snapshot{ID: f.nextID(), CreatedAt: f.now(), Bytes: p.DiskUsedBytes, Reason: reason}
	p.snapshots = append(p.snapshots, s)
	at := s.CreatedAt
	p.LastSnapshotAt = &at
	f.event(p, "snapshot.created", "", "snapshot taken ("+reason+")")
	return s
}

// stop stops p and returns the snapshot it took, or nil.
func (f *Fake) stop(p *project, snapshot bool) *Snapshot {
	var s *Snapshot
	if p.State == "running" && snapshot {
		s = f.snapshot(p, "stop")
	}
	p.State = "stopped"
	p.StartedAt = nil
	p.Signals = nil
	p.GuestIP = ""
	f.event(p, "guest.stopped", "", "guest stopped")
	return s
}

func (f *Fake) create(u *userRec, name, remoteURL, class string) (*project, *apiError) {
	return f.createGated(u, name, remoteURL, class, class)
}

// createGated is create with the class the memory gate is asked about:
// "" for a project that is not started, as a fork with start: false
// (the api's gateClass).
func (f *Fake) createGated(u *userRec, name, remoteURL, class, gateClass string) (*project, *apiError) {
	if !nameRe.MatchString(name) {
		return nil, invalid("name: must match [A-Za-z0-9._-]{1,64}")
	}
	if _, ok := classes[class]; !ok {
		return nil, invalid("class: must be one of small, large, xl")
	}
	slug := slugOf(name)
	if slug == "" {
		return nil, invalid("name: yields an empty slug")
	}
	// The billing gate runs on every create, fork and restore as on a
	// start (I-290): no plan, no compute; a plan, its memory and disk.
	if e := f.gate(u, gateClass, &diskAsk{addGB: newProjectHeldGB, volumeBytes: classes[class]}, nil); e != nil {
		return nil, e
	}
	if e := f.projectCap(u, 1); e != nil {
		return nil, e
	}
	for _, p := range f.userProjects(u) {
		switch {
		case p.Name == name:
			return nil, errf("conflict", "a project named %q exists", name).withDetail(map[string]any{"project_id": p.ID})
		case p.Slug == slug:
			return nil, errf("conflict", "project %q has the same slug %q", p.Name, slug).withDetail(map[string]any{"project_id": p.ID})
		case remoteURL != "" && p.RemoteURL == remoteURL:
			return nil, errf("conflict", "project %q already tracks %s", p.Name, remoteURL).withDetail(map[string]any{"project_id": p.ID})
		}
	}
	now := f.now()
	p := &project{
		Project: Project{
			ID: f.nextID(), Name: name, Slug: slug, RemoteURL: remoteURL, Class: class,
			State: "creating", AgentDefault: "claude", BaseVersion: baseVersion, Multiplexer: multiplexer.Tmux,
			VolumeBytes: classes[class], DiskUsedBytes: 1 << 30, CreatedAt: now,
			RootUsedBytes: 1 << 30, RootSizeBytes: classes[class] - classes[class]/40,
		},
		owner:     u.ID,
		secrets:   map[string]*SecretMeta{},
		eventKeys: map[string]bool{},
	}
	rev := &Revision{ID: f.nextID(), CreatedAt: now, Status: "applied", BaseVersion: baseVersion, AppliedAt: &now}
	p.revisions = append(p.revisions, rev)
	p.ConfigRevisionID = rev.ID
	f.projects[p.ID] = p
	f.event(p, "project.created", "", "project created as "+class)
	f.run(p)
	return p, nil
}

func (f *Fake) listProjects(w http.ResponseWriter, r *http.Request) *apiError {
	out := []Project{}
	for _, p := range f.userProjects(userFrom(r)) {
		out = append(out, p.Project)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) createProject(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Name         string `json:"name"`
		RemoteURL    string `json:"remote_url"`
		Class        string `json:"class"`
		TZ           string `json:"tz"`
		AgentDefault string `json:"agent_default"`
		ExpiresIn    *int64 `json:"expires_in_s"`
		// PersonalOptOut keeps machine.nix off (I-490).
		PersonalOptOut bool `json:"personal_opt_out"`
		// Multiplexer is tmux (the default) or herdr (I-502).
		Multiplexer *string `json:"multiplexer"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if body.Multiplexer != nil {
		if e := checkMultiplexer(*body.Multiplexer); e != nil {
			return e
		}
		if *body.Multiplexer == multiplexer.Herdr {
			if e := f.herdrGate(slugOf(body.Name), "", true); e != nil {
				return e
			}
		}
	}
	if body.TZ != "" {
		if _, err := time.LoadLocation(body.TZ); err != nil {
			return invalid("tz: unknown time zone")
		}
	}
	// A temporary project (DECISIONS I-347): 600..86400 seconds, no remote.
	if body.ExpiresIn != nil && (*body.ExpiresIn < 600 || *body.ExpiresIn > 86400) {
		return invalid("expires_in_s must be between 600 and 86400")
	}
	if body.ExpiresIn != nil && body.RemoteURL != "" {
		return invalid("a temporary project has no remote_url")
	}
	u := userFrom(r)
	p, e := f.create(u, body.Name, body.RemoteURL, body.Class)
	if e != nil {
		return e
	}
	if body.ExpiresIn != nil {
		t := f.now().Add(time.Duration(*body.ExpiresIn) * time.Second)
		p.ExpiresAt = &t
	}
	if body.TZ != "" {
		z := body.TZ
		p.TZ = &z
	}
	if body.AgentDefault != "" {
		p.AgentDefault = body.AgentDefault
	}
	p.PersonalOptOut = body.PersonalOptOut
	if body.Multiplexer != nil {
		p.Multiplexer = *body.Multiplexer
	}
	if cur := f.currentPersonal(u); cur != nil && cur.Fragment != "" && !body.PersonalOptOut {
		if rev := p.revision(p.ConfigRevisionID); rev != nil {
			rev.Personal, rev.personalText = true, cur.Fragment
		}
	}
	if f.opts.CreateDelay > 0 {
		// Under f.mu already (ServeHTTP); only the goroutine takes it.
		o := f.newOp(p, "create")
		o.State = "running"
		p.State = "creating"
		p.OpID = o.id
		out := p.Project
		// The engine's create op builds first, so a project read back a
		// moment after the create is "building", not "creating"
		// (DECISIONS I-114); the fake shows the same sequence.
		p.State = "building"
		go func() {
			time.Sleep(f.opts.CreateDelay)
			f.mu.Lock()
			defer f.mu.Unlock()
			p.OpID = ""
			f.run(p)
			o.State = "done"
		}()
		writeJSON(w, http.StatusCreated, out)
		return nil
	}
	writeJSON(w, http.StatusCreated, p.Project)
	return nil
}

func (f *Fake) getProject(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	writeJSON(w, http.StatusOK, p.Project)
	return nil
}

func (f *Fake) patchProject(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Class           *string `json:"class"`
		HoldBaseUpdates *bool   `json:"hold_base_updates"`
		AgentDefault    *string `json:"agent_default"`
		TZ              *string `json:"tz"`
		// Only null: `repose keep` (DECISIONS I-347).
		ExpiresAt json.RawMessage `json:"expires_at"`
		// A temporary project's new lifetime from now (I-612).
		ExpiresIn *int64 `json:"expires_in_s"`
		// machine.nix off or back on (I-490).
		PersonalOptOut *bool `json:"personal_opt_out"`
		// What the next start runs (I-502).
		Multiplexer *string `json:"multiplexer"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if body.Multiplexer != nil {
		// Checked before anything changes, as the api does.
		if e := checkMultiplexer(*body.Multiplexer); e != nil {
			return e
		}
		if p.State == "destroying" {
			return errf("conflict", "%s is being destroyed", p.Slug)
		}
		if *body.Multiplexer == multiplexer.Herdr {
			if e := f.herdrGate(p.Slug, p.BaseVersion, false); e != nil {
				return e
			}
		}
	}
	if len(body.ExpiresAt) > 0 && body.ExpiresIn != nil {
		return invalid("expires_at: null and expires_in_s cannot both be sent")
	}
	if len(body.ExpiresAt) > 0 {
		if strings.TrimSpace(string(body.ExpiresAt)) != "null" {
			return invalid("expires_at can only be set to null, which keeps a temporary project")
		}
		if p.State == "destroying" {
			return errf("conflict", "%s is already being destroyed", p.Slug)
		}
		p.ExpiresAt = nil
	}
	if body.ExpiresIn != nil {
		if *body.ExpiresIn < 600 || *body.ExpiresIn > 86400 {
			return invalid("expires_in_s must be between 600 and 86400")
		}
		if p.ExpiresAt == nil {
			return invalid("expires_in_s applies only to a temporary project")
		}
		if p.State == "destroying" {
			return errf("conflict", "%s is already being destroyed", p.Slug)
		}
		t := f.now().Add(time.Duration(*body.ExpiresIn) * time.Second)
		p.ExpiresAt = &t
	}
	if body.TZ != nil {
		if _, err := time.LoadLocation(*body.TZ); err != nil || *body.TZ == "" {
			return invalid("tz: unknown time zone")
		}
		z := *body.TZ
		p.TZ = &z
	}
	if body.Class != nil && *body.Class != p.Class {
		if _, ok := classes[*body.Class]; !ok {
			return invalid("class: must be one of small, large, xl")
		}
		if p.State != "stopped" {
			return invalid("class: change requires the project to be stopped").withDetail(map[string]any{"state": p.State})
		}
		p.Class = *body.Class
	}
	if body.HoldBaseUpdates != nil {
		p.HoldBaseUpdates = *body.HoldBaseUpdates
	}
	if body.PersonalOptOut != nil {
		f.setPersonalOptOut(userFrom(r), p, *body.PersonalOptOut)
	}
	if body.AgentDefault != nil {
		if *body.AgentDefault == "" {
			return invalid("agent_default: must not be empty")
		}
		p.AgentDefault = *body.AgentDefault
	}
	if body.Multiplexer != nil {
		p.Multiplexer = *body.Multiplexer
	}
	writeJSON(w, http.StatusOK, p.Project)
	return nil
}

func (f *Fake) destroyProject(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	o := f.newOp(p, "destroy")
	if f.opts.DestroyDelay > 0 {
		// Under f.mu already (ServeHTTP); only the goroutine takes it.
		if p.State == "running" {
			f.stop(p, false)
		}
		o.State = "running"
		p.State = "destroying"
		p.OpID = o.id
		p.GuestIP = ""
		go func() {
			time.Sleep(f.opts.DestroyDelay)
			f.mu.Lock()
			defer f.mu.Unlock()
			p.OpID = ""
			f.finishDestroy(p)
			o.State = "done"
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"op_id": o.id, "state": o.State})
		return nil
	}
	f.finishDestroy(p)
	// api.md: 202 {op_id, state} (I-156); the fake's ops finish at once.
	writeJSON(w, http.StatusAccepted, map[string]string{"op_id": o.id, "state": o.State})
	return nil
}

// finishDestroy is the end of a destroy: the final snapshot, the volume
// gone, the project destroyed.
func (f *Fake) finishDestroy(p *project) {
	if p.State == "running" {
		f.stop(p, false)
	}
	temporary := p.ExpiresAt != nil
	if !temporary {
		// The real destroy stops, then snapshots the stopped volume (I-165).
		f.snapshot(p, "stop")
	}
	p.State = "destroyed"
	p.HostID = ""
	p.destroyed = true
	p.destroyedAt = f.now()
	p.retained = f.now().Add(retentionDays * 24 * time.Hour)
	if temporary {
		// A temporary project keeps nothing (I-347): every snapshot it has
		// expires now.
		p.retained = f.now()
	}
	for _, s := range p.snapshots {
		if s.ExpiresAt == nil || temporary {
			t := p.retained
			s.ExpiresAt = &t
		}
	}
	if temporary {
		f.event(p, "project.destroyed", "", "volume deleted, no snapshot kept (temporary)")
		return
	}
	f.event(p, "project.destroyed", "", "volume deleted, last snapshot kept 30 days")
}

// ExpireTemporary is the api's reaper at the fake's now (DECISIONS
// I-347): every temporary project whose expires_at has passed is
// destroyed, with no snapshot, and records temp_destroyed. It returns the
// slugs destroyed. The fake has no samples, so nothing holds a destroy.
func (f *Fake) ExpireTemporary() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.projects {
		if p.destroyed || p.ExpiresAt == nil || p.ExpiresAt.After(f.now()) {
			continue
		}
		f.finishDestroy(p)
		f.event(p, "temp_destroyed", "", p.Slug+" was temporary and its time ran out, so it was destroyed. No snapshot was kept.")
		out = append(out, p.Slug)
	}
	sort.Strings(out)
	return out
}

// SetExpiresAt moves a temporary project's end (tests), or makes a
// project temporary.
func (f *Fake) SetExpiresAt(projectID string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		p.ExpiresAt = &t
	}
}

func (f *Fake) startProject(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if p.State == "creating" || p.State == "starting" {
		// What the api answers while the create op is still running.
		return errf("conflict", "%s is already starting", p.Slug)
	}
	if p.State != "running" {
		if e := f.gate(userFrom(r), p.Class, nil, p); e != nil {
			return e
		}
	}
	o := f.newOp(p, "start")
	restart := p.State == "error" // api.md: a start from error is a restart (I-157)
	if p.State != "running" && f.opts.StartDelay > 0 {
		// Under f.mu already (ServeHTTP); only the goroutine takes it.
		o.State = "running"
		o.phase = "start_guest"
		o.LogURL = "" // the api has a build log for build and create ops only
		p.State = "starting"
		p.OpID = o.id
		go func() {
			time.Sleep(f.opts.StartDelay)
			f.mu.Lock()
			defer f.mu.Unlock()
			p.OpID = ""
			f.run(p)
			o.State = "done"
			o.phase = ""
			f.endStart(p, o)
		}()
	} else if p.State != "running" {
		f.run(p)
		f.endStart(p, o)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": o.id, "restart": restart})
	return nil
}

func (f *Fake) stopProject(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Snapshot *bool `json:"snapshot"`
	}
	if e := decodeBody(r, &body, true); e != nil {
		return e
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	o := f.newOp(p, "stop")
	if p.State != "stopped" {
		// The api's stop op carries the snapshot it recorded, as a
		// snapshot op does.
		if s := f.stop(p, body.Snapshot == nil || *body.Snapshot); s != nil {
			o.Result = map[string]any{"snapshot_id": s.ID}
		}
	}
	return opResult(w, o)
}

func (f *Fake) findOp(u *userRec, projectID, opID string) (*project, *op, *apiError) {
	p, e := f.projectAny(u, projectID)
	if e != nil {
		return nil, nil, e
	}
	o, ok := f.ops[opID]
	if !ok || o.projectID != p.ID {
		return nil, nil, notFound("op")
	}
	return p, o, nil
}

func (f *Fake) getOp(w http.ResponseWriter, r *http.Request) *apiError {
	if r.URL.Query().Get("wait") != "" {
		return f.getOpWait(w, r) // dispatched without mu (ServeHTTP)
	}
	p, o, e := f.findOp(userFrom(r), r.PathValue("id"), r.PathValue("op_id"))
	if e != nil {
		return e
	}
	writeJSON(w, http.StatusOK, f.opView(p, o))
	return nil
}

// opView is the op as GET answers it. Callers hold mu.
func (f *Fake) opView(p *project, o *op) Op {
	v := o.Op
	if f.opts.NoLongPoll {
		return v
	}
	v.ProjectState = p.State
	v.Phase = o.phase
	v.Version = o.State + "." + o.phase + "." + p.State
	return v
}

// getOpWait is the op read with ?wait (I-236): held until the op's
// version differs from ?seen (or from the first read), the op finishes,
// or the wait (capped at OpWaitMax) runs out. It takes mu per read.
func (f *Fake) getOpWait(w http.ResponseWriter, r *http.Request) *apiError {
	q := r.URL.Query()
	wait, err := time.ParseDuration(q.Get("wait"))
	if err != nil {
		n, nerr := strconv.Atoi(q.Get("wait"))
		if nerr != nil || n < 0 {
			return invalid("wait: a duration such as 20s")
		}
		wait = time.Duration(n) * time.Second
	}
	if wait < 0 {
		return invalid("wait: a duration such as 20s")
	}
	wait = min(wait, OpWaitMax)
	read := func() (Op, *apiError) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p, o, e := f.findOp(userFrom(r), r.PathValue("id"), r.PathValue("op_id"))
		if e != nil {
			return Op{}, e
		}
		return f.opView(p, o), nil
	}
	v, e := read()
	if e != nil {
		return e
	}
	if f.opts.NoLongPoll {
		writeJSON(w, http.StatusOK, v)
		return nil
	}
	base := q.Get("seen")
	if base == "" {
		base = v.Version
	}
	deadline := time.Now().Add(wait)
	for v.Version == base && v.State != "done" && v.State != "error" && time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			return nil
		case <-time.After(100 * time.Millisecond): // the api re-reads every 100 ms
		}
		if v, e = read(); e != nil {
			return e
		}
	}
	w.Header().Set(LongPollHeader, "1")
	writeJSON(w, http.StatusOK, v)
	return nil
}

// opLog streams the canned build log as SSE. It runs outside mu (see
// ServeHTTP) and so takes it for the lookup only.
func (f *Fake) opLog(w http.ResponseWriter, r *http.Request) *apiError {
	since := 0
	if s := r.URL.Query().Get("since"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return invalid("since: must be a non-negative integer")
		}
		since = n
	}
	f.mu.Lock()
	_, _, e := f.findOp(userFrom(r), r.PathValue("id"), r.PathValue("op_id"))
	f.mu.Unlock()
	if e != nil {
		return e
	}
	var frames []string
	for i, line := range buildLines {
		seq := i + 1
		if seq <= since {
			continue
		}
		b, err := json.Marshal(map[string]any{"seq": seq, "line": line})
		if err != nil {
			return errf("internal", "encoding log line")
		}
		frames = append(frames, fmt.Sprintf("id: %d\ndata: %s\n\n", seq, b))
	}
	frames = append(frames, "event: done\ndata: {\"state\":\"done\"}\n\n")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher) // httptest's recorder and server both flush; a nil flusher only delays delivery
	for _, fr := range frames {
		if _, err := fmt.Fprint(w, fr); err != nil {
			return nil // the client hung up mid-stream; headers are already out
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}

func (f *Fake) resizeProject(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		VolumeBytes int64 `json:"volume_bytes"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if body.VolumeBytes <= p.VolumeBytes {
		return invalid("volume_bytes: volumes only grow").withDetail(map[string]any{"volume_bytes": p.VolumeBytes})
	}
	if e := f.gate(userFrom(r), "", &diskAsk{volumeBytes: body.VolumeBytes}, nil); e != nil {
		return e
	}
	o := f.newOp(p, "resize")
	p.VolumeBytes = body.VolumeBytes
	// The guest's growfs follows a resize, so its root filesystem grows too.
	p.RootSizeBytes = body.VolumeBytes - body.VolumeBytes/40
	f.event(p, "volume.resized", "", fmt.Sprintf("volume grown to %d bytes", body.VolumeBytes))
	return opResult(w, o)
}

func (f *Fake) projectRoute(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := map[string]string{"host_id": p.HostID, "guest_ip": p.GuestIP, "state": p.State}
	if p.HostID != "" {
		out["host_name"] = FakeHostName // the real api adds the host's name and state (api.md)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// projectSamples is GET /projects/:id/samples (I-492): a deterministic
// hour of a busy machine while it runs, so the dashboard's charts have
// something to draw against the fake; none while it does not.
func (f *Fake) projectSamples(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	steps := map[string]int{"1h": 60, "24h": 300, "7d": 3600}
	win := r.URL.Query().Get("window")
	if win == "" {
		win = "1h"
	}
	step, ok := steps[win]
	if !ok {
		return errf("invalid", "window must be 1h, 24h or 7d")
	}
	vcpus := map[string]int{"small": 2, "large": 4, "xl": 8}[p.Class]
	mem := map[string]int64{"small": 4 << 30, "large": 8 << 30, "xl": 16 << 30}[p.Class]
	points := []map[string]any{}
	procs := []map[string]any{}
	if p.State == "running" {
		end := time.Now().UTC().Truncate(time.Duration(step) * time.Second)
		for i := 59; i >= 0; i-- {
			busy := 0.15
			if i >= 10 && i < 25 {
				busy = 1 // a build that held every core for a quarter of the window
			}
			var pressure any = 0.01
			if busy == 1 {
				pressure = 0.62
			}
			points = append(points, map[string]any{
				"ts": end.Add(-time.Duration(i*step) * time.Second), "cpu": busy, "mem_used_bytes": mem * 45 / 100,
				"cpu_pressure": pressure, "host_cpu_wait": 0.02, "disk_used_bytes": int64(6200) << 20,
			})
		}
		procs = []map[string]any{
			{"comm": "cc1plus", "cpu_s": 2710.4, "rss_max_bytes": int64(410) << 20},
			{"comm": "node", "cpu_s": 640.2, "rss_max_bytes": int64(1200) << 20},
			{"comm": "claude", "cpu_s": 95.0, "rss_max_bytes": int64(380) << 20},
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": win, "step_s": step, "vcpus": vcpus, "memory_bytes": mem, "points": points, "procs": procs})
	return nil
}

// Config.

func (p *project) revision(id string) *Revision {
	for _, rev := range p.revisions {
		if rev.ID == id {
			return rev
		}
	}
	return nil
}

func (f *Fake) getConfig(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	rev := p.revision(p.ConfigRevisionID)
	if rev == nil {
		return errf("internal", "current revision missing")
	}
	out := map[string]any{"revision_id": rev.ID, "fragment": rev.Fragment, "base_version": rev.BaseVersion,
		"personal": rev.personalText, "personal_revision_id": nil, "personal_opt_out": p.PersonalOptOut}
	if len(rev.Menu) > 0 {
		out["menu"] = rev.Menu
	}
	if rev.AppliedAt != nil {
		out["applied_at"] = rev.AppliedAt
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// renderMenu turns a MenuSelection into the fragment the real api
// generates, with internal/menu's own validation and renderer.
func renderMenu(raw json.RawMessage) (string, *apiError) {
	var sel menu.Selection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return "", invalid("menu: %v", err)
	}
	cat, err := menuCatalog()
	if err != nil {
		return "", errf("internal", "catalog: %v", err)
	}
	frag, err := cat.Render(sel)
	if err != nil {
		var me *menu.Error
		if errors.As(err, &me) {
			return "", invalid("%s", me.Message)
		}
		return "", errf("internal", "menu: %v", err)
	}
	return frag, nil
}

// fakeDefaultFragment is what a new project starts with in the real api
// (internal/api/http DefaultFragment); the fake's first revision has none.
const fakeDefaultFragment = "{ pkgs, ... }:\n{\n  home.packages = [ ];\n}\n"

func (f *Fake) putConfig(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Fragment *string         `json:"fragment"`
		Menu     json.RawMessage `json:"menu"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	hasMenu := len(body.Menu) > 0 && string(body.Menu) != "null"
	if (body.Fragment == nil) == !hasMenu {
		return invalid("exactly one of fragment or menu is required")
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	var fragment string
	var menuRaw json.RawMessage
	if body.Fragment != nil {
		if len(*body.Fragment) > maxFragmentBytes {
			return invalid("fragment: larger than 256 KB").withDetail(map[string]any{"max_bytes": maxFragmentBytes})
		}
		fragment = *body.Fragment
	} else {
		// The api's takeover rule: a hand-written fragment turns the menu off.
		if cur := p.revision(p.ConfigRevisionID); cur != nil && len(cur.Menu) == 0 && strings.TrimSpace(cur.Fragment) != "" &&
			!menu.IsGenerated(cur.Fragment) && strings.TrimSpace(cur.Fragment) != strings.TrimSpace(fakeDefaultFragment) {
			return errf("conflict", "project uses a custom fragment; use fragment mode or reset")
		}
		rendered, e := renderMenu(body.Menu)
		if e != nil {
			return e
		}
		fragment, menuRaw = rendered, body.Menu
	}
	now := f.now()
	o := f.newOp(p, "config")
	// A canned eval failure, for the dashboard and CLI to exercise the
	// error path (08-dashboard.md §7, nix-build-contract.md "What the user
	// reads" — the exact first canonical message) without a real Nix
	// evaluation. A failed build changes nothing: the previous revision
	// stays applied.
	if strings.Contains(fragment, forceEvalErrorMarker) {
		msg := "config error: syntax error at fragment.nix:1:32, unexpected ';'"
		rev := &Revision{ID: f.nextID(), CreatedAt: now, Status: "failed", Error: msg, Fragment: fragment, Menu: menuRaw, BaseVersion: baseVersion}
		p.revisions = append(p.revisions, rev)
		o.State, o.Error = "error", msg
		f.event(p, "config.failed", "", "revision failed: "+msg)
		writeJSON(w, http.StatusAccepted, map[string]string{"revision_id": rev.ID, "op_id": o.id})
		return nil
	}
	rev := &Revision{ID: f.nextID(), CreatedAt: now, Status: "applied", Fragment: fragment, Menu: menuRaw, BaseVersion: baseVersion, AppliedAt: &now}
	p.revisions = append(p.revisions, rev)
	p.ConfigRevisionID = rev.ID
	f.event(p, "config.applied", "", "revision applied")
	writeJSON(w, http.StatusAccepted, map[string]string{"revision_id": rev.ID, "op_id": o.id})
	return nil
}

func (f *Fake) listRevisions(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []Revision{}
	for _, rev := range p.revisions {
		out = append(out, *rev)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) applyRevision(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	rev := p.revision(r.PathValue("rev"))
	if rev == nil {
		return notFound("revision")
	}
	if rev.Status != "applied" {
		return invalid("revision: only a successful revision can be re-applied").withDetail(map[string]any{"status": rev.Status})
	}
	now := f.now()
	rev.AppliedAt = &now
	p.ConfigRevisionID = rev.ID
	o := f.newOp(p, "apply")
	f.event(p, "config.applied", "", "revision re-applied")
	return opResult(w, o)
}

func (f *Fake) getCatalog(w http.ResponseWriter, r *http.Request) *apiError {
	cat, err := menuCatalog()
	if err != nil {
		return errf("internal", "catalog: %v", err)
	}
	writeJSON(w, http.StatusOK, cat.Public())
	return nil
}

// Certificates.

func (f *Fake) issueCert(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		PublicKey  string   `json:"public_key"`
		ProjectIDs []string `json:"project_ids"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if !strings.HasPrefix(body.PublicKey, "ssh-") {
		return invalid("public_key: must be an OpenSSH public key")
	}
	if len(body.ProjectIDs) == 0 {
		return invalid("project_ids: at least one project is required")
	}
	u := userFrom(r)
	for _, id := range body.ProjectIDs {
		if _, e := f.project(u, id); e != nil {
			return e
		}
	}
	f.serial++
	c := &cert{serial: f.serial, owner: u.ID, projectIDs: body.ProjectIDs, expiresAt: f.now().Add(sshca.UserCertTTL)}
	f.certs[c.serial] = c
	line := fmt.Sprintf("ssh-ed25519-cert-v01@openssh.com AAAA...fake serial %d", c.serial)
	if f.opts.CA != nil {
		pub, err := sshca.ParsePublicKey(body.PublicKey)
		if err != nil {
			return invalid("public_key: %v", err)
		}
		signed, err := f.opts.CA.User.SignUser(sshca.UserCert{PublicKey: pub, KeyID: u.ID + ":" + u.Handle, Principals: body.ProjectIDs,
			Serial: c.serial, ValidAfter: f.now().Add(-time.Minute), ValidBefore: c.expiresAt})
		if err != nil {
			return errf("internal", "signing: %v", err)
		}
		line = sshca.Marshal(signed)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate": line,
		"serial":      c.serial,
		"expires_at":  c.expiresAt,
		"gateway":     map[string]any{"host": gatewayHost, "port": 22, "host_ca_pub": f.hostCAPub()},
	})
	return nil
}

// hostCAPub and userCAPub are the CA lines: the test CA's when one is
// configured, placeholders otherwise.
func (f *Fake) hostCAPub() string {
	if f.opts.CA != nil {
		return f.opts.CA.Host.PublicLine()
	}
	return hostCAPub
}

func (f *Fake) userCAPub() string {
	if f.opts.CA != nil {
		return f.opts.CA.User.PublicLine()
	}
	return userCAPub
}

func (f *Fake) revokeCerts(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Serial *uint64 `json:"serial"`
		All    bool    `json:"all"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if (body.Serial == nil) == !body.All {
		return invalid("exactly one of serial or all is required")
	}
	u := userFrom(r)
	revoked := []uint64{}
	revoke := func(c *cert) {
		if !c.revoked {
			c.revoked = true
			f.revoked = append(f.revoked, revocation{serial: c.serial, at: f.now()})
		}
		revoked = append(revoked, c.serial)
	}
	if body.All {
		serials := make([]uint64, 0, len(f.certs))
		for s, c := range f.certs {
			if c.owner == u.ID {
				serials = append(serials, s)
			}
		}
		sort.Slice(serials, func(i, j int) bool { return serials[i] < serials[j] })
		for _, s := range serials {
			revoke(f.certs[s])
		}
	} else {
		c, ok := f.certs[*body.Serial]
		if !ok || c.owner != u.ID {
			return notFound("certificate")
		}
		revoke(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
	return nil
}

// Secrets.

func (f *Fake) listSecrets(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []SecretMeta{}
	for _, s := range p.secrets {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) putSecret(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Value string `json:"value"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	name := r.PathValue("name")
	if reservedSecretNames[name] {
		return invalid("name: %q is reserved for the guest's sshd material", name)
	}
	if !secretNameRe.MatchString(name) {
		return invalid("name: must match [A-Z][A-Z0-9_]{0,63}")
	}
	if name == "BASH_ENV" || name == "ENV" || name == "REPOSE_ENV_GEN" {
		return invalid("name: %q is reserved: the machine uses it to keep each command's secrets current", name)
	}
	raw, err := base64.StdEncoding.DecodeString(body.Value)
	if err != nil {
		return invalid("value: must be base64")
	}
	if len(raw) > maxSecretBytes {
		return invalid("value: larger than 64 KB decoded").withDetail(map[string]any{"max_bytes": maxSecretBytes})
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	now := f.now()
	if s, ok := p.secrets[name]; ok {
		s.UpdatedAt = now
	} else {
		p.secrets[name] = &SecretMeta{Name: name, CreatedAt: now, UpdatedAt: now}
	}
	f.event(p, "secret.updated", "", "secret "+name+" set")
	writeJSON(w, http.StatusOK, p.secrets[name])
	return nil
}

func (f *Fake) deleteSecret(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	name := r.PathValue("name")
	if _, ok := p.secrets[name]; !ok {
		return notFound("secret")
	}
	delete(p.secrets, name)
	f.event(p, "secret.deleted", "", "secret "+name+" removed")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Snapshots.

func (f *Fake) listSnapshots(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.projectAny(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []Snapshot{}
	for _, s := range p.snapshots {
		out = append(out, *s)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) createSnapshot(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	o := f.newOp(p, "snapshot")
	s := f.snapshot(p, "manual")
	o.Result = map[string]any{"snapshot_id": s.ID}
	return opResult(w, o)
}

// forkProject is POST /projects/:id/fork (I-254): count projects restored
// from one of p's snapshots, named <base>-<k> for the lowest free k,
// without p's remote, with its secrets; the limit is checked for all of
// them first, and a resent request_id answers with the same projects.
func (f *Fake) forkProject(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		SnapshotID string `json:"snapshot_id"`
		Count      *int   `json:"count"`
		Name       string `json:"name"`
		Class      string `json:"class"`
		Start      *bool  `json:"start"`
		RequestID  string `json:"request_id"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	u := userFrom(r)
	p, e := f.project(u, r.PathValue("id"))
	if e != nil {
		return e
	}
	if body.SnapshotID == "" {
		return invalid("snapshot_id is required: take a snapshot of %s first (POST /projects/:id/snapshots)", p.Slug)
	}
	count := 1
	if body.Count != nil {
		count = *body.Count
	}
	if count < 1 || count > 10 {
		return invalid("count must be 1 to 10")
	}
	var snap *Snapshot
	for _, s := range p.snapshots {
		if s.ID == body.SnapshotID {
			snap = s
		}
	}
	if snap == nil {
		return errf("not_found", "that snapshot is not one of %s's, or it has expired", p.Slug)
	}
	class := p.Class
	if body.Class != "" {
		if _, ok := classes[body.Class]; !ok {
			return invalid("class must be small, large or xl")
		}
		class = body.Class
	}
	base := body.Name
	if base == "" {
		base = p.Slug + "-fork"
	}
	if !nameRe.MatchString(base) || slugOf(base) == "" {
		return invalid("name must match [A-Za-z0-9._-]{1,64}")
	}
	type forked struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		Slug      string `json:"slug"`
		Class     string `json:"class"`
		OpID      string `json:"op_id"`
	}
	answer := func(list []forked) *apiError {
		writeJSON(w, http.StatusAccepted, map[string]any{"snapshot_id": snap.ID, "snapshot_created_at": snap.CreatedAt, "from_project_id": p.ID, "projects": list})
		return nil
	}
	if body.RequestID != "" {
		if prev := f.forks[u.ID+"/"+body.RequestID]; prev != nil {
			var list []forked
			for _, fp := range prev {
				list = append(list, forked{ProjectID: fp.projectID, Name: fp.name, Slug: fp.name, Class: fp.class, OpID: fp.opID})
			}
			return answer(list)
		}
	}
	// The project cap is checked for all N at once; xl is the memory
	// gate's to refuse, as in the api (I-293), not a count.
	taken := map[string]bool{}
	for _, q := range f.userProjects(u) {
		taken[q.Slug] = true
	}
	if e := f.projectCap(u, count); e != nil {
		return e
	}
	b := slugOf(base)
	var list []forked
	var rec []forkRec
	for k := 1; len(list) < count; k++ {
		suffix := fmt.Sprintf("-%d", k)
		bb := b
		if len(bb)+len(suffix) > 40 {
			bb = strings.TrimRight(bb[:40-len(suffix)], "-")
		}
		name := bb + suffix
		if taken[name] {
			continue
		}
		taken[name] = true
		gateClass := class
		if body.Start != nil && !*body.Start {
			gateClass = "" // a stopped copy takes no memory (api fork.go)
		}
		np, e := f.createGated(u, name, "", class, gateClass)
		if e != nil {
			return e
		}
		f.copyMultiplexer(p, np)
		copyHeld(p, np)
		for n, s := range p.secrets {
			c := *s
			np.secrets[n] = &c
		}
		if body.Start != nil && !*body.Start {
			f.stop(np, false)
		}
		f.event(np, "volume.restored", "", "restored from snapshot "+snap.ID+" of "+p.Name)
		o := f.newOp(np, "restore")
		list = append(list, forked{ProjectID: np.ID, Name: np.Name, Slug: np.Slug, Class: np.Class, OpID: o.id})
		rec = append(rec, forkRec{projectID: np.ID, name: np.Slug, class: np.Class, opID: o.id})
	}
	if body.RequestID != "" {
		f.forks[u.ID+"/"+body.RequestID] = rec
	}
	return answer(list)
}

func (f *Fake) restoreSnapshot(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		AsNewProject string `json:"as_new_project"`
	}
	if e := decodeBody(r, &body, true); e != nil {
		return e
	}
	u := userFrom(r)
	p, e := f.projectAny(u, r.PathValue("id"))
	if e != nil {
		return e
	}
	var snap *Snapshot
	for _, s := range p.snapshots {
		if s.ID == r.PathValue("sid") {
			snap = s
		}
	}
	if snap == nil {
		return notFound("snapshot")
	}
	if body.AsNewProject != "" {
		np, e := f.restoreAsNew(u, p, snap, body.AsNewProject)
		if e != nil {
			return e
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"op_id": f.newOp(np, "restore").id, "project_id": np.ID})
		return nil
	}
	if p.State != "stopped" {
		return invalid("restore in place requires the project to be stopped").withDetail(map[string]any{"state": p.State})
	}
	o := f.newOp(p, "restore")
	f.event(p, "volume.restored", "", "volume replaced from snapshot "+snap.ID)
	return opResult(w, o)
}

// projectCap refuses n more projects past the account's cap, running or
// stopped (I-569), as the api's projectLimitError does. A project being
// destroyed no longer counts (I-300); its name stays taken until the
// destroy ends.
func (f *Fake) projectCap(u *userRec, n int) *apiError {
	limit := f.meOf(u).Limits.Projects
	if limit <= 0 {
		return nil
	}
	have := 0
	for _, q := range f.userProjects(u) {
		if q.State != "destroying" {
			have++
		}
	}
	if have+n <= limit {
		return nil
	}
	detail := map[string]any{"reason": "project_limit", "limit": limit, "projects": have}
	if n <= 1 {
		return invalid("you have %d of the %d projects an account can have, running or stopped; destroy one first", have, limit).withDetail(detail)
	}
	detail["requested"] = n
	return invalid("you have %d of the %d projects an account can have, running or stopped, and %d more would make %d; destroy some first", have, limit, n, have+n).withDetail(detail)
}

// restoreAsNew is the as-new restore: a new project from p's class and,
// when no live project has it, p's remote (I-167).
func (f *Fake) restoreAsNew(u *userRec, p *project, snap *Snapshot, name string) (*project, *apiError) {
	remote := p.RemoteURL
	for _, q := range f.userProjects(u) {
		if remote != "" && q.RemoteURL == remote {
			remote = ""
		}
		if q.Slug == slugOf(name) {
			return nil, errf("conflict", "a project named %s already exists; pick another name for the restored one", slugOf(name)).withDetail(map[string]any{"reason": "name_taken", "name": slugOf(name)})
		}
	}
	np, e := f.create(u, name, remote, p.Class)
	if e != nil {
		return nil, e
	}
	f.copyMultiplexer(p, np)
	copyHeld(p, np)
	f.event(np, "volume.restored", "", "restored from snapshot "+snap.ID+" of "+p.Name)
	return np, nil
}

// copyHeld starts a restored or forked copy at what its source holds,
// as the api's insertRestored does (DECISIONS I-585).
func copyHeld(src, np *project) {
	if src.DiskUsedBytes > 0 {
		np.DiskUsedBytes = src.DiskUsedBytes
	}
}

// restorable is p's newest snapshot that has not expired, or nil.
func (f *Fake) restorable(p *project) *Snapshot {
	var best *Snapshot
	for _, s := range p.snapshots {
		if s.ExpiresAt != nil && !s.ExpiresAt.After(f.now()) {
			continue
		}
		if best == nil || !s.CreatedAt.Before(best.CreatedAt) {
			best = s
		}
	}
	return best
}

// listDestroyed is GET /projects/destroyed (I-167).
func (f *Fake) listDestroyed(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	live := map[string]bool{}
	for _, p := range f.userProjects(u) {
		live[p.Slug] = true
	}
	out := []DestroyedProject{}
	for _, p := range f.projects {
		if p.owner != u.ID || !p.destroyed {
			continue
		}
		s := f.restorable(p)
		if s == nil {
			continue
		}
		out = append(out, DestroyedProject{ID: p.ID, Name: p.Name, Slug: p.Slug, Class: p.Class, RemoteURL: p.RemoteURL,
			VolumeBytes: p.VolumeBytes, DestroyedAt: p.destroyedAt, NameFree: !live[p.Slug], RestorableUntil: s.ExpiresAt, Snapshot: *s})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DestroyedAt.Equal(out[j].DestroyedAt) {
			return out[i].DestroyedAt.After(out[j].DestroyedAt)
		}
		return out[i].ID > out[j].ID
	})
	// limit (default 100) and before page as the api does (I-420).
	if b := r.URL.Query().Get("before"); b != "" {
		i := slices.IndexFunc(out, func(d DestroyedProject) bool { return d.ID == b })
		if i < 0 {
			out = []DestroyedProject{}
		} else {
			out = out[i+1:]
		}
	}
	limit := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n >= 1 && n <= 200 {
		limit = n
	}
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// restoreByName is POST /projects/restore (I-167).
func (f *Fake) restoreByName(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Slug       string  `json:"slug"`
		ProjectID  string  `json:"project_id"`
		SnapshotID string  `json:"snapshot_id"`
		Name       *string `json:"name"`
		Start      *bool   `json:"start"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	u := userFrom(r)
	if body.Slug == "" && body.ProjectID == "" && body.SnapshotID == "" {
		return invalid("name the project (slug or project_id) or the snapshot (snapshot_id) to restore")
	}
	var src *project
	var snap *Snapshot
	switch {
	case body.SnapshotID != "":
		for _, p := range f.projects {
			for _, s := range p.snapshots {
				if s.ID == body.SnapshotID && p.owner == u.ID && (s.ExpiresAt == nil || s.ExpiresAt.After(f.now())) {
					src, snap = p, s
				}
			}
		}
		if src == nil || (body.Slug != "" && slugOf(body.Slug) != src.Slug) || (body.ProjectID != "" && body.ProjectID != src.ID) {
			return errf("not_found", "that snapshot does not exist or has expired")
		}
	default:
		var cands []*project
		for _, p := range f.projects {
			if p.owner != u.ID || (body.ProjectID != "" && p.ID != body.ProjectID) || (body.ProjectID == "" && p.Slug != slugOf(body.Slug)) {
				continue
			}
			if !p.destroyed && body.ProjectID == "" {
				if p.State == "destroying" {
					return errf("conflict", "%s is still being destroyed; its final snapshot is not taken yet. Try again in a few seconds", p.Slug).withDetail(map[string]any{"reason": "destroying"})
				}
				cands = []*project{p} // a live project with the name is the one meant
				break
			}
			cands = append(cands, p)
		}
		if len(cands) == 0 {
			return errf("not_found", "you have no project called %s", slugOf(body.Slug))
		}
		for _, p := range cands {
			if s := f.restorable(p); s != nil && (snap == nil || s.CreatedAt.After(snap.CreatedAt)) {
				src, snap = p, s
			}
		}
		if snap == nil {
			return errf("not_found", "%s has no snapshot left to restore", cands[0].Slug).withDetail(map[string]any{"reason": "no_snapshot"})
		}
	}
	name := src.Name
	if body.Name != nil {
		name = *body.Name
	}
	np, e := f.restoreAsNew(u, src, snap, name)
	if e != nil {
		return e
	}
	o := f.newOp(np, "restore")
	writeJSON(w, http.StatusAccepted, map[string]any{"op_id": o.id, "project_id": np.ID, "name": np.Name, "slug": np.Slug,
		"snapshot_id": snap.ID, "snapshot_created_at": snap.CreatedAt, "snapshot_bytes": snap.Bytes, "from_project_id": src.ID})
	return nil
}

// Events and logs.

func (f *Fake) listEvents(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.projectAny(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []Event{}
	since := r.URL.Query().Get("since")
	var sinceTS time.Time
	sinceIsTime := false
	if since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			sinceTS, sinceIsTime = t, true
		}
	}
	for _, ev := range p.events {
		switch {
		case since == "":
		case sinceIsTime && !ev.TS.After(sinceTS):
			continue
		case !sinceIsTime && ev.ID <= since:
			continue
		}
		out = append(out, *ev)
	}
	// Newest first, at most limit (50), and before pages back, as the api
	// answers (I-414).
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.After(out[j].TS) })
	if b := r.URL.Query().Get("before"); b != "" {
		i := slices.IndexFunc(out, func(ev Event) bool { return ev.ID == b })
		if i < 0 {
			out = nil
		} else {
			out = out[i+1:]
		}
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n >= 1 && n <= 200 {
		limit = n
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Event{}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) projectLogs(w http.ResponseWriter, r *http.Request) *apiError {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "console"
	}
	if kind != "console" && kind != "build" && kind != "ops" {
		return invalid("kind: must be console, build or ops")
	}
	if s := r.URL.Query().Get("since"); s != "" {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return invalid("since: must be RFC 3339")
		}
	}
	p, e := f.projectAny(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	var lines []map[string]any
	ts := f.now()
	switch kind {
	case "console":
		// What hostd kept of boots that failed (DECISIONS I-592); a
		// project that booted cleanly has none.
		for _, l := range f.console[p.ID] {
			lines = append(lines, map[string]any{"ts": ts, "kind": kind, "line": l})
		}
	case "build":
		for i, l := range buildLines {
			lines = append(lines, map[string]any{"ts": ts, "kind": kind, "seq": i + 1, "line": l})
		}
	case "ops":
		for _, ev := range p.events {
			lines = append(lines, map[string]any{"ts": ev.TS, "kind": kind, "line": ev.Kind + ": " + ev.Summary})
		}
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return nil // the client hung up; headers are already out
		}
	}
	return nil
}

// Usage and billing.

func (f *Fake) getUsage(w http.ResponseWriter, r *http.Request) *apiError {
	for _, k := range []string{"from", "to"} {
		if v := r.URL.Query().Get(k); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return invalid("%s: must be YYYY-MM-DD", k)
			}
		}
	}
	writeJSON(w, http.StatusOK, []UsageRow{})
	return nil
}

// Internal (gateway).

func (f *Fake) internalRoute(w http.ResponseWriter, r *http.Request) *apiError {
	login := r.URL.Query().Get("login")
	i := strings.LastIndex(login, ".")
	if i <= 0 || i == len(login)-1 {
		return invalid("login: must be <slug>.<handle>")
	}
	slug, handle := login[:i], login[i+1:]
	for _, p := range f.projects {
		if p.destroyed || p.Slug != slug {
			continue
		}
		u, ok := f.users[p.owner]
		if !ok || u.Handle != handle {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"project_id": p.ID, "guest_ip": p.GuestIP, "state": p.State, "principals": []string{p.ID},
		})
		return nil
	}
	return notFound("login")
}

func (f *Fake) internalRevoked(w http.ResponseWriter, r *http.Request) *apiError {
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return invalid("since: must be RFC 3339")
		}
		since = t
	}
	out := []uint64{}
	for _, rv := range f.revoked {
		if !rv.at.Before(since) {
			out = append(out, rv.serial)
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) internalCA(w http.ResponseWriter, r *http.Request) *apiError {
	writeJSON(w, http.StatusOK, map[string]string{"user_ca_pub": f.userCAPub(), "host_ca_pub": f.hostCAPub()})
	return nil
}

func (f *Fake) internalSessions(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		ProjectID  string `json:"project_id"`
		Event      string `json:"event"`
		CertSerial uint64 `json:"cert_serial"`
		SessionID  string `json:"session_id"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if body.Event != "opened" && body.Event != "closed" {
		return invalid("event: must be opened or closed")
	}
	p, ok := f.projects[body.ProjectID]
	if !ok || p.destroyed {
		return notFound("project")
	}
	f.sessions = append(f.sessions, SessionReport{ProjectID: body.ProjectID, Event: body.Event, CertSerial: body.CertSerial, SessionID: body.SessionID})
	if p.Signals == nil {
		p.Signals = &Signals{Agents: []AgentSignal{}, GuestdOK: true}
	}
	if body.Event == "opened" {
		p.Signals.SSHSessions++
	} else if p.Signals.SSHSessions > 0 {
		p.Signals.SSHSessions--
	}
	writeJSON(w, http.StatusOK, map[string]int{"ssh_sessions": p.Signals.SSHSessions})
	return nil
}

func (f *Fake) internalHosts(w http.ResponseWriter, r *http.Request) *apiError {
	if f.hosts != nil {
		writeJSON(w, http.StatusOK, f.hosts)
		return nil
	}
	writeJSON(w, http.StatusOK, []Host{{
		HostID: hostID, WGPubkey: "fakewgpubkey0000000000000000000000000000000=", WGIP: "10.64.0.1",
		GuestCIDR: "10.64.4.0/22", State: "ready",
	}})
	return nil
}

func (f *Fake) internalGatewayCerts(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		PublicKey string `json:"public_key"`
		ProjectID string `json:"project_id"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if !strings.HasPrefix(body.PublicKey, "ssh-") {
		return invalid("public_key: must be an OpenSSH public key")
	}
	p, ok := f.projects[body.ProjectID]
	if !ok || p.destroyed {
		return notFound("project")
	}
	f.serial++
	line := fmt.Sprintf("ssh-ed25519-cert-v01@openssh.com AAAA...fake serial %d principal %s key_id %s:via-gateway", f.serial, p.ID, p.ID)
	expires := f.now().Add(sshca.GatewayCertTTL)
	if f.opts.CA != nil {
		pub, err := sshca.ParsePublicKey(body.PublicKey)
		if err != nil {
			return invalid("public_key: %v", err)
		}
		u := f.users[p.owner]
		signed, err := f.opts.CA.User.SignUser(sshca.UserCert{PublicKey: pub, KeyID: u.ID + ":" + u.Handle + ":via-gateway", Principals: []string{p.ID},
			Serial: f.serial, ValidAfter: f.now().Add(-time.Minute), ValidBefore: expires})
		if err != nil {
			return errf("internal", "signing: %v", err)
		}
		line = sshca.Marshal(signed)
	}
	f.gatewayCerts++
	writeJSON(w, http.StatusOK, map[string]any{"certificate": line, "expires_at": expires})
	return nil
}

func (f *Fake) internalEvents(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		SourceIP string `json:"source_ip"`
		Agent    string `json:"agent"`
		Kind     string `json:"kind"`
		Summary  string `json:"summary"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	if body.SourceIP == "" || body.Kind == "" {
		return invalid("source_ip and kind are required")
	}
	for _, p := range f.projects {
		if p.destroyed || p.GuestIP == "" || p.GuestIP != body.SourceIP {
			continue
		}
		key := body.Agent + "\x00" + body.Kind + "\x00" + f.now().Format(time.RFC3339)
		if p.eventKeys[key] {
			writeJSON(w, http.StatusOK, map[string]any{"deduped": true})
			return nil
		}
		p.eventKeys[key] = true
		ev := f.event(p, body.Kind, body.Agent, body.Summary)
		writeJSON(w, http.StatusOK, map[string]any{"id": ev.ID, "deduped": false})
		return nil
	}
	return notFound("project for source_ip")
}
