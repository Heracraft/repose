package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// The overlap menu (DECISIONS I-1100). The sync's exit 6 names the files
// the machine changed that the laptop changed too (I-573) and, until
// now, one flag to type the command again with. On a terminal it offers
// the three answers instead, one key each.

// overlapChoice is the key pressed at the menu.
type overlapChoice byte

const (
	overlapStop    overlapChoice = 0   // Enter, q, Esc, EOF
	overlapLaptop  overlapChoice = 'l' // the laptop's copy; the machine's is stashed
	overlapMachine overlapChoice = 'm' // the machine's copy; the rest is synced
	overlapStash   overlapChoice = 's' // as --stash-machine
)

// overlapMenuLines is what follows the file list: one line per key, then
// the question. A submodule among the overlapping paths leaves `m` out:
// a submodule's copy cannot be put back from a stash without a commit
// of its own (I-1100).
func overlapMenuLines(hasSubmodule bool) string {
	var b strings.Builder
	b.WriteString("  l  use the laptop's copy; the machine's goes to its git stash\n")
	if !hasSubmodule {
		b.WriteString("  m  keep the machine's copy; sync the rest\n")
	}
	b.WriteString("  s  move all of the machine's changes to its git stash, then sync\n")
	if hasSubmodule {
		b.WriteString("Choose l or s, or Enter to stop: ")
	} else {
		b.WriteString("Choose l, m or s, or Enter to stop: ")
	}
	return b.String()
}

// overlapMenu prints the refusal and the menu on w and reads one key
// from r, which the caller has put in raw mode. Every line ends CR LF,
// as the logins list does: raw mode turns off the terminal's own
// CR-for-LF, and a bare LF leaves each line further right than the one
// above it. The key is echoed. l, m and s act at once, in either case;
// Enter, q and Esc stop with overlapStop, and so does the input ending;
// Ctrl-C is errPromptInterrupted. Any other key is ignored.
func overlapMenu(r io.Reader, w io.Writer, d *dirtyTreeError) (overlapChoice, error) {
	_, _ = io.WriteString(w, strings.ReplaceAll(d.list()+overlapMenuLines(d.hasSubmodule()), "\n", "\r\n"))
	br := bufio.NewReader(r)
	for {
		c, err := br.ReadByte()
		if err != nil {
			_, _ = io.WriteString(w, "\r\n")
			return overlapStop, nil
		}
		switch c {
		case 'l', 'L', 's', 'S':
			_, _ = fmt.Fprintf(w, "%c\r\n", c)
			return overlapChoice(c | 0x20), nil
		case 'm', 'M':
			if d.hasSubmodule() {
				continue
			}
			_, _ = fmt.Fprintf(w, "%c\r\n", c)
			return overlapMachine, nil
		case '\r', '\n', 'q', 'Q', 0x1b:
			_, _ = io.WriteString(w, "\r\n")
			return overlapStop, nil
		case 0x03:
			_, _ = io.WriteString(w, "\r\n")
			return overlapStop, errPromptInterrupted
		}
	}
}

// canAskOverlap is whether there is a terminal to show the menu on, as
// I-1065's question asks: a real terminal on both stdin and stderr.
// Tests replace it.
var canAskOverlap = func() bool { return canPrompt(os.Stdin) && isatty(os.Stderr) }

// askOverlap runs the menu on the real terminal; tests replace it.
var askOverlap = func(w io.Writer, d *dirtyTreeError) (overlapChoice, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		// No raw mode, no one-key menu: the refusal stands as it is.
		return overlapStop, errNoOverlapMenu
	}
	ch, err := overlapMenu(os.Stdin, w, d)
	_ = term.Restore(fd, state)
	return ch, err
}

// errNoOverlapMenu is a terminal that would not go into raw mode: the
// sync falls back to the refusal and exit 6, with the text that names
// --stash-machine.
var errNoOverlapMenu = fmt.Errorf("no raw terminal for the overlap menu")

// overlapAsker is SyncOptions.Choose for a command that may ask, or nil
// when there is nobody to ask: --json (a script reads the exit code),
// no terminal, a flag that already decided, or `repose run`'s first-only
// sync, which syncs only into a machine with no commit and so cannot
// overlap (I-367).
func overlapAsker(e *Env, opts RunOptions) func(*dirtyTreeError) (overlapChoice, error) {
	if e.JSON || !opts.Sync || opts.StashRemote || opts.DiscardRemote || !canAskOverlap() {
		return nil
	}
	return func(d *dirtyTreeError) (overlapChoice, error) { return askOverlap(e.ErrOut, d) }
}
