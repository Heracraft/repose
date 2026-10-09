package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestDestroyThenRestoreByName is the owner's 2026-09-23 request end to
// end against the fake api: `repose rm izma` returns at once with
// `repose restore izma` (I-166), `repose ls --destroyed` lists it
// with its expiry, and `repose restore izma` brings it back under its
// name (I-167).
func TestDestroyThenRestoreByName(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large", RemoteURL: "github.com/owner/izma"})
	if err != nil {
		t.Fatal(err)
	}
	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	// With --yes nothing asked, so the line says until when (I-614).
	until := time.Now().AddDate(0, 0, 30).Local().Format("2006-01-02")
	if got := out.String(); got != "Destroying izma. Its final snapshot is kept until "+until+".\n" {
		t.Fatalf("destroy said %q", got)
	}

	out.Reset()
	if err := DestroyedCmd(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "PROJECT") || !strings.Contains(lines[0], "RESTORABLE UNTIL") ||
		!strings.HasPrefix(lines[1], "izma ") {
		t.Fatalf("projects --destroyed:\n%s", out.String())
	}
	out.Reset()
	e.JSON = true
	if err := DestroyedCmd(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil || len(decoded) != 1 || decoded[0]["restorable_until"] == nil {
		t.Fatalf("--destroyed --json: %v %s", err, out.String())
	}
	e.JSON = false

	out.Reset()
	if err := RestoreCmd(ctx, e, "izma", "", "", nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Restored izma from its 1.0 GB snapshot of ") || strings.Contains(out.String(), "`repose ") {
		t.Fatalf("restore said %q", out.String())
	}
	back, err := findByIDOrSlug(ctx, e.Client, "izma")
	if err != nil || back == nil || back.RemoteURL != "github.com/owner/izma" {
		t.Fatalf("restored project: %+v %v", back, err)
	}

	// `izma` now means the live project, which has no snapshot yet.
	err = RestoreCmd(ctx, e, "izma", "", "", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitProjectNotFound || !strings.Contains(ee.msg, "izma has no snapshot left to restore") {
		t.Fatalf("restore of a live project without snapshots: %v", err)
	}
	// The destroyed one, by the id the old destroy message printed: the
	// name is taken, so without a terminal the CLI says to pass --as,
	// with one it asks, and an empty answer restores nothing.
	err = RestoreCmd(ctx, e, p.ID, "", "", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "--as NEW-NAME") {
		t.Fatalf("restore onto a taken name: %v", err)
	}
	out.Reset()
	if err := RestoreCmd(ctx, e, p.ID, "", "", func(string) (string, error) { return "", nil }); exitCode(err) != ExitGeneric || err.Error() != "Nothing restored." {
		t.Fatalf("cancelled restore: %v %q", err, out.String())
	}
	if err := RestoreCmd(ctx, e, p.ID, "", "", func(string) (string, error) { return "", errPromptInterrupted }); exitCode(err) != ExitInterrupted || err.Error() != "Nothing restored." {
		t.Fatalf("Ctrl-C at the name: %v", err)
	}
	var asked string
	out.Reset()
	if err := RestoreCmd(ctx, e, p.ID, "", "", func(q string) (string, error) { asked = q; return "izma-old", nil }); err != nil {
		t.Fatalf("restore under an asked name: %v", err)
	}
	if !strings.Contains(asked, "A project called izma already exists") || !strings.HasPrefix(out.String(), "Restored izma-old") {
		t.Fatalf("asked %q, said %q", asked, out.String())
	}

	// Nothing by that name.
	err = RestoreCmd(ctx, e, "nope", "", "", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitProjectNotFound || !strings.Contains(ee.msg, "repose ls --destroyed") {
		t.Fatalf("restore of an unknown name: %v", err)
	}
	if err := RestoreCmd(ctx, e, "", "", "", nil); err == nil {
		t.Fatal("restore with no name accepted")
	}
}

// A failed destroy the CLI no longer waits for shows in `repose
// projects` with the api's reason, whose own next step (destroy again)
// wins over the generic "start restarts it" (I-165, I-166).
func TestProjectsShowAFailedDestroy(t *testing.T) {
	le := "internal: destroying izma failed: the host could not remove the volume. `repose rm izma` tries again"
	var out strings.Builder
	writeProjectsTable(&out, []Project{{Slug: "izma", Class: "large", State: "error", LastError: &le}})
	if !strings.Contains(out.String(), "izma: destroying izma failed") || !strings.Contains(out.String(), "`repose rm izma` tries again") ||
		strings.Contains(out.String(), "repose start izma") {
		t.Fatalf("projects table:\n%s", out.String())
	}
	out.Reset()
	writeProjectsTable(&out, []Project{{Slug: "izma", Class: "large", State: "destroying"}})
	if !strings.Contains(out.String(), "destroying") {
		t.Fatalf("projects table:\n%s", out.String())
	}
}

// TestNewestSnapshotIgnoresOrder: the api lists snapshots newest first,
// the fake oldest first; v0.1.5 took the last element.
func TestNewestSnapshotIgnoresOrder(t *testing.T) {
	now := mustTime(t, "2026-09-23T02:23:19Z")
	old := mustTime(t, "2026-09-22T03:00:00Z")
	for _, snaps := range [][]Snapshot{{{ID: "new", CreatedAt: now}, {ID: "old", CreatedAt: old}}, {{ID: "old", CreatedAt: old}, {ID: "new", CreatedAt: now}}} {
		if got := newestSnapshot(snaps); got == nil || got.ID != "new" {
			t.Fatalf("newest of %+v = %+v", snaps, got)
		}
	}
	if newestSnapshot(nil) != nil {
		t.Fatal("newest of none")
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRestoreWithoutANameInACheckout: `repose restore` with no NAME finds
// the destroyed project by the checkout's remote (I-172): one name
// restores without asking, two names ask on a terminal (an unknown answer
// asks again) and are listed otherwise, a checkout whose remote matches
// nothing, and a directory with no remote, say what to type.
func TestRestoreWithoutANameInACheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	if err := RestoreCmd(ctx, e, "", "", "", nil); err == nil || !strings.Contains(err.Error(), "no git remote") {
		t.Fatalf("no remote: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:Owner/izma.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", e.Cwd}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	err := RestoreCmd(ctx, e, "", "", "", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitProjectNotFound || !strings.Contains(ee.msg, "github.com/owner/izma") {
		t.Fatalf("nothing destroyed: %v", err)
	}
	destroy := func(name, remote string) {
		t.Helper()
		p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: name, Class: "small", RemoteURL: remote})
		if err != nil {
			t.Fatal(err)
		}
		if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	destroy("other", "github.com/owner/other")
	destroy("izma", "github.com/owner/izma")
	out.Reset()
	if err := RestoreCmd(ctx, e, "", "", "", func(string) (string, error) { t.Fatal("asked with one match"); return "", nil }); err != nil {
		t.Fatalf("restore by remote: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Restored izma from its 1.0 GB snapshot of ") {
		t.Fatalf("restore said %q", out.String())
	}

	// Two destroyed names with this remote: the restored izma and a fork.
	back, _ := findByIDOrSlug(ctx, e.Client, "izma")
	if err := DestroyCmd(ctx, e, back.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	destroy("izma-fork", "github.com/owner/izma")
	err = RestoreCmd(ctx, e, "", "", "", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "izma-fork") || !strings.Contains(ee.msg, "izma,") && !strings.Contains(ee.msg, ": izma") {
		t.Fatalf("ambiguous without a terminal: %v", err)
	}
	var asked []string
	answers := []string{"nope", "izma"}
	out.Reset()
	if err := RestoreCmd(ctx, e, "", "", "", func(q string) (string, error) {
		asked = append(asked, q)
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}); err != nil {
		t.Fatalf("ambiguous with a terminal: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(asked[0], "Which one") || !strings.HasPrefix(out.String(), "Restored izma ") {
		t.Fatalf("asked %q, said %q", asked, out.String())
	}
}

// I-420: the CLI reads every page of the destroyed list, past the api's
// first 100.
func TestListDestroyedReadsEveryPage(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	for i := 0; i < 230; i++ {
		p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: fmt.Sprintf("gone-%03d", i), Class: "large"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Client.DestroyProject(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.Client.ListDestroyed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range list {
		seen[d.Slug] = true
	}
	if len(list) != 230 || len(seen) != 230 {
		t.Fatalf("ListDestroyed gave %d (%d distinct), want 230", len(list), len(seen))
	}
}

// `repose restore NAME --snapshot ID` takes the short id `repose
// snapshots list` prints, the way `snapshots restore` does (I-619).
func TestRestoreTakesAShortSnapshotID(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "zed", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, p.ID)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("snapshots of the destroyed project: %+v %v", snaps, err)
	}
	err = RestoreCmd(ctx, e, "zed", "", "ffffffff", nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "zed has no snapshot ffffffff") {
		t.Fatalf("restore of an unknown short id: %v", err)
	}
	out.Reset()
	if err := RestoreCmd(ctx, e, "zed", "", shortID(snaps[0].ID), nil); err != nil {
		t.Fatalf("restore --snapshot SHORT: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Restored zed from") {
		t.Fatalf("restore said %q", out.String())
	}
}
