package cli

import (
	"context"
	"strings"
	"testing"
	"time"

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
	msg := "stopped: your host mac-mini ran out of memory, so repose stopped todo-app, the machine started last, to keep the others running. `repose start todo-app` starts it again"
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

// A running machine whose guestd has not answered for about three
// minutes says so (DECISIONS I-1652): the STATE cell of `repose ls` and
// the `status` header carry `not answering`, a line under each names how
// long and the one command that brings it back, with what it costs.
func TestNotAnsweringIsShown(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetState(p.ID, "running")
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	since := time.Now().Add(-4*time.Minute - 10*time.Second)
	fake.SetNotAnswering(p.ID, &since)
	line := "todo-app: not answering for 4m. `repose start todo-app` restarts it; what was in its memory is lost.\n"

	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	if !strings.Contains(squash(got), "todo-app * - running not answering") || !strings.Contains(got, line) {
		t.Fatalf("repose ls:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	got = out.buf.String()
	first, _, _ := strings.Cut(got, "\n")
	if !strings.Contains(first, "running") || !strings.Contains(first, "  not answering") {
		t.Fatalf("status first line %q", first)
	}
	if !strings.Contains(got, "  "+strings.TrimPrefix(line, "todo-app: ")) || strings.Contains(got, "repose's service on the machine is not answering") {
		t.Fatalf("repose status:\n%s", got)
	}
}

// No line names a size for a machine on a host of the account's own
// (DECISIONS I-1654, review-cli F-C): the start, the restore, the
// connect line and `ls --destroyed`. A machine on repose's servers keeps
// its class in each.
func TestOwnedLinesNameNoSize(t *testing.T) {
	no := false
	own := &Project{Slug: "api", Class: "large", Sized: &no}
	fleet := &Project{Slug: "web", Class: "small"}
	if got := classNote(own); got != "" {
		t.Fatalf("own host class note %q, want none", got)
	}
	if got := classNote(fleet); got != " (small)" {
		t.Fatalf("fleet class note %q", got)
	}

	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	fake.SetState(p.ID, "stopped")
	if err := StartCmd(ctx, e, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app is running, ready in") || strings.Contains(got, "large") {
		t.Fatalf("start on an own host: %q", got)
	}

	var buf strings.Builder
	writeDestroyedTable(&buf, []DestroyedProject{
		{Slug: "api", Class: "large", Sized: &no, DestroyedAt: time.Now(), NameFree: true},
		{Slug: "web", Class: "small", DestroyedAt: time.Now(), NameFree: true},
	})
	got := squash(buf.String())
	if !strings.Contains(got, "api - ") || !strings.Contains(got, "web small ") || strings.Contains(got, "large") {
		t.Fatalf("ls --destroyed:\n%s", got)
	}
}

// A figure that rounds to 1000 of a unit is said in the next one
// (review-cli N4/N8): `1005.6 MB used` beside `1.6 GB used`.
func TestHumanBytesRollsOverAtAThousand(t *testing.T) {
	for n, want := range map[int64]string{
		1054448435: "1.0 GB", // 1005.6 MiB
		800 << 20:  "800.0 MB",
		1717986918: "1.6 GB",
		999 << 10:  "999.0 KB",
	} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// Events in one second print in the order they were written (review-cli
// N5): the api lists them newest first, and `machine stopped` came out
// above `machine stopping`.
func TestEventsInOneSecondKeepTheirOrder(t *testing.T) {
	ts := time.Date(2026, 11, 25, 22, 57, 57, 0, time.UTC)
	evs := []Event{
		{ID: "01a2160b-0000-7000-8000-000000000003", TS: ts, Summary: "stopped"},
		{ID: "01a2160b-0000-7000-8000-000000000002", TS: ts, Summary: "stopping"},
		{ID: "01a2160b-0000-7000-8000-000000000001", TS: ts.Add(-time.Second), Summary: "snapshot"},
	}
	sortEvents(evs)
	if evs[0].Summary != "snapshot" || evs[1].Summary != "stopping" || evs[2].Summary != "stopped" {
		t.Fatalf("order %s, %s, %s; want snapshot, stopping, stopped", evs[0].Summary, evs[1].Summary, evs[2].Summary)
	}
}

// A machine parked on a Mac waiting for its user's login (DECISIONS
// I-1672): `repose ls` and `status` say `running parked` with one line
// naming the host and that its memory is kept; never "not answering",
// never a hint that a start loses memory; `repose start` says the same and
// restarts nothing.
func TestParkedIsShownAndStartRestartsNothing(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetState(p.ID, "running")
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	since := time.Now().Add(-20 * time.Minute)
	fake.SetParked(p.ID, &since)
	line := "todo-app: parked; it waits for you to log in to mac-mini, and its memory is kept.\n"

	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	if !strings.Contains(squash(got), "running parked") || !strings.Contains(got, line) || strings.Contains(got, "not answering") || strings.Contains(got, "memory is lost") {
		t.Fatalf("repose ls:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	got = out.buf.String()
	if first, _, _ := strings.Cut(got, "\n"); !strings.Contains(first, "  parked") {
		t.Fatalf("status first line %q", first)
	}
	if !strings.Contains(got, "  "+strings.TrimPrefix(line, "todo-app: ")) || strings.Contains(got, "not answering") || strings.Contains(got, "restarts it") {
		t.Fatalf("repose status:\n%s", got)
	}
	out.buf.Reset()
	if err := StartCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "todo-app is "+strings.TrimPrefix(line, "todo-app: ") {
		t.Fatalf("repose start: %q", got)
	}
}

// ENG-264 (DECISIONS I-1680): a machine started smaller than it gets with
// its host to itself says so, once, in `repose start`, `repose ls` and
// `repose status`; a machine at its full size says nothing more.
func TestSizedDownStartIsSaid(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	fake.SetState(p.ID, "stopped")
	fake.SetVM(p.ID, &fakeapi.VM{VCPUs: 2, MemBytes: 3968 << 20, FullVCPUs: 5, FullMemBytes: 8 << 30})
	line := "runs with 2 vCPUs and 3.9 GB of its 5 and 8.0 GB: the machines running beside it held the rest when it started. A start after one of them stops gives it all.\n"

	if err := StartCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasSuffix(got, "It "+line) || strings.Count(got, "runs with") != 1 {
		t.Fatalf("repose start:\n%s", got)
	}
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: "+line) {
		t.Fatalf("repose ls:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "  "+line) {
		t.Fatalf("repose status:\n%s", got)
	}

	fake.SetVM(p.ID, &fakeapi.VM{VCPUs: 5, MemBytes: 8 << 30, FullVCPUs: 5, FullMemBytes: 8 << 30})
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); strings.Contains(got, "runs with") {
		t.Fatalf("repose ls at the full size:\n%s", got)
	}
}

// DECISIONS I-1682: a machine on a Mac whose memory is in a save file the
// locked Mac cannot read says what it waits for: resting, it wakes once
// the Mac is unlocked; parked, it waits for the unlock, not a login, when
// the user is logged in with the screen locked.
func TestLockedMacSaysWhatTheMachineWaitsFor(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	fake.SetState(p.ID, "running")
	fake.SetProjectHost(p.ID, fake.AddOwnedHost("mac-mini"))
	since := time.Now().Add(-20 * time.Minute)
	if err := fake.SetResting(p.ID, &since); err != nil {
		t.Fatal(err)
	}
	fake.SetWaitsFor(p.ID, "unlock")
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: resting; it wakes once you unlock mac-mini, and its memory is kept.\n") {
		t.Fatalf("repose ls:\n%s", got)
	}
	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "  resting; it wakes once you unlock mac-mini") {
		t.Fatalf("repose status:\n%s", got)
	}
	_ = fake.SetResting(p.ID, nil)
	fake.SetParked(p.ID, &since)
	fake.SetWaitsFor(p.ID, "unlock")
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: parked; it waits for you to unlock mac-mini, and its memory is kept.\n") || strings.Contains(got, "log in") {
		t.Fatalf("repose ls parked:\n%s", got)
	}
}

// DECISIONS I-1712: on a Mac MEMORY is what macOS gives up for a machine
// (its VM's footprint), 0 while it rests as a save, not what the guest
// uses.
func TestMacMemoryCellIsWhatTheMacHolds(t *testing.T) {
	p := &Project{State: "running", MemUsedBytes: 3864 << 20, VM: &ProjectVM{VCPUs: 5, MemBytes: 8 << 30, FullVCPUs: 5, FullMemBytes: 8 << 30}}
	if got := usedCell(p); got != "3.8 GB held" {
		t.Fatalf("cell %q", got)
	}
	p.MemUsedBytes = 0
	if got := usedCell(p); got != "0 B held" {
		t.Fatalf("saved cell %q", got)
	}
	p.VM = nil
	p.MemUsedBytes = 1400 << 20
	if got := usedCell(p); got != "1.4 GB used" {
		t.Fatalf("x86 cell %q", got)
	}
}
