package machine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	remoteA = "git@github.com:me/todo-app.git"
	dirA    = "/home/me/code/todo-app"
)

func TestProjectsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config", "repose")

	p, err := LoadProjects(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ByRemote) != 0 || len(p.ByDir) != 0 {
		t.Fatalf("a missing file loaded %+v", p)
	}

	p.Remember(remoteA, dirA, Project{Machine: "homebox", Slug: "todo-app"})
	p.Remember("", "/home/me/scratch", Project{Machine: "workshop", Slug: "scratch"})
	if err := SaveProjects(dir, p); err != nil {
		t.Fatal(err)
	}

	back, err := LoadProjects(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.ByRemote[remoteA]; got != (Project{Machine: "homebox", Slug: "todo-app"}) {
		t.Errorf("by_remote = %+v", got)
	}
	if got := back.ByDir[dirA]; got != (Project{Machine: "homebox", Slug: "todo-app"}) {
		t.Errorf("by_dir = %+v", got)
	}
	if _, ok := back.ByRemote[""]; ok {
		t.Error("a checkout with no remote was recorded under an empty remote")
	}

	info, err := os.Stat(filepath.Join(dir, ProjectsName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	// The keys are a URL and a path, which TOML needs quoted.
	b, err := os.ReadFile(filepath.Join(dir, ProjectsName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"`+remoteA+`"`) {
		t.Errorf("the remote is not a quoted key:\n%s", b)
	}
}

// Resolve reads the remote first, then the directory: a remote names a
// repository, a directory is only where a copy of it sits.
func TestProjectsResolve(t *testing.T) {
	onHome := Project{Machine: "homebox", Slug: "todo-app"}
	onWorkshop := Project{Machine: "workshop", Slug: "todo-app"}
	p := NewProjects()
	p.ByRemote[remoteA] = onHome
	p.ByDir[dirA] = onWorkshop
	p.ByDir["/home/me/scratch"] = Project{Machine: "workshop", Slug: "scratch"}

	cases := []struct {
		remote, dir string
		want        Project
		ok          bool
	}{
		{remoteA, dirA, onHome, true}, // the remote wins
		{"", dirA, onWorkshop, true},  // no remote: the directory
		{remoteA, "", onHome, true},   // no directory: the remote
		{"", "/home/me/scratch", Project{Machine: "workshop", Slug: "scratch"}, true}, // a checkout with no remote
		{"git@github.com:me/other.git", "/home/me/code/other", Project{}, false},      // neither is recorded
		{"git@github.com:me/other.git", dirA, onWorkshop, true},                       // an unknown remote falls to the directory
		{"", "", Project{}, false},
	}
	for _, c := range cases {
		got, ok := p.Resolve(c.remote, c.dir)
		if ok != c.ok || got != c.want {
			t.Errorf("Resolve(%q, %q) = %+v, %v; want %+v, %v", c.remote, c.dir, got, ok, c.want, c.ok)
		}
	}
}

// UpdateProjects is the read-modify-write two `repose run`s share, so one
// in another checkout does not erase the other's entry.
func TestUpdateProjects(t *testing.T) {
	dir := t.TempDir()
	add := func(remote, d string, pr Project) error {
		return UpdateProjects(dir, func(p *Projects) error {
			p.Remember(remote, d, pr)
			return nil
		})
	}
	if err := add(remoteA, dirA, Project{Machine: "homebox", Slug: "todo-app"}); err != nil {
		t.Fatal(err)
	}
	if err := add("git@github.com:me/api.git", "/home/me/code/api", Project{Machine: "homebox", Slug: "api"}); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProjects(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ByRemote) != 2 || len(p.ByDir) != 2 {
		t.Fatalf("the second update erased the first: %+v", p)
	}

	// A change that fails leaves the file as it was.
	boom := errors.New("no")
	if err := UpdateProjects(dir, func(p *Projects) error {
		p.ByRemote = nil
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("UpdateProjects = %v, want the change's error", err)
	}
	if p, err := LoadProjects(dir); err != nil || len(p.ByRemote) != 2 {
		t.Errorf("a failed update changed the file: %+v, %v", p, err)
	}

	// `machine rm` takes the machine's projects with it.
	if err := UpdateProjects(dir, func(p *Projects) error {
		if !p.ForgetMachine("homebox") {
			t.Error("ForgetMachine found nothing to forget")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadProjects(dir); err != nil || len(p.ByRemote) != 0 || len(p.ByDir) != 0 {
		t.Errorf("entries on a removed machine stayed: %+v, %v", p, err)
	}
}

func TestProjectsRefusesHalfEntries(t *testing.T) {
	dir := t.TempDir()
	cases := []Project{
		{Machine: "", Slug: "todo-app"},
		{Machine: "homebox", Slug: ""},
		{Machine: "Home Box", Slug: "todo-app"},
		{Machine: "homebox", Slug: "Todo App"},
	}
	for _, pr := range cases {
		p := NewProjects()
		p.ByRemote[remoteA] = pr
		if err := SaveProjects(dir, p); err == nil {
			t.Errorf("SaveProjects(%+v): want an error", pr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ProjectsName)); !os.IsNotExist(err) {
		t.Errorf("a refused save wrote the file (%v)", err)
	}
}

// A hand-edited file with an entry no command could use is an error
// naming the file.
func TestLoadProjectsRefusesABadFile(t *testing.T) {
	dir := t.TempDir()
	body := "[by_dir.\"/home/me/code/todo-app\"]\nmachine = \"Home Box\"\nslug = \"todo-app\"\n"
	if err := os.WriteFile(filepath.Join(dir, ProjectsName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjects(dir); err == nil {
		t.Error("LoadProjects over an unusable entry: want an error")
	}
}
