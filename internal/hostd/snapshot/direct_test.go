package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// directDevice is an empty device of size bytes and an O_DIRECT handle on
// it. The test is skipped where the temp directory refuses O_DIRECT, since
// then it would only cover the buffered path.
func directDevice(t *testing.T, size int64) (string, *os.File, *os.File) {
	t.Helper()
	p := emptyDevice(t, size)
	d := openDirect(p)
	if d == nil {
		t.Skipf("%s refuses O_DIRECT", filepath.Dir(p))
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close(); _ = d.Close() })
	return p, f, d
}

// patterned is size bytes of zero and non-zero runs at odd offsets, so
// records start and end on piece boundaries and in between.
func patterned(size int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, size)
	for off := 0; off < size; {
		n := min(size-off, 4096*(1+r.IntN(600)))
		if r.IntN(3) > 0 {
			for i := off; i < off+n; i++ {
				b[i] = byte(r.Uint32())
			}
		}
		off += n
	}
	return b
}

// stream is an extent stream written by hand: one record per entry.
func stream(device uint64, recs ...struct {
	off  uint64
	data []byte
}) []byte {
	var s bytes.Buffer
	var h [16]byte
	copy(h[:8], extentMagic)
	binary.LittleEndian.PutUint64(h[8:], device)
	s.Write(h[:])
	var total uint64
	for _, r := range recs {
		binary.LittleEndian.PutUint64(h[:8], r.off)
		binary.LittleEndian.PutUint64(h[8:], uint64(len(r.data)))
		s.Write(h[:])
		s.Write(r.data)
		total += uint64(len(r.data))
	}
	binary.LittleEndian.PutUint64(h[:8], trailerMark)
	binary.LittleEndian.PutUint64(h[8:], total)
	s.Write(h[:])
	return s.Bytes()
}

type rec = struct {
	off  uint64
	data []byte
}

// TestDirectRestoreMatchesBuffered: the same extent stream applied
// through O_DIRECT writers and through the page cache leaves the same
// bytes, and those are the source's.
func TestDirectRestoreMatchesBuffered(t *testing.T) {
	const size = 96 << 20
	src := patterned(size, 1)
	sp := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(sp, src, 0o644); err != nil {
		t.Fatal(err)
	}
	sf, _ := os.Open(sp)
	defer func() { _ = sf.Close() }()
	// Two used ranges with a hole between them, one ending mid-chunk.
	l := &usedLayout{blockSize: 4096, blocks: size / 4096, used: [][2]uint64{{0, 37<<20 + 4096*3}, {40 << 20, size}}}
	var s bytes.Buffer
	if _, err := writeExtents(sf, nil, size, l, &s); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), src...)
	clear(want[37<<20+4096*3 : 40<<20]) // not in the layout, so not restored

	dp, f, d := directDevice(t, size)
	if err := readExtents(bytes.NewReader(s.Bytes()), f, d); err != nil {
		t.Fatal(err)
	}
	bp := emptyDevice(t, size)
	g, _ := os.OpenFile(bp, os.O_WRONLY, 0)
	if err := readExtents(bytes.NewReader(s.Bytes()), g, nil); err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	direct, _ := os.ReadFile(dp)
	buffered, _ := os.ReadFile(bp)
	if !bytes.Equal(direct, want) {
		t.Fatal("direct restore differs from the source")
	}
	if !bytes.Equal(buffered, want) {
		t.Fatal("buffered restore differs from the source")
	}
}

// TestUnalignedRecordGoesBuffered: a record O_DIRECT cannot take (offset
// or length off a 4096 boundary) is written through the page cache after
// the direct writes in flight land, and every later one too; the result
// is what writing them one at a time gives.
func TestUnalignedRecordGoesBuffered(t *testing.T) {
	const size = 8 << 20
	fill := func(n int, b byte) []byte { return bytes.Repeat([]byte{b}, n) }
	recs := []rec{
		{0, fill(1<<20, 1)},
		{1<<20 + 100, fill(300, 2)},   // unaligned offset and length, shares a page with the next
		{1<<20 + 4096, fill(8192, 3)}, // aligned, after the switch
		{3 << 20, fill(4096*5, 4)},
		{6<<20 + 4096, fill(1000, 5)}, // aligned offset, unaligned length
	}
	want := make([]byte, size)
	for _, r := range recs {
		copy(want[r.off:], r.data)
	}
	p, f, d := directDevice(t, size)
	if err := readExtents(bytes.NewReader(stream(size, recs...)), f, d); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, want) {
		t.Fatal("restore with unaligned records differs")
	}
}

// TestOverlappingRecordsLaterWins: records that overlap land in stream
// order even with several writers, as they did one at a time.
func TestOverlappingRecordsLaterWins(t *testing.T) {
	const size = 16 << 20
	var recs []rec
	want := make([]byte, size)
	for i := range 40 {
		off := uint64((i * 7 % 13) << 20 / 4) // revisits earlier quarters of a MiB
		r := rec{off, bytes.Repeat([]byte{byte(i + 1)}, 1<<20)}
		recs = append(recs, r)
		copy(want[r.off:], r.data)
	}
	p, f, d := directDevice(t, size)
	if err := readExtents(bytes.NewReader(stream(size, recs...)), f, d); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, want) {
		t.Fatal("overlapping records landed out of order")
	}
}

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

// TestRestoreRefusalsStillHoldWithWriters: the stream checks hold on the
// direct path: a record past the device, a trailer that disagrees, bytes
// after the trailer, a stream cut inside a record.
func TestRestoreRefusalsStillHoldWithWriters(t *testing.T) {
	const size = 4 << 20
	good := stream(size, rec{0, bytes.Repeat([]byte{1}, 1<<20)}, rec{2 << 20, bytes.Repeat([]byte{2}, 4096)})
	badTrailer := append([]byte(nil), good...)
	binary.LittleEndian.PutUint64(badTrailer[len(badTrailer)-8:], 1)
	for name, s := range map[string][]byte{
		"past the device": stream(size, rec{size - 4096, bytes.Repeat([]byte{1}, 8192)}),
		"trailer count":   badTrailer,
		"after trailer":   append(append([]byte(nil), good...), 0),
		"cut in a record": good[:16+16+1000],
	} {
		t.Run(name, func(t *testing.T) {
			_, f, d := directDevice(t, size)
			if err := readExtents(bytes.NewReader(s), f, d); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	_, f, d := directDevice(t, size)
	if err := readExtents(bytes.NewReader(good), f, d); err != nil {
		t.Fatalf("good stream: %v", err)
	}
}

// TestRawRestoreIsSparseAndExact: a raw image comes back byte for byte,
// with a tail shorter than a chunk, and an all-zero chunk is not written
// (the thin volume never provisions it).
func TestRawRestoreIsSparseAndExact(t *testing.T) {
	const size = 5*extentChunk + 3*4096 + 512
	img := patterned(size, 7)
	clear(img[extentChunk : 3*extentChunk]) // two zero chunks
	for _, direct := range []bool{true, false} {
		t.Run(fmt.Sprintf("direct=%v", direct), func(t *testing.T) {
			p, f, d := directDevice(t, size)
			if !direct {
				d = nil
			}
			if err := readRaw(bytes.NewReader(img), f, d); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if !bytes.Equal(got, img) {
				t.Fatal("raw restore differs")
			}
			var st syscall.Stat_t
			if err := syscall.Stat(p, &st); err != nil {
				t.Fatal(err)
			}
			if alloc := st.Blocks * 512; alloc > size-2*extentChunk+extentChunk/2 {
				t.Fatalf("%d bytes allocated for an image with 8 MiB of zero chunks (%d bytes)", alloc, size)
			}
		})
	}
}

// TestRawRestoreRefusesALongerImage: an image longer than the device is
// an error, as dd's write past the end was.
func TestRawRestoreRefusesALongerImage(t *testing.T) {
	_, f, d := directDevice(t, 1<<20)
	if err := readRaw(bytes.NewReader(bytes.Repeat([]byte{1}, 1<<20+4096)), f, d); err == nil {
		t.Fatal("wrote an image longer than its device")
	}
}

// TestRawThroughPipelineIsDirect: Pipeline.Write still takes a raw zstd image.
func TestRawThroughPipelineIsDirect(t *testing.T) {
	needTools(t, "zstd")
	img := patterned(3*extentChunk+4096, 3)
	p, _, _ := directDevice(t, int64(len(img)))
	var z bytes.Buffer
	cmd := exec.Command("zstd", "-q", "-c")
	cmd.Stdin = bytes.NewReader(img)
	cmd.Stdout = &z
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := (&Pipeline{R: shell.Exec{}}).Write(context.Background(), p, &z); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, img) {
		t.Fatal("raw image through the pipeline differs")
	}
}

// TestOpenDirectFallsBack: where O_DIRECT is refused (tmpfs), openDirect
// says so and the restore goes through the page cache.
func TestOpenDirectFallsBack(t *testing.T) {
	dir := "/dev/shm"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no /dev/shm")
	}
	p, err := os.CreateTemp(dir, "repose-direct-")
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = os.Remove(p.Name()) }()
	_ = p.Truncate(1 << 20)
	if d := openDirect(p.Name()); d != nil {
		_ = d.Close()
		t.Skip("/dev/shm takes O_DIRECT here")
	}
	if err := readExtents(bytes.NewReader(stream(1<<20, rec{4096, []byte("hello")})), p, nil); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	_, _ = p.ReadAt(got, 4096)
	_ = p.Close()
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestParallelRangesOrderAndSizes(t *testing.T) {
	const block = 1000
	for _, size := range []int64{0, 1, block - 1, block, block + 1, 10*block + 500, 64 * block} {
		for _, par := range []int{1, 3, 8} {
			t.Run(fmt.Sprintf("size=%d/par=%d", size, par), func(t *testing.T) {
				src := patterned(int(size), uint64(size))
				var inFlight, peak atomic.Int32
				fetch := func(_ context.Context, off int64, dst []byte) error {
					n := inFlight.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					time.Sleep(time.Duration(rand.IntN(300)) * time.Microsecond) // finish out of order
					copy(dst, src[off:off+int64(len(dst))])
					inFlight.Add(-1)
					return nil
				}
				var out bytes.Buffer
				if err := parallelRanges(context.Background(), size, block, par, fetch, &out); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out.Bytes(), src) {
					t.Fatal("bytes out of order or missing")
				}
				if p := peak.Load(); int(p) > par {
					t.Fatalf("%d fetches at once, limit %d", p, par)
				}
			})
		}
	}
}

func TestParallelRangesErrors(t *testing.T) {
	const block, size = 100, 5000
	src := patterned(size, 9)
	boom := errors.New("boom")
	before := runtime.NumGoroutine()
	t.Run("fetch fails", func(t *testing.T) {
		var out bytes.Buffer
		err := parallelRanges(context.Background(), size, block, 4, func(ctx context.Context, off int64, dst []byte) error {
			if off == 20*block {
				return boom
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
			copy(dst, src[off:])
			return nil
		}, &out)
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
		if !bytes.Equal(out.Bytes(), src[:20*block]) {
			t.Fatalf("wrote %d bytes before the failed range, want %d", out.Len(), 20*block)
		}
	})
	t.Run("writer fails", func(t *testing.T) {
		var calls atomic.Int32
		err := parallelRanges(context.Background(), size, block, 4, func(_ context.Context, off int64, dst []byte) error {
			calls.Add(1)
			copy(dst, src[off:])
			return nil
		}, &failingWriter{after: 3, err: boom})
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
		if c := calls.Load(); c > 3+4+1 {
			t.Fatalf("%d fetches after the writer failed at the 3rd", c)
		}
	})
	t.Run("caller cancels", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var n atomic.Int32
		err := parallelRanges(ctx, size, block, 4, func(ctx context.Context, off int64, dst []byte) error {
			if n.Add(1) == 5 {
				cancel()
			}
			copy(dst, src[off:])
			return ctx.Err()
		}, io.Discard)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > before {
		t.Fatalf("%d goroutines left behind", g-before)
	}
}

// failingWriter takes after writes, then fails.
type failingWriter struct {
	after int
	err   error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.after == 0 {
		return 0, f.err
	}
	f.after--
	return len(p), nil
}

// serialExtents is the extent stream as writeExtents wrote it before
// I-571, one chunk at a time: the reference the parallel reader must
// match byte for byte.
func serialExtents(t *testing.T, src []byte, size uint64, l *usedLayout) []byte {
	t.Helper()
	var s bytes.Buffer
	var h [16]byte
	copy(h[:8], extentMagic)
	binary.LittleEndian.PutUint64(h[8:], size)
	s.Write(h[:])
	var data uint64
	rec := func(off uint64, b []byte) {
		binary.LittleEndian.PutUint64(h[:8], off)
		binary.LittleEndian.PutUint64(h[8:], uint64(len(b)))
		s.Write(h[:])
		s.Write(b)
		data += uint64(len(b))
	}
	zero := make([]byte, extentPiece)
	for _, r := range l.used {
		end := min(r[1], size)
		for off := r[0]; off < end; {
			n := min(uint64(extentChunk), end-off)
			chunk := src[off : off+n]
			runStart := -1
			for p := 0; p < len(chunk); p += extentPiece {
				q := min(p+extentPiece, len(chunk))
				if bytes.Equal(chunk[p:q], zero[:q-p]) {
					if runStart >= 0 {
						rec(off+uint64(runStart), chunk[runStart:p])
						runStart = -1
					}
					continue
				}
				if runStart < 0 {
					runStart = p
				}
			}
			if runStart >= 0 {
				rec(off+uint64(runStart), chunk[runStart:])
			}
			off += n
		}
	}
	binary.LittleEndian.PutUint64(h[:8], trailerMark)
	binary.LittleEndian.PutUint64(h[8:], data)
	s.Write(h[:])
	return s.Bytes()
}

// TestParallelSnapshotReadMatchesSerial: reading snapshotReaders chunks
// at once, through the page cache or O_DIRECT, gives the stream the
// one-chunk-at-a-time reader gave, including ranges that end mid-chunk
// and mid-piece, a range past the device's end, and a device whose
// size is not a multiple of the O_DIRECT alignment (DECISIONS I-571).
func TestParallelSnapshotReadMatchesSerial(t *testing.T) {
	for _, size := range []uint64{96 << 20, 96<<20 + 1536} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			src := patterned(int(size), size)
			p := filepath.Join(t.TempDir(), "src")
			if err := os.WriteFile(p, src, 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			l := &usedLayout{blockSize: 512, blocks: size / 512, used: [][2]uint64{
				{0, 37<<20 + 4096*3},
				{38<<20 + 512, 39 << 20},
				{40 << 20, size + 8192}, // clamped to the device
			}}
			want := serialExtents(t, src, size, l)

			var buffered bytes.Buffer
			if _, err := writeExtents(f, nil, size, l, &buffered); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buffered.Bytes(), want) {
				t.Fatalf("buffered parallel stream differs from the serial one (%d vs %d bytes)", buffered.Len(), len(want))
			}
			d := openDirectRead(p)
			if d == nil {
				t.Skipf("%s refuses O_DIRECT", filepath.Dir(p))
			}
			defer func() { _ = d.Close() }()
			var direct bytes.Buffer
			n, err := writeExtents(f, d, size, l, &direct)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(direct.Bytes(), want) {
				t.Fatalf("O_DIRECT parallel stream differs from the serial one (%d vs %d bytes)", direct.Len(), len(want))
			}
			if wantRead := l.used[0][1] + (l.used[1][1] - l.used[1][0]) + (size - l.used[2][0]); n != wantRead {
				t.Fatalf("read %d bytes, want %d", n, wantRead)
			}
			// And it restores to the source, outside the holes.
			back := emptyDevice(t, int64(size))
			g, _ := os.OpenFile(back, os.O_WRONLY, 0)
			if err := readExtents(bytes.NewReader(direct.Bytes()), g, nil); err != nil {
				t.Fatal(err)
			}
			_ = g.Close()
			got, _ := os.ReadFile(back)
			exp := append([]byte(nil), src...)
			clear(exp[l.used[0][1]:l.used[1][0]])
			clear(exp[l.used[1][1]:l.used[2][0]])
			if !bytes.Equal(got, exp) {
				t.Fatal("restored bytes differ from the source")
			}
		})
	}
}

// TestParallelSnapshotReadStopsOnAFailedRead: a chunk that cannot be read
// fails the stream with its offset, and writeExtents returns with every
// reader finished instead of hanging on the ones still out.
func TestParallelSnapshotReadStopsOnAFailedRead(t *testing.T) {
	const size = 64 << 20
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, patterned(size, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer func() { _ = f.Close() }()
	// The layout says the device is twice its size: every chunk past
	// 64 MiB reads nothing.
	l := &usedLayout{blockSize: 4096, blocks: 2 * size / 4096, used: [][2]uint64{{0, 2 * size}}}
	done := make(chan error, 1)
	go func() {
		_, err := writeExtents(f, nil, 2*size, l, io.Discard)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("read at %d", size)) {
			t.Fatalf("err = %v, want the read at %d", err, size)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("writeExtents hung after a failed read")
	}
}

// TestParallelSnapshotReadStopsWhenTheWriterFails: a stream whose reader
// (zstd) went away ends the read with that error.
func TestParallelSnapshotReadStopsWhenTheWriterFails(t *testing.T) {
	const size = 64 << 20
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, patterned(size, 9), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer func() { _ = f.Close() }()
	l := &usedLayout{blockSize: 4096, blocks: size / 4096, used: [][2]uint64{{0, size}}}
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.CopyN(io.Discard, pr, 3<<20)
		_ = pr.CloseWithError(io.ErrClosedPipe)
	}()
	done := make(chan error, 1)
	go func() {
		_, err := writeExtents(f, nil, size, l, pw)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("err = %v, want the closed pipe", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("writeExtents hung after its writer failed")
	}
}

// slowWriter takes its bytes a little at a time, like a zstd that falls
// behind: the streams writing to it fill their reads ahead.
type slowWriter struct{ n int }

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(time.Millisecond)
	s.n += len(p)
	return len(p), nil
}

// TestSnapshotReadsShareAHostBound: many snapshot streams at once hold
// at most maxChunkReads chunks between them, so the nightly snapshot of
// every guest cannot hold snapshotReaders buffers per guest, and each
// stream still comes out whole and right (DECISIONS I-571).
func TestSnapshotReadsShareAHostBound(t *testing.T) {
	const size = 32 << 20 // eight chunks: a stream can fill its snapshotReaders
	src := patterned(size, 11)
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	l := &usedLayout{blockSize: 4096, blocks: size / 4096, used: [][2]uint64{{0, size}}}
	want := sha256.Sum256(serialExtents(t, src, size, l))

	streams := 2*maxChunkReads/snapshotReaders + 2 // more than the bound has room for
	chunkReadsPeak.Store(0)
	outs := make([]hash.Hash, streams)
	errs := make([]error, streams)
	var start, wg sync.WaitGroup
	start.Add(1)
	for i := range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := os.Open(p)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = f.Close() }()
			outs[i] = sha256.New()
			start.Wait()
			// Reads pile up ahead of the slow writer until a bound
			// stops them.
			sw := &slowWriter{}
			bw := &teeWriter{a: outs[i], b: sw}
			_, errs[i] = writeExtents(f, nil, size, l, bw)
		}()
	}
	start.Done()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		t.Fatal("concurrent snapshot streams did not finish")
	}
	for i := range streams {
		if errs[i] != nil {
			t.Fatalf("stream %d: %v", i, errs[i])
		}
		if got := outs[i].Sum(nil); !bytes.Equal(got, want[:]) {
			t.Fatalf("stream %d differs from the serial stream", i)
		}
	}
	peak := chunkReadsPeak.Load()
	if peak > maxChunkReads {
		t.Fatalf("%d chunks held at once, bound %d", peak, maxChunkReads)
	}
	if peak <= snapshotReaders {
		t.Fatalf("%d chunks held at once with %d streams: the streams did not read side by side", peak, streams)
	}
	if n := chunkReadsInUse.Load(); n != 0 || len(chunkSlots) != 0 {
		t.Fatalf("%d chunks (%d slots) still held after every stream ended", n, len(chunkSlots))
	}
}

// teeWriter writes to a, then b.
type teeWriter struct{ a, b io.Writer }

func (w *teeWriter) Write(p []byte) (int, error) {
	if _, err := w.a.Write(p); err != nil {
		return 0, err
	}
	return w.b.Write(p)
}
