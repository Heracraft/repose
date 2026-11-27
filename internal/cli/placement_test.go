package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestRunHostChoosesWhereTheMachineIsMade is `repose run --host`
// (DECISIONS I-1390): `repose` is repose's servers, a name is one of the
// account's own hosts, and a name the account has no host under exits 2
// before anything is created.
func TestRunHostChoosesWhereTheMachineIsMade(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)

	err := runRun(ctx, e, RunOptions{Name: "api", Host: "nas", NoSync: true, NoAttach: true}, false)
	if exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "no host called nas") || !strings.Contains(err.Error(), "`repose host ls`") {
		t.Fatalf("a host the account does not have: %v, want exit 2 naming repose host ls", err)
	}
	if bySlug(listed(t, e), "api") != nil {
		t.Fatal("a refused --host made a project")
	}

	mac := fake.AddOwnedHost("mac-mini")
	if err := runRun(ctx, e, RunOptions{Name: "api", Host: "repose", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if p := bySlug(listed(t, e), "api"); p == nil || p.Placement != "hosted" || p.HostID == mac {
		t.Fatalf("--host repose made %+v, want placement hosted off the Mac", p)
	}
	if err := runRun(ctx, e, RunOptions{Name: "web", Host: "mac-mini", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if p := bySlug(listed(t, e), "web"); p == nil || p.Placement != "own" || p.HostID != mac || p.HostName != "mac-mini" {
		t.Fatalf("--host mac-mini made %+v, want placement own on %s named mac-mini", p, mac)
	}
	if p := bySlug(listed(t, e), "api"); p.HostName != "" {
		t.Fatalf("a project on repose's servers carries host_name %q", p.HostName)
	}

	// A machine is pinned to its host for life: the flag on one that
	// exists exits 2 and says so, whatever it names.
	err = runRun(ctx, e, RunOptions{Name: "web", Host: "repose", NoSync: true, NoAttach: true}, false)
	// No command moves a machine to another host, so nothing is offered
	// that would (review-cli N9, I-1654).
	if exitCodeOf(err) != ExitUsage || err.Error() != "web exists and stays on the host it was made on; --host chooses where a new machine is made." {
		t.Fatalf("--host on a project that exists: %v", err)
	}
	if p := bySlug(listed(t, e), "web"); p.HostID != mac || p.State != "running" {
		t.Fatalf("after the refusal: %+v", p)
	}
}

// TestCreateWaitsWhileOwnHostInstalls is ENG-131 (DECISIONS I-1700): a
// customer whose Mac has not finished installing gets a project that
// waits for it, not a refusal. `repose run` says so once, in the owner's
// words, `repose ls` says it under the table, and the machine starts on
// the Mac once it registers.
func TestCreateWaitsWhileOwnHostInstalls(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, errOut := freshEnv(f.env, f.local)
	mac := fake.AddOwnedHost("mac-mini")
	fake.SetOwnedHostState("mac-mini", "registering")

	done := make(chan error, 1)
	go func() { done <- runRun(ctx, e, RunOptions{Name: "todo-app", NoSync: true, NoAttach: true}, false) }()
	const want = "Waiting for your Mac (mac-mini) to finish installing. This project starts there when it's ready.\n"
	stderr := func() string {
		errOut.mu.Lock()
		defer errOut.mu.Unlock()
		return errOut.buf.String()
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr(), want) {
		select {
		case err := <-done:
			t.Fatalf("run ended while the Mac installs: %v (stderr %q)", err, stderr())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no wait line; stderr %q", stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := strings.Count(stderr(), "Waiting for"); n != 1 {
		t.Fatalf("the wait line %d times: %q", n, stderr())
	}
	p := bySlug(listed(t, e), "todo-app")
	if p == nil || p.State != "creating" || p.HostID != "" {
		t.Fatalf("while it waits: %+v", p)
	}
	var b strings.Builder
	writeProjectsTableHere(&b, []Project{*p}, "", nil)
	if !strings.Contains(b.String(), "todo-app: waiting for your Mac (mac-mini) to finish installing; it starts there when it's ready.\n") {
		t.Fatalf("repose ls while it waits:\n%s", b.String())
	}

	fake.SetOwnedHostState("mac-mini", "ready")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run after the Mac registered: %v (stderr %q)", err, stderr())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run still waits after the Mac registered")
	}
	if p := bySlug(listed(t, e), "todo-app"); p == nil || p.State != "running" || p.HostID != mac {
		t.Fatalf("after the Mac registered: %+v", p)
	}
}

// TestCreateRefusedWhileOwnHostCannotJoinNamesTheWayOut is the P0 of
// 2026-11-19 (ENG-28, ENG-67) where it still holds after I-1700: a host
// that has not registered and can no longer join (its token expired)
// is no place to wait, so the create is refused with the api's sentence
// about the host, nothing is stranded, the refusal names `--host
// repose`, and that flag makes the machine on repose's servers.
func TestCreateRefusedWhileOwnHostCannotJoinNamesTheWayOut(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)
	fake.AddOwnedHost("mac-mini")
	fake.SetOwnedHostState("mac-mini", "registering")
	fake.ExpireJoinToken("mac-mini")

	for attempt := 1; attempt <= 2; attempt++ {
		err := runRun(ctx, e, RunOptions{Name: "todo-app", NoSync: true, NoAttach: true}, false)
		want := "your host mac-mini never finished installing, and its join token has expired; `repose host rm mac-mini` and `repose host add` start over."
		if exitCodeOf(err) != ExitCapacity || err.Error() != want {
			t.Fatalf("attempt %d: %v, want exit 8 with %q", attempt, err, want)
		}
		if n := len(listed(t, e)); n != 0 {
			t.Fatalf("attempt %d stranded %d project(s)", attempt, n)
		}
	}
	if err := runRun(ctx, e, RunOptions{Name: "todo-app", Host: "repose", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if p := bySlug(listed(t, e), "todo-app"); p == nil || p.State != "running" || p.Placement != "hosted" {
		t.Fatalf("--host repose while the Mac cannot join: %+v", p)
	}

	// A plan that sells no memory on repose's servers has no way out to
	// name: the sentence stands alone.
	fake.SetHostLimit(1)
	err := runRun(ctx, e, RunOptions{Name: "api", NoSync: true, NoAttach: true}, false)
	if exitCodeOf(err) != ExitCapacity || err.Error() != "your host mac-mini never finished installing, and its join token has expired; `repose host rm mac-mini` and `repose host add` start over." {
		t.Fatalf("own hardware plan: %v", err)
	}
}

// TestWaitingLineNamesTheKindOfHost: the api keeps the provider a host was
// added with (`mac` from `repose host add` on a Mac, I-1700), and the wait
// line says "your Mac" for one and "your host" for any other.
func TestWaitingLineNamesTheKindOfHost(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)
	h, err := e.Client.CreateHost(ctx, CreateHostRequest{Name: "studio", Provider: "mac"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Provider != "mac" {
		t.Fatalf("provider %q, want mac", h.Provider)
	}
	p := &Project{State: "creating", WaitingForHost: &ProjectWaitingForHost{Name: "box-1", Provider: "metal"}}
	if got := waitingLine(p); got != "Waiting for your host box-1 to finish installing. This project starts there when it's ready." {
		t.Fatalf("an x86 host: %q", got)
	}
}

// TestHostReadoutNamesTheHostYouOwn is the readout half of I-1392: a
// machine on a host of the account's own names it where the account
// looks, in `repose ls`'s HOST column and `repose status`'s `host` row,
// and an account with no hardware of its own sees neither.
func TestHostReadoutNamesTheHostYouOwn(t *testing.T) {
	ps := []Project{
		{ID: "a", Slug: "todo-app", Class: "large", State: "running", HostID: "h-mac", HostName: "mac-mini"},
		{ID: "b", Slug: "web", Class: "small", State: "running", HostID: "h-01"},
		{ID: "c", Slug: "notes", Class: "tiny", State: "creating"},
	}
	var b strings.Builder
	writeProjectsTableHere(&b, ps, "", nil)
	want := "PROJECT   SIZE   STATE     UP  AGENTS  HOST\n" +
		"todo-app  large  running   -   -       mac-mini\n" +
		"web       small  running   -   -       repose\n" +
		"notes     tiny   creating  -   -       -\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}

	// Nothing of the account's own: the table it always had, HOST
	// included nowhere, because a column that reads `repose` in every
	// row says nothing (I-484).
	hosted := []Project{
		{ID: "b", Slug: "web", Class: "small", State: "running", HostID: "h-01"},
		{ID: "c", Slug: "notes", Class: "tiny", State: "creating"},
	}
	b.Reset()
	writeProjectsTableHere(&b, hosted, "", nil)
	if got := b.String(); strings.Contains(got, "HOST") || strings.Contains(got, "repose") {
		t.Fatalf("an account with no hardware got a host readout:\n%s", got)
	}

	// An api too old to send `host_name` still names the host from the
	// hosts `repose ls` reads when one of them is quiet.
	b.Reset()
	writeProjectsTableHere(&b, []Project{{ID: "a", Slug: "todo-app", Class: "large", State: "running", HostID: "h-mac", HostUnreachable: true}},
		"", map[string]Host{"h-mac": {ID: "h-mac", Name: "mac-mini"}})
	if got := b.String(); !strings.Contains(got, "AGENTS  HOST\n") || !strings.Contains(got, "  mac-mini\n") {
		t.Fatalf("an api that sends no host_name:\n%s", got)
	}

	// `repose status` says the same in a row, last, where the -v
	// readout always was.
	mac := &Project{Slug: "todo-app", Class: "large", State: "stopped", HostID: "h-mac", HostName: "mac-mini", VolumeBytes: 20 << 30}
	b.Reset()
	writeStatus(&b, statusView{p: mac, mux: "tmux", now: time.Now()})
	if !strings.HasSuffix(b.String(), "\n  host       mac-mini\n") {
		t.Fatalf("a machine on the account's own host:\n%s", b.String())
	}

	// A machine on repose's servers keeps I-616's ruling: the host is a
	// platform internal there, and stays under -v.
	b.Reset()
	writeStatus(&b, statusView{p: &Project{Slug: "web", Class: "small", State: "stopped", HostID: "h-01", VolumeBytes: 20 << 30}, mux: "tmux", now: time.Now()})
	if got := b.String(); strings.Contains(got, "\n  host ") {
		t.Fatalf("a machine on repose's servers named its host:\n%s", got)
	}

	// -v on the same machine keeps one row, with the address.
	b.Reset()
	writeStatus(&b, statusView{p: mac, mux: "tmux", now: time.Now(),
		route: &Route{HostName: "mac-mini", GuestIP: "10.100.0.12"}})
	if got := b.String(); !strings.HasSuffix(got, "\n  host       mac-mini, ip 10.100.0.12\n") || strings.Count(got, "  host ") != 1 {
		t.Fatalf("-v on the account's own host:\n%s", got)
	}
}

// TestHostReadoutComesFromTheProjectRecord is the same readout end to
// end: the name is the api's `host_name` on the project (I-1392), so
// `repose ls` and `repose status` name the host with no /hosts read of
// their own, and hardware with none of the account's machines on it
// still changes nothing either command prints.
func TestHostReadoutComesFromTheProjectRecord(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}

	id := fake.AddOwnedHost("mac-mini")
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); strings.Contains(got, "HOST") || strings.Contains(got, "mac-mini") {
		t.Fatalf("a listing with no machine on the account's hardware:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); strings.Contains(got, "\n  host ") {
		t.Fatalf("a status with the machine on repose's servers:\n%s", got)
	}

	fake.SetProjectHost(p.ID, id)
	hosts := fake.Calls("GET /v1/hosts")
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "AGENTS  HOST\n") || !strings.Contains(got, "mac-mini") {
		t.Fatalf("repose ls on the account's own host:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "\n  host       mac-mini\n") {
		t.Fatalf("repose status on the account's own host:\n%s", got)
	}
	if n := fake.Calls("GET /v1/hosts") - hosts; n != 0 {
		t.Fatalf("the readout cost %d /hosts read(s); the name is on the project", n)
	}
}

// TestRunHostPicksOneOfSeveral is `--host NAME` with two hosts of the
// account's own (DECISIONS I-1584): the machine is made on the host
// named, the create line names it, and a named host that is offline
// refuses as that host with exit 8 rather than using the other.
func TestRunHostPicksOneOfSeveral(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, errOut := freshEnv(f.env, f.local)
	a := fake.AddOwnedHost("box-a")
	b := fake.AddOwnedHost("box-b")

	if err := runRun(ctx, e, RunOptions{Name: "web", Host: "box-b", NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if p := bySlug(listed(t, e), "web"); p == nil || p.HostID != b {
		t.Fatalf("--host box-b made %+v, want it on box-b", p)
	}
	// No class: a machine on a host of the account's own has no size
	// (I-1632).
	if out := errOut.buf.String(); !strings.Contains(out, "Created web on box-b ") || strings.Contains(out, "Created web on box-b (") {
		t.Fatalf("create output does not name the host:\n%s", out)
	}

	fake.SetOwnedHostOffline(a, 14*time.Minute)
	err := runRun(ctx, e, RunOptions{Name: "api", Host: "box-a", NoSync: true, NoAttach: true}, false)
	if exitCodeOf(err) != ExitCapacity || err.Error() != "your host box-a has not answered for 14m." {
		t.Fatalf("--host on an offline host: %v, want exit 8 naming box-a alone", err)
	}
	if bySlug(listed(t, e), "api") != nil {
		t.Fatal("a refused --host made a project on the other host")
	}
}

// TestCreateWaitEndsWhenTheHostIsRemoved: the host a create waits for is
// removed while `repose run` waits (I-1700): the run fails with the
// api's reason, and nothing is left behind.
func TestCreateWaitEndsWhenTheHostIsRemoved(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, out, errOut := freshEnv(f.env, f.local)
	h, err := e.Client.CreateHost(ctx, CreateHostRequest{Name: "mac-mini", Provider: "mac"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runRun(ctx, e, RunOptions{Name: "todo-app", NoSync: true, NoAttach: true}, false) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if p := bySlug(listed(t, e), "todo-app"); p != nil && p.WaitingForHost != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the create never waited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := e.Client.DeleteHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run still waits after the host was removed")
	}
	errOut.mu.Lock()
	stderr := errOut.buf.String()
	errOut.mu.Unlock()
	if want := "todo-app was not created: your host mac-mini was removed before it finished installing."; exitCodeOf(err) != ExitCapacity || err == nil || err.Error() != want {
		t.Fatalf("run after the host was removed: %v (exit %d, stderr %q), want exit 8 with %q", err, exitCodeOf(err), stderr, want)
	}
	if p := bySlug(listed(t, e), "todo-app"); p != nil {
		t.Fatalf("left behind: %+v", p)
	}
	_ = out
}

// TestRmTakesBackAWaitingProject: `repose rm` on a project whose create
// waits for its host is not refused behind the create (I-1700): the
// project goes, and the host's registration later makes nothing.
func TestRmTakesBackAWaitingProject(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)
	if _, err := e.Client.CreateHost(ctx, CreateHostRequest{Name: "mac-mini", Provider: "mac"}); err != nil {
		t.Fatal(err)
	}
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "small"})
	if err != nil || p.WaitingForHost == nil {
		t.Fatalf("create: %+v %v, want it to wait", p, err)
	}
	if err := DestroyCmd(ctx, e, "todo-app", true, true, nil); err != nil {
		t.Fatalf("rm while it waits: %v", err)
	}
	fake.SetOwnedHostState("mac-mini", "ready")
	if got := bySlug(listed(t, e), "todo-app"); got != nil {
		t.Fatalf("after rm and the host registering: %+v", got)
	}
}
