// Package hostfiles brings an appliance or an installer-made x86 host the
// host files it was set up without (DECISIONS I-1638). hostd's own update
// replaces only its binary (images/appliance-arm64/host/bin/
// repose-hostd-update), and a Mac's image is never replaced in place
// (internal/cli/appliance.go fetches one only for a new install), so the
// zram swap (I-1626), the snapshot store volume (I-1636) and the
// --owned-host flag reach a host set up before them only through hostd.
//
// Ensure runs once at hostd's start, before anything uses the snapshot
// directory, and only where /etc/repose/appliance-version (written by the
// image's build.sh) or the installer's header in /etc/repose/hostd.env
// says the host is one of these; a fleet host has neither and nothing is
// written. Every write is a temporary file renamed into place, so a hostd
// killed at any point leaves each file old or new, never half, and the
// next start finishes the rest: files, then units, then hostd.env last.
package hostfiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	appliance "github.com/heracraft/repose/images/appliance-arm64"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// ApplianceVersion marks the Mac appliance (build.sh writes it).
const ApplianceVersion = "/etc/repose/appliance-version"

// HostdEnv is hostd's flags on both kinds of host.
const HostdEnv = "/etc/repose/hostd.env"

// InstallerHeader is the first line ops/install-host-x86.sh writes into
// hostd.env.
const InstallerHeader = "# hostd's flags on an x86 host (ops/install-host-x86.sh"

// OwnedFlag is the flag a host its user owns runs with (I-1626).
const OwnedFlag = "--owned-host"

// file is one host file: where the image keeps it, where it goes, its mode.
type file struct {
	src, dst string
	mode     os.FileMode
}

var files = []file{
	{"host/bin/repose-zram", "/usr/local/lib/repose/repose-zram", 0o755},
	{"host/bin/repose-snapshots-volume", "/usr/local/lib/repose/repose-snapshots-volume", 0o755},
	{"host/systemd/repose-zram.service", "/etc/systemd/system/repose-zram.service", 0o644},
	{"host/systemd/repose-snapshots-volume.service", "/etc/systemd/system/repose-snapshots-volume.service", 0o644},
}

// units are enabled and started once their files are in place: the
// snapshot volume moves what the root holds onto it before hostd writes
// another snapshot (hostd has started nothing yet when Ensure runs).
var units = []string{"repose-zram.service", "repose-snapshots-volume.service"}

// Ensurer is Ensure's surroundings; Root prefixes every path (tests).
type Ensurer struct {
	Root string
	R    shell.Runner
	Log  *slog.Logger
	// Files is the embedded tree; nil is appliance.HostFiles.
	Files fs.FS
}

// Result says what Ensure did. Restart is set when hostd.env changed:
// the running hostd was started without the new flag, so it exits and
// systemd starts it again (Restart=always; KillMode=process keeps the
// guests).
type Result struct {
	Eligible bool
	Written  []string
	Restart  bool
}

func (e *Ensurer) path(p string) string { return filepath.Join(e.Root, p) }

// Eligible reports whether this host is an appliance or an installer
// host, and so one Ensure may write to.
func (e *Ensurer) Eligible() bool {
	if _, err := os.Stat(e.path(ApplianceVersion)); err == nil {
		return true
	}
	b, err := os.ReadFile(e.path(HostdEnv))
	return err == nil && bytes.HasPrefix(b, []byte(InstallerHeader))
}

// Ensure writes the missing or changed host files, enables and starts
// their units, and adds OwnedFlag to hostd.env, in that order. A failed
// step stops the ones after it, so hostd.env never names a flag whose
// host files are not in place and running, and the next start tries
// again, a unit that is enabled but not active included.
func (e *Ensurer) Ensure(ctx context.Context) (Result, error) {
	var res Result
	if !e.Eligible() {
		return res, nil
	}
	res.Eligible = true
	src := e.Files
	if src == nil {
		src = appliance.HostFiles
	}
	changedUnits := false
	for _, f := range files {
		want, err := fs.ReadFile(src, f.src)
		if err != nil {
			return res, fmt.Errorf("host file %s: %w", f.src, err)
		}
		dst := e.path(f.dst)
		if have, err := os.ReadFile(dst); err == nil && bytes.Equal(have, want) {
			if st, err := os.Stat(dst); err == nil && st.Mode().Perm() == f.mode {
				continue
			}
		}
		if err := writeAtomic(dst, want, f.mode); err != nil {
			return res, fmt.Errorf("host file %s: %w", f.dst, err)
		}
		res.Written = append(res.Written, f.dst)
		if strings.HasSuffix(f.dst, ".service") {
			changedUnits = true
		}
		e.Log.Info("host file written", "event", "host_file", "part", filepath.Base(f.dst), "bytes", len(want))
	}
	if changedUnits {
		if err := e.run(ctx, "systemctl", "daemon-reload"); err != nil {
			return res, err
		}
	}
	// A unit is done when it is enabled and active: `enable --now` leaves
	// a unit whose start failed enabled, and on the lab's appliance the
	// next start took that for done and wrote the flag (2026-11-26).
	// `start` runs a failed oneshot again.
	for _, u := range units {
		if !changedUnits && e.enabled(ctx, u) && e.active(ctx, u) {
			continue
		}
		if err := e.run(ctx, "systemctl", "enable", "--now", u); err != nil {
			return res, err
		}
		e.Log.Info("host unit started", "event", "host_file", "unit", u)
	}
	changed, err := e.ownedFlag()
	if err != nil {
		return res, err
	}
	if changed {
		res.Written = append(res.Written, HostdEnv)
		res.Restart = true
		e.Log.Info("host file written", "event", "host_file", "part", filepath.Base(HostdEnv), "flag", OwnedFlag)
	}
	return res, nil
}

func (e *Ensurer) run(ctx context.Context, argv ...string) error {
	if _, err := e.R.Run(ctx, argv...); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// enabled reports whether a unit is enabled; an error reads as no.
func (e *Ensurer) enabled(ctx context.Context, unit string) bool {
	r, err := e.R.Run(ctx, "systemctl", "is-enabled", unit)
	return err == nil && strings.TrimSpace(string(r.Stdout)) == "enabled"
}

// active reports whether a unit is active (a oneshot with
// RemainAfterExit that succeeded); failed, inactive or an error read as no.
func (e *Ensurer) active(ctx context.Context, unit string) bool {
	r, err := e.R.Run(ctx, "systemctl", "is-active", unit)
	return err == nil && strings.TrimSpace(string(r.Stdout)) == "active"
}

// ownedFlag puts OwnedFlag at the front of hostd.env's HOSTD_ARGS line,
// once; every other byte of the file stays as it was.
func (e *Ensurer) ownedFlag() (bool, error) {
	p := e.path(HostdEnv)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for i, l := range lines {
		rest, ok := strings.CutPrefix(l, "HOSTD_ARGS=")
		if !ok {
			continue
		}
		found = true
		for _, a := range strings.Fields(rest) {
			if a == OwnedFlag {
				return false, nil
			}
		}
		lines[i] = "HOSTD_ARGS=" + strings.TrimSpace(OwnedFlag+" "+rest)
		break
	}
	if !found {
		lines = append(lines[:len(lines)-1], "HOSTD_ARGS="+OwnedFlag, lines[len(lines)-1])
	}
	st, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(p, []byte(strings.Join(lines, "\n")), st.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// writeAtomic writes data beside path and renames it over path, synced,
// so a reader or a crash sees the old file or the new one.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // gone after the rename; left only by a failure
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
