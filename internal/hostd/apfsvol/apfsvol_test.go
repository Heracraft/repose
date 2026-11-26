package apfsvol

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/heracraft/repose/internal/hostd/shell"
)

func TestVolumeLifecycle(t *testing.T) {
	ctx := context.Background()
	v := &Volumes{Dir: t.TempDir(), R: shell.Exec{}}
	if ok, err := v.VolumeExists(ctx, "g1"); err != nil || ok {
		t.Fatalf("exists before create: %v %v", ok, err)
	}
	if err := v.CreateVolume(ctx, "g1", 64<<20); err != nil {
		t.Fatal(err)
	}
	size, used, err := v.VolumeStats(ctx, "g1")
	if err != nil || size != 64<<20 || used > 1<<20 {
		t.Fatalf("a new volume is sparse: size %d used %d %v", size, used, err)
	}
	if has, err := v.HasFilesystem(ctx, "g1"); err != nil || has {
		t.Fatalf("no fs yet: %v %v", has, err)
	}
	// A second create leaves the volume (and what is on it) alone.
	if err := os.WriteFile(v.DevPath("g1")+".probe", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(v.DevPath("g1"), os.O_WRONLY, 0)
	_, _ = f.WriteAt([]byte{0x53, 0xEF}, 1080)
	_ = f.Close()
	if err := v.CreateVolume(ctx, "g1", 1<<20); err != nil {
		t.Fatal(err)
	}
	if has, _ := v.HasFilesystem(ctx, "g1"); !has {
		t.Fatal("create replaced an existing volume")
	}
	if err := v.ExtendVolume(ctx, "g1", 128<<20); err != nil {
		t.Fatal(err)
	}
	if err := v.ExtendVolume(ctx, "g1", 1<<20); err != nil {
		t.Fatal(err)
	}
	if size, _, _ := v.VolumeStats(ctx, "g1"); size != 128<<20 {
		t.Fatalf("extend grows and never shrinks: %d", size)
	}
	if err := v.Clone(ctx, "g1", "g2"); err != nil {
		t.Fatal(err)
	}
	if has, _ := v.HasFilesystem(ctx, "g2"); !has {
		t.Fatal("the clone lacks the origin's bytes")
	}
	names, _ := v.ListVolumes(ctx)
	if len(names) != 2 || names[0] != "g1" || names[1] != "g2" {
		t.Fatalf("list %v", names)
	}
	if err := v.RemoveVolume(ctx, "g2"); err != nil {
		t.Fatal(err)
	}
	if err := v.RemoveVolume(ctx, "g2"); err != nil {
		t.Fatal("removing an absent volume:", err)
	}
	st, err := v.PoolStatus(ctx)
	if err != nil || !st.Available() || st.Size == 0 {
		t.Fatalf("pool %+v %v", st, err)
	}
	if err := v.validNameCheck(); err != nil {
		t.Fatal(err)
	}
}

func (v *Volumes) validNameCheck() error {
	for _, n := range []string{"", "../x", "a/b", ".."} {
		if _, err := v.VolumeExists(context.Background(), n); err == nil {
			return os.ErrInvalid
		}
	}
	return nil
}

func TestMkfsRWAndFsckWithE2fsprogs(t *testing.T) {
	mk, err1 := exec.LookPath("mkfs.ext4")
	ck, err2 := exec.LookPath("e2fsck")
	if err1 != nil || err2 != nil {
		t.Skip("no e2fsprogs on PATH")
	}
	ctx := context.Background()
	v := &Volumes{Dir: t.TempDir(), R: shell.Exec{}, Mke2fs: mk, E2fsck: ck}
	if err := v.CreateVolume(ctx, "g1", 64<<20); err != nil {
		t.Fatal(err)
	}
	if err := v.MkfsRW(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	if has, _ := v.HasFilesystem(ctx, "g1"); !has {
		t.Fatal("no ext4 after MkfsRW")
	}
	if code, err := v.Fsck(ctx, "g1"); err != nil || code > 1 {
		t.Fatalf("fsck %d %v", code, err)
	}
	out, err := exec.Command("blkid", "-o", "value", "-s", "LABEL", v.DevPath("g1")).Output()
	if err == nil && string(out) != "repose-rw\n" {
		t.Fatalf("label %q", out)
	}
}

func TestChownOnCreateAndClone(t *testing.T) {
	var got []string
	v := &Volumes{Dir: t.TempDir(), R: shell.Exec{}, Chown: func(p string) error { got = append(got, p); return nil }}
	ctx := context.Background()
	if err := v.CreateVolume(ctx, "a", 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := v.Clone(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("chown calls %v", got)
	}
}
