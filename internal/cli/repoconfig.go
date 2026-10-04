package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// repoConfigName is the project configuration a checkout carries at its
// root (DECISIONS I-489).
const repoConfigName = "repose.nix"

// applyRepoConfig is the run's and sync's half of I-489: the repose.nix at
// the root of the checkout that was just synced is the machine's
// configuration, applied without being asked. It is sent as the project's
// fragment; the api answers an unchanged fragment with no op, so a run
// whose file did not change costs one request and prints nothing. A
// changed one is built and applied server-side while the run goes on, as
// after Ctrl-C in `repose config apply`, and the run says so in one line.
// A refusal (a syntax error the api finds, a secret in the file) is a
// warning: the run itself still works.
//
// The api skips only a fragment that is already applied, so a file whose
// build failed would be built again by every run. The CLI remembers, per
// project, the hash of the file it last sent and the revision that made
// (repoConfigSent): the same file again is not resent while that revision
// builds, and after it failed the run names the error instead. `repose
// config apply` still sends it on demand.
//
// Only the project's own checkout counts: the repository whose remote is
// the project's (or its by_dir directory), or, on a temporary machine
// (which has no remote and is reached by name), the checkout this run
// just synced into it. Another repository's repose.nix never
// replaces this machine's configuration.
func (e *Env) applyRepoConfig(ctx context.Context, project *Project, root string, temp bool) {
	if root == "" || project == nil {
		return
	}
	if !temp && !e.checkoutOwnsProject(root, project) {
		return
	}
	path := filepath.Join(root, repoConfigName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		e.warn("Could not read %s (%s); the machine keeps its configuration.", repoConfigName, oneLine(err.Error()))
		return
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	sent := loadRepoConfigSent(e.Dir)
	if last, ok := sent[project.ID]; ok && last.Hash == hash {
		if e.lastRepoConfigStands(ctx, project, last.RevisionID) {
			return
		}
	}
	revisionID, opID, err := e.Client.PutConfigFragment(ctx, project.ID, string(b))
	if err != nil {
		var apiErr *APIError
		if asAPIError(err, &apiErr) && apiErr.Code == "invalid" {
			e.warn("%s was not applied (%s). `repose config apply` shows the whole error.", repoConfigName, strings.TrimSuffix(firstLine(apiErr.Message), "."))
			return
		}
		e.warn("Could not send %s (%s); the machine keeps its configuration. `repose config apply` sends it.", repoConfigName, oneLine(err.Error()))
		return
	}
	sent[project.ID] = repoConfigRecord{Hash: hash, RevisionID: revisionID}
	saveRepoConfigSent(e.Dir, sent)
	if opID == "" {
		return
	}
	_, _ = fmt.Fprintf(e.ErrOut, "Applying %s (revision %s) in the background. `repose config show --revisions` shows when it is done.\n", repoConfigName, shortRev(revisionID))
}

// lastRepoConfigStands reports whether the revision the same file made
// last time still stands for it: building, built or applied. A failed one
// is reported once per run and not rebuilt. One it cannot find (the
// revisions list is short, or the read failed) does not stand, and the
// file is sent again.
func (e *Env) lastRepoConfigStands(ctx context.Context, project *Project, revisionID string) bool {
	revs, err := e.Client.ListRevisions(ctx, project.ID)
	if err != nil {
		return false
	}
	for _, r := range revs {
		if r.ID != revisionID {
			continue
		}
		switch r.Status {
		case "failed", "error":
			// The api's code prefix ("eval_failed: ") and the name the
			// build gives the file are not what the user edits.
			msg := firstLine(r.Error)
			if code, rest, ok := strings.Cut(msg, ": "); ok && !strings.ContainsAny(code, " .") {
				msg = rest
			}
			msg = strings.TrimSuffix(strings.ReplaceAll(msg, "fragment.nix", repoConfigName), ".")
			if msg == "" {
				msg = "the build failed"
			}
			e.warn("%s did not build last time (revision %s: %s), so it was not sent again. Fix it and run again, or `repose config apply` to retry it as it is.", repoConfigName, shortRev(r.ID), msg)
			return true
		default:
			return true
		}
	}
	return false
}

// repoConfigRecord is what the CLI last sent of a project's repose.nix.
type repoConfigRecord struct {
	Hash       string `json:"sha256"`
	RevisionID string `json:"revision_id"`
}

// repoConfigSentPath is the CLI's record of the repose.nix files it sent,
// by project id. Only hashes and revision ids, never the file.
func repoConfigSentPath(dir string) string { return filepath.Join(dir, "repo-config.json") }

func loadRepoConfigSent(dir string) map[string]repoConfigRecord {
	m := map[string]repoConfigRecord{}
	if b, err := os.ReadFile(repoConfigSentPath(dir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m == nil {
		m = map[string]repoConfigRecord{}
	}
	return m
}

func saveRepoConfigSent(dir string, m map[string]repoConfigRecord) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil || dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = writeFileAtomic(repoConfigSentPath(dir), b, 0o600)
}
