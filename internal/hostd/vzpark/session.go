package vzpark

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// Session is the login session of one user as the window server
// publishes it (IORegistry IOConsoleUsers).
type Session struct {
	LoggedIn  bool
	OnConsole bool
	Locked    bool
}

// Unlocked is the state in which VZ can restore a save file: its Secure
// Enclave key is usable only while the user's session is unlocked
// (DECISIONS I-1662; restores failed "permission denied" n=5 while
// locked).
func (s Session) Unlocked() bool { return s.LoggedIn && s.OnConsole && !s.Locked }

// ReadSession reads uid's session from `ioreg -n Root -d1 -a`, through
// plutil's JSON. No session for uid is LoggedIn false.
func ReadSession(ctx context.Context, r shell.Runner, uid int) (Session, error) {
	res, err := r.Run(ctx, "/bin/sh", "-c", "/usr/sbin/ioreg -n Root -d1 -a | /usr/bin/plutil -extract IOConsoleUsers json -o - -")
	if err != nil {
		return Session{}, err
	}
	return parseSession(res.Stdout, uid)
}

func parseSession(b []byte, uid int) (Session, error) {
	var users []map[string]any
	if err := json.Unmarshal(b, &users); err != nil {
		return Session{}, err
	}
	for _, u := range users {
		id, _ := u["kCGSSessionUserIDKey"].(float64)
		if int(id) != uid {
			continue
		}
		done, _ := u["kCGSessionLoginDoneKey"].(bool)
		on, _ := u["kCGSSessionOnConsoleKey"].(bool)
		locked, _ := u["CGSSessionScreenIsLocked"].(bool)
		return Session{LoggedIn: done, OnConsole: on, Locked: locked}, nil
	}
	return Session{}, nil
}

// HasParked reports whether the park left records in dir: guests whose
// memory is in a save file waiting to be restored.
func HasParked(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	return len(m) > 0
}

// WaitUnlocked holds hostd's start while parked machines wait and the
// user's session is locked or absent: a restore now fails and the park
// would discard the machine's memory. It returns when the session is
// unlocked, when nothing is parked, or when ctx ends. A session that
// cannot be read is taken as unlocked: the restore then tries, as on
// Linux.
func WaitUnlocked(ctx context.Context, r shell.Runner, uid int, recordDir string, poll time.Duration, log *slog.Logger) {
	if !HasParked(recordDir) {
		return
	}
	said := false
	t0 := time.Now()
	for {
		s, err := ReadSession(ctx, r, uid)
		if err != nil {
			log.Warn("login session unreadable; restoring parked machines anyway", "event", "guest_unpark", "err", err.Error())
			return
		}
		if s.Unlocked() {
			if said {
				log.Info("session unlocked; restoring parked machines", "event", "guest_unpark", "waited_ms", time.Since(t0).Milliseconds())
			}
			return
		}
		if !said {
			log.Warn("parked machines wait for this Mac's login session to be unlocked", "event", "guest_unpark",
				"logged_in", s.LoggedIn, "locked", s.Locked)
			said = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}
