package lvm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

func TestRealRendersDocumentedCommands(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"lvs", "vg-guests/g-1"}, Result: shell.Result{ExitCode: 5, Stderr: []byte(notFoundStderr)}},
		{Prefix: []string{"lvs", "vg-guests/snap-1"}, Result: shell.Result{ExitCode: 5, Stderr: []byte(notFoundStderr)}},
		{Prefix: []string{"blkid"}, Result: shell.Result{ExitCode: 2}},
		{Prefix: poolStatusArgv, Result: shell.Result{Stdout: []byte(activePool)}},
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

// PoolMetadataPercent reads the pool's metadata_percent (DECISIONS I-586),
// from the one lvs call PoolStatus makes (I-644).
func TestRealPoolMetadataPercent(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"lvs", "--noheadings"}, Result: shell.Result{Stdout: []byte("  active||1099511627776|28.63|12.34\n")}},
	}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	got, err := l.PoolMetadataPercent(context.Background())
	if err != nil || got != 12.34 {
		t.Fatalf("metadata %v %v", got, err)
	}
	if c := strings.Join(r.Calls[0], " "); c != "lvs --noheadings --units b --nosuffix --separator | -o lv_active,lv_health_status,lv_size,data_percent,metadata_percent vg-guests/thin" {
		t.Fatalf("command %q", c)
	}
}

const (
	notFoundStderr = "  Failed to find logical volume \"vg-guests/g-1\"\n"
	activePool     = "  active||1099511627776|28.63|1.22\n"
	// What lvs prints for host-01's pool after the 2026-10-10 reboot left
	// it inactive: a size, and no percentages.
	inactivePool = "  ||1099511627776||\n"
)

var poolStatusArgv = []string{"lvs", "--noheadings", "--units", "b", "--nosuffix", "--separator", "|", "-o", "lv_active,lv_health_status,lv_size,data_percent,metadata_percent"}

// Before I-644 an inactive pool parsed as 0 percent used, so PoolStats
// returned free == size: the scheduler saw an empty host and placed new
// machines on a host whose pool was down.
func TestPoolStatusInactive(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: poolStatusArgv, Result: shell.Result{Stdout: []byte(inactivePool)}}}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	ctx := context.Background()
	p, err := l.PoolStatus(ctx)
	if !errors.Is(err, ErrPoolUnavailable) || p.Active || p.Reason() != "inactive" {
		t.Fatalf("status %+v err %v", p, err)
	}
	if size, free, err := l.PoolStats(ctx); !errors.Is(err, ErrPoolUnavailable) || free != 0 || size != 0 {
		t.Fatalf("stats size %d free %d err %v; an inactive pool must not read as empty", size, free, err)
	}
	if _, err := l.PoolMetadataPercent(ctx); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("metadata err %v", err)
	}
}

func TestPoolStatusReadOnly(t *testing.T) {
	for _, health := range []string{"metadata_read_only", "failed"} {
		r := &shell.Fake{Scripts: []shell.Script{{Prefix: poolStatusArgv, Result: shell.Result{Stdout: []byte("  active|" + health + "|1099511627776|28.63|1.22\n")}}}}
		l := &Real{VG: "vg-guests", Pool: "thin", R: r}
		p, err := l.PoolStatus(context.Background())
		if !errors.Is(err, ErrPoolUnavailable) || p.Reason() != health {
			t.Fatalf("%s: status %+v err %v", health, p, err)
		}
	}
	// out_of_data is the PoolFull page's, not unavailable.
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: poolStatusArgv, Result: shell.Result{Stdout: []byte("  active|out_of_data|1099511627776|100.00|1.22\n")}}}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	if _, err := l.PoolStatus(context.Background()); err != nil {
		t.Fatalf("out_of_data: %v", err)
	}
}

// A missing VG is an error, not "not found": read as "no volume" it sent
// every running guest to error at reconcile and let destroy leak the
// volume (RemoveVolume returned nil).
func TestVolumeExistsMissingVGIsError(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: []string{"lvs", "vg-guests/g-1"}, Result: shell.Result{ExitCode: 5, Stderr: []byte("  Volume group \"vg-guests\" not found\n  Cannot process volume group vg-guests\n")}},
	}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	ok, err := l.VolumeExists(context.Background(), "g-1")
	if err == nil || ok {
		t.Fatalf("missing VG read as exists=%v err=%v", ok, err)
	}
	r2 := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"lvs", "vg-guests/g-1"}, Result: shell.Result{ExitCode: 5, Stderr: []byte(notFoundStderr)}}}}
	l2 := &Real{VG: "vg-guests", Pool: "thin", R: r2}
	if ok, err := l2.VolumeExists(context.Background(), "g-1"); err != nil || ok {
		t.Fatalf("not found read as exists=%v err=%v", ok, err)
	}
}

// Every mutator asks PoolStatus first and issues no lvm command against
// an inactive pool: lvm would activate the pool itself, with its own
// thin_check, behind repose-guest-pool's back.
func TestRealMutatorsRefuseInactivePool(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{
		{Prefix: poolStatusArgv, Result: shell.Result{Stdout: []byte(inactivePool)}},
		{Prefix: []string{"lvs"}, Result: shell.Result{ExitCode: 5, Stderr: []byte(notFoundStderr)}},
	}}
	l := &Real{VG: "vg-guests", Pool: "thin", R: r}
	ctx := context.Background()
	for name, op := range map[string]func() error{
		"create":   func() error { return l.CreateVolume(ctx, "g-1", 1<<30) },
		"remove":   func() error { return l.RemoveVolume(ctx, "g-1") },
		"extend":   func() error { return l.ExtendVolume(ctx, "g-1", 2<<30) },
		"snapshot": func() error { return l.Snapshot(ctx, "g-1", "snap-1") },
		"activate": func() error { return l.Activate(ctx, "g-1") },
	} {
		if err := op(); !errors.Is(err, ErrPoolUnavailable) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	for _, c := range r.Calls {
		switch c[0] {
		case "lvcreate", "lvremove", "lvchange", "lvextend", "vgchange":
			t.Fatalf("issued %v against an inactive pool", c)
		}
	}
}

func TestFakeRefusesUnavailablePool(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	_ = f.CreateVolume(ctx, "g-1", 10)
	f.PoolDown = true
	if err := f.CreateVolume(ctx, "g-2", 10); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("create %v", err)
	}
	if _, _, err := f.PoolStats(ctx); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("stats %v", err)
	}
	if ok, err := f.VolumeExists(ctx, "g-1"); err != nil || !ok {
		t.Fatalf("an inactive pool still lists its volumes: %v %v", ok, err)
	}
	f.PoolDown, f.VGMissing = false, true
	if _, err := f.VolumeExists(ctx, "g-1"); err == nil {
		t.Fatal("missing VG must be an error")
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
