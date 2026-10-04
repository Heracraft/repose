package mount

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

const sample = `22 1 259:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
98 22 253:7 / /var/lib/repose/users/u-1/claude-auth rw,nosuid,nodev,noexec shared:40 - ext4 /dev/mapper/vg--guests-auth--u--1 rw
99 22 253:8 / /var/lib/repose/users/with\040space/claude-auth rw shared:41 - ext4 /dev/dm-8 rw
`

func TestMountedIn(t *testing.T) {
	for path, want := range map[string]bool{
		"/var/lib/repose/users/u-1/claude-auth":        true,
		"/var/lib/repose/users/with space/claude-auth": true,
		"/var/lib/repose/users/u-2/claude-auth":        false,
		"/var/lib/repose/users/u-1":                    false,
	} {
		if got := mountedIn([]byte(sample), path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

func TestRealCommands(t *testing.T) {
	info := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(info, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &shell.Fake{}
	m := &Real{R: r, MountInfo: info}
	ctx := context.Background()
	if err := m.Mount(ctx, "/dev/vg-guests/auth-u-1", "/var/lib/repose/users/u-1/claude-auth"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unmount(ctx, "/var/lib/repose/users/u-1/claude-auth"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unmount(ctx, "/var/lib/repose/users/u-2/claude-auth"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range r.Calls {
		got = append(got, strings.Join(c, " "))
	}
	want := []string{
		"mount -t ext4 -o nosuid,nodev,noexec /dev/vg-guests/auth-u-1 /var/lib/repose/users/u-1/claude-auth",
		"umount /var/lib/repose/users/u-1/claude-auth",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n%s", strings.Join(got, "\n"))
	}
}
