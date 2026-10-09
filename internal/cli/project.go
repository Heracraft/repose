package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// resolveDeps lets tests replace the git and filesystem calls resolution
// makes.
type resolveDeps struct {
	RemoteFor func(cwd string) string // "" means no remote
	RootFor   func(cwd string) string // the repo root, "" outside a repo
}

func defaultResolveDeps() resolveDeps {
	return resolveDeps{RemoteFor: gitRemoteOrigin, RootFor: gitRepoRoot}
}

// ResolveResult is what resolveProject found.
type ResolveResult struct {
	Project *Project // nil if nothing matched
	Remote  string   // normalised remote for cwd, "" if none
	// NoRemote says a project this run creates gets no remote_url even
	// though cwd has one: it is temporary, or a second project for a
	// repository whose remote another project has (DECISIONS I-348), like
	// a fork's copies (I-254).
	NoRemote bool
	// Checkout is the machine's other checkout cwd is, or PROJECT:CHECKOUT
	// named (`repose run --on`, DECISIONS I-480); "" for the checkout
	// itself.
	Checkout string
	// Home says no project was named and the working directory is the
	// home folder, which resolves to nothing (I-601).
	Home bool
}

// CreateRemote is the remote_url a project created from this result
// gets.
func (r *ResolveResult) CreateRemote() string {
	if r.NoRemote {
		return ""
	}
	return r.Remote
}

// resolveForRun is resolveProject for `repose run` and `repose sync`,
// which also take --name and --temp (DECISIONS I-348, I-351):
//
//   - --temp resolves nothing: the run always creates a new project, with
//     no remote, cached nowhere.
//   - --name picks the project with that name (or slug) wherever the run
//     is; without one, the run creates it. It never lands on a project of
//     another name. The new project takes the checkout's remote only when
//     no project has it yet (the first project for a repository, named);
//     otherwise it has none and is reached by name.
//   - otherwise the usual order (07-cli.md §5.3).
func resolveForRun(ctx context.Context, e *Env, opts RunOptions, attachOnly bool) (*ResolveResult, error) {
	deps := defaultResolveDeps()
	explicit := e.resolveArg(opts.ProjectArg)
	switch {
	case attachOnly:
		return resolveProject(ctx, e.Client, e.Dir, e.Cwd, explicit, &e.Cache, deps)
	case opts.Temp > 0:
		return &ResolveResult{Remote: deps.RemoteFor(e.Cwd), NoRemote: true}, nil
	case opts.On != "":
		if explicit == "" && inHomeFolder(e.Cwd, deps) {
			return nil, exitf(ExitUsage, "Your home folder is not a checkout, so it cannot join %s.", opts.On)
		}
		return resolveOn(ctx, e, opts.On, deps)
	case opts.Name != "":
		remote := deps.RemoteFor(e.Cwd)
		if inHomeFolder(e.Cwd, deps) {
			// A dotfiles repository's remote is not the machine's.
			remote = ""
		}
		p, err := findByName(ctx, e.Client, opts.Name)
		if err != nil {
			return nil, err
		}
		if p != nil {
			if co := e.extraCheckout(); co != nil && co.ProjectID == p.ID {
				// This folder is one of p's other checkouts (I-480).
				return &ResolveResult{Project: p, Remote: remote, Checkout: co.Name}, nil
			}
			if p.RemoteURL != "" && remote != "" && p.RemoteURL != remote {
				return nil, exitf(ExitUsage, "%s is the project for %s, and this checkout is %s, so `repose run %s` here would sync one repository into the other's machine. `repose attach %s` gets you onto it; another name makes a new machine for this checkout.", p.Slug, p.RemoteURL, remote, opts.Name, p.Slug)
			}
			return &ResolveResult{Project: p, Remote: remote}, nil
		}
		res, err := resolveProject(ctx, e.Client, e.Dir, e.Cwd, "", &e.Cache, deps)
		if err != nil {
			return nil, err
		}
		return &ResolveResult{Remote: res.Remote, NoRemote: res.Project != nil}, nil
	}
	res, err := resolveProject(ctx, e.Client, e.Dir, e.Cwd, explicit, &e.Cache, deps)
	if err != nil {
		return nil, err
	}
	if res.Home {
		return nil, errHomeRun()
	}
	if explicit == "" && res.Project != nil && res.Remote == "" && deps.RootFor(e.Cwd) == "" {
		// A directory that is not a repository (the home directory, say)
		// found its project through by_dir: the last one `run` made
		// here. Say which, and how to get another (I-348, I-358).
		e.warn("Using %s, the machine last made in this directory.", res.Project.Slug)
	}
	return res, nil
}

// resolveOn is resolveForRun for `repose run --on PROJECT` (I-480): the
// machine must exist, and this folder must not be its checkout or
// another machine's. A folder that joined before gets its checkout back;
// a new one gets Checkout "", and the run claims a name once connected.
func resolveOn(ctx context.Context, e *Env, on string, deps resolveDeps) (*ResolveResult, error) {
	p, err := findByIDOrSlug(ctx, e.Client, on)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, exitf(ExitProjectNotFound, "No repose project is called %s. `repose ls` lists yours.", on)
	}
	remote := deps.RemoteFor(e.Cwd)
	key := dirKey(e.Cwd, deps)
	if co := e.extraCheckout(); co != nil {
		if co.ProjectID != p.ID {
			return nil, exitf(ExitUsage, "This folder is already a checkout on another machine. Run `repose run` here to use it there.")
		}
		return &ResolveResult{Project: p, Remote: remote, Checkout: co.Name}, nil
	}
	if (remote != "" && remote == p.RemoteURL) || (p.RemoteURL == "" && e.Cache.ByDir[key] == p.ID) {
		return nil, exitf(ExitUsage, "This folder is %s's own checkout already. Run `repose run` here without --on.", p.Slug)
	}
	return &ResolveResult{Project: p, Remote: remote}, nil
}

// addCheckout makes this folder another checkout of project's machine
// (`repose run --on`, I-480): it claims a name there and remembers the
// folder in the cache. It returns the name.
func (e *Env) addCheckout(ctx context.Context, t sshTarget, project *Project) (string, error) {
	root := syncRoot(e.Cwd)
	want := checkoutName(root)
	if want == "" {
		want = "checkout"
	}
	name, err := claimCheckout(ctx, t, project.Slug, want)
	if err != nil {
		return "", err
	}
	if e.Cache.Checkouts == nil {
		e.Cache.Checkouts = map[string]CachedCheckout{}
	}
	e.Cache.Checkouts[dirKey(e.Cwd, defaultResolveDeps())] = CachedCheckout{ProjectID: project.ID, Name: name}
	if err := e.saveCache(); err != nil {
		e.warn("Could not save the folder's place in %s (%s); the next run here needs --on again.", projectsPath(e.Dir), oneLine(err.Error()))
	}
	_, _ = fmt.Fprintf(e.Out, "Added %s to %s as %s.\n", filepath.Base(root), project.Slug, tildePath(name))
	return name, nil
}

// extraCheckout is the cache's entry for cwd when `repose run --on`
// added this folder to a machine (I-480), nil otherwise.
func (e *Env) extraCheckout() *CachedCheckout {
	deps := defaultResolveDeps()
	for _, k := range uniqueStrings(dirKey(e.Cwd, deps), e.Cwd) {
		if co, ok := e.Cache.Checkouts[k]; ok {
			return &co
		}
	}
	return nil
}

// slugOf is the api's slug for a project name (internal/api/http Slug):
// lowercase, [a-z0-9-], runs of anything else one dash, at most 40.
func slugOf(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// findByName is the live project called name: by its name, or by the
// slug that name would get.
func findByName(ctx context.Context, client *Client, name string) (*Project, error) {
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	slug := slugOf(name)
	for _, p := range projects {
		if p.Name == name || (slug != "" && p.Slug == slug) {
			return &p, nil
		}
	}
	return nil, nil
}

// dirKey is the by_dir key for cwd: the repository root when cwd is inside
// one, so `repose run` from a subdirectory finds the same project, else
// cwd itself.
func dirKey(cwd string, deps resolveDeps) string {
	if deps.RootFor != nil {
		if root := deps.RootFor(cwd); root != "" {
			return root
		}
	}
	return cwd
}

// resolveProject implements docs/workstreams/07-cli.md §5.3's order. It
// mutates and saves cache in place when a lookup fills in something the
// cache did not have. explicit is the positional PROJECT, --project or
// $REPOSE_PROJECT; empty when none was given.
//
// Two rules keep the directory cache honest (DECISIONS I-152): an
// explicit project is never written to by_dir (naming a project is not a
// statement about the directory you happen to be in), and a by_dir entry
// is only believed when that project's remote is the directory's remote
// (or neither has one), so a stale or poisoned entry can never send
// `repose run` in one repository to another repository's guest.
func resolveProject(ctx context.Context, client *Client, dir, cwd, explicit string, cache *ProjectsCache, deps resolveDeps) (*ResolveResult, error) {
	if explicit != "" {
		explicit, checkout, hasCheckout := strings.Cut(explicit, ":")
		if hasCheckout && (checkout == "" || dirProjectName(checkout) != checkout) {
			return nil, exitf(ExitUsage, "%s:%s does not name a checkout. PROJECT:CHECKOUT takes the checkout's folder name on the machine, as `repose ps` shows it before the /.", explicit, checkout)
		}
		p, err := findByIDOrSlug(ctx, client, explicit)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, exitf(ExitProjectNotFound, "No repose project is called %s. `repose ls` lists yours.", explicit)
		}
		return &ResolveResult{Project: p, Checkout: checkout}, nil
	}

	key := dirKey(cwd, deps)
	if inHomeFolder(cwd, deps) {
		// The home folder is no project's (I-601). An older CLI may have
		// linked it to the first machine made there: forget that.
		forget := func(c *ProjectsCache) bool {
			forgot := false
			for _, k := range uniqueStrings(key, cwd) {
				if _, ok := c.ByDir[k]; ok {
					delete(c.ByDir, k)
					forgot = true
				}
				if _, ok := c.Checkouts[k]; ok {
					delete(c.Checkouts, k)
					forgot = true
				}
			}
			return forgot
		}
		forget(cache)
		// From the file as it is, as forgetProjectOnDisk does.
		if disk, err := loadProjectsCache(dir); err == nil && forget(&disk) {
			_ = saveProjectsCache(dir, disk)
		}
		return &ResolveResult{Home: true}, nil
	}
	remote := deps.RemoteFor(cwd)
	// A folder added to another machine with `repose run --on` (I-480).
	// It has a remote of its own, which is not the project's, so the
	// by_dir rule below would refuse it.
	for _, k := range uniqueStrings(key, cwd) {
		co, ok := cache.Checkouts[k]
		if !ok {
			continue
		}
		p, err := client.GetProject(ctx, co.ProjectID)
		if err == nil {
			return &ResolveResult{Project: p, Remote: remote, Checkout: co.Name}, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
		delete(cache.Checkouts, k)
		_ = saveProjectsCache(dir, *cache)
	}
	for _, k := range uniqueStrings(key, cwd) {
		id, ok := cache.ByDir[k]
		if !ok {
			continue
		}
		p, err := client.GetProject(ctx, id)
		if err == nil && p.RemoteURL == remote {
			return &ResolveResult{Project: p, Remote: remote}, nil
		}
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		// Destroyed, moved, or a different repository's project (the
		// v0.1.4 cache wrote by_dir for every --project): forget it.
		delete(cache.ByDir, k)
		_ = saveProjectsCache(dir, *cache)
	}

	if remote == "" {
		return &ResolveResult{}, nil
	}
	if cached, ok := cache.ByRemote[remote]; ok {
		p, err := client.GetProject(ctx, cached.ProjectID)
		if err == nil && p.RemoteURL == remote {
			return &ResolveResult{Project: p, Remote: remote}, nil
		}
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		delete(cache.ByRemote, remote)
		_ = saveProjectsCache(dir, *cache)
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.RemoteURL == remote {
			rememberProject(cache, remote, "", p)
			_ = saveProjectsCache(dir, *cache)
			return &ResolveResult{Project: &p, Remote: remote}, nil
		}
	}
	return &ResolveResult{Remote: remote}, nil
}

func uniqueStrings(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

func isNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Code == "not_found" || apiErr.Status == 404)
}

func findByIDOrSlug(ctx context.Context, client *Client, idOrSlug string) (*Project, error) {
	if looksLikeUUID(idOrSlug) {
		if p, err := client.GetProject(ctx, idOrSlug); err == nil {
			return p, nil
		} else if !isNotFound(err) && !isInvalid(err) {
			return nil, err
		}
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.Slug == idOrSlug || p.ID == idOrSlug {
			return &p, nil
		}
	}
	return nil, nil
}

func isInvalid(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Code == "invalid" || apiErr.Status == 400)
}

// rememberProject caches a project under its remote and, when dir is not
// "", under that directory (only `run` creating a project writes a
// directory: a project named on run, with no remote, has nothing else to be found
// by).
func rememberProject(cache *ProjectsCache, remote, dir string, p Project) {
	if remote != "" {
		cache.ByRemote[remote] = CachedProject{ProjectID: p.ID, Slug: p.Slug, Name: p.Name}
	}
	if dir != "" {
		cache.ByDir[dir] = p.ID
	}
}

// errNoProject is what a command that needs a project reports when res
// found none.
func errNoProject(res *ResolveResult, command string) error {
	if res.Home {
		usage := "`repose <command> PROJECT`"
		if command != "" {
			usage = "`" + command + " PROJECT`"
		}
		return errHomeNoProject(usage)
	}
	return errNoProjectFoundFor(res.Remote, command)
}

// errNoProjectFound is what non-run commands report when resolution finds
// nothing (07-cli.md §5.3 step 4).
func errNoProjectFound(remote string) error { return errNoProjectFoundFor(remote, "") }

// errNoProjectFoundFor names the command the user typed ("repose attach")
// in the hint when it is known.
func errNoProjectFoundFor(remote, command string) error {
	usage := "`repose <command> PROJECT`"
	if command != "" {
		// command ends in " --project" when the command takes PROJECT
		// as that flag only.
		usage = "`" + command + " PROJECT`"
	}
	if remote == "" {
		return exitf(ExitProjectNotFound, "No repose project here, and this directory has no git remote. Name one: %s (`repose ls` lists them).", usage)
	}
	return exitf(ExitProjectNotFound, "No repose project for %s. Run `repose run` here to create one, or name one: %s.", remote, usage)
}
