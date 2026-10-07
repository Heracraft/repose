package snapshot

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/shell"
)

// Streamer moves bytes between a block device and a stream.
type Streamer interface {
	// Read returns the compressed contents of dev.
	Read(ctx context.Context, dev string) (io.ReadCloser, error)
	// Write decompresses r onto dev.
	Write(ctx context.Context, dev string, r io.Reader) error
}

// Pipeline is the real Streamer. An ext4 volume whose bitmaps can be
// trusted goes out in the extent format (extents.go, DECISIONS I-164): its
// used, non-zero blocks, framed and piped through zstd -T4 -3. Anything
// else goes out raw, dd if=dev bs=4M | zstd -T4 -3, which reads the whole
// device. On the way in, zstd -d feeds an extent stream's records, or a
// raw image's non-zero pieces, to O_DIRECT writes (direct.go, I-403).
type Pipeline struct {
	R shell.Runner
	// Sandbox runs dumpe2fs, which parses metadata the guest wrote, in
	// shell.Sandboxed rather than as hostd (DECISIONS I-465). hostd sets
	// it; tests on image files leave it off.
	Sandbox bool
}

// Mode names how a stream was produced, for the snapshot log line.
type Mode struct {
	// Format is "extents" or "raw".
	Format string
	// Why is the reason a device went out raw ("" for extents).
	Why string
	// UsedBytes is what the filesystem says it uses (extents only).
	UsedBytes uint64
}

// Moder is implemented by the readers Pipeline returns.
type Moder interface{ Mode() Mode }

type procReader struct {
	io.ReadCloser
	wait []func() error
	mode Mode
	rt   *readTimer // nil for a raw stream
}

func (p *procReader) Mode() Mode { return p.mode }

// ReadDuration is how long reading the device took, from Read to the last
// byte read, or 0 when it is not known (a raw stream) or not over. A
// snapshot whose duration is mostly this was bound by the disk; one that
// ended well after it, by zstd or the upload (DECISIONS I-571).
func (p *procReader) ReadDuration() time.Duration {
	if p.rt == nil {
		return 0
	}
	return p.rt.duration()
}

// ReadTimer is implemented by the readers Pipeline returns.
type ReadTimer interface{ ReadDuration() time.Duration }

// readTimer records when the device read ended.
type readTimer struct {
	start time.Time
	mu    sync.Mutex
	took  time.Duration
}

func (r *readTimer) finish() {
	r.mu.Lock()
	r.took = time.Since(r.start)
	r.mu.Unlock()
}

func (r *readTimer) duration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.took
}

func (p *procReader) Close() error {
	err := p.ReadCloser.Close()
	for _, w := range p.wait {
		if e := w(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// Read implements Streamer.
func (p *Pipeline) Read(ctx context.Context, dev string) (io.ReadCloser, error) {
	l, lerr := p.layout(ctx, dev)
	if lerr == nil {
		return p.readExtents(ctx, dev, l)
	}
	why := lerr.Error()
	var ne *errNotExtentable
	if errors.As(lerr, &ne) {
		why = ne.reason
	}
	return p.readRaw(ctx, dev, why)
}

func (p *Pipeline) readExtents(ctx context.Context, dev string, l *usedLayout) (io.ReadCloser, error) {
	f, err := os.Open(dev)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dev, err)
	}
	size, err := deviceSize(f)
	if err != nil {
		_ = f.Close() // read only
		return nil, fmt.Errorf("device size: %w", err)
	}
	// O_DIRECT only when every chunk can be aligned for it: chunks start
	// on filesystem blocks and end on one or at the device's end.
	var direct *os.File
	if l.blockSize%directAlign == 0 && size%directAlign == 0 {
		direct = openDirectRead(dev)
	}
	pr, pw := io.Pipe()
	gen := make(chan error, 1)
	rt := &readTimer{start: time.Now()}
	go func() {
		_, err := writeExtents(f, direct, size, l, pw)
		rt.finish()
		_ = f.Close() // read only
		if direct != nil {
			_ = direct.Close() // read only
		}
		_ = pw.CloseWithError(err) // zstd sees EOF, or a failed read
		gen <- err
	}()
	zs, err := p.R.Start(ctx, pr, "zstd", "-T4", "-3", "-q", "-c")
	if err != nil {
		_ = pr.CloseWithError(err) // stops the generator
		<-gen
		return nil, fmt.Errorf("zstd: %w", err)
	}
	go func() { _, _ = io.Copy(io.Discard, zs.Stderr()) }() // -q keeps it empty; drained so zstd never blocks
	stop := func() error {
		_ = pr.CloseWithError(io.ErrClosedPipe) // unblocks the generator if zstd stopped reading early
		if err := <-gen; err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return fmt.Errorf("reading the snapshot: %w", err)
		}
		return nil
	}
	return &procReader{ReadCloser: zs.Stdout(), wait: []func() error{zs.Wait, stop}, mode: Mode{Format: "extents", UsedBytes: l.usedBytes()}, rt: rt}, nil
}

func (p *Pipeline) readRaw(ctx context.Context, dev, why string) (io.ReadCloser, error) {
	dd, err := p.R.Start(ctx, nil, "dd", "if="+dev, "bs=4M", "status=none")
	if err != nil {
		return nil, fmt.Errorf("dd: %w", err)
	}
	go func() { _, _ = io.Copy(io.Discard, dd.Stderr()) }() // status=none keeps it empty; drained so dd never blocks
	zs, err := p.R.Start(ctx, dd.Stdout(), "zstd", "-T4", "-3", "-q", "-c")
	if err != nil {
		_ = dd.Kill() // zstd failed to start; nothing consumed dd's output
		return nil, fmt.Errorf("zstd: %w", err)
	}
	go func() { _, _ = io.Copy(io.Discard, zs.Stderr()) }() // -q keeps it empty; drained so zstd never blocks
	return &procReader{ReadCloser: zs.Stdout(), wait: []func() error{zs.Wait, dd.Wait}, mode: Mode{Format: "raw", Why: why}}, nil
}

// Write implements Streamer: an extent stream is applied record by record,
// a raw image (every snapshot taken before I-164, and any volume that was
// not a clean ext4) is written piece by piece, its zero pieces skipped.
// Both go around the page cache where the device allows it (direct.go).
func (p *Pipeline) Write(ctx context.Context, dev string, r io.Reader) error {
	zs, err := p.R.Start(ctx, r, "zstd", "-d", "-q", "-c")
	if err != nil {
		return fmt.Errorf("zstd -d: %w", err)
	}
	go func() { _, _ = io.Copy(io.Discard, zs.Stderr()) }() // drained so zstd never blocks
	br := bufio.NewReaderSize(zs.Stdout(), 1<<20)
	head, _ := br.Peek(len(extentMagic)) // a short or failed stream is reported by whichever writer reads it
	if string(head) == extentMagic {
		werr := p.writeExtentStream(dev, br)
		if werr != nil {
			_ = zs.Kill() // stop decompressing what will not be written
		}
		_, _ = io.Copy(io.Discard, br) // let zstd finish so Wait returns
		zsErr := zs.Wait()
		if werr != nil {
			return werr
		}
		if zsErr != nil {
			return fmt.Errorf("zstd -d: %w", zsErr)
		}
		return nil
	}
	werr := writeRawStream(dev, br)
	if werr != nil {
		_ = zs.Kill() // stop decompressing what will not be written
	}
	_, _ = io.Copy(io.Discard, br) // let zstd finish so Wait returns
	zsErr := zs.Wait()
	if werr != nil {
		return werr
	}
	if zsErr != nil {
		return fmt.Errorf("zstd -d: %w", zsErr)
	}
	return nil
}

func (p *Pipeline) writeExtentStream(dev string, r io.Reader) error {
	return withDevice(dev, func(f, d *os.File) error {
		if err := readExtents(r, f, d); err != nil {
			return fmt.Errorf("restore extents: %w", err)
		}
		return nil
	})
}

// writeRawStream writes a raw image onto dev the way `dd conv=sparse`
// did: every piece that is not all zero, in order, through the same
// writer as an extent stream.
func writeRawStream(dev string, r io.Reader) error {
	return withDevice(dev, func(f, d *os.File) error {
		if err := readRaw(r, f, d); err != nil {
			return fmt.Errorf("restore raw image: %w", err)
		}
		return nil
	})
}

// withDevice opens dev for writing, buffered and (where it can) direct.
func withDevice(dev string, fn func(f, d *os.File) error) error {
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dev, err)
	}
	d := openDirect(dev)
	werr := fn(f, d)
	cerr := f.Close()
	if d != nil {
		if err := d.Close(); err != nil && cerr == nil {
			cerr = err
		}
	}
	if werr != nil {
		return werr
	}
	return cerr
}

// FakeStreamer copies bytes between the fake LVM's volumes and streams.
type FakeStreamer struct {
	LVM  *lvm.Fake
	Fail error
}

func devName(dev string) string {
	for i := len(dev) - 1; i >= 0; i-- {
		if dev[i] == '/' {
			return dev[i+1:]
		}
	}
	return dev
}

// Read implements Streamer.
func (f *FakeStreamer) Read(_ context.Context, dev string) (io.ReadCloser, error) {
	if f.Fail != nil {
		return nil, f.Fail
	}
	data := f.LVM.GetData(devName(dev))
	if data == nil {
		return nil, errors.New("fake streamer: no such volume " + dev)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Write implements Streamer.
func (f *FakeStreamer) Write(_ context.Context, dev string, r io.Reader) error {
	if f.Fail != nil {
		return f.Fail
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.LVM.SetData(devName(dev), b)
	return nil
}
