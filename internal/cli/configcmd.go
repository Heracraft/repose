package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ConfigShowCmd implements `repose config show`; --revisions, hidden
// since `config revisions` (I-622), keeps its old lines for a release.
func ConfigShowCmd(ctx context.Context, e *Env, projectArg string, revisions bool) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if revisions {
		revs, err := e.Client.ListRevisions(ctx, project.ID)
		if err != nil {
			return err
		}
		if e.JSON {
			return writeJSONOut(e.Out, revs)
		}
		for _, r := range revs {
			line := fmt.Sprintf("%s\t%s\t%s", r.ID, r.Status, r.CreatedAt.Format("2006-01-02 15:04"))
			if r.Error != "" {
				line += "\t" + r.Error
			}
			_, _ = fmt.Fprintln(e.Out, line)
		}
		return nil
	}
	cfg, err := e.Client.GetConfig(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, cfg)
	}
	_, _ = fmt.Fprint(e.Out, cfg.Fragment)
	return nil
}

// applyFragmentAndRender PUTs a fragment, streams the build log, and
// prints the error block on failure (07-cli.md §5.10, §5.8).
func applyFragmentAndRender(ctx context.Context, e *Env, project *Project, fragment, localFragmentPath string) error {
	revisionID, opID, err := e.Client.PutConfigFragment(ctx, project.ID, fragment)
	if err != nil {
		var apiErr *APIError
		if ok := asAPIError(err, &apiErr); ok && apiErr.Code == "invalid" {
			RenderBuildError(e.Out, apiErr.Code, apiErr.Message, localFragmentPath, mustReadFragment(localFragmentPath))
			return silent(ExitBuildFailed)
		}
		return err
	}
	if opID == "" {
		_, _ = fmt.Fprintf(e.Out, "configuration unchanged; %s is still active\n", revisionID)
		return nil
	}
	_, _ = fmt.Fprintf(e.Out, "Building revision %s ...\n", shortRev(revisionID))
	op, pr, err := waitConfigOp(ctx, e, project, opID)
	if err != nil {
		return err
	}
	if op.State == "error" {
		RenderBuildError(e.Out, op.Error.Code, op.Error.Message, localFragmentPath, mustReadFragment(localFragmentPath))
		return silent(ExitBuildFailed)
	}
	if !op.RebootRequired {
		// An api before the op carried reboot_required said it on the
		// revision only.
		if revs, err := e.Client.ListRevisions(ctx, project.ID); err == nil {
			for _, r := range revs {
				if r.ID == revisionID && r.RebootRequired {
					op.RebootRequired = true
				}
			}
		}
	}
	printApplied(e, project, op, revisionID, pr)
	return nil
}

func mustReadFragment(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

func asAPIError(err error, target **APIError) bool {
	if ae, ok := err.(*APIError); ok {
		*target = ae
		return true
	}
	return false
}

// ConfigApplyCmd implements `repose config apply [PATH]`.
func ConfigApplyCmd(ctx context.Context, e *Env, projectArg, path string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if path == "" {
		if _, err := os.Stat("./repose.nix"); errors.Is(err, os.ErrNotExist) {
			return reapplyRevision(ctx, e, project)
		}
		path = "./repose.nix"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return exitf(ExitUsage, "reading %s: %v", path, err)
	}
	return applyFragmentAndRender(ctx, e, project, string(b), path)
}

// reapplyRevision is `repose config apply` with no file to apply (I-321):
// switch the machine to the project's newest revision that built, again.
// That is the active one after an apply that went through, or a newer
// one whose switch failed or was never sent; either way the machine ends
// up on the configuration the project says it has.
func reapplyRevision(ctx context.Context, e *Env, project *Project) error {
	if project.State != "running" {
		return exitf(ExitUsage, "No ./repose.nix here, and %s is %s. A stopped machine starts on its newest built revision: `repose start %s`.", project.Slug, project.State, project.Slug)
	}
	revs, err := e.Client.ListRevisions(ctx, project.ID)
	if err != nil {
		return err
	}
	var rev *Revision
	for i := range revs { // newest first
		if revs[i].Status == "built" || revs[i].Status == "applied" {
			rev = &revs[i]
			break
		}
	}
	if rev == nil {
		return exitf(ExitUsage, "No ./repose.nix here, and %s has no revision that built to apply again. `repose config revisions %s` lists them.", project.Slug, project.Slug)
	}
	if rev.Status == "applied" {
		_, _ = fmt.Fprintf(e.Out, "No ./repose.nix here; applying the active revision %s again.\n", shortRev(rev.ID))
	} else {
		_, _ = fmt.Fprintf(e.Out, "No ./repose.nix here; applying revision %s, which built but is not applied yet.\n", shortRev(rev.ID))
	}
	return applyRevision(ctx, e, project, rev)
}

// ConfigApplyRevisionCmd is `repose config apply --revision ID` (I-622):
// switch the machine to an earlier revision that built, the way back
// after a `config add` that broke something. ID is a revision's id or
// its start, as `repose config revisions` and "Building revision
// 4f1c2a9e" show it.
func ConfigApplyRevisionCmd(ctx context.Context, e *Env, projectArg, id string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	revs, err := e.Client.ListRevisions(ctx, project.ID)
	if err != nil {
		return err
	}
	rev, err := findRevision(revs, project.Slug, id)
	if err != nil {
		return err
	}
	if rev.Status != "built" && rev.Status != "applied" {
		return exitf(ExitUsage, "Revision %s of %s is %s; only one that built can be applied.", shortRev(rev.ID), project.Slug, rev.Status)
	}
	if project.State != "running" {
		return exitf(ExitGuestNotRunning, "%s is %s; a revision is applied to a running machine: `repose start %s` first.", project.Slug, project.State, project.Slug)
	}
	return applyRevision(ctx, e, project, rev)
}

// findRevision is the revision id names: its whole id, its end (as
// "Building revision 9c41d2e7" shows it) or its start.
func findRevision(revs []Revision, slug, id string) (*Revision, error) {
	ids := make([]string, len(revs))
	for i, r := range revs {
		ids[i] = r.ID
	}
	switch hits := matchID(ids, id); len(hits) {
	case 1:
		for i := range revs {
			if revs[i].ID == hits[0] {
				return &revs[i], nil
			}
		}
	case 0:
	default:
		return nil, exitf(ExitUsage, "%s names %d revisions of %s; give more of the id.", id, len(hits), slug)
	}
	return nil, exitf(ExitUsage, "%s has no revision %s. `repose config revisions %s` lists them.", slug, id, slug)
}

// applyRevision switches the running machine to rev and waits for it.
func applyRevision(ctx context.Context, e *Env, project *Project, rev *Revision) error {
	opID, err := e.Client.ApplyRevision(ctx, project.ID, rev.ID)
	if err != nil {
		var apiErr *APIError
		if asAPIError(err, &apiErr) && apiErr.Code == "conflict" && strings.Contains(apiErr.Message, "kernel") {
			return exitf(ExitUsage, "Revision %s changes the kernel, so it applies when %s restarts: `repose stop %s && repose start %s` when the agents are idle.", shortRev(rev.ID), project.Slug, project.Slug, project.Slug)
		}
		return err
	}
	op, pr, err := waitConfigOp(ctx, e, project, opID)
	if err != nil {
		return err
	}
	if op.State == "error" {
		_, _ = fmt.Fprintf(e.Out, "error: %s\n", firstLine(op.Error.Message))
		if rest := strings.Trim(restAfterFirstLine(op.Error.Message), "\n"); rest != "" {
			_, _ = fmt.Fprintf(e.Out, "\n%s\n", rest)
		}
		return silent(ExitGeneric)
	}
	printApplied(e, project, op, rev.ID, pr)
	return nil
}

// ConfigRevisionsCmd is `repose config revisions [PROJECT]` (I-622): the
// project's revisions, newest first, with the id `config apply
// --revision` takes.
func ConfigRevisionsCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	revs, err := e.Client.ListRevisions(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		if revs == nil {
			revs = []Revision{}
		}
		return writeJSONOut(e.Out, revs)
	}
	if e.Quiet {
		for _, r := range revs {
			_, _ = fmt.Fprintln(e.Out, r.ID)
		}
		return nil
	}
	if len(revs) == 0 {
		_, _ = fmt.Fprintf(e.Out, "%s has no revisions yet.\n", project.Slug)
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REVISION\tCREATED\tSTATUS\tMACHINE.NIX\tERROR")
	for _, r := range revs {
		personal := "-"
		if r.Personal {
			personal = "yes"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", shortRev(r.ID), r.CreatedAt.Local().Format("2006-01-02 15:04"), r.Status, personal, firstLine(r.Error))
	}
	return tw.Flush()
}

// PersonalSwitchCmd is `repose config --global on|off [PROJECT]` (I-622):
// your machine.nix on or off for one machine, the undo of `repose run
// --no-personal` and what the dashboard's switch does.
func PersonalSwitchCmd(ctx context.Context, e *Env, projectArg string, on bool) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	word, with := "off", "without"
	if on {
		word, with = "on", "with"
	}
	if project.PersonalOptOut == !on {
		_, _ = fmt.Fprintf(e.Out, "machine.nix is already %s for %s.\n", word, project.Slug)
		return nil
	}
	optOut := !on
	if _, err := e.Client.PatchProject(ctx, project.ID, PatchProjectRequest{PersonalOptOut: &optOut}); err != nil {
		return err
	}
	if project.State == "running" {
		_, _ = fmt.Fprintf(e.Out, "machine.nix is %s for %s; the machine switches %s it in the background.\n", word, project.Slug, with)
	} else {
		_, _ = fmt.Fprintf(e.Out, "machine.nix is %s for %s; the machine starts %s it.\n", word, project.Slug, with)
	}
	return nil
}

// ConfigEditCmd implements `repose config edit`: fetch, open $EDITOR on a
// temp file, PUT on save. editor is injected for testing.
func ConfigEditCmd(ctx context.Context, e *Env, projectArg string, editor func(path string) error) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	cfg, err := e.Client.GetConfig(ctx, project.ID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "repose-config-*.nix")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(cfg.Fragment); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := editor(tmp.Name()); err != nil {
		return err
	}
	b, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	return applyFragmentAndRender(ctx, e, project, string(b), tmp.Name())
}
