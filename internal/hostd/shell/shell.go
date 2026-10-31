// Package shell is the one place hostd runs external programs. Everything
// that touches LVM, nftables, tc, systemd or nix goes through a Runner, so
// the state machine is tested against Fake without a host.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Result is what a finished process left behind.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExitError is returned by Run when the process exits non-zero.
type ExitError struct {
	Argv   []string
	Result Result
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s: exit %d: %s", e.Argv[0], e.Result.ExitCode, Tail(e.Result.Stderr, 512))
}

// Proc is a started process whose output the caller consumes.
type Proc interface {
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	// Wait blocks until exit. It returns *ExitError on non-zero exit.
	Wait() error
	// Kill terminates the process.
	Kill() error
}

// Runner runs programs. Run collects output; Start streams it.
type Runner interface {
	Run(ctx context.Context, argv ...string) (Result, error)
	Start(ctx context.Context, stdin io.Reader, argv ...string) (Proc, error)
}

// Tail returns the last n bytes of b as a string, whole lines where possible.
func Tail(b []byte, n int) string {
	if len(b) <= n {
		return strings.TrimSpace(string(b))
	}
	t := b[len(b)-n:]
	if i := bytes.IndexByte(t, '\n'); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return strings.TrimSpace(string(t))
}

// Exec runs real processes.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, argv ...string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("shell: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 137 // killed by signal
		}
		return res, &ExitError{Argv: argv, Result: res}
	default:
		return res, fmt.Errorf("%s: %w", argv[0], err)
	}
}

type execProc struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
	argv   []string
}

func (p *execProc) Stdout() io.ReadCloser { return p.stdout }
func (p *execProc) Stderr() io.ReadCloser { return p.stderr }
func (p *execProc) Kill() error           { return p.cmd.Process.Kill() }
func (p *execProc) Wait() error {
	err := p.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code < 0 {
			code = 137
		}
		// Result.Stderr stays empty here on purpose. A caller that
		// wants the output reads the pipe to EOF and then calls Wait,
		// which is what os/exec requires ("it is incorrect to call Wait
		// before all reads from the pipe have completed"), and wraps
		// the error itself. Capturing it here was tried and reverted:
		// the capture raced the caller's own reader and lost the tail
		// in 10 of 10 runs as soon as that reader was slow (I-926).
		return &ExitError{Argv: p.argv, Result: Result{ExitCode: code}}
	}
	return err
}

// Start implements Runner.
func (Exec) Start(ctx context.Context, stdin io.Reader, argv ...string) (Proc, error) {
	if len(argv) == 0 {
		return nil, errors.New("shell: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdout pipe: %w", argv[0], err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stderr pipe: %w", argv[0], err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	return &execProc{cmd: cmd, stdout: stdout, stderr: stderr, argv: argv}, nil
}

// Script is one scripted response of a Fake: when the recorded argv starts
// with Prefix, Handle decides the result. A nil Handle returns Result and
// Err as given.
type Script struct {
	Prefix []string
	Result Result
	Err    error
	Handle func(argv []string) (Result, error)
}

// Fake records every invocation and answers from its scripts. Unscripted
// commands succeed with empty output, so tests only script what matters.
type Fake struct {
	mu      sync.Mutex
	Scripts []Script
	Calls   [][]string
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, argv ...string) (Result, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, append([]string(nil), argv...))
	scripts := append([]Script(nil), f.Scripts...)
	f.mu.Unlock()
	for _, s := range scripts {
		if hasPrefix(argv, s.Prefix) {
			if s.Handle != nil {
				return s.Handle(argv)
			}
			if s.Err != nil {
				return s.Result, s.Err
			}
			if s.Result.ExitCode != 0 {
				return s.Result, &ExitError{Argv: argv, Result: s.Result}
			}
			return s.Result, nil
		}
	}
	return Result{}, nil
}

type fakeProc struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
	err    error
}

func (p *fakeProc) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeProc) Stderr() io.ReadCloser { return p.stderr }
func (p *fakeProc) Wait() error           { return p.err }
func (p *fakeProc) Kill() error           { return nil }

// Start implements Runner; the scripted stdout and stderr are streamed.
func (f *Fake) Start(ctx context.Context, stdin io.Reader, argv ...string) (Proc, error) {
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin) // a fake consumer; the bytes are inspected by the fake streamer, not here
	}
	res, err := f.Run(ctx, argv...)
	var ee *ExitError
	if err != nil && !errors.As(err, &ee) {
		return nil, err
	}
	return &fakeProc{
		stdout: io.NopCloser(bytes.NewReader(res.Stdout)),
		stderr: io.NopCloser(bytes.NewReader(res.Stderr)),
		err:    err,
	}, nil
}

// CallsWithPrefix returns the recorded invocations starting with prefix.
func (f *Fake) CallsWithPrefix(prefix ...string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.Calls {
		if hasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// Reset clears recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.Calls = nil
	f.mu.Unlock()
}

func hasPrefix(argv, prefix []string) bool {
	if len(prefix) > len(argv) {
		return false
	}
	for i := range prefix {
		if argv[i] != prefix[i] {
			return false
		}
	}
	return true
}
