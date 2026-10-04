// Package mount mounts and unmounts the small volumes hostd keeps outside
// guests: the per-user Claude login share (DECISIONS I-464). Mounts are in
// the host namespace, so a virtiofsd started after the mount serves the
// volume's root.
package mount

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Mounter is what the guest manager needs.
type Mounter interface {
	// IsMounted reports whether path is a mount point.
	IsMounted(path string) (bool, error)
	// Mount mounts the ext4 on dev at path with nosuid,nodev,noexec.
	Mount(ctx context.Context, dev, path string) error
	// Unmount unmounts path; a path that is not mounted is not an error.
	Unmount(ctx context.Context, path string) error
}

// Real runs mount and umount and reads /proc/self/mountinfo.
type Real struct {
	R         shell.Runner
	MountInfo string // /proc/self/mountinfo; tests point it elsewhere
}

// IsMounted implements Mounter.
func (m *Real) IsMounted(path string) (bool, error) {
	info := m.MountInfo
	if info == "" {
		info = "/proc/self/mountinfo"
	}
	b, err := os.ReadFile(info)
	if err != nil {
		return false, err
	}
	return mountedIn(b, filepath.Clean(path)), nil
}

// mountedIn reports whether mountinfo lists path as a mount point. Field
// 5 is the mount point, with space, tab, newline and backslash octal-escaped.
func mountedIn(mountinfo []byte, path string) bool {
	sc := bufio.NewScanner(bytes.NewReader(mountinfo))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 4 && unescape(f[4]) == path {
			return true
		}
	}
	return false
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Mount implements Mounter.
func (m *Real) Mount(ctx context.Context, dev, path string) error {
	_, err := m.R.Run(ctx, "mount", "-t", "ext4", "-o", "nosuid,nodev,noexec", dev, path)
	return err
}

// Unmount implements Mounter.
func (m *Real) Unmount(ctx context.Context, path string) error {
	ok, err := m.IsMounted(path)
	if err != nil || !ok {
		return err
	}
	_, err = m.R.Run(ctx, "umount", path)
	return err
}

// Fake keeps mounts in a map.
type Fake struct {
	mu     sync.Mutex
	Mounts map[string]string // path -> dev
	Err    error             // returned by Mount when set
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{Mounts: map[string]string{}} }

// IsMounted implements Mounter.
func (f *Fake) IsMounted(path string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Mounts[filepath.Clean(path)]
	return ok, nil
}

// Mount implements Mounter.
func (f *Fake) Mount(_ context.Context, dev, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Mounts[filepath.Clean(path)] = dev
	return nil
}

// Unmount implements Mounter.
func (f *Fake) Unmount(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Mounts, filepath.Clean(path))
	return nil
}
