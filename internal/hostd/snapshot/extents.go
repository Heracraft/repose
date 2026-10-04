package snapshot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// The extent format (DECISIONS I-164). A snapshot of an ext4 volume used to
// be `dd` of the whole device through zstd: a 40 GB volume holding 1 GB of
// files read 40 GB (32.9 s on host-01 for 2 MB of output), and a restore
// wrote 40 GB back. The extent format carries only the blocks the
// filesystem uses and, of those, only the ones that are not all zero:
//
//	header  "RPSXT001" | u64 device bytes
//	record  u64 offset | u64 length | length bytes      (repeated)
//	trailer u64 0xFFFFFFFFFFFFFFFF | u64 total data bytes
//
// little endian, the whole stream zstd-compressed like the raw format and
// stored under the same `.img.zst` name. A restore tells the two apart by
// the magic after decompression; a raw image of an ext4 volume starts
// with 1024 zero bytes, so it never begins with the magic.
const extentMagic = "RPSXT001"

const (
	// extentPiece is the zero-skipping granularity; 64 KiB is dm-thin's
	// default chunk, so a skipped piece is a chunk never provisioned.
	extentPiece = 64 << 10
	// extentChunk is how much is read per ReadAt and the longest record.
	extentChunk = 4 << 20
	trailerMark = ^uint64(0)
)

var groupLine = regexp.MustCompile(`^Group [0-9]+:`)

// usedLayout is the filesystem's used byte ranges on the device.
type usedLayout struct {
	blockSize uint64
	blocks    uint64
	used      [][2]uint64 // byte ranges [start, end), sorted, disjoint
}

// errNotExtentable is why a device is streamed raw instead.
type errNotExtentable struct{ reason string }

func (e *errNotExtentable) Error() string { return "raw snapshot: " + e.reason }

// parseDumpe2fs reads `dumpe2fs <dev>` output: the geometry from the
// header and every group's "Free blocks:" line. Anything that makes the
// on-disk bitmaps untrustworthy is refused, and the caller falls back to
// reading the whole device: a journal that still needs replaying (a guest
// that was killed; freeze and a clean shutdown both clear the flag) can
// allocate blocks the bitmaps do not show yet.
func parseDumpe2fs(out []byte) (*usedLayout, error) {
	var (
		bs, count, first, perGroup uint64
		haveBS, haveCount          bool
		groups                     int
		freeLines                  int
		free                       [][2]uint64
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		if groupLine.MatchString(line) { // not "Group descriptor size:"
			groups++
			continue
		}
		if strings.HasPrefix(line, "  Free blocks:") {
			freeLines++
			rs, err := parseRanges(strings.TrimSpace(strings.TrimPrefix(line, "  Free blocks:")))
			if err != nil {
				return nil, &errNotExtentable{"unparseable free blocks line"}
			}
			free = append(free, rs...)
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Filesystem features":
			for _, f := range strings.Fields(v) {
				if f == "needs_recovery" {
					return nil, &errNotExtentable{"journal needs recovery"}
				}
			}
		case "Filesystem state":
			if v != "clean" {
				return nil, &errNotExtentable{"filesystem state " + v}
			}
		case "Block size":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil || n < 1024 || n > 65536 {
				return nil, &errNotExtentable{"block size"}
			}
			bs, haveBS = n, true
		case "Block count":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil || n == 0 {
				return nil, &errNotExtentable{"block count"}
			}
			count, haveCount = n, true
		case "First block":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return nil, &errNotExtentable{"first block"}
			}
			first = n
		case "Blocks per group":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil || n == 0 {
				return nil, &errNotExtentable{"blocks per group"}
			}
			perGroup = n
		}
	}
	if err := sc.Err(); err != nil {
		return nil, &errNotExtentable{"reading dumpe2fs output"}
	}
	if !haveBS || !haveCount || perGroup == 0 {
		return nil, &errNotExtentable{"no ext4 geometry"}
	}
	// Every group must have been listed, or the output was cut short and
	// a group's used blocks would be missed.
	want := int((count - first + perGroup - 1) / perGroup)
	if groups != want || freeLines != want {
		return nil, &errNotExtentable{fmt.Sprintf("%d of %d groups listed", freeLines, want)}
	}
	return layoutFromFree(bs, count, free)
}

// parseRanges reads "9710-32767, 40000, 41000-41999" (empty is none).
func parseRanges(s string) ([][2]uint64, error) {
	var out [][2]uint64
	if s == "" {
		return nil, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		a, b, isRange := strings.Cut(part, "-")
		lo, err := strconv.ParseUint(a, 10, 64)
		if err != nil {
			return nil, err
		}
		hi := lo
		if isRange {
			if hi, err = strconv.ParseUint(b, 10, 64); err != nil || hi < lo {
				return nil, errors.New("bad range")
			}
		}
		out = append(out, [2]uint64{lo, hi})
	}
	return out, nil
}

// layoutFromFree turns free block ranges (inclusive) into used byte ranges.
func layoutFromFree(bs, count uint64, free [][2]uint64) (*usedLayout, error) {
	l := &usedLayout{blockSize: bs, blocks: count}
	next := uint64(0) // first block not yet accounted for
	for _, r := range free {
		if r[0] < next || r[1] >= count {
			return nil, &errNotExtentable{"free ranges out of order"}
		}
		if r[0] > next {
			l.used = append(l.used, [2]uint64{next * bs, r[0] * bs})
		}
		next = r[1] + 1
	}
	if next < count {
		l.used = append(l.used, [2]uint64{next * bs, count * bs})
	}
	return l, nil
}

// usedBytes is the sum of the used ranges.
func (l *usedLayout) usedBytes() uint64 {
	var n uint64
	for _, r := range l.used {
		n += r[1] - r[0]
	}
	return n
}

// writeExtents reads the used ranges of dev and writes the uncompressed
// extent stream to w, skipping pieces that are all zero (the inode tables
// that mkfs left to lazy init, blocks the guest freed but the bitmap still
// holds). It returns the bytes it read from dev.
func writeExtents(dev *os.File, size uint64, l *usedLayout, w io.Writer) (uint64, error) {
	bw := bufio.NewWriterSize(w, 1<<20)
	var hdr [16]byte
	copy(hdr[:8], extentMagic)
	binary.LittleEndian.PutUint64(hdr[8:], size)
	if _, err := bw.Write(hdr[:]); err != nil {
		return 0, err
	}
	buf := make([]byte, extentChunk)
	zero := make([]byte, extentPiece)
	var read, data uint64
	record := func(off uint64, b []byte) error {
		var h [16]byte
		binary.LittleEndian.PutUint64(h[:8], off)
		binary.LittleEndian.PutUint64(h[8:], uint64(len(b)))
		if _, err := bw.Write(h[:]); err != nil {
			return err
		}
		_, err := bw.Write(b)
		data += uint64(len(b))
		return err
	}
	for _, r := range l.used {
		end := min(r[1], size) // a filesystem never extends past its device
		for off := r[0]; off < end; {
			n := min(uint64(len(buf)), end-off)
			got, err := dev.ReadAt(buf[:n], int64(off))
			if err != nil && (!errors.Is(err, io.EOF) || got == 0) {
				return read, fmt.Errorf("read at %d: %w", off, err)
			}
			chunk := buf[:got]
			read += uint64(got)
			// Emit each maximal run of non-zero pieces as one record.
			runStart := -1
			for p := 0; p < len(chunk); p += extentPiece {
				q := min(p+extentPiece, len(chunk))
				if bytes.Equal(chunk[p:q], zero[:q-p]) {
					if runStart >= 0 {
						if err := record(off+uint64(runStart), chunk[runStart:p]); err != nil {
							return read, err
						}
						runStart = -1
					}
					continue
				}
				if runStart < 0 {
					runStart = p
				}
			}
			if runStart >= 0 {
				if err := record(off+uint64(runStart), chunk[runStart:]); err != nil {
					return read, err
				}
			}
			off += uint64(got)
		}
	}
	var t [16]byte
	binary.LittleEndian.PutUint64(t[:8], trailerMark)
	binary.LittleEndian.PutUint64(t[8:], data)
	if _, err := bw.Write(t[:]); err != nil {
		return read, err
	}
	return read, bw.Flush()
}

// readExtents applies an extent stream (magic already peeked, not
// consumed) to dev, which must read as zeros wherever the stream has no
// record: restore always writes into a volume it has just created. When
// direct is an O_DIRECT handle on the same device, the records go through
// it, restoreWriters at a time (direct.go); otherwise, or from the first
// record O_DIRECT cannot take, they go through dev and the page cache.
func readExtents(r io.Reader, dev, direct *os.File) error {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("extent header: %w", err)
	}
	if string(hdr[:8]) != extentMagic {
		return errors.New("extent header: bad magic")
	}
	want := binary.LittleEndian.Uint64(hdr[8:])
	size, err := dev.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("device size: %w", err)
	}
	w := newExtentWriter(dev, direct)
	defer func() { _ = w.close() }() // the error that ended the restore is returned below; this only stops the writers
	var data uint64
	for {
		var h [16]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return fmt.Errorf("extent stream cut short: %w", err)
		}
		off := binary.LittleEndian.Uint64(h[:8])
		n := binary.LittleEndian.Uint64(h[8:])
		if off == trailerMark {
			if n != data {
				return fmt.Errorf("extent stream carried %d bytes, trailer says %d", data, n)
			}
			if _, err := io.ReadFull(r, make([]byte, 1)); !errors.Is(err, io.EOF) {
				return errors.New("extent stream has bytes after its trailer")
			}
			if err := w.close(); err != nil {
				return err
			}
			return dev.Sync() // also the flush that commits the thin pool's mappings
		}
		if n == 0 || n > extentChunk || off+n > want || off+n > uint64(size) {
			return fmt.Errorf("extent %d+%d does not fit the %d-byte device (snapshot of %d bytes)", off, n, size, want)
		}
		buf, err := w.buffer()
		if err != nil {
			return err
		}
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			w.release(buf)
			return fmt.Errorf("extent data cut short: %w", err)
		}
		if err := w.write(off, buf, 0, int(n)); err != nil {
			return err
		}
		data += n
	}
}

// readRaw writes a raw image onto dev, which reads as zeros, like
// `dd bs=4M conv=sparse`: an extentChunk that is all zero is skipped, and
// any other is written from its first non-zero extentPiece to its last.
// An image longer than the device is refused, as dd's write would have
// been.
func readRaw(r io.Reader, dev, direct *os.File) error {
	size, err := dev.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("device size: %w", err)
	}
	w := newExtentWriter(dev, direct)
	defer func() { _ = w.close() }() // the error that ended the restore is returned below; this only stops the writers
	zero := make([]byte, extentPiece)
	for off := uint64(0); ; {
		buf, err := w.buffer()
		if err != nil {
			return err
		}
		n, rerr := io.ReadFull(r, buf)
		last := errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF)
		if rerr != nil && !last {
			w.release(buf)
			return fmt.Errorf("reading the image: %w", rerr)
		}
		if off+uint64(n) > uint64(size) {
			w.release(buf)
			return fmt.Errorf("image is longer than the %d-byte device", size)
		}
		lo, hi := n, 0
		for p := 0; p < n; p += extentPiece {
			q := min(p+extentPiece, n)
			if !bytes.Equal(buf[p:q], zero[:q-p]) {
				lo, hi = min(lo, p), q
			}
		}
		if lo < hi {
			if err := w.write(off+uint64(lo), buf, lo, hi); err != nil {
				return err
			}
		} else {
			w.release(buf)
		}
		off += uint64(n)
		if last {
			break
		}
	}
	if err := w.close(); err != nil {
		return err
	}
	return dev.Sync()
}

// layout runs dumpe2fs on dev, sandboxed when p.Sandbox, and returns its used ranges, or why the
// device must be streamed raw.
func (p *Pipeline) layout(ctx context.Context, dev string) (*usedLayout, error) {
	argv := []string{"dumpe2fs", dev}
	if p.Sandbox {
		argv = shell.Sandboxed(dev, false, argv...)
	}
	res, err := p.R.Run(ctx, argv...)
	if err != nil {
		return nil, &errNotExtentable{"dumpe2fs failed (not ext4?)"}
	}
	return parseDumpe2fs(res.Stdout)
}

// deviceSize is the byte size of a block device or regular file.
func deviceSize(f *os.File) (uint64, error) {
	n, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return uint64(n), nil
}
