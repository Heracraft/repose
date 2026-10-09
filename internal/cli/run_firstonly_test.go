package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// emptyGuestCheckout makes the fixture's guest checkout what guestd leaves
// on a new machine: a git init with no commit.
func emptyGuestCheckout(t *testing.T, f *syncFixture) {
	t.Helper()
	if err := os.RemoveAll(f.guestRepo()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.guestRepo(), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.guestRepo(), "git", "init", "-q")
}

// The owner's case (DECISIONS I-367): an agent left uncommitted work on
// the machine and the laptop has new work too. `repose run` used to
// refuse with exit 6 and three choices; it now attaches to the machine as
// it is, says the laptop has work, and names `repose sync`.
func TestRunLeavesAnExistingCheckoutAlone(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	f.env.HomeDir = laptopHomeWithGH(t)
	out := &discardWriter{}
	f.env.Out = out
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "timing.txt"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "notes.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("run refused: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "hello\n" {
		t.Fatalf("guest README.md = %q, want it untouched", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "timing.txt")); string(b) != "agent\n" {
		t.Fatalf("the agent's file = %q, want it untouched", b)
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "notes.txt")); err == nil {
		t.Fatal("the laptop's untracked file reached the machine")
	}
	want := "Not synced: your laptop has work the machine doesn't (1 modified, 1 untracked). `repose sync " + testSlug + "` sends it."
	if !strings.Contains(out.buf.String(), want) {
		t.Fatalf("output = %q, want %q", out.buf.String(), want)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); !strings.Contains(string(b), "gho_test") {
		t.Fatalf("guest hosts.yml = %q, want the login copied all the same", b)
	}
	if strings.Contains(out.buf.String(), "Synced:") {
		t.Fatalf("output = %q, claims a sync", out.buf.String())
	}
}

// A laptop commit the machine never took counts too.
func TestRunNamesLaptopCommitsItDidNotSend(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	out := &discardWriter{}
	f.env.Out = out
	if err := os.WriteFile(filepath.Join(f.local, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "add", "a.txt")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "a")
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "(1 commit)") {
		t.Fatalf("output = %q, want the commit counted", out.buf.String())
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "a.txt")); err == nil {
		t.Fatal("the laptop's commit reached the machine")
	}
}

// With nothing new on the laptop, the run says nothing about syncing.
func TestRunIsQuietWhenTheLaptopHasNothingNew(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatal(err)
	}
	out := &discardWriter{}
	f.env.Out = out
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if s := out.buf.String(); strings.Contains(s, "sync") || strings.Contains(s, "Synced") {
		t.Fatalf("output = %q, want nothing about the sync", s)
	}
}

// A new machine (a git init, no commit) gets the checkout on the first run.
func TestRunSyncsIntoANewMachine(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	emptyGuestCheckout(t, f.syncFixture)
	out := &discardWriter{}
	f.env.Out = out
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop edit\n" {
		t.Fatalf("guest README.md = %q, want the laptop's", b)
	}
	if !strings.Contains(out.buf.String(), "Synced:") {
		t.Fatalf("output = %q, want the Synced line", out.buf.String())
	}
	// The second run leaves it alone.
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("second edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop edit\n" {
		t.Fatalf("guest README.md = %q after the second run, want the first sync's", b)
	}
}

// `repose sync` is the explicit sync: it lays the laptop's work over an
// existing checkout, keeps an agent's uncommitted work at other paths,
// and refuses over the agent's work at a path it writes, naming its own
// flags (I-573).
func TestSyncCommandSyncsAnExistingCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop edit\n" {
		t.Fatalf("guest README.md = %q, want the laptop's", b)
	}
	if err := os.WriteFile(filepath.Join(f.guestRepo(), "timing.txt"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("sync beside the agent's timing.txt: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "newer\n" {
		t.Fatalf("guest README.md = %q, want the laptop's", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "timing.txt")); string(b) != "agent\n" {
		t.Fatalf("the agent's timing.txt = %q, want it kept", b)
	}

	if err := os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("newest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree {
		t.Fatalf("err = %v, want exit %d", err, ExitDirtyRemoteTree)
	}
	for _, want := range []string{"  README.md\n", "repose sync --stash-machine"} {
		if !strings.Contains(ee.msg, want) {
			t.Errorf("refusal = %q, want %q", ee.msg, want)
		}
	}
	for _, not := range []string{"repose run", "timing.txt", "--discard-machine"} {
		if strings.Contains(ee.msg, not) {
			t.Errorf("refusal = %q, names %q", ee.msg, not)
		}
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true, StashRemote: true}, false); err != nil {
		t.Fatalf("sync --stash-machine: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "newest\n" {
		t.Fatalf("guest README.md = %q, want the laptop's", b)
	}
}

// run's old sync flags say where they went.
func TestRunStashRemoteSaysUseSync(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	for flag, opts := range map[string]RunOptions{
		"--stash-machine":   {Name: testSlug, StashRemote: true},
		"--discard-machine": {Name: testSlug, DiscardRemote: true},
	} {
		err := runRun(context.Background(), f.env, opts, false)
		ee, ok := err.(*exitError)
		if !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "repose sync "+flag) {
			t.Errorf("%s: err = %v, want exit 2 naming `repose sync %s`", flag, err, flag)
		}
	}
}
