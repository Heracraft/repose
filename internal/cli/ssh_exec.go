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

	"github.com/heracraft/repose/internal/machine"
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
	// Owned marks a machine the user owns (`repose machine`, SPINE.md
	// Contract 2). There is no gateway and no certificate on that path,
	// so a connection that fails there fails for other reasons and is
	// said in other words (sshError.Error). machineTarget sets it.
	Owned bool
}

func hostTarget(slug string) sshTarget { return sshTarget{Args: []string{slug + ".repose"}} }

// host is the alias ssh was given: the last argument, since the remote
// command is not in Args.
func (t sshTarget) host() string {
	if len(t.Args) == 0 {
		return ""
	}
	return t.Args[len(t.Args)-1]
}

// sshError is a failed `ssh <target> <cmd>`: exit 255 is ssh's own
// failure (connection, authentication), anything else is the remote
// command's exit status. Its message is a clause meant to follow "Could
// not <step>: " (DECISIONS I-153); it never carries the remote command,
// which can hold paths and names from the user's tree.
type sshError struct {
	ExitCode int // -1 when ssh did not run at all
	Stderr   string
	Err      error
	// Target is what the connection was to, so a failure on a machine the
	// user owns reads as what it was: there is no gateway and no
	// certificate on that path (sshFailure).
	Target sshTarget
}

func (e *sshError) Error() string {
	detail := sshStderrDetail(e.Stderr)
	if e.ExitCode == -1 {
		return fmt.Sprintf("could not run ssh (%v)", e.Err)
	}
	if e.ExitCode != 255 {
		return withDetail(fmt.Sprintf("the command on the machine exited with status %d", e.ExitCode), detail)
	}
	if !e.Target.Owned {
		// The hosted path: ssh reaches a guest through the gateway, with a
		// certificate, and a refusal there is one of those two.
		if strings.Contains(detail, "Permission denied") {
			return withDetail("the gateway refused the SSH certificate", detail)
		}
		return withDetail("the SSH connection to the machine failed", detail)
	}
	switch classifySSH(e.Stderr) {
	case sshFailHostKey:
		// The one connection failure the user cannot get past without
		// being told which file remembers the old key.
		return fmt.Sprintf("the machine's host key is not the one it had when it was added; `ssh-keygen -R %s -f %s` forgets the old one",
			e.Target.host(), machineKnownHosts())
	case sshFailKeyRefused:
		return withDetail("the machine refused the key", detail)
	case sshFailNoAnswer:
		return withDetail("the machine did not answer", detail)
	case sshFailNoName:
		return withDetail("the machine's address does not resolve on this computer", detail)
	case sshFailNoConfig:
		return withDetail("ssh could not read the configuration repose writes for the machine", detail)
	default:
		return withDetail("the SSH connection to the machine failed", detail)
	}
}

// sshFailure is what ssh's own stderr says a failed connection was. A
// machine the user owns is silent for several reasons, and each is a
// different thing to do about it, so `repose machine ls` and every
// failure message name which one it was.
type sshFailure int

const (
	sshFailOther      sshFailure = iota
	sshFailNoAnswer              // nothing answered: the box is off, or asleep, or nothing is listening
	sshFailNoName                // the address resolves to nothing here
	sshFailKeyRefused            // the box answered and refused the login
	sshFailHostKey               // the box's host key is not the one it had
	sshFailNoConfig              // ssh could not read the config it was given
)

// classifySSH reads ssh's stderr. The host key comes first: a changed key
// refuses the login too, and the key is what has to be fixed.
func classifySSH(stderr string) sshFailure {
	switch {
	case containsAny(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED", "Host key verification failed"):
		return sshFailHostKey
	case containsAny(stderr, "Can't open user config file", "Bad configuration option", "Cannot open user config"):
		return sshFailNoConfig
	case containsAny(stderr, "Could not resolve hostname", "Name or service not known", "nodename nor servname", "Temporary failure in name resolution"):
		return sshFailNoName
	case containsAny(stderr, "Permission denied", "Too many authentication failures", "No supported authentication methods"):
		return sshFailKeyRefused
	case containsAny(stderr, "Connection refused", "Connection timed out", "Operation timed out", "No route to host",
		"Network is unreachable", "Host is down", "Connection closed by", "Connection reset by"):
		return sshFailNoAnswer
	default:
		return sshFailOther
	}
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// machineKnownHosts is where a machine's host key is remembered, as a
// message spells it. The file itself is in ~/.ssh/repose (machine.KnownHostsName).
func machineKnownHosts() string {
	return sshPathPrefix() + "/.ssh/repose/" + machine.KnownHostsName
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
	verboseSSH(t.Args, remoteCmd)
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
		return stdout.Bytes(), &sshError{ExitCode: code, Stderr: stderr.String(), Err: err, Target: t}
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
	verboseSSH(append(append([]string{}, extraArgs...), t.Args...), remoteCmd)
	noteNewerCLI()
	timingf("exec ssh (attach)")
	return execSSH("ssh", args)
}

// execAddsWait bounds execReplaceSSH's wait for sidebar adds in flight;
// an add to a running machine takes about a second.
const execAddsWait = 10 * time.Second

// execSSH is sysExec, a variable so a test can see when it would run.
var execSSH = sysExec
