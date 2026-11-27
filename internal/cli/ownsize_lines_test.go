package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// d2-lab 2026-11-27 (design2-shipped §10): `snapshots restore --as` on a
// Mac ended "it is running (large)." while own-hardware.md says no line
// about a machine on your own hardware names a size (I-1632). The line
// names the class only for a machine that has one, as every other
// one-line report does (classNote).
func TestRestoredAsLineNamesNoSizeOnOwnHardware(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.AddOwnedHost("mac-mini")
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-old", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if !unsized(p) {
		t.Fatalf("the fake placed %s sized; the test needs a machine with no size", p.Slug)
	}
	line := restoredAsLine(ctx, e, p, p.ID, p.Slug, "", time.Second)
	if strings.Contains(line, "(large)") || strings.Contains(line, "(") {
		t.Errorf("restore --as on own hardware names a size: %q", line)
	}
	if !strings.HasSuffix(line, ".") || !strings.Contains(line, "it is ") {
		t.Errorf("restore --as line %q", line)
	}
}

// d2-lab 2026-11-27 (design2-shipped §3): `repose stop` printed "Note: a
// machine on this host gets a bigger disk while it is stopped..." , the
// last_error an earlier refused resize left, on a stop that had nothing
// to do with it. The note is for what this stop did (I-158): a
// last_error the stop wrote, or a snapshot that failed.
func TestStopNoteIsThisStopsOwn(t *testing.T) {
	s := func(v string) *string { return &v }
	resize := s("invalid_argument: a machine on this host gets a bigger disk while it is stopped: stop it, resize, and start it again")
	snap := s("snapshot_failed: the host's snapshot space is full")
	for _, c := range []struct {
		name          string
		before, after *string
		want          bool
	}{
		{"an earlier refusal, left as it was", resize, resize, false},
		{"nothing before or after", nil, nil, false},
		{"this stop's snapshot failed", nil, snap, true},
		{"the same snapshot failure again", snap, snap, true},
		{"the stop replaced an older error", resize, snap, true},
	} {
		got := stopNote(&Project{LastError: c.before}, &Project{LastError: c.after})
		if (got != "") != c.want {
			t.Errorf("%s: note %q, want one: %v", c.name, got, c.want)
		}
	}
}

// d2-lab 2026-11-27 (design2-shipped §1): `repose host rm` right after
// `repose rm` of the host's last machine was refused ("still has e2e-li1
// on it") for the ~8 s the destroy took. A machine already being
// destroyed is not in the way: the removal waits for it, says so once,
// and then goes.
func TestHostRmWaitsForADestroyUnderWay(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{DestroyDelay: 1500 * time.Millisecond})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	var errOut bytes.Buffer
	e.Out, e.ErrOut = &discardWriter{}, &errOut
	id := fake.AddOwnedHost("mac-mini")
	fake.SetProjectHost(p.ID, id)
	if _, err := e.Client.DestroyProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := HostRmCmd(ctx, e, "mac-mini", true, nil); err != nil {
		t.Fatalf("host rm during the last machine's destroy: %v", err)
	}
	if !strings.Contains(errOut.String(), "Waiting for todo-app to be destroyed") {
		t.Errorf("no progress line: %q", errOut.String())
	}
	hs, _ := e.Client.ListHosts(ctx)
	for _, h := range hs {
		if h.Name == "mac-mini" && h.State != "retired" {
			t.Errorf("mac-mini is still %s", h.State)
		}
	}
}

// review-cli 2026-11-27 P3: `repose restore` of a destroyed Mac machine
// ended "(large)", as `snapshots restore --as` did (#240).
func TestRestoreLineNamesNoSizeOnOwnHardware(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.AddOwnedHost("mac-mini")
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "macbox", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RestoreCmd(ctx, e, "macbox", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "(large)") || !strings.HasPrefix(out.String(), "Restored macbox") {
		t.Errorf("restore on own hardware said %q", out.String())
	}
}

// review-cli 2026-11-27 P3: `repose start --json` said "ready in 0.0s"
// after 4-5 s: with --json there is no progress line, and its clock was
// the one the time came from.
func TestStartJSONSaysTheRealTime(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StartDelay: 400 * time.Millisecond})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "timed", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	e.JSON = true
	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ready in 0.0s") || !strings.Contains(out.String(), "ready in") {
		t.Errorf("start --json said %q", out.String())
	}
}

// review-cli 2026-11-27 R3: a restore said "it is running" up to 8 s
// before the machine answered. It now says so once the machine answers,
// and says it has not when it does not within the wait.
func TestRestoreSaysRunningOnceItAnswers(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "slow", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	probes := 0
	e.readGuest = func(context.Context, string, []string, time.Duration) guestStatus {
		probes++
		if probes < 3 {
			return guestStatus{probeErr: "ssh_timeout"}
		}
		return guestStatus{mux: "tmux"}
	}
	out.Reset()
	if err := RestoreCmd(ctx, e, "slow", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if probes < 3 || !strings.Contains(out.String(), "; it is running") || strings.Contains(out.String(), "not answered") {
		t.Errorf("after %d probes, restore said %q", probes, out.String())
	}
	restoreAnswerWait = 200 * time.Millisecond
	defer func() { restoreAnswerWait = 30 * time.Second }()
	e.readGuest = func(context.Context, string, []string, time.Duration) guestStatus {
		return guestStatus{probeErr: "ssh_timeout"}
	}
	if err := DestroyCmd(ctx, e, "slow", true, false, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RestoreCmd(ctx, e, "slow", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "it is running and has not answered yet") {
		t.Errorf("a machine that never answers: %q", out.String())
	}
}

// review-cli 2026-11-27 P3: a refusal capitalised the machine's name,
// "E2e-rc-f2 has no usable volume".
func TestRefusalKeepsAMachineNameAsWritten(t *testing.T) {
	got := apiRefusal(&APIError{Code: "conflict", Message: "e2e-rc-f2 has no usable volume because the restore into it did not finish"})
	if !strings.HasPrefix(got, "e2e-rc-f2 has no usable volume") {
		t.Errorf("refusal %q", got)
	}
	if got := apiRefusal(&APIError{Code: "invalid", Message: "the name is too long"}); !strings.HasPrefix(got, "The name") {
		t.Errorf("a sentence lost its capital: %q", got)
	}
}

// review-cli 2026-11-27 P3: `snapshots ls` showed TAKEN to the minute,
// and seven snapshots of one minute read alike.
func TestSnapshotsListTellsSnapshotsOfOneMinuteApart(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "snappy", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.CreateSnapshot(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotsListCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d`).MatchString(out.String()) {
		t.Errorf("TAKEN has no seconds:\n%s", out.String())
	}
}

// The owner's own run (2026-11-27): "Could not start muqsit-labs: the
// machines running on this host hold the memory it gives its machines;
// stopping a running machine frees room (insufficient_capacity)." With the
// api's new sentence the CLI says what happened on one line and the one
// action on the next, with no code (I-1724).
func TestOpFailedIsTwoPlainLines(t *testing.T) {
	err := opFailed("start", "muqsit-labs", OpError{Code: "insufficient_capacity",
		Message: "your Mac e2e-d2lab is full. Stop api there, or run it on repose's servers: repose run --host repose"}, nextAfterFailedStart("muqsit-labs", "insufficient_capacity"))
	var buf bytes.Buffer
	if code := exitCodeFor(err, &buf); code != ExitCapacity {
		t.Errorf("exit %d", code)
	}
	want := "Could not start muqsit-labs: your Mac e2e-d2lab is full.\nStop api there, or run it on repose's servers: repose run --host repose\n"
	if buf.String() != want {
		t.Errorf("printed %q\nwant    %q", buf.String(), want)
	}
	if strings.Contains(buf.String(), "insufficient_capacity") || strings.Contains(buf.String(), "(") {
		t.Errorf("a code reached the user: %q", buf.String())
	}
}
