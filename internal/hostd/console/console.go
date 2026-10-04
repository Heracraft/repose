// Package console captures a guest's serial output. Cloud Hypervisor cannot
// reopen a log file for rotation, so the runner passes `--serial socket=`
// and a Tailer copies the socket into console.log, rotating at 64 MB and
// keeping 3 old files. Fluent Bit tails the current file. What one guest
// adds to its log is rate-limited, so a guest printing without pause cannot
// fill the host's shared log buffer or the log store (DECISIONS I-452).
package console

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Defaults from workstream 03 §5.13, and the rate limit of I-452: 2 KiB a
// second sustained, with 1 MiB at once, which a boot log fits in.
const (
	RotateBytes = 64 << 20
	Keep        = 3
	RateBytes   = 2 << 10
	BurstBytes  = 1 << 20
)

// DrainQuiet and DrainMax bound the read-out before capture closes the
// socket (see Run): close once nothing arrived for DrainQuiet, and never
// later than DrainMax after capture was asked to end.
var (
	DrainQuiet = 100 * time.Millisecond
	DrainMax   = 2 * time.Second
)

// Tailer copies one guest's console socket into its log.
type Tailer struct {
	Socket string
	Log    string
	Rotate int64
	Keep   int
	// Retry is the reconnect interval while the socket is absent.
	Retry time.Duration
	// Rate and Burst are a token bucket on what reaches the log, in bytes:
	// Rate a second, Burst at once; Rate 0 is unlimited. Output over it is
	// still read from the socket, so the guest's console never stalls,
	// and dropped; one line in the log then says how much went.
	Rate  int64
	Burst int64
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	written int64
	f       *os.File
	tokens  float64
	filled  time.Time
	dropped int64
}

// New returns a Tailer with the documented defaults.
func New(socket, log string) *Tailer {
	return &Tailer{Socket: socket, Log: log, Rotate: RotateBytes, Keep: Keep, Retry: 500 * time.Millisecond, Rate: RateBytes, Burst: BurstBytes}
}

func (t *Tailer) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// allow takes n bytes from the bucket, refilled for the time since the
// last call; false means the bytes are over the limit.
func (t *Tailer) allow(n int) bool {
	if t.Rate <= 0 {
		return true
	}
	now := t.now()
	if t.filled.IsZero() {
		t.tokens = float64(t.Burst)
	} else if d := now.Sub(t.filled); d > 0 {
		t.tokens += d.Seconds() * float64(t.Rate)
		if t.tokens > float64(t.Burst) {
			t.tokens = float64(t.Burst)
		}
	}
	t.filled = now
	if t.tokens < float64(n) {
		return false
	}
	t.tokens -= float64(n)
	return true
}

// droppedLine is the line the log gets in place of output over the limit.
func (t *Tailer) droppedLine() []byte {
	return []byte(fmt.Sprintf("\n[repose: %d bytes of console output dropped, over the limit of %d bytes a second]\n", t.dropped, t.Rate))
}

func (t *Tailer) open() error {
	f, err := os.OpenFile(t.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close() // stat failed; nothing was written
		return err
	}
	t.f, t.written = f, st.Size()
	return nil
}

func (t *Tailer) rotate() error {
	if t.f != nil {
		_ = t.f.Close() // rotating; the old file is renamed next
		t.f = nil
	}
	for i := t.Keep; i >= 1; i-- {
		from := t.Log
		if i > 1 {
			from = fmt.Sprintf("%s.%d", t.Log, i-1)
		}
		to := fmt.Sprintf("%s.%d", t.Log, i)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return t.open()
}

// Write appends to the log, rotating when the size cap is reached. Output
// over the rate limit is counted and dropped, and reported as written, so
// the caller keeps reading the socket.
func (t *Tailer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.allow(len(p)) {
		t.dropped += int64(len(p))
		return len(p), nil
	}
	if t.dropped > 0 {
		if err := t.write(t.droppedLine()); err != nil {
			return 0, err
		}
		t.dropped = 0
	}
	if err := t.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *Tailer) write(p []byte) error {
	if t.f == nil {
		if err := t.open(); err != nil {
			return err
		}
	}
	if t.written+int64(len(p)) > t.Rotate {
		if err := t.rotate(); err != nil {
			return err
		}
	}
	n, err := t.f.Write(p)
	t.written += int64(n)
	return err
}

// Close closes the log, first noting any output dropped since the last
// write.
func (t *Tailer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dropped > 0 {
		_ = t.write(t.droppedLine()) // best effort: the capture is ending either way
		t.dropped = 0
	}
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

// Run connects to the socket (retrying while the hypervisor is starting)
// and copies until ctx ends. A dropped connection is retried, since a
// guest restart recreates the socket.
func (t *Tailer) Run(ctx context.Context) error {
	defer func() { _ = t.Close() }() // the log is append-only; nothing is lost on a close error
	if err := os.MkdirAll(filepath.Dir(t.Log), 0o750); err != nil {
		return err
	}
	for {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", t.Socket)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(t.Retry):
				continue
			}
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				// Unblock the read loop without closing: it drains first.
				_ = c.SetReadDeadline(time.Now())
			case <-done:
			}
		}()
		buf := make([]byte, 32<<10)
		var drainUntil, lastData time.Time
		for {
			n, rerr := c.Read(buf)
			if n > 0 {
				if _, werr := t.Write(buf[:n]); werr != nil {
					close(done)
					_ = c.Close() // giving up on this connection; the write error is returned
					return werr
				}
				lastData = time.Now()
			}
			if ctx.Err() == nil {
				if rerr != nil {
					break
				}
				continue
			}
			// Capture is ending while the hypervisor may still be writing.
			// Closing a unix socket with bytes unread in its receive queue
			// hands the peer ECONNRESET, and Cloud Hypervisor 53's serial
			// thread exits on that read error without detaching the dead
			// socket: every later byte the guest writes fails, the UART's
			// transmit-empty interrupt never comes, and the guest's tty
			// output stalls for good. PID 1 then blocks on its next console
			// line and the guest never powers off (DECISIONS I-186). So read
			// until the socket has been quiet for DrainQuiet, for at most
			// DrainMax, and close right after an empty read.
			var ne net.Error
			if rerr != nil && (!errors.As(rerr, &ne) || !ne.Timeout()) {
				break // EOF or a real error: nothing is left unread
			}
			now := time.Now()
			if drainUntil.IsZero() {
				drainUntil, lastData = now.Add(DrainMax), now
			}
			if now.After(drainUntil) || (n == 0 && now.Sub(lastData) >= DrainQuiet) {
				break
			}
			_ = c.SetReadDeadline(now.Add(DrainQuiet))
		}
		close(done)
		_ = c.Close() // the read loop ended; the socket is reconnected or ctx is done
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(t.Retry):
		}
	}
}
