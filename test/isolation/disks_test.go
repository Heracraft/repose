package isolation

import (
	"path/filepath"
	"strings"
	"testing"
)

// The disk row's rule, per guest runtime (DECISIONS I-1480): a
// Nix guest sees one disk, its volume; an erofs guest sees exactly vda,
// the read-only base image, and vdb, its volume, and vda refuses a write
// from root inside the guest. Fixtures are what lsblk and a dd print on
// the two kinds of guest; this runs in CI without a host.
func TestDiskVerdict(t *testing.T) {
	refused := "dd: failed to open '/dev/vda': Read-only file system"
	for _, c := range []struct {
		name string
		p    diskProbe
		bad  string // a substring of the error, "" for a pass
	}{
		{"nix one disk", diskProbe{Runtime: "nix", Disks: []string{"vda"}}, ""},
		{"nix two disks", diskProbe{Runtime: "nix", Disks: []string{"vda", "vdb"}}, "want 1"},
		{"erofs vda ro and vdb", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb"}, RO: map[string]string{"vda": "1", "vdb": "0"}, VdaWrite: refused, VdaWriteCode: 1}, ""},
		{"erofs EROFS errno", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb"}, RO: map[string]string{"vda": "1", "vdb": "0"}, VdaWrite: "open: EROFS", VdaWriteCode: 1}, ""},
		{"erofs one disk", diskProbe{Runtime: "erofs", Disks: []string{"vdb"}, RO: map[string]string{"vdb": "0"}, VdaWrite: refused, VdaWriteCode: 1}, "want exactly [vda vdb]"},
		{"erofs a third disk", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb", "vdc"}, RO: map[string]string{"vda": "1", "vdb": "0", "vdc": "0"}, VdaWrite: refused, VdaWriteCode: 1}, "want exactly [vda vdb]"},
		{"erofs vda writable flag", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb"}, RO: map[string]string{"vda": "0", "vdb": "0"}, VdaWrite: refused, VdaWriteCode: 1}, "vda is RO=0"},
		{"erofs write went through", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb"}, RO: map[string]string{"vda": "1", "vdb": "0"}, VdaWrite: "1+0 records out", VdaWriteCode: 0}, "write to /dev/vda succeeded"},
		{"erofs write refused for another reason", diskProbe{Runtime: "erofs", Disks: []string{"vda", "vdb"}, RO: map[string]string{"vda": "1", "vdb": "0"}, VdaWrite: "sudo: a password is required", VdaWriteCode: 1}, "not as read-only"},
		{"unknown runtime", diskProbe{Runtime: "", Disks: []string{"vda"}}, "runtime"},
	} {
		err := diskVerdict(c.p)
		switch {
		case c.bad == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.bad)
		}
	}
}

// The row's own test, run through a fixture guest of each runtime: the
// probes it sends and the answers a real guest of that kind gives.
func TestGuestSeesOneDiskFixture(t *testing.T) {
	script, err := filepath.Abs("testdata/fake-guest.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{runtimeNix, runtimeErofs} {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("FAKE_RUNTIME", rt)
			t.Setenv(envPrefix+"EXEC_A", script)
			TestGuestSeesOneDisk(t)
		})
	}
}

// The store row on an erofs guest (DECISIONS I-1480): the base image at
// /run/repose/ro is an erofs mounted read-only, a write into it fails as
// read-only (never because the path is missing, which is how the Nix
// check passed on an erofs guest for the wrong reason), and a root write
// under /usr lands in the overlay's upper directory, not in the base.
func TestErofsStoreVerdict(t *testing.T) {
	ro := "touch: cannot touch '/run/repose/ro/repose-isolation-probe': Read-only file system"
	good := storeProbe{Mount: "erofs ro,relatime,user_xattr,acl", DirExists: true, DevWrite: ro, DevWriteCode: 1, RootWrite: ro, RootWriteCode: 1}
	for _, c := range []struct {
		name string
		mut  func(p *storeProbe)
		bad  string
	}{
		{"read-only base", func(*storeProbe) {}, ""},
		{"missing directory", func(p *storeProbe) {
			p.DirExists = false
			p.DevWrite = "touch: cannot touch '/run/repose/ro/x': No such file or directory"
			p.RootWrite = p.DevWrite
		}, "does not exist"},
		{"ENOENT not EROFS", func(p *storeProbe) { p.RootWrite = "No such file or directory" }, "not as read-only"},
		{"permission not EROFS", func(p *storeProbe) { p.DevWrite = "Permission denied" }, "not as read-only"},
		{"root write went through", func(p *storeProbe) { p.RootWriteCode = 0; p.RootWrite = "" }, "succeeded"},
		{"mounted rw", func(p *storeProbe) { p.Mount = "erofs rw,relatime" }, "not mounted read-only"},
		{"not erofs", func(p *storeProbe) { p.Mount = "ext4 ro,relatime" }, "not an erofs"},
		{"usr reached the base", func(p *storeProbe) { p.UsrInBase = true }, "reached the base image"},
	} {
		p := good
		c.mut(&p)
		err := erofsStoreVerdict(p)
		switch {
		case c.bad == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.bad)
		}
	}
}

// The store row's own test through the fixture guest of each runtime.
func TestGuestCannotWriteStoreFixture(t *testing.T) {
	script, err := filepath.Abs("testdata/fake-guest.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{runtimeNix, runtimeErofs} {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("FAKE_RUNTIME", rt)
			t.Setenv(envPrefix+"EXEC_A", script)
			TestGuestCannotWriteStore(t)
		})
	}
}
