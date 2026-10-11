package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/freeze"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/mount"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// The guest id is the bench directory's base name; the volume is vol.img.
func benchImage(vg string) *freeze.Image {
	r := shell.Exec{}
	return &freeze.Image{R: r, LVM: &lvm.Real{VG: vg, Pool: "thin", R: r}, Mount: &mount.Real{R: r}}
}

var classMiB = map[string]uint64{"small": 4096, "large": 8192, "xl": 16384}

func ms(t time.Time) int64 { return time.Since(t).Milliseconds() }

func hostdFreeze(ctx context.Context, dir, class, keyPEM, recDir, vg string) error {
	t0 := time.Now()
	id := "gf" + filepath.Base(dir)
	sealer := &freeze.FileKey{Path: keyPEM}
	key, err := freeze.NewKey()
	if err != nil {
		return err
	}
	defer freeze.Wipe(key)
	wrapped, kid, err := sealer.Wrap(key)
	if err != nil {
		return err
	}
	recs := freeze.Store{Dir: recDir}
	rec := &freeze.Record{GuestID: id, State: freeze.StateWriting, Class: class, KeyID: kid, WrappedKey: wrapped, KeyCheck: freeze.KeyCheck(key), CreatedAt: time.Now().UTC()}
	if err := recs.Put(rec); err != nil {
		return err
	}
	img := benchImage(vg)
	mnt := filepath.Join(dir, "freeze")
	if err := img.Create(ctx, id, (classMiB[class]+256)<<20, key, mnt, 0, 0); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	tImg := ms(t0)
	snap := filepath.Join(mnt, "snap")
	if err := os.MkdirAll(snap, 0o700); err != nil {
		return err
	}
	c := &ch.HTTP{Timeout: 10 * time.Minute}
	sock := ch.APISocket(dir)
	var fz ch.Freezer = c
	if err := c.Pause(ctx, sock); err != nil {
		return err
	}
	t1 := time.Now()
	if err := fz.Snapshot(ctx, sock, snap); err != nil {
		_ = c.Resume(ctx, sock) // the bench reports the snapshot error
		return fmt.Errorf("snapshot: %w", err)
	}
	snapMs := ms(t1)
	_ = c.Shutdown(ctx, sock) // the caller kills what is left
	fp, err := freeze.Fingerprint(filepath.Join(dir, "vol.img"))
	if err != nil {
		return err
	}
	var bytes uint64
	_ = filepath.Walk(snap, func(_ string, fi os.FileInfo, err error) error { // a file it cannot stat counts zero
		if err == nil && fi.Mode().IsRegular() {
			bytes += uint64(fi.Sys().(*syscall.Stat_t).Blocks) * 512
		}
		return nil
	})
	if err := img.Close(ctx, id, mnt); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	rec.State, rec.DoneAt, rec.Fingerprint, rec.ImageBytes = freeze.StateComplete, time.Now().UTC(), fp, bytes
	if err := recs.Put(rec); err != nil {
		return err
	}
	fmt.Printf("image_ms=%d snapshot_ms=%d total_ms=%d image_bytes=%d\n", tImg, snapMs, ms(t0), bytes)
	return nil
}

func hostdThaw(ctx context.Context, dir, keyPEM, recDir, vg string) error {
	t0 := time.Now()
	id := "gf" + filepath.Base(dir)
	recs := freeze.Store{Dir: recDir}
	rec, err := recs.Get(id)
	if err != nil || rec == nil {
		return fmt.Errorf("no record: %v", err)
	}
	if rec.State != freeze.StateComplete {
		return errors.New("image incomplete")
	}
	fp, err := freeze.Fingerprint(filepath.Join(dir, "vol.img"))
	if err != nil || fp != rec.Fingerprint {
		return fmt.Errorf("volume changed since the freeze (%v)", err)
	}
	key, err := (&freeze.FileKey{Path: keyPEM}).Unwrap(ctx, rec.WrappedKey, rec.KeyID)
	if err != nil {
		return err
	}
	defer freeze.Wipe(key)
	if freeze.KeyCheck(key) != rec.KeyCheck {
		return errors.New("key check failed")
	}
	img := benchImage(vg)
	mnt := filepath.Join(dir, "freeze")
	if err := img.Open(ctx, id, key, mnt); err != nil {
		return err
	}
	tOpen := ms(t0)
	argv := ch.RestoreArgs(dir, filepath.Join(mnt, "snap"))
	chBin := os.Getenv("CH")
	logf, err := os.OpenFile(filepath.Join(dir, "ch.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_ = os.Remove(ch.APISocket(dir))   // stale socket; absent is fine
	_ = os.Remove(ch.VsockSocket(dir)) // same
	cmd := exec.Command(chBin, argv[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, "ch.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600) // the bench script kills by it; best effort
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }() // the exit is what is waited for, not its status
	c := &ch.HTTP{}
	sock := ch.APISocket(dir)
	t1 := time.Now()
	for {
		if _, err := c.Info(ctx, sock); err == nil {
			break
		}
		select {
		case <-exited:
			return errors.New("hypervisor exited during the restore")
		case <-time.After(20 * time.Millisecond):
		}
		if time.Since(t1) > 3*time.Minute {
			return errors.New("restore did not finish in time")
		}
	}
	restoreMs := ms(t1)
	if err := img.Close(ctx, id, mnt); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := c.Resume(ctx, sock); err != nil {
		return err
	}
	if err := run(ctx, "wait", dir, []string{"30s"}); err != nil {
		return err
	}
	if err := run(ctx, "settime", dir, nil); err != nil {
		return err
	}
	if err := img.Remove(ctx, id, mnt); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	if err := recs.Delete(id); err != nil {
		return err
	}
	_ = cmd.Process.Release() // the hypervisor outlives the bench
	fmt.Printf("open_ms=%d restore_ms=%d total_ms=%d\n", tOpen, restoreMs, ms(t0))
	return nil
}
