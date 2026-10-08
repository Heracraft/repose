package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose ls` and `repose status` say so while the projects hold more
// than the plan's disk, and only then (DECISIONS I-585): creating,
// restoring, forking and growing a disk are refused until they hold less.
func TestDiskOverPlanLine(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "stopped")
	const line = "disk: your projects hold 112.4 GB of the plan's 100 GB; creating, restoring, forking and growing a disk are refused until they hold less"
	held := func(gb float64) {
		t.Helper()
		if err := fake.SetBillingState(fakeapi.BillingState{DiskHeldGB: &gb}); err != nil {
			t.Fatal(err)
		}
	}
	run := func() string {
		t.Helper()
		out.Reset()
		if err := ProjectsCmd(ctx, e); err != nil {
			t.Fatal(err)
		}
		ls := out.String()
		out.Reset()
		if err := StatusCmd(ctx, e, "izma"); err != nil {
			t.Fatal(err)
		}
		return ls + "\n--\n" + out.String()
	}

	// No plan (billing off): nothing.
	held(112.4)
	if got := run(); strings.Contains(got, "disk:") {
		t.Fatalf("a line without a plan:\n%s", got)
	}
	fake.SetBilling(fakeapi.BillingActive)
	fake.SetPlan("solo")
	got := run()
	if strings.Count(got, line) != 2 {
		t.Fatalf("over the plan, ls and status each want the line:\n%s", got)
	}
	held(100)
	if got := run(); strings.Contains(got, "disk:") {
		t.Fatalf("a line at the plan's disk:\n%s", got)
	}
}
