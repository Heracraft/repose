// Package storeview gives each guest a store that holds only its own
// closure (DECISIONS I-463). The guest's virtiofsd@<id> unit starts with a
// private mount namespace whose shared directory is an empty tmpfs, and
// virtiofsd pivots into it; hostd then bind-mounts each path of the
// guest's system closures into that namespace (the one it runs and the
// ones it ran before, see guest.viewClosures), read-only and with private
// propagation. Nothing is mounted in the host's namespace, so systemd
// tracks none of it, and a guest can list and read its own closures and
// nothing else: not other projects' closures or fragment sources, not the
// host's system.
//
// An in-place switch adds the new closure's paths before the guest
// switches; paths are never removed while the guest runs (processes may
// still use the old ones), and the view starts empty at the next boot.
package storeview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/hostd/systemd"
)

// Dir is the shared directory of every guest's virtiofsd: a tmpfs in that
// unit's own mount namespace, so all units use the same path.
const Dir = "/run/repose/store-view"

// UnitProps are the properties virtiofsd@<id> needs for a view: its own
// mount namespace and an empty, root-owned tmpfs at Dir. virtiofsd's user
// cannot write to it; hostd fills it.
func UnitProps() []string {
	return []string{"PrivateMounts=yes", "TemporaryFileSystem=" + Dir + ":mode=0755,nosuid,nodev,size=16m"}
}

// View is what the guest manager needs.
type View interface {
	// Populate makes each store path appear in the view served by unit.
	// Paths already there are left alone.
	Populate(ctx context.Context, unit string, paths []string) error
	// Serves says what unit's running virtiofsd shares: ServesView,
	// ServesWhole (the pre-I-463 export), or "" when it is not running or
	// has not entered its shared directory yet.
	Serves(ctx context.Context, unit string) (string, error)
}

// What a running virtiofsd shares.
const (
	ServesView  = "view"
	ServesWhole = "whole-store"
)

// Real binds into the namespace of the unit's main process.
type Real struct {
	SD systemd.Systemd
	// Wait bounds how long Populate waits for virtiofsd to pivot into its
	// view; zero means 10 s.
	Wait time.Duration
	// Proc is /proc; tests point it elsewhere.
	Proc string
}

// ErrNotPivoted is returned when virtiofsd did not enter its view in time.
var ErrNotPivoted = errors.New("virtiofsd did not enter its store view")

// Serves implements View.
func (r *Real) Serves(ctx context.Context, unit string) (string, error) {
	proc := r.Proc
	if proc == "" {
		proc = "/proc"
	}
	props, err := r.SD.Show(ctx, unit, "MainPID")
	if err != nil {
		return "", err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(props["MainPID"]))
	if err != nil || pid <= 0 {
		return "", nil
	}
	switch root, err := rootOf(proc, pid); {
	case err != nil:
		return "", err
	case root == rootView:
		return ServesView, nil
	case root == rootOther:
		return ServesWhole, nil
	}
	return "", nil
}

// Populate implements View. A unit started before I-463 serves the whole
// store export; it is left as it is until the guest's next boot.
func (r *Real) Populate(ctx context.Context, unit string, paths []string) error {
	proc := r.Proc
	if proc == "" {
		proc = "/proc"
	}
	wait := r.Wait
	if wait == 0 {
		wait = 10 * time.Second
	}
	props, err := r.SD.Show(ctx, unit, "MainPID")
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(props["MainPID"]))
	if err != nil || pid <= 0 {
		return fmt.Errorf("%s has no main process", unit)
	}
	deadline := time.Now().Add(wait)
	for {
		root, err := rootOf(proc, pid)
		if err != nil {
			return err
		}
		switch root {
		case rootView:
			return Bind(pid, paths)
		case rootOther:
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNotPivoted
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type rootKind int

const (
	rootHost  rootKind = iota // not pivoted yet
	rootView                  // a tmpfs: the store view
	rootOther                 // pivoted into something else: the pre-I-463 export
)

func rootOf(proc string, pid int) (rootKind, error) {
	p := filepath.Join(proc, strconv.Itoa(pid), "root")
	var st, host unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		return 0, fmt.Errorf("virtiofsd root: %w", err)
	}
	if err := unix.Stat("/", &host); err != nil {
		return 0, err
	}
	if st.Dev == host.Dev && st.Ino == host.Ino {
		return rootHost, nil
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(p, &fs); err != nil {
		return 0, err
	}
	if fs.Type == unix.TMPFS_MAGIC {
		return rootView, nil
	}
	return rootOther, nil
}

// storePathRe is a top-level store path: no further slashes, no dot names.
var storePathRe = regexp.MustCompile(`^/nix/store/[0-9a-df-np-sv-z]{32}-[A-Za-z0-9+._?=-]+$`)

// storeName returns the entry name of a top-level store path.
func storeName(p string) (string, bool) {
	if !storePathRe.MatchString(p) {
		return "", false
	}
	return filepath.Base(p), true
}

// Fake records what each unit's view holds.
type Fake struct {
	mu    sync.Mutex
	Views map[string]map[string]bool // unit -> store path -> present
	Err   error
	Whole map[string]bool // units that serve the whole store
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{Views: map[string]map[string]bool{}} }

// Populate implements View.
func (f *Fake) Populate(_ context.Context, unit string, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	v := f.Views[unit]
	if v == nil {
		v = map[string]bool{}
		f.Views[unit] = v
	}
	for _, p := range paths {
		v[p] = true
	}
	return nil
}

// Serves implements View: ServesWhole for a unit in Whole, ServesView for
// one populated, "" otherwise.
func (f *Fake) Serves(_ context.Context, unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.Whole[unit]:
		return ServesWhole, nil
	case f.Views[unit] != nil:
		return ServesView, nil
	}
	return "", nil
}

// Has reports whether unit's view holds p.
func (f *Fake) Has(unit, p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Views[unit][p]
}

// Reset forgets a unit's view, as a new virtiofsd starts with an empty one.
func (f *Fake) Reset(unit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Views, unit)
}
