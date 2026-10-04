package shell

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// ParserProps are the sandbox of a tool that parses a guest's filesystem
// metadata (dumpe2fs on a snapshot, e2fsck on a restored volume). A guest
// writes every byte of its volume, so a parser bug in e2fsprogs must land
// in a process that holds none of hostd's keys and can reach nothing but
// that one device: a dynamic user with no capabilities, no network, no
// view of hostd's state or sockets, and a device policy naming only the
// device it was given (DECISIONS I-465).
var ParserProps = []string{
	"DynamicUser=yes",
	"DevicePolicy=closed",
	"PrivateNetwork=yes",
	"RestrictAddressFamilies=AF_UNIX",
	"CapabilityBoundingSet=",
	"NoNewPrivileges=yes",
	"InaccessiblePaths=-/var/lib/repose",
	"InaccessiblePaths=-/run/repose",
	"MemoryMax=2G",
	"SystemCallFilter=@system-service",
	"SystemCallArchitectures=native",
	"ProtectKernelTunables=yes",
	"ProtectKernelModules=yes",
	"ProtectKernelLogs=yes",
	"ProtectControlGroups=yes",
	"ProtectClock=yes",
	"ProtectHostname=yes",
	"LockPersonality=yes",
	"MemoryDenyWriteExecute=yes",
	"RestrictNamespaces=yes",
	"RestrictRealtime=yes",
	"PrivateIPC=yes",
	"ProtectProc=invisible",
	"ProcSubset=pid",
	"UMask=0077",
	"RuntimeMaxSec=3600",
}

// Sandboxed returns argv wrapped in a transient systemd unit with
// ParserProps, allowed dev only (read-only unless write), in the device
// node's group so the dynamic user can open it. `systemd-run --pipe
// --wait` passes stdout, stderr and the exit status through, so callers
// read the result as if they had run argv directly.
func Sandboxed(dev string, write bool, argv ...string) []string {
	node := dev
	if p, err := filepath.EvalSymlinks(dev); err == nil {
		node = p // DeviceAllow wants the node, not an LVM link to it
	}
	access := "r"
	if write {
		access = "rw"
	}
	out := []string{"systemd-run", "--quiet", "--pipe", "--wait", "--collect"}
	for _, p := range ParserProps {
		out = append(out, "--property", p)
	}
	out = append(out, "--property", "DeviceAllow="+node+" "+access)
	if fi, err := os.Stat(node); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			out = append(out, "--property", "SupplementaryGroups="+strconv.Itoa(int(st.Gid)))
		}
	}
	return append(append(out, "--"), argv...)
}
