package snapshot

// Linux only: O_DIRECT is a Linux open flag (macOS has F_NOCACHE), and the
// rest of direct_test.go runs on a Mac too, where the snapshot pipeline
// is the same code (design 2, I-1666).

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDirectWriteErrorIsReturned: a write that fails ends the restore
// with that error instead of hanging or reporting success.
func TestDirectWriteErrorIsReturned(t *testing.T) {
	const size = 64 << 20
	p := emptyDevice(t, size)
	f, _ := os.OpenFile(p, os.O_WRONLY, 0)
	defer func() { _ = f.Close() }()
	ro, err := os.OpenFile(p, os.O_RDONLY|syscall.O_DIRECT, 0)
	if err != nil {
		t.Skipf("no O_DIRECT here: %v", err)
	}
	defer func() { _ = ro.Close() }()
	var recs []rec
	for i := range 60 {
		recs = append(recs, rec{uint64(i) << 20, bytes.Repeat([]byte{9}, 1<<20)})
	}
	done := make(chan error, 1)
	go func() { done <- readExtents(bytes.NewReader(stream(size, recs...)), f, ro) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "write at") {
			t.Fatalf("got %v, want the write error", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("restore hung after a failed write")
	}
}
