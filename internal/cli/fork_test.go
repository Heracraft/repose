package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestFork is I-254 against the fake api: N projects cloned from the
// source as it is now (I-701), named <slug>-fork-<k>, running with the
// source's secrets and without its remote, the summary, a timed line per
// copy (I-702), --json, the prompt sent to each, and the refusals that
// happen before anything is created.
func TestFork(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out, errOut strings.Builder
	e.Out, e.ErrOut = &out, &errOut
	ctx := context.Background()
	src, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large", RemoteURL: "github.com/owner/izma"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.PutSecret(ctx, src.ID, "DATABASE_URL", []byte("postgres://x")); err != nil {
		t.Fatal(err)
	}

	type started struct{ slug, agent, prompt string }
	var agents []started
	defer func(f func(context.Context, *Env, string, string, string) error) { forkStartAgent = f }(forkStartAgent)
	forkStartAgent = func(_ context.Context, _ *Env, slug, agent, prompt string) error {
		agents = append(agents, started{slug, agent, prompt})
		return nil
	}

	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 2, Prompt: "make the tests pass", Agent: "codex"}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Forked izma into 2 projects in ",
		"  izma-fork-1  running (large)\n",
		"  izma-fork-2  running (large)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fork said:\n%s\nwant %q", got, want)
		}
	}
	// Every copy's start ends in a timed line, not in silence (I-702).
	for _, want := range []string{"✓ Started izma-fork-1  ", "✓ Started izma-fork-2  "} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("fork's progress:\n%s\nwant %q", errOut.String(), want)
		}
	}
	if strings.Contains(errOut.String(), "Snapshot") {
		t.Fatalf("a cloned fork took a snapshot:\n%s", errOut.String())
	}
	// No footer: the table is the answer, and a stopped or running fork
	// is not billed by the hour (I-484, I-570).
	if !strings.HasSuffix(got, "  izma-fork-2  running (large)\n") || strings.Contains(got, "billed") {
		t.Fatalf("fork printed a line after its table:\n%s", got)
	}
	if strings.Contains(got, "`repose ") {
		t.Fatalf("fork names a command after it worked (I-484):\n%s", got)
	}
	if len(agents) != 2 || agents[0] != (started{"izma-fork-1", "codex", "make the tests pass"}) || agents[1].slug != "izma-fork-2" {
		t.Fatalf("agents started: %+v", agents)
	}
	for _, slug := range []string{"izma-fork-1", "izma-fork-2"} {
		p, err := findByIDOrSlug(ctx, e.Client, slug)
		if err != nil || p == nil || p.State != "running" || p.RemoteURL != "" || p.Class != "large" {
			t.Fatalf("%s: %+v %v", slug, p, err)
		}
		secrets, err := e.Client.ListSecrets(ctx, p.ID)
		if err != nil || len(secrets) != 1 || secrets[0].Name != "DATABASE_URL" {
			t.Fatalf("%s secrets: %+v %v", slug, secrets, err)
		}
	}
	// The source still has its remote, and no snapshot was taken of it:
	// the copies are clones (I-701, I-703).
	if p, _ := findByIDOrSlug(ctx, e.Client, "izma"); p == nil || p.RemoteURL != "github.com/owner/izma" || p.State != "running" {
		t.Fatalf("source after the fork: %+v", p)
	}
	if snaps, err := e.Client.ListSnapshots(ctx, src.ID); err != nil || len(snaps) != 0 {
		t.Fatalf("a cloned fork left snapshots of the source: %+v %v", snaps, err)
	}
	if _, err := e.Client.CreateSnapshot(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, src.ID)
	if err != nil || len(snaps) != 1 || snaps[0].Reason != "manual" {
		t.Fatalf("snapshots of the source: %+v %v", snaps, err)
	}

	// At the account's cap, running or stopped (I-569): refused before a
	// snapshot is taken, in the words every command uses. Stopped
	// projects fill it, on Solo as on any plan.
	for i := 0; i < fakeapi.ProjectCap-3; i++ {
		f, err := fake.CreateProject(fmt.Sprintf("filler-%d", i), "small")
		if err != nil {
			t.Fatal(err)
		}
		fake.SetState(f.ID, "stopped")
	}
	fake.SetBilling(fakeapi.BillingActive)
	fake.SetPlan("solo")
	err = ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1})
	if ee, ok := err.(*exitError); !ok || ee.code != ExitPaymentRequired || ee.msg != "You have 100 of the 100 projects an account can have, running or stopped. Destroy one first with `repose rm PROJECT`." {
		t.Fatalf("fork at the limit: %v", err)
	}
	fake.SetBilling(fakeapi.BillingExempt)
	if snaps, _ := e.Client.ListSnapshots(ctx, src.ID); len(snaps) != 1 {
		t.Fatalf("a refused fork took a snapshot: %+v", snaps)
	}
	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 11}); err == nil || !strings.Contains(err.Error(), "--count must be 1 to 10") {
		t.Fatalf("count 11: %v", err)
	}

	// Room for one more: --json, from the snapshot already taken, with a name.
	fork2, _ := findByIDOrSlug(ctx, e.Client, "izma-fork-2")
	if err := DestroyCmd(ctx, e, fork2.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	e.JSON = true
	agents = nil
	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1, SnapshotID: shortID(snaps[0].ID), Name: "try", Size: "small"}); err != nil {
		t.Fatalf("fork --snapshot --json: %v", err)
	}
	var res ForkResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.SnapshotID != snaps[0].ID || len(res.Projects) != 1 ||
		res.Projects[0].Slug != "try-1" || res.Projects[0].Class != "small" || res.Projects[0].State != "running" {
		t.Fatalf("fork --json: %v %s", err, out.String())
	}
	if len(agents) != 0 {
		t.Fatalf("an agent started without --prompt: %+v", agents)
	}
	if snaps, _ := e.Client.ListSnapshots(ctx, src.ID); len(snaps) != 1 {
		t.Fatalf("--snapshot took another snapshot: %+v", snaps)
	}
	e.JSON = false

	try1, _ := findByIDOrSlug(ctx, e.Client, "try-1")
	if err := DestroyCmd(ctx, e, try1.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	// A cloned fork has no snapshot: --json says null, as the api does.
	out.Reset()
	e.JSON = true
	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1, Name: "cl"}); err != nil {
		t.Fatalf("fork --json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw["snapshot_id"]; !ok || v != nil {
		t.Fatalf("a cloned fork's --json snapshot_id: %#v\n%s", v, out.String())
	}
	if cl1, _ := findByIDOrSlug(ctx, e.Client, "cl-1"); cl1 == nil {
		t.Fatal("no cl-1")
	} else if err := DestroyCmd(ctx, e, cl1.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	e.JSON = false

	// A snapshot that is not the project's: nothing is created.
	err = ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1, SnapshotID: "01900000-0000-7000-8000-999999999999"})
	if ee, ok := err.(*exitError); !ok || !strings.Contains(ee.msg, "Could not fork izma: that snapshot is not one of izma's, or it has expired. Nothing was created.") {
		t.Fatalf("unknown snapshot: %v", err)
	}
	// izma, izma-fork-1 and the fillers.
	if ps, _ := e.Client.ListProjects(ctx); len(ps) != fakeapi.ProjectCap-1 {
		t.Fatalf("projects after a refused fork: %d", len(ps))
	}
}

// When the source's server cannot hold the copies (409 snapshot_needed),
// or the api is older than clones, fork takes a snapshot of its own,
// reason fork, and restores the copies from it (I-701, I-703).
func TestForkFallsBackToAForkSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name           string
		noClone, older bool
		reason         string
	}{{"server full", true, false, "fork"}, {"older api", false, true, "manual"}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeapi.New(fakeapi.Options{})
			defer fake.Close()
			fake.SetForkClone(tc.noClone, tc.older)
			e := newLifecycleEnv(t, fake)
			var out, errOut strings.Builder
			e.Out, e.ErrOut = &out, &errOut
			ctx := context.Background()
			src, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
			if err != nil {
				t.Fatal(err)
			}
			if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1}); err != nil {
				t.Fatalf("fork: %v", err)
			}
			if !strings.Contains(out.String(), "Forked izma into 1 project from its snapshot of ") {
				t.Fatalf("fork said:\n%s", out.String())
			}
			for _, want := range []string{"✓ Snapshot of izma taken  ", "✓ Started izma-fork-1  "} {
				if !strings.Contains(errOut.String(), want) {
					t.Fatalf("fork's progress:\n%s\nwant %q", errOut.String(), want)
				}
			}
			// Off a terminal the fork is announced once, the snapshot
			// between its start and its copies.
			if n := strings.Count(errOut.String(), "Forking izma into 1"); n != 1 {
				t.Fatalf("Forking announced %d times:\n%s", n, errOut.String())
			}
			snaps, err := e.Client.ListSnapshots(ctx, src.ID)
			// An api older than I-703 ignores the body's reason and keeps
			// the snapshot as a manual one, with no expiry.
			if err != nil || len(snaps) != 1 || snaps[0].Reason != tc.reason || (snaps[0].ExpiresAt == nil) != (tc.reason == "manual") {
				t.Fatalf("the fork's snapshot: %+v %v", snaps, err)
			}
			if p, _ := findByIDOrSlug(ctx, e.Client, "izma-fork-1"); p == nil || p.State != "running" {
				t.Fatalf("copy: %+v", p)
			}
		})
	}
}

// TestNewRequestIDIsAUUID: the api decodes request_id as a uuid.
func TestNewRequestIDIsAUUID(t *testing.T) {
	a, b := newRequestID(), newRequestID()
	if a == b || !looksLikeUUID(a) || a[14] != '4' {
		t.Fatalf("request ids %q %q", a, b)
	}
}

// The first copy is watched from its start, so each of its steps gets a
// line with that step's time. The others run beside it, so by the time the
// CLI reads one it may be steps in: it gets one line with its op's own
// time, never a step timed from when the CLI got to it (I-702).
func TestForkTimesEachStep(t *testing.T) {
	const d = 400 * time.Millisecond
	fake := fakeapi.New(fakeapi.Options{ForkStepDelay: d})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out, errOut strings.Builder
	e.Out, e.ErrOut = &out, &errOut
	ctx := context.Background()
	if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"}); err != nil {
		t.Fatal(err)
	}
	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 2}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	took := func(prefix string) time.Duration {
		t.Helper()
		for _, l := range strings.Split(errOut.String(), "\n") {
			if rest, ok := strings.CutPrefix(l, prefix+"  "); ok {
				v, err := time.ParseDuration(rest)
				if err != nil {
					t.Fatalf("line %q: %v", l, err)
				}
				return v
			}
		}
		t.Fatalf("fork's progress:\n%s\nwant a line %q", errOut.String(), prefix)
		return 0
	}
	// The bounds are loose on purpose. What this test is for is that a
	// step's line carries ITS OWN time and copy 2's carries its whole
	// op's -- d against 3d -- so anything that separates those two will
	// do. A tight lower bound does not: on a loaded box the measured
	// time comes in UNDER the fake's own sleep, because the timer starts
	// late, and `v < d-50ms` failed at 300ms against a 400ms step while
	// the same test passed alone on the same tree (2026-10-17). A test
	// that fails when the box is busy is a test nobody can read.
	if v := took("✓ Copied izma's disk into izma-fork-1"); v < d/2 || v > 2*d {
		t.Fatalf("copy 1's disk step took %v, want about %v", v, d)
	}
	if v := took("✓ Started izma-fork-1"); v < d/2 || v > 2*d {
		t.Fatalf("copy 1's start took %v, want about %v", v, d)
	}
	// Copy 2: restore d, start 2d; the CLI reaches it a d into its start.
	// Still well clear of copy 1's 2d ceiling, so the two cannot be
	// confused for one another.
	if v := took("✓ Started izma-fork-2"); v < 5*d/2 {
		t.Fatalf("copy 2's line says %v, want its whole op's time, about %v:\n%s", v, 3*d, errOut.String())
	}
	if strings.Contains(errOut.String(), "into izma-fork-2") {
		t.Fatalf("copy 2's disk step, which the CLI did not watch, got a line:\n%s", errOut.String())
	}
}
