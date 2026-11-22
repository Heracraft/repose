package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// P0 from the Mac review (I-1544): a machine's snapshots on the account's
// own host live on that host's disk. `repose rm` says so instead of the
// 30-day promise, `host rm` asks with the count, and afterwards `ls
// --destroyed` and `snapshots list` say they went with the host and a
// restore is refused with the api's sentence, creating nothing.
func TestOwnHostSnapshotsGoWithTheHost(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}
	hostID := fake.AddOwnedHost("mac-mini")
	fake.SetProjectHost(p.ID, hostID)

	if err := DestroyCmd(ctx, e, p.Slug, true, true, nil); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, "Its final snapshot is kept on your host mac-mini; removing the host deletes it.") || strings.Contains(got, "kept until") {
		t.Fatalf("rm said:\n%s", got)
	}

	var asked []string
	confirm := func(q string) (bool, error) { asked = append(asked, q); return true, nil }
	if err := HostRmCmd(ctx, e, "mac-mini", false, confirm); err != nil {
		t.Fatalf("host rm: %v", err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "This deletes the 1 snapshot kept on it; repose has no copy.") {
		t.Fatalf("host rm asked %q", asked)
	}

	out.buf.Reset()
	if err := DestroyedCmd(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "gone with host mac-mini") {
		t.Fatalf("ls --destroyed:\n%s", got)
	}
	out.buf.Reset()
	if err := snapshotsListAll(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "gone with host mac-mini") {
		t.Fatalf("snapshots list:\n%s", got)
	}

	before, _ := e.Client.ListProjects(ctx)
	err := RestoreCmd(ctx, e, p.Slug, "back", "", nil)
	var errOut discardWriter
	code := exitCodeFor(err, &errOut)
	if code != ExitGeneric || !strings.Contains(errOut.buf.String(), "That snapshot was kept on your host mac-mini, which was removed, so it is gone; repose had no copy") {
		t.Fatalf("restore: exit %d %q", code, errOut.buf.String())
	}
	if after, _ := e.Client.ListProjects(ctx); len(after) != len(before) {
		t.Fatalf("a refused restore left %d projects, was %d", len(after), len(before))
	}
}

// The rm question says the same for a machine on the account's own host.
func TestDestroyPromptOnOwnHost(t *testing.T) {
	got := destroyPrompt([]*Project{{Slug: "app", HostName: "mac-mini"}})
	if got != "Destroy app? Its final snapshot is kept on your host mac-mini; removing the host deletes it. [y/N] " {
		t.Fatalf("%q", got)
	}
}
