package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// A machine's laptop folders (DECISIONS I-638). `status` counts what the
// laptop lacks, and `stop -w` fetches before it removes a worktree, in
// every folder on this laptop whose `repose` remote is the machine,
// wherever the command runs. (`stop` and `rm` fetched here too until
// I-1240.) projects.json's `folders` remembers each folder the CLI
// pointed a `repose` remote at; by_dir and checkouts name more. A folder
// counts only while its `repose` remote still names the machine.

// rememberFolder records root as a laptop folder of project id, saving
// projects.json only when that is news. A failed save is a warning: the
// folder is a convenience for later commands, not this one.
func (e *Env) rememberFolder(root, id string) {
	if root == "" || id == "" || e.Cache.Folders[root] == id {
		return
	}
	if e.Cache.Folders == nil {
		e.Cache.Folders = map[string]string{}
	}
	e.Cache.Folders[root] = id
	if err := e.saveCache(); err != nil {
		e.warn("Could not save %s (%s).", projectsPath(e.Dir), oneLine(err.Error()))
	}
}

// laptopFolders is every checkout on this laptop whose `repose` remote
// names p's machine: the working directory's first, then the folders
// projects.json links to p, in name order.
func (e *Env) laptopFolders(p *Project) []string {
	if p == nil || p.Slug == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		root := gitRepoRoot(dir)
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		if reposeRemoteHost(remoteURLOf(root, reposeRemoteName)) == p.Slug {
			out = append(out, root)
		}
	}
	if e.Cwd != "" {
		add(e.Cwd)
	}
	var dirs []string
	for k, v := range e.Cache.Folders {
		if v == p.ID {
			dirs = append(dirs, k)
		}
	}
	for k, v := range e.Cache.ByDir {
		if v == p.ID {
			dirs = append(dirs, k)
		}
	}
	for k, c := range e.Cache.Checkouts {
		if c.ProjectID == p.ID {
			dirs = append(dirs, k)
		}
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if statOK(d) {
			add(d)
		}
	}
	return out
}

// fetchJob is one folder a fetch goes into (stop -w, stop_window.go).
type fetchJob struct {
	slug, root string
	here       bool // the working directory's checkout
	fetched    string
	err        error
}

// fetchJobsFor lists the folders of the running projects.
func (e *Env) fetchJobsFor(running []*Project) []*fetchJob {
	here := ""
	if e.Cwd != "" {
		here = gitRepoRoot(e.Cwd)
	}
	var jobs []*fetchJob
	for _, p := range running {
		if p.State != "running" {
			continue
		}
		for _, root := range e.laptopFolders(p) {
			jobs = append(jobs, &fetchJob{slug: p.Slug, root: root, here: root == here})
		}
	}
	return jobs
}

// runFetches fetches every job, four at a time.
func runFetches(ctx context.Context, jobs []*fetchJob) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			j.fetched, j.err = fetchRepose(ctx, j.root)
		}()
	}
	wg.Wait()
}

// laptopPath is an absolute path on this laptop as the user reads it:
// "~/code/api" under the home folder, the path itself anywhere else.
//
// It exists because these lines used tildePath, which spells a path the
// *guest's* shell gives it and so only ever takes a name relative to
// the guest's home. Handed a laptop absolute path it prepended "~/" to
// it, and the owner's stop printed
// "Fetched 10 commits ... in ~//home/agustus/Documents/projects/..."
// (I-892).
func laptopPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// fetchedLine is the line for one folder's fetch: "Fetched 3 commits on
// repose/main." in the working directory's checkout, with " in
// ~/code/api" for another folder; "" when nothing came.
func (j *fetchJob) fetchedLine() string {
	if j.err != nil || j.fetched == "" {
		return ""
	}
	if j.here {
		return fmt.Sprintf("Fetched %s.", j.fetched)
	}
	return fmt.Sprintf("Fetched %s in %s.", j.fetched, laptopPath(j.root))
}

// fetchFailure is the warning for a failed fetch, or "".
func (j *fetchJob) fetchFailure(what string) string {
	if j.err == nil {
		return ""
	}
	where := ""
	if !j.here {
		where = " into " + laptopPath(j.root)
	}
	return fmt.Sprintf("Could not fetch from %s%s before %s: %s", j.slug, where, what, oneLine(j.err.Error()))
}
