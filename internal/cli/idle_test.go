package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// DECISIONS I-262: an idle project says so, with how long and the stop
// command (no rate since I-289: a plan buys memory, not hours), on
// `repose status` and under `repose ls`; a project that is not idle, or
// not running, says nothing.
func TestIdleLineOnStatusAndProjects(t *testing.T) {
	since := time.Now().Add(-26*time.Hour - 10*time.Minute)
	p := &Project{Slug: "todo-app", Class: "large", State: "running", Idle: &ProjectIdle{Since: since, HourlyCents: 14}}
	want := "unused for 26h"
	var b strings.Builder
	writeStatusLines(&b, p, nil, nil, nil)
	if !strings.Contains(b.String(), "\n  "+want+"\n") {
		t.Fatalf("status: %s", b.String())
	}
	b.Reset()
	writeProjectsTable(&b, []Project{*p, {Slug: "busy", Class: "small", State: "running"}})
	if !strings.Contains(b.String(), "todo-app: "+want+"\n") || strings.Contains(b.String(), "busy: idle") {
		t.Fatalf("projects: %s", b.String())
	}
	stopped := *p
	stopped.State = "stopped"
	if idleLine(&stopped, time.Now()) != "" {
		t.Fatal("a stopped project shown as idle")
	}
	// An agent waiting for an answer is why it is unused (I-617).
	waiting := *p
	waiting.Signals = &Signals{Agents: []AgentSignal{{Agent: "claude", Window: "claude-2", State: "needs_input"}, {Agent: "codex", State: "idle"}}}
	if got := idleLine(&waiting, time.Now()); got != "unused for 26h; claude-2 needs input" {
		t.Fatalf("waiting agent: %q", got)
	}
	waiting.Signals.Agents[1].State = "needs_input"
	if got := idleLine(&waiting, time.Now()); got != "unused for 26h; claude-2, codex need input" {
		t.Fatalf("two waiting agents: %q", got)
	}
	if got := idleFor(75 * time.Hour); got != "3d" {
		t.Fatalf("idleFor(75h) = %q", got)
	}
}

// The run/attach note names other idle projects once per idle stretch:
// not the one being attached, not again on the next command, and again
// once the project was used and went idle anew.
func TestIdleOthersNoteOncePerStretch(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s1 := now.Add(-30 * time.Hour)
	ps := []Project{
		{ID: "a", Slug: "api-v2", State: "running", Idle: &ProjectIdle{Since: s1, HourlyCents: 28}},
		{ID: "b", Slug: "here", State: "running", Idle: &ProjectIdle{Since: s1, HourlyCents: 14}},
		{ID: "c", Slug: "busy", State: "running"},
	}
	got := idleOthersNote(dir, ps, "b", now)
	if got != "Running and unused: api-v2 (30h)." {
		t.Fatalf("first note: %q", got)
	}
	if got := idleOthersNote(dir, ps, "b", now); got != "" {
		t.Fatalf("second note: %q", got)
	}
	// api-v2 was used, then idle again from a later time: a new stretch.
	ps[0].Idle = &ProjectIdle{Since: now.Add(-25 * time.Hour), HourlyCents: 28}
	if got := idleOthersNote(dir, ps, "b", now); !strings.Contains(got, "api-v2 (25h") {
		t.Fatalf("new stretch not noted: %q", got)
	}
	// Attaching api-v2 itself drops it, so its next stretch is new too.
	if got := idleOthersNote(dir, ps, "a", now); !strings.Contains(got, "here (30h") {
		t.Fatalf("here not noted: %q", got)
	}
	if got := idleOthersNote(dir, ps, "b", now); !strings.Contains(got, "api-v2") {
		t.Fatalf("api-v2 not noted after being attached: %q", got)
	}
}

// startIdleNote reads the list from the api and prints the note on stderr.
func TestStartIdleNoteReadsTheAPI(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	idleP, err := fake.CreateProject("api-v2", "xl")
	if err != nil {
		t.Fatal(err)
	}
	here, err := fake.CreateProject("here", "large")
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(idleP.ID, "running")
	since := time.Now().Add(-49 * time.Hour)
	fake.SetIdle(idleP.ID, &since, 28)
	errOut := &discardWriter{}
	e := &Env{Dir: t.TempDir(), Client: newClient(fake.URL()+"/v1", staticToken("tok")), Out: &discardWriter{}, ErrOut: errOut}
	note := startIdleNote(context.Background(), e)
	time.Sleep(50 * time.Millisecond) // the list is asked for at the start of the command
	note(here.ID)
	if got := errOut.buf.String(); got != "Running and unused: api-v2 (2d).\n" {
		t.Fatalf("note: %q", got)
	}
}

// `repose run` prints the note once connected (the wiring in runRun).
func TestRunMentionsOtherIdleProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	other, err := fake.CreateProject("forgotten", "large")
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(other.ID, "running")
	since := time.Now().Add(-27 * time.Hour)
	fake.SetIdle(other.ID, &since, 14)
	f := newRunFixture(t, fake)
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("runRun: %v", err)
	}
	if got := f.env.ErrOut.(*discardWriter).buf.String(); !strings.Contains(got, "Running and unused: forgotten (27h).") {
		t.Fatalf("stderr: %s", got)
	}
}
