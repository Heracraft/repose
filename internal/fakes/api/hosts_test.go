package api

import (
	"encoding/base64"
	"testing"
	"time"
)

// The fake's half of api.md "Hosts you own" (DECISIONS I-1366): the
// shapes `repose host` and the dashboard's Hosts page read, so a client
// test that passes here passes against the api.

func TestFakeHostsWalkthrough(t *testing.T) {
	f := New(Options{})
	defer f.Close()

	var list []OwnedHost
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	if len(list) != 0 {
		t.Fatalf("an account that owns nothing has %d hosts", len(list))
	}

	var h OwnedHost
	r := call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "mac-mini"})
	want(t, r, 201)
	r.json(t, &h)
	if h.Name != "mac-mini" || h.State != "registering" || h.JoinToken == "" || h.JoinTokenExpiresAt == nil {
		t.Fatalf("add answered %+v", h)
	}
	if h.Install == nil || h.Install.Command == "" || h.Install.URL == "" {
		t.Fatalf("add answered install %+v, want the fake's line and the docs page", h.Install)
	}
	// The default install line carries the join bundle, which carries the
	// token (DECISIONS I-1380), so a client that prints it prints
	// something a user could paste.
	if h.Install.Argument == "" || !contains(h.Install.Command, h.Install.Argument) {
		t.Fatalf("the install line does not carry the argument: %q", h.Install.Command)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(h.Install.Argument); err != nil || !contains(string(raw), `"token":"`+h.JoinToken+`"`) {
		t.Fatalf("the argument does not carry the token: %q (%v)", h.Install.Argument, err)
	}

	// The list never carries the token: it is minted once and shown once.
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	if len(list) != 1 || list[0].JoinToken != "" || list[0].Install != nil {
		t.Fatalf("list %+v", list)
	}

	// One host per plan.
	r = call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "spare"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "host_limit" {
		t.Fatalf("second host detail %v", detailOf(t, r))
	}
	// The name before the limit, so the account at its limit asking for
	// the name it already uses hears about the name.
	r = call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "mac-mini"})
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "name_taken" {
		t.Fatalf("the same name twice: %v", detailOf(t, r))
	}
	// A bad name.
	r = call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "Mac Mini"})
	wantErr(t, r, 400, "invalid")
	if detailOf(t, r)["reason"] != "name" {
		t.Fatalf("a bad name: %v", detailOf(t, r))
	}

	// Remove, and remove again.
	want(t, call(t, f, "DELETE", "/v1/hosts/"+h.ID, tok, nil), 204)
	want(t, call(t, f, "DELETE", "/v1/hosts/"+h.ID, tok, nil), 204)
	// The removed host's name is free again, on the row it kept.
	var again OwnedHost
	r = call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "mac-mini"})
	want(t, r, 201)
	r.json(t, &again)
	if again.ID != h.ID || again.State != "registering" {
		t.Fatalf("adding the removed name again gave %+v, want the kept row %s registering", again, h.ID)
	}
	want(t, call(t, f, "DELETE", "/v1/hosts/"+h.ID, tok, nil), 204)
	wantErr(t, call(t, f, "DELETE", "/v1/hosts/01900000-0000-7000-8000-00000000ffff", tok, nil), 404, "not_found")
}

// TestFakeHostNoInstallLine is the live-today case: nothing has recorded
// an install line, so `install.command` is empty and a client shows the
// token and the page instead.
func TestFakeHostNoInstallLine(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetHostInstall("")
	var h OwnedHost
	r := call(t, f, "POST", "/v1/hosts", tok, nil)
	want(t, r, 201)
	r.json(t, &h)
	if h.Install == nil || h.Install.Command != "" || h.Install.URL == "" {
		t.Fatalf("install %+v, want an empty command and a page", h.Install)
	}
	if h.Name != "host-1" {
		t.Fatalf("an unnamed host is %q, want host-1", h.Name)
	}
}

// TestFakeHostNeedsAPlan: SetHostLimit(0) is a plan that sells no host.
func TestFakeHostNeedsAPlan(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.SetHostLimit(0)
	r := call(t, f, "POST", "/v1/hosts", tok, nil)
	wantErr(t, r, 402, "payment_required")
	if detailOf(t, r)["reason"] != "own_hardware_plan_required" {
		t.Fatalf("detail %v", detailOf(t, r))
	}
}

// TestFakeHostFixtures covers the three fixtures a client test starts
// from: a host that has registered, one that has gone quiet, and a
// project placed on it.
func TestFakeHostFixtures(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	id := f.AddOwnedHost("mac-mini")
	p, err := f.CreateProject("todo", "large")
	if err != nil {
		t.Fatal(err)
	}
	f.SetProjectHost(p.ID, id)

	var list []OwnedHost
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	got := list[0]
	if got.State != "ready" || got.Arch != "aarch64" || got.MemBytes == 0 || got.RegisteredAt == nil {
		t.Fatalf("a registered host reads %+v", got)
	}
	if got.Projects != 1 || got.RunningProjects != 1 {
		t.Fatalf("counts %d/%d, want 1/1", got.Projects, got.RunningProjects)
	}
	// A host still holding a machine is not removed.
	r := call(t, f, "DELETE", "/v1/hosts/"+id, tok, nil)
	wantErr(t, r, 409, "conflict")
	if detailOf(t, r)["reason"] != "host_has_projects" {
		t.Fatalf("detail %v", detailOf(t, r))
	}

	f.SetOwnedHostOffline(id, 14*time.Minute)
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	if list[0].State != "unreachable" || list[0].LastHeartbeatAt == nil {
		t.Fatalf("an offline host reads %+v", list[0])
	}
	if quiet := time.Since(*list[0].LastHeartbeatAt); quiet < 13*time.Minute || quiet > 16*time.Minute {
		t.Fatalf("quiet for %s, want about 14m", quiet)
	}
}

// TestFakeProjectPlacement: the fake answers `placement` and refuses a
// value that is not one of the two.
func TestFakeProjectPlacement(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	var p Project
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "one", "class": "small"})
	want(t, r, 201)
	r.json(t, &p)
	if p.Placement != "own" {
		t.Fatalf("placement %q, want own", p.Placement)
	}
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "two", "class": "small", "placement": "hosted"})
	want(t, r, 201)
	r.json(t, &p)
	if p.Placement != "hosted" {
		t.Fatalf("placement %q, want hosted", p.Placement)
	}
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "three", "class": "small", "placement": "somewhere"})
	wantErr(t, r, 400, "invalid")
	if detailOf(t, r)["reason"] != "placement" {
		t.Fatalf("detail %v", detailOf(t, r))
	}
}

// TestFakeHostFollowsTheBillingMode: the fake refuses a host exactly
// where the api does, from its own billing mode, rather than handing one
// to every account. The api reads the plan (billing.Gate.HostsAllowed:
// exempt or enforcement off gets one, no live subscription gets none,
// otherwise the plan's Hosts), and a fake that said yes where the api
// says no is how a reviewer reports "nothing stopped me" against a
// contract that does stop them (ENG-36).
func TestFakeHostFollowsTheBillingMode(t *testing.T) {
	for _, c := range []struct {
		name    string
		mode    string
		plan    string
		allowed bool
	}{
		{"billing off is the exempt account", BillingOff, "", true},
		{"a billing-exempt account", BillingExempt, "", true},
		{"no plan at all", BillingNone, "", false},
		{"a hosted plan sells no host", BillingActive, "solo", false},
		{"the Own hardware plan", BillingActive, "own", true},
		{"on trial on Own hardware", BillingTrial, "own", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := New(Options{})
			defer f.Close()
			state := BillingState{Mode: &c.mode}
			if c.plan != "" {
				on := true
				state.Plan, state.OwnForSale = &c.plan, &on
			}
			if err := f.SetBillingState(state); err != nil {
				t.Fatal(err)
			}
			r := call(t, f, "POST", "/v1/hosts", tok, nil)
			if c.allowed {
				if r.status != 201 {
					t.Fatalf("mode %s plan %q: %d %s, want a host", c.mode, c.plan, r.status, r.body)
				}
				return
			}
			wantErr(t, r, 402, "payment_required")
			if detailOf(t, r)["reason"] != "own_hardware_plan_required" {
				t.Fatalf("mode %s plan %q: detail %v", c.mode, c.plan, detailOf(t, r))
			}
		})
	}
}
