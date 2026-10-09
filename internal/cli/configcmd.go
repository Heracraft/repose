package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ConfigShowCmd implements `repose config show [--revisions]`.
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
			line := fmt.Sprintf("%s\t%s\t%s", r.ID, r.Status, tableTime(r.CreatedAt))
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
		return exitf(ExitUsage, "No ./repose.nix here, and %s has no revision that built to apply again. `repose config show --revisions` lists them.", project.Slug)
	}
	if rev.Status == "applied" {
		_, _ = fmt.Fprintf(e.Out, "No ./repose.nix here; applying the active revision %s again.\n", shortRev(rev.ID))
	} else {
		_, _ = fmt.Fprintf(e.Out, "No ./repose.nix here; applying revision %s, which built but is not applied yet.\n", shortRev(rev.ID))
	}
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
