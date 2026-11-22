package api

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
