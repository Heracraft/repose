package system

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// storeName is a store path basename: a 32-character hash, a dash, a name.
func storeName(c byte, name string) string { return strings.Repeat(string(c), 32) + "-" + name }

// overlayRoot builds a fake guest whose /nix/store is an overlay as the
// scripted initrd mounts it (I-231), with these names in the lower layer.
func overlayRoot(t *testing.T, mountsPrefix string, lower ...string) (sysdep.Paths, storeView) {
	t.Helper()
	root := t.TempDir()
	p := sysdep.Paths{Root: root}
	for _, d := range []string{"proc", "nix/.ro-store", "nix/.rw-store/store", "nix/.rw-store/work", "nix/store", "run/repose", "nix/var/nix/db"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mounts := "ro-store /nix/.ro-store virtiofs rw,relatime 0 0\n" +
		"overlay /nix/store overlay rw,relatime,lowerdir=" + mountsPrefix + "/nix/.ro-store,upperdir=" + mountsPrefix +
		"/nix/.rw-store/store,workdir=" + mountsPrefix + "/nix/.rw-store/work 0 0\n" +
		"overlay /nix/store overlay ro,nosuid,nodev,relatime,lowerdir=" + mountsPrefix + "/nix/.ro-store,upperdir=" +
		mountsPrefix + "/nix/.rw-store/store,workdir=" + mountsPrefix + "/nix/.rw-store/work 0 0\n"
	if err := os.WriteFile(p.Mounts(), []byte(mounts), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range lower {
		if err := os.MkdirAll(filepath.Join(root, "nix/.ro-store", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p, storeView{upper: filepath.Join(root, "nix/.rw-store/store"), lowers: []string{filepath.Join(root, "nix/.ro-store")}}
}

// mkWhiteout makes what overlayfs leaves in the upper dir when a path the
// lower layer serves is deleted: a 0:0 character device. Linux lets any
// user make that one (5.8 and later).
func mkWhiteout(t *testing.T, v storeView, name string) {
	t.Helper()
	if err := syscall.Mknod(filepath.Join(v.upper, name), syscall.S_IFCHR|0o600, 0); err != nil {
		t.Fatalf("mknod a whiteout: %v", err)
	}
}

func roots(t *testing.T, p sysdep.Paths) map[string]string {
	t.Helper()
	es, err := os.ReadDir(p.ViewRoots())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range es {
		target, err := os.Readlink(filepath.Join(p.ViewRoots(), e.Name()))
		if err != nil {
			t.Fatalf("%s is not a symlink: %v", e.Name(), err)
		}
		out[e.Name()] = target
	}
	return out
}

func countCalls(run *sysdep.FakeRunner, prefix string) int {
	n := 0
	for _, c := range run.Calls() {
		if strings.HasPrefix(strings.Join(c.Argv, " "), prefix) {
			n++
		}
	}
	return n
}

func registration(paths ...string) []byte {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p + "\n0000\n1\n/nix/store/" + storeName('z', "x.drv") + "\n0\n")
	}
	return []byte(b.String())
}

// I-588: after a registration every path the shared store serves and the
// database lists is rooted under its own name, a path the database does
// not list is not (nix would skip that root), and the root of a path the
// view no longer serves goes.
func TestRegisterPathsRootsEverySharedPath(t *testing.T) {
	sys, lib, unreg := storeName('a', "nixos-system"), storeName('b', "glibc"), storeName('c', "never-run")
	p, _ := overlayRoot(t, "/mnt-root", sys, lib, unreg)
	stale := storeName('d', "old-system")
	if err := os.MkdirAll(p.ViewRoots(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/"+stale, filepath.Join(p.ViewRoots(), stale)); err != nil {
		t.Fatal(err)
	}
	run := sysdep.NewFakeRunner()
	run.Match["nix-store --check-validity"] = sysdep.RunResult{Stdout: []byte("/nix/store/" + unreg + "\n/nix/store/" + sys + "\n")}
	h := New(p, run, 0, quietLog())

	if err := h.RegisterPaths(context.Background(), registration("/nix/store/"+sys, "/nix/store/"+lib)); err != nil {
		t.Fatal(err)
	}
	got := roots(t, p)
	want := map[string]string{sys: "/nix/store/" + sys, lib: "/nix/store/" + lib}
	if len(got) != len(want) || got[sys] != want[sys] || got[lib] != want[lib] {
		t.Fatalf("roots = %v, want %v (the registered system, now valid, and the valid lib; not the unregistered path or the stale root)", got, want)
	}
	if n := countCalls(run, "nix-store --load-db"); n != 1 {
		t.Fatalf("%d loads, want 1", n)
	}
	// One validity question for the listing and the view together.
	if n := countCalls(run, "nix-store --check-validity --print-invalid"); n != 1 {
		t.Fatalf("%d validity checks, want 1", n)
	}

	// A guestd start with the same view changes nothing.
	h.SyncViewRoots(context.Background())
	if got2 := roots(t, p); len(got2) != 1 || got2[lib] == "" {
		// The fake still says sys is invalid: nothing loaded it this time.
		t.Fatalf("after a sync roots = %v", got2)
	}
}

// I-588: a registration this volume loaded before is loaded again when the
// database lost one of its paths (a garbage collection inside the guest
// deleted it), and skipped, as I-225 does, when it did not.
func TestRegisterPathsReloadsARegistrationTheDatabaseLost(t *testing.T) {
	sys := storeName('a', "nixos-system")
	p, _ := overlayRoot(t, "/mnt-root", sys)
	if err := os.WriteFile(p.NixDB(), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := sysdep.NewFakeRunner()
	h := New(p, run, 0, quietLog())
	reg := registration("/nix/store/" + sys)
	ctx := context.Background()
	if err := h.RegisterPaths(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterPaths(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(run, "nix-store --load-db"); n != 1 {
		t.Fatalf("all valid: %d loads, want 1 (the second skipped)", n)
	}
	run.Match["nix-store --check-validity"] = sysdep.RunResult{Stdout: []byte("/nix/store/" + sys + "\n")}
	if err := h.RegisterPaths(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(run, "nix-store --load-db"); n != 2 {
		t.Fatalf("a registered path invalid: %d loads, want 2", n)
	}
	if got := roots(t, p); got[sys] != "/nix/store/"+sys {
		t.Fatalf("the reloaded system is not rooted: %v", got)
	}
}

// Without an answer from nix every served path is rooted: nix skips a root
// to a path it does not list, so that protects at least as much.
func TestViewRootsWhenValidityIsUnknown(t *testing.T) {
	a, b := storeName('a', "x"), storeName('b', "y")
	p, _ := overlayRoot(t, "/sysroot", a, b)
	run := sysdep.NewFakeRunner()
	run.Match["nix-store --check-validity"] = sysdep.RunResult{ExitCode: 1}
	h := New(p, run, 0, quietLog())
	h.SyncViewRoots(context.Background())
	if got := roots(t, p); len(got) != 2 || got[a] != "/nix/store/"+a || got[b] != "/nix/store/"+b {
		t.Fatalf("roots = %v", got)
	}
}

// No overlay (a test VM's plain store, a dev root): nothing is rooted and
// nix is not asked.
func TestViewRootsWithoutAnOverlay(t *testing.T) {
	p, _, _ := guestRoot(t)
	for _, d := range []string{filepath.Dir(p.Mounts()), p.RunDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(p.Mounts(), []byte("/dev/vda / ext4 rw 0 0\nstore /nix/store virtiofs ro 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := sysdep.NewFakeRunner()
	h := New(p, run, 0, quietLog())
	h.SyncViewRoots(context.Background())
	if err := h.RegisterPaths(context.Background(), []byte("/nix/store/aaaa-x\n\n0\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.ViewRoots()); !os.IsNotExist(err) {
		t.Fatalf("roots written without an overlay: %v", err)
	}
	if n := countCalls(run, "nix-store --check-validity"); n != 0 {
		t.Fatalf("%d validity checks without an overlay", n)
	}
}

// I-589: a switch to a closure a whiteout hides fails with a message that
// says so and what repairs it, before anything is registered or
// activated, with its own code; so does one whose requisite is hidden.
// kanali, 2026-10-06, got "not in the store share" and hostd's `internal`.
func TestSwitchRefusesAClosureAWhiteoutHides(t *testing.T) {
	sys, lib := storeName('a', "nixos-system-next"), storeName('b', "systemd")
	p, v := overlayRoot(t, "/mnt-root", sys, lib)
	run := sysdep.NewFakeRunner()
	h := New(p, run, 0, quietLog())

	mkWhiteout(t, v, sys)
	_, err := h.Switch(context.Background(), "/nix/store/"+sys, false, registration("/nix/store/"+sys, "/nix/store/"+lib))
	if err == nil {
		t.Fatal("a hidden closure switched")
	}
	msg := err.Error()
	if sysdep.CodeOf(err) != sysdep.CodeStorePathHidden || !strings.Contains(msg, "hidden in this machine's store") ||
		!strings.Contains(msg, "Restart the machine") || !strings.Contains(msg, "1 of the store paths") {
		t.Fatalf("error = %s: %q", sysdep.CodeOf(err), msg)
	}
	if len(run.Calls()) != 0 {
		t.Fatalf("ran %v before refusing", run.Calls())
	}

	// The closure itself visible, one of its paths hidden.
	if err := os.Remove(filepath.Join(v.upper, sys)); err != nil {
		t.Fatal(err)
	}
	mkWhiteout(t, v, lib)
	_, err = h.Switch(context.Background(), "/nix/store/"+sys, false, registration("/nix/store/"+sys, "/nix/store/"+lib))
	if err == nil || !strings.Contains(err.Error(), "/nix/store/"+lib+" first") {
		t.Fatalf("hidden requisite: %v", err)
	}

	// A character device that is not 0:0 is no whiteout; nor is a plain
	// file. Neither is reported (the switch fails later, on the missing
	// closure, as before).
	if err := os.Remove(filepath.Join(v.upper, lib)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.upper, lib), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = h.Switch(context.Background(), "/nix/store/"+sys, false, nil)
	if err == nil || strings.Contains(err.Error(), "hidden") || !strings.Contains(err.Error(), "not in the store share") {
		t.Fatalf("no whiteout: %v", err)
	}
}

func TestRegisteredPathsLeavesDeriversOut(t *testing.T) {
	a, b := "/nix/store/"+storeName('a', "x"), "/nix/store/"+storeName('b', "y")
	reg := a + "\nhash\n10\n/nix/store/" + storeName('d', "x.drv") + "\n1\n" + b + "\n" +
		b + "\nhash\n10\n\n0\n"
	got := registeredPaths([]byte(reg))
	sort.Strings(got)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("registeredPaths = %v", got)
	}
}

func TestReadViewDropsTheInitrdPrefixAndUnescapes(t *testing.T) {
	root := t.TempDir()
	p := sysdep.Paths{Root: root}
	if err := os.MkdirAll(filepath.Dir(p.Mounts()), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `overlay /nix/store overlay rw,lowerdir=/sysroot/nix/.ro\040store:/mnt-root/l2,upperdir=/nix/.rw-store/upper,workdir=/w 0 0` + "\n"
	if err := os.WriteFile(p.Mounts(), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(p, sysdep.NewFakeRunner(), 0, quietLog())
	v, ok := h.readView()
	if !ok || v.upper != filepath.Join(root, "/nix/.rw-store/upper") || len(v.lowers) != 2 ||
		v.lowers[0] != filepath.Join(root, "/nix/.ro store") || v.lowers[1] != filepath.Join(root, "/l2") {
		t.Fatalf("view = %+v, %v", v, ok)
	}
}
