package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/heracraft/repose/internal/menu"
)

// Guest runtimes (DECISIONS I-1462), as the api answers them.

// MsgNixConfigRetired is the api's sentence for a Nix file sent to an
// erofs machine (internal/api/http.MsgNixConfigRetired).
const MsgNixConfigRetired = "This machine runs the Ubuntu base; custom Nix files are not used, and `repose config add <item>` installs packages and services."

// SetDefaultGuestRuntime is settings.default_guest_runtime: what a create
// gets when it names no runtime.
func (f *Fake) SetDefaultGuestRuntime(runtime string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultRuntime = runtime
}

// SetPendingBase sets a project's pending_base_version, as the base bump
// does for an erofs project; "" clears it.
func (f *Fake) SetPendingBase(projectID, version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[projectID]; ok {
		if version == "" {
			p.PendingBaseVersion = nil
		} else {
			v := version
			p.PendingBaseVersion = &v
		}
	}
}

// createRuntime is the api's rule, called under f.mu: the default, or what an exempt account
// asks for; any other account asking for the other runtime is refused.
func (f *Fake) createRuntime(asked *string) (string, *apiError) {
	// Handlers run under f.mu (ServeHTTP).
	def := f.defaultRuntime
	exempt := f.billingStatus() == "exempt"
	if def != "erofs" {
		def = "nix"
	}
	if asked == nil {
		return def, nil
	}
	if *asked != "nix" && *asked != "erofs" {
		return "", invalid("guest_runtime must be nix or erofs").withDetail(map[string]any{"reason": "guest_runtime"})
	}
	if *asked != def && !exempt {
		return "", errf("forbidden", "this account cannot choose the guest runtime; new machines run %s", def).withDetail(map[string]any{"reason": "guest_runtime"})
	}
	return *asked, nil
}

// FailMenuPackage is a package name whose install the fake fails, for the
// CLI and dashboard to exercise an erofs menu's failure (as
// forceEvalErrorMarker does a Nix build's).
const FailMenuPackage = "repose-fake-missing"

// putMenuErofs is the api's PUT /config on an erofs project (I-1462,
// I-1464): ValidateFor("erofs") (apt package names; never Normalize, which
// applies the Nix rule), no fragment, and an op whose log is ApplyMenu's
// lines rather than a Nix build's: per item "installing X" then "X ready"
// (a package: "X installed"), per item the previous menu had and this one
// has not "removing X" then "X removed", and "X failed: <reason>" for a
// failure, which fails the op with menu_failed.
func (f *Fake) putMenuErofs(w http.ResponseWriter, p *project, raw json.RawMessage) *apiError {
	var sel menu.Selection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return invalid("menu: %v", err)
	}
	cat, err := menuCatalog()
	if err != nil {
		return errf("internal", "catalog: %v", err)
	}
	if err := cat.ValidateFor(menu.RuntimeErofs, sel); err != nil {
		var me *menu.Error
		if errors.As(err, &me) {
			return invalid("%s", me.Message)
		}
		return errf("internal", "menu: %v", err)
	}
	name := func(it menu.Item) string {
		if it.Package != "" {
			return it.Package
		}
		return it.ID
	}
	now := f.now()
	keep := map[string]bool{}
	for _, it := range sel {
		keep[name(it)] = true
	}
	var lines []string
	if cur := p.revision(p.ConfigRevisionID); cur != nil && len(cur.Menu) > 0 {
		var prev menu.Selection
		if json.Unmarshal(cur.Menu, &prev) == nil {
			for _, it := range prev {
				if n := name(it); !keep[n] {
					lines = append(lines, "removing "+n, n+" removed")
				}
			}
		}
	}
	failed := ""
	for _, it := range sel {
		n := name(it)
		lines = append(lines, "installing "+n)
		if it.Package == FailMenuPackage {
			lines = append(lines, n+" failed: E: Unable to locate package "+n)
			failed = n
			break
		}
		if it.Package != "" {
			lines = append(lines, n+" installed")
		} else {
			lines = append(lines, n+" ready")
		}
	}
	o := f.newOp(p, "config")
	o.lines = lines
	if failed != "" {
		msg := "menu_failed: " + failed + " failed: E: Unable to locate package " + failed
		rev := &Revision{ID: f.nextID(), CreatedAt: now, Status: "failed", Error: msg, Menu: raw}
		p.revisions = append(p.revisions, rev)
		o.State, o.Error = "error", msg
		f.event(p, "config.failed", "", "revision failed: "+msg)
		writeJSON(w, http.StatusAccepted, map[string]string{"revision_id": rev.ID, "op_id": o.id})
		return nil
	}
	rev := &Revision{ID: f.nextID(), CreatedAt: now, Status: "applied", Menu: raw, AppliedAt: &now, BaseVersion: p.BaseVersion}
	p.revisions = append(p.revisions, rev)
	p.ConfigRevisionID = rev.ID
	f.event(p, "config.applied", "", "revision applied")
	writeJSON(w, http.StatusAccepted, map[string]string{"revision_id": rev.ID, "op_id": o.id})
	return nil
}
