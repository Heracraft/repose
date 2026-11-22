package sysdep

import (
	"os"
	"path/filepath"
)

// FixtureMountinfoErofs is /proc/self/mountinfo of an erofs guest as the
// initramfs leaves it (docs/interfaces/guest-conventions.md "The erofs
// guest"): an overlayfs root over the erofs at /run/repose/ro, the vdb
// ext4 at /run/repose/rw, and its four binds. Tests across guestd's
// packages write it with WriteMountinfo.
const FixtureMountinfoErofs = `21 26 0:20 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
22 26 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:2 - sysfs sysfs rw
23 26 0:5 / /dev rw,nosuid shared:8 - devtmpfs devtmpfs rw,size=1012345k,mode=755
24 26 0:23 / /run rw,nosuid,nodev shared:9 - tmpfs tmpfs rw,size=404040k,mode=755
26 1 0:24 / / rw,relatime shared:1 - overlay overlay rw,lowerdir=/run/repose/ro,upperdir=/run/repose/rw/overlay/upper,workdir=/run/repose/rw/overlay/work
27 24 253:0 / /run/repose/ro ro,relatime shared:3 - erofs /dev/vda ro,user_xattr,acl,cache_strategy=readaround
28 24 253:16 / /run/repose/rw rw,noatime shared:4 - ext4 /dev/vdb rw,discard
29 26 253:16 /home-dev /home/dev rw,noatime shared:5 - ext4 /dev/vdb rw,discard
30 26 253:16 /docker /var/lib/docker rw,noatime shared:6 - ext4 /dev/vdb rw,discard
31 26 253:16 /services /var/lib/repose/services rw,noatime shared:7 - ext4 /dev/vdb rw,discard
32 26 253:16 /etc-persist/machine-id /etc/machine-id rw,noatime shared:4 - ext4 /dev/vdb rw,discard
`

// FixtureMountinfoNix is a NixOS guest's: the volume's ext4 at / and the
// host's store over virtio-fs.
const FixtureMountinfoNix = `21 26 0:20 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
24 26 0:23 / /run rw,nosuid,nodev shared:9 - tmpfs tmpfs rw,size=404040k,mode=755
26 1 253:0 / / rw,relatime shared:1 - ext4 /dev/vda rw
27 24 0:25 / /run/repose rw,nosuid,nodev shared:10 - tmpfs tmpfs rw,mode=755
28 26 0:26 / /nix/.ro-store ro,relatime shared:11 - virtiofs ro-store ro
`

// WriteMountinfo writes content as p's /proc/self/mountinfo (tests).
func WriteMountinfo(p Paths, content string) error {
	if err := os.MkdirAll(filepath.Dir(p.Mountinfo()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.Mountinfo(), []byte(content), 0o644)
}
