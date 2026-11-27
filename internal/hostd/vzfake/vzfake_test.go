package vzfake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
)

type exits struct {
	mu  sync.Mutex
	got map[string]int
}

func (e *exits) on(sock string, code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got[sock] = code
}

func (e *exits) wait(t *testing.T, sock string) int {
	t.Helper()
	for i := 0; i < 200; i++ {
		e.mu.Lock()
		c, ok := e.got[sock]
		e.mu.Unlock()
		if ok {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never exited", sock)
	return 0
}

func host(t *testing.T) (*Host, *exits, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "vzf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h := New()
	e := &exits{got: map[string]int{}}
	h.OnExit = e.on
	t.Cleanup(h.Close)
	return h, e, dir
}

// Every refusal of the launcher, through hostd's client, with its words.
func TestRefusesWhatVZRefuses(t *testing.T) {
	h, _, dir := host(t)
	sock := filepath.Join(dir, "ch.sock")
	if err := h.Start([]string{"cloud-hypervisor", "--api-socket", sock}); err != nil {
		t.Fatal(err)
	}
	c := &ch.HTTP{}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"resume of a running VM", func() error { return c.Resume(ctx, sock) }, "cannot resume in state Running"},
		{"save of a running VM", func() error { return c.Snapshot(ctx, sock, filepath.Join(dir, "s")) }, "the VM must be paused to snapshot, it is Running"},
		{"live disk resize", func() error { return c.ResizeDisk(ctx, sock, "vol", 1<<30) }, ErrResizeDisk},
	} {
		if err := tc.call(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := c.Pause(ctx, sock); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, sock); err == nil || !strings.Contains(err.Error(), "cannot pause in state Paused") {
		t.Errorf("second pause: %v", err)
	}
	if len(h.Refused()) != 4 {
		t.Errorf("refused %v", h.Refused())
	}
}

// A save by a launcher run as root ends the VM (I-1662).
func TestSaveAsRootEndsTheVM(t *testing.T) {
	h, e, dir := host(t)
	h.SetAsRoot(true)
	sock := filepath.Join(dir, "ch.sock")
	if err := h.Start([]string{"--api-socket", sock}); err != nil {
		t.Fatal(err)
	}
	c := &ch.HTTP{}
	if err := c.Pause(context.Background(), sock); err != nil {
		t.Fatal(err)
	}
	if err := c.Snapshot(context.Background(), sock, filepath.Join(dir, "s")); err == nil {
		t.Fatal("a save as root answered")
	}
	if code := e.wait(t, sock); code != ExitError {
		t.Fatalf("exit %d", code)
	}
	if h.State(sock) != "" {
		t.Fatal("the VM outlived its save as root")
	}
}

// A save restores, paused, with the session unlocked; with it locked the
// launcher exits 3 and the save stays.
func TestRestoreNeedsTheUnlockedSession(t *testing.T) {
	h, e, dir := host(t)
	sock := filepath.Join(dir, "ch.sock")
	save := filepath.Join(dir, "s")
	if err := h.Start([]string{"--api-socket", sock}); err != nil {
		t.Fatal(err)
	}
	c := &ch.HTTP{}
	ctx := context.Background()
	if err := c.Pause(ctx, sock); err != nil {
		t.Fatal(err)
	}
	if err := c.Snapshot(ctx, sock, save); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(ctx, sock); err != nil {
		t.Fatal(err)
	}
	if code := e.wait(t, sock); code != ExitOK {
		t.Fatalf("shutdown exit %d", code)
	}
	e.mu.Lock()
	e.got = map[string]int{}
	e.mu.Unlock()
	h.SetLocked(true)
	if err := h.Start(ch.RestoreArgs(dir, save)); err != nil {
		t.Fatal(err)
	}
	if code := e.wait(t, sock); code != ExitRestore {
		t.Fatalf("locked restore exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(save, "vz.state")); err != nil {
		t.Fatal("the save went with the refused restore")
	}
	h.SetLocked(false)
	if err := h.Start(ch.RestoreArgs(dir, save)); err != nil {
		t.Fatal(err)
	}
	if s := h.State(sock); s != Paused {
		t.Fatalf("restored state %q, want Paused", s)
	}
	if err := c.Resume(ctx, sock); err != nil {
		t.Fatal(err)
	}
}

// A save on a locked session fails and ends the VM (lab Mac, 2026-11-27).
func TestSaveOnALockedSessionEndsTheVM(t *testing.T) {
	h, e, dir := host(t)
	sock := filepath.Join(dir, "ch.sock")
	if err := h.Start([]string{"--api-socket", sock}); err != nil {
		t.Fatal(err)
	}
	c := &ch.HTTP{}
	if err := c.Pause(context.Background(), sock); err != nil {
		t.Fatal(err)
	}
	h.SetLocked(true)
	err := c.Snapshot(context.Background(), sock, filepath.Join(dir, "s"))
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("locked save: %v", err)
	}
	if code := e.wait(t, sock); code != ExitError {
		t.Fatalf("exit %d", code)
	}
}
