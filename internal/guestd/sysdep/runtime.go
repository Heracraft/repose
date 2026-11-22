package sysdep

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Runtime is the kind of guest guestd runs in (DECISIONS I-1474). Every
// behaviour that differs between the two branches on it, and the Nix
// branch is the one guestd had before erofs guests.
type Runtime string

const (
	// RuntimeNix is a NixOS guest: root ext4 on the volume, the host's
	// store shared in over virtio-fs.
	RuntimeNix Runtime = "nix"
	// RuntimeErofs is an Ubuntu guest booted from an erofs base: / is an
	// overlayfs over the erofs, and the volume (vdb, ext4) is mounted at
	// /run/repose/rw (docs/interfaces/guest-conventions.md "The erofs guest").
	RuntimeErofs Runtime = "erofs"
)

// Guest paths of the two mounts an erofs guest's initramfs leaves. They
// are compared with mountinfo's mount points, which are always the
// guest's absolute paths, so a test root never prefixes them.
const (
	ErofsROMount = "/run/repose/ro"
	ErofsRWMount = "/run/repose/rw"
)

// Mount is one line of /proc/self/mountinfo, the fields guestd reads.
type Mount struct {
	// Point is the mount point, unescaped.
	Point string
	// FSType is the filesystem type after the separator.
	FSType string
	// Source is the mount source (a device for a block filesystem).
	Source string
}

// ReadMountinfo parses a mountinfo file (proc(5)): field 5 is the mount
// point, and after the lone "-" come the type and the source.
func ReadMountinfo(path string) ([]Mount, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var out []Mount
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if len(fields) < 5 || sep < 0 || sep+2 >= len(fields) {
			continue
		}
		out = append(out, Mount{
			Point:  unescapeMount(fields[4]),
			FSType: fields[sep+1],
			Source: unescapeMount(fields[sep+2]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan mountinfo: %w", err)
	}
	return out, nil
}

// FindMount is the mount on top at point: mountinfo lists mounts in the
// order they were made, so the last match is the one a path resolves to.
func FindMount(ms []Mount, point string) (Mount, bool) {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Point == point {
			return ms[i], true
		}
	}
	return Mount{}, false
}

// DetectRuntime decides the guest's runtime from mountinfo, the one place
// it is decided (DECISIONS I-1474): an erofs guest has /run/repose/ro as
// an erofs mount and /run/repose/rw as an ext4 mount. Anything else,
// including a mountinfo that cannot be read, is a Nix guest, which is
// what every guest was before erofs.
func DetectRuntime(p Paths) Runtime {
	ms, err := ReadMountinfo(p.Mountinfo())
	if err != nil {
		return RuntimeNix
	}
	ro, okRO := FindMount(ms, ErofsROMount)
	rw, okRW := FindMount(ms, ErofsRWMount)
	if okRO && okRW && ro.FSType == "erofs" && rw.FSType == "ext4" {
		return RuntimeErofs
	}
	return RuntimeNix
}

// unescapeMount undoes the octal escapes mountinfo writes for space, tab,
// newline and backslash.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
