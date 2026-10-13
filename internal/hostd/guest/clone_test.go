package guest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
)

const (
	forkPoint = "0192f0b0aaaa70008000000000000001"
	gidF1     = "0192f0b1-1111-7000-8000-0000000000f1"
	gidF2     = "0192f0b2-2222-7000-8000-0000000000f2"
	gidF3     = "0192f0b3-3333-7000-8000-0000000000f3"
)

func cloneReq(h *harness, gid string, siblings ...string) *hostdv1.Restore {
	return &hostdv1.Restore{
		ProjectId: "proj-" + gid[:8], GuestId: gid, Class: "large", VolumeBytes: 40 << 30, SystemClosure: h.closure,
		UserId: "user-1", ProjectSlug: "todo-app-fork-1", HostKey: []byte("hk"),
		Clone: &hostdv1.RestoreClone{SourceGuestId: gid1, PointId: forkPoint, SiblingGuestIds: siblings},
	}
}

// A same-host fork copies the source's volume as thin clones of one fork
// point, with no blob written or read: the source is frozen once for all
// the copies, every copy holds the source as it was at that point, and
// the point goes once every copy is restored (DECISIONS I-701).
func TestForkClonesOnePointOnTheHost(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.lvm.SetData("g-"+gid1, []byte("the tenant's filesystem"))
	blobs := len(h.blob.Blobs)
	downloads := 0
	h.blob.BeforeDownload = func(string) { downloads++ }

	res := h.mustOK(cmd(cloneReq(h, gidF1, gidF1, gidF2)))
	if res.GetCreate().GuestIp == "" {
		t.Fatalf("clone result %v", res.GetCreate())
	}
	// The source writes on after the point; the second copy still gets
	// the point, not the source as it is now.
	h.lvm.SetData("g-"+gid1, []byte("written after the fork"))
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; !ok {
		t.Fatal("the fork point went before every copy was restored")
	}
	h.mustOK(cmd(cloneReq(h, gidF2, gidF1, gidF2)))
	for _, id := range []string{gidF1, gidF2} {
		if got := string(h.lvm.GetData("g-" + id)); got != "the tenant's filesystem" {
			t.Fatalf("copy %s holds %q, want the source at the fork point", id, got)
		}
		if g := h.guest(id); g.State != StateStopped {
			t.Fatalf("copy %s is %s, want stopped", id, g.State)
		}
	}
	if n := strings.Count(strings.Join(h.guestd(gid1).Kinds(), ","), "Freeze"); n != 1 {
		t.Fatalf("source frozen %d times for one fork, want once", n)
	}
	if h.guestd(gid1).Frozen() {
		t.Fatal("source left frozen")
	}
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; ok {
		t.Fatal("the fork point outlived the fork")
	}
	if len(h.blob.Blobs) != blobs || downloads != 0 {
		t.Fatalf("a same-host fork went through the store: %d blobs written, %d downloads", len(h.blob.Blobs)-blobs, downloads)
	}
	if strings.Contains(strings.Join(h.lvm.Ops, ","), "fsck") {
		t.Fatal("a clone was checked as if its bytes came through the store")
	}
	// The copy starts like any restored guest.
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gidF1}))
	if !strings.Contains(h.logs.String(), `"source":"clone"`) {
		t.Fatal("restore_done does not say the volume was cloned")
	}
}

// A Restore sent again for a copy that finished answers as before, even
// when its source has gone since.
func TestForkCloneResentAfterSourceGone(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	first := h.mustOK(cmd(cloneReq(h, gidF1, gidF1)))
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	again := h.mustOK(cmd(cloneReq(h, gidF1, gidF1)))
	if again.GetCreate().GuestIp != first.GetCreate().GuestIp {
		t.Fatalf("resent clone answered %v, first %v", again.GetCreate(), first.GetCreate())
	}
}

func TestForkCloneRefusals(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	bad := cloneReq(h, gidF1, gidF1)
	bad.Clone.PointId = "xyz"
	h.mustFail(cmd(bad), CodeInvalidArgument)
	bad = cloneReq(h, gidF1, gidF2)
	h.mustFail(cmd(bad), CodeInvalidArgument) // siblings must name the copy
	bad = cloneReq(h, gidF1, gidF1)
	bad.Clone.SourceGuestId = "0192f0c9-9999-7000-8000-000000000009"
	h.mustFail(cmd(bad), CodeNotFound)
	bad = cloneReq(h, gidF1, gidF1)
	bad.BlobPath = "user-1/p/x.img.zst"
	h.mustFail(cmd(bad), CodeInvalidArgument)
	bad = cloneReq(h, gidF1, gidF1)
	bad.VolumeBytes = 20 << 30
	h.mustFail(cmd(bad), CodeInvalidArgument)
	for _, id := range []string{gidF1} {
		if _, err := h.st.GetGuest(id); err == nil {
			t.Fatalf("a refused clone recorded guest %s", id)
		}
	}
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; ok {
		t.Fatal("a refused clone took a fork point")
	}
}

// A copy larger than its source (a resize between) gets its volume grown
// after the clone.
func TestForkCloneGrowsToTheVolumeAsked(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	req := cloneReq(h, gidF1, gidF1)
	req.VolumeBytes = 80 << 30
	h.mustOK(cmd(req))
	if v := h.lvm.Volumes["g-"+gidF1]; v == nil || v.Size != 80<<30 {
		t.Fatalf("copy volume %+v, want 80 GiB", v)
	}
}

// A clone that fails leaves no volume, puts the copy in error, and lets
// the point go when it was the last copy.
func TestForkCloneFailureLeavesNoVolume(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.lvm.FailOn["clone"] = errString("lvcreate: out of metadata")
	h.mustFail(cmd(cloneReq(h, gidF1, gidF1)), CodeInternal)
	delete(h.lvm.FailOn, "clone")
	if _, ok := h.lvm.Volumes["g-"+gidF1]; ok {
		t.Fatal("a failed clone left a volume")
	}
	if g := h.guest(gidF1); g.State != StateError {
		t.Fatalf("failed clone state %s", g.State)
	}
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; ok {
		t.Fatal("the fork point outlived its only copy")
	}
	// And a retry of the same copy takes the point again and succeeds.
	h.mustOK(cmd(cloneReq(h, gidF1, gidF1)))
}

// A fork point whose siblings never all arrive is removed after
// ForkPointTTL, and one left by a hostd that died is swept at start.
func TestForkPointExpiresAndIsSweptAtStart(t *testing.T) {
	old := ForkPointTTL
	ForkPointTTL = 50 * time.Millisecond
	defer func() { ForkPointTTL = old }()
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(cloneReq(h, gidF1, gidF1, gidF2)))
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; !ok {
		t.Fatal("the point went while a sibling had not arrived")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ok, _ := h.lvm.VolumeExists(context.Background(), ForkPointName(forkPoint))
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fork point outlived ForkPointTTL")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.lvm.Volumes["fp-0192f0b0bbbb70008000000000000002"] = &lvm.FakeVolume{}
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.lvm.Volumes["fp-0192f0b0bbbb70008000000000000002"]; ok {
		t.Fatal("a fork point left by an earlier hostd survived reconcile")
	}
}

// Ten copies of one fork at once, on a host whose op slots they outnumber,
// all finish: the fork point runs on the source's worker without taking a
// slot the copies hold while they wait for it.
func TestForkOfTenDoesNotDeadlockTheSlots(t *testing.T) {
	forkOfTen(t, nil)
}

// A command for the source queued while every slot is held by a copy
// waiting for the fork point sits in front of the point's job on the
// source's worker: the copies give their slots back while they wait, or
// that command never gets one and the whole host stops.
func TestForkOfTenWithASourceCommandDoesNotDeadlock(t *testing.T) {
	for i := 0; i < 20; i++ {
		var sec *hostdv1.Command
		h := forkOfTen(t, func(h *harness) {
			sec = cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "TOKEN", Value: []byte("t")}}})
			h.m.Dispatch(sec)
		})
		deadline := time.Now().Add(5 * time.Second)
		for h.rec.resultFor(sec.CommandId) == nil {
			if time.Now().After(deadline) {
				t.Fatal("the source's command never finished")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if r := h.rec.resultFor(sec.CommandId); !r.Ok {
			t.Fatalf("source's command: %v", r)
		}
	}
}

func forkOfTen(t *testing.T, during func(h *harness)) *harness {
	t.Helper()
	h := newHarness(t, func(c *Config) { c.MaxOps = 2 })
	h.create(gid1)
	var ids []string
	for i := 0; i < 10; i++ {
		ids = append(ids, "0192f0d"+string(rune('0'+i))+"-1111-7000-8000-00000000000"+string(rune('0'+i)))
	}
	var cmds []*hostdv1.Command
	for i, id := range ids {
		c := cmd(cloneReq(h, id, ids...))
		cmds = append(cmds, c)
		h.m.Dispatch(c)
		if i == 1 && during != nil {
			during(h)
		}
	}
	results := make([]*hostdv1.Result, len(cmds))
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; i < len(cmds); {
		if results[i] = h.rec.resultFor(cmds[i].CommandId); results[i] != nil {
			i++
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("copy %d never finished: the fork deadlocked", i)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i, r := range results {
		if r == nil || !r.Ok {
			t.Fatalf("copy %d: %v", i, r)
		}
	}
	if n := strings.Count(strings.Join(h.guestd(gid1).Kinds(), ","), "Freeze"); n != 1 {
		t.Fatalf("source frozen %d times for one fork of ten", n)
	}
	if _, ok := h.lvm.Volumes[ForkPointName(forkPoint)]; ok {
		t.Fatal("the fork point outlived the fork")
	}
	return h
}

func TestHelloNamesLocalClone(t *testing.T) {
	h := newHarness(t, nil)
	if !slices.Contains(h.m.Hello().Features, FeatureLocalClone) {
		t.Fatalf("Hello features %v", h.m.Hello().Features)
	}
}

func (r *recorder) resultFor(commandID string) *hostdv1.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, res := range r.results {
		if res.CommandId == commandID {
			return res
		}
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
