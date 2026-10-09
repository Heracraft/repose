package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
)

// projectForSnapshots resolves the project for the snapshot commands. A
// destroyed project no longer resolves by name, but its snapshots are
// kept 30 days and the api serves them by id (userProjectAny), which is
// exactly what `repose rm` prints: `--project <id>` (I-153).
func projectForSnapshots(ctx context.Context, e *Env, projectArg string) (*Project, error) {
	arg := e.resolveArg(projectArg)
	if looksLikeUUID(arg) {
		p, err := e.Client.GetProject(ctx, arg)
		if err == nil {
			return p, nil
		}
		if isNotFound(err) {
			return &Project{ID: arg, Slug: arg, State: "destroyed"}, nil
		}
		return nil, err
	}
	return requireProject(ctx, e, projectArg)
}

// SnapshotsListCmd implements `repose snapshots list`.
func SnapshotsListCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := projectForSnapshots(ctx, e, projectArg)
	if err != nil {
		return err
	}
	snaps, err := e.Client.ListSnapshots(ctx, project.ID)
	if err != nil {
		return err
	}
	if e.JSON {
		if snaps == nil {
			snaps = []Snapshot{}
		}
		return writeJSONOut(e.Out, snaps)
	}
	if e.Quiet {
		for _, s := range snaps {
			_, _ = fmt.Fprintln(e.Out, s.ID)
		}
		return nil
	}
	if len(snaps) == 0 {
		_, _ = fmt.Fprintf(e.Out, "%s has no snapshots yet.\n", project.Slug)
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTAKEN\tSIZE\tREASON")
	for _, s := range snaps {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.ID, s.CreatedAt.Local().Format("2006-01-02 15:04"), humanBytes(s.Bytes), s.Reason)
	}
	return tw.Flush()
}

// SnapshotsCreateCmd implements `repose snapshots create`.
func SnapshotsCreateCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	pr := e.newProgress()
	defer pr.Fail()
	opID, err := e.Client.CreateSnapshot(ctx, project.ID)
	if err != nil {
		return err
	}
	pr.Phase("Snapshotting "+project.Slug, "")
	op, err := waitOpPhased(ctx, e, project, opID, pr, false)
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("snapshot", project.Slug, op.Error, "")
	}
	if e.JSON {
		// The snapshot the op recorded (its result names it), else the
		// newest, for an api that does not say (I-609).
		snaps, err := e.Client.ListSnapshots(ctx, project.ID)
		if err != nil {
			return err
		}
		id, _ := op.Result["snapshot_id"].(string)
		var got *Snapshot
		for i := range snaps {
			s := &snaps[i]
			if (id != "" && s.ID == id) || (id == "" && (got == nil || s.CreatedAt.After(got.CreatedAt))) {
				got = s
			}
		}
		if got == nil {
			return fmt.Errorf("the snapshot of %s was taken, and the api does not list it", project.Slug)
		}
		return writeJSONOut(e.Out, got)
	}
	_, _ = fmt.Fprintf(e.Out, "Snapshot of %s taken in %s.\n", project.Slug, fmtElapsed(pr.Total()))
	return nil
}

// SnapshotsRestoreCmd implements `repose snapshots restore [PROJECT] SNAPSHOT_ID
// [--as-new NAME]`. Without --as-new it requires the project stopped and
// asks for confirmation (07-cli.md §5.10).
func SnapshotsRestoreCmd(ctx context.Context, e *Env, projectArg, snapshotID, asNew string, confirm func() (bool, error)) error {
	var project *Project
	var err error
	if asNew != "" {
		project, err = projectForSnapshots(ctx, e, projectArg)
	} else {
		project, err = requireProject(ctx, e, projectArg)
	}
	if err != nil {
		return err
	}
	if asNew == "" {
		if project.State != "stopped" {
			return exitf(ExitGuestNotRunning, "%s must be stopped before restoring over it: `repose stop %s` first, or restore into a new project with --as-new NAME.", project.Slug, project.Slug)
		}
		if confirm != nil {
			ok, err := confirm()
			if err != nil {
				return err
			}
			if !ok {
				_, _ = fmt.Fprintln(e.Out, "Not restored.")
				return nil
			}
		}
	}
	pr := e.newProgress()
	defer pr.Fail()
	opID, owner, err := e.Client.RestoreSnapshot(ctx, project.ID, snapshotID, asNew)
	if err != nil {
		return err
	}
	pr.Phase("Restoring "+snapshotID, "")
	// With --as-new the op belongs to the new project; asked under the
	// source it is not_found.
	waitOn := project
	if owner != project.ID {
		waitOn = &Project{ID: owner, Slug: asNew}
	}
	op, err := waitOpPhased(ctx, e, waitOn, opID, pr, false)
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("restore", snapshotID, op.Error, "")
	}
	if asNew != "" {
		refreshSSHAccess(ctx, e, asNew)
		_, _ = fmt.Fprintf(e.Out, "Restored into a new project, %s.\n", asNew)
		return nil
	}
	_, _ = fmt.Fprintf(e.Out, "Restored %s; it is stopped.\n", project.Slug)
	return nil
}
