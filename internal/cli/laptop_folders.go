package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// A machine's laptop folders (DECISIONS I-638). `stop` fetches the
// agent's commits and `status` counts what the laptop lacks in every
// folder on this laptop whose `repose` remote is the machine, wherever
// the command runs: a machine stopped from ~ with `stop api web` or
// `stop --unused` no longer leaves its commits behind until the next
// start. projects.json's `folders` remembers each folder the CLI pointed
// a `repose` remote at; by_dir and checkouts name more. A folder counts
// only while its `repose` remote still names the machine.

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

// fetchJob is one folder fetchBeforeStop fetches into.
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
	return fmt.Sprintf("Fetched %s in %s.", j.fetched, tildePath(j.root))
}

// fetchFailure is the warning for a failed fetch, or "".
func (j *fetchJob) fetchFailure(what string) string {
	if j.err == nil {
		return ""
	}
	where := ""
	if !j.here {
		where = " into " + tildePath(j.root)
	}
	return fmt.Sprintf("Could not fetch from %s%s before %s: %s", j.slug, where, what, oneLine(j.err.Error()))
}

// slugsOfJobs is the projects the jobs fetch from, each once, in order.
func slugsOfJobs(jobs []*fetchJob) []string {
	var out []string
	for _, j := range jobs {
		if len(out) == 0 || out[len(out)-1] != j.slug {
			out = append(out, j.slug)
		}
	}
	return out
}
