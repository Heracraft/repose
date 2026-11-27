package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SetOwnedHostRestart makes the account's live host called name one whose
// hostd parks its machines and restarts the Mac (the `restart` Hello
// feature, DECISIONS I-1690), or one that does not. notParked, when set,
// are the slugs its next restart cannot park: the restart is refused with
// their names unless forced, as hostd refuses it.
func (f *Fake) SetOwnedHostRestart(name string, on bool, notParked []string, locked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.ownedHosts {
		if h.Name == name && h.State != "retired" && h.State != "lost" {
			h.restarts, h.notParked, h.locked = on, notParked, locked
		}
	}
}

// restartHost is POST /v1/hosts/{id}/restart, with the api's refusals: a
// host that is not ready, one whose hostd cannot park, and a machine that
// could not be parked without force (the op's error, code not_parked).
func (f *Fake) restartHost(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	h := f.accountOwnedHost(u.ID, r.PathValue("id"))
	if h == nil {
		return notFound("host " + r.PathValue("id"))
	}
	var body struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			return invalid("malformed body: %v", err)
		}
	}
	if h.State != "ready" {
		word := map[string]string{"registering": "waiting to join", "unreachable": "offline", "draining": "being removed"}[h.State]
		if word == "" {
			word = h.State
		}
		return errf("conflict", "%s is %s, so it cannot be restarted from here.", h.Name, word).
			withDetail(map[string]any{"reason": "host_not_ready", "state": h.State})
	}
	if !h.restarts {
		return errf("conflict", "%s cannot park its machines, so restarting it would lose what they hold in memory; restart it yourself.", h.Name).
			withDetail(map[string]any{"reason": "restart_unsupported"})
	}
	_, running := f.machinesOn(h.ID)
	not := append([]string{}, h.notParked...)
	// hostd's fixed reason for each, as the api puts it after the name.
	named := make([]string, len(not))
	for i, s := range not {
		named[i] = s + ": started with nobody logged in"
	}
	o := &op{id: f.nextID(), kind: "host_restart", created: f.now()}
	o.hostID = h.ID
	o.State = "done"
	restarting := len(not) == 0 || body.Force
	o.Result = map[string]any{"parked": running - len(not), "not_parked": not, "restarting": restarting, "park_ms": 900}
	if !restarting {
		o.State = "error"
		what := fmt.Sprintf("%d machines could not be parked and would start fresh", len(not))
		if len(not) == 1 {
			what = "1 machine could not be parked and would start fresh"
		}
		o.Error = fmt.Sprintf("%s (%s). Nothing restarted, and the parked machines run again.", what, strings.Join(named, "; "))
		if h.locked && running == len(not) {
			o.Error = "This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again."
		}
		o.errCode = "not_parked"
	}
	f.ops[o.id] = o
	writeJSON(w, http.StatusAccepted, map[string]string{"op_id": o.id})
	return nil
}

// getHostOp is GET /v1/hosts/{id}/ops/{op_id}.
func (f *Fake) getHostOp(w http.ResponseWriter, r *http.Request) *apiError {
	u := userFrom(r)
	h := f.accountOwnedHost(u.ID, r.PathValue("id"))
	if h == nil {
		return notFound("host " + r.PathValue("id"))
	}
	o, ok := f.ops[r.PathValue("op_id")]
	if !ok || o.hostID != h.ID || o.projectID != "" {
		return notFound("op")
	}
	// The api's op shape: error is {code, message}, as on every op of
	// the real api (docs/interfaces/api.md).
	out := map[string]any{"op_id": o.id, "kind": o.kind, "state": o.State, "created_at": o.created, "finished_at": o.created, "result": o.Result}
	if o.State == "error" {
		out["error"] = map[string]any{"code": o.errCode, "message": o.Error}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// accountOwnedHost is the account's host by id, nil when it is not theirs.
func (f *Fake) accountOwnedHost(owner, id string) *ownedHostRec {
	for _, h := range f.ownedHosts {
		if h.ID == id && h.owner == owner {
			return h
		}
	}
	return nil
}
