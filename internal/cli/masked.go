package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/term"
)

// errMaskedInterrupted is Ctrl-C at a masked prompt.
var errMaskedInterrupted = errors.New("interrupted")

// errMaskedMultiline is a paste of several lines at a masked prompt.
var errMaskedMultiline = exitf(ExitUsage, "The pasted value has more than one line, and this prompt takes one. Pipe it in instead: `cat FILE | repose ...`.")

func peekIs(br *bufio.Reader, c byte) bool {
	b, err := br.Peek(1)
	return err == nil && b[0] == c
}

// readMasked reads one line from r, a terminal in raw mode, and writes
// one `*` to w for every character it takes, so a secret typed or pasted
// at `repose secrets set` shows that it arrived without showing what it
// is (DECISIONS I-365). Enter ends the line; Backspace takes back one
// character, Ctrl-U all of them; Ctrl-C is errMaskedInterrupted; Ctrl-D
// on an empty line is io.EOF. Escape sequences (arrow keys) and other
// control characters are dropped, as they would be in a password field.
func readMasked(r io.Reader, w io.Writer) ([]byte, error) {
	br := bufio.NewReader(r)
	var line []byte
	erase := func(n int) {
		if n > 0 {
			_, _ = io.WriteString(w, strings.Repeat("\b \b", n))
		}
	}
	lastRuneLen := func() int {
		_, size := utf8.DecodeLastRune(line)
		return size
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			if len(line) > 0 && err == io.EOF {
				return line, nil
			}
			return line, err
		}
		switch {
		case b == '\r' || b == '\n':
			// More bytes already here after the line ends is a paste of
			// several lines (a PEM key): keeping the first would store
			// a value that looks set and is not. The rest is read off,
			// so it does not reach the shell (I-929).
			crlf := b == '\r' && br.Buffered() == 1 && peekIs(br, '\n')
			if br.Buffered() > 0 && !crlf {
				_, _ = br.Discard(br.Buffered())
				return nil, errMaskedMultiline
			}
			return line, nil
		case b == 0x03: // Ctrl-C
			return nil, errMaskedInterrupted
		case b == 0x04: // Ctrl-D
			if len(line) == 0 {
				return nil, io.EOF
			}
		case b == 0x7f || b == 0x08: // Backspace, Ctrl-H
			if len(line) > 0 {
				line = line[:len(line)-lastRuneLen()]
				erase(1)
			}
		case b == 0x15: // Ctrl-U
			erase(utf8.RuneCount(line))
			line = line[:0]
		case b == 0x1b: // ESC: skip a CSI or SS3 sequence whole
			next, err := br.ReadByte()
			if err != nil {
				continue
			}
			if next != '[' && next != 'O' {
				continue
			}
			for {
				c, err := br.ReadByte()
				if err != nil || (c >= 0x40 && c <= 0x7e) {
					break
				}
			}
		case b < 0x20:
			// Tab and the other controls: not part of a secret.
		default:
			line = append(line, b)
			// A multi-byte character gets its `*` on its first byte.
			if b < 0x80 || b >= 0xc0 {
				_, _ = io.WriteString(w, "*")
			}
		}
	}
}

// readHiddenLine reads the value for `repose secrets set` from the
// terminal, echoing `*` per character (readMasked). The terminal goes
// back to how it was however the read ends; Ctrl-C exits 130. When raw
// mode is not available it falls back to the echo-off read that was here
// before (DECISIONS I-365).
func readHiddenLine() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return readEchoOffLine()
	}
	restore := func() { _ = term.Restore(fd, state) }
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			restore()
			_, _ = fmt.Fprintln(os.Stderr)
			os.Exit(ExitInterrupted)
		case <-done:
		}
	}()
	line, err := readMasked(os.Stdin, os.Stderr)
	close(done)
	signal.Stop(sig)
	restore()
	_, _ = fmt.Fprintln(os.Stderr)
	if errors.Is(err, errMaskedInterrupted) {
		return nil, silent(ExitInterrupted)
	}
	if errors.Is(err, errMaskedMultiline) {
		return nil, err
	}
	if err != nil && len(line) == 0 {
		return nil, exitf(ExitUsage, "No value given.")
	}
	return line, nil
}
