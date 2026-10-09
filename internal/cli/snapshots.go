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

// SnapshotsCreateCmd implements `repose snapshots create`. The line names
// the snapshot, so `repose snapshots restore` can take it from there
// (DECISIONS I-615).
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
	if interrupted(ctx, err) {
		return exitf(ExitInterrupted, "Interrupted. The snapshot of %s goes on.", project.Slug)
	}
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("snapshot", project.Slug, op.Error, "")
	}
	took := fmtElapsed(pr.Total())
	if id, _ := op.Result["snapshot_id"].(string); id != "" {
		if snaps, err := e.Client.ListSnapshots(ctx, project.ID); err == nil {
			if s := stopSnapshot(snaps, id); s != nil {
				_, _ = fmt.Fprintf(e.Out, "Snapshot %s of %s taken in %s (%s).\n", id, project.Slug, took, humanBytes(s.Bytes))
				return nil
			}
		}
		_, _ = fmt.Fprintf(e.Out, "Snapshot %s of %s taken in %s.\n", id, project.Slug, took)
		return nil
	}
	_, _ = fmt.Fprintf(e.Out, "Snapshot of %s taken in %s.\n", project.Slug, took)
	return nil
}

// snapshotTime is how a question or a line names a snapshot: when it
// was taken, in the laptop's zone, as `repose snapshots list` shows it.
func snapshotTime(s *Snapshot) string { return s.CreatedAt.Local().Format("2006-01-02 15:04") }

// restoreInPlacePrompt is the question before a snapshot replaces a
// stopped project's disk (DECISIONS I-614). It names the project and the
// snapshot, and says whether a snapshot keeps the disk as it is now: the
// newest one, when a stop took it after the last start. Otherwise what
// changed since the newest snapshot is lost for good, as after `resize
// --size`, whose stop takes none.
func restoreInPlacePrompt(p *Project, snaps []Snapshot, id string) string {
	var target *Snapshot
	for i := range snaps {
		if snaps[i].ID == id {
			target = &snaps[i]
		}
	}
	if target == nil {
		return fmt.Sprintf("Replace %s's disk with snapshot %s? [y/N] ", p.Slug, id)
	}
	q := fmt.Sprintf("Replace %s's disk with its snapshot of %s? ", p.Slug, snapshotTime(target))
	newest := newestSnapshot(snaps)
	switch {
	case newest.ID == target.ID, p.StartedAt == nil:
		// The same snapshot, or a project that has not said when it last
		// started (never started, or an api that clears it on stop): no
		// claim either way.
	case newest.Reason == "stop" && newest.CreatedAt.After(*p.StartedAt):
		q += fmt.Sprintf("The stop snapshot of %s keeps the disk as it is now. ", snapshotTime(newest))
	default:
		q += fmt.Sprintf("No snapshot keeps the disk as it is now; what changed after %s is lost for good. ", snapshotTime(newest))
	}
	return q + "[y/N] "
}

// SnapshotsRestoreCmd implements `repose snapshots restore [PROJECT] SNAPSHOT_ID
// [--as-new NAME]`. Without --as-new it requires the project stopped and
// asks for confirmation (07-cli.md §5.10); a no exits 1 (I-614).
func SnapshotsRestoreCmd(ctx context.Context, e *Env, projectArg, snapshotID, asNew string, confirm func(prompt string) (bool, error)) error {
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
	// The snapshot's time names it in the question and the progress line;
	// an api that does not list it leaves the id.
	snaps, _ := e.Client.ListSnapshots(ctx, project.ID)
	from := snapshotID
	for i := range snaps {
		if snaps[i].ID == snapshotID {
			from = project.Slug + "'s snapshot of " + snapshotTime(&snaps[i])
		}
	}
	if asNew == "" {
		if project.State != "stopped" {
			return exitf(ExitGuestNotRunning, "%s must be stopped before restoring over it: `repose stop %s` first, or restore into a new project with --as-new NAME.", project.Slug, project.Slug)
		}
		if confirm != nil {
			if err := confirmOr(confirm, restoreInPlacePrompt(project, snaps, snapshotID), "Not restored."); err != nil {
				return err
			}
		}
	}
	pr := e.newProgress()
	defer pr.Fail()
	opID, owner, err := e.Client.RestoreSnapshot(ctx, project.ID, snapshotID, asNew)
	if err != nil {
		return err
	}
	into := project.Slug
	if asNew != "" {
		into = asNew
	}
	pr.Phase("Restoring "+into+" from "+from, "")
	// With --as-new the op belongs to the new project; asked under the
	// source it is not_found.
	waitOn := project
	if owner != project.ID {
		waitOn = &Project{ID: owner, Slug: asNew}
	}
	op, err := waitOpPhased(ctx, e, waitOn, opID, pr, false)
	if interrupted(ctx, err) {
		return exitf(ExitInterrupted, "Interrupted. The restore into %s goes on.", into)
	}
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
