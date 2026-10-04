package guest

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// The guest@<id> transient unit runs Cloud Hypervisor as the unprivileged
// hostd user, not root (security review 2026-09-20, H-2; DECISIONS I-51).
// A KVM escape then lands as a user that owns taps and can open /dev/kvm,
// /dev/net/tun and its own volume, and nothing else: the cgroup device
// policy allows those three nodes only, the mount namespace shows the unit
// its own guest directory and no other guest's sockets, and the address
// families stop it opening network sockets on the host. hostd itself stays
// root: it needs LVM, nftables and tap creation.

// GuestUnitProps renders the systemd properties of guest@<id>. dir is the
// guest's directory, guestsDir its parent, volumeDev the guest's thin
// volume. The first three properties are the memory and CPU caps that
// docs/interfaces/host-conventions.md names; the rest is the sandbox.
// They take effect when the unit starts: a running guest keeps the shape
// it was started with until its next start (reconcile never restarts one).
func GuestUnitProps(class Class, user, guestsDir, dir, volumeDev string) []string {
	return []string{
		fmt.Sprintf("MemoryMax=%dM", class.MemMiB+OverheadMiB),
		fmt.Sprintf("MemoryHigh=%dM", class.MemMiB+OverheadMiB-HighMarginMiB),
		fmt.Sprintf("CPUQuota=%d%%", class.VCPUs*100),
		"Restart=no",
		"Slice=guests.slice",
		"User=" + user,
		"NoNewPrivileges=yes",
		"CapabilityBoundingSet=",
		"UMask=0077",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectKernelLogs=yes",
		"ProtectControlGroups=yes",
		"ProtectProc=invisible",
		"RestrictNamespaces=yes",
		"RestrictRealtime=yes",
		"RestrictSUIDSGID=yes",
		"LockPersonality=yes",
		"SystemCallArchitectures=native",
		// Only this guest's directory is visible under the guests root:
		// another guest's vsock.sock is a direct line to its guestd.
		"TemporaryFileSystem=" + guestsDir,
		"BindPaths=" + dir,
		"ReadWritePaths=" + dir,
		"DevicePolicy=closed",
		"DeviceAllow=/dev/kvm rw",
		"DeviceAllow=/dev/net/tun rw",
		"DeviceAllow=" + volumeDev + " rw",
		"RestrictAddressFamilies=AF_UNIX AF_VSOCK",
	}
}

// LookupUser returns a user's uid and primary gid.
func LookupUser(name string) (uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("user %s: uid %q", name, u.Uid)
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("user %s: gid %q", name, u.Gid)
	}
	return uid, gid, nil
}

// prepareGuestDir makes the guest directory usable by the two unprivileged
// units that create sockets in it. The layout (host-conventions.md):
//
//	<guests>/                 0711 root      virtiofsd traverses to its socket
//	<guests>/<id>/            1770 root:hostd  CH creates ch.sock, vsock.sock,
//	                                            console.sock; sticky so it
//	                                            cannot remove hostd's files
//	<guests>/<id>/virtiofsd/  0750 virtiofsd:hostd  virtiofsd.sock, group hostd
//	                                            so CH can connect
//
// The owner of everything but the virtiofsd directory is hostd's own uid
// (root on a host), which keeps ch.args, guest.json and console.log out of
// the hypervisor's reach. Every directory goes through ensureDir: the
// hypervisor's user can create entries in <id>/, so a name there may be a
// planted link.
func (m *Manager) prepareGuestDir(dir string) error {
	lookup := m.cfg.Lookup
	if lookup == nil {
		lookup = LookupUser
	}
	_, ggid, err := lookup(m.cfg.GuestUser)
	if err != nil {
		return fmt.Errorf("guest user %s: %w", m.cfg.GuestUser, err)
	}
	vuid, _, err := lookup(m.cfg.VirtiofsUser)
	if err != nil {
		return fmt.Errorf("virtiofsd user %s: %w", m.cfg.VirtiofsUser, err)
	}
	if err := os.MkdirAll(filepath.Dir(m.cfg.GuestsDir), 0o755); err != nil {
		return err
	}
	if err := ensureDir(m.cfg.GuestsDir, 0o711, -1, -1); err != nil {
		return err
	}
	if err := ensureDir(dir, os.FileMode(0o770)|os.ModeSticky, -1, ggid); err != nil {
		return err
	}
	return ensureDir(filepath.Join(dir, "virtiofsd"), 0o750, vuid, ggid)
}
