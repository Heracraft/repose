//go:build linux || darwin

package cli

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type termMode struct{ echo, canonical bool }

func getTermios(f *os.File) (termMode, error) {
	t, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	if err != nil {
		return termMode{}, err
	}
	return termMode{echo: t.Lflag&unix.ECHO != 0, canonical: t.Lflag&unix.ICANON != 0}, nil
}

// The whole prompt on a real terminal: stars while typing, the value
// intact, and the terminal back in cooked mode with echo afterwards.
func TestReadHiddenLineOnATerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = tty, tty
	defer func() { os.Stdin, os.Stderr = oldIn, oldErr }()

	screenBuf := &syncBuffer{}
	go func() { _, _ = io.Copy(screenBuf, ptmx) }()

	type result struct {
		v   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := readHiddenLine()
		done <- result{v, err}
	}()
	// The terminal must be raw before the keys go in, or the line
	// discipline echoes them.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := getTermios(tty); err == nil && !st.echo && !st.canonical {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal never left echo mode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := ptmx.Write([]byte("s3cr\x7fret\r")); err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readHiddenLine did not return")
	}
	if r.err != nil || string(r.v) != "s3cret" {
		t.Fatalf("value = %q, err = %v", r.v, r.err)
	}
	echoDeadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(screenBuf.String(), "****\b \b***") && time.Now().Before(echoDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	screen := screenBuf.String()
	if strings.Contains(screen, "s3c") || strings.Contains(screen, "ret") {
		t.Errorf("the screen showed the value: %q", screen)
	}
	if !strings.Contains(screen, "****\b \b***") {
		t.Errorf("screen = %q, want the stars and one erase", screen)
	}
	if st, err := getTermios(tty); err != nil || !st.echo || !st.canonical {
		t.Errorf("terminal after the read = %+v (%v), want echo and canonical mode back", st, err)
	}
}
