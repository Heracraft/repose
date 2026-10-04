//go:build !windows

package cli

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// runInputProxy runs `ssh args...` on a pty of its own and proxies the
// terminal through the input scanner (I-280). handled is false when the
// proxy could not start (stdin or stdout is not a terminal, no pty) and
// nothing has run, so the caller execs ssh as before. Once ssh ran, the
// result is its exit status: nil for 0, silent(code) otherwise, 128+N
// for a signal N, as a shell reports it.
func runInputProxy(args []string, h *dropHandler) (handled bool, err error) {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return false, nil
	}
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return false, nil
	}
	state, err := term.MakeRaw(in)
	if err != nil {
		return false, nil
	}
	restore := func() { _ = term.Restore(in, state) }
	cmd := exec.Command("ssh", args...)
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		restore()
		return false, nil
	}
	timingf("attach through the input proxy")
	stopWatch := startClipboardWatch() // Cmd+V with an image on macOS (I-341)
	defer stopWatch()
	return true, proxySession(cmd, ptmx, os.Stdin, os.Stdout, h, restore)
}

// proxySession is the proxy's life once ssh runs on ptmx: the terminal's
// output copied out, its input through the scanner, window size changes
// passed on, and signals meant for the session handed to ssh.
func proxySession(cmd *exec.Cmd, ptmx *os.File, stdin io.Reader, stdout io.Writer, h *dropHandler, restore func()) error {
	defer func() { _ = ptmx.Close() }()
	defer restore()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer signal.Stop(winch)
	defer signal.Stop(stop)

	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()

	outDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, ptmx)
		close(outDone)
	}()

	chunks := make(chan []byte, 256)
	go func() {
		defer close(chunks)
		buf := make([]byte, 32<<10)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				p := make([]byte, n)
				copy(p, buf[:n])
				chunks <- p
			}
			if err != nil {
				return
			}
		}
	}()

	sc := &inputScanner{isFile: localDropFile}
	deliver := func(acts []inputAction) {
		for _, a := range acts {
			var b []byte
			switch a.kind {
			case actFiles:
				b = h.files(a)
			case actCtrlV:
				b = h.ctrlV(a)
			default:
				b = a.raw
			}
			if _, err := ptmx.Write(b); err != nil {
				return
			}
		}
	}

	hold := time.NewTimer(time.Hour)
	hold.Stop()
	in := chunks
loop:
	for {
		select {
		case <-exited:
			break loop
		case s := <-stop:
			_ = cmd.Process.Signal(s)
		case <-winch:
			_ = pty.InheritSize(os.Stdin, ptmx)
		case p, ok := <-in:
			if !ok {
				in = nil // the terminal is gone; ssh sees its hangup through the pty's close
				continue
			}
			deliver(sc.feed(p))
		case <-hold.C:
			deliver(sc.flush())
		}
		if held, d := sc.holding(); held {
			hold.Reset(d)
		} else {
			hold.Stop()
		}
	}
	// What ssh wrote last is still in the pty; the copy ends at its EOF
	// (EIO on Linux once the last slave is closed).
	select {
	case <-outDone:
	case <-time.After(time.Second):
	}
	return proxyExit(cmd, waitErr)
}

// proxyExit turns ssh's end into the CLI's exit status.
func proxyExit(cmd *exec.Cmd, waitErr error) error {
	ps := cmd.ProcessState
	if ps == nil {
		return &exitError{code: ExitGeneric, msg: "Could not run ssh: " + waitErr.Error() + "."}
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return silent(128 + int(ws.Signal()))
	}
	if code := ps.ExitCode(); code != 0 {
		return silent(code)
	}
	return nil
}
