package cli

import (
	"bytes"
	"context"
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
