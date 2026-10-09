package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// progress shows what a long command is doing (DECISIONS I-154): on a
// terminal, one live line with a spinner, the phase and its elapsed time,
// replaced by a "✓ <done>  <time>" line when the phase ends; elsewhere, one
// plain "<phase>..." line per phase, the same ✓ line when it ends, and a
// "<phase>... <time>" line every heartbeat while it runs, so a CI log
// tells a hung step from a slow one (DECISIONS I-609). It writes to
// stderr only, so stdout stays the command's result (07-cli.md §5.12).
// Every method is safe on a nil *progress, which is what tests and the
// commands that have no phases pass.
type progress struct {
	w     io.Writer
	tty   bool
	now   func() time.Time
	start time.Time
	// heartbeat is how often a phase off a terminal prints that it is
	// still running; 0 never.
	heartbeat time.Duration

	mu         sync.Mutex
	label      string // the running phase, "" between phases
	done       string // what its ✓ line says when it ends ("" prints none)
	phaseStart time.Time
	drawn      bool // a spinner line is on screen and must be cleared before any other output
	frame      int
	stop       chan struct{}
	stopped    chan struct{}
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newProgress(w io.Writer, tty bool) *progress {
	p := &progress{w: w, tty: tty, now: time.Now, heartbeat: 30 * time.Second}
	p.start = p.now()
	return p
}

// canDrawSpinner reports whether f is a terminal a spinner may redraw:
// not with TERM=dumb (Emacs shells, some IDE consoles) or
// REPOSE_NO_SPINNER=1. Asking a question is canPrompt's business, so
// turning the spinner off never stops a confirmation (DECISIONS I-614).
func canDrawSpinner(f *os.File) bool {
	if os.Getenv("TERM") == "dumb" || os.Getenv(envNoSpinner) == "1" {
		return false
	}
	return isatty(f)
}

// canPrompt reports whether stdin f is a terminal someone can answer a
// question on. /dev/null is a character device too, which is what cron,
// systemd, `ssh -n` and many CI runners give a command; a question read
// from it gets EOF, so it is not one (DECISIONS I-614).
func canPrompt(f *os.File) bool { return isatty(f) }

func isatty(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}

// Phase ends the current phase (printing its done line) and starts label.
// done is what the ✓ line says when this phase ends ("" for a phase whose
// result the caller prints itself).
func (p *progress) Phase(label, done string) { p.phase(label, done, true) }

// Resume starts label again after a line printed between its parts (run's
// `Worktree:` line inside "Starting claude"): on a terminal the spinner
// comes back; off one the start line printed once already, so a log does
// not read as two starts (I-633).
func (p *progress) Resume(label, done string) { p.phase(label, done, false) }

func (p *progress) phase(label, done string, announce bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	same := p.label != "" && p.label == label
	p.mu.Unlock()
	if same {
		// The same phase announced again (run's "Creating x" and then the
		// project's "creating" state) is one phase: one line, one ✓ (I-191).
		return
	}
	p.End()
	p.mu.Lock()
	p.label, p.done, p.phaseStart = label, done, p.now()
	if !p.tty {
		if announce {
			_, _ = fmt.Fprintf(p.w, "%s...\n", label)
		}
		if p.heartbeat <= 0 {
			p.mu.Unlock()
			return
		}
		p.stop, p.stopped = make(chan struct{}), make(chan struct{})
		stop, stopped, every := p.stop, p.stopped, p.heartbeat
		p.mu.Unlock()
		go p.beat(stop, stopped, every)
		return
	}
	p.stop, p.stopped = make(chan struct{}), make(chan struct{})
	p.drawLocked()
	stop, stopped := p.stop, p.stopped
	p.mu.Unlock()
	go func() {
		defer close(stopped)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				p.mu.Lock()
				p.frame++
				p.drawLocked()
				p.mu.Unlock()
			}
		}
	}()
}

// beat prints the running phase and its time every interval until stop:
// the off-terminal stand-in for the spinner's clock.
func (p *progress) beat(stop, stopped chan struct{}, every time.Duration) {
	defer close(stopped)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.mu.Lock()
			if p.label != "" {
				_, _ = fmt.Fprintf(p.w, "%s... %s\n", p.label, fmtElapsed(p.now().Sub(p.phaseStart)))
			}
			p.mu.Unlock()
		}
	}
}

// Relabel changes the running phase's label and done text without
// ending it: a count that moves ("Fetching 17/42 paths"). Without a
// terminal it prints nothing; the phase's line was printed once.
func (p *progress) Relabel(label, done string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.label == "" {
		return
	}
	p.label, p.done = label, done
	if p.tty {
		p.drawLocked()
	}
}

// busy reports whether a phase is running.
func (p *progress) busy() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.label != ""
}

// End finishes the current phase: on a terminal the spinner line becomes
// the ✓ line (or disappears when the phase has no done text).
func (p *progress) End() {
	if p == nil {
		return
	}
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.label == "" {
		return
	}
	p.clearLocked()
	if p.done != "" {
		_, _ = fmt.Fprintf(p.w, "✓ %s  %s\n", p.done, fmtElapsed(p.now().Sub(p.phaseStart)))
	}
	p.label, p.done = "", ""
}

// Fail ends the current phase without a ✓, before an error is printed.
func (p *progress) Fail() {
	if p == nil {
		return
	}
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	p.label, p.done = "", ""
}

// Total is the time since the command started.
func (p *progress) Total() time.Duration {
	if p == nil {
		return 0
	}
	return p.now().Sub(p.start)
}

// Write lets streamed output (the build log) pass through without
// tearing the spinner line: clear it, write, draw it again.
func (p *progress) Write(b []byte) (int, error) {
	if p == nil {
		return len(b), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLocked()
	n, err := p.w.Write(b)
	if p.label != "" && p.tty {
		p.drawLocked()
	}
	return n, err
}

func (p *progress) halt() {
	p.mu.Lock()
	stop, stopped := p.stop, p.stopped
	p.stop, p.stopped = nil, nil
	p.mu.Unlock()
	if stop != nil {
		close(stop)
		<-stopped
	}
}

func (p *progress) drawLocked() {
	if !p.tty || p.label == "" {
		return
	}
	frame := spinnerFrames[p.frame%len(spinnerFrames)]
	_, _ = fmt.Fprintf(p.w, "\r\033[K%s %s  %s", frame, p.label, fmtElapsed(p.now().Sub(p.phaseStart)))
	p.drawn = true
}

func (p *progress) clearLocked() {
	if p.drawn {
		_, _ = fmt.Fprint(p.w, "\r\033[K")
		p.drawn = false
	}
}

// fmtElapsed is "0.4s", "12s", "1m04s".
func fmtElapsed(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	default:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
}

// phaseForState is the progress label for a project state seen while
// waiting on an op, and its done text.
func phaseForState(slug, state string) (label, done string) {
	switch state {
	case "creating":
		return "Creating " + slug, "Created " + slug
	case "building":
		return "Building the configuration", "Built the configuration"
	case "starting":
		return "Booting " + slug, "Booted " + slug
	case "stopping":
		return "Stopping " + slug, "Stopped " + slug
	case "restoring":
		return "Restoring " + slug, "Restored " + slug
	case "destroying":
		return "Destroying " + slug, "Destroyed " + slug
	default:
		return "", ""
	}
}
