//go:build hostbench

package guest

// The fork clone (DECISIONS I-701) on a real host: hostd's Manager with
// lvm.Real against the host's thin pool, every volume it names mapped to
// bench-ff-*, so it neither sees nor touches a tenant's. The source is a
// real ext4 mounted on the host with a writer running; the fake guestd's
// Freeze and Thaw run fsfreeze on it. Run as root on a host:
//
//	CGO_ENABLED=0 go test -c -tags hostbench -o zz-clone.test ./internal/hostd/guest
//	REPOSE_HOSTBENCH_VG=vg-guests ./zz-clone.test -test.run TestHostClone -test.v
//
// It removes every bench-ff-* volume it made.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
)

const benchPrefix = "bench-ff-"

// benchLVM maps every volume name to bench-ff-<name> and lists only those.
type benchLVM struct{ r *lvm.Real }

func bn(name string) string { return benchPrefix + name }

func (b benchLVM) VolumeExists(ctx context.Context, n string) (bool, error) {
	return b.r.VolumeExists(ctx, bn(n))
}
func (b benchLVM) CreateVolume(ctx context.Context, n string, sz uint64) error {
	return b.r.CreateVolume(ctx, bn(n), sz)
}
func (b benchLVM) HasFilesystem(ctx context.Context, n string) (bool, error) {
	return b.r.HasFilesystem(ctx, bn(n))
}
func (b benchLVM) Mkfs(ctx context.Context, n string) error      { return b.r.Mkfs(ctx, bn(n)) }
func (b benchLVM) MkfsShare(ctx context.Context, n string) error { return b.r.MkfsShare(ctx, bn(n)) }
func (b benchLVM) RemoveVolume(ctx context.Context, n string) error {
	return b.r.RemoveVolume(ctx, bn(n))
}
func (b benchLVM) ExtendVolume(ctx context.Context, n string, sz uint64) error {
	return b.r.ExtendVolume(ctx, bn(n), sz)
}
func (b benchLVM) Snapshot(ctx context.Context, n, s string) error {
	return b.r.Snapshot(ctx, bn(n), bn(s))
}
func (b benchLVM) Clone(ctx context.Context, o, n string) error { return b.r.Clone(ctx, bn(o), bn(n)) }
func (b benchLVM) Activate(ctx context.Context, n string) error { return b.r.Activate(ctx, bn(n)) }
func (b benchLVM) VolumeStats(ctx context.Context, n string) (uint64, uint64, error) {
	return b.r.VolumeStats(ctx, bn(n))
}
func (b benchLVM) PoolStats(ctx context.Context) (uint64, uint64, error) { return b.r.PoolStats(ctx) }
func (b benchLVM) PoolMetadataPercent(ctx context.Context) (float64, error) {
	return b.r.PoolMetadataPercent(ctx)
}
func (b benchLVM) PoolStatus(ctx context.Context) (lvm.PoolState, error) { return b.r.PoolStatus(ctx) }
func (b benchLVM) VolumeActive(ctx context.Context, n string) (bool, error) {
	return b.r.VolumeActive(ctx, bn(n))
}
func (b benchLVM) Fsck(ctx context.Context, n string) (int, error) { return b.r.Fsck(ctx, bn(n)) }
func (b benchLVM) DevPath(n string) string                         { return b.r.DevPath(bn(n)) }
func (b benchLVM) ListVolumes(ctx context.Context) ([]string, error) {
	all, err := b.r.ListVolumes(ctx)
	var out []string
	for _, v := range all {
		if n, ok := strings.CutPrefix(v, benchPrefix); ok {
			out = append(out, n)
		}
	}
	return out, err
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func TestHostClone(t *testing.T) {
	vg := os.Getenv("REPOSE_HOSTBENCH_VG")
	if vg == "" {
		t.Skip("REPOSE_HOSTBENCH_VG not set")
	}
	real := &lvm.Real{VG: vg, Pool: "thin", R: shell.Exec{}, Sandbox: true}
	b := benchLVM{r: real}
	cleanup := func() {
		_ = exec.Command("fsfreeze", "-u", "/root/ff-src").Run()
		_ = exec.Command("umount", "/root/ff-src").Run()
		_ = exec.Command("umount", "/root/ff-chk").Run()
		vols, _ := b.ListVolumes(context.Background())
		for _, v := range vols {
			_ = exec.Command("lvremove", "-fy", vg+"/"+bn(v)).Run()
		}
		_ = os.RemoveAll("/root/ff-src")
		_ = os.RemoveAll("/root/ff-chk")
	}
	cleanup()
	t.Cleanup(cleanup)
	realLVM = b
	defer func() { realLVM = nil }()

	h := newHarness(t, func(c *Config) { c.MaxOps = 2 })
	src := filepath.Join("/root", "ff-src")
	var frozenSums atomic.Value
	var stopWriter atomic.Bool
	h.gopts.FreezeWatchdog = 10 * time.Second
	h.gopts.OnFreeze = func() error {
		if out, err := exec.Command("fsfreeze", "-f", src).CombinedOutput(); err != nil {
			return fmt.Errorf("fsfreeze -f: %v %s", err, out)
		}
		// What the copies must hold: every file as it is while frozen.
		out, err := exec.Command("sh", "-c", "cd "+src+" && find . -type f ! -name lost+found -print0 | sort -z | xargs -0 sha256sum").Output()
		if err != nil {
			return err
		}
		frozenSums.Store(string(out))
		return nil
	}
	h.gopts.OnThaw = func() error {
		if out, err := exec.Command("fsfreeze", "-u", src).CombinedOutput(); err != nil {
			return fmt.Errorf("fsfreeze -u: %v %s", err, out)
		}
		return nil
	}

	// The source: created through hostd (lvcreate and mkfs, real), then
	// mounted here, filled, and written to all through the fork.
	t0 := time.Now()
	h.create(gid1)
	t.Logf("source created in %s", time.Since(t0).Round(time.Millisecond))
	sh(t, "mkdir", "-p", src, "/root/ff-chk")
	sh(t, "mount", b.DevPath(VolumeName(gid1)), src)
	sh(t, "sh", "-c", "head -c 2G /dev/urandom > "+src+"/big.bin && mkdir -p "+src+"/tree && cp -r /etc/ssl "+src+"/tree/ && sync")
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; !stopWriter.Load(); i++ {
			f, err := os.OpenFile(filepath.Join(src, "live.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				_, _ = fmt.Fprintf(f, "line %d %s\n", i, strings.Repeat("x", 4000))
				_ = f.Close()
			}
			_ = os.WriteFile(filepath.Join(src, fmt.Sprintf("tree/f-%d", i%500)), bytes.Repeat([]byte{byte(i)}, 8192), 0o644)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	time.Sleep(2 * time.Second)

	ids := []string{gidF1, gidF2, gidF3}
	var cmds []*hostdv1.Command
	t1 := time.Now()
	for _, id := range ids {
		r := cloneReq(h, id, ids...)
		r.VolumeBytes = 48 << 30 // one bigger than the source: grown after the clone
		c := cmd(r)
		cmds = append(cmds, c)
		h.m.Dispatch(c)
	}
	// A command for the source among the copies (the review's deadlock).
	sec := cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "TOKEN", Value: []byte("t")}}})
	h.m.Dispatch(sec)
	for _, c := range append(cmds, sec) {
		deadline := time.Now().Add(2 * time.Minute)
		for h.rec.resultFor(c.CommandId) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("%s never finished", c.CommandId)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if r := h.rec.resultFor(c.CommandId); !r.Ok {
			t.Fatalf("%s: %v", c.CommandId, r)
		}
	}
	t.Logf("3 copies restored in %s", time.Since(t1).Round(time.Millisecond))
	stopWriter.Store(true)
	<-writerDone
	sh(t, "umount", src)

	if n := strings.Count(strings.Join(h.guestd(gid1).Kinds(), ","), "Freeze"); n != 1 {
		t.Fatalf("source frozen %d times", n)
	}
	if ok, _ := b.VolumeExists(context.Background(), ForkPointName(forkPoint)); ok {
		t.Fatal("the fork point outlived the fork")
	}
	t.Log(sh(t, "lvs", "-o", "lv_name,lv_attr,lv_size,origin,data_percent", vg, "-S", "lv_name=~^"+benchPrefix))
	want, _ := frozenSums.Load().(string)
	if want == "" {
		t.Fatal("no sums taken at the freeze")
	}

	// Each copy: started through hostd, then mounted read-write as the
	// guest's own mount would (the journal replayed), holding the files
	// as they were at the freeze, and e2fsck clean after.
	for _, id := range ids {
		h.mustOK(cmd(&hostdv1.StartGuest{GuestId: id}))
		h.waitState(id, StateRunning)
		h.mustOK(cmd(&hostdv1.StopGuest{GuestId: id}))
		dev := b.DevPath(VolumeName(id))
		attr := strings.TrimSpace(sh(t, "lvs", "--noheadings", "-o", "lv_attr", dev))
		if attr[4] != 'a' || strings.HasSuffix(attr, "k") {
			t.Fatalf("%s attr %q: want active, no activation skip", id, attr)
		}
		sh(t, "mount", dev, "/root/ff-chk")
		got := sh(t, "sh", "-c", "cd /root/ff-chk && find . -type f ! -name lost+found -print0 | sort -z | xargs -0 sha256sum")
		size := strings.TrimSpace(sh(t, "sh", "-c", "df -h --output=size /root/ff-chk | tail -1"))
		sh(t, "umount", "/root/ff-chk")
		if got != want {
			t.Fatalf("%s does not hold the source as frozen (%d vs %d bytes of sums)", id, len(got), len(want))
		}
		fsck := exec.Command("e2fsck", "-fn", dev)
		out, err := fsck.CombinedOutput()
		if err != nil {
			t.Fatalf("%s e2fsck -fn: %v\n%s", id, err, out)
		}
		t.Logf("%s: files as frozen (%d files), mounted with its journal replayed, e2fsck -fn clean, fs %s", id, strings.Count(want, "\n"), size)
	}
	for _, l := range strings.Split(h.logs.String(), "\n") {
		if strings.Contains(l, `"restore_done"`) || strings.Contains(l, `"fork_point"`) {
			t.Log(l)
		}
	}
}
