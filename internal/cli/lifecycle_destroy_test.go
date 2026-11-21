package cli

import (
	"context"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// A destroy whose host is gone takes no final snapshot, so it must not
// promise one. ENG-119, DECISIONS I-1418, measured on the owner's own
// account on 2026-11-20: `repose rm rig1 --yes` printed "Destroying rig1.
// Its final snapshot is kept until 2026-12-20" twice, ten minutes apart,
// while `count(*) from snapshots where project_id = rig1` was 0 and had
// never been anything else. The Mac's appliance and its disk had been
// deleted five seconds after the first destroy, so the snapshot phase
// could not run and I-924 skips it on a host nobody expects back.
//
// The CLI cannot work this out: it prints the line the moment the api
// answers 202, before any phase has run. So the api says whether a final
// snapshot is coming and this reads it.
func TestDestroyDoesNotPromiseASnapshotItCannotTake(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "rig1", Class: "small", RemoteURL: "github.com/owner/rig1"})
	if err != nil {
		t.Fatal(err)
	}
	// The machine is on a Mac of the user's own, and the Mac is gone:
	// they removed the appliance, so nobody expects the host back.
	id := fake.AddOwnedHost("mac-rig2")
	fake.SetProjectHost(p.ID, id)
	fake.SetOwnedHostState("mac-rig2", "lost")

	if err := DestroyCmd(ctx, e, p.ID, true, false, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "final snapshot") {
		t.Errorf("the destroy promised a snapshot that cannot be taken, the host being gone: %q", got)
	}
	if !strings.Contains(got, "Destroying rig1") {
		t.Errorf("the destroy says nothing: %q", got)
	}
}
