package isolation

import (
	"strconv"
	"strings"
	"testing"
)

// Row: guest cannot write the store. Mechanism: virtiofsd (unprivileged,
// namespace sandbox) serves a read-only bind of the store with `.links`
// masked by an empty tmpfs; the guest mounts the tag read-only.
func TestGuestCannotWriteStore(t *testing.T) {
	need(t, "EXEC_A")
	mustFail(t, inA(t, "touch /nix/store/repose-isolation-probe"), "touch /nix/store as dev")
	mustFail(t, inA(t, "sudo -n touch /nix/.ro-store/repose-isolation-probe"), "touch the virtio-fs share as root")
	mustFail(t, inA(t, "sudo -n sh -c 'echo x > /nix/.ro-store/.links/repose-isolation-probe'"), "write under .links")
	r := inA(t, "ls -A /nix/.ro-store/.links 2>&1 | head -c 400; echo; ls -A /nix/.ro-store/.links 2>/dev/null | wc -l")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	count := strings.TrimSpace(lines[len(lines)-1])
	if n, err := strconv.Atoi(count); err == nil && n > 0 {
		t.Fatalf("the store's .links hard-link farm is visible from the guest (%d entries):\n%s", n, r)
	}
	t.Logf(".links from the guest: %s", strings.TrimSpace(r.out))
}

// Row: guest cannot see host block devices; only its own disks are
// virtio-blk devices: one, its thin volume, on a Nix guest; exactly vda
// (the base image, read-only to the guest) and vdb (its thin volume) on
// an erofs guest (DECISIONS I-1480). The runtime is the guest's own:
// an erofs guest has its volume mounted at /run/repose/rw;
// REPOSE_ISOLATION_A_RUNTIME (nix or erofs) overrides it.
func TestGuestSeesOneDisk(t *testing.T) {
	need(t, "EXEC_A")
	rt := env("A_RUNTIME")
	if rt == "" {
		r := inA(t, "if mountpoint -q /run/repose/rw; then echo erofs; else echo nix; fi")
		mustSucceed(t, r, "runtime probe in A")
		rt = strings.TrimSpace(r.out)
	}
	// zram (the guest's swap), loop and ram devices are the guest's own
	// kernel, not something the hypervisor exposed; the row is about block
	// devices reaching the guest from the host (host-01 showed [zram0 vda]).
	r := inA(t, "lsblk -dn -o NAME,TYPE | awk '$2==\"disk\" && $1 !~ /^(zram|loop|ram)/{print $1}'")
	mustSucceed(t, r, "lsblk in A")
	p := diskProbe{Runtime: rt, Disks: strings.Fields(r.out)}
	if rt == runtimeErofs {
		ro := inA(t, "lsblk -dn -o NAME,RO /dev/vda /dev/vdb")
		mustSucceed(t, ro, "lsblk RO in A")
		p.RO = parseRO(ro.out)
		// One sector of zeros as root, opened for writing without
		// truncating: the open itself is what a read-only device refuses.
		w := inA(t, "sudo -n dd if=/dev/zero of=/dev/vda bs=512 count=1 conv=notrunc,fsync 2>&1")
		p.VdaWrite, p.VdaWriteCode = w.out+w.err, w.code
	}
	if err := diskVerdict(p); err != nil {
		t.Fatal(err)
	}
	mustFail(t, inA(t, "test -e /dev/vg-guests"), "host volume group visible in A")
	t.Logf("runtime %s, guest disks %v, RO %v", rt, p.Disks, p.RO)
}
