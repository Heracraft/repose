package vzpark

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

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

// ScreenLockOff reads `sysadminctl -screenLock status` (run as the user):
// true only when it says the screen lock is off. A Mac whose screen can
// lock cannot restore a save while locked (I-1662), so a machine rested as
// a save there could be out of reach until someone unlocks the Mac.
func ScreenLockOff(out []byte) bool {
	return bytes.Contains(out, []byte("screenLock is off"))
}

// ReadScreenLockOff asks sysadminctl as user uid/name, in that user's
// session; an answer it cannot read is "the lock may be on".
func ReadScreenLockOff(ctx context.Context, r shell.Runner, uid int, name string) bool {
	res, err := r.Run(ctx, "/bin/launchctl", "asuser", strconv.Itoa(uid), "/usr/bin/sudo", "-n", "-u", name, "/usr/sbin/sysadminctl", "-screenLock", "status")
	if err != nil {
		return false
	}
	return ScreenLockOff(res.Stdout) || ScreenLockOff(res.Stderr)
}
