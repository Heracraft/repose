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
	if exitCodeOf(err) != ExitUsage || !strings.Contains(err.Error(), "web exists and stays on its host") {
		t.Fatalf("--host on a project that exists: %v", err)
	}
	if p := bySlug(listed(t, e), "web"); p.HostID != mac || p.State != "running" {
		t.Fatalf("after the refusal: %+v", p)
	}
}

// TestCreateRefusedWhileOwnHostWaitsNamesTheWayOut is the P0 of
// 2026-11-19 (ENG-28, ENG-67): a customer whose Mac has not finished
// installing could create a project nowhere. The create is refused with
// the api's sentence about their host, nothing is stranded, the refusal
// names `--host repose`, and that flag makes the machine on repose's
// servers.
func TestCreateRefusedWhileOwnHostWaitsNamesTheWayOut(t *testing.T) {
	ctx := context.Background()
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	e, _, _ := freshEnv(f.env, f.local)
	fake.AddOwnedHost("mac-mini")
	fake.SetOwnedHostState("mac-mini", "registering")

	for attempt := 1; attempt <= 2; attempt++ {
		err := runRun(ctx, e, RunOptions{Name: "todo-app", NoSync: true, NoAttach: true}, false)
		want := "your host mac-mini has not registered yet. `repose run --host repose` makes it on repose's servers instead."
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
		t.Fatalf("--host repose while the Mac waits: %+v", p)
	}

	// A plan that sells no memory on repose's servers has no way out to
	// name: the sentence stands alone.
	fake.SetHostLimit(1)
	err := runRun(ctx, e, RunOptions{Name: "api", NoSync: true, NoAttach: true}, false)
	if exitCodeOf(err) != ExitCapacity || err.Error() != "your host mac-mini has not registered yet." {
		t.Fatalf("own hardware plan: %v", err)
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
