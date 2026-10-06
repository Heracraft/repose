package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sshTarget is what runSSH needs to reach a guest: the args ssh needs
// after "ssh" and before the remote command. In production this is just
// the Host alias from ~/.ssh/repose/config ("todo-app.repose"), or that
// alias with `-F ~/.ssh/repose/config` when the Include line in
// ~/.ssh/config is not effective (I-151); tests point it at a fake guest
// with -o overrides instead of the real gateway.
type sshTarget struct {
	Args []string // e.g. []string{"todo-app.repose"}
	// Checkout is the machine's other checkout this command works in
	// (`repose run --on`, DECISIONS I-480), "" for the checkout itself.
	// Every guest script that finds the checkout takes it from here.
	Checkout string
}

func hostTarget(slug string) sshTarget { return sshTarget{Args: []string{slug + ".repose"}} }

// sshError is a failed `ssh <target> <cmd>`: exit 255 is ssh's own
// failure (connection, authentication), anything else is the remote
// command's exit status. Its message is a clause meant to follow "Could
// not <step>: " (DECISIONS I-153); it never carries the remote command,
// which can hold paths and names from the user's tree.
type sshError struct {
	ExitCode int // -1 when ssh did not run at all
	Stderr   string
	Err      error
}

func (e *sshError) Error() string {
	detail := sshStderrDetail(e.Stderr)
	switch {
	case e.ExitCode == -1:
		return fmt.Sprintf("could not run ssh (%v)", e.Err)
	case e.ExitCode == 255 && strings.Contains(detail, "Permission denied"):
		return withDetail("the gateway refused the SSH certificate", detail)
	case e.ExitCode == 255:
		return withDetail("the SSH connection to the guest failed", detail)
	default:
		return withDetail(fmt.Sprintf("the command in the guest exited with status %d", e.ExitCode), detail)
	}
}

func (e *sshError) Unwrap() error { return e.Err }

func withDetail(s, detail string) string {
	if detail == "" {
		return s
	}
	return s + " (" + detail + ")"
}

// sshStderrDetail keeps the last two meaningful stderr lines, dropping
// ssh's routine chatter, so a failure reads as one sentence.
func sshStderrDetail(stderr string) string {
	var keep []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "Warning: Permanently added") || strings.HasPrefix(l, "hint:") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 2 {
		keep = keep[len(keep)-2:]
	}
	return strings.Join(keep, "; ")
}

// stepFailed is the one shape every guest-side failure reaches the user
// in: "Could not <step>: <why>." plus, when given, the next thing to try.
func stepFailed(step string, err error, next string) error {
	// Errors that already have their own rendering (exit codes 3, 7, 8
	// and the api's code: message) pass through untouched.
	var ee *exitError
	var apiErr *APIError
	var nl *notLoggedInError
	var ur *unreachableError
	if errors.As(err, &ee) || errors.As(err, &apiErr) || errors.As(err, &nl) || errors.As(err, &ur) {
		return err
	}
	msg := fmt.Sprintf("Could not %s: %v.", step, err)
	if next != "" {
		msg += " " + next
	}
	return &exitError{code: ExitGeneric, msg: msg, cause: err}
}

// sshWaitDelay bounds how long runSSH waits for ssh's output pipes to
// close after ssh itself has exited. A ControlPersist master forked by
// the first connection must not hold them open (OpenSSH points its
// stdio at /dev/null when it detaches), but an old client that did would
// otherwise hang the CLI forever.
const sshWaitDelay = 3 * time.Second

// runSSH runs one remote command over the project's SSH connection
// (multiplexed through the ControlMaster when the config enables it,
// I-149), returning stdout. A non-zero exit is an *sshError.
func runSSH(ctx context.Context, t sshTarget, remoteCmd string, stdin io.Reader) ([]byte, error) {
	args := append(append([]string{}, t.Args...), remoteCmd)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = stdin
	cmd.WaitDelay = sshWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	err := cmd.Run()
	if timingEnabled() {
		timingf("ssh %s out=%dB exit=%d %dms", sshLabel(remoteCmd), stdout.Len(), cmd.ProcessState.ExitCode(), time.Since(started).Milliseconds())
	}
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		err = nil
	}
	if err != nil {
		code := -1
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			code = xe.ExitCode()
		}
		return stdout.Bytes(), &sshError{ExitCode: code, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// closeMaster ends the persisted multiplexed connection to slug (I-149's
// ControlPersist 10m), best effort. After a stop, destroy or restore that
// connection leads to a guest that is gone: the next command's sessions
// ride it and fail ("the SSH connection to the guest failed", 2026-09-23),
// and a restored project with the same slug has the same ControlPath
// (I-188). Tests (TargetFor set) and Windows have no master.
func closeMaster(ctx context.Context, e *Env, slug string) {
	if e.TargetFor != nil || goos() == "windows" {
		return
	}
	sd, err := sshDir()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-F", filepath.Join(sd, "config"), "-O", "exit", slug+".repose")
	cmd.WaitDelay = time.Second
	_ = cmd.Run() // no master is the common case, and ssh says so on stderr, which is discarded
}

// runSSHOK is runSSH for commands whose output is not needed, only
// success (07-cli.md §5.5 step 4: `ssh <slug>.repose true`).
func runSSHOK(ctx context.Context, t sshTarget, remoteCmd string) error {
	_, err := runSSH(ctx, t, remoteCmd, nil)
	return err
}

// execReplaceSSH is the foreground, process-replacing ssh of `repose run`
// step 8 and `repose attach`/`repose open`: the CLI process becomes ssh so
// signals and the terminal behave exactly like plain ssh (07-cli.md §5.5,
// checklist "the CLI process is replaced").
func execReplaceSSH(t sshTarget, extraArgs []string, remoteCmd string) error {
	args := append(append([]string{}, extraArgs...), t.Args...)
	if remoteCmd != "" {
		args = append(args, remoteCmd)
	}
	// The exec ends every goroutine, and with them any laptop herdr
	// `machine add` still running (herdrSyncFor): give those a moment.
	waitHerdrAdds(execAddsWait)
	timingf("exec ssh (attach)")
	return execSSH("ssh", args)
}

// execAddsWait bounds execReplaceSSH's wait for sidebar adds in flight;
// an add to a running machine takes about a second.
const execAddsWait = 10 * time.Second

// execSSH is sysExec, a variable so a test can see when it would run.
var execSSH = sysExec
