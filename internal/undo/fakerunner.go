package undo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FakeRunner answers btrfs, blkid, mkfs.btrfs and mount from a model kept
// on a real directory: a subvolume is a directory holding a marker file,
// so the backend's renames, sidecar files and listings are the real ones
// and only btrfs is pretend. A marker inside the directory is what makes
// a renamed subvolume still a subvolume, which is how a rewind's swap
// works. It records every invocation, so a test can assert what was run
// and in what order — which is how the order of a rewind (point, then
// swap) is checked without a filesystem.
//
// Consumers of the backend (cmd/repose-agent's golden tests) use it the
// same way: undo.New(dir, &undo.FakeRunner{Usage: ...}).
type FakeRunner struct {
	mu sync.Mutex
	// Calls is every argv, in order.
	Calls [][]string
	// Usage is what `btrfs filesystem usage --raw` reports.
	Usage Space
	// Exclusive is each subvolume's exclusive bytes, keyed the way
	// `btrfs qgroup show` prints the path ("points/<slug>/<name>").
	Exclusive map[string]uint64
	// QuotasOff makes qgroup fail the way a store without quotas does.
	QuotasOff bool
	// Created is the creation time `btrfs subvolume show` reports.
	Created time.Time
	// FailOn fails any invocation whose argv contains the key, with the
	// value as stderr. One key at a time: the order of several is the
	// map's.
	FailOn map[string]string
	// FreedPerDelete is what a subvolume delete takes off Usage.Used, so
	// a test can watch a GC loop come under its high-water mark.
	FreedPerDelete uint64

	formatted map[string]bool
}

// fakeMarker names a directory the model holds as a subvolume. It travels
// with a rename, as a subvolume does.
const fakeMarker = ".fake-subvolume"

func fakeIsSubvol(path string) bool {
	_, err := os.Stat(filepath.Join(path, fakeMarker))
	return err == nil
}

func fakeMakeSubvol(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, fakeMarker), nil, 0o644)
}

// Run implements Runner.
func (f *FakeRunner) Run(_ context.Context, argv ...string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(argv) == 0 {
		return Result{}, errors.New("undo: empty argv")
	}
	f.Calls = append(f.Calls, append([]string(nil), argv...))
	joined := strings.Join(argv, " ")
	for key, stderr := range f.FailOn {
		if strings.Contains(joined, key) {
			return f.exit(argv, stderr)
		}
	}
	if argv[0] != "btrfs" {
		// mkfs.btrfs, blkid and mount do nothing a test needs beyond
		// being recorded; blkid finds no filesystem, as it does on a
		// fresh image (exit 2).
		switch argv[0] {
		case "blkid":
			if f.formatted[last(argv)] {
				return Result{Stdout: []byte("btrfs\n")}, nil
			}
			return Result{ExitCode: 2}, &ExitError{Argv: argv, Result: Result{ExitCode: 2}}
		case "mkfs.btrfs":
			if f.formatted == nil {
				f.formatted = map[string]bool{}
			}
			f.formatted[last(argv)] = true
		}
		return Result{}, nil
	}
	rest := argv[1:]
	switch {
	case has(rest, "subvolume", "show"):
		return f.show(argv, last(rest))
	case has(rest, "subvolume", "create"):
		if err := fakeMakeSubvol(last(rest)); err != nil {
			return f.exit(argv, "ERROR: cannot create subvolume: "+err.Error())
		}
		return Result{}, nil
	case has(rest, "subvolume", "snapshot"):
		return f.snapshot(argv, rest)
	case has(rest, "subvolume", "delete"):
		p := last(rest)
		if !fakeIsSubvol(p) {
			return f.exit(argv, "ERROR: Could not statfs: No such file or directory")
		}
		if err := os.RemoveAll(p); err != nil {
			return f.exit(argv, "ERROR: cannot delete: "+err.Error())
		}
		if f.FreedPerDelete > f.Usage.Used {
			f.Usage.Used = 0
		} else {
			f.Usage.Used -= f.FreedPerDelete
		}
		f.Usage.Free += f.FreedPerDelete
		return Result{}, nil
	case has(rest, "filesystem", "usage"):
		return Result{Stdout: []byte(fmt.Sprintf("Overall:\n"+
			"    Device size:\t\t%d\n"+
			"    Device allocated:\t\t%d\n"+
			"    Used:\t\t\t%d\n"+
			"    Free (estimated):\t\t%d\t(min: %d)\n",
			f.Usage.Total, f.Usage.Used, f.Usage.Used, f.Usage.Free, f.Usage.Free/2))}, nil
	case has(rest, "qgroup", "show"):
		if f.QuotasOff {
			return f.exit(argv, "ERROR: can't list qgroups: quotas not enabled")
		}
		var sb strings.Builder
		sb.WriteString("Qgroupid    Referenced    Exclusive   Path \n--------    ----------    ---------   ---- \n")
		i := 0
		for p, n := range f.Exclusive {
			i++
			fmt.Fprintf(&sb, "0/%d %d %d %s\n", 256+i, n, n, p)
		}
		return Result{Stdout: []byte(sb.String())}, nil
	}
	return Result{}, nil
}

// Subvol is the model's subvolume check, for Btrfs.Subvol: the real one
// is a stat for inode 256, which a directory under a test's temp
// directory cannot be.
func (f *FakeRunner) Subvol(path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeIsSubvol(path), nil
}

// Mark makes path a subvolume of the model without a call, for a test
// setting up a store that already has one.
func (f *FakeRunner) Mark(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeMakeSubvol(path)
}

func (f *FakeRunner) show(argv []string, p string) (Result, error) {
	if !fakeIsSubvol(p) {
		if _, err := os.Stat(p); err == nil {
			return f.exit(argv, "ERROR: Not a Btrfs subvolume: Invalid argument")
		}
		return f.exit(argv, "ERROR: cannot find real path for '"+p+"': No such file or directory")
	}
	t := f.Created
	if t.IsZero() {
		t = time.Unix(0, 0).UTC()
	}
	out := fmt.Sprintf("%s\n\tName: \t\t\t%s\n\tCreation time: \t\t%s\n\tSubvolume ID: \t\t257\n",
		p, filepath.Base(p), t.Format("2006-01-02 15:04:05 -0700"))
	return Result{Stdout: []byte(out)}, nil
}

func (f *FakeRunner) snapshot(argv, rest []string) (Result, error) {
	paths := rest[2:]
	if len(paths) > 0 && paths[0] == "-r" {
		paths = paths[1:]
	}
	if len(paths) != 2 {
		return f.exit(argv, "ERROR: wrong number of arguments")
	}
	src, dest := paths[0], paths[1]
	if !fakeIsSubvol(src) {
		return f.exit(argv, "ERROR: cannot find real path for '"+src+"': No such file or directory")
	}
	if fakeIsSubvol(dest) {
		// The real one tries to snapshot into the existing subvolume and
		// fails on its read-only flag, which is why the backend checks
		// for the point before it takes one.
		return f.exit(argv, "ERROR: cannot snapshot '"+src+"': Read-only file system")
	}
	if err := copyTree(src, dest); err != nil {
		return f.exit(argv, "ERROR: cannot snapshot: "+err.Error())
	}
	if err := fakeMakeSubvol(dest); err != nil {
		return f.exit(argv, "ERROR: cannot snapshot: "+err.Error())
	}
	return Result{}, nil
}

func (f *FakeRunner) exit(argv []string, stderr string) (Result, error) {
	res := Result{Stderr: []byte(stderr + "\n"), ExitCode: 1}
	return res, &ExitError{Argv: argv, Result: res}
}

// has reports whether argv starts with the given words.
func has(argv []string, words ...string) bool {
	if len(argv) < len(words) {
		return false
	}
	for i, w := range words {
		if argv[i] != w {
			return false
		}
	}
	return true
}

func last(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[len(argv)-1]
}

// copyTree copies src to dest, which is what a snapshot looks like from
// above when the blocks are not shared.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		buf, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, buf, 0o644)
	})
}

var _ Runner = (*FakeRunner)(nil)
