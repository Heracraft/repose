package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/multiplexer"
)

// The pick order of a new project's multiplexer (I-502): the flag, the
// config key, a laptop herdr pane for a project that is not temporary,
// tmux.
func TestPickMultiplexerOrder(t *testing.T) {
	cases := []struct {
		name        string
		flag, cfg   string
		herdrEnv    bool
		temp        bool
		want        string
		wantAutoSet bool
	}{
		{"nothing", "", "", false, false, "tmux", false},
		{"flag wins over config and env", "tmux", "herdr", true, false, "tmux", false},
		{"flag herdr", "herdr", "", false, false, "herdr", false},
		{"config", "", "herdr", false, false, "herdr", false},
		{"config tmux wins over the herdr pane", "", "tmux", true, false, "tmux", false},
		{"herdr pane", "", "", true, false, "herdr", true},
		{"herdr pane, temporary", "", "", true, true, "tmux", false},
		{"config herdr on a temporary machine", "", "herdr", false, true, "herdr", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.herdrEnv {
				t.Setenv("HERDR_ENV", "1")
			} else {
				t.Setenv("HERDR_ENV", "")
			}
			got, auto := pickMultiplexer(c.flag, c.cfg, c.temp)
			if got != c.want || auto != c.wantAutoSet {
				t.Fatalf("pickMultiplexer(%q, %q, temp=%v) = %q, auto=%v; want %q, %v", c.flag, c.cfg, c.temp, got, auto, c.want, c.wantAutoSet)
			}
		})
	}
}

func TestMultiplexerFlagRefusesOtherValues(t *testing.T) {
	for _, v := range []string{"", "tmux", "herdr"} {
		if err := checkMultiplexerFlag(v); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
	}
	err := checkMultiplexerFlag("screen")
	var ue cobraUsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "tmux or herdr") || !strings.Contains(err.Error(), `"screen"`) {
		t.Fatalf("screen: %v", err)
	}
}

func TestConfigDefaultMultiplexer(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(configPath(dir), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("default_agent = \"claude\"\ndefault_multiplexer = \"herdr\"\n")
	cfg, err := loadConfig(dir)
	if err != nil || cfg.DefaultMultiplexer != "herdr" {
		t.Fatalf("herdr: %+v %v", cfg.DefaultMultiplexer, err)
	}
	write("default_multiplexer = \"screen\"\n")
	if _, err := loadConfig(dir); err == nil || !strings.Contains(err.Error(), "default_multiplexer") || !strings.Contains(err.Error(), "tmux or herdr") {
		t.Fatalf("screen: %v", err)
	}
	write("default_agent = \"claude\"\n")
	if cfg, err := loadConfig(dir); err != nil || cfg.DefaultMultiplexer != "" {
		t.Fatalf("absent: %q %v", cfg.DefaultMultiplexer, err)
	}
}

// patchLog wraps the fake api and records the multiplexer of each PATCH
// and POST /projects body.
type patchLog struct {
	mu      sync.Mutex
	patches []string
	posts   []string
}

func (l *patchLog) server(t *testing.T, fake *fakeapi.Fake) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch || (r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/projects")) {
			b, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(b))
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if v, ok := m["multiplexer"].(string); ok {
				l.mu.Lock()
				if r.Method == http.MethodPatch {
					l.patches = append(l.patches, v)
				} else {
					l.posts = append(l.posts, v)
				}
				l.mu.Unlock()
			}
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// muxFixture is a run fixture whose api goes through a patchLog, with
// the guest's multiplexer probe answering tmux.
func muxFixture(t *testing.T) (*runFixture, *fakeapi.Fake, *patchLog) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	log := &patchLog{}
	f.env.Client = newClient(log.server(t, fake)+"/v1", staticToken("tok"))
	old := muxProbe
	muxProbe = func(context.Context, sshTarget) string { return multiplexer.Tmux }
	t.Cleanup(func() { muxProbe = old })
	return f, fake, log
}

// A new project from a herdr pane gets herdr; with the base gate shut
// (herdrMinBase empty, as until the release that sets it) it is created
// with tmux and nothing is said (I-502).
func TestRunNewProjectFromHerdrPane(t *testing.T) {
	f, fake, log := muxFixture(t)
	ctx := context.Background()
	t.Setenv("HERDR_ENV", "1")

	e, out, errOut := freshEnv(f.env, f.local)
	if err := runRun(ctx, e, RunOptions{Name: "gated", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("run with the gate shut: %v (%s)", err, errOut.buf.String())
	}
	p := bySlug(listed(t, e), "gated")
	if p == nil || p.Multiplexer != "tmux" {
		t.Fatalf("gated project %+v, want tmux", p)
	}
	if strings.Contains(out.buf.String()+errOut.buf.String(), "herdr") {
		t.Fatalf("the fallback said something: %q %q", out.buf.String(), errOut.buf.String())
	}
	if len(log.posts) != 1 || log.posts[0] != "herdr" {
		t.Fatalf("posts %v, want one asking for herdr", log.posts)
	}

	fake.SetHerdrMinBase("2026.09.01")
	e2, _, errOut2 := freshEnv(f.env, f.local)
	if err := runRun(ctx, e2, RunOptions{Name: "paned", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("run from a herdr pane: %v", err)
	}
	if p := bySlug(listed(t, e2), "paned"); p == nil || p.Multiplexer != "herdr" {
		t.Fatalf("paned %+v, want herdr", p)
	}
	_ = errOut2

	// A temporary machine from a herdr pane stays on tmux.
	e3, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e3, RunOptions{Temp: tempDefault, NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range listed(t, e3) {
		if p.ExpiresAt != nil && p.Multiplexer != "tmux" {
			t.Fatalf("temporary machine got %q", p.Multiplexer)
		}
	}
}

// An explicit --multiplexer herdr, or config.toml's default_multiplexer,
// refused by the gate prints the api's message and exits 1; tmux is
// never refused.
func TestRunExplicitHerdrRefusedByGate(t *testing.T) {
	f, _, _ := muxFixture(t)
	ctx := context.Background()
	e, _, _ := freshEnv(f.env, f.local)
	err := runRun(ctx, e, RunOptions{Name: "flagged", Multiplexer: "herdr", NoAttach: true, NoSync: true}, false)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitGeneric || !strings.Contains(err.Error(), "herdr is not available yet.") {
		t.Fatalf("flag: %v", err)
	}
	if bySlug(listed(t, e), "flagged") != nil {
		t.Fatal("a project was created")
	}
	e2, _, _ := freshEnv(f.env, f.local)
	e2.Cfg.DefaultMultiplexer = "herdr"
	if err := runRun(ctx, e2, RunOptions{Name: "configured", NoAttach: true, NoSync: true}, false); err == nil || !strings.Contains(err.Error(), "herdr is not available yet.") {
		t.Fatalf("config: %v", err)
	}
	e3, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e3, RunOptions{Name: "plain", Multiplexer: "tmux", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("tmux: %v", err)
	}
}

// --multiplexer on an existing project PATCHes it once, prints the
// switch line for a running machine, and sends nothing for the value
// it already has (I-502).
func TestRunSwitchMultiplexer(t *testing.T) {
	f, fake, log := muxFixture(t)
	fake.SetHerdrMinBase("2026.09.01")
	ctx := context.Background()
	e, _, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e, RunOptions{Name: testSlug, NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}

	e2, out, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e2, RunOptions{Name: testSlug, Multiplexer: "herdr", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if want := testSlug + " uses herdr from its next start; tmux keeps running until then."; !strings.Contains(out.buf.String(), want) {
		t.Fatalf("stdout %q lacks %q", out.buf.String(), want)
	}
	if p := bySlug(listed(t, e2), testSlug); p == nil || p.Multiplexer != "herdr" {
		t.Fatalf("after the switch: %+v", p)
	}
	if len(log.patches) != 1 || log.patches[0] != "herdr" {
		t.Fatalf("patches %v", log.patches)
	}

	e3, out3, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e3, RunOptions{Name: testSlug, Multiplexer: "herdr", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	if len(log.patches) != 1 || strings.Contains(out3.buf.String(), "next start") {
		t.Fatalf("the same value sent %v and said %q", log.patches, out3.buf.String())
	}

	// A stopped machine switches with no line; it starts on the new one.
	p := bySlug(listed(t, e3), testSlug)
	fake.SetState(p.ID, "stopped")
	e4, out4, _ := freshEnv(f.env, f.local)
	if err := runRun(ctx, e4, RunOptions{Name: testSlug, Multiplexer: "tmux", NoAttach: true, NoSync: true}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out4.buf.String(), "next start") || len(log.patches) != 2 || log.patches[1] != "tmux" {
		t.Fatalf("stopped switch said %q, patches %v", out4.buf.String(), log.patches)
	}

	// The gate refuses a project on an older base: exit 1, the api's words.
	fake.SetBaseVersion(p.ID, "2026.08.01")
	e5, _, _ := freshEnv(f.env, f.local)
	err := runRun(ctx, e5, RunOptions{Name: testSlug, Multiplexer: "herdr", NoAttach: true, NoSync: true}, false)
	if err == nil || !strings.Contains(err.Error(), "herdr needs 2026.09.01 or newer") {
		t.Fatalf("gated switch: %v", err)
	}
}

func TestSwitchLine(t *testing.T) {
	if got := switchLine("todo-app", "tmux", "herdr"); got != "todo-app uses tmux from its next start; herdr keeps running until then." {
		t.Fatal(got)
	}
}

// fakeLaptopHerdr puts a herdr on lookHerdr's path that reports version
// and answers `machine list --json` from machines (a JSON array), logging
// every call; listFails makes the list fail and addFails each add.
type fakeLaptopHerdr struct {
	dir string
}

func newFakeLaptopHerdr(t *testing.T, version, machines string) *fakeLaptopHerdr {
	t.Helper()
	dir := t.TempDir()
	script := `#!/usr/bin/env bash
d=$(dirname "$0")
printf '%s\n' "$*" >> "$d/calls.log"
case "$1" in
--version) cat "$d/version" ;;
machine)
  case "$2" in
  list) [ -f "$d/listfail" ] && exit 1; cat "$d/machines.json" ;;
  add) [ -f "$d/addfail" ] && { echo 'Preparing remote...' >&2; echo 'ssh: connect to host gone.repose: refused' >&2; exit 1; }; exit 0 ;;
  remove) exit 0 ;;
  esac ;;
esac
`
	for name, body := range map[string]string{"herdr": script, "version": version + "\n", "machines.json": machines} {
		mode := os.FileMode(0o644)
		if name == "herdr" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	old := lookHerdr
	lookHerdr = func(string) (string, error) { return filepath.Join(dir, "herdr"), nil }
	t.Cleanup(func() { lookHerdr = old })
	home := withHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(includeLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &fakeLaptopHerdr{dir: dir}
}

func (l *fakeLaptopHerdr) calls(t *testing.T) []string {
	b, err := os.ReadFile(filepath.Join(l.dir, "calls.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, c := range nonEmptyLines(string(b)) {
		if c != "--version" {
			out = append(out, c)
		}
	}
	return out
}

func (l *fakeLaptopHerdr) touch(t *testing.T, name string) {
	if err := os.WriteFile(filepath.Join(l.dir, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

const catalogMachines = `[
  {"id":"ssh-1","label":"todo-app","target":"todo-app.repose","session":"default","enabled":true,"selected":false},
  {"id":"ssh-2","label":"old","target":"old.repose","session":"default","enabled":true,"selected":false},
  {"id":"ssh-3","label":"work box","target":"me@work.example.com","session":"default","enabled":true,"selected":true},
  {"id":"ssh-4","label":"paused","target":"paused.repose","session":"default","enabled":false,"selected":false},
  {"id":"ssh-5","label":"gone off","target":"gone-off.repose","session":"default","enabled":false,"selected":false}
]`

func catalogProjects() []Project {
	exp := time.Now().Add(time.Hour)
	return []Project{
		{Slug: "todo-app", State: "running", Multiplexer: "herdr"}, // has an entry
		{Slug: "new-one", State: "running", Multiplexer: "herdr"},  // added
		{Slug: "stopped-one", State: "stopped", Multiplexer: "herdr"},
		{Slug: "tmux-one", State: "running", Multiplexer: "tmux"},
		{Slug: "old-api", State: "running"}, // an api before I-502: tmux
		{Slug: "tmp-k3f9", State: "running", Multiplexer: "herdr", ExpiresAt: &exp},
		{Slug: "paused", State: "running", Multiplexer: "herdr"}, // disabled stays disabled
		{Slug: "leaving", State: "destroying", Multiplexer: "herdr"},
	}
}

func TestPlanHerdrCatalog(t *testing.T) {
	var entries []herdrMachine
	if err := json.Unmarshal([]byte(catalogMachines), &entries); err != nil {
		t.Fatal(err)
	}
	plan := planHerdrCatalog(entries, catalogProjects())
	if strings.Join(plan.Add, ",") != "new-one" {
		t.Fatalf("add %v, want only new-one (not the stopped, tmux, temporary, disabled or destroying ones)", plan.Add)
	}
	if strings.Join(plan.Remove, ",") != "ssh-2,ssh-5" {
		t.Fatalf("remove %v, want the entries of gone slugs only (old, gone-off), never work box", plan.Remove)
	}
}

func TestSyncHerdrMachinesRunsHerdrsCommands(t *testing.T) {
	l := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	var warned []string
	syncHerdrMachines(context.Background(), catalogProjects(), true, func(s string) { warned = append(warned, s) })
	waitHerdrAdds(10 * time.Second)
	got := strings.Join(l.calls(t), "\n")
	want := "machine list --json\nmachine remove ssh-2\nmachine remove ssh-5\nmachine add new-one.repose --label new-one --remote-session default"
	if got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
	if len(warned) != 0 {
		t.Fatalf("warned %v", warned)
	}

	// Without adds (the certificate refresh): removes only.
	l2 := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	syncHerdrMachines(context.Background(), catalogProjects(), false, nil)
	if got := strings.Join(l2.calls(t), "\n"); got != "machine list --json\nmachine remove ssh-2\nmachine remove ssh-5" {
		t.Fatalf("no-add calls: %q", got)
	}
}

func TestSyncHerdrMachinesAddFailsOnce(t *testing.T) {
	l := newFakeLaptopHerdr(t, "herdr 0.9.3", `[]`)
	l.touch(t, "addfail")
	herdrAddFailed = sync.Map{}
	var warned []string
	var mu sync.Mutex
	warn := func(s string) { mu.Lock(); warned = append(warned, s); mu.Unlock() }
	ps := []Project{{Slug: "gone", State: "running", Multiplexer: "herdr"}}
	syncHerdrMachines(context.Background(), ps, true, warn)
	waitHerdrAdds(10 * time.Second)
	syncHerdrMachines(context.Background(), ps, true, warn)
	waitHerdrAdds(10 * time.Second)
	if len(warned) != 1 || warned[0] != "Could not add gone to herdr's sidebar: ssh: connect to host gone.repose: refused" {
		t.Fatalf("warned %q", warned)
	}
}

// A herdr older than 0.9.0, one whose list fails, no Include line, or no
// herdr at all: nothing runs past the version and list, nothing is said.
func TestSyncHerdrMachinesDoesNothing(t *testing.T) {
	old := newFakeLaptopHerdr(t, "herdr 0.8.5", catalogMachines)
	syncHerdrMachines(context.Background(), catalogProjects(), true, func(s string) { t.Errorf("said %q", s) })
	waitHerdrAdds(5 * time.Second)
	if c := old.calls(t); len(c) != 0 {
		t.Fatalf("0.8.5 ran %v", c)
	}

	failing := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	failing.touch(t, "listfail")
	syncHerdrMachines(context.Background(), catalogProjects(), true, func(s string) { t.Errorf("said %q", s) })
	waitHerdrAdds(5 * time.Second)
	if c := failing.calls(t); len(c) != 1 || c[0] != "machine list --json" {
		t.Fatalf("failing list ran %v", c)
	}

	noInclude := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	home, _ := os.UserHomeDir()
	_ = os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host *\n"), 0o600)
	syncHerdrMachines(context.Background(), catalogProjects(), true, nil)
	if c := noInclude.calls(t); len(c) != 0 {
		t.Fatalf("no Include ran %v", c)
	}

	lookHerdr = func(string) (string, error) { return "", os.ErrNotExist }
	syncHerdrMachines(context.Background(), catalogProjects(), true, nil) // must not panic
}

// `repose rm` removes the entry repose owns for that slug, and only it.
func TestForgetHerdrMachine(t *testing.T) {
	l := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	forgetHerdrMachine(context.Background(), "todo-app")
	if got := strings.Join(l.calls(t), "\n"); got != "machine list --json\nmachine remove ssh-1" {
		t.Fatalf("calls %q", got)
	}
}

func TestParseHerdrVersion(t *testing.T) {
	for in, want := range map[string][3]int{"herdr 0.9.3": {0, 9, 3}, "0.9.0\n": {0, 9, 0}, "herdr v1.2.3-preview.4": {1, 2, 3}, "herdr 0.10": {0, 10, 0}} {
		got, ok := parseHerdrVersion(in)
		if !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	if _, ok := parseHerdrVersion("herdr"); ok {
		t.Error("no version parsed")
	}
	if !versionLess([3]int{0, 8, 9}, laptopHerdrMin) || versionLess([3]int{0, 9, 0}, laptopHerdrMin) {
		t.Error("versionLess")
	}
}

// The attach rule's choice (features/run-and-attach.md "herdr
// projects"): the pane path needs HERDR_ENV, a project that is not
// temporary, a laptop herdr of 0.9.0 or newer and an enabled entry; the
// child client needs that herdr and the plain alias; else ssh.
func TestHerdrAttachPath(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	normal := &Project{Slug: "todo-app"}
	temp := &Project{Slug: "tmp-k3f9", ExpiresAt: &exp}
	alias := hostTarget("todo-app")
	tempAlias := hostTarget("tmp-k3f9")
	minusF := sshTarget{Args: []string{"-F", "/x/config", "todo-app.repose"}}
	cases := []struct {
		name    string
		version string // "" is no herdr on the laptop
		env     bool
		p       *Project
		t       sshTarget
		entry   bool
		want    herdrPath
	}{
		{"pane, sidebar", "herdr 0.9.3", true, normal, alias, true, herdrPathSidebar},
		{"pane, entry disabled", "herdr 0.9.3", true, normal, alias, false, herdrPathRemote},
		{"pane, temporary", "herdr 0.9.3", true, temp, tempAlias, true, herdrPathRemote},
		{"no pane, laptop herdr", "herdr 0.9.0", false, normal, alias, true, herdrPathRemote},
		{"old laptop herdr", "herdr 0.8.5", true, normal, alias, true, herdrPathSSH},
		{"no laptop herdr", "", true, normal, alias, true, herdrPathSSH},
		{"alias needs -F", "herdr 0.9.3", false, normal, minusF, true, herdrPathSSH},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env {
				t.Setenv("HERDR_ENV", "1")
			} else {
				t.Setenv("HERDR_ENV", "")
			}
			var lh *laptopHerdrCLI
			if c.version != "" {
				newFakeLaptopHerdr(t, c.version, "[]")
				lh = laptopHerdr()
			}
			got := chooseHerdrPath(lh, c.p, c.t, func() bool { return c.entry })
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// ensureEntry adds a missing entry, keeps a disabled one disabled, and
// accepts an enabled one as it is.
func TestEnsureEntry(t *testing.T) {
	l := newFakeLaptopHerdr(t, "herdr 0.9.3", catalogMachines)
	lh := laptopHerdr()
	e := &Env{ErrOut: &discardWriter{}, Out: &discardWriter{}}
	if !lh.ensureEntry(e, "todo-app") {
		t.Fatal("todo-app has an enabled entry")
	}
	if lh.ensureEntry(e, "paused") {
		t.Fatal("a disabled entry was taken as the sidebar")
	}
	if !lh.ensureEntry(e, "fresh") {
		t.Fatal("fresh was not added")
	}
	calls := strings.Join(l.calls(t), "\n")
	if strings.Count(calls, "machine add") != 1 || !strings.Contains(calls, "machine add fresh.repose --label fresh --remote-session default") || strings.Contains(calls, "enable") {
		t.Fatalf("calls %q", calls)
	}
}

func TestHerdrStatePick(t *testing.T) {
	st := herdrState{
		Label: "todo-app",
		Agents: []herdrAgent{
			{Name: "claude", Agent: "claude", WorkspaceID: "w2"},
			{Name: "claude-2", Agent: "claude", WorkspaceID: "w1"},
			{Agent: "codex", WorkspaceID: "w1"},
		},
		Workspaces: []herdrWorkspace{{ID: "w1", Label: "todo-app"}, {ID: "w2", Label: "other"}},
	}
	if n, others := st.pick("claude"); n != "claude-3" || !others {
		t.Fatalf("claude: %q %v", n, others)
	}
	if n, others := st.pick("gemini"); n != "gemini" || others {
		t.Fatalf("gemini: %q %v", n, others)
	}
	st.Label = "other"
	if _, others := st.pick("codex"); others {
		t.Fatal("codex is in todo-app, not other")
	}
}

// ps on herdr reads only ids, labels, names, kinds, states and focus:
// fields such as cwd in herdr's answer never reach the rows.
func TestParseHerdrStateDropsCwd(t *testing.T) {
	out := `#label todo-app
#agents {"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_status":"working","cwd":"/home/dev/secret-dir","foreground_cwd":"/home/dev/secret-dir","focused":true,"name":"claude","pane_id":"w1:p2","state_change_seq":3,"tab_id":"w1:t2","terminal_id":"term_x","workspace_id":"w1"}],"type":"agent_list"}}
#workspaces {"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"active_tab_id":"w1:t1","focused":true,"label":"todo-app","workspace_id":"w1","worktree":{"checkout_path":"/home/dev/secret-dir"}}]}}
`
	st, err := parseHerdrState(out)
	if err != nil {
		t.Fatal(err)
	}
	rows := herdrPsRows(st)
	b, _ := json.Marshal(rows)
	if string(b) != `[{"workspace":"todo-app","agent":"claude","name":"claude","state":"working","focused":true}]` {
		t.Fatalf("rows %s", b)
	}
	if _, err := parseHerdrState("#label x\n#agents {\"error\":{\"code\":\"server_unavailable\"}}\n#workspaces {}"); err == nil {
		t.Fatal("an error answer parsed")
	}
}

func TestCreatedLabelNamesHerdr(t *testing.T) {
	if got := createdLabel(&Project{Slug: "todo-app", Multiplexer: "herdr"}, "large"); got != "Created todo-app (large, herdr)" {
		t.Fatal(got)
	}
	if got := createdLabel(&Project{Slug: "todo-app", Multiplexer: "tmux"}, "large"); got != "Created todo-app (large)" {
		t.Fatal(got)
	}
}

func TestStatusNamesHerdr(t *testing.T) {
	p := &Project{Slug: "todo-app", Class: "large", State: "running", Signals: &Signals{SSHSessions: 1, TmuxClients: 0}}
	var b strings.Builder
	writeStatusLinesMux(&b, p, nil, nil, nil, multiplexer.Herdr)
	first, rest, _ := strings.Cut(b.String(), "\n")
	if !strings.HasPrefix(first, "todo-app   large  herdr  running") {
		t.Fatalf("first line %q", first)
	}
	if !strings.Contains(rest, "  sessions 1   docker 0\n") || strings.Contains(rest, "tmux clients") {
		t.Fatalf("sessions line %q", rest)
	}
	b.Reset()
	writeStatusLines(&b, p, nil, nil, nil)
	if strings.Contains(b.String(), "herdr") || !strings.Contains(b.String(), "tmux clients 0") {
		t.Fatalf("tmux status %q", b.String())
	}
}

func TestGuestMessageScriptBothMultiplexers(t *testing.T) {
	got := guestMessageScript("todo-app", "50% #1 done")
	for _, want := range []string{
		`if tmux list-sessions >/dev/null 2>&1; then if tmux list-clients -t '=todo-app' -F x`,
		`tmux display-message -d 4000 -t '=todo-app:' '50% ##1 done'`,
		`elif [ -S /home/dev/.config/herdr/herdr.sock ]; then herdr notification show repose --body '50% #1 done'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s\nlacks %s", got, want)
		}
	}
}

func TestBaseCommandsHasHerdr(t *testing.T) {
	if !baseCommands["herdr"] {
		t.Fatal("herdr is in every base (I-501)")
	}
}
