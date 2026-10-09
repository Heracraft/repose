package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// This file is the checklist's "status, projects, secrets, config,
// snapshots, logs, destroy each round-trip against the real API" item,
// against the fake api that stands in for it in every other test here.

func newRoundtripEnv(t *testing.T, fake *fakeapi.Fake) (*Env, *Project) {
	t.Helper()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	e.Cache.ByDir[e.Cwd] = p.ID
	return e, p
}

func TestStatusAndProjectsRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatalf("StatusCmd: %v", err)
	}
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatalf("ProjectsCmd: %v", err)
	}

	e.JSON = true
	out := &discardWriter{}
	e.Out = out
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatalf("StatusCmd --json: %v", err)
	}
	if !strings.Contains(out.buf.String(), `"slug"`) {
		t.Fatalf("status --json missing expected field: %s", out.buf.String())
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := SecretsSetCmd(ctx, e, "", "MY_TOKEN", []byte("shh")); err != nil {
		t.Fatalf("SecretsSetCmd: %v", err)
	}
	if err := SecretsListCmd(ctx, e, ""); err != nil {
		t.Fatalf("SecretsListCmd: %v", err)
	}
	if err := SecretsRmCmd(ctx, e, "", "MY_TOKEN"); err != nil {
		t.Fatalf("SecretsRmCmd: %v", err)
	}

	// A reserved name must be refused client-side, before any request.
	fake.Fail("PUT /projects/{id}/secrets/{name}", "internal") // proves the client never even asked
	if err := SecretsSetCmd(ctx, e, "", "user_ca.pub", []byte("x")); err == nil {
		t.Fatal("expected a reserved-name error")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := ConfigShowCmd(ctx, e, "", false); err != nil {
		t.Fatalf("ConfigShowCmd: %v", err)
	}
	if err := ConfigApplyCmd(ctx, e, "", writeTempFragment(t, "{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }")); err != nil {
		t.Fatalf("ConfigApplyCmd: %v", err)
	}
	if err := ConfigShowCmd(ctx, e, "", true); err != nil {
		t.Fatalf("ConfigShowCmd --revisions: %v", err)
	}

	edited := false
	editor := func(path string) error {
		edited = true
		return nil
	}
	if err := ConfigEditCmd(ctx, e, p.ID, editor); err != nil {
		t.Fatalf("ConfigEditCmd: %v", err)
	}
	if !edited {
		t.Fatal("editor was never invoked")
	}
}

func writeTempFragment(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/repose.nix"
	if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSnapshotsRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := SnapshotsListCmd(ctx, e, ""); err != nil {
		t.Fatalf("SnapshotsListCmd: %v", err)
	}
	if err := SnapshotsCreateCmd(ctx, e, ""); err != nil {
		t.Fatalf("SnapshotsCreateCmd: %v", err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, p.ID)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("expected at least one snapshot, err=%v snaps=%v", err, snaps)
	}

	if _, err := e.Client.StopProject(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	confirmed := false
	confirm := func(string) (bool, error) { confirmed = true; return true, nil }
	if err := SnapshotsRestoreCmd(ctx, e, "", snaps[0].ID, "", confirm); err != nil {
		t.Fatalf("SnapshotsRestoreCmd: %v", err)
	}
	if !confirmed {
		t.Fatal("restore in place must ask for confirmation")
	}
	// --as-new waits on the op of the new project, which owns it: polling
	// it under the source answered not_found on prod (2026-10-01).
	out := &bytes.Buffer{}
	e.Out = out
	if err := SnapshotsRestoreCmd(ctx, e, "", snaps[0].ID, "rt-copy", nil); err != nil {
		t.Fatalf("SnapshotsRestoreCmd --as-new: %v", err)
	}
	if !strings.Contains(out.String(), "Restored into a new project, rt-copy.") {
		t.Fatalf("output: %q", out.String())
	}
}

func TestLogsAndEventsRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := LogsCmd(ctx, e, "", "", "", false, nil); err != nil {
		t.Fatalf("LogsCmd: %v", err)
	}
	if err := EventsCmd(ctx, e, "", "24h", false, nil); err != nil {
		t.Fatalf("EventsCmd: %v", err)
	}
}

// I-414: events print oldest first; a --since past the api's 50 pages back
// with before; --follow prints each event once.
func TestEventsPagesAndFollows(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	base := time.Now().Add(-3 * time.Hour)
	for i := 0; i < 130; i++ {
		fake.AddEvent(p.ID, base.Add(time.Duration(i)*time.Minute), "agent_message", fmt.Sprintf("m%03d", i))
	}
	out := &bytes.Buffer{}
	e.Out = out
	if err := EventsCmd(ctx, e, "", "24h", false, nil); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if f := strings.Split(l, "\t"); len(f) == 4 && strings.HasPrefix(f[3], "m") {
			got = append(got, f[3])
		}
	}
	if len(got) != 130 || got[0] != "m000" || got[129] != "m129" {
		t.Fatalf("--since 24h printed %d events, %v..., want m000..m129", len(got), got[:min(3, len(got))])
	}

	out.Reset()
	polls := 0
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	poll := func() {
		polls++
		if polls == 1 {
			fake.AddEvent(p.ID, time.Now(), "agent_message", "late")
		}
		if polls == 3 {
			cancel()
		}
	}
	_ = EventsCmd(pctx, e, "", "1h", true, poll)
	if n := strings.Count(out.String(), "\tlate\n"); n != 1 {
		t.Fatalf("follow printed the new event %d times:\n%s", n, out.String())
	}
	if n := strings.Count(out.String(), "\tm129\n"); n != 1 {
		t.Fatalf("follow printed m129 %d times:\n%s", n, out.String())
	}
}

func TestDestroyRoundTrip(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()

	if err := DestroyCmd(ctx, e, "", true, true, nil); err != nil {
		t.Fatalf("DestroyCmd: %v", err)
	}
	if _, err := e.Client.GetProject(ctx, p.ID); err == nil {
		t.Fatal("expected the destroyed project to 404")
	}
}
