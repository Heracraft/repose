package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/billing"
	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/hostd/guest"
)

// TestClassSpecsMatchBillingAndHost pins the CLI's copy of the class table
// to the memory billing's plans count and the vCPUs and memory hostd boots.
func TestClassSpecsMatchBillingAndHost(t *testing.T) {
	for class, c := range classSpecs {
		if c.MemGB != billing.ClassMemoryGB(class) {
			t.Errorf("%s: cli %d GB, billing %d GB", class, c.MemGB, billing.ClassMemoryGB(class))
		}
		wantPlan := billing.Solo
		if c.MemGB > billing.Solo.MemoryGB {
			wantPlan = billing.Plus
		}
		if c.Plan != wantPlan.Name {
			t.Errorf("%s: cli says %s, the smallest plan with %d GB is %s", class, c.Plan, c.MemGB, wantPlan.Name)
		}
		h, ok := guest.Classes[class]
		if !ok || uint64(c.VCPUs) != uint64(h.VCPUs) || uint64(c.MemGB)*1024 != uint64(h.MemMiB) {
			t.Errorf("%s: cli %d vCPU %d GB, hostd %+v", class, c.VCPUs, c.MemGB, h)
		}
	}
	if len(classSpecs) != len(guest.Classes) {
		t.Errorf("cli has %d classes, hostd %d", len(classSpecs), len(guest.Classes))
	}
	if got := classSummary("large"); got != "4 vCPU, 8 GB memory; fits the Solo plan" {
		t.Errorf("summary %q", got)
	}
}

func TestResizeClass(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*fakeapi.Fake, *Env, *bytes.Buffer, *Project) {
		fake := fakeapi.New(fakeapi.Options{})
		t.Cleanup(fake.Close)
		e := newLifecycleEnv(t, fake)
		out := &bytes.Buffer{}
		e.Out = out
		p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
		if err != nil {
			t.Fatal(err)
		}
		return fake, e, out, p
	}
	get := func(t *testing.T, e *Env, id string) *Project {
		p, err := e.Client.GetProject(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("stopped project changes and stays stopped", func(t *testing.T) {
		_, e, out, p := setup(t)
		if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
			t.Fatal(err)
		}
		if err := ResizeClassCmd(ctx, e, p.ID, "xl", func(string) (bool, error) {
			t.Fatal("a stopped project needs no confirmation")
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := get(t, e, p.ID); got.Class != "xl" || got.State != "stopped" {
			t.Fatalf("after: %s %s", got.Class, got.State)
		}
		if !strings.Contains(out.String(), "from large to xl: 8 vCPU, 16 GB memory; needs the Plus plan. It starts at the new size.\n") || strings.Contains(out.String(), "`repose ") {
			t.Fatalf("output %q", out.String())
		}
	})

	t.Run("same class is a no-op", func(t *testing.T) {
		_, e, out, p := setup(t)
		if err := ResizeClassCmd(ctx, e, p.ID, "large", nil); err != nil {
			t.Fatal(err)
		}
		if got := get(t, e, p.ID); got.State != "running" {
			t.Fatalf("a no-op stopped the project: %s", got.State)
		}
		if !strings.Contains(out.String(), "todo-app is already large") {
			t.Fatalf("output %q", out.String())
		}
	})

	t.Run("running project declined stays as it was", func(t *testing.T) {
		_, e, out, p := setup(t)
		var asked string
		if err := ResizeClassCmd(ctx, e, p.ID, "small", func(prompt string) (bool, error) { asked = prompt; return false, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(asked, "agents included") || !strings.HasSuffix(asked, "[y/N] ") || strings.Contains(asked, "snapshot") {
			t.Fatalf("prompt %q", asked)
		}
		if got := get(t, e, p.ID); got.Class != "large" || got.State != "running" {
			t.Fatalf("after declining: %s %s", got.Class, got.State)
		}
		if !strings.Contains(out.String(), "Not changed") {
			t.Fatalf("output %q", out.String())
		}
	})

	t.Run("running project with --yes is stopped, changed and started", func(t *testing.T) {
		_, e, out, p := setup(t)
		if err := ResizeClassCmd(ctx, e, p.ID, "small", nil); err != nil {
			t.Fatal(err)
		}
		if got := get(t, e, p.ID); got.Class != "small" || got.State != "running" {
			t.Fatalf("after: %s %s", got.Class, got.State)
		}
		if !strings.Contains(out.String(), "Changed todo-app from large to small: 2 vCPU, 4 GB memory; fits the Solo plan. Running again") {
			t.Fatalf("output %q", out.String())
		}
		// The disk stays on the host, untouched; a snapshot was most of
		// the restart's time (I-595).
		if snaps, err := e.Client.ListSnapshots(ctx, p.ID); err != nil || len(snaps) != 0 {
			t.Fatalf("a size change took snapshots %+v (%v)", snaps, err)
		}
	})

	t.Run("a refused change starts the machine again", func(t *testing.T) {
		fake, e, _, p := setup(t)
		fake.FailNext("PATCH", "/projects/"+p.ID, "invalid")
		err := ResizeClassCmd(ctx, e, p.ID, "xl", nil)
		if err == nil {
			t.Fatal("want the PATCH error")
		}
		if got := get(t, e, p.ID); got.Class != "large" || got.State != "running" {
			t.Fatalf("after a refused change: %s %s", got.Class, got.State)
		}
	})

	t.Run("unknown class is a usage error", func(t *testing.T) {
		_, e, _, p := setup(t)
		err := ResizeClassCmd(ctx, e, p.ID, "medium", nil)
		if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage {
			t.Fatalf("err = %v", err)
		}
	})
}
