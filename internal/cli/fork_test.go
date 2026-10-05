package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// TestFork is I-254 against the fake api: a snapshot now, N projects
// named <slug>-fork-<k> running with the source's secrets and without its
// remote, the summary, --json, the prompt sent to each, and the refusals
// that happen before anything is created.
func TestFork(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
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
		"Forked izma into 2 projects from its snapshot of ",
		"  izma-fork-1  running (large)\n",
		"  izma-fork-2  running (large)\n",
		"izma is unchanged and is still its checkout's project.\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fork said:\n%s\nwant %q", got, want)
		}
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
	// The source still has its remote, and a snapshot was taken of it.
	if p, _ := findByIDOrSlug(ctx, e.Client, "izma"); p == nil || p.RemoteURL != "github.com/owner/izma" || p.State != "running" {
		t.Fatalf("source after the fork: %+v", p)
	}
	snaps, err := e.Client.ListSnapshots(ctx, src.ID)
	if err != nil || len(snaps) != 1 || snaps[0].Reason != "manual" {
		t.Fatalf("snapshots of the source: %+v %v", snaps, err)
	}

	// At the plan's limit: refused before a snapshot is taken. The fake's
	// default account is exempt (I-295), so the limit is Solo's 10 here;
	// seven more projects fill it.
	for i := 0; i < 7; i++ {
		if _, err := fake.CreateProject(fmt.Sprintf("filler-%d", i), "small"); err != nil {
			t.Fatal(err)
		}
	}
	fake.SetBilling(fakeapi.BillingActive)
	fake.SetPlan("solo")
	err = ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1})
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGeneric || !strings.Contains(ee.msg, "You have 10 of 10 projects (Solo's limit), and 1 more would make 11") {
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
	if err := ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1, SnapshotID: snaps[0].ID, Name: "try", Size: "small"}); err != nil {
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

	// A snapshot that is not the project's: nothing is created.
	try1, _ := findByIDOrSlug(ctx, e.Client, "try-1")
	if err := DestroyCmd(ctx, e, try1.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	err = ForkCmd(ctx, e, ForkOptions{ProjectArg: "izma", Count: 1, SnapshotID: "01900000-0000-7000-8000-999999999999"})
	if ee, ok := err.(*exitError); !ok || !strings.Contains(ee.msg, "Could not fork izma: that snapshot is not one of izma's, or it has expired. Nothing was created.") {
		t.Fatalf("unknown snapshot: %v", err)
	}
	// izma, izma-fork-1 and the seven fillers.
	if ps, _ := e.Client.ListProjects(ctx); len(ps) != 9 {
		t.Fatalf("projects after a refused fork: %d", len(ps))
	}
}

// TestNewRequestIDIsAUUID: the api decodes request_id as a uuid.
func TestNewRequestIDIsAUUID(t *testing.T) {
	a, b := newRequestID(), newRequestID()
	if a == b || !looksLikeUUID(a) || a[14] != '4' {
		t.Fatalf("request ids %q %q", a, b)
	}
}
