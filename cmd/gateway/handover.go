package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A handover replaces the running gateway with a new binary without
// dropping a session (DECISIONS I-471). `systemctl reload gateway` runs
// `gateway handover` from the new build, which asks the running gateway
// over its control socket to start that build on the same listening
// sockets. Once the new process says it serves, the old one names it the
// unit's main process, stops accepting, and keeps its open relays until
// each ends on its own. SSH keys cannot move between processes, so the
// old relays stay where they are: a drain, not a migration.

const (
	// controlDefault is the control socket, in the unit's RuntimeDirectory.
	controlDefault = "/run/repose-gateway/control.sock"
	// readyEnv names the fd a handed-over gateway writes "ready" to once
	// it serves on every listener.
	readyEnv = "REPOSE_GATEWAY_READY_FD"
	// handoverReadyTimeout is how long the old gateway waits for the new
	// one; past it the new one is killed and the old one serves on.
	handoverReadyTimeout = 30 * time.Second
)

type handoverRequest struct {
	Exe string   `json:"exe"`
	Env []string `json:"env"`
}

type handoverReply struct {
	PID   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

// namedListener is one socket to pass on.
type namedListener struct {
	name string
	ln   net.Listener
}

// control serves the control socket of one gateway process.
type control struct {
	ln  *net.UnixListener
	log *slog.Logger
	// listeners are the sockets a handover passes on, read when one runs.
	listeners func() []namedListener

	mu   sync.Mutex
	busy bool // a handover is running or has succeeded
	// handedOver is closed once a new gateway serves and is the unit's
	// main process.
	handedOver chan struct{}
}

// listenControl binds the control socket at path, replacing the socket of
// the gateway this one takes over from (which keeps its own descriptor,
// unreachable). Only the gateway's own user may connect.
func listenControl(path string, log *slog.Logger, listeners func() []namedListener) (*control, error) {
	_ = os.Remove(path)
	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("control socket: %w", err)
	}
	// The next gateway binds the same path; closing ours must not take
	// its socket file away.
	ln.SetUnlinkOnClose(false)
	return &control{ln: ln, log: log, listeners: listeners, handedOver: make(chan struct{})}, nil
}

// serve answers handover requests until ctx ends or the handover is done.
func (c *control) serve(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
		case <-c.handedOver:
		}
		_ = c.ln.Close()
	}()
	for {
		conn, err := c.ln.AcceptUnix()
		if err != nil {
			return
		}
		go c.answer(conn)
	}
}

func (c *control) answer(conn *net.UnixConn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handoverReadyTimeout + 15*time.Second))
	reply := func(r handoverReply) { _ = json.NewEncoder(conn).Encode(r) }
	if uid, err := peerUID(conn); err != nil || uid != os.Getuid() {
		reply(handoverReply{Error: "the control socket answers the gateway's own user only"})
		return
	}
	var req handoverRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		reply(handoverReply{Error: "bad request: " + err.Error()})
		return
	}
	pid, err := c.handover(req)
	if err != nil {
		c.log.Error("handover failed", "event", "handover", "result", "failed", "err", err.Error())
		reply(handoverReply{Error: err.Error()})
		return
	}
	reply(handoverReply{PID: pid})
	close(c.handedOver)
}

// handover starts the new gateway on this one's sockets and, once it
// serves, makes it systemd's main process. On any failure the new process
// is gone and this one carries on as if nothing happened.
func (c *control) handover(req handoverRequest) (int, error) {
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return 0, errors.New("a handover is already running or done")
	}
	c.busy = true
	c.mu.Unlock()
	ok := false
	defer func() {
		if !ok {
			c.mu.Lock()
			c.busy = false
			c.mu.Unlock()
		}
	}()

	if req.Exe == "" {
		return 0, errors.New("bad request: no executable")
	}
	var names []string
	var files []*os.File
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	for _, nl := range c.listeners() {
		fl, ok := nl.ln.(interface{ File() (*os.File, error) })
		if !ok {
			return 0, fmt.Errorf("listener %s cannot be passed on", nl.name)
		}
		f, err := fl.File()
		if err != nil {
			return 0, fmt.Errorf("listener %s: %w", nl.name, err)
		}
		names = append(names, nl.name)
		files = append(files, f)
	}
	c.log.Info("handover starting", "event", "handover", "result", "starting")
	cmd, err := startSuccessor(req, names, files)
	if err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := sdNotify("MAINPID=" + strconv.Itoa(pid)); err != nil {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("tell systemd the new main process: %w", err)
	}
	if err := sdBarrier(5 * time.Second); err != nil {
		// systemd has the MAINPID or will; without the barrier the old
		// process waits a moment before it may exit (drainFloor).
		c.log.Warn("notify barrier failed", "event", "handover", "err", err.Error())
	}
	ok = true
	c.log.Info("handed over", "event", "handover", "result", "ok", "pid", pid)
	return pid, nil
}

// startSuccessor runs the new gateway with the sockets as LISTEN_FDS and
// waits for it to say it serves. The environment is the new unit's, sent
// by `gateway handover`, so a changed Environment= takes effect; what
// belongs to this process (the notify socket, the journal stream) is this
// process's.
func startSuccessor(req handoverRequest, names []string, files []*os.File) (*exec.Cmd, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	drop := map[string]bool{"LISTEN_FDS": true, "LISTEN_FDNAMES": true, "LISTEN_PID": true, "NOTIFY_SOCKET": true, "JOURNAL_STREAM": true, "MAINPID": true, readyEnv: true}
	var env []string
	for _, kv := range req.Env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			env = append(env, kv)
		}
	}
	for _, k := range []string{"NOTIFY_SOCKET", "JOURNAL_STREAM"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	env = append(env,
		"LISTEN_FDS="+strconv.Itoa(len(files)),
		"LISTEN_FDNAMES="+strings.Join(names, ":"),
		readyEnv+"="+strconv.Itoa(3+len(files)),
	)
	cmd := exec.Command(req.Exe, "serve")
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.ExtraFiles = append(append([]*os.File{}, files...), w)
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("start the new gateway: %w", err)
	}
	_ = w.Close()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // also reaps it, should it end before this process
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(r).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	select {
	case s := <-line:
		if s == "ready" {
			return cmd, nil
		}
		_ = cmd.Process.Kill() // it closed the pipe without the word: it is ending
		return nil, fmt.Errorf("the new gateway exited before it served: %v", <-exited)
	case err := <-exited:
		return nil, fmt.Errorf("the new gateway exited before it served: %v", err)
	case <-time.After(handoverReadyTimeout):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("the new gateway did not serve within %s", handoverReadyTimeout)
	}
}

// signalReady tells a handing-over gateway that this one serves.
func signalReady() {
	v := os.Getenv(readyEnv)
	_ = os.Unsetenv(readyEnv)
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return
	}
	f := os.NewFile(uintptr(fd), "ready")
	_, _ = f.WriteString("ready\n")
	_ = f.Close()
}

func peerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if serr != nil {
		return -1, serr
	}
	return int(cred.Uid), nil
}

// handoverMain is `gateway handover`: ExecReload of the unit. It asks the
// running gateway to start this binary, with this environment, and waits
// until it has.
func handoverMain() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path := env("GATEWAY_CONTROL", controlDefault)
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return fmt.Errorf("no gateway answers on %s (%v). A gateway from before handovers (DECISIONS I-471) cannot hand over; `systemctl restart gateway` once, which drops open sessions", path, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handoverReadyTimeout + 30*time.Second))
	if err := json.NewEncoder(conn).Encode(handoverRequest{Exe: exe, Env: os.Environ()}); err != nil {
		return err
	}
	var reply handoverReply
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return fmt.Errorf("no answer from the running gateway: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("the running gateway refused the handover and serves on: %s", reply.Error)
	}
	fmt.Printf("gateway handed over to pid %d; the old process serves its open sessions until they end\n", reply.PID)
	return nil
}
