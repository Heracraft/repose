package httpapi

import (
	"net/http"
)

// `repose fork` was removed (DECISIONS I-1082). The route stays for one
// release so an old CLI on a laptop reads a deliberate refusal instead
// of a bare 404, and so that the refusal is one an old CLI prints well:
// it prints an `invalid` as "Could not fork X: <message>. Nothing was
// created." and exits 1. The message must therefore read as a clause
// after a colon and must not contain "snapshot_id is required", which
// an old CLI takes as "take a fork snapshot and try again".

// forkRemoved is POST /v1/projects/{id}/fork: 400 invalid with
// `detail.reason = "fork_removed"`.
func (s *Server) forkRemoved(w http.ResponseWriter, r *http.Request) error {
	return withDetail(
		errf("invalid", "repose fork was removed; `repose run --worktree -p PROMPT` runs another agent on its own branch on the same machine"),
		map[string]any{"reason": "fork_removed"})
}
