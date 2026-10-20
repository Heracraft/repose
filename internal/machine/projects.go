package machine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ProjectsName is the project map, beside the registry in the CLI's
// config directory (internal/cli configDir: ~/.config/repose).
const ProjectsName = "projects.toml"

// Project is where a checkout's work happens: the machine in the registry
// and the project's slug on it.
type Project struct {
	Machine string
	Slug    string
}

// Projects is projects.toml: a checkout's git remote, or its directory,
// to the machine and the slug it has there. It is what projects.json is
// for the hosted path (internal/cli/config.go), without the api — there
// is no id to exchange for a project, so the machine and the slug are the
// whole record.
type Projects struct {
	ByRemote map[string]Project
	ByDir    map[string]Project
}

// NewProjects is an empty map, ready to write to.
func NewProjects() Projects {
	return Projects{ByRemote: map[string]Project{}, ByDir: map[string]Project{}}
}

// Resolve is where the checkout in dir belongs: by its git remote, then
// by the directory. internal/cli/project.go's resolveProject reads the
// same two places and asks the api whether the entry still points at a
// live project; there is no api here, so the remote is read first — it
// names a repository, while a directory is only where a copy of it sits.
//
// remote is "" for a checkout with no remote; dir is the repository root
// when the working directory is inside one (internal/cli dirKey), else
// the working directory.
func (p Projects) Resolve(remote, dir string) (Project, bool) {
	if remote != "" {
		if pr, ok := p.ByRemote[remote]; ok {
			return pr, true
		}
	}
	if dir != "" {
		if pr, ok := p.ByDir[dir]; ok {
			return pr, true
		}
	}
	return Project{}, false
}

// Remember records pr under remote and under dir, skipping either when it
// is "": a checkout with no remote has only its directory to be found by.
func (p *Projects) Remember(remote, dir string, pr Project) {
	if p.ByRemote == nil {
		p.ByRemote = map[string]Project{}
	}
	if p.ByDir == nil {
		p.ByDir = map[string]Project{}
	}
	if remote != "" {
		p.ByRemote[remote] = pr
	}
	if dir != "" {
		p.ByDir[dir] = pr
	}
}

// ForgetMachine drops every entry on the machine called name, which is
// what `repose machine rm` leaves behind. It reports whether anything
// went.
func (p *Projects) ForgetMachine(name string) bool {
	gone := false
	for _, m := range []map[string]Project{p.ByRemote, p.ByDir} {
		for k, pr := range m {
			if pr.Machine == name {
				delete(m, k)
				gone = true
			}
		}
	}
	return gone
}

// LoadProjects reads projects.toml from dir. A missing file is an empty
// map.
func LoadProjects(dir string) (Projects, error) {
	b, err := os.ReadFile(projectsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return NewProjects(), nil
		}
		return NewProjects(), err
	}
	var f projectsFile
	if _, err := toml.Decode(string(b), &f); err != nil {
		return NewProjects(), fmt.Errorf("%s: %w", projectsPath(dir), err)
	}
	p := NewProjects()
	for k, e := range f.ByRemote {
		p.ByRemote[k] = Project(e)
	}
	for k, e := range f.ByDir {
		p.ByDir[k] = Project(e)
	}
	if err := p.check(); err != nil {
		return NewProjects(), fmt.Errorf("%s: %w", projectsPath(dir), err)
	}
	return p, nil
}

// SaveProjects replaces projects.toml with p.
func SaveProjects(dir string, p Projects) error {
	unlock, err := lockProjects(dir)
	if err != nil {
		return err
	}
	defer unlock()
	return saveProjects(dir, p)
}

// UpdateProjects applies change to projects.toml under the lock, so two
// `repose run`s in different checkouts do not erase each other's entry
// (projects.json merges for the same reason). change is not called when
// the file cannot be read.
func UpdateProjects(dir string, change func(*Projects) error) error {
	unlock, err := lockProjects(dir)
	if err != nil {
		return err
	}
	defer unlock()
	p, err := LoadProjects(dir)
	if err != nil {
		return err
	}
	if err := change(&p); err != nil {
		return err
	}
	return saveProjects(dir, p)
}

// projectsFile is projects.toml: [by_remote."URL"] and [by_dir."/path"]
// tables, the keys the CLI resolves a checkout by.
type projectsFile struct {
	ByRemote map[string]projectEntry `toml:"by_remote"`
	ByDir    map[string]projectEntry `toml:"by_dir"`
}

// projectEntry is one [by_remote."..."] or [by_dir."..."] table.
type projectEntry struct {
	Machine string `toml:"machine"`
	Slug    string `toml:"slug"`
}

func projectsPath(dir string) string { return filepath.Join(dir, ProjectsName) }

// saveProjects writes the file. The caller holds the lock.
func saveProjects(dir string, p Projects) error {
	if err := p.check(); err != nil {
		return err
	}
	var f projectsFile
	if len(p.ByRemote) > 0 {
		f.ByRemote = make(map[string]projectEntry, len(p.ByRemote))
		for k, pr := range p.ByRemote {
			f.ByRemote[k] = projectEntry(pr)
		}
	}
	if len(p.ByDir) > 0 {
		f.ByDir = make(map[string]projectEntry, len(p.ByDir))
		for k, pr := range p.ByDir {
			f.ByDir[k] = projectEntry(pr)
		}
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return err
	}
	return writeFileAtomic(projectsPath(dir), buf.Bytes(), 0o600)
}

// check refuses an entry that does not name both a machine and a slug:
// the file is read back to build an ssh alias and a path on the box, and
// neither survives a half-written entry.
func (p Projects) check() error {
	for _, m := range []map[string]Project{p.ByRemote, p.ByDir} {
		for k, pr := range m {
			if k == "" {
				return errors.New("a project entry needs a remote or a directory")
			}
			if pr.Machine == "" || Slug(pr.Machine) != pr.Machine {
				return fmt.Errorf("%q does not name a machine", pr.Machine)
			}
			if pr.Slug == "" || Slug(pr.Slug) != pr.Slug {
				return fmt.Errorf("%q is not a project slug", pr.Slug)
			}
		}
	}
	return nil
}

// lockProjects serialises a read-modify-write against another repose
// process.
func lockProjects(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(projectsPath(dir) + ".lock")
}
