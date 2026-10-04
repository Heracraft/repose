package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Listener names, as systemd's FileDescriptorName= and a handover's
// LISTEN_FDNAMES carry them (DECISIONS I-470, I-471).
const (
	lnSSH     = "ssh"
	lnPreview = "preview"
	lnHook    = "hook"
	lnMetrics = "metrics"
)

// inheritedListeners takes the listening sockets this process was started
// with, by name: from systemd's socket unit (LISTEN_FDS, LISTEN_FDNAMES,
// LISTEN_PID), or from the gateway handing over to this one, which sets
// no LISTEN_PID because it cannot know the pid before the fork. A
// LISTEN_PID naming another process means the variables were inherited by
// mistake, and nothing is taken. The variables are cleared either way, so
// a later child never reads them.
func inheritedListeners() (map[string]net.Listener, error) {
	defer func() {
		for _, k := range []string{"LISTEN_FDS", "LISTEN_FDNAMES", "LISTEN_PID"} {
			_ = os.Unsetenv(k)
		}
	}()
	out := map[string]net.Listener{}
	n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if n <= 0 {
		return out, nil
	}
	if pid := os.Getenv("LISTEN_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return out, nil
	}
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")
	for i := 0; i < n; i++ {
		fd := 3 + i
		syscall.CloseOnExec(fd)
		name := "unknown"
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		f := os.NewFile(uintptr(fd), name)
		ln, err := net.FileListener(f)
		_ = f.Close() // FileListener holds its own copy
		if err != nil {
			return nil, fmt.Errorf("inherited listener %s (fd %d): %w", name, fd, err)
		}
		out[name] = ln
	}
	return out, nil
}

// listenerSet is the sockets a gateway serves on. Inherited ones are used
// as they are; the rest are bound here.
type listenerSet struct {
	inherited map[string]net.Listener
}

// listen returns the listener for name: the inherited one when there is
// one, otherwise a new one on addr.
func (s *listenerSet) listen(name, addr string) (net.Listener, error) {
	if ln, ok := s.inherited[name]; ok {
		delete(s.inherited, name)
		return ln, nil
	}
	return net.Listen("tcp", addr)
}

// closeUnused closes inherited listeners nothing asked for (a socket for a
// listener this configuration has turned off).
func (s *listenerSet) closeUnused() {
	for name, ln := range s.inherited {
		_ = ln.Close()
		delete(s.inherited, name)
	}
}
