package cli

import (
	"context"
	"strings"
	"testing"
)

// `repose snapshots list` with no PROJECT, from a folder that is no
// project, lists every snapshot of the account rather than refusing
// (I-904). The owner ran `repose snapshots`, `snapshots ls` and
// `snapshots list` from their home folder and got the same "name one"
// three times, for a question the api can answer.
func TestSnapshotsListFromTheHomeFolderListsThemAll(t *testing.T) {
	f, home := homeFixture(t)
	ctx := context.Background()
	e, out, _ := freshEnv(f.env, home)

	// Nothing yet: it says so, and does not ask for a project.
	if err := SnapshotsListCmd(ctx, e, ""); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, "You have no snapshots yet.") {
		t.Fatalf("empty output %q", got)
	}

	// Two projects, each with a snapshot.
	for _, name := range []string{"api", "todo-app"} {
		p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: name, Class: "large"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Client.CreateSnapshot(ctx, p.ID); err != nil {
			t.Fatalf("snapshot of %s: %v", name, err)
		}
	}
	e, out, _ = freshEnv(f.env, home)
	if err := SnapshotsListCmd(ctx, e, ""); err != nil {
		t.Fatalf("list all: %v", err)
	}
	got := out.buf.String()
	for _, want := range []string{"PROJECT", "api", "todo-app"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "Your home folder is not a project") {
		t.Fatalf("still refused: %q", got)
	}

	// Naming a project still narrows it, and that table has no PROJECT
	// column: the account-wide list is the only one that needs it.
	e, out, _ = freshEnv(f.env, home)
	if err := SnapshotsListCmd(ctx, e, "api"); err != nil {
		t.Fatalf("list api: %v", err)
	}
	got = out.buf.String()
	if strings.Contains(got, "PROJECT") || strings.Contains(got, "todo-app") {
		t.Fatalf("`snapshots list api` listed more than api: %q", got)
	}
}
