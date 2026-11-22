package guest

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/baseimg"
	"github.com/heracraft/repose/internal/hostd/ch"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

// erofs guests (DECISIONS I-1458, I-1478..I-1484): a guest that boots an
// Ubuntu base bundle from the host's image cache instead of a Nix
// closure. Its record names BaseVersion and no SystemClosure; it has no
// GC root, no store share, no store view and no path registration, and
// its menu is replayed by guestd's ApplyMenu after every boot.

// Feature names in Hello.features for the two guest runtimes (DECISIONS
// I-1458): which guests this host can run.
const (
	FeatureNix   = "nix"
	FeatureErofs = "erofs"
)

// CodeNixUnsupported is the answer of a host with the Nix machinery off
// (hostd --nix-mode=off, or no nix on PATH) to a command for a Nix guest:
// a failed precondition of the host, never of the command, which the api
// only sends here by mistake since such a host names no `nix` feature
// (DECISIONS I-1484).
const CodeNixUnsupported = "nix_unsupported"

// CodeMenuFailed is an erofs guest's menu that did not apply on
// ApplyConfig (docs/interfaces/grpc-hostd.md, "erofs guests").
const CodeMenuFailed = "menu_failed"

// NixOn reports whether this host builds and boots Nix guests.
func (m *Manager) NixOn() bool { return m.d.Nix != nil }

// ErofsOn reports whether this host boots erofs guests: it has an image
// cache.
func (m *Manager) ErofsOn() bool { return m.d.Images != nil }

func (m *Manager) nixUnsupported(what string) *Error {
	return errf(CodeNixUnsupported, "%s: this host runs no Nix guests (failed precondition: hostd --nix-mode=off or no nix on the host)", what)
}

// isErofs is whether g boots a base bundle.
func isErofs(g *state.Guest) bool { return g.BaseVersion != "" }

// unitBundle is what GuestUnitProps binds into guest@<id> for g: its
// bundle on an erofs guest, nothing on a Nix one.
func (m *Manager) unitBundle(g *state.Guest) UnitBundle {
	if !isErofs(g) || m.d.Images == nil {
		return UnitBundle{}
	}
	return UnitBundle{ImagesDir: m.d.Images.Dir, Dir: m.d.Images.BundleDir(g.BaseVersion)}
}

// volumeDiskID is the disk id vm.resize-disk names for g's volume: vdb
// on an erofs guest (I-1480), the implicit first disk on a Nix guest.
func volumeDiskID(g *state.Guest) string {
	if isErofs(g) {
		return ch.VolumeID
	}
	return ch.DiskID
}

// parseBase checks a command's base_image.
func parseBase(b *hostdv1.BaseImage) (baseimg.Spec, *Error) {
	s, err := baseimg.FromProto(b)
	if err != nil {
		return baseimg.Spec{}, errf(CodeInvalidArgument, "base_image: %v", err)
	}
	return s, nil
}

// ensureBase fetches spec's bundle into the cache when it is not there.
// A fetch that fails answers `internal` with the contract's wording for
// a file whose digest differs ("base image checksum mismatch"), and the
// caller has touched nothing yet (I-1482).
func (m *Manager) ensureBase(ctx context.Context, spec baseimg.Spec) *Error {
	if m.d.Images == nil {
		return errf(CodeInvalidArgument, "this host boots no erofs guests (no --images-dir)")
	}
	if _, err := m.d.Images.Ensure(ctx, spec); err != nil {
		if baseimg.IsChecksum(err) {
			return errf(CodeInternal, "base image checksum mismatch: %v", err)
		}
		return errf(CodeInternal, "base image fetch failed: %v", err)
	}
	return nil
}

// recordMenu keeps a command's menu on the record (I-1482); nil keeps
// what the record had.
func recordMenu(g *state.Guest, menu *hostdv1.MenuSpec) {
	if menu == nil {
		return
	}
	if b, err := proto.Marshal(menu); err == nil {
		g.Menu = b
	}
}

// recordedMenu is the menu the record holds, nil when none.
func recordedMenu(g *state.Guest) *hostdv1.MenuSpec {
	if len(g.Menu) == 0 {
		return nil
	}
	ms := &hostdv1.MenuSpec{}
	if err := proto.Unmarshal(g.Menu, ms); err != nil {
		return nil
	}
	return ms
}

// replayMenu is the ApplyMenu hostd sends after every erofs boot, once the
// guest has run SetupProject (docs/interfaces/grpc-hostd.md "erofs
// guests"): replay_after_base_upgrade set, so guestd installs again only
// when the initramfs rotated the overlay's upper directory for a new
// base (/run/repose/rw/.base-upgraded) or the menu differs from the one
// it last applied, and answers done at once otherwise. That marker is
// how "guestd reports base_upgraded" reaches hostd: there is no field
// for it on the wire, guestd acts on it itself (DECISIONS I-1482). A
// failure does not fail the boot; it is the start's menu_error.
func (m *Manager) replayMenu(ctx context.Context, g *state.Guest, sess vsockclient.Session) *hostdv1.Error {
	menu := recordedMenu(g)
	if menu == nil {
		return nil
	}
	res, err := sess.ApplyMenu(ctx, &guestdv1.ApplyMenu{Menu: menu, ReplayAfterBaseUpgrade: true}, nil)
	if err == nil {
		return nil
	}
	code, msg := CodeMenuFailed, err.Error()
	if re, ok := vsockclient.IsRemote(err); ok {
		code, msg = re.Code, re.Message
		if code != CodeInvalidArgument {
			code = CodeMenuFailed
		}
	}
	if res != nil && res.GetLine() != "" {
		msg = res.GetLine()
	}
	// The line names a package or a catalog entry: tenant content, sent
	// to the api in the result and never logged.
	m.log(g).Warn("menu replay failed; the guest runs", "event", "guest_menu", "code", code, "items", len(menu.GetItems()))
	return &hostdv1.Error{Code: code, Message: msg}
}

// applyMenu is ApplyConfig's menu on a running erofs guest: guestd's
// progress lines go to the api as BuildLog under the command id, and a
// failure is the command's error with guestd's code (menu_failed or
// invalid_argument); the guest keeps running with what it had.
func (m *Manager) applyMenu(ctx context.Context, commandID string, g *state.Guest, menu *hostdv1.MenuSpec) (*hostdv1.ApplyResult, *Error) {
	switch g.State {
	case StateRunning:
	case StateStopped, StateError:
		// Kept for the next boot, whose replay applies it: the menu
		// differs from the one the guest last applied (I-1482).
		recordMenu(g, menu)
		if err := m.d.State.PutGuest(g); err != nil {
			return nil, errf(CodeInternal, "state write: %v", err)
		}
		m.writeGuestJSON(g)
		return &hostdv1.ApplyResult{}, nil
	default:
		return nil, errf(CodeInvalidArgument, "guest is %s; a menu applies to a running or stopped guest", g.State)
	}
	sess, serr := m.sessionAwake(ctx, g)
	if serr != nil {
		return nil, serr
	}
	defer func() {
		m.mu.Lock()
		delete(m.buildSeq, commandID)
		m.mu.Unlock()
	}()
	res, err := sess.ApplyMenu(ctx, &guestdv1.ApplyMenu{Menu: menu}, func(line string) { m.buildLog(commandID, line) })
	if err != nil {
		code, msg := CodeGuestUnresponsive, err.Error()
		if re, ok := vsockclient.IsRemote(err); ok {
			code, msg = re.Code, re.Message
			if code != CodeInvalidArgument {
				code = CodeMenuFailed
			}
		}
		if res != nil && res.GetLine() != "" {
			msg = res.GetLine()
		}
		m.log(g).Warn("menu not applied", "event", "guest_menu", "code", code, "items", len(menu.GetItems()))
		return nil, &Error{Code: code, Message: msg}
	}
	recordMenu(g, menu)
	if err := m.d.State.PutGuest(g); err != nil {
		return nil, errf(CodeInternal, "state write: %v", err)
	}
	m.writeGuestJSON(g)
	m.log(g).Info("menu applied", "event", "guest_menu", "items", len(menu.GetItems()))
	return &hostdv1.ApplyResult{}, nil
}

// menuErr is the menu_error of g's last boot, cleared by each boot.
func (m *Manager) setMenuErr(id string, e *hostdv1.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.menuErrs == nil {
		m.menuErrs = map[string]*hostdv1.Error{}
	}
	if e == nil {
		delete(m.menuErrs, id)
		return
	}
	m.menuErrs[id] = e
}

func (m *Manager) lastMenuErr(id string) *hostdv1.Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.menuErrs[id]
}

// BaseRefs is the base versions the guests on this host name, running
// or not: what the image cache's GC never removes.
func (m *Manager) BaseRefs() (map[string]bool, error) {
	gs, err := m.d.State.ListGuests()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, g := range gs {
		if g.BaseVersion != "" {
			out[g.BaseVersion] = true
		}
	}
	return out, nil
}

// GCImages runs the image cache's GC (I-1478).
func (m *Manager) GCImages(ctx context.Context) {
	if m.d.Images == nil {
		return
	}
	if _, err := m.d.Images.GC(ctx); err != nil {
		m.d.Log.Warn("base image gc failed", "event", "base_image_gc", "err", err.Error())
	}
}

// markBaseMissing is reconcile's answer to an erofs guest whose bundle is
// not in the cache (I-1484): the guest is reported as it is, and the
// flag says its next start must fetch, which a start carrying base_image
// does. Nothing crashes, and nothing is removed.
func (m *Manager) markBaseMissing(g *state.Guest) {
	if !isErofs(g) || m.d.Images == nil {
		return
	}
	missing := !m.d.Images.Has(g.BaseVersion)
	if missing == g.BaseNeedsFetch {
		return
	}
	g.BaseNeedsFetch = missing
	if missing {
		m.log(g).Warn("base image missing from the cache; the next start fetches it", "event", "base_image", "base_version", g.BaseVersion, "state", g.State)
	}
	if err := m.d.State.PutGuest(g); err != nil {
		m.log(g).Warn("base image mark not written", "event", "base_image", "err", err.Error())
		return
	}
	m.writeGuestJSON(g)
}

var errNoBundle = errors.New("base image not in the host's cache")

// bundleFor is the bundle g boots, which must be in the cache.
func (m *Manager) bundleFor(g *state.Guest) (baseimg.Bundle, error) {
	if m.d.Images == nil {
		return baseimg.Bundle{}, fmt.Errorf("%w: this host has no image cache", errNoBundle)
	}
	if !m.d.Images.Has(g.BaseVersion) {
		return baseimg.Bundle{}, fmt.Errorf("%w: %s", errNoBundle, g.BaseVersion)
	}
	return m.d.Images.Paths(g.BaseVersion), nil
}
