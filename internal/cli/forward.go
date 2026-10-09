package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Auto-forward (I-199): while a CLI session is attached, every port the
// guest starts listening on is forwarded to the same port on the laptop's
// localhost, on the command's ControlMaster, and cancelled when it closes.
// The session helper runs it. Detection is `ss -Hltn` over the mux every
// second: a stock tool, no guestd change, no api. Output goes inside tmux
// only: a message per new forward, and the live list in the session's
// status-right while any CLI forwards.

// forwardPoll is how often the guest's listeners are read.
const forwardPoll = time.Second

// forwardHeartbeat is how often a helper re-stamps its entry in the
// guest's forward list; entries older than forwardStale belong to a CLI
// that is gone (a laptop that slept) and are ignored.
const (
	forwardHeartbeat = 20 * time.Second
	forwardStale     = 60 * time.Second
)

// forwardRetry is how long a port that could not be forwarded waits
// before it is tried again.
const forwardRetry = 30 * time.Second

// Forwards that appear together (a test suite's servers, a compose file)
// are announced in one message, not one four-second message each
// (DECISIONS I-324): a forward waits until a poll finds no new one after
// it, forwardQuiet, or at most forwardCoalesceMax since the first.
const (
	forwardQuiet       = 900 * time.Millisecond
	forwardCoalesceMax = 5 * time.Second
	// forwardListMax is how many ports one message names.
	forwardListMax = 8
)

// forwardPortless is portless's proxy port: never remapped silently,
// because the URLs portless prints name it.
const forwardPortless = 1355

// forwardPlatformPorts are the guest's own listeners, never forwarded:
// the desktop's noVNC, websockify and VNC (guest-conventions.md "Ports"
// and "Desktop"; `repose browser` forwards 6080 itself), the agents'
// browser's DevTools endpoint, the port behind it and the bridge's tunnel
// end (9224, 9225, 9226: no laptop process may drive the guest's logged-in
// browser, I-246; the tunnel end leads back to the laptop, I-296), and the
// name-resolution protocols a system service may answer on above 1024,
// LLMNR (5355) and mDNS (5353): systemd-resolved listened on 0.0.0.0:5355
// and every status bar showed ⇄ 5355 (DECISIONS I-215). Ports under 1024
// (ssh, the DNS stub) are never forwarded at all. The rule is by port, not
// by owner: a container the dev user publishes is served by root's
// docker-proxy and must still forward.
var forwardPlatformPorts = map[int]bool{6080: true, 6081: true, 5900: true, 9224: true, 9225: true, bridgeGuestPort: true, 5353: true, 5355: true}

// forwardDisabled is REPOSE_NO_FORWARD=1, the one knob (15 §5.5).
const forwardEnvOff = envNoForward

// guestListener is one forwardable listener: the port, and the address to
// reach it at from the guest's side of the tunnel.
type guestListener struct {
	Port int
	Host string // "127.0.0.1" or "::1"
}

// parseListeners reads `ss -Hltn` output. It keeps listeners on the
// loopback and wildcard addresses (127.0.0.1, 0.0.0.0, ::1, ::, *), ports
// from 1024 up, minus the platform's.
func parseListeners(out string) map[int]guestListener {
	ls := map[int]guestListener{}
	for _, line := range strings.Split(out, "\n") {
		l, ok := parseListenerLine(line)
		if !ok || l.Port < 1024 || forwardPlatformPorts[l.Port] {
			continue
		}
		// An IPv4 listener wins over a v6-only loopback one on the same port.
		if prev, ok := ls[l.Port]; ok && prev.Host == "127.0.0.1" {
			continue
		}
		ls[l.Port] = l
	}
	return ls
}

// parseListenerLine reads one `ss -Hltn` line: the port, and the address
// that reaches the listener from the guest's side of the tunnel. A
// wildcard listener (0.0.0.0, ::, *) and a 127.0.0.1 one are reached at
// 127.0.0.1; a listener on ::1 alone only at ::1 (Vite's default where
// localhost resolves to ::1 first). Other addresses are not forwardable.
func parseListenerLine(line string) (guestListener, bool) {
	f := strings.Fields(line)
	if len(f) < 4 {
		return guestListener{}, false
	}
	local := f[3]
	i := strings.LastIndex(local, ":")
	if i < 0 {
		return guestListener{}, false
	}
	host, portS := local[:i], local[i+1:]
	port, err := strconv.Atoi(portS)
	if err != nil || port < 1 || port > 65535 {
		return guestListener{}, false
	}
	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "0.0.0.0", "*", "::", "::ffff:127.0.0.1":
		return guestListener{Port: port, Host: "127.0.0.1"}, true
	case "::1":
		return guestListener{Port: port, Host: "::1"}, true
	}
	return guestListener{}, false
}

// forwarder keeps the laptop's forwards equal to the guest's listeners.
type forwarder struct {
	t    sshTarget
	slug string
	id   string // this helper's entry on the machine's forward list

	fwd map[int]forwardEntry // guest port -> the laptop's side
	// failed holds guest ports no laptop port could be forwarded to, and
	// when; they are tried again after forwardRetry, not every poll.
	failed map[int]time.Time
	// localFree reports whether a laptop port can be bound; a variable so
	// a test can hold ports.
	localFree func(port int) bool
	// say shows one line in tmux.
	say func(msg string)
	// pending is the same-number forwards not announced yet, and when the
	// first and the newest of them were made.
	pending                 []int
	pendingFirst, pendingAt time.Time
	// listeners and ctl are ss over the mux and `ssh -O`; variables so a
	// test on a machine full of its own listeners can say which exist.
	listeners func(ctx context.Context) (string, error)
	ctl       func(ctx context.Context, op, spec string) error
	// master is the running ControlMaster's pid, "" when none answers.
	// The forwards live in that process: when the connection drops and
	// the attach connects again (I-469), a new master has none of them.
	master   func(ctx context.Context) string
	masterID string
}

func newForwarder(t sshTarget, slug string, say func(string)) *forwarder {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	f := &forwarder{t: t, slug: slug, id: hex.EncodeToString(b), fwd: map[int]forwardEntry{}, failed: map[int]time.Time{}, localFree: laptopPortFree, say: say}
	f.listeners = func(ctx context.Context) (string, error) {
		out, err := runSSH(ctx, t, "ss -Hltn", nil)
		return string(out), err
	}
	f.ctl = f.control
	f.master = func(ctx context.Context) string { return masterPID(ctx, t) }
	return f
}

func (f *forwarder) masterPID(ctx context.Context) string {
	if f.master == nil {
		return ""
	}
	return f.master(ctx)
}

// masterPID is the pid `ssh -O check` reports ("Master running
// (pid=N)"), "" when no master answers.
func masterPID(ctx context.Context, t sshTarget) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", append([]string{"-O", "check"}, t.Args...)...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(string(out), "(pid=")
	if !ok {
		return ""
	}
	pid, _, _ := strings.Cut(rest, ")")
	return pid
}

type forwardEntry struct {
	Local int
	Host  string // the machine address the forward reaches
}

// laptopPortFree tries to bind the port on every address a laptop server
// may hold it on: both loopbacks and both wildcards. A vite on macOS binds
// ::1 only, and 127.0.0.1 is then free, yet the browser's "localhost"
// reaches the laptop's server and not the forward. An address the laptop
// does not have (no IPv6) says nothing; only "in use" means taken.
func laptopPortFree(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0", "::"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || host == "127.0.0.1" {
				return false
			}
			continue
		}
		_ = l.Close()
	}
	return true
}

// sync reads the guest's listeners once and adds and cancels forwards to
// match. It reports whether the set changed.
func (f *forwarder) sync(ctx context.Context) (bool, error) {
	out, err := f.listeners(ctx)
	if err != nil {
		return false, err
	}
	want := parseListeners(out)
	changed := false
	readd := map[int]bool{}
	if m := f.masterPID(ctx); m != "" && m != f.masterID {
		if f.masterID != "" {
			// A new connection: the old master took its forwards with
			// it. Add them again, quietly; they were announced once.
			for gp := range f.fwd {
				readd[gp] = true
				delete(f.fwd, gp)
				changed = true
			}
		}
		f.masterID = m
	}
	for gp, fe := range f.fwd {
		if l, ok := want[gp]; ok && l.Host == fe.Host {
			continue
		}
		f.cancel(ctx, gp, fe)
		delete(f.fwd, gp)
		changed = true
	}
	for gp := range f.failed {
		if _, ok := want[gp]; !ok {
			delete(f.failed, gp) // gone; a new listener there is tried at once
		}
	}
	for _, gp := range sortedPorts(want) {
		if _, ok := f.fwd[gp]; ok {
			continue
		}
		if at, ok := f.failed[gp]; ok && time.Since(at) < forwardRetry {
			continue
		}
		lp, ok := f.add(ctx, want[gp])
		if !ok {
			f.failed[gp] = time.Now()
			continue
		}
		delete(f.failed, gp)
		f.fwd[gp] = forwardEntry{Local: lp, Host: want[gp].Host}
		changed = true
		switch {
		case readd[gp]:
		case lp == gp:
			if len(f.pending) == 0 {
				f.pendingFirst = time.Now()
			}
			f.pending = append(f.pending, gp)
			f.pendingAt = time.Now()
		case gp == forwardPortless:
			f.say(fmt.Sprintf("%d is taken on your laptop (portless?); %s's portless is on localhost:%d", gp, f.slug, lp))
		default:
			f.say(fmt.Sprintf("⇄ localhost:%d → :%d (%d is taken on your laptop)", lp, gp, gp))
		}
	}
	return changed, nil
}

// announce says the pending forwards once no new one came for
// forwardQuiet (or the first has waited forwardCoalesceMax): one port as
// before, several as one line. A port whose listener went before the
// message is not named.
func (f *forwarder) announce(now time.Time) {
	if len(f.pending) == 0 || now.Sub(f.pendingAt) < forwardQuiet && now.Sub(f.pendingFirst) < forwardCoalesceMax {
		return
	}
	var ports []int
	for _, gp := range f.pending {
		if fe, ok := f.fwd[gp]; ok && fe.Local == gp {
			ports = append(ports, gp)
		}
	}
	f.pending = nil
	sort.Ints(ports)
	switch len(ports) {
	case 0:
	case 1:
		f.say(fmt.Sprintf("⇄ localhost:%d → :%d", ports[0], ports[0]))
	default:
		names := make([]string, 0, forwardListMax+1)
		for i, p := range ports {
			if i == forwardListMax {
				names = append(names, "…")
				break
			}
			names = append(names, strconv.Itoa(p))
		}
		f.say(fmt.Sprintf("⇄ %d ports on localhost: %s", len(ports), strings.Join(names, ", ")))
	}
}

// add forwards a free laptop port, the guest's own number first, to the
// listener, on the ControlMaster. A port ssh cannot bind (taken since the
// check, or on ::1) moves on to the next.
func (f *forwarder) add(ctx context.Context, l guestListener) (int, bool) {
	for lp := l.Port; lp < l.Port+20 && lp <= 65535; lp++ {
		if !f.localFree(lp) {
			continue
		}
		err := f.ctl(ctx, "forward", forwardSpec(lp, l))
		if err == nil {
			return lp, true
		}
		// The master refused this one port (bound since the check): try
		// the next. Anything else (no master, a dead connection) would
		// fail the same way for every port.
		if !strings.Contains(err.Error(), "forwarding request failed") {
			return 0, false
		}
	}
	return 0, false
}

func (f *forwarder) cancel(ctx context.Context, gp int, fe forwardEntry) {
	_ = f.ctl(ctx, "cancel", forwardSpec(fe.Local, guestListener{Port: gp, Host: fe.Host}))
}

// forwardSpec is ssh's `-L` argument; an IPv6 host goes in brackets.
func forwardSpec(lp int, l guestListener) string {
	host := l.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%d:%s:%d", lp, host, l.Port)
}

// control is `ssh -O <op> -L <spec> <target>`, a request to the running
// master: no new connection, no new process that stays.
func (f *forwarder) control(ctx context.Context, op, spec string) error {
	args := append([]string{"-O", op, "-L", spec}, f.t.Args...)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh -O %s: %v (%s)", op, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// closeAll cancels every forward this helper holds.
func (f *forwarder) closeAll(ctx context.Context) {
	for gp, fe := range f.fwd {
		f.cancel(ctx, gp, fe)
		delete(f.fwd, gp)
	}
}

// guestPorts is this helper's forwarded guest ports, for the status bar.
func (f *forwarder) guestPorts() []int {
	ps := make([]int, 0, len(f.fwd))
	for gp := range f.fwd {
		ps = append(ps, gp)
	}
	sort.Ints(ps)
	return ps
}

// publish writes this helper's ports to the guest's forward list (an
// empty list removes the entry) and sets the session's status-right to
// the union of every live entry, or back to the global one when none is
// left: two CLIs attached from two laptops each forward on their own and
// the bar shows both. A machine with no tmux session of that name (a
// herdr machine) has no bar; the script still exits 0 there, so the
// heartbeat, not every poll, sets the pace of publishes.
func (f *forwarder) publish(ctx context.Context) error {
	return runSSHOK(ctx, f.t, f.publishScript())
}

// publishScript is publish's guest side.
func (f *forwarder) publishScript() string {
	ports := make([]string, 0, len(f.fwd))
	for _, p := range f.guestPorts() {
		ports = append(ports, strconv.Itoa(p))
	}
	entry := fmt.Sprintf("rm -f \"$d/%s\"", f.id)
	if len(ports) > 0 {
		entry = fmt.Sprintf("printf '%%s\\n' %s > \"$d/%s\"", shQuote(strings.Join(ports, " ")), f.id)
	}
	return fmt.Sprintf(`d="$HOME/.repose/forwards"
mkdir -p "$d"
%s
u=$(find "$d" -type f -newermt "%d seconds ago" -exec cat {} + 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' | sort -un | tr '\n' ' ')
u=${u%% }
s=%s
if ! tmux has-session -t "=$s" 2>/dev/null; then
  :
elif [ -n "$u" ]; then
  g=$(tmux show-options -gv status-right 2>/dev/null || true)
  tmux set-option -t "=$s:" status-right-length 120
  tmux set-option -t "=$s:" status-right "⇄ $u │ $g"
else
  tmux set-option -u -t "=$s:" status-right 2>/dev/null || true
  tmux set-option -u -t "=$s:" status-right-length 2>/dev/null || true
fi
`, entry, int(forwardStale/time.Second), shQuote(f.slug))
}

func sortedPorts(m map[int]guestListener) []int {
	ps := make([]int, 0, len(m))
	for p := range m {
		ps = append(ps, p)
	}
	sort.Ints(ps)
	return ps
}

// runForwards is the helper's forward loop: until alive says the attach
// is gone (or ctx ends), keep the forwards equal to the guest's
// listeners, publish on every change and every heartbeat, then cancel
// every forward and take the entry out.
func runForwards(ctx context.Context, f *forwarder, alive func() bool) {
	lastPublish := time.Time{}
	for alive() && ctx.Err() == nil {
		changed, err := f.sync(ctx)
		f.announce(time.Now())
		if err == nil && (changed || (len(f.fwd) > 0 && time.Since(lastPublish) > forwardHeartbeat)) {
			if f.publish(ctx) == nil {
				lastPublish = time.Now()
			}
		}
		if sleepOrDone(ctx, forwardPoll) != nil {
			break
		}
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	had := len(f.fwd) > 0
	f.closeAll(cleanup)
	if had {
		_ = f.publish(cleanup)
	}
}
