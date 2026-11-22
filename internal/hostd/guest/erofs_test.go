package guest

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/gcroot"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
)

// countingNix records every call into the Nix machinery, so a test can
// say an erofs guest's create made none (DECISIONS I-1480).
type countingNix struct {
	nixbuild.Builder
	mu    sync.Mutex
	calls []string
}

func (c *countingNix) note(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, s)
}

func (c *countingNix) PathExists(ctx context.Context, p string) (bool, error) {
	c.note("PathExists")
	return c.Builder.PathExists(ctx, p)
}

func (c *countingNix) DumpDB(ctx context.Context, p string) ([]byte, error) {
	c.note("DumpDB")
	return c.Builder.DumpDB(ctx, p)
}

func (c *countingNix) Requisites(ctx context.Context, p ...string) ([]string, error) {
	c.note("Requisites")
	return c.Builder.Requisites(ctx, p...)
}

func (c *countingNix) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

func newCountingHarness(t *testing.T) (*harness, *countingNix) {
	t.Helper()
	var cn *countingNix
	h := newHarnessWith(t, nil, func(d *Deps) {
		cn = &countingNix{Builder: d.Nix}
		d.Nix = cn
	})
	h.nix.Registration = []byte("dump-db")
	return h, cn
}

func menu(items ...string) *hostdv1.MenuSpec {
	m := &hostdv1.MenuSpec{RevisionId: "rev-1"}
	for _, it := range items {
		m.Items = append(m.Items, &hostdv1.MenuItem{Id: it})
	}
	return m
}

func (h *harness) ops() string { return strings.Join(h.sd.Ops, "\n") }

func (h *harness) unitOf(id string) (argv, props []string) {
	u := h.sd.Units[GuestUnit(id)]
	return slices.Clone(u.Argv), slices.Clone(u.Props)
}

// The create of an erofs guest skips every Nix step (the GC root, nix
// path-info, the store share and view, the closure's registration) and
// keeps the rest (the login share, secrets, principals, triggers, the
// project's setup), then replays its menu; the same harness's Nix guest
// still runs all of them (DECISIONS I-1480).
func TestCreateDualModeSkipsNixStepsOnErofs(t *testing.T) {
	h, cn := newCountingHarness(t)
	h.mustOK(cmd(h.erofsCreate(gid1, "2026.11.22.1", menu("postgresql"))))
	if c := cn.Calls(); len(c) != 0 {
		t.Fatalf("erofs create called into nix: %v", c)
	}
	g := h.guest(gid1)
	if g.BaseVersion != "2026.11.22.1" || g.SystemClosure != "" || g.State != StateRunning || g.LastGoodClosure != "" {
		t.Fatalf("record %+v", g)
	}
	if ok, _ := h.roots.Exists(gid1); ok {
		t.Fatal("an erofs guest has a GC root")
	}
	if v := h.lvm.Volumes[VolumeName(gid1)]; v == nil || v.Label != "repose-rw" {
		t.Fatalf("volume %+v, want ext4 labelled repose-rw", v)
	}
	ops := h.ops()
	if strings.Contains(ops, "run "+virtiofs.Unit(gid1)) {
		t.Fatalf("erofs guest started a store virtiofsd:\n%s", ops)
	}
	if !strings.Contains(ops, "run "+virtiofs.AuthUnit(gid1)) {
		t.Fatalf("erofs guest lost its login share:\n%s", ops)
	}
	argv, props := h.unitOf(gid1)
	joined := strings.Join(argv, " ")
	for _, want := range []string{"rootfs.erofs,image_type=raw,readonly=on,id=vda", ",id=vdb,", "tag=claude-auth"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv lacks %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "ro-store") || strings.Contains(joined, "init=") {
		t.Fatalf("erofs argv carries the Nix store or init=: %s", joined)
	}
	if !slices.Contains(props, "BindReadOnlyPaths="+h.images.BundleDir("2026.11.22.1")) {
		t.Fatalf("unit lacks the bundle bind: %v", props)
	}
	kinds := h.guestd(gid1).Kinds()
	if slices.Contains(kinds, "RegisterPaths") {
		t.Fatalf("RegisterPaths sent to an erofs guest: %v", kinds)
	}
	for _, want := range []string{"WriteSecrets", "SetPrincipals", "SetupProject", "ApplyMenu"} {
		if !slices.Contains(kinds, want) {
			t.Fatalf("erofs guest missed %s: %v", want, kinds)
		}
	}
	if i, j := slices.Index(kinds, "SetupProject"), slices.Index(kinds, "ApplyMenu"); i > j {
		t.Fatalf("ApplyMenu before SetupProject: %v", kinds)
	}
	if h.guestd(gid1).MenuRuns() != 1 {
		t.Fatalf("first boot's menu installed %d times", h.guestd(gid1).MenuRuns())
	}

	// The Nix guest beside it runs every step.
	h.create(gid2)
	for _, want := range []string{"PathExists", "DumpDB", "Requisites"} {
		if !slices.Contains(cn.Calls(), want) {
			t.Fatalf("Nix create skipped %s: %v", want, cn.Calls())
		}
	}
	if ok, _ := h.roots.Exists(gid2); !ok {
		t.Fatal("Nix guest has no GC root")
	}
	if !strings.Contains(h.ops(), "run "+virtiofs.Unit(gid2)) {
		t.Fatal("Nix guest has no store virtiofsd")
	}
	if !slices.Contains(h.guestd(gid2).Kinds(), "RegisterPaths") || slices.Contains(h.guestd(gid2).Kinds(), "ApplyMenu") {
		t.Fatalf("Nix guest kinds %v", h.guestd(gid2).Kinds())
	}
	if _, props := h.unitOf(gid2); slices.ContainsFunc(props, func(p string) bool { return strings.HasPrefix(p, "BindReadOnlyPaths=") }) {
		t.Fatalf("Nix unit has a bundle bind: %v", props)
	}
	if h.guest(gid2).BaseVersion != "" {
		t.Fatal("Nix guest names a base")
	}

	// Hello reports each with its system and the host with both runtimes.
	hello := h.m.Hello()
	for _, gs := range hello.Guests {
		switch gs.GuestId {
		case gid1:
			if gs.BaseVersion != "2026.11.22.1" || gs.SystemClosure != "" {
				t.Fatalf("erofs status %+v", gs)
			}
		case gid2:
			if gs.BaseVersion != "" || gs.SystemClosure == "" {
				t.Fatalf("nix status %+v", gs)
			}
		}
	}
	if !slices.Contains(hello.Features, FeatureNix) || !slices.Contains(hello.Features, FeatureErofs) {
		t.Fatalf("features %v", hello.Features)
	}
}

func TestCreateRefusesBothSystems(t *testing.T) {
	h := newHarness(t, nil)
	req := h.erofsCreate(gid1, "v1", nil)
	req.SystemClosure = h.closure
	h.mustFail(cmd(req), CodeInvalidArgument)
	req = h.erofsCreate(gid1, "v1", nil)
	req.BaseImage.Sha256 = "short"
	h.mustFail(cmd(req), CodeInvalidArgument)
	if _, err := h.st.GetGuest(gid1); err == nil {
		t.Fatal("a refused create left a record")
	}
}

// A bundle whose file is not the one named is refused before anything
// is allocated: no record, no volume, nothing in the cache.
func TestCreateBadBundleLeavesNothing(t *testing.T) {
	h := newHarness(t, nil)
	req := h.erofsCreate(gid1, "v-bad", nil)
	req.BaseImage.KernelSha256 = strings.Repeat("0", 64)
	res := h.mustFail(cmd(req), CodeInternal)
	if !strings.Contains(res.Error.Message, "base image checksum mismatch") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if _, err := h.st.GetGuest(gid1); err == nil {
		t.Fatal("record left")
	}
	if _, ok := h.lvm.Volumes[VolumeName(gid1)]; ok {
		t.Fatal("volume left")
	}
	if h.images.Has("v-bad") {
		t.Fatal("bad bundle cached")
	}
}

// Stop and start of an erofs guest on the same base: the boot replays
// the menu with replay_after_base_upgrade, guestd finds nothing to do,
// and nothing Nix runs; resize names vdb.
func TestStopStartAndResizeErofs(t *testing.T) {
	h, cn := newCountingHarness(t)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("redis"))))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	res := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	if c := cn.Calls(); len(c) != 0 {
		t.Fatalf("erofs start called into nix: %v", c)
	}
	st := res.GetStart()
	if st.BootedBaseVersion != "v1" || st.BootedClosure != "" || st.MenuError != nil {
		t.Fatalf("start result %+v", st)
	}
	var replay *guestdv1.ApplyMenu
	for _, c := range h.guestd(gid1).Calls() {
		if c.Kind == "ApplyMenu" {
			replay = c.Req.GetApplyMenu()
		}
	}
	if replay == nil || !replay.ReplayAfterBaseUpgrade || len(replay.Menu.GetItems()) != 1 {
		t.Fatalf("replay %+v", replay)
	}
	if h.guestd(gid1).MenuRuns() != 0 {
		t.Fatal("same base and same menu installed again")
	}
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 50 << 30}))
	if ids := h.chc.Resizes(); len(ids) != 1 || ids[0] != ch.VolumeID {
		t.Fatalf("resize ids %v, want [vdb]", ids)
	}
	h.create(gid2)
	h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid2, NewBytes: 50 << 30}))
	if ids := h.chc.Resizes(); ids[len(ids)-1] != ch.DiskID {
		t.Fatalf("nix resize id %v", ids)
	}
}

// A StartGuest naming a new base fetches it, records it, boots it and
// the replay after SetupProject finds the initramfs's .base-upgraded
// marker and installs the menu again (DECISIONS I-1482).
func TestStartBaseBumpFetchesRecordsAndReplays(t *testing.T) {
	h := newHarness(t, nil)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("postgresql", "bun"))))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if h.images.Has("v2") {
		t.Fatal("v2 cached before the bump")
	}
	res := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1, BaseImage: h.baseImage("v2")}))
	if !h.images.Has("v2") || !h.images.Has("v1") {
		t.Fatalf("cache v1 %v v2 %v", h.images.Has("v1"), h.images.Has("v2"))
	}
	if g := h.guest(gid1); g.BaseVersion != "v2" {
		t.Fatalf("record names %s", g.BaseVersion)
	}
	if v := res.GetStart().BootedBaseVersion; v != "v2" {
		t.Fatalf("booted %s", v)
	}
	argv, props := h.unitOf(gid1)
	if !strings.Contains(strings.Join(argv, " "), h.images.Paths("v2").Rootfs+",") {
		t.Fatalf("booted %v", argv)
	}
	if !slices.Contains(props, "BindReadOnlyPaths="+h.images.BundleDir("v2")) {
		t.Fatalf("unit binds %v", props)
	}
	fg := h.guestd(gid1)
	if fg.MenuRuns() != 1 || fg.BaseUpgraded() {
		t.Fatalf("after the bump: menu runs %d, marker left %v", fg.MenuRuns(), fg.BaseUpgraded())
	}
	kinds := fg.Kinds()
	if slices.Index(kinds, "SetupProject") > slices.Index(kinds, "ApplyMenu") {
		t.Fatalf("order %v", kinds)
	}
	// v1 is no longer named: GC marks it and keeps v2, the newest.
	if refs, _ := h.m.BaseRefs(); refs["v1"] || !refs["v2"] {
		t.Fatalf("refs %v", refs)
	}
}

// A start whose new bundle cannot be fetched touches nothing: the record
// keeps its base, no unit runs, and the error is the contract's.
func TestStartBaseFetchFailureLeavesTheGuest(t *testing.T) {
	h := newHarness(t, nil)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", nil)))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	before := h.ops()
	bi := h.baseImage("v2")
	bi.Url = "file:///nonexistent/rootfs.erofs"
	res := h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1, BaseImage: bi}), CodeInternal)
	if !strings.Contains(res.Error.Message, "base image fetch failed") {
		t.Fatalf("message %q", res.Error.Message)
	}
	g := h.guest(gid1)
	if g.BaseVersion != "v1" || g.State != StateStopped {
		t.Fatalf("record %+v", g)
	}
	if h.ops() != before {
		t.Fatalf("units touched:\n%s", strings.TrimPrefix(h.ops(), before))
	}
}

// A menu replay that fails does not fail the boot: the guest runs and the
// result carries the code.
func TestMenuReplayFailureIsMenuError(t *testing.T) {
	h := newHarness(t, nil)
	h.gopts.Fail = map[string]*guestdv1.Error{"ApplyMenu": {Code: "menu_failed", Message: "postgresql did not start"}}
	res := h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("postgresql"))))
	me := res.GetCreate().GetMenuError()
	if me == nil || me.Code != CodeMenuFailed || me.Message != "postgresql did not start" {
		t.Fatalf("menu_error %+v", me)
	}
	if h.guest(gid1).State != StateRunning {
		t.Fatal("guest not running")
	}
}

// ApplyConfig's menu on a running erofs guest relays guestd's progress as
// BuildLog and records the menu; the wrong system for the guest's kind
// is invalid_argument.
func TestApplyConfigMenu(t *testing.T) {
	h := newHarness(t, nil)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("redis"))))
	c := cmd(&hostdv1.ApplyConfig{GuestId: gid1, Menu: menu("redis", "awscli")})
	h.mustOK(c)
	h.rec.mu.Lock()
	lines := slices.Clone(h.rec.logSeq[c.CommandId])
	h.rec.mu.Unlock()
	if strings.Join(lines, "|") != "1 installing redis|2 installing awscli" {
		t.Fatalf("build log %v", lines)
	}
	if m := recordedMenu(h.guest(gid1)); len(m.GetItems()) != 2 {
		t.Fatalf("recorded menu %v", m)
	}
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: h.closure}), CodeInvalidArgument)
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: h.closure, Menu: menu("x")}), CodeInvalidArgument)
	h.create(gid2)
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid2, Menu: menu("x")}), CodeInvalidArgument)

	h.gopts.Fail = nil
	bad := cmd(&hostdv1.ApplyConfig{GuestId: gid1, Menu: &hostdv1.MenuSpec{Items: []*hostdv1.MenuItem{{Id: "a", Package: "b"}}}})
	h.mustFail(bad, CodeInvalidArgument)
}

// With the Nix machinery off, a Nix guest's commands answer
// nix_unsupported, an erofs guest works, and Hello names erofs alone
// (DECISIONS I-1484).
func TestNixModeOff(t *testing.T) {
	h := newHarnessWith(t, nil, func(d *Deps) {
		d.Nix, d.View, d.Roots = nil, nil, gcroot.Roots{}
	})
	req := createReq(gid2)
	req.SystemClosure = h.closure
	h.mustFail(cmd(req), CodeNixUnsupported)
	h.mustFail(cmd(&hostdv1.Build{ProjectId: "p", RevisionId: "r", Fragment: []byte("{}")}), CodeNixUnsupported)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("redis"))))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	f := h.m.Hello().Features
	if slices.Contains(f, FeatureNix) || !slices.Contains(f, FeatureErofs) {
		t.Fatalf("features %v", f)
	}
	on := newHarness(t, nil).m.Hello().Features
	if !slices.Contains(on, FeatureNix) || !slices.Contains(on, FeatureErofs) {
		t.Fatalf("features with nix on %v", on)
	}
	noImages := newHarnessWith(t, nil, func(d *Deps) { d.Images = nil })
	if f := noImages.m.Hello().Features; slices.Contains(f, FeatureErofs) {
		t.Fatalf("features without an image cache %v", f)
	}
	noImages.mustFail(cmd(noImages.erofsCreate(gid1, "v1", nil)), CodeInvalidArgument)
}

// Reconcile of an erofs guest whose bundle went (a disk wipe) marks it
// rather than failing; a start without base_image then says what is
// missing, and a start with it fetches and clears the mark (I-1484).
func TestReconcileMarksMissingBundle(t *testing.T) {
	h := newHarness(t, nil)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", nil)))
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if err := os.RemoveAll(h.images.BundleDir("v1")); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	g := h.guest(gid1)
	if !g.BaseNeedsFetch || g.State != StateStopped {
		t.Fatalf("record %+v", g)
	}
	var found bool
	for _, gs := range h.m.Hello().Guests {
		found = found || (gs.GuestId == gid1 && gs.BaseVersion == "v1" && gs.State == StateStopped)
	}
	if !found {
		t.Fatal("Hello lost the guest")
	}
	res := h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeNotFound)
	if !strings.Contains(res.Error.Message, "base image v1 missing") {
		t.Fatalf("message %q", res.Error.Message)
	}
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1, BaseImage: h.baseImage("v1")}))
	if g := h.guest(gid1); g.BaseNeedsFetch || g.State != StateRunning {
		t.Fatalf("record after the fetch %+v", g)
	}
}

// A snapshot records the base in the blob's metadata; a restore with
// base_image records and fetches it; a fork of an erofs guest takes its
// source's base (I-1480).
func TestSnapshotRestoreForkCarryTheBase(t *testing.T) {
	h := newHarness(t, nil)
	h.mustOK(cmd(h.erofsCreate(gid1, "v1", menu("redis"))))
	snap := h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"})).GetSnapshot()
	if meta := h.blob.Meta[snap.BlobPath]; meta["base_version"] != "v1" {
		t.Fatalf("blob meta %v", meta)
	}
	const gid3 = "0192f0a3-3333-7000-8000-000000000003"
	r := &hostdv1.Restore{ProjectId: "proj-0192f0a3", GuestId: gid3, BlobPath: snap.BlobPath, Sha256: snap.Sha256, Class: "large", VolumeBytes: 40 << 30, BaseImage: h.baseImage("v2")}
	h.mustOK(cmd(r))
	if g := h.guest(gid3); g.BaseVersion != "v2" || g.SystemClosure != "" || g.State != StateStopped || !h.images.Has("v2") {
		t.Fatalf("restored %+v, cached %v", g, h.images.Has("v2"))
	}
	r2 := &hostdv1.Restore{ProjectId: "proj-0192f0a3", GuestId: gid2, BlobPath: snap.BlobPath, Class: "large", VolumeBytes: 40 << 30, BaseImage: h.baseImage("v2"), SystemClosure: h.closure}
	h.mustFail(cmd(r2), CodeInvalidArgument)

	// The fork: a same-host clone of the erofs source, no base_image.
	fork := cloneReq(h, gidF1, gidF1)
	fork.SystemClosure = ""
	h.mustOK(cmd(fork))
	if g := h.guest(gidF1); g.BaseVersion != "v1" || g.SystemClosure != "" || recordedMenu(g) == nil {
		t.Fatalf("fork %+v", g)
	}
}

// An erofs guest parks and unparks without a store share: the unpark
// brings back the login share and the hypervisor from its image, starts
// no virtiofsd@<id>, and binds the bundle into the unit; an image whose
// guest now names another base is discarded (I-1480).
func TestParkUnparkErofs(t *testing.T) {
	fh := newParkHarness(t, nil)
	fh.mustOK(cmd(fh.erofsCreate(gid1, "v1", nil)))
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("park %+v", r)
	}
	if rec := fh.record(gid1); rec.BaseVersion != "v1" || rec.Closure != "" {
		t.Fatalf("record %+v", rec)
	}
	mark := len(fh.sd.Ops)
	if err := fh.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fh.unitActive(gid1) {
		t.Fatal("not running after the unpark")
	}
	after := strings.Join(fh.sd.Ops[mark:], "\n")
	if strings.Contains(after, "run "+virtiofs.Unit(gid1)) {
		t.Fatalf("unpark started a store virtiofsd:\n%s", after)
	}
	if !strings.Contains(after, "run "+virtiofs.AuthUnit(gid1)) {
		t.Fatalf("unpark lost the login share:\n%s", after)
	}
	if _, props := fh.unitOf(gid1); !slices.Contains(props, "BindReadOnlyPaths="+fh.images.BundleDir("v1")) {
		t.Fatalf("restored unit lacks the bundle: %v", props)
	}
	fh.waitState(gid1, StateRunning)

	// Park again, then change the recorded base under the image.
	if r := fh.parkOne(gid1); !r.OK {
		t.Fatalf("second park %+v", r)
	}
	g := fh.guest(gid1)
	g.BaseVersion = "v9"
	if err := fh.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	res := fh.m.UnparkAll(context.Background())
	if len(res) != 1 || res[0].OK || res[0].Reason != "base changed since the park" {
		t.Fatalf("unpark %+v", res)
	}
}
