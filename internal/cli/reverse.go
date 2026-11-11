package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reverse forwards (DECISIONS I-1104): `[forward] reverse` in
// config.toml lists the laptop's ports that appear on the machine's
// `localhost` while this laptop is attached. They exist only inside an
// attach, travel on a connection of their own that the session helper
// holds, and end with it.
//
// The connection is not the ControlMaster: a `-R` on the master would
// live as long as the master, up to ControlPersist's ten minutes after
// the last client, and a laptop port must not stay open on the machine
// after a detach (I-149, the same reason `repose browser bridge` holds
// its own). Its remote command reads the helper's pipe, so the helper's
// exit, for any reason, closes the pipe, ends the command and takes the
// listeners with it.
//
// The guest's sshd has GatewayPorts no, so the listeners are on the
// machine's loopback whatever the request asks for; both `127.0.0.1`
// and `::1` are asked for, because one request binds one of them (ssh
// 9.6 against a GatewayPorts-no sshd: `-R 127.0.0.1:P` left `::1:P`
// free) and a client on the machine may try `::1` first for
// `localhost`.

// reverseMax is how many ports one laptop may lend.
const reverseMax = 16

// reverseHoldCommand holds the connection open on the machine: it reads
// the helper's pipe and ends when the helper does.
const reverseHoldCommand = "exec cat >/dev/null"

// reverseRetry is how long the helper waits before it tries a port the
// machine held, or a connection that dropped, again.
const (
	reverseRetry     = 30 * time.Second
	reverseReconnect = time.Second
	// reverseSettleTries is how many of those short waits a port that
	// the machine's sshd has not released yet is given before it is
	// read as somebody else's.
	reverseSettleTries = 5
)

// reverseDeniedLaptopPorts are laptop ports this never lends: a raw pipe
// to them goes round a repose control, or runs code on the laptop by
// design. The text is what the refusal says after "PORT is ".
var reverseDeniedLaptopPorts = map[int]string{
	9222: "Chrome's DevTools port. To lend the agents your Chrome, use repose browser bridge",
	9229: "Node's inspector port, which runs code on your laptop",
	2375: "Docker's API port, which runs anything on your laptop",
	2376: "Docker's API port, which runs anything on your laptop",
}

// reverseForward is one lent port: Machine is where it listens on the
// machine, Laptop is the port behind it on this laptop. A plain number
// in config.toml sets both; "MACHINE:LAPTOP" sets them apart, the
// listening port first, as `repose open LOCAL:PORT` and ssh's own -L
// and -R spell it (I-619).
type reverseForward struct {
	Machine int `json:"machine"`
	Laptop  int `json:"laptop"`
}

// parseReverseEntry reads one entry of a `reverse` list. The message it
// returns is the refusal, without the key or the file.
func parseReverseEntry(v any) (reverseForward, string) {
	switch t := v.(type) {
	case int64:
		return reverseForward{Machine: int(t), Laptop: int(t)}, ""
	case string:
		s := strings.TrimSpace(t)
		m, l, split := strings.Cut(s, ":")
		if !split {
			l = m
		}
		mp, err1 := strconv.Atoi(strings.TrimSpace(m))
		lp, err2 := strconv.Atoi(strings.TrimSpace(l))
		if err1 != nil || err2 != nil || strings.Contains(l, ":") {
			return reverseForward{}, fmt.Sprintf("%q is not a port or \"MACHINE:LAPTOP\".", t)
		}
		return reverseForward{Machine: mp, Laptop: lp}, ""
	default:
		return reverseForward{}, fmt.Sprintf("%v is not a port or \"MACHINE:LAPTOP\".", v)
	}
}

// checkReverseForward is the refusal for one entry, "" when it is fine.
func checkReverseForward(rf reverseForward) string {
	for _, p := range []int{rf.Machine, rf.Laptop} {
		if p < 1024 {
			return fmt.Sprintf("%d is below 1024. Ports below 1024 can't be lent to the machine.", p)
		}
		if p > 65535 {
			return fmt.Sprintf("%d is above 65535, which is not a port.", p)
		}
	}
	if forwardPlatformPorts[rf.Machine] {
		return fmt.Sprintf("%d is a port the machine keeps for itself.", rf.Machine)
	}
	if why, denied := reverseDeniedLaptopPorts[rf.Laptop]; denied {
		return fmt.Sprintf("%d is %s.", rf.Laptop, why)
	}
	return ""
}

// parseReverseList reads a whole `reverse` list, each machine port once
// and in the order it was written. The message it returns is the
// refusal, without the file.
func parseReverseList(key string, raw []any) ([]reverseForward, string) {
	var out []reverseForward
	seen := map[int]bool{}
	for _, v := range raw {
		rf, msg := parseReverseEntry(v)
		if msg == "" {
			msg = checkReverseForward(rf)
		}
		if msg != "" {
			return nil, key + ": " + msg
		}
		if seen[rf.Machine] {
			continue
		}
		seen[rf.Machine] = true
		out = append(out, rf)
	}
	if len(out) > reverseMax {
		return nil, fmt.Sprintf("%s: %d ports; at most %d.", key, len(out), reverseMax)
	}
	return out, ""
}

// reverseSSHArgs is the held connection's ssh: its own connection, exit
// if a listener cannot be made, one request per loopback address per
// port, and the hold as the remote command.
func reverseSSHArgs(t sshTarget, rs []reverseForward) []string {
	args := append(ownConnection(), "-o", "ExitOnForwardFailure=yes")
	for _, r := range rs {
		args = append(args,
			"-R", fmt.Sprintf("127.0.0.1:%d:localhost:%d", r.Machine, r.Laptop),
			"-R", fmt.Sprintf("[::1]:%d:localhost:%d", r.Machine, r.Laptop))
	}
	args = append(args, t.Args...)
	return append(args, reverseHoldCommand)
}

// reverseFailedPorts reads the machine ports ssh could not make a
// listener for out of its stderr ("Warning: remote port forwarding
// failed for listen port 5037").
var reverseFailedRe = regexp.MustCompile(`remote port forwarding failed for listen port (\d+)`)

func reverseFailedPorts(stderr string) []int {
	var ps []int
	for _, m := range reverseFailedRe.FindAllStringSubmatch(stderr, -1) {
		if p, err := strconv.Atoi(m[1]); err == nil {
			ps = append(ps, p)
		}
	}
	return ps
}

// reverseHeld is one lent port as it really stands, for the registry and
// the messages: the laptop's process name when this laptop's own tools
// said, "" when they did not.
type reverseHeld struct {
	reverseForward
	Process string
}

// reverseProcName keeps the characters the registry carries.
var reverseProcName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// laptopProcess is the name of the program listening on port on this
// laptop, "" when nothing listens or the laptop's tools did not answer.
// It is asked of the laptop's own tools, never of the machine.
func laptopProcess(ctx context.Context, port int) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch goos() {
	case "darwin":
		cmd = exec.CommandContext(ctx, "lsof", "-nP", "-Fc", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
	case "linux":
		cmd = exec.CommandContext(ctx, "ss", "-Hltnp", "sport = :"+strconv.Itoa(port))
	default:
		return ""
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseLaptopProcess(goos(), string(out))
}

// parseLaptopProcess reads laptopProcess's output: lsof -Fc prints
// "cNAME" lines, ss -Hltnp the users:(("NAME",pid=N,...)) field.
func parseLaptopProcess(os, out string) string {
	name := ""
	if os == "darwin" {
		for _, l := range strings.Split(out, "\n") {
			if n, ok := strings.CutPrefix(l, "c"); ok && n != "" {
				name = n
				break
			}
		}
	} else if m := ssUsers.FindStringSubmatch(out); m != nil {
		name = m[1]
	}
	name = reverseProcName.ReplaceAllString(strings.TrimSpace(name), "")
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// reverseLines are the messages for the ports this laptop lent, in the
// shape I-1104 gives: one port names the laptop's process when it is
// known, or says that nothing listens there yet; several name the ports
// and leave the processes out.
func reverseLines(held []reverseHeld) []string {
	if len(held) == 0 {
		return nil
	}
	if len(held) == 1 {
		h := held[0]
		switch {
		case h.Process != "" && h.Machine != h.Laptop:
			return []string{fmt.Sprintf("⇄ localhost:%d on the machine → %s on your laptop's %d", h.Machine, h.Process, h.Laptop)}
		case h.Process != "":
			return []string{fmt.Sprintf("⇄ localhost:%d on the machine → %s on your laptop", h.Machine, h.Process)}
		case h.Machine != h.Laptop:
			return []string{fmt.Sprintf("⇄ localhost:%d on the machine → your laptop's %d (nothing listens on %d there yet)", h.Machine, h.Laptop, h.Laptop)}
		default:
			return []string{fmt.Sprintf("⇄ localhost:%d on the machine → your laptop (nothing listens on %d there yet)", h.Machine, h.Laptop)}
		}
	}
	ports := make([]string, 0, len(held))
	for _, h := range held {
		ports = append(ports, strconv.Itoa(h.Machine))
	}
	return []string{fmt.Sprintf("⇄ localhost:%s on the machine → your laptop", strings.Join(ports, ", "))}
}

// reverseBusyLine says a port could not be lent because the machine
// listens there. who is the machine's process, or "" when ss did not
// name one; lent is another attached laptop already lending that port.
func reverseBusyLine(rf reverseForward, who string, lent bool) string {
	if lent {
		return fmt.Sprintf("Could not lend your laptop's %d: another laptop lends %d on the machine.", rf.Laptop, rf.Machine)
	}
	what := "something on the machine listens there"
	if who != "" {
		what = who + " on the machine listens there"
	}
	free := rf.Laptop + 10000
	if free > 65535 {
		free = rf.Laptop - 10000
	}
	return fmt.Sprintf("Could not lend your laptop's %d: %s. Use %q in [forward] reverse.", rf.Laptop, what, fmt.Sprintf("%d:%d", free, rf.Laptop))
}

// reverseBusyScript reads, in one call, what listens on the machine and
// what other attached laptops lend there: the two reasons a port cannot
// be lent.
func reverseBusyScript() string {
	return "ss -Hltnp 2>/dev/null; echo '" + forwardRegistryMark + "'; " + forwardRegistryScript()
}

// reverseBusy is reverseBusyScript read: the machine's listening ports
// with the process holding each, and the ports other laptops lend.
func reverseBusy(out string) (map[int]string, map[int]bool) {
	ssPart, fwdPart, _ := strings.Cut(out, forwardRegistryMark)
	busy := map[int]string{}
	for _, line := range strings.Split(ssPart, "\n") {
		l, ok := parseListenerLine(line)
		if !ok {
			continue
		}
		name := ""
		if m := ssUsers.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		if _, seen := busy[l.Port]; !seen || name != "" {
			busy[l.Port] = name
		}
	}
	lent := map[int]bool{}
	for _, r := range parseForwardRows(fwdPart) {
		if r.In {
			lent[r.Machine] = true
		}
	}
	return busy, lent
}

// runSessionReverse holds this laptop's lent ports for as long as the
// attach lasts. It tells f what it holds, so the forwarder writes the
// registry's `in:` lines and never forwards a reverse listener back to
// the laptop, and it says each port once.
func runSessionReverse(ctx context.Context, t sshTarget, rs []reverseForward, f *forwarder, say func(string), alive func() bool) {
	said := map[int]bool{}
	announced := false
	dropped := map[int]bool{}
	// justHeld are the ports the connection that just ended held, so a
	// listener the machine's sshd has not released yet is not read as
	// somebody else's.
	justHeld := map[int]bool{}
	settleTries := 0
	names := map[int]string{}
	for _, r := range rs {
		names[r.Laptop] = laptopProcess(ctx, r.Laptop)
	}
	for alive() && ctx.Err() == nil {
		busy, lent := map[int]string{}, map[int]bool{}
		if out, err := runSSH(ctx, t, reverseBusyScript(), nil); err == nil {
			busy, lent = reverseBusy(string(out))
		}
		var want []reverseForward
		var held []reverseHeld
		settling := false
		for _, r := range rs {
			who, taken := busy[r.Machine]
			if taken && justHeld[r.Machine] {
				// Our own listener from the connection that just
				// dropped: the machine's sshd has not let it go yet.
				// Nothing to tell the user, and nothing is wrong.
				settling = true
				continue
			}
			if dropped[r.Machine] || taken {
				if !said[r.Machine] {
					said[r.Machine] = true
					say(reverseBusyLine(r, who, lent[r.Machine]))
				}
				continue
			}
			want = append(want, r)
			held = append(held, reverseHeld{reverseForward: r, Process: names[r.Laptop]})
		}
		if len(want) == 0 {
			wait := reverseRetry
			if settling && settleTries < reverseSettleTries {
				settleTries++
				wait = reverseReconnect
			} else {
				// Either nothing was settling, or it has had its few
				// seconds and the port is somebody else's now: stop
				// treating it as ours, so the user is told.
				settling = false
				settleTries = 0
				justHeld = map[int]bool{}
			}
			if sleepOrDone(ctx, wait) != nil {
				return
			}
			if !settling {
				dropped = map[int]bool{} // a port the machine let go is tried again
			}
			continue
		}
		settleTries = 0
		if !announced {
			announced = true
			for _, m := range reverseLines(held) {
				say(m)
			}
		}
		f.setReverse(held)
		stderr := holdReverse(ctx, t, want, alive)
		f.setReverse(nil)
		justHeld = map[int]bool{}
		for _, r := range want {
			justHeld[r.Machine] = true
		}
		failed := reverseFailedPorts(stderr)
		for _, p := range failed {
			dropped[p] = true
		}
		if len(failed) > 0 {
			continue // the race ExitOnForwardFailure caught: try again without it
		}
		if sleepOrDone(ctx, reverseReconnect) != nil {
			return
		}
	}
}

// holdReverse runs the connection until ctx ends, the attach ends, or
// ssh does, and returns ssh's stderr. Closing the hold's stdin is how
// the machine learns the forwards are over, before ssh itself is ended.
func holdReverse(ctx context.Context, t sshTarget, rs []reverseForward, alive func() bool) string {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", reverseSSHArgs(t, rs)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ""
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return ""
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
				if !alive() {
					// The pipe first: closing it ends `cat` on the
					// machine, which takes the listeners with it before
					// ssh is killed.
					_ = stdin.Close()
					time.AfterFunc(time.Second, cancel)
					return
				}
			}
		}
	}()
	_ = cmd.Wait()
	_ = stdin.Close()
	cancel()
	<-done
	return stderr.String()
}
