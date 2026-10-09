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
	if got := syncResultLine(&SyncSummary{Modified: 3, Unchanged: true}, false); got != "" {
		t.Fatalf("attaching run, unchanged: %q", got)
	}
	if got := syncResultLine(&SyncSummary{Modified: 3}, false); got != "Synced: 3 modified, 0 untracked" {
		t.Fatalf("attaching run, sent: %q", got)
	}
	if got := syncResultLine(&SyncSummary{Unchanged: true}, true); !strings.HasPrefix(got, "Nothing new to sync") {
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
