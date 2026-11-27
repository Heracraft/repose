package api

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
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

	// A limit of one, as the Own hardware plan sold before I-1594: the
	// refusal stays for a plan with a figure, and an older api answers it.
	f.SetHostLimit(1)
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
// exempt or enforcement off gets any number, no live subscription gets
// none, otherwise the plan's Hosts, any number on Own hardware since
// I-1594 and on every plan since I-1704), and a fake that said yes where the api says no is how a
// reviewer reports "nothing stopped me" against a contract that does
// stop them (ENG-36).
func TestFakeHostFollowsTheBillingMode(t *testing.T) {
	for _, c := range []struct {
		name  string
		mode  string
		plan  string
		sells bool
	}{
		{"billing off is the exempt account", BillingOff, "", true},
		{"a billing-exempt account", BillingExempt, "", true},
		{"no plan at all", BillingNone, "", false},
		{"a hosted plan includes hosts (I-1704)", BillingActive, "solo", true},
		{"Pro includes hosts too", BillingActive, "pro", true},
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
			if !c.sells {
				r := call(t, f, "POST", "/v1/hosts", tok, nil)
				wantErr(t, r, 402, "payment_required")
				if detailOf(t, r)["reason"] != "own_hardware_plan_required" {
					t.Fatalf("mode %s plan %q: detail %v", c.mode, c.plan, detailOf(t, r))
				}
				return
			}
			// A second and a third host go in: no plan sold limits them.
			for i := 1; i <= 3; i++ {
				if r := call(t, f, "POST", "/v1/hosts", tok, nil); r.status != 201 {
					t.Fatalf("mode %s plan %q: host %d: %d %s, want a host", c.mode, c.plan, i, r.status, r.body)
				}
			}
			r := call(t, f, "GET", "/v1/me", tok, nil)
			if !strings.Contains(string(r.body), fmt.Sprintf(`"hosts":%d`, unlimitedHosts)) {
				t.Fatalf("mode %s plan %q: /me %s, want limits.hosts %d", c.mode, c.plan, r.body, unlimitedHosts)
			}
		})
	}
}

// TestFakePlacementWithSeveralOwnHosts: as the api, an `own` project
// goes to a host of the account's that is online whichever of its hosts
// that is, and a refusal while none is names every one (I-1582).
func TestFakePlacementWithSeveralOwnHosts(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	for _, name := range []string{"e2e-x86-1", "e2e-x86-2"} {
		want(t, call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": name}), 201)
	}
	var list []OwnedHost
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	f.SetOwnedHostOffline(list[1].ID, 14*time.Minute)
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "one", "class": "small"})
	wantErr(t, r, 503, "capacity")
	if d := detailOf(t, r); d["reason"] != "no_own_host" || d["host"] != "e2e-x86-1" || d["host_state"] != "registering" {
		t.Fatalf("detail %v", d)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(t, &body)
	if want := "none of your hosts has room: e2e-x86-1 has not joined your account yet; e2e-x86-2 has not answered for 14m"; body.Error.Message != want {
		t.Fatalf("message %q, want %q", body.Error.Message, want)
	}

	third := f.AddOwnedHost("e2e-x86-3")
	var p Project
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "one", "class": "small"})
	want(t, r, 201)
	r.json(t, &p)
	if p.HostID != third {
		t.Fatalf("placed on %q, want the online host %q", p.HostID, third)
	}
}

// TestSetOwnedHostStateLeavesRemovedHostsRemoved: a name is the live
// host's, as in the api. A removed mac-mini stays removed when a test
// moves the new mac-mini, so a create names one waiting host, not every
// mac-mini the account ever had (main 49b140b0).
func TestSetOwnedHostStateLeavesRemovedHostsRemoved(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	old := f.AddOwnedHost("mac-mini")
	if r := call(t, f, "DELETE", "/v1/hosts/"+old, tok, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	f.AddOwnedHost("mac-mini")
	f.SetOwnedHostState("mac-mini", "registering")
	var list []OwnedHost
	call(t, f, "GET", "/v1/hosts", tok, nil).json(t, &list)
	for _, h := range list {
		if h.ID == old && h.State != "retired" {
			t.Fatalf("the removed mac-mini is %s again", h.State)
		}
	}
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "one", "class": "small"})
	wantErr(t, r, 503, "capacity")
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(t, &body)
	if want := "your Mac mac-mini has not joined your account yet"; body.Error.Message != want {
		t.Fatalf("message %q, want %q", body.Error.Message, want)
	}
}

// TestFakeOwnHostFullNamesTheFigure: as the api (I-1586, I-1630), a host
// of the account's own takes any number of machines of any class while
// it reports a gigabyte free, says they have no size and what a running
// one uses, and refuses the next once it reports less, with the host's
// name, what it has and what a start needs.
func TestFakeOwnHostFullNamesTheFigure(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	mac := f.AddOwnedHost("mac-mini")
	for i, class := range []string{"xl", "xl", "large", "small"} { // 48 GiB of classes on a 24 GiB Mac
		var p Project
		r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": fmt.Sprintf("m%d", i), "class": class})
		want(t, r, 201)
		r.json(t, &p)
		if p.HostID != mac || p.Sized {
			t.Fatalf("%s placed on %q sized %v, want mac-mini and no size", class, p.HostID, p.Sized)
		}
	}
	f.SetOwnedHostFree("mac-mini", 900<<20)
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "m4", "class": "tiny"})
	wantErr(t, r, 503, "capacity")
	if d := detailOf(t, r); d["reason"] != "no_own_host" || d["host"] != "mac-mini" || d["host_state"] != "ready" {
		t.Fatalf("detail %v", d)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(t, &body)
	if want := "your Mac mac-mini is full. Stop a machine there"; body.Error.Message != want {
		t.Fatalf("message %q, want %q", body.Error.Message, want)
	}
	f.SetOwnedHostFree("mac-mini", 1<<30)
	want(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "m5", "class": "xl"}), 201)
}

// TestFakeCapsWaitingHosts mirrors the api's cap on hosts that have not
// joined yet (ENG-211): the eleventh is refused with waiting_limit, and
// one that joins makes room for the next.
func TestFakeCapsWaitingHosts(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	for i := 1; i <= maxWaitingHosts; i++ {
		want(t, call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": fmt.Sprintf("box-%d", i)}), 201)
	}
	r := call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "box-11"})
	wantErr(t, r, 409, "conflict")
	if d := detailOf(t, r); d["reason"] != "waiting_limit" || d["limit"] != float64(maxWaitingHosts) {
		t.Fatalf("eleventh waiting host: %v", d)
	}
	f.SetOwnedHostState("box-1", "ready")
	want(t, call(t, f, "POST", "/v1/hosts", tok, map[string]any{"name": "box-11"}), 201)
}

// TestFakeOldHostdPlacesByClass: as the api, an owned host whose hostd
// does not share keeps the class arithmetic and its machines keep their
// size (I-1630): the fake's 24 GiB Mac takes 21 GiB of classes.
func TestFakeOldHostdPlacesByClass(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	f.AddOwnedHost("mac-mini")
	f.SetOwnedHostOldHostd("mac-mini", true)
	for i, class := range []string{"xl", "small"} {
		var p Project
		r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": fmt.Sprintf("m%d", i), "class": class})
		want(t, r, 201)
		r.json(t, &p)
		if !p.Sized {
			t.Fatalf("%s on an old hostd: sized false, want true", class)
		}
	}
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "m2", "class": "tiny"})
	wantErr(t, r, 503, "capacity")
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	r.json(t, &body)
	if want := "your Mac mac-mini is full. Stop a machine there"; body.Error.Message != want {
		t.Fatalf("message %q, want %q", body.Error.Message, want)
	}
}

// The fake's refusal of a live grow on a Mac is hostd's own sentence, so
// a dashboard or CLI test reads what a customer reads.
func TestMacLiveGrowRefusalIsHostdsWords(t *testing.T) {
	src, err := os.ReadFile("../../hostd/guest/stop.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"`+MacLiveGrowRefusal+`"`) {
		t.Fatalf("hostd's refusal no longer reads %q; update MacLiveGrowRefusal", MacLiveGrowRefusal)
	}
}

// TestFakeHostedPlanRunsOwnHostPastItsMemory mirrors the api's
// TestHostedPlanCreatesOnOwnHostPastItsMemory (I-1704): on Solo with its
// 8 GB used on repose's servers, a machine that goes to the account's own
// host is made and started, and one on repose's servers is refused.
func TestFakeHostedPlanRunsOwnHostPastItsMemory(t *testing.T) {
	f := New(Options{})
	defer f.Close()
	mode, plan := BillingActive, "solo"
	if err := f.SetBillingState(BillingState{Mode: &mode, Plan: &plan}); err != nil {
		t.Fatal(err)
	}
	want(t, call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "fleet", "class": "large"}), 201)
	mac := f.AddOwnedHost("mac-mini")
	var p Project
	r := call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "mine", "class": "large"})
	want(t, r, 201)
	r.json(t, &p)
	if p.HostID != mac {
		t.Fatalf("placed on %q, want mac-mini", p.HostID)
	}
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/stop", tok, nil), 202)
	want(t, call(t, f, "POST", "/v1/projects/"+p.ID+"/start", tok, nil), 202)
	r = call(t, f, "POST", "/v1/projects", tok, map[string]any{"name": "fleet-2", "class": "small", "placement": "hosted"})
	wantErr(t, r, 402, "payment_required")
	if detailOf(t, r)["reason"] != "plan_limit" {
		t.Fatalf("detail %v, want plan_limit", detailOf(t, r))
	}
}
