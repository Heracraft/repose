package config

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// simGuest is a fake runner that keeps the state the commands change: the
// packages dpkg has, the tools mise has, the units that are active. It is
// what lets a test say "the same state" rather than "the same commands".
type simGuest struct {
	mu       sync.Mutex
	pkgs     map[string]bool
	mise     map[string]bool
	active   map[string]bool
	failPkgs map[string]bool
	calls    [][]string
	// fg is every apt-get call that did not ask to run below the agent
	// (RunSpec.Background, DECISIONS I-1560).
	fg [][]string
}

func newSim() *simGuest {
	return &simGuest{pkgs: map[string]bool{}, mise: map[string]bool{}, active: map[string]bool{}, failPkgs: map[string]bool{}}
}

func (g *simGuest) Run(_ context.Context, spec sysdep.RunSpec) (sysdep.RunResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	argv := spec.Argv
	g.calls = append(g.calls, append([]string(nil), argv...))
	if argv[0] == "apt-get" && !spec.Background {
		g.fg = append(g.fg, append([]string(nil), argv...))
	}
	switch argv[0] {
	case "dpkg-query":
		var out strings.Builder
		code := 0
		for _, p := range argv[3:] {
			if g.pkgs[p] {
				out.WriteString(p + " ii \n")
			} else {
				code = 1
			}
		}
		return sysdep.RunResult{ExitCode: code, Stdout: []byte(out.String())}, nil
	case "apt-get":
		verb, pkgs := aptVerb(argv)
		say := func(lines ...string) {
			if spec.OnLine != nil {
				for _, l := range lines {
					spec.OnLine(l)
				}
			}
		}
		switch verb {
		case "update":
			say("Hit:1 http://archive.ubuntu.com/ubuntu noble InRelease", "Reading package lists...")
		case "install":
			say("Reading package lists...", "Building dependency tree...")
			for _, p := range pkgs {
				if g.failPkgs[p] {
					say("E: Unable to locate package " + p)
					return sysdep.RunResult{ExitCode: 100, Stderr: []byte("E: Unable to locate package " + p + "\n")}, nil
				}
			}
			say("0 upgraded, 3 newly installed, 0 to remove and 0 not upgraded.",
				"Need to get 1,526 kB of archives.",
				"Get:1 http://archive.ubuntu.com/ubuntu noble/main amd64 libjemalloc2 amd64 5.3.0-2build1 [256 kB]",
				"Unpacking libjemalloc2:amd64 (5.3.0-2build1) ...",
				"Unpacking "+pkgs[0]+" ...",
				"Setting up libjemalloc2:amd64 (5.3.0-2build1) ...",
				"Setting up "+pkgs[0]+" ...")
			for _, p := range pkgs {
				g.pkgs[p] = true
			}
		case "remove":
			for _, p := range pkgs {
				delete(g.pkgs, p)
			}
		}
	case "mise":
		switch argv[1] {
		case "use":
			g.mise[argv[3]] = true
		case "where":
			if !g.mise[argv[2]] {
				return sysdep.RunResult{ExitCode: 1}, nil
			}
		case "unuse":
			for spec := range g.mise {
				if miseTool(spec) == argv[3] {
					delete(g.mise, spec)
				}
			}
		}
	case "systemctl":
		switch argv[1] {
		case "is-active":
			if !g.active[argv[3]] {
				return sysdep.RunResult{ExitCode: 3}, nil
			}
		case "start", "restart":
			g.active[argv[2]] = true
		case "disable":
			delete(g.active, argv[3])
		}
	}
	return sysdep.RunResult{}, nil
}

func aptVerb(argv []string) (string, []string) {
	for i, a := range argv {
		if a == "install" || a == "remove" || a == "update" {
			return a, argv[i+1:]
		}
	}
	return "", nil
}

func (g *simGuest) reset() {
	g.mu.Lock()
	g.calls = nil
	g.mu.Unlock()
}

func (g *simGuest) ran() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.calls...)
}

// mutating is every call that changes the guest: anything but a check.
func (g *simGuest) mutating() []string {
	var out []string
	for _, c := range g.ran() {
		j := strings.Join(c, " ")
		switch {
		case c[0] == "dpkg-query", c[0] == "id",
			c[0] == "mise" && c[1] == "where",
			c[0] == "systemctl" && c[1] == "is-active",
			c[0] == "sh" && strings.Contains(j, "sha256sum -c --status") && !strings.Contains(j, "curl"):
			continue
		}
		out = append(out, j)
	}
	return out
}

func (g *simGuest) state() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var parts []string
	for _, m := range []map[string]bool{g.pkgs, g.mise, g.active} {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		parts = append(parts, strings.Join(ks, ","))
	}
	return strings.Join(parts, " | ")
}

func quiet() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func erofsRoot(t *testing.T) sysdep.Paths {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir(), Erofs: true}
	for _, d := range []string{p.RWMount(), p.ServicesDir(), p.Home()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func newHandler(t *testing.T, p sysdep.Paths, g *simGuest) *Handler {
	t.Helper()
	h := New(p, g, func(context.Context, int, time.Duration) error { return nil }, quiet())
	if h.recipes == nil {
		t.Fatal("recipes did not load")
	}
	h.SetArch("amd64")
	return h
}

func menuOf(items ...*hostdv1.MenuItem) *hostdv1.MenuSpec { return &hostdv1.MenuSpec{Items: items} }
func id(s string) *hostdv1.MenuItem                       { return &hostdv1.MenuItem{Id: s} }
func pkg(s string) *hostdv1.MenuItem                      { return &hostdv1.MenuItem{Package: s} }

func run(h *Handler, spec *hostdv1.MenuSpec, replay bool) (*guestdv1.ApplyMenuProgress, []string) {
	var lines []string
	res := h.Apply(context.Background(), spec, replay, func(l string) { lines = append(lines, l) })
	return res, lines
}

// treeOf is every file under the test root with its content, so two
// applies can be compared as guest state on disk.
func treeOf(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if fi.IsDir() {
			b.WriteString(rel + "/\n")
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString(rel + "\n" + string(body) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestApplyMenuTwiceIsIdempotent(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)

	res, lines := run(h, menuOf(id("postgresql"), id("bun")), false)
	if res.GetErrorCode() != "" || !res.GetDone() {
		t.Fatalf("first apply: %+v (lines %q)", res, lines)
	}
	for _, want := range []string{"installing postgresql", "postgresql 16 ready on 127.0.0.1:5432", "installing bun", "bun ready", "menu applied"} {
		if !containsLine(lines, want) {
			t.Fatalf("first apply lines %q lack %q", lines, want)
		}
	}
	unit := filepath.Join(p.SystemdUnitDir(), "repose-svc-postgresql.service")
	body, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "listen_addresses=127.0.0.1") || !strings.Contains(string(body), "/var/lib/repose/services/postgresql/16") {
		t.Fatalf("unit:\n%s", body)
	}
	if fi, err := os.Stat(filepath.Join(p.ServicesDir(), "postgresql", "16")); err != nil || !fi.IsDir() {
		t.Fatalf("data dir: %v", err)
	}
	firstState, firstTree := g.state(), treeOf(t, p.Root)
	t.Logf("after the first apply: %s", firstState)

	g.reset()
	res, lines = run(h, menuOf(id("postgresql"), id("bun")), false)
	if res.GetErrorCode() != "" || res.GetLine() != "nothing to do" {
		t.Fatalf("second apply: %+v (lines %q)", res, lines)
	}
	if len(lines) != 1 || lines[0] != "nothing to do" {
		t.Fatalf("second apply lines %q, want only \"nothing to do\"", lines)
	}
	if m := g.mutating(); len(m) != 0 {
		t.Fatalf("second apply changed the guest: %q", m)
	}
	if g.state() != firstState {
		t.Fatalf("state changed: %s -> %s", firstState, g.state())
	}
	if treeOf(t, p.Root) != firstTree {
		t.Fatal("files changed on the second apply")
	}
	t.Logf("second apply: %d checks, 0 changes, line %q", len(g.ran()), res.GetLine())

	// The call hostd makes at a boot, with nothing changed, runs nothing.
	g.reset()
	res, _ = run(h, menuOf(id("postgresql"), id("bun")), true)
	if res.GetLine() != "nothing to do" || len(g.ran()) != 0 {
		t.Fatalf("replay with no base upgrade: %+v, %d commands", res, len(g.ran()))
	}
}

func TestRemovalStopsTheUnitAndKeepsTheData(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)
	if res, lines := run(h, menuOf(id("postgresql"), id("bun")), false); res.GetErrorCode() != "" {
		t.Fatalf("apply: %+v %q", res, lines)
	}
	data := filepath.Join(p.ServicesDir(), "postgresql", "16")
	if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	g.reset()
	res, lines := run(h, menuOf(id("bun")), false)
	if res.GetErrorCode() != "" {
		t.Fatalf("removal: %+v %q", res, lines)
	}
	if !ranCmd(g, "systemctl disable --now repose-svc-postgresql.service") {
		t.Fatalf("the unit was not stopped and disabled: %q", g.mutating())
	}
	if _, err := os.Stat(filepath.Join(p.SystemdUnitDir(), "repose-svc-postgresql.service")); !os.IsNotExist(err) {
		t.Fatalf("unit file still there: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "PG_VERSION")); err != nil || string(b) != "16\n" {
		t.Fatalf("data directory not kept: %v", err)
	}
	if ranCmd(g, "apt-get") {
		t.Fatalf("a service's removal touched packages: %q", g.mutating())
	}
	if !containsLine(lines, "postgresql removed; its data stays in /var/lib/repose/services/postgresql/16") {
		t.Fatalf("lines %q", lines)
	}

	// A runtime's removal undoes its mise tool.
	g.reset()
	if res, _ := run(h, menuOf(), false); res.GetErrorCode() != "" {
		t.Fatalf("empty menu: %+v", res)
	}
	if !ranCmd(g, "mise unuse -g bun") || g.mise["bun@1"] {
		t.Fatalf("bun not removed: %q", g.mutating())
	}
}

func TestAFailingItemDoesNotStopTheOthers(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	g.failPkgs["ffmpeg"] = true
	h := newHandler(t, p, g)

	res, lines := run(h, menuOf(pkg("ffmpeg"), id("postgresql"), id("bun")), false)
	if res.GetErrorCode() != CodeMenuFailed {
		t.Fatalf("error_code = %q, want menu_failed (lines %q)", res.GetErrorCode(), lines)
	}
	if res.GetLine() != "ffmpeg failed: E: Unable to locate package ffmpeg (apt-get exited 100)" {
		t.Fatalf("final line %q", res.GetLine())
	}
	if !g.pkgs["postgresql-16"] || !g.active["repose-svc-postgresql.service"] || !g.mise["bun@1"] {
		t.Fatalf("the items after the failure were not applied: %s", g.state())
	}
	// Retried safely: the same menu once the package installs does only
	// what is missing.
	delete(g.failPkgs, "ffmpeg")
	g.reset()
	res, lines = run(h, menuOf(pkg("ffmpeg"), id("postgresql"), id("bun")), false)
	if res.GetErrorCode() != "" {
		t.Fatalf("retry: %+v %q", res, lines)
	}
	for _, c := range g.mutating() {
		if strings.Contains(c, "postgresql") || strings.Contains(c, "bun") {
			t.Fatalf("retry redid an item that was in place: %q", c)
		}
	}
}

// A free-form name is an apt-get install, which marks it manual; the
// base's dpkg hook writes apt-manual.list from that, and guestd leaves
// the hook's file alone (DECISIONS I-1476).
func TestFreeFormNamesAreAptInstallsAndTheListIsTheHooks(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)
	hook := "curl\nffmpeg\n"
	if err := os.WriteFile(p.AptManual(), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}
	res, lines := run(h, menuOf(pkg("ffmpeg"), pkg("jq")), false)
	if res.GetErrorCode() != "" {
		t.Fatalf("%+v %q", res, lines)
	}
	installs, updates := 0, 0
	for _, c := range g.ran() {
		if c[0] != "apt-get" {
			continue
		}
		switch verb, pkgs := aptVerb(c); verb {
		case "update":
			updates++
		case "install":
			installs++
			if len(pkgs) != 1 || (pkgs[0] != "ffmpeg" && pkgs[0] != "jq") {
				t.Fatalf("install of %q", pkgs)
			}
		}
	}
	if installs != 2 || updates != 1 {
		t.Fatalf("%d installs and %d apt-get updates, want 2 and one per apply", installs, updates)
	}
	if !g.pkgs["ffmpeg"] || !g.pkgs["jq"] {
		t.Fatalf("not installed: %s", g.state())
	}
	if res, _ := run(h, menuOf(pkg("jq")), false); res.GetErrorCode() != "" {
		t.Fatalf("removal: %+v", res)
	}
	if g.pkgs["ffmpeg"] {
		t.Fatal("ffmpeg still installed after it left the menu")
	}
	if b, _ := os.ReadFile(p.AptManual()); string(b) != hook {
		t.Fatalf("guestd wrote the hook's apt-manual.list: %q", b)
	}
}

func TestAnInvalidMenuChangesNothing(t *testing.T) {
	cases := map[string]*hostdv1.MenuSpec{
		"unknown id":        menuOf(id("postgresql"), id("cobol")),
		"unknown option":    menuOf(&hostdv1.MenuItem{Id: "postgresql", Options: map[string]string{"flavour": "x"}}),
		"bad option value":  menuOf(&hostdv1.MenuItem{Id: "postgresql", Options: map[string]string{"version": "16; rm -rf /"}}),
		"both id and pkg":   menuOf(&hostdv1.MenuItem{Id: "bun", Package: "jq"}),
		"neither":           menuOf(&hostdv1.MenuItem{}),
		"bad package name":  menuOf(pkg("jq && reboot")),
		"uppercase package": menuOf(pkg("FFmpeg")),
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			p := erofsRoot(t)
			g := newSim()
			h := newHandler(t, p, g)
			res, _ := run(h, spec, false)
			if res.GetErrorCode() != CodeInvalidArgument || !res.GetDone() {
				t.Fatalf("%+v", res)
			}
			if len(g.ran()) != 0 {
				t.Fatalf("ran %q", g.ran())
			}
		})
	}
}

func TestReplayAfterABaseUpgrade(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)
	if res, _ := run(h, menuOf(id("postgresql"), id("bun"), pkg("ffmpeg")), false); res.GetErrorCode() != "" {
		t.Fatalf("apply: %+v", res)
	}
	// The initramfs rotated the upper directory: every apt package and
	// every file in /etc went, mise's tools in /home/dev stayed, and a
	// package the user installed by hand is in the rotated list.
	for k := range g.pkgs {
		delete(g.pkgs, k)
	}
	for k := range g.active {
		delete(g.active, k)
	}
	if err := os.RemoveAll(p.Etc()); err != nil {
		t.Fatal(err)
	}
	rotated := p.AptManualRotated()
	if err := os.MkdirAll(filepath.Dir(rotated), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rotated, []byte("ripgrep\nnot a package!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.AptManual(), []byte("htop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.BaseUpgraded(), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	g.reset()
	res, lines := run(h, menuOf(id("postgresql"), id("bun"), pkg("ffmpeg")), true)
	if res.GetErrorCode() != "" {
		t.Fatalf("replay: %+v %q", res, lines)
	}
	if !g.pkgs["postgresql-16"] || !g.active["repose-svc-postgresql.service"] || !g.pkgs["ffmpeg"] || !g.pkgs["ripgrep"] || !g.pkgs["htop"] {
		t.Fatalf("replay did not restore: %s", g.state())
	}
	if ranCmd(g, "mise use") {
		t.Fatal("replay reinstalled a mise tool the volume kept")
	}
	if _, err := os.Stat(p.BaseUpgraded()); !os.IsNotExist(err) {
		t.Fatal("the marker survived a successful replay")
	}
	t.Logf("replay lines: %q", lines)

	// A replay that fails keeps the marker, so the next boot tries again;
	// a hand-installed package that no longer installs does not.
	for k := range g.pkgs {
		delete(g.pkgs, k)
	}
	g.failPkgs["ripgrep"] = true
	_ = os.WriteFile(p.BaseUpgraded(), nil, 0o644)
	if res, _ := run(h, menuOf(id("postgresql"), id("bun"), pkg("ffmpeg")), true); res.GetErrorCode() != "" {
		t.Fatalf("a failing hand-installed package failed the replay: %+v", res)
	}
	if _, err := os.Stat(p.BaseUpgraded()); !os.IsNotExist(err) {
		t.Fatal("marker kept for a hand-installed package")
	}
	for k := range g.pkgs {
		delete(g.pkgs, k)
	}
	g.failPkgs["postgresql-16"] = true
	_ = os.WriteFile(p.BaseUpgraded(), nil, 0o644)
	if res, _ := run(h, menuOf(id("postgresql"), id("bun"), pkg("ffmpeg")), true); res.GetErrorCode() != CodeMenuFailed {
		t.Fatalf("failing replay: %+v", res)
	}
	if _, err := os.Stat(p.BaseUpgraded()); err != nil {
		t.Fatal("the marker went after a failed replay")
	}
}

func TestADownloadIsPinnedByItsSHA256(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)
	h.SetArch("arm64")
	if res, lines := run(h, menuOf(id("meilisearch")), false); res.GetErrorCode() != "" {
		t.Fatalf("%+v %q", res, lines)
	}
	var script string
	for _, c := range g.ran() {
		if c[0] == "sh" && strings.Contains(c[2], "curl") {
			script = c[2]
		}
	}
	if !strings.Contains(script, "meilisearch-linux-aarch64") ||
		!strings.Contains(script, "0e670a95eda37bf53afb04d495d8647a1a68708b1688f31ac89cbf5851152779") ||
		strings.Index(script, "sha256sum -c") > strings.Index(script, "install -m 0755") {
		t.Fatalf("download script does not check the arm64 pin before installing:\n%s", script)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func ranCmd(g *simGuest, prefix string) bool {
	for _, c := range g.ran() {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			return true
		}
	}
	return false
}

// A line per item and per step of it, as each happens, and a failure
// that carries apt's own E: line and its exit code (DECISIONS I-1536).
// apt's other output never reaches a line.
func TestApplyMenuSaysEachStepAndAptsError(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	g.failPkgs["ffmpeg-typo"] = true
	h := newHandler(t, p, g)
	res, lines := run(h, menuOf(id("redis"), pkg("ffmpeg-typo")), false)
	want := []string{
		"installing redis",
		"redis: updating package lists",
		"redis: installing 3 packages (1,526 kB)",
		"redis: unpacking",
		"redis: setting up",
		"redis: starting repose-svc-redis.service",
		"redis ready on 127.0.0.1:6379",
		"installing ffmpeg-typo",
		"ffmpeg-typo failed: E: Unable to locate package ffmpeg-typo (apt-get exited 100)",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if res.GetErrorCode() != CodeMenuFailed || res.GetLine() != want[len(want)-1] {
		t.Fatalf("final %+v", res)
	}
	for _, l := range lines {
		if strings.Contains(l, "Get:") || strings.Contains(l, "libjemalloc") || strings.Contains(l, "Reading package") {
			t.Fatalf("apt output reached a line: %q", l)
		}
	}
}

// Every apt-get of an apply runs below the agent (DECISIONS I-1560,
// ENG-186): an install, its package-list update and a removal all ask
// for RunSpec.Background, which the real runner turns into a scope at a
// fifth of the agent's CPU weight, nice 10 and I/O priority 7.
func TestAptRunsBelowTheAgent(t *testing.T) {
	p := erofsRoot(t)
	g := newSim()
	h := newHandler(t, p, g)
	if res, lines := run(h, menuOf(&hostdv1.MenuItem{Id: "redis"}, pkg("jq")), false); res.GetErrorCode() != "" {
		t.Fatalf("%+v %q", res, lines)
	}
	if res, lines := run(h, menuOf(&hostdv1.MenuItem{Id: "redis"}), false); res.GetErrorCode() != "" {
		t.Fatalf("removal: %+v %q", res, lines)
	}
	apt := 0
	for _, c := range g.ran() {
		if c[0] == "apt-get" {
			apt++
		}
	}
	if apt < 3 {
		t.Fatalf("%d apt-get calls; the test needs an update, an install and a remove", apt)
	}
	if len(g.fg) != 0 {
		t.Fatalf("apt-get at the agent's priority: %q", g.fg)
	}
}
