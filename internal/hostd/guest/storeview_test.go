package guest

import (
	"context"
	"os"
	"slices"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/storeview"
)

// A guest's virtiofsd serves a view of the guest's own closure (I-463):
// its unit has a private mount namespace with an empty tmpfs as the
// shared directory, and hostd binds the closure's paths into it before
// the hypervisor starts. An in-place switch adds the new closure's paths
// before the guest switches, and keeps the old ones.
func TestGuestStoreIsAViewOfItsClosure(t *testing.T) {
	h := newHarness(t, nil)
	dep := "/nix/store/2ndah67h0z5m31v2wkdmg2md4380ggr5-bash-interactive-5.3p15"
	h.nix.Closures = map[string][]string{h.closure: {h.closure, dep}}
	h.create(gid1)
	unit := "virtiofsd@" + gid1
	v := h.sd.Units[unit]
	if !slices.Contains(v.Props, "PrivateMounts=yes") || !slices.Contains(v.Argv, storeview.Dir) {
		t.Fatalf("virtiofsd is not serving a view: %v %v", v.Props, v.Argv)
	}
	for _, p := range []string{h.closure, dep} {
		if !h.view.Has(unit, p) {
			t.Fatalf("view lacks %s", p)
		}
	}
	if n := len(h.view.Views[unit]); n != 2 {
		t.Fatalf("view holds %d paths, want the closure's 2", n)
	}

	closure2 := fakeClosure(t, "nixos-system-v2")
	dep2 := "/nix/store/pyghkr26krhfj4hbr3xz2sh3xbip048s-fonts.conf"
	h.nix.Closures[closure2] = []string{closure2, dep2}
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure2}))
	for _, p := range []string{h.closure, dep, closure2, dep2} {
		if !h.view.Has(unit, p) {
			t.Fatalf("after the switch the view lacks %s", p)
		}
	}
}

// A view that cannot be filled fails the boot at the virtiofsd step: a
// guest whose store is missing its own closure would not come up.
func TestStoreViewFailureFailsTheBoot(t *testing.T) {
	h := newHarness(t, nil)
	h.view.Err = storeview.ErrNotPivoted
	req := createReq(gid1)
	req.SystemClosure = h.closure
	h.mustFail(cmd(req), CodeInternal)
	if g := h.guest(gid1); g.State != StateError {
		t.Fatalf("state %s", g.State)
	}
	if _, ok := h.sd.Units["guest@"+gid1]; ok {
		t.Fatal("hypervisor started over an empty store view")
	}
}

// --store-export naming another directory shares it whole, as before
// I-463: no namespace properties, nothing bound.
func TestWholeStoreExportStillWorks(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.StoreExport = "/run/repose/store-export" })
	h.create(gid1)
	v := h.sd.Units["virtiofsd@"+gid1]
	if slices.Contains(v.Props, "PrivateMounts=yes") || !slices.Contains(v.Argv, "/run/repose/store-export") {
		t.Fatalf("whole export: %v %v", v.Props, v.Argv)
	}
	if len(h.view.Views) != 0 {
		t.Fatalf("bound into a whole export: %v", h.view.Views)
	}
}

// After a restart the view still holds the closures the guest ran before
// (its nix database lists their paths as valid, and a user's profile may
// point into them), as long as the host has them; one the host removed is
// left out without failing the boot.
func TestViewKeepsPastClosures(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	unit := "virtiofsd@" + gid1
	first := h.closure
	closure2 := fakeClosure(t, "nixos-system-v2")
	gone := fakeClosure(t, "nixos-system-gone")
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: gone}))
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: closure2}))
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if got := h.guest(gid1).PastClosures; !slices.Equal(got, []string{gone, first}) {
		t.Fatalf("past closures %v", got)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.view.Reset(unit) // a new virtiofsd starts with an empty view
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	for _, p := range []string{closure2, first} {
		if !h.view.Has(unit, p) {
			t.Fatalf("view after restart lacks %s", p)
		}
	}
	if h.view.Has(unit, gone) {
		t.Fatal("view holds a closure the host no longer has")
	}
}

// A guest whose virtiofsd was started before I-463 shares the whole store
// until it restarts: `hostd guests` says so, and reconcile logs it.
func TestGuestsFlagsAWholeStoreExport(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.create(gid2)
	h.view.Whole = map[string]bool{"virtiofsd@" + gid2: true}
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	gs, err := h.m.Guests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{gid1: storeview.ServesView, gid2: storeview.ServesWhole}
	for _, g := range gs {
		if g.Store != want[g.GuestID] {
			t.Fatalf("%s store %q, want %q", g.GuestID, g.Store, want[g.GuestID])
		}
	}
}
