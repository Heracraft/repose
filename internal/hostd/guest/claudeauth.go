package guest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/virtiofs"
)

// The Claude login share (DECISIONS I-278). A user signs in to Claude Code
// once, in any guest, through Anthropic's own flow; the file it writes is
// on a directory the host shares into every guest of that user, so every
// other project is signed in too. hostd creates the directory and serves
// it. It never opens, reads, copies or moves anything inside it.
//
//	UsersDir/                0711 root
//	  <user_id>/             0711 root
//	    last-guest           root; mtime is when a guest of this user last booted or was seen
//	    claude-auth/         0700 AuthUser, the only thing the guest sees

// authLastGuest is the marker whose age decides the sweep.
const authLastGuest = "last-guest"

// userIDRe is what a user id may look like to become a path segment; the
// api sends a UUID, and anything else gets no share rather than a path.
var userIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (m *Manager) authUserDir(userID string) string { return filepath.Join(m.cfg.UsersDir, userID) }

// hasAuthShare reports whether a guest gets the login share: it needs a
// user id that is safe as a path segment. Guests created before hostd
// recorded user ids have none and keep their own login.
func hasAuthShare(g *state.Guest) bool { return userIDRe.MatchString(g.UserID) }

// prepareAuthShare creates the user's share and the guest's socket
// directory and marks the user as having a guest here. Every step is
// idempotent; a directory that exists is re-owned and re-moded through
// ensureDir, which never follows a link: the socket directory is in the
// guest directory, where the hypervisor's user can create entries.
func (m *Manager) prepareAuthShare(g *state.Guest, dir string) (virtiofs.AuthConfig, error) {
	lookup := m.cfg.Lookup
	if lookup == nil {
		lookup = LookupUser
	}
	auid, agid, err := lookup(m.cfg.AuthUser)
	if err != nil {
		return virtiofs.AuthConfig{}, fmt.Errorf("login share user %s: %w", m.cfg.AuthUser, err)
	}
	_, ggid, err := lookup(m.cfg.GuestUser)
	if err != nil {
		return virtiofs.AuthConfig{}, fmt.Errorf("guest user %s: %w", m.cfg.GuestUser, err)
	}
	udir := m.authUserDir(g.UserID)
	share := filepath.Join(udir, "claude-auth")
	if err := os.MkdirAll(filepath.Dir(m.cfg.UsersDir), 0o755); err != nil {
		return virtiofs.AuthConfig{}, err
	}
	for _, d := range []struct {
		path     string
		mode     os.FileMode
		uid, gid int
	}{
		{m.cfg.UsersDir, 0o711, -1, -1},
		{udir, 0o711, -1, -1},
		{share, 0o700, auid, agid},
		// The socket directory, like the store's: the virtiofsd creates
		// its socket there and the hypervisor (group) connects to it.
		{filepath.Dir(ch.AuthSocket(dir)), 0o750, auid, ggid},
	} {
		if err := ensureDir(d.path, d.mode, d.uid, d.gid); err != nil {
			return virtiofs.AuthConfig{}, err
		}
	}
	if err := touch(filepath.Join(udir, authLastGuest), m.d.Now()); err != nil {
		return virtiofs.AuthConfig{}, err
	}
	return virtiofs.AuthConfig{SharedDir: share, User: m.cfg.AuthUser, Group: m.cfg.AuthUser, UID: auid, GID: agid,
		Binary: m.cfg.VirtiofsBinary, SocketGroup: m.cfg.GuestUser}, nil
}

// startAuthShare runs the guest's login-share virtiofsd and waits for its
// socket. The guest boots without the share when this fails: the user
// then signs in inside that guest as before, which is better than a guest
// that does not start.
func (m *Manager) startAuthShare(ctx context.Context, g *state.Guest, dir string) bool {
	if m.cfg.NoAuthShare || !hasAuthShare(g) {
		return false
	}
	cfg, err := m.prepareAuthShare(g, dir)
	if err == nil {
		err = virtiofs.StartAuth(ctx, m.d.Systemd, cfg, g.GuestID, ch.AuthSocket(dir))
	}
	if err == nil {
		err = m.waitSocket(ctx, virtiofs.AuthUnit(g.GuestID), ch.AuthSocket(dir))
	}
	if err != nil {
		_ = virtiofs.StopAuth(ctx, m.d.Systemd, g.GuestID) // a half-started share is not attached
		m.log(g).Warn("guest starts without the login share", "event", "auth_share", "err", err.Error())
		return false
	}
	return true
}

// SweepAuthShares removes the login share of every user who has had no
// guest on this host for AuthKeep, and stamps the marker of every user
// who still has one. It runs at start and daily. A share with no marker
// (a hostd that died between mkdir and touch) starts its clock now.
func (m *Manager) SweepAuthShares(ctx context.Context) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		m.d.Log.Warn("login share sweep: list guests", "event", "auth_share_sweep", "err", err.Error())
		return
	}
	has := map[string]bool{}
	for _, g := range gs {
		has[g.UserID] = true
	}
	entries, err := os.ReadDir(m.cfg.UsersDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.d.Log.Warn("login share sweep: read users dir", "event", "auth_share_sweep", "err", err.Error())
		}
		return
	}
	now := m.d.Now()
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || !userIDRe.MatchString(id) {
			continue
		}
		marker := filepath.Join(m.authUserDir(id), authLastGuest)
		fi, err := os.Stat(marker)
		switch {
		case has[id] || errors.Is(err, fs.ErrNotExist):
			err = touch(marker, now)
		case err == nil && now.Sub(fi.ModTime()) > m.cfg.AuthKeep:
			err = os.RemoveAll(m.authUserDir(id))
			if err == nil {
				m.d.Log.Info("login share removed", "event", "auth_share_sweep", "user_id", id)
			}
		}
		if err != nil {
			m.d.Log.Warn("login share sweep", "event", "auth_share_sweep", "user_id", id, "err", err.Error())
		}
	}
}

// touch creates path if missing and sets its mtime.
func touch(path string, t time.Time) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, t, t)
}
