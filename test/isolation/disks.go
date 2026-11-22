package isolation

import (
	"fmt"
	"slices"
	"strings"
)

// The disk row of docs/workstreams/14-security.md: a guest sees no block
// device of the host's, only its own. What "its own" is depends on the
// guest's runtime (DECISIONS I-1480): a Nix guest has one disk, its thin
// volume; an erofs guest has two, vda the base image hostd attaches
// `readonly=on` and vdb its thin volume, and the property that matters
// is that vda is read-only to the guest, root included.

// Guest runtimes, as docs/interfaces/README.md names them.
const (
	runtimeNix   = "nix"
	runtimeErofs = "erofs"
)

// diskProbe is what the disk row reads inside a guest: its runtime, the
// virtio disks lsblk lists, each disk's RO column, and what a write to
// /dev/vda as root printed and exited with (erofs guests only).
type diskProbe struct {
	Runtime      string
	Disks        []string
	RO           map[string]string
	VdaWrite     string
	VdaWriteCode int
}

// readOnlyRefusal is how a write to a read-only block device fails: the
// open answers EROFS, which dd and the shell print as "Read-only file
// system".
func readOnlyRefusal(out string) bool {
	return strings.Contains(out, "Read-only file system") || strings.Contains(out, "EROFS")
}

// deviceWriteRefusal is how the guest's kernel refuses a write to a
// virtio-blk device the host attached readonly=on: the open succeeds and
// the write answers EPERM, which dd prints as "Operation not permitted"
// (staging's Ubuntu guest, 2026-11-22). It counts as read-only only for
// /dev/vda and only beside lsblk's RO=1, which diskVerdict checks first;
// the store row keeps the filesystem's EROFS.
func deviceWriteRefusal(out string) bool {
	return strings.Contains(out, "Operation not permitted") || strings.Contains(out, "EPERM")
}

// diskVerdict is the disk row's judgement of a probe.
func diskVerdict(p diskProbe) error {
	switch p.Runtime {
	case runtimeNix:
		if len(p.Disks) != 1 {
			return fmt.Errorf("guest sees %d disks, want 1: %v", len(p.Disks), p.Disks)
		}
		return nil
	case runtimeErofs:
		if !slices.Equal(p.Disks, []string{"vda", "vdb"}) {
			return fmt.Errorf("erofs guest sees %v, want exactly [vda vdb]", p.Disks)
		}
		if p.RO["vda"] != "1" {
			return fmt.Errorf("vda is RO=%s to the guest, want 1: the base image must be read-only", p.RO["vda"])
		}
		if p.VdaWriteCode == 0 {
			return fmt.Errorf("a write to /dev/vda succeeded as root in the guest: %s", strings.TrimSpace(p.VdaWrite))
		}
		if !readOnlyRefusal(p.VdaWrite) && !deviceWriteRefusal(p.VdaWrite) {
			return fmt.Errorf("a write to /dev/vda failed, but not as read-only (exit %d): %s", p.VdaWriteCode, strings.TrimSpace(p.VdaWrite))
		}
		return nil
	}
	return fmt.Errorf("guest runtime %q unknown; want nix or erofs", p.Runtime)
}

// parseRO reads `lsblk -dn -o NAME,RO` into name -> RO.
func parseRO(out string) map[string]string {
	ro := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			ro[f[0]] = f[1]
		}
	}
	return ro
}

// storeProbe is what the store row reads inside an erofs guest: the
// read-only base's mount, writes into it as dev and as root, and whether
// a root write under /usr reached the base image.
type storeProbe struct {
	Mount         string // findmnt -no FSTYPE,OPTIONS /run/repose/ro
	DirExists     bool
	DevWrite      string
	DevWriteCode  int
	RootWrite     string
	RootWriteCode int
	UsrInBase     bool // /run/repose/ro/usr/<probe> existed after a root write to /usr/<probe>
}

// erofsStoreVerdict is the store row on an erofs guest: the base at
// /run/repose/ro is an erofs mounted read-only, writes into it as dev and
// as root fail as read-only (a missing directory is not evidence of
// anything), and a root write under /usr, which the overlay's upper
// directory takes by design, does not reach the base image.
func erofsStoreVerdict(p storeProbe) error {
	f := strings.Fields(p.Mount)
	if len(f) < 2 || f[0] != "erofs" {
		return fmt.Errorf("/run/repose/ro is not an erofs mount: %q", p.Mount)
	}
	if !slices.Contains(strings.Split(f[1], ","), "ro") {
		return fmt.Errorf("/run/repose/ro is not mounted read-only: %q", p.Mount)
	}
	if !p.DirExists {
		return fmt.Errorf("/run/repose/ro does not exist in the guest, so a refused write there proves nothing")
	}
	for _, w := range []struct {
		who  string
		out  string
		code int
	}{{"dev", p.DevWrite, p.DevWriteCode}, {"root", p.RootWrite, p.RootWriteCode}} {
		if w.code == 0 {
			return fmt.Errorf("a write into /run/repose/ro as %s succeeded: %s", w.who, strings.TrimSpace(w.out))
		}
		if !readOnlyRefusal(w.out) {
			return fmt.Errorf("a write into /run/repose/ro as %s failed, but not as read-only (exit %d): %s", w.who, w.code, strings.TrimSpace(w.out))
		}
	}
	if p.UsrInBase {
		return fmt.Errorf("a root write under /usr reached the base image at /run/repose/ro/usr")
	}
	return nil
}
