package snapshot

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
)

func TestFileBlobRoundTrip(t *testing.T) {
	b := &FileBlob{Dir: t.TempDir()}
	ctx := context.Background()
	n, err := b.Upload(ctx, "u1/p1/2026-09-19T03:00:00Z.img.zst", strings.NewReader("hello"), map[string]string{"guest_id": "g"})
	if err != nil || n != 5 {
		t.Fatalf("upload %d %v", n, err)
	}
	var out bytes.Buffer
	if err := b.Download(ctx, "u1/p1/2026-09-19T03:00:00Z.img.zst", "", &out); err != nil || out.String() != "hello" {
		t.Fatalf("download %q %v", out.String(), err)
	}
	if _, ok, _ := b.Stat(ctx, "u1/p1/nope"); ok {
		t.Fatal("missing blob reported present")
	}
	testVersions(t, b, "u1/p1/2026-09-19T03:00:00Z.img.zst")
	if _, err := b.Upload(ctx, "../escape", strings.NewReader("x"), nil); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestFakeStreamerRoundTrip(t *testing.T) {
	l := lvm.NewFake()
	ctx := context.Background()
	_ = l.CreateVolume(ctx, "g-1", 10)
	l.SetData("g-1", []byte("data"))
	s := &FakeStreamer{LVM: l}
	r, err := s.Read(ctx, "/dev/vg-guests/g-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = l.CreateVolume(ctx, "g-2", 10)
	if err := s.Write(ctx, "/dev/vg-guests/g-2", r); err != nil {
		t.Fatal(err)
	}
	if string(l.GetData("g-2")) != "data" {
		t.Fatal("bytes did not round-trip")
	}
}

func TestPipelineWithRealTools(t *testing.T) {
	for _, tool := range []string{"dd", "zstd"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	src, dst := t.TempDir()+"/src", t.TempDir()+"/dst"
	payload := bytes.Repeat([]byte("repose"), 100000)
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{R: shell.Exec{}}
	ctx := context.Background()
	r, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	if _, err := compressed.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if compressed.Len() >= len(payload)/10 {
		t.Fatalf("zstd did not compress: %d bytes", compressed.Len())
	}
	// A restore writes onto a volume it has just created, never a new file.
	if err := os.WriteFile(dst, make([]byte, len(payload)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Write(ctx, dst, &compressed); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, payload) {
		t.Fatal("restored bytes differ")
	}
}

func TestMemBlobVersions(t *testing.T) {
	testVersions(t, NewMemBlob(), "u1/p1/a.img.zst")
}

// testVersions is I-462's promise: a download of the version Stat
// reported reads those bytes, and once the blob is replaced it fails
// with ErrVersionChanged instead of reading the new ones.
func testVersions(t *testing.T, b Blob, path string) {
	t.Helper()
	ctx := context.Background()
	if _, err := b.Upload(ctx, path, strings.NewReader("first"), nil); err != nil {
		t.Fatal(err)
	}
	info, ok, err := b.Stat(ctx, path)
	if err != nil || !ok || info.Size != 5 || info.Version == "" {
		t.Fatalf("stat: %+v %v %v", info, ok, err)
	}
	var out bytes.Buffer
	if err := b.Download(ctx, path, info.Version, &out); err != nil || out.String() != "first" {
		t.Fatalf("download of the version stat reported: %q %v", out.String(), err)
	}
	time.Sleep(2 * time.Millisecond) // a file's version is its mtime
	if _, err := b.Upload(ctx, path, strings.NewReader("second!"), nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := b.Download(ctx, path, info.Version, &out); !errors.Is(err, ErrVersionChanged) || out.Len() != 0 {
		t.Fatalf("download of a replaced version: %q %v", out.String(), err)
	}
	if next, _, _ := b.Stat(ctx, path); next.Version == info.Version || next.Size != 7 {
		t.Fatalf("stat after replace: %+v (was %+v)", next, info)
	}
}
