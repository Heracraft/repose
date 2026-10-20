package undo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs external programs. btrfs, mkfs.btrfs, blkid and mount go
// through it, so the backend is tested without a filesystem, the way
// hostd's volume layer is (internal/hostd/shell). It is a local interface
// rather than hostd's: the wrapper shape has no hostd in it.
type Runner interface {
	Run(ctx context.Context, argv ...string) (Result, error)
}

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
	return fmt.Sprintf("%s: exit %d: %s", e.Argv[0], e.Result.ExitCode, tail(e.Result.Stderr, 512))
}

// tail returns the last n bytes of b as a string, whole lines where possible.
func tail(b []byte, n int) string {
	if len(b) <= n {
		return strings.TrimSpace(string(b))
	}
	t := b[len(b)-n:]
	if i := bytes.IndexByte(t, '\n'); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return strings.TrimSpace(string(t))
}

// stderrHas reports whether err is a non-zero exit whose stderr contains
// any of the given sentences. btrfs has no exit code for "not a
// subvolume", so its own wording is the only signal.
func stderrHas(err error, sentences ...string) bool {
	var ee *ExitError
	if !errors.As(err, &ee) {
		return false
	}
	s := string(ee.Result.Stderr)
	for _, want := range sentences {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// Exec runs real processes.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, argv ...string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("undo: empty argv")
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
