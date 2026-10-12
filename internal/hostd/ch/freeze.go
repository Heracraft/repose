package ch

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Freezer is the API-socket side of a guest freeze (DECISIONS I-652):
// Cloud Hypervisor's vm.snapshot of a paused VM, and the argv that starts a
// hypervisor from that snapshot instead of booting. HTTP and Fake implement
// it; it is its own interface so Client, which every test double of hostd
// implements, did not grow.
type Freezer interface {
	// Snapshot writes the paused VM's config, device state and memory to
	// dir (a directory the hypervisor can write; it must exist and be
	// empty). The VM stays paused.
	Snapshot(ctx context.Context, sock, dir string) error
}

// FileURL is the file:// URL of a local directory, as vm.snapshot and
// --restore take it.
func FileURL(dir string) string {
	return (&url.URL{Scheme: "file", Path: dir}).String()
}

// Snapshot implements Freezer.
// The call returns when the image is written, which takes as long as the
// guest's memory takes to write: seconds per GB. The client's own timeout
// (10 s by default) would give up while the hypervisor goes on writing, and
// a resume sent then is refused, which left a guest paused on staging
// (docs/research/guest-freeze.md); only ctx bounds it.
func (h *HTTP) Snapshot(ctx context.Context, sock, dir string) error {
	long := *h
	long.Timeout = 24 * time.Hour
	return long.putJSON(ctx, sock, "vm.snapshot", map[string]any{"destination_url": FileURL(dir)})
}

// Snapshot implements Freezer.
func (f *Fake) Snapshot(_ context.Context, sock, _ string) error { return f.record("snapshot", sock) }

// RestoreArgs renders the argv that starts Cloud Hypervisor from the
// snapshot in dir. Everything else (kernel, disks, the tap, both virtio-fs
// sockets, vsock, serial, the balloon) comes from the snapshot's own
// config.json, so the paths it names must be the guest's paths of today:
// they are, because every one of them is derived from the guest id. The
// copy restore is used, not memory_restore_mode=ondemand: on-demand paging
// keeps the image in use for the life of the guest and left the guest's
// memory fully populated after the next freeze (docs/research/guest-freeze.md).
// The VM comes up paused; the caller resumes it.
func RestoreArgs(guestDir, dir string) []string {
	return []string{
		"cloud-hypervisor",
		"--api-socket", APISocket(guestDir),
		"--restore", fmt.Sprintf("source_url=%s,memory_restore_mode=copy", FileURL(dir)),
		"--seccomp", "true",
	}
}

// Balloon is the --balloon argument a guest gets when hostd starts it with
// free page reporting (DECISIONS I-652): a balloon that never inflates
// (size=0), present so the guest kernel hands pages it frees back to the
// host. A freeze then writes what the guest uses, not everything it ever
// touched, and the guest's unit holds the same.
const Balloon = "size=0,free_page_reporting=on"
