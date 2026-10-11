package guest

import (
	"context"
	"strings"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

const interruptedSuffix = ": interrupted by the host shutting down"

// SetShuttingDown marks hostd as going down: its root context ended
// (SIGTERM, a host shutdown or a hostd restart). It only mutes the
// monitor's exit check (Stopping); a boot cut off by a plain hostd restart
// is still torn down and answered as a failure, since the guest's units
// outlive hostd and nothing else would settle it. DECISIONS I-650.
func (m *Manager) SetShuttingDown() { m.shuttingDown.Store(true) }

// Stopping reports whether hostd or the host is going down. A guest@ unit
// can stop before hostd hears of the shutdown, since systemd stops both
// at once, so systemd is asked too; once true it stays true.
func (m *Manager) Stopping(ctx context.Context) bool {
	return m.shuttingDown.Load() || m.HostStopping(ctx)
}

// HostStopping reports whether the host itself is shutting down (systemd
// says stopping). Only then is a boot left as it is and its command
// neither stored nor sent: the guest's units go down with the host, and
// the next boot's reconcile settles the record. Once true it stays true.
func (m *Manager) HostStopping(ctx context.Context) bool {
	if m.hostDown.Load() {
		return true
	}
	if m.d.SystemStopping != nil && m.d.SystemStopping(ctx) {
		m.hostDown.Store(true)
		m.shuttingDown.Store(true)
		return true
	}
	return false
}

// interrupted is the error of a command cut off by a shutdown. It is
// neither stored nor sent (run), so the api's resend after the restart
// runs the command again rather than reading this back.
func interrupted(what string) *Error {
	return &Error{Code: CodeInternal, Message: what + interruptedSuffix, interrupted: true}
}

// wasInterrupted reads interrupted back from a result: the flag does not
// survive payloadOrErr, and the message is only ever this one's.
func wasInterrupted(res *hostdv1.Result) bool {
	return res != nil && !res.Ok && res.Error != nil && res.Error.Code == CodeInternal &&
		strings.HasSuffix(res.Error.Message, interruptedSuffix)
}
