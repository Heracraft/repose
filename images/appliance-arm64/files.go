// Package appliance holds the host files of the Mac appliance and the x86
// installer (host/), which build.sh copies into the image and embed.sh
// into the installer, and the tests of the host scripts with logic worth
// one. HostFiles is the part hostd installs itself on a host that was set
// up before the file existed (DECISIONS I-1638).
package appliance

import "embed"

// HostFiles are the files a hostd brings to an appliance or an installer
// host that lacks them: the zram swap (I-1626) and the snapshot store
// volume (I-1636), each a script and its unit.
//
//go:embed host/bin/repose-zram host/bin/repose-snapshots-volume host/systemd/repose-zram.service host/systemd/repose-snapshots-volume.service
var HostFiles embed.FS
