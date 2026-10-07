package guest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/nixbuild"
	"github.com/heracraft/repose/internal/hostd/state"
)

// CodeBootFailed is a boot that never reached Ready and whose console
// shows why: the guest stopped before its system started (DECISIONS
// I-592). guest_unresponsive stays the code for a boot whose console
// shows nothing hostd recognises, or one that reached the system and
// whose guestd never answered.
const CodeBootFailed = "boot_failed"

// Console tail bounds (I-592): what a boot failure carries to the api.
const (
	consoleTailLines = 200
	consoleTailBytes = 16 << 10
)

// consoleLog is the guest's console log, which console capture appends
// to across boots.
func (m *Manager) consoleLog(id string) string {
	return filepath.Join(m.guestDir(id), "console.log")
}

// consoleSize is where this boot's console output starts in the log.
func (m *Manager) consoleSize(id string) int64 {
	fi, err := os.Stat(m.consoleLog(id))
	if err != nil {
		return 0
	}
	return fi.Size()
}

// consoleTail is the end of what the guest printed since offset from, at
// most consoleTailLines lines and consoleTailBytes bytes, cut at a line
// start. A log rotated since from is read from its start.
func (m *Manager) consoleTail(id string, from int64) string {
	f, err := os.Open(m.consoleLog(id))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read-only
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	if from > fi.Size() {
		from = 0
	}
	start := from
	if fi.Size()-start > consoleTailBytes {
		start = fi.Size() - consoleTailBytes
	}
	b, err := io.ReadAll(io.NewSectionReader(f, start, fi.Size()-start))
	if err != nil {
		return ""
	}
	if start > from {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(b), "?"), "\n"), "\n")
	if len(lines) > consoleTailLines {
		lines = lines[len(lines)-consoleTailLines:]
	}
	out := strings.Join(lines, "\n")
	if strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

// bootFailure is a classified console: the code and the fixed sentence a
// user reads. Neither ever carries console text; log fields may hold them.
type bootFailure struct {
	code    string
	message string
}

// classifyConsole names the known ways a NixOS guest fails before its
// system starts, from what it printed. The first match in this order
// wins: stage 1 prints its generic error line after the specific ones.
func classifyConsole(tail string) bootFailure {
	low := strings.ToLower(tail)
	has := func(s ...string) bool {
		for _, x := range s {
			if !strings.Contains(low, x) {
				return false
			}
		}
		return true
	}
	switch {
	case has("stage 2 init script", "not found"):
		return bootFailure{CodeBootFailed, "the system it boots is missing from the machine's store (stage 1 found no stage 2 init)"}
	case has("kernel panic"):
		return bootFailure{CodeBootFailed, "the guest's kernel panicked while booting"}
	// NixOS stage 1 says "<dev> has unrepaired errors" or "fsck on <dev>
	// failed."; e2fsck itself "UNEXPECTED INCONSISTENCY".
	case has("unrepaired errors") || has("unexpected inconsistency") || has("fsck on", "failed") || has("fsck failed"):
		return bootFailure{CodeBootFailed, "the check of the machine's disk failed while booting"}
	case has("an error occurred in stage 1"):
		return bootFailure{CodeBootFailed, "the boot stopped in stage 1, before the machine's system started"}
	case has("welcome to nixos") || has("systemd[1]") || has("starting systemd"):
		return bootFailure{CodeGuestUnresponsive, "the machine booted but its agent (guestd) never answered"}
	}
	return bootFailure{CodeGuestUnresponsive, "the machine did not answer while booting"}
}

// readyFailure is the step-10 error of a boot whose guestd never sent
// Ready: classified from the console this boot wrote since offset from,
// with that console's tail attached for the api (never logged).
// A guest_unresponsive message keeps the step and the wait in it, as
// before I-592; a boot_failed one is the fixed sentence alone.
func (m *Manager) readyFailure(g *state.Guest, from int64, waitErr error) *Error {
	tail := m.consoleTail(g.GuestID, from)
	bf := classifyConsole(tail)
	msg := bf.message
	if bf.code == CodeGuestUnresponsive {
		msg = fmt.Sprintf("create: step %d (%s) failed: guest did not become ready: %v (%s)", stepReady, stepNames[stepReady], waitErr, bf.message)
	}
	return &Error{Code: bf.code, Message: msg, ConsoleTail: tail, NoReady: true, Reason: bf.message}
}

// goodRoot is the GC root that keeps a guest's last good closure in the
// host store while the guest runs another (I-590).
func goodRoot(guestID string) string { return "good-" + guestID }

// markGood records closure as the newest one the guest's disk is known to
// run and roots it. Called with the record about to be written.
func (m *Manager) markGood(g *state.Guest, closure string) {
	if closure == "" || g.LastGoodClosure == closure {
		return
	}
	if err := m.d.Roots.Set(goodRoot(g.GuestID), closure); err != nil {
		m.log(g).Warn("last good closure not rooted", "event", "boot_fallback", "err", err.Error())
		return
	}
	g.LastGoodClosure = closure
}

// fallbackTarget is the closure a failed boot of g falls back to, or ""
// when there is none: no last good one (a first boot, a restored guest),
// the one that failed, or one the host store no longer holds whole.
func (m *Manager) fallbackTarget(ctx context.Context, g *state.Guest) string {
	lg := g.LastGoodClosure
	if lg == "" || lg == g.SystemClosure {
		return ""
	}
	if ok, err := m.d.Nix.PathExists(ctx, lg); err != nil || !ok {
		return ""
	}
	if _, err := nixbuild.ClosureInfo(lg); err != nil {
		return ""
	}
	return lg
}

// bootOrFallBack boots g; when that boot never reaches Ready and g has a
// last good closure other than the one that failed, it boots that one
// instead, once (DECISIONS I-590). It returns the fallback it made (nil
// when the first boot ran or none was possible) and the error to report:
// nil once a boot reached running. The failure that caused a fallback is
// recorded on the guest under commandID before the second boot, so a
// resend of the command after a hostd restart answers the same way.
// tail is the failed boot's console tail, for the result.
func (m *Manager) bootOrFallBack(ctx context.Context, g *state.Guest, commandID string) (fb *state.BootFallback, tail string, err *Error) {
	err = m.boot(ctx, g, stepRunner)
	if err == nil {
		return nil, "", nil
	}
	if !err.NoReady || ctx.Err() != nil {
		return nil, "", err
	}
	target := m.fallbackTarget(ctx, g)
	if target == "" {
		return nil, "", err
	}
	fb = &state.BootFallback{CommandID: commandID, Failed: g.SystemClosure, Code: err.Code, Message: err.Reason, At: m.d.Now().UTC()}
	g.BootFallback = fb
	if aerr := m.adoptClosure(g, target); aerr != nil {
		return nil, "", err
	}
	m.log(g).Warn("boot never reached ready; booting the last good closure", "event", "boot_fallback", "code", fb.Code)
	if err2 := m.boot(ctx, g, stepRunner); err2 != nil {
		// Both failed: the guest is in error on its last good closure,
		// and the first failure is the one to report.
		err.Message += "; its previous system did not boot either: " + err2.Message
		return nil, "", err
	}
	return fb, err.ConsoleTail, nil
}

// startResult is a StartGuest's payload: the closure the guest runs and,
// after a fallback, which one failed and why.
func startResult(g *state.Guest, fb *state.BootFallback, consoleTail string) *hostdv1.StartResult {
	r := &hostdv1.StartResult{BootedClosure: g.SystemClosure}
	if fb != nil {
		r.FailedClosure = fb.Failed
		r.BootError = &hostdv1.Error{Code: fb.Code, Message: fb.Message, ConsoleTail: consoleTail}
	}
	return r
}
