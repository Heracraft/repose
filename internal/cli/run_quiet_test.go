package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// A second run with nothing new on the laptop printed "Synced: 3
// modified, 0 untracked; the machine already had them" before attaching;
// it now says nothing about the sync, and `repose sync` says there was
// nothing to send (I-303).
func TestSecondRunIsQuietAboutAnUnchangedSync(t *testing.T) {
	if got := syncResultLine(&SyncSummary{Modified: 3, Unchanged: true}, false, ""); got != "" {
		t.Fatalf("attaching run, unchanged: %q", got)
	}
	if got := syncResultLine(&SyncSummary{Modified: 3}, false, ""); got != "Synced: 3 modified, 0 untracked" {
		t.Fatalf("attaching run, sent: %q", got)
	}
	if got := syncResultLine(&SyncSummary{Unchanged: true}, true, ""); !strings.HasPrefix(got, "Nothing new to sync") {
		t.Fatalf("sync, unchanged: %q", got)
	}

	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	var out strings.Builder
	f.env.Out = &out
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if !strings.Contains(out.String(), "Synced: ") {
		t.Fatalf("first run: %q", out.String())
	}
	out.Reset()
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("second runRun: %v", err)
	}
	if strings.Contains(out.String(), "Synced: ") || !strings.Contains(out.String(), "Nothing new to sync: the machine already has this checkout.") {
		t.Fatalf("second run: %q", out.String())
	}
}

// A run that named its project names it in the sync line's command
// (I-631).
func TestLaptopAheadLineNamesTheProject(t *testing.T) {
	s := &SyncSummary{Skipped: true, LaptopAhead: true, Commits: 1}
	if got := syncResultLine(s, false, ""); !strings.HasSuffix(got, "`repose sync` sends it.") {
		t.Errorf("unnamed: %q", got)
	}
	if got := syncResultLine(s, false, "todo-app"); !strings.HasSuffix(got, "`repose sync todo-app` sends it.") {
		t.Errorf("named: %q", got)
	}
}

// A run into a machine that has the checkout syncs nothing (I-367), so
// its phase is named for what it does: Syncing only on the run that
// creates the machine, and on `repose sync` (I-633).
func TestRunNamesItsPhaseForWhatItDoes(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	var errOut strings.Builder
	f.env.ErrOut = &errOut
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("first runRun: %v", err)
	}
	if !strings.Contains(errOut.String(), "Syncing...") {
		t.Fatalf("creating run: %q", errOut.String())
	}
	errOut.Reset()
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true}, false); err != nil {
		t.Fatalf("second runRun: %v", err)
	}
	if strings.Contains(errOut.String(), "Syncing...") || !strings.Contains(errOut.String(), "Copying logins...") {
		t.Fatalf("second run: %q", errOut.String())
	}
	errOut.Reset()
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(errOut.String(), "Syncing...") {
		t.Fatalf("sync: %q", errOut.String())
	}
}
