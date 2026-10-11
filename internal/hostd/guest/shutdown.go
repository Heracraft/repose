package guest

import (
	"context"
	"strings"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

const interruptedSuffix = ": interrupted by the host shutting down"

// SetShuttingDown marks hostd as going down: its root context ended
// (SIGTERM, a host shutdown or a hostd restart). DECISIONS I-650.
func (m *Manager) SetShuttingDown() { m.shuttingDown.Store(true) }

// Stopping reports whether hostd or the host is going down. A guest@ unit
// can stop before hostd hears of the shutdown, since systemd stops both
// at once, so systemd is asked too; once true it stays true.
func (m *Manager) Stopping(ctx context.Context) bool {
	if m.shuttingDown.Load() {
		return true
	}
	if m.d.SystemStopping != nil && m.d.SystemStopping(ctx) {
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
