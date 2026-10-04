package snapshot

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

// mkfsImage makes a sparse file of size bytes holding an ext4 filesystem
// populated from a directory, the way hostd formats a volume
// (lazy_itable_init=1, I-162).
func mkfsImage(t *testing.T, size int64, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	for name, b := range files {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	img := filepath.Join(dir, "vol")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if out, err := exec.Command("mkfs.ext4", "-q", "-F", "-E", "lazy_itable_init=1", "-d", src, img).CombinedOutput(); err != nil {
		t.Fatalf("mkfs: %v %s", err, out)
	}
	return img
}

func emptyDevice(t *testing.T, size int64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "restored")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

func roundTrip(t *testing.T, p *Pipeline, src, dst string) (compressed int, mode Mode) {
	t.Helper()
	ctx := context.Background()
	r, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	mode = r.(Moder).Mode()
	compressed = buf.Len()
	if err := p.Write(ctx, dst, &buf); err != nil {
		t.Fatal(err)
	}
	return compressed, mode
}

func debugfsCat(t *testing.T, img, path string) []byte {
	t.Helper()
	out, err := exec.Command("debugfs", "-R", "cat "+path, img).Output()
	if err != nil {
		t.Fatalf("debugfs cat %s: %v", path, err)
	}
	return out
}

// TestExtentSnapshotRoundTrip: a clean ext4 volume goes out as extents,
// comes back onto an empty device as a filesystem e2fsck passes, with the
// same files, and the stream is far smaller than the volume.
func TestExtentSnapshotRoundTrip(t *testing.T) {
	needTools(t, "mkfs.ext4", "dumpe2fs", "e2fsck", "debugfs", "zstd")
	random := make([]byte, 3<<20)
	_, _ = rand.Read(random)
	files := map[string][]byte{"hello": []byte("hi\n"), "d/random": random, "d/e/text": bytes.Repeat([]byte("repose "), 200000)}
	const size = 2 << 30
	src := mkfsImage(t, size, files)
	dst := emptyDevice(t, size)
	p := &Pipeline{R: shell.Exec{}}
	n, mode := roundTrip(t, p, src, dst)
	if mode.Format != "extents" {
		t.Fatalf("mode %+v, want extents", mode)
	}
	if n > 8<<20 {
		t.Fatalf("compressed stream is %d bytes for 4 MB of files", n)
	}
	if out, err := exec.Command("e2fsck", "-fn", dst).CombinedOutput(); err != nil {
		t.Fatalf("e2fsck on the restored volume: %v\n%s", err, out)
	}
	for name, want := range files {
		if got := debugfsCat(t, dst, "/"+name); !bytes.Equal(got, want) {
			t.Fatalf("%s differs after restore (%d vs %d bytes)", name, len(got), len(want))
		}
	}
}

// TestRawFallbacks: a device that is not ext4, and an ext4 whose journal
// still needs replaying, go out raw and still round-trip.
func TestRawFallbacks(t *testing.T) {
	needTools(t, "mkfs.ext4", "dumpe2fs", "debugfs", "zstd", "dd")
	p := &Pipeline{R: shell.Exec{}}

	plain := filepath.Join(t.TempDir(), "plain")
	payload := bytes.Repeat([]byte("not a filesystem"), 100000)
	if err := os.WriteFile(plain, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := emptyDevice(t, int64(len(payload)))
	if _, mode := roundTrip(t, p, plain, dst); mode.Format != "raw" || mode.Why == "" {
		t.Fatalf("mode %+v, want raw with a reason", mode)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, payload) {
		t.Fatal("raw bytes differ")
	}

	img := mkfsImage(t, 256<<20, map[string][]byte{"f": []byte("x")})
	if out, err := exec.Command("debugfs", "-w", "-R", "feature needs_recovery", img).CombinedOutput(); err != nil {
		t.Fatalf("debugfs: %v %s", err, out)
	}
	dst2 := emptyDevice(t, 256<<20)
	_, mode := roundTrip(t, p, img, dst2)
	if mode.Format != "raw" || !strings.Contains(mode.Why, "recovery") {
		t.Fatalf("mode %+v, want raw because of the journal", mode)
	}
	a, _ := os.ReadFile(img)
	b, _ := os.ReadFile(dst2)
	if !bytes.Equal(a, b) {
		t.Fatal("raw ext4 image differs after restore")
	}
}

func TestParseDumpe2fsRefusals(t *testing.T) {
	head := "Filesystem features:      has_journal extent 64bit\nFilesystem state:         clean\nBlock count:              65536\nFirst block:              0\nBlock size:               4096\nBlocks per group:         32768\n"
	g0 := "Group 0: (Blocks 0-32767)\n  Free blocks: 100-32767\n"
	g1 := "Group 1: (Blocks 32768-65535) [BLOCK_UNINIT]\n  Free blocks: 33025-65535\n"
	l, err := parseDumpe2fs([]byte(head + g0 + g1))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]uint64{{0, 100 * 4096}, {32768 * 4096, 33025 * 4096}}
	if len(l.used) != 2 || l.used[0] != want[0] || l.used[1] != want[1] {
		t.Fatalf("used %v, want %v", l.used, want)
	}
	for name, out := range map[string]string{
		"cut short":      head + g0,
		"needs recovery": strings.Replace(head, "64bit", "64bit needs_recovery", 1) + g0 + g1,
		"not clean":      strings.Replace(head, "state:         clean", "state:         not clean", 1) + g0 + g1,
		"not ext4":       "dumpe2fs: Bad magic number in super-block\n",
		"garbled":        head + g0 + "Group 1:\n  Free blocks: 40000-x\n",
	} {
		if _, err := parseDumpe2fs([]byte(out)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestExtentStreamRefusesATruncatedOrOversizedStream: a restore never
// writes past its device or accepts a stream without its trailer.
func TestExtentStreamRefusesATruncatedOrOversizedStream(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	data := bytes.Repeat([]byte{7}, 1<<20)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(src)
	defer func() { _ = f.Close() }()
	var s bytes.Buffer
	if _, err := writeExtents(f, 1<<20, &usedLayout{blockSize: 4096, blocks: 256, used: [][2]uint64{{0, 1 << 20}}}, &s); err != nil {
		t.Fatal(err)
	}
	ok := emptyDevice(t, 1<<20)
	g, _ := os.OpenFile(ok, os.O_WRONLY, 0)
	if err := readExtents(bytes.NewReader(s.Bytes()), g, nil); err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	small := emptyDevice(t, 1<<19)
	h, _ := os.OpenFile(small, os.O_WRONLY, 0)
	if err := readExtents(bytes.NewReader(s.Bytes()), h, nil); err == nil {
		t.Fatal("wrote past a smaller device")
	}
	_ = h.Close()
	cut := emptyDevice(t, 1<<20)
	c, _ := os.OpenFile(cut, os.O_WRONLY, 0)
	if err := readExtents(bytes.NewReader(s.Bytes()[:s.Len()-16]), c, nil); err == nil {
		t.Fatal("accepted a stream without its trailer")
	}
	_ = c.Close()
}

// TestExtentSnapshotTiming measures a 40 GB volume holding 1 GB of files,
// the shape of izma's, both ways. It is the dev box, not a host (AMD, a
// sparse file standing in for a thin volume), so the numbers say which
// way and by roughly how much, not what host-01 will do. Run with
// REPOSE_SNAPSHOT_TIMING=1.
func TestExtentSnapshotTiming(t *testing.T) {
	if os.Getenv("REPOSE_SNAPSHOT_TIMING") == "" {
		t.Skip("REPOSE_SNAPSHOT_TIMING not set")
	}
	needTools(t, "mkfs.ext4", "dumpe2fs", "zstd", "dd")
	big := make([]byte, 1<<30)
	_, _ = rand.Read(big[:256<<20]) // a quarter incompressible, the rest text-like zeros
	src := mkfsImage(t, 40<<30, map[string][]byte{"blob": big})
	p := &Pipeline{R: shell.Exec{}}
	ctx := context.Background()
	for _, raw := range []bool{false, true} {
		start := time.Now()
		var r io.ReadCloser
		var err error
		if raw {
			r, err = p.readRaw(ctx, src, "forced")
		} else {
			r, err = p.Read(ctx, src)
		}
		if err != nil {
			t.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, r)
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes compressed in %s", r.(Moder).Mode().Format, n, time.Since(start).Round(time.Millisecond))
	}
}

// With Sandbox set, dumpe2fs on a snapshot runs in shell.Sandboxed with
// read-only access to that device alone.
func TestLayoutSandboxed(t *testing.T) {
	r := &shell.Fake{Scripts: []shell.Script{{Prefix: []string{"systemd-run"}, Result: shell.Result{ExitCode: 1}}}}
	p := &Pipeline{R: r, Sandbox: true}
	if _, err := p.layout(context.Background(), "/dev/vg-guests/snap-1"); err == nil {
		t.Fatal("a failed dumpe2fs gave a layout")
	}
	argv := strings.Join(r.Calls[0], " ")
	if !strings.HasPrefix(argv, "systemd-run ") || !strings.Contains(argv, "DeviceAllow=/dev/vg-guests/snap-1 r") ||
		!strings.HasSuffix(argv, "-- dumpe2fs /dev/vg-guests/snap-1") {
		t.Fatalf("dumpe2fs argv %s", argv)
	}
}
