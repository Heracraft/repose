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
		if !readOnlyRefusal(p.VdaWrite) {
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
