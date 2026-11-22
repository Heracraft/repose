package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// Second-pass P1 (I-1532): `repose sync` in a checkout with no machine
// never creates one on its own. Without a terminal it refuses with the
// command that creates one, and nothing is created.
func TestSyncWithNoMachineRefusesWithoutTerminal(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	err := runRun(context.Background(), f.env, RunOptions{NoAttach: true, Sync: true}, false)
	if exitCode(err) != ExitProjectNotFound || err.Error() != "No repose machine for this checkout; `repose run` creates one." {
		t.Fatalf("got %v (exit %d)", err, exitCode(err))
	}
	if n := fake.Calls("POST /v1/projects"); n != 0 {
		t.Fatalf("sync created %d machines", n)
	}
}

// On a terminal sync asks once, naming the machine and its size, and a
// no (the default) creates nothing; a yes creates it and syncs.
func TestSyncWithNoMachineAsksOnTerminal(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	var asked []string
	no := func(prompt string) (bool, error) { asked = append(asked, prompt); return false, nil }
	err := runRun(context.Background(), f.env, RunOptions{NoAttach: true, Sync: true, Confirm: no}, false)
	if exitCode(err) != ExitGeneric || err.Error() != "Not created." {
		t.Fatalf("no: %v (exit %d)", err, exitCode(err))
	}
	if len(asked) != 1 || !strings.HasPrefix(asked[0], "No machine for this checkout. Create ") || !strings.HasSuffix(asked[0], " (large: 2 cores, 8 GB) and sync to it? [y/N] ") {
		t.Fatalf("asked %q", asked)
	}
	if n := fake.Calls("POST /v1/projects"); n != 0 {
		t.Fatalf("a no created %d machines", n)
	}
	yes := func(string) (bool, error) { return true, nil }
	if err := runRun(context.Background(), f.env, RunOptions{NoAttach: true, Sync: true, Confirm: yes}, false); err != nil {
		t.Fatalf("yes: %v", err)
	}
	if n := fake.Calls("POST /v1/projects"); n != 1 {
		t.Fatalf("a yes created %d machines", n)
	}
	// The question named the machine the yes made.
	projects, err := f.env.Client.ListProjects(context.Background())
	if err != nil || len(projects) != 1 || !strings.Contains(asked[0], "Create "+projects[0].Name+" (") {
		t.Fatalf("made %+v after asking %q (%v)", projects, asked[0], err)
	}
}

// syncYes answers sync's create question yes, for the tests whose first
// sync is meant to make the machine (I-1532).
func syncYes(string) (bool, error) { return true, nil }
