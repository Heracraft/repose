package sysdep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// RunSpec is one command guestd runs outside itself.
type RunSpec struct {
	Argv []string
	// User is "" or "root" for root, "dev" for the guest user. docs/
	// workstreams/04-guestd.md: Exec drops privileges with setpriv.
	User string
	// Dir is the working directory; empty means guestd's own.
	Dir string
	// Env replaces the environment entirely when non-nil.
	Env []string
	// Stdin is fed to the command.
	Stdin []byte
	// MaxOutput caps each of stdout and stderr. Zero means DefaultMaxOutput.
	MaxOutput int
	// OnLine, when set, is called with each line of stdout and stderr as
	// the command writes it, not at its end (DECISIONS I-1536): what
	// ApplyMenu reads apt's phases from while apt runs. It is called from
	// the reader goroutines, one line at a time, and must not block.
	OnLine func(line string)
}

// DefaultMaxOutput is the per-stream cap when a RunSpec does not set one.
const DefaultMaxOutput = 64 << 10

// GuestPATH is where a guest's commands live. guestd runs as a systemd unit,
// whose PATH does not include the NixOS system profile, so a command resolved
// against guestd's own environment would be reported missing even though the
// guest has it. Every command is resolved against the PATH it will run with,
// and this is the floor.
const GuestPATH = "/run/current-system/sw/bin:/usr/bin:/bin"

// ErofsPATH is an erofs guest's PATH: Ubuntu's, with mise's install
// directory (/usr/local/bin) and the sbin directories resize2fs, useradd
// and apt's helpers live in (DECISIONS I-1474).
const ErofsPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// RunResult is what a command produced. Stdout and Stderr are the tail of each
// stream, capped at MaxOutput.
type RunResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
}

// Runner runs a command. The real implementation forks; the fake records.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

// ExecRunner is the real Runner.
type ExecRunner struct {
	// SetprivPath overrides the setpriv binary; empty means look it up on PATH.
	SetprivPath string
}

// Run executes spec. A non-zero exit is not an error: it comes back in
// RunResult.ExitCode, because every caller reports it rather than failing.
func (r ExecRunner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if len(spec.Argv) == 0 {
		return RunResult{}, errors.New("run: empty argv")
	}
	argv := append([]string(nil), spec.Argv...)
	resolved, err := LookPath(argv[0], spec.Env)
	if err != nil {
		return RunResult{}, fmt.Errorf("run %s: %w", argv[0], err)
	}
	argv[0] = resolved
	if spec.User == "dev" {
		argv, err = r.setprivArgv(argv, spec.Env)
		if err != nil {
			return RunResult{}, err
		}
	}

	max := spec.MaxOutput
	if max <= 0 {
		max = DefaultMaxOutput
	}
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = max, max

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var outLines, errLines *lineWriter
	if spec.OnLine != nil {
		// stdout and stderr are copied by two goroutines; OnLine sees one
		// line at a time.
		var mu sync.Mutex
		one := func(l string) {
			mu.Lock()
			defer mu.Unlock()
			spec.OnLine(l)
		}
		outLines, errLines = &lineWriter{fn: one}, &lineWriter{fn: one}
		cmd.Stdout = io.MultiWriter(&stdout, outLines)
		cmd.Stderr = io.MultiWriter(&stderr, errLines)
	}
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	// Its own process group, so the context's kill reaches children too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	runErr := cmd.Run()
	if outLines != nil {
		outLines.flush()
		errLines.flush()
	}
	res := RunResult{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.truncated || stderr.truncated,
	}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("run %s: %w", argv[0], runErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("run %s: %w", argv[0], ctxErr)
	}
	return res, nil
}

// setprivArgv wraps argv so it runs as dev. docs/workstreams/04-guestd.md
// names setpriv; it is in util-linux, which every NixOS guest has.
func (r ExecRunner) setprivArgv(argv []string, env []string) ([]string, error) {
	path := r.SetprivPath
	if path == "" {
		var err error
		path, err = LookPath("setpriv", env)
		if err != nil {
			return nil, fmt.Errorf("setpriv not found, cannot drop privileges: %w", err)
		}
	}
	uid, gid := DevIdentity()
	wrapped := []string{
		path,
		"--reuid=" + strconv.Itoa(uid),
		"--regid=" + strconv.Itoa(gid),
		"--init-groups",
		"--",
	}
	return append(wrapped, argv...), nil
}

// LookPath resolves a command against the PATH it will run with: the PATH in
// env when there is one, then guestd's own, then GuestPATH. A name containing
// a separator is taken as written.
func LookPath(name string, env []string) (string, error) {
	if name == "" {
		return "", errors.New("look up command: empty name")
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	seen := map[string]bool{}
	for _, list := range []string{pathFromEnv(env), os.Getenv("PATH"), GuestPATH, ErofsPATH} {
		for _, dir := range filepath.SplitList(list) {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			candidate := filepath.Join(dir, name)
			if isExecutable(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%q not found in PATH", name)
}

func pathFromEnv(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			return v
		}
	}
	return ""
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// DevEnv is the environment a command run as dev gets: enough to find binaries
// and the home directory, and nothing carried over from guestd's own.
func DevEnv(p Paths, user string) []string {
	home, name := p.Home(), "dev"
	if user == "" || user == "root" {
		home, name = "/root", "root"
	}
	path := GuestPATH
	if p.Erofs {
		path = ErofsPATH
	}
	env := []string{
		"PATH=" + path,
		"HOME=" + home,
		"USER=" + name,
		"LOGNAME=" + name,
		"SHELL=/bin/sh",
		"TERM=dumb",
	}
	for _, k := range []string{"TZ", "LANG", "REPOSE_PROJECT", "REPOSE"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// tailBuffer keeps at most max bytes, discarding from the front, so a command
// that floods stdout cannot exhaust the guest's memory and the interesting end
// of the output survives.
type tailBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if t.max <= 0 {
		return n, nil
	}
	if len(p) > t.max {
		t.truncated = true
		p = p[len(p)-t.max:]
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.truncated = true
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return n, nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }

// lineWriter calls fn with each complete line written to it; the last
// line without a newline is passed on by flush. A line longer than
// MaxStreamLine is cut there, so one without a newline (a progress bar
// redrawn with \r) cannot grow without end.
type lineWriter struct {
	fn  func(string)
	buf []byte
}

// MaxStreamLine caps one line OnLine is given.
const MaxStreamLine = 4 << 10

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			w.buf = append(w.buf, p...)
			if len(w.buf) > MaxStreamLine {
				w.fn(string(w.buf[:MaxStreamLine]))
				w.buf = w.buf[:0]
			}
			break
		}
		w.buf = append(w.buf, p[:i]...)
		if len(w.buf) > 0 {
			if len(w.buf) > MaxStreamLine {
				w.buf = w.buf[:MaxStreamLine]
			}
			w.fn(string(w.buf))
		}
		w.buf = w.buf[:0]
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = w.buf[:0]
	}
}
