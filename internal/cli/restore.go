package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Restore by name (DECISIONS I-167). `repose destroy` (now `rm`) used to end with
// `repose snapshots restore <snapshot id> --project <project id> --as-new
// NAME`; now it ends with `repose restore <slug>`, and the api resolves
// the name, the snapshot and the new project.

// DestroyedProject is one row of GET /projects/destroyed (api.md).
type DestroyedProject struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Slug            string     `json:"slug"`
	Class           string     `json:"class"`
	RemoteURL       string     `json:"remote_url,omitempty"`
	VolumeBytes     int64      `json:"volume_bytes"`
	DestroyedAt     time.Time  `json:"destroyed_at"`
	NameFree        bool       `json:"name_free"`
	RestorableUntil *time.Time `json:"restorable_until"`
	Snapshot        Snapshot   `json:"snapshot"`
}

// RestoreRequest is POST /projects/restore's body.
type RestoreRequest struct {
	Slug       string `json:"slug,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// RestoreResult is POST /projects/restore's answer.
type RestoreResult struct {
	OpID              string    `json:"op_id"`
	ProjectID         string    `json:"project_id"`
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	SnapshotID        string    `json:"snapshot_id"`
	SnapshotCreatedAt time.Time `json:"snapshot_created_at"`
	// SnapshotBytes is the snapshot's stored size; 0 from an api older
	// than I-595.
	SnapshotBytes int64  `json:"snapshot_bytes,omitempty"`
	FromProjectID string `json:"from_project_id"`
}

// destroyedPage is how many destroyed projects each request asks for.
const destroyedPage = 200

// ListDestroyed is every destroyed project that can still be restored,
// newest destroy first: page after page (I-420), until one comes back
// short or with nothing new, which is how an api older than I-420, with
// its one list of 100, ends it.
func (c *Client) ListDestroyed(ctx context.Context) ([]DestroyedProject, error) {
	var out []DestroyedProject
	seen := map[string]bool{}
	path := "/projects/destroyed?limit=" + strconv.Itoa(destroyedPage)
	for {
		var page []DestroyedProject
		if err := c.get(ctx, path, &page); err != nil {
			return nil, err
		}
		added := 0
		for _, d := range page {
			if !seen[d.ID] {
				seen[d.ID] = true
				out = append(out, d)
				added++
			}
		}
		if len(page) < destroyedPage || added == 0 {
			return out, nil
		}
		path = "/projects/destroyed?limit=" + strconv.Itoa(destroyedPage) + "&before=" + url.QueryEscape(page[len(page)-1].ID)
	}
}

func (c *Client) RestoreByName(ctx context.Context, req RestoreRequest) (*RestoreResult, error) {
	var r RestoreResult
	if err := c.post(ctx, "/projects/restore", req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// restoreDestroyWait bounds how long `repose restore` waits for a destroy
// in progress to take its final snapshot (a stop and a snapshot: seconds
// since I-164/I-165, a minute on a guest that ignores the shutdown).
var restoreDestroyWait = 3 * time.Minute

// stillDestroying reports whether the user's live project called slug is
// being destroyed right now.
func stillDestroying(ctx context.Context, e *Env, slug string) bool {
	if slug == "" {
		return false
	}
	projects, err := e.Client.ListProjects(ctx)
	if err != nil {
		return false
	}
	for _, p := range projects {
		if p.Slug == slug && p.State == "destroying" {
			return true
		}
	}
	return false
}

// restoreHint is the line a destroy ends with.
func restoreHint(slug string) string { return "repose restore " + slug }

// RestoreCmd implements `repose restore [NAME] [--as NEW] [--snapshot ID]`;
// with no NAME and no --snapshot, the checkout's remote finds it.
// NAME is the project's name (or id); it is restored from its newest
// snapshot (or --snapshot) as a new project called NAME, or NEW. When a
// live project holds the name, a terminal is asked for another one
// (askName) and anything else is told to pass --as.
func RestoreCmd(ctx context.Context, e *Env, name, as, snapshotID string, askName func(prompt string) (string, error)) error {
	name = strings.TrimSpace(name)
	req := RestoreRequest{SnapshotID: snapshotID, Name: as}
	switch {
	case name == "" && snapshotID == "":
		// Inside a checkout, the remote names the project (I-172), the
		// way it does for run, attach and the rest.
		d, err := destroyedForCheckout(ctx, e, askName)
		if err != nil || d == nil {
			return err
		}
		req.ProjectID, name = d.ID, d.Slug
	case looksLikeUUID(name):
		req.ProjectID = name
	case name != "":
		req.Slug = name
	}
	var res *RestoreResult
	wpr := e.newProgress()
	defer wpr.Fail()
	var waitStart time.Time
	for {
		var err error
		res, err = e.Client.RestoreByName(ctx, req)
		if err == nil {
			wpr.Fail()
			break
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			return err
		}
		switch {
		case apiErr.Code == "conflict" && apiErr.Detail["reason"] == "destroying",
			// An api older than I-190 answers "no snapshot left" while the
			// destroy that takes it is still running, and "name taken" (by
			// the project being destroyed) once the snapshot is there.
			apiErr.Code == "not_found" && apiErr.Detail["reason"] == "no_snapshot" && stillDestroying(ctx, e, req.Slug),
			apiErr.Code == "conflict" && apiErr.Detail["reason"] == "name_taken" && req.Name == "" && stillDestroying(ctx, e, req.Slug):
			if waitStart.IsZero() {
				waitStart = time.Now()
				wpr.Phase(fmt.Sprintf("Waiting for %s's destroy to take its final snapshot", name), "")
			}
			if time.Since(waitStart) > restoreDestroyWait {
				wpr.Fail()
				return exitf(ExitGeneric, "%s is still being destroyed after %s. `repose status %s` shows it; `repose restore %s` again once it is gone.", name, fmtElapsed(restoreDestroyWait), name, name)
			}
			if err := sleepOrDone(ctx, 2*time.Second); err != nil {
				return err
			}
			continue
		case apiErr.Code == "conflict" && apiErr.Detail["reason"] == "name_taken":
			taken, _ := apiErr.Detail["name"].(string)
			if askName == nil {
				return exitf(ExitUsage, "A project called %s already exists. Restore under another name with `repose restore %s --as NEW-NAME`.", taken, name)
			}
			newName, err := askName(fmt.Sprintf("A project called %s already exists. Name for the restored one (empty to cancel): ", taken))
			if err != nil {
				return err
			}
			if strings.TrimSpace(newName) == "" {
				_, _ = fmt.Fprintln(e.Out, "Nothing restored.")
				return nil
			}
			req.Name = strings.TrimSpace(newName)
			continue
		case apiErr.Code == "not_found":
			return exitf(ExitProjectNotFound, "%s. `repose ls --destroyed` lists what can be restored.", strings.TrimSuffix(humaneMessage(apiErr.Message), "."))
		}
		return err
	}
	pr := e.newProgress()
	defer pr.Fail()
	// The same label as the project's restoring state, so it prints once (I-191);
	// the snapshot's time is on the final line.
	pr.Phase("Restoring "+res.Slug, "Restored "+res.Slug)
	project := &Project{ID: res.ProjectID, Slug: res.Slug, Name: res.Name}
	op, err := waitOpPhased(ctx, e, project, res.OpID, pr, true)
	if err != nil {
		return err
	}
	pr.Fail()
	if op.State == "error" {
		return e.opFailed("restore", res.Slug, op.Error, fmt.Sprintf("`repose status %s` shows where it stopped; `repose rm %s` removes it, and the snapshot stays restorable.", res.Slug, res.Slug))
	}
	p, err := e.Client.GetProject(ctx, res.ProjectID)
	if err != nil {
		return err
	}
	refreshSSHAccess(ctx, e, p.Slug)
	// The snapshot's size is what the restore's time went on, as a stop's
	// is (I-570, I-595).
	from := "its snapshot"
	if res.SnapshotBytes > 0 {
		from = "its " + humanBytes(res.SnapshotBytes) + " snapshot"
	}
	_, _ = fmt.Fprintf(e.Out, "Restored %s from %s of %s in %s; it is %s (%s).\n",
		p.Slug, from, res.SnapshotCreatedAt.Local().Format("2006-01-02 15:04"), fmtElapsed(pr.Total()), stateWords(p.State), p.Class)
	return nil
}

// destroyedForCheckout finds the destroyed project `repose restore` with
// no NAME means: the one whose remote is this checkout's (DECISIONS
// I-172). Several projects destroyed under one name are one choice (the
// newest destroy is restored, from its newest snapshot, as by name);
// several names are asked about on a terminal and listed otherwise. A nil
// project with a nil error means the user cancelled.
func destroyedForCheckout(ctx context.Context, e *Env, ask func(prompt string) (string, error)) (*DestroyedProject, error) {
	remote := gitRemoteOrigin(e.Cwd)
	if remote == "" {
		return nil, exitf(ExitUsage, "Name the project to restore: `repose restore NAME` (this directory has no git remote to find it by). `repose ls --destroyed` lists what can be restored.")
	}
	list, err := e.Client.ListDestroyed(ctx)
	if err != nil {
		return nil, err
	}
	var slugs []string
	newest := map[string]*DestroyedProject{} // the list is newest destroy first
	for i := range list {
		d := &list[i]
		if d.RemoteURL == "" || normalizeRemote(d.RemoteURL) != remote {
			continue
		}
		if newest[d.Slug] == nil {
			newest[d.Slug] = d
			slugs = append(slugs, d.Slug)
		}
	}
	switch {
	case len(slugs) == 0:
		return nil, exitf(ExitProjectNotFound, "No destroyed project was a checkout of %s. `repose ls --destroyed` lists what can be restored; `repose restore NAME` restores one.", remote)
	case len(slugs) == 1:
		return newest[slugs[0]], nil
	}
	names := strings.Join(slugs, ", ")
	if ask == nil {
		return nil, exitf(ExitUsage, "Several destroyed projects were checkouts of %s: %s. Name one: `repose restore NAME`.", remote, names)
	}
	for {
		answer, err := ask(fmt.Sprintf("Several destroyed projects were checkouts of %s: %s. Which one (empty to cancel)? ", remote, names))
		if err != nil {
			return nil, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			_, _ = fmt.Fprintln(e.Out, "Nothing restored.")
			return nil, nil
		}
		if d := newest[answer]; d != nil {
			return d, nil
		}
	}
}

// DestroyedCmd implements `repose ls --destroyed`: what can be
// restored, and until when. A name destroyed several times (izma ×3) is
// one row, the one `repose restore NAME` picks, with a count of the
// earlier ones; all lists every row with the id that restores it (I-192).
func DestroyedCmd(ctx context.Context, e *Env, all bool) error {
	list, err := e.Client.ListDestroyed(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		if list == nil {
			list = []DestroyedProject{}
		}
		return writeJSONOut(e.Out, list)
	}
	if e.Quiet {
		seen := map[string]bool{}
		for _, d := range list {
			if !seen[d.Slug] {
				seen[d.Slug] = true
				_, _ = fmt.Fprintln(e.Out, d.Slug)
			}
		}
		return nil
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(e.Out, "Nothing to restore: no project destroyed in the last 30 days still has a snapshot.")
		return nil
	}
	if all {
		writeDestroyedTableAll(e.Out, list)
	} else {
		writeDestroyedTable(e.Out, list)
	}
	return nil
}

// destroyedByName groups the list per name, each group newest snapshot
// first: the api's `repose restore NAME` takes the newest restorable
// snapshot among the destroyed projects that had the name (I-167), so a
// group's first row is the one it restores. Groups keep the order of
// their first row's destroy, newest first.
func destroyedByName(list []DestroyedProject) [][]DestroyedProject {
	idx := map[string]int{}
	var groups [][]DestroyedProject
	for _, d := range list {
		i, ok := idx[d.Slug]
		if !ok {
			i = len(groups)
			idx[d.Slug] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], d)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(a, b int) bool { return g[a].Snapshot.CreatedAt.After(g[b].Snapshot.CreatedAt) })
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a][0].DestroyedAt.After(groups[b][0].DestroyedAt) })
	return groups
}

func destroyedCells(d DestroyedProject) (until, destroyed, snap, size string) {
	until = "-"
	if d.RestorableUntil != nil {
		until = d.RestorableUntil.Local().Format("2006-01-02")
	}
	return until, d.DestroyedAt.Local().Format("2006-01-02 15:04"), d.Snapshot.CreatedAt.Local().Format("2006-01-02 15:04"), humanBytes(d.Snapshot.Bytes)
}

func writeDestroyedTable(w io.Writer, list []DestroyedProject) {
	groups := destroyedByName(list)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROJECT\tSIZE\tDESTROYED\tSNAPSHOT\tSTORED\tRESTORABLE UNTIL\tEARLIER")
	var inUse []DestroyedProject
	for _, g := range groups {
		d := g[0]
		until, destroyed, snap, size := destroyedCells(d)
		name := d.Slug
		if !d.NameFree {
			name += " (name in use)"
			inUse = append(inUse, d)
		}
		more := "-"
		if len(g) > 1 {
			more = fmt.Sprint(len(g) - 1)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, d.Class, destroyed, snap, size, until, more)
	}
	_ = tw.Flush()
	for _, d := range inUse {
		// With a live project of the name, `repose restore NAME` means the
		// live one (I-167), so the destroyed one is named by its id.
		_, _ = fmt.Fprintf(w, "A live project is called %s; this one comes back with `repose restore %s --as NEW-NAME`.\n", d.Slug, d.ID)
	}
}

func writeDestroyedTableAll(w io.Writer, list []DestroyedProject) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROJECT\tID\tSIZE\tDESTROYED\tSNAPSHOT\tSTORED\tRESTORABLE UNTIL")
	for _, g := range destroyedByName(list) {
		for i, d := range g {
			until, destroyed, snap, size := destroyedCells(d)
			name := d.Slug
			if i > 0 {
				name = "  (earlier)"
			} else if !d.NameFree {
				name += " (name in use)"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, d.ID, d.Class, destroyed, snap, size, until)
		}
	}
	_ = tw.Flush()
}

// destroyedSlugsForCompletion is what `repose restore <TAB>` offers.
func destroyedSlugsForCompletion(env func() (*Env, error)) []string {
	e, err := env()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := e.Client.ListDestroyed(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, d := range list {
		seen[d.Slug] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
