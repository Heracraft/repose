package lvm

import (
	"context"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

func TestRealRendersDocumentedCommands(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"lvs", "vg-guests/g-1"}, Result: shell.Result{ExitCode: 5}},
		{Prefix: []string{"lvs", "vg-guests/snap-1"}, Result: shell.Result{ExitCode: 5}},
		{Prefix: []string{"blkid"}, Result: shell.Result{ExitCode: 2}},
		{Prefix: []string{"lvs", "--noheadings"}, Result: shell.Result{Stdout: []byte("  42949672960|12.50\n")}},
	}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	ctx := context.Background()
	if err := l.CreateVolume(ctx, "g-1", 42949672960); err != nil {
		t.Fatal(err)
	}
	if err := l.Mkfs(ctx, "g-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Snapshot(ctx, "g-1", "snap-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.ExtendVolume(ctx, "g-1", 85899345920); err != nil {
		t.Fatal(err)
	}
	size, used, err := l.VolumeStats(ctx, "g-1")
	if err != nil || size != 42949672960 || used != 5368709120 {
		t.Fatalf("stats: %d %d %v", size, used, err)
	}
	want := []string{
		"lvcreate -V 42949672960b -T vg-guests/thin -n g-1",
		"mkfs.ext4 -q -L guest -E lazy_itable_init=1 /dev/vg-guests/g-1",
		"lvcreate -s -n snap-1 vg-guests/g-1",
		"lvextend -L 85899345920b vg-guests/g-1",
	}
	var got []string
	for _, c := range r.Calls {
		s := strings.Join(c, " ")
		for _, w := range want {
			if s == w {
				got = append(got, s)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("expected commands %v, matched %v in %v", want, got, r.Calls)
	}
}

func TestFakeLifecycle(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	_ = f.CreateVolume(ctx, "g-1", 10)
	_ = f.Mkfs(ctx, "g-1")
	f.SetData("g-1", []byte("hello"))
	if err := f.Snapshot(ctx, "g-1", "s"); err != nil {
		t.Fatal(err)
	}
	if string(f.GetData("s")) != "hello" {
		t.Fatal("snapshot did not copy data")
	}
	if err := f.RemoveVolume(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.VolumeExists(ctx, "s"); ok {
		t.Fatal("snapshot still exists")
	}
}

// PoolMetadataPercent reads the pool's metadata_percent (DECISIONS I-586).
func TestRealPoolMetadataPercent(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"lvs", "--noheadings"}, Result: shell.Result{Stdout: []byte("  12.34\n")}},
	}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	got, err := l.PoolMetadataPercent(context.Background())
	if err != nil || got != 12.34 {
		t.Fatalf("metadata %v %v", got, err)
	}
	if c := strings.Join(r.Calls[0], " "); c != "lvs --noheadings --units b --nosuffix --separator | -o metadata_percent vg-guests/thin" {
		t.Fatalf("command %q", c)
	}
}

// With Sandbox set, e2fsck runs in shell.Sandboxed and its exit code still
// reaches the caller: restore reads 1 as "fixed" and 4 as "left errors".
func TestFsckSandboxed(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"systemd-run"}, Result: shell.Result{ExitCode: 1}}}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r, Sandbox: true}
	code, err := l.Fsck(context.Background(), "g-1")
	if err != nil || code != 1 {
		t.Fatalf("fsck: %d %v", code, err)
	}
	argv := strings.Join(r.Calls[0], " ")
	if !strings.HasPrefix(argv, "systemd-run ") || !strings.Contains(argv, "DynamicUser=yes") ||
		!strings.Contains(argv, "DeviceAllow=/dev/vg-guests/g-1 rw") || !strings.HasSuffix(argv, "-- e2fsck -fp /dev/vg-guests/g-1") {
		t.Fatalf("fsck argv %s", argv)
	}
}

// With Sandbox set, blkid runs in shell.Sandboxed with read-only access to
// the one volume, and its "no filesystem" exit (2) still reads as false.
func TestHasFilesystemSandboxed(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"systemd-run"}, Result: shell.Result{ExitCode: 2}}}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r, Sandbox: true}
	has, err := l.HasFilesystem(context.Background(), "g-1")
	if err != nil || has {
		t.Fatalf("has %v err %v", has, err)
	}
	argv := strings.Join(r.Calls[0], " ")
	if !strings.HasPrefix(argv, "systemd-run ") || !strings.Contains(argv, "DeviceAllow=/dev/vg-guests/g-1 r") ||
		!strings.HasSuffix(argv, "-- blkid -s TYPE -o value /dev/vg-guests/g-1") {
		t.Fatalf("blkid argv %s", argv)
	}
}
