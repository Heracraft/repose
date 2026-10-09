package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Short ids (DECISIONS I-619). Snapshot and revision ids are UUIDv7: the
// first characters are a timestamp that changes about once a minute, so
// two snapshots of one afternoon share their first eight. Tables show the
// random last eight, as `repose questions` does, and every command that
// takes such an id takes the whole id, its end or its start.

// minIDPart is the shortest end or start of an id a command takes.
const minIDPart = 4

// matchID returns the ids that part names: the one equal to it, else
// each that ends or starts with it.
func matchID(ids []string, part string) []string {
	part = strings.ToLower(strings.TrimSpace(part))
	for _, id := range ids {
		if id == part {
			return []string{id}
		}
	}
	if len(part) < minIDPart {
		return nil
	}
	var out []string
	for _, id := range ids {
		if strings.HasSuffix(id, part) || strings.HasPrefix(id, part) {
			out = append(out, id)
		}
	}
	return out
}

// resolveSnapshotID is the full id of the project's snapshot that idArg
// names. A full id passes as it is, with no extra call; the api answers
// for one that does not exist.
func resolveSnapshotID(ctx context.Context, e *Env, projectArg, idArg string) (string, error) {
	if looksLikeUUID(idArg) {
		return idArg, nil
	}
	project, err := projectForSnapshots(ctx, e, projectArg)
	if err != nil {
		return "", err
	}
	snaps, err := e.Client.ListSnapshots(ctx, project.ID)
	if err != nil {
		return "", err
	}
	ids := make([]string, len(snaps))
	for i, s := range snaps {
		ids[i] = s.ID
	}
	switch hits := matchID(ids, idArg); len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", exitf(ExitUsage, "%s has no snapshot %s. `repose snapshots list %s` lists them.", project.Slug, idArg, project.Slug)
	default:
		return "", exitf(ExitUsage, "%s names %d snapshots of %s; give more of the id.", idArg, len(hits), project.Slug)
	}
}

// completeSnapshotRestore completes `snapshots restore [PROJECT]
// SNAPSHOT_ID`: projects and the checkout's snapshots first, then the
// named project's snapshots.
func completeSnapshotRestore(env func() (*Env, error), g *globalFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return append(projectSlugsForCompletion(env), snapshotIDsForCompletion(env, g.project)...), cobra.ShellCompDirectiveNoFileComp
		case 1:
			return snapshotIDsForCompletion(env, args[0]), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// snapshotIDsForCompletion is the project's snapshot ids, newest first
// (two seconds at most, a shell is waiting).
func snapshotIDsForCompletion(env func() (*Env, error), projectArg string) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	project, err := projectForSnapshots(ctx, e, projectArg)
	if err != nil {
		return nil
	}
	snaps, err := e.Client.ListSnapshots(ctx, project.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.ID)
	}
	return out
}

// revisionIDsForCompletion is the project's revisions that built, for
// `config apply --revision`.
func revisionIDsForCompletion(env func() (*Env, error), projectArg string) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return nil
	}
	revs, err := e.Client.ListRevisions(ctx, project.ID)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range revs {
		if r.Status == "built" || r.Status == "applied" {
			out = append(out, r.ID)
		}
	}
	return out
}
