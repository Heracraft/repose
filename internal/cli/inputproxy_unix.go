//go:build !windows

package cli

import (
	"context"
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
//
// With re set, an ssh that ends with 255 (the connection dropped) is
// started again once re says the machine answers (I-469): the terminal
// stays in raw mode and keeps one reader across the attaches.
func runInputProxy(args []string, h *dropHandler, re *reattacher) (handled bool, err error) {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return false, nil
	}
	if _, err := pty.GetsizeFull(os.Stdin); err != nil {
		return false, nil
	}
	state, err := term.MakeRaw(in)
	if err != nil {
		return false, nil
	}
	defer func() { _ = term.Restore(in, state) }()
	stopWatch := startClipboardWatch() // Cmd+V with an image on macOS (I-341)
	defer stopWatch()
	var input <-chan []byte
	start := func() <-chan []byte {
		// Only once ssh runs: a proxy that cannot start reads nothing.
		if input == nil {
			input = readInput(os.Stdin)
		}
		return input
	}
	return attachLoop(args, h, re, start, os.Stdout, func() (*pty.Winsize, error) { return pty.GetsizeFull(os.Stdin) })
}

// attachLoop is the proxy's attaches: ssh on a pty, proxied until it ends,
// and again after a dropped connection while re says so. input gives the
// terminal's input once the first ssh runs; size is the terminal's size
// at each start. started is false only when the first ssh could not
// start.
func attachLoop(args []string, h *dropHandler, re *reattacher, input func() <-chan []byte, out io.Writer, size func() (*pty.Winsize, error)) (started bool, err error) {
	for attempt := 0; ; attempt++ {
		ws, err := size()
		if err != nil {
			return attempt > 0, err
		}
		if attempt > 0 && re.args != nil {
			args = re.args
		}
		cmd := exec.Command("ssh", args...)
		ptmx, err := pty.StartWithSize(cmd, ws)
		if err != nil {
			if attempt == 0 {
				return false, nil
			}
			return true, err
		}
		in := input()
		timingf("attach through the input proxy")
		began := time.Now()
		err = proxySession(cmd, ptmx, in, out, h)
		if re == nil || !connectionLost(cmd) {
			return true, err
		}
		if !re.again(context.Background(), out, in, time.Since(began), attempt > 0) {
			return true, err
		}
	}
}

// readInput reads the terminal for as long as the process lives, one
// chunk per read, and closes the channel when the terminal is gone.
func readInput(stdin io.Reader) <-chan []byte {
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
	return chunks
}

// proxySession is the proxy's life once ssh runs on ptmx: the terminal's
// output copied out, its input through the scanner, window size changes
// passed on, and signals meant for the session handed to ssh.
func proxySession(cmd *exec.Cmd, ptmx *os.File, chunks <-chan []byte, stdout io.Writer, h *dropHandler) error {
	defer func() { _ = ptmx.Close() }()

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
