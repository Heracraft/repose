// Package fs implements the GrowFs request of
// docs/interfaces/vsock-guestd.md: grow the root ext4 online after hostd has
// extended the thin volume underneath it.
package fs

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"golang.org/x/sys/unix"
)

// DefaultTimeout bounds a resize2fs run.
const DefaultTimeout = 2 * time.Minute

// Handler grows the root filesystem.
type Handler struct {
	paths   sysdep.Paths
	run     sysdep.Runner
	timeout time.Duration
	log     *slog.Logger
}

// New builds the handler.
func New(p sysdep.Paths, run sysdep.Runner, timeout time.Duration, log *slog.Logger) *Handler {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Handler{paths: p, run: run, timeout: timeout, log: log}
}

// Grow runs resize2fs on the root block device and reports the new size.
// ext4 grows online, so nothing in the guest is interrupted.
func (h *Handler) Grow(ctx context.Context) (*guestdv1.GrowFsResult, error) {
	dev, err := h.rootDevice()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	res, runErr := h.run.Run(ctx, sysdep.RunSpec{
		Argv:      []string{"resize2fs", dev},
		MaxOutput: 8 << 10,
		Env:       sysdep.DevEnv(h.paths, "root"),
	})
	if runErr != nil {
		return nil, sysdep.Errf(sysdep.CodeInternal, "grow root filesystem: %w", runErr)
	}
	if res.ExitCode != 0 {
		return nil, sysdep.Errf(sysdep.CodeInternal, "grow root filesystem: resize2fs exited %d", res.ExitCode)
	}

	size, err := h.size()
	if err != nil {
		return nil, err
	}
	h.log.Info("root filesystem grown", "event", "grow_fs", "bytes", size)
	return &guestdv1.GrowFsResult{NewBytes: size}, nil
}

// rootDevice finds the block device to grow: the one mounted at / from
// /proc/mounts on a Nix guest, and on an erofs guest the one under
// /run/repose/rw from mountinfo (it is /dev/vdb), never / itself, which
// is an overlayfs (DECISIONS I-1474).
func (h *Handler) rootDevice() (string, error) {
	if h.paths.Erofs {
		return h.rwDevice()
	}
	f, err := os.Open(h.paths.Mounts())
	if err != nil {
		return "", sysdep.Errf(sysdep.CodeInternal, "read mounts: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[1] != "/" {
			continue
		}
		if !strings.HasPrefix(fields[0], "/dev/") {
			// An overlay or tmpfs root cannot be grown with resize2fs; say so
			// rather than running it against a name that is not a device.
			return "", sysdep.Invalid("grow root filesystem: root is mounted from %s, not a block device", fields[0])
		}
		return fields[0], nil
	}
	if err := sc.Err(); err != nil {
		return "", sysdep.Errf(sysdep.CodeInternal, "scan mounts: %w", err)
	}
	return "", sysdep.NotFound("grow root filesystem: no mount for /")
}

// rwDevice is the block device mounted at /run/repose/rw.
func (h *Handler) rwDevice() (string, error) {
	ms, err := sysdep.ReadMountinfo(h.paths.Mountinfo())
	if err != nil {
		return "", sysdep.Errf(sysdep.CodeInternal, "grow root filesystem: %w", err)
	}
	m, ok := sysdep.FindMount(ms, sysdep.ErofsRWMount)
	if !ok {
		return "", sysdep.NotFound("grow root filesystem: no mount for %s", sysdep.ErofsRWMount)
	}
	if !strings.HasPrefix(m.Source, "/dev/") {
		return "", sysdep.Invalid("grow root filesystem: %s is mounted from %s, not a block device", sysdep.ErofsRWMount, m.Source)
	}
	return m.Source, nil
}

// size is statfs on the root mount, in bytes.
func (h *Handler) size() (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(h.paths.RootMount(), &st); err != nil {
		return 0, sysdep.Errf(sysdep.CodeInternal, "statfs root: %w", err)
	}
	return st.Blocks * uint64(st.Bsize), nil
}
