package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The resize of 2026-10-07 17:38Z: `repose resize kanali 100G --size xl`
// printed "✓ Started kanali 0.8s" and then "Could not start kanali". A
// start's first phase has no ✓ of its own: it printed as the boot began.
func TestResizeClassPrintsNoSuccessBeforeTheStartFails(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StartDelay: 300 * time.Millisecond})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var errOut, out strings.Builder
	e.ErrOut, e.Out, e.TTY = &errOut, &out, true
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "kanali", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetStartFailure(p.ID, "boot_failed", "the environment did not boot: the system it boots is missing from the machine's store (stage 1 found no stage 2 init); `repose logs --kind console` shows what it printed")
	err = ResizeClassCmd(ctx, e, "kanali", "xl", nil)
	if err == nil {
		t.Fatal("the resize reported success")
	}
	if strings.Contains(errOut.String(), "✓ Started") {
		t.Fatalf("a ✓ before the start failed:\n%s", errOut.String())
	}
	if !strings.Contains(err.Error(), "missing from the machine's store") {
		t.Fatalf("err = %v", err)
	}
}

// I-590: a start that ends running, but on the previous system, says so
// on stderr; stdout keeps the plain result.
func TestStartWithAWarningSaysIt(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var errOut, out strings.Builder
	e.ErrOut, e.Out, e.TTY = &errOut, &out, true
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "kanali", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "stopped")
	msg := "its new system did not boot, so it runs its previous one: the system it boots is missing from the machine's store (stage 1 found no stage 2 init); `repose logs --kind console` shows what the new one printed"
	fake.SetStartWarning(p.ID, "boot_failed", msg)
	if err := StartCmd(ctx, e, "kanali"); err != nil {
		t.Fatalf("StartCmd: %v", err)
	}
	if !strings.Contains(errOut.String(), "kanali: "+msg+".") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "✓") {
		t.Fatalf("a ✓ for a start that did not do what it says: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "kanali is running") {
		t.Fatalf("stdout = %q", out.String())
	}
	// ls and status carry the reason while the project runs on it.
	out.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "kanali: its new system did not boot") {
		t.Fatalf("ls = %q", out.String())
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var st strings.Builder
	writeStatusLines(&st, got, nil, nil, nil)
	if !strings.Contains(st.String(), "  its new system did not boot") {
		t.Fatalf("status = %q", st.String())
	}
}

// A start whose boot failed points at the console, which now holds that
// boot; any other failure does not.
func TestFailedStartPointsAtTheConsoleOnlyForABoot(t *testing.T) {
	if s := nextAfterFailedStart("kanali", "boot_failed"); !strings.Contains(s, "--kind console") {
		t.Fatalf("boot_failed: %q", s)
	}
	if s := nextAfterFailedStart("kanali", "internal"); strings.Contains(s, "console") {
		t.Fatalf("internal: %q", s)
	}
	le := "internal: the host failed"
	if s := notRunningMessage(&Project{Slug: "kanali", State: "error", LastError: &le}); strings.Contains(s, "console") {
		t.Fatalf("error state: %q", s)
	}
}

// `repose logs` with no project here says so and exits 4; with a project
// whose console is empty it says whose (it printed nothing and exited 0,
// 2026-10-07).
func TestLogsSaysWhatItFound(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var errOut, out strings.Builder
	e.ErrOut, e.Out = &errOut, &out
	ctx := context.Background()
	err := LogsCmd(ctx, e, "", "console", "", false, nil)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitProjectNotFound || !strings.Contains(ee.msg, "No repose project here") {
		t.Fatalf("no project: err = %v", err)
	}
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "kanali", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if err := LogsCmd(ctx, e, "kanali", "console", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" || !strings.Contains(errOut.String(), "kanali has no console output") {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
	// A failed boot's console prints line by line.
	errOut.Reset()
	fake.SetConsole(p.ID, "<<< NixOS Stage 1 >>>", "stage 2 init script (/mnt-root/nix/store/x/init) not found")
	if err := LogsCmd(ctx, e, "kanali", "console", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), " console stage 2 init script") || errOut.String() != "" {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
}
