package guest

import (
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
