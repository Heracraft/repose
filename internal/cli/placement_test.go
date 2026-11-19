package cli

import (
	"context"
	"strings"
	"testing"

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
