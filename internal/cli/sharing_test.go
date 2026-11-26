package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// A machine on a host of the account's own has no size (DECISIONS
// I-1630): `repose ls` and `repose status` show what it uses instead of
// a class, and a fleet-only listing keeps SIZE and the class (I-1632).
func TestOwnHostMachineShowsUseNotSize(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetState(p.ID, "running")

	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := squash(out.buf.String()); !strings.Contains(got, "PROJECT SIZE STATE") || !strings.Contains(got, "todo-app * large running") {
		t.Fatalf("fleet listing:\n%s", got)
	}

	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := squash(out.buf.String())
	if !strings.Contains(got, "PROJECT MEMORY STATE") || !strings.Contains(got, "todo-app * 1.4 GB used running") || strings.Contains(got, "large") {
		t.Fatalf("own host listing:\n%s", got)
	}

	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(out.buf.String(), "\n")
	if !strings.HasPrefix(first, "todo-app  running") || !strings.HasSuffix(first, "  1.4 GB used") || strings.Contains(first, "large") {
		t.Fatalf("status first line %q", first)
	}

	// Stopped, it uses nothing, and the cell is a dash.
	fake.SetState(p.ID, "stopped")
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := squash(out.buf.String()); !strings.Contains(got, "todo-app * - stopped") {
		t.Fatalf("stopped own host listing:\n%s", got)
	}
}

// --size on a machine of a host of the account's own is accepted for one
// release and ignored, with one line on stderr (I-1632): nothing is
// patched and nothing is stopped.
func TestSizeIsIgnoredOnAnOwnHost(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut
	fake.SetState(p.ID, "running")
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))

	if err := ResizeClassCmd(ctx, e, "todo-app", "xl", nil); err != nil {
		t.Fatalf("resize --size on an own host: %v", err)
	}
	if got, want := errOut.buf.String(), "--size is ignored: todo-app runs on your host mac-mini, where a machine has no size and uses the memory it needs.\n"; got != want {
		t.Fatalf("stderr %q, want %q", got, want)
	}
	if out.buf.Len() != 0 {
		t.Fatalf("stdout %q, want nothing", out.buf.String())
	}
	got, err := e.Client.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != "large" || got.State != "running" {
		t.Fatalf("after an ignored --size: class %s state %s, want large running", got.Class, got.State)
	}
}

// A machine the kernel stopped because a host of the account's own ran
// out of memory says so in `repose ls`, `repose status` and when a
// command needs it running (I-1632).
func TestOOMStopIsShown(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	fake.SetState(p.ID, "stopped")
	msg := "stopped: your host mac-mini ran out of memory, and the kernel stopped todo-app to keep the others running. `repose start todo-app` starts it again"
	fake.SetLastError(p.ID, "oom_stopped: "+msg)

	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: "+msg+".\n") {
		t.Fatalf("repose ls:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "  "+msg+".\n") {
		t.Fatalf("repose status:\n%s", got)
	}
	pr, err := e.Client.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := notRunningMessage(pr), "todo-app is "+msg+"."; got != want {
		t.Fatalf("not running %q, want %q", got, want)
	}
}

// The created line names no class for a machine on a host of the
// account's own (I-1632), and keeps it on repose's servers.
func TestCreatedLabelOnAnOwnHost(t *testing.T) {
	no := false
	if got := createdLabel(&Project{Slug: "api", HostName: "mac-mini", Sized: &no}, "large"); got != "Created api on mac-mini" {
		t.Fatalf("own host: %q", got)
	}
	if got := createdLabel(&Project{Slug: "api"}, "large"); got != "Created api (large)" {
		t.Fatalf("fleet: %q", got)
	}
}

// squash is s with every run of spaces made one, so a table is read by
// its cells and not by the widths tabwriter gave them.
func squash(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, "\n")
}
