package main

import (
	"flag"

	"github.com/heracraft/repose/internal/hostd/app"
)

// platformFlags are hostd's flags on macOS (design 2, DECISIONS I-1664).
func platformFlags(fs *flag.FlagSet, o *app.Options) {
	fs.StringVar(&o.Mac.User, "mac-user", "", "the user every VM runs as when hostd is root (a LaunchDaemon); a VM never runs as root (DECISIONS I-1664)")
	fs.StringVar(&o.Mac.VolumesDir, "mac-volumes-dir", "", "guest volumes, one sparse file each, on APFS (default <state>/volumes)")
	fs.StringVar(&o.Mac.LaunchdDir, "mac-launchd-dir", "", "the launchd jobs hostd writes for its VMs, and their logs (default <state>/launchd)")
	fs.StringVar(&o.Mac.Launcher, "mac-launcher", "", "repose-vm, the VM launcher (default: beside hostd)")
	fs.StringVar(&o.Mac.NetDir, "mac-net-dir", "", "the userspace switch's sockets, one <tap>.sock a VM; empty puts every VM on VZ's NAT with no rules (lab only)")
	fs.IntVar(&o.Mac.NetMTU, "mac-net-mtu", 65520, "the MTU of each VM's NIC and its eth0 (65520: the file-handle NIC carries 27-38 Gbit/s against 4.9 at 1500, I-1668)")
	fs.StringVar(&o.Mac.Mke2fs, "mac-mke2fs", "", "mke2fs, which makes a new volume's ext4 (e2fsprogs; macOS ships none)")
	fs.Uint64Var(&o.Mac.GuestMemMiB, "mac-guest-mem-mib", 6144, "each machine's memory in MiB, below the Mac's placeable memory; VZ's park and wake time follow it (DECISIONS I-1666); 0 gives every machine the placeable memory")
	fs.StringVar(&o.Mac.E2fsck, "mac-e2fsck", "", "e2fsck, which checks a restored snapshot's volume")
}
