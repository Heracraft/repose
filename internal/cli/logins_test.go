package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// I-422: `repose secrets choose` edits only its own table in config.toml; the
// rest of the file, comments and other tables included, stays byte for
// byte.
func TestReplaceTOMLTable(t *testing.T) {
	const other = "# my settings\ndefault_class = \"small\"\n\n[sync]\nexclude = [\"dist\"] # big\n"
	for _, tc := range []struct {
		name, src string
		path      []string
		body      []string
		remove    bool
		want      string
	}{
		{"empty file", "", []string{"logins"}, []string{`skip = ["gh"]`}, false,
			"[logins]\nskip = [\"gh\"]\n"},
		{"appended after the rest", other, []string{"logins"}, []string{`skip = ["gh"]`}, false,
			other + "\n[logins]\nskip = [\"gh\"]\n"},
		{"replaced in place, comment kept", "[logins]\n# no gh here\nskip = [\n  \"gh\",\n]\n\n[sync]\nexclude = []\n", []string{"logins"}, []string{`skip = ["env"]`}, false,
			"[logins]\n# no gh here\nskip = [\"env\"]\n\n[sync]\nexclude = []\n"},
		{"quoted project header found", "[projects.\"todo-app\".logins]\nskip = []\n", []string{"projects", "todo-app", "logins"}, []string{`skip = ["codex"]`}, false,
			"[projects.\"todo-app\".logins]\nskip = [\"codex\"]\n"},
		{"project header written bare", other, []string{"projects", "todo-app", "logins"}, []string{`skip = []`}, false,
			other + "\n[projects.todo-app.logins]\nskip = []\n"},
		{"removed, neighbours kept", "a = 1\n\n[logins]\nskip = [\"gh\"]\n\n[sync]\nexclude = []\n", []string{"logins"}, nil, true,
			"a = 1\n\n[sync]\nexclude = []\n"},
		{"remove with no table", other, []string{"logins"}, nil, true, other},
		{"another project untouched", "[projects.a.logins]\nskip = [\"gh\"]\n", []string{"projects", "b", "logins"}, []string{`skip = []`}, false,
			"[projects.a.logins]\nskip = [\"gh\"]\n\n[projects.b.logins]\nskip = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replaceTOMLTable(tc.src, tc.path, tc.body, tc.remove); got != tc.want {
				t.Fatalf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestLoginSkipPrecedence(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) Config {
		t.Helper()
		if err := os.WriteFile(configPath(dir), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := loadConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := write("")
	if skip, chosen, _ := c.loginSkip("todo"); chosen || len(skip) != 0 {
		t.Fatalf("no table: skip %v chosen %v", skip, chosen)
	}
	c = write("[logins]\nskip = [\"gh\", \"vercel\"]\n\n[projects.todo.logins]\nskip = []\n\n[projects.api.logins]\nskip = [\"env\"]\n")
	if skip, chosen, unknown := c.loginSkip("other"); !chosen || !skip["gh"] || len(skip) != 1 || strings.Join(unknown, ",") != "vercel" {
		t.Fatalf("global: skip %v chosen %v unknown %v", skip, chosen, unknown)
	}
	if skip, chosen, _ := c.loginSkip("todo"); !chosen || len(skip) != 0 {
		t.Fatalf("todo's empty list must override the global one: %v", skip)
	}
	if skip, _, _ := c.loginSkip("api"); !skip["env"] || skip["gh"] {
		t.Fatalf("api's list replaces the global one: %v", skip)
	}
}

func TestLoginsOnOffWritesConfig(t *testing.T) {
	t.Setenv(envProject, "")
	dir := t.TempDir()
	const before = "# keep me\ndefault_agent = \"codex\"\n"
	if err := os.WriteFile(configPath(dir), []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e := &Env{Dir: dir, Out: &out, ErrOut: &out, Cwd: t.TempDir(), HomeDir: t.TempDir()}
	reload := func() {
		t.Helper()
		c, err := loadConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		e.Cfg = c
	}
	reload()
	if err := SecretsChooseCmd(context.Background(), e, "", false, true, false, []string{"env", "gh"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(configPath(dir))
	if want := before + "\n[logins]\nskip = [\"gh\", \"env\"]\n"; string(b) != want {
		t.Fatalf("config.toml =\n%s\nwant\n%s", b, want)
	}
	if info, _ := os.Stat(configPath(dir)); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want the file's own 0640", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "gh, env stay on your laptop") || !strings.Contains(out.String(), "gh auth login") {
		t.Fatalf("output = %q", out.String())
	}
	reload()
	if err := SecretsChooseCmd(context.Background(), e, "", true, false, false, []string{"gh"}); err != nil {
		t.Fatal(err)
	}
	reload()
	if skip, _, _ := e.Cfg.loginSkip(""); !skip["env"] || skip["gh"] {
		t.Fatalf("after on gh: %v", skip)
	}
	if err := SecretsChooseCmd(context.Background(), e, "", false, true, false, []string{"vercel"}); exitCodeFor(err, &out) != ExitUsage {
		t.Fatalf("unknown name: %v", err)
	}
	for _, bad := range []struct {
		on, off, reset bool
		names          []string
	}{{true, true, false, []string{"gh"}}, {false, true, false, nil}, {false, false, false, []string{"gh"}}, {false, true, true, []string{"gh"}}} {
		if err := SecretsChooseCmd(context.Background(), e, "", bad.on, bad.off, bad.reset, bad.names); exitCodeFor(err, &out) != ExitUsage {
			t.Fatalf("%+v: want a usage error, got %v", bad, err)
		}
	}
	// Not a terminal and no flags: the list, with the change hint.
	out.Reset()
	if err := SecretsChooseCmd(context.Background(), e, "", false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  off  env ") || !strings.Contains(out.String(), "  on   gh ") {
		t.Fatalf("list = %q", out.String())
	}
	if err := SecretsChooseCmd(context.Background(), e, "", false, false, true, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(configPath(dir)); string(b) != before {
		t.Fatalf("after reset =\n%q\nwant\n%q", b, before)
	}
}

// A dotfiles setup keeps config.toml as a symlink: the target is written
// and the link stays a link.
func TestLoginsWritesThroughASymlink(t *testing.T) {
	dir, dots := t.TempDir(), t.TempDir()
	target := filepath.Join(dots, "repose.toml")
	if err := os.WriteFile(target, []byte("default_class = \"small\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := writeLoginsTable(dir, loginsScope{}, []string{"codex"}, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(configPath(dir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "skip = [\"codex\"]") {
		t.Fatalf("target = %q", b)
	}
}

// A list set some other way (a dotted key at the top) is not edited: the
// file is left as it was.
func TestLoginsRefusesAListItDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	const src = "logins.skip = [\"gh\"]\n"
	if err := os.WriteFile(configPath(dir), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeLoginsTable(dir, loginsScope{}, []string{"env"}, false); err == nil {
		t.Fatal("want a refusal")
	}
	if b, _ := os.ReadFile(configPath(dir)); string(b) != src {
		t.Fatalf("file changed: %q", b)
	}
}

func TestPickLogins(t *testing.T) {
	items := loginItems()
	var screen bytes.Buffer
	// Down twice with j and the arrow, toggle opencode off; up, toggle
	// codex off; Enter.
	on, err := pickLogins(strings.NewReader("j\x1b[B \x1b[A \r"), &screen, "h", items, map[string]bool{"env": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"gh": true, "codex": false, "opencode": false, "env": false}
	for n, v := range want {
		if on[n] != v {
			t.Fatalf("on = %v, want %v", on, want)
		}
	}
	if _, err := pickLogins(strings.NewReader(" q"), &screen, "h", items, nil, nil); err != errPickCancelled {
		t.Fatalf("q: %v", err)
	}
}

func TestLoginsLine(t *testing.T) {
	if l := loginsLine([]string{"git"}, nil, false); l != "" {
		t.Fatalf("git alone is named on every run, so no hint: %q", l)
	}
	if l := loginsLine([]string{"gh", "git"}, nil, false); l != "" {
		t.Fatalf("before any choice, nothing (I-484): %q", l)
	}
	if l := loginsLine([]string{"codex"}, map[string]bool{"gh": true, "env": true}, true); l != "Left on your laptop: env, gh." {
		t.Fatalf("line = %q", l)
	}
	if l := loginsLine([]string{"codex"}, map[string]bool{}, true); l != "" {
		t.Fatalf("nothing skipped: %q", l)
	}
}

// I-422 on the guest: a skipped login is not sent and no git helper is set
// up for it; a copy an earlier run left is removed once, with a notice; a
// login made on the machine stays; the other logins still travel.
func TestSyncCredentialsSkipRemovesTheCopy(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()
	hosts := "github.com:\n    oauth_token: NEVER-GH-TOKEN\n    user: dev\n"
	writeFile := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(filepath.Join(home, ".config", "gh", "hosts.yml"), hosts)
	writeFile(filepath.Join(home, ".codex", "auth.json"), `{"k":"codex"}`)
	g := filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")
	sync := func(skip map[string]bool) ([]string, *carryOutcome) {
		t.Helper()
		copied, o, err := syncCredentialsAndCarry(context.Background(), f.target, home, f.local, credSyncOptions{Skip: skip}, carryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return copied, o
	}

	// Copied while on.
	if copied, _ := sync(nil); strings.Join(copied, ",") != "gh,codex,git" || !fileExists(g) {
		t.Fatalf("copied = %v", copied)
	}
	// Off: the copy is the laptop's, so it goes, said once.
	var stream strings.Builder
	observePayload = func(script string, tarball []byte) { stream.WriteString(script); stream.Write(tarball) }
	t.Cleanup(func() { observePayload = nil })
	off := map[string]bool{"gh": true}
	copied, o := sync(off)
	if fileExists(g) {
		t.Fatal("the gh copy is still on the machine")
	}
	if strings.Join(copied, ",") != "codex,git" || len(o.Warnings) != 1 || o.Warnings[0] != skippedCredNotice("gh") {
		t.Fatalf("copied = %v warnings = %q", copied, o.Warnings)
	}
	if strings.Contains(stream.String(), "NEVER-GH-TOKEN") {
		t.Fatal("the skipped token is in the stream")
	}
	if _, o = sync(off); len(o.Warnings) != 0 {
		t.Fatalf("second run warnings = %q", o.Warnings)
	}
	// A login made on the machine stays.
	writeFile(g, "github.com:\n    oauth_token: made-on-the-machine\n")
	writeFile(filepath.Join(home, ".codex", "auth.json"), `{"k":"codex2"}`) // something changes, so the part goes
	if _, o = sync(off); len(o.Warnings) != 0 || !fileExists(g) {
		t.Fatalf("a login made on the machine was touched: %q", o.Warnings)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".codex", "auth.json")); string(b) != `{"k":"codex2"}` {
		t.Fatalf("codex = %q", b)
	}
}

// I-422 for .env files: with env off nothing is written, a copy an earlier
// carry wrote is removed while it is the laptop's, an edited one is left
// and named, and turning env back on sends the set again.
func TestSyncEnvOffRemovesCopies(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, ".gitignore"), []byte(".env*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "add", ".gitignore")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "ignore env")
	for n, v := range map[string]string{".env": "A=1\n", ".env.local": "B=2\n"} {
		if err := os.WriteFile(filepath.Join(f.local, n), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sync := func(off bool) *SyncSummary {
		t.Helper()
		s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
			EnvOff: off,
			EnvLater: func() []envFile {
				envs, err := buildEnvCarry(f.local)
				if err != nil {
					t.Fatal(err)
				}
				return envs
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := sync(false); s.EnvFiles != 2 {
		t.Fatalf("env files = %d", s.EnvFiles)
	}
	// The agent edits one on the machine.
	edited := filepath.Join(f.guestRepo(), ".env.local")
	if err := os.WriteFile(edited, []byte("B=agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Newer than the laptop's by more than the second mtime compares at.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(edited, later, later); err != nil {
		t.Fatal(err)
	}
	s := sync(true)
	if s.EnvRemoved != 1 || strings.Join(s.EnvLeft, ",") != ".env.local" || s.EnvFiles != 0 {
		t.Fatalf("removed %d left %v files %d", s.EnvRemoved, s.EnvLeft, s.EnvFiles)
	}
	if fileExists(filepath.Join(f.guestRepo(), ".env")) {
		t.Fatal(".env is still on the machine")
	}
	if b, _ := os.ReadFile(edited); string(b) != "B=agent\n" {
		t.Fatalf("the edited file was touched: %q", b)
	}
	if w := strings.Join(s.Warnings(), "\n"); !strings.Contains(w, "Removed the .env file") || !strings.Contains(w, "Left .env.local") {
		t.Fatalf("warnings = %s", w)
	}
	// The carry is forgotten: the next run has nothing to remove.
	if s := sync(true); s.EnvRemoved != 0 || len(s.EnvLeft) != 0 {
		t.Fatalf("second run: removed %d left %v", s.EnvRemoved, s.EnvLeft)
	}
	// On again: the set goes again (the edited copy is newer, so kept).
	if s := sync(false); s.EnvFiles != 1 {
		t.Fatalf("back on: env files = %d kept %v", s.EnvFiles, s.EnvKept)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), ".env")); string(b) != "A=1\n" {
		t.Fatalf(".env = %q", b)
	}
}

// `repose run` on a machine that already has its checkout leaves the
// checkout alone (I-367) but still removes the .env copies once env is
// off, so "the next repose run" holds (I-422).
func TestRunEnvOffRemovesCopiesWithoutSyncing(t *testing.T) {
	f := newSyncFixture(t)
	if err := os.WriteFile(filepath.Join(f.local, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "git", "add", ".gitignore")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "ignore env")
	if err := os.WriteFile(filepath.Join(f.local, ".env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	sync := func(firstOnly, off bool) *SyncSummary {
		t.Helper()
		s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
			FirstOnly: firstOnly,
			EnvOff:    off,
			EnvLater: func() []envFile {
				envs, err := buildEnvCarry(f.local)
				if err != nil {
					t.Fatal(err)
				}
				return envs
			},
			Carry: func(markers map[string]string) (*credCarry, error) {
				return buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers})
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := sync(false, false); s.EnvFiles != 1 {
		t.Fatalf("env files = %d", s.EnvFiles)
	}
	s := sync(true, true)
	if !s.Skipped || s.EnvRemoved != 1 {
		t.Fatalf("skipped %v removed %d", s.Skipped, s.EnvRemoved)
	}
	if fileExists(filepath.Join(f.guestRepo(), ".env")) {
		t.Fatal(".env is still on the machine")
	}
	if s := sync(true, true); s.EnvRemoved != 0 {
		t.Fatalf("second run removed %d", s.EnvRemoved)
	}
}

// A gh login copied from the laptop sets gh as the helper for github.com
// and gist.github.com and the two SSH-URL rewrites, each once however
// often it runs (I-247, I-526).
func TestSyncCredentialsGhHelpers(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(hosts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hosts, []byte("github.com:\n    oauth_token: NEVER-GH-TOKEN\n    user: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	guestGit := func(args ...string) []string {
		t.Helper()
		out, _ := exec.Command("git", append([]string{"config", "--file", filepath.Join(f.guestHome, ".gitconfig")}, args...)...).Output()
		return strings.Fields(strings.TrimSpace(string(out)))
	}
	for run := 0; run < 2; run++ {
		// No markers passed, so the second run sends the lines again.
		if _, _, err := syncCredentialsAndCarry(context.Background(), f.target, home, f.local, credSyncOptions{}, carryOptions{}); err != nil {
			t.Fatal(err)
		}
		for _, h := range []string{"https://github.com", "https://gist.github.com"} {
			got := strings.Join(guestGit("--get-all", "credential."+h+".helper"), " ")
			if got != "!gh auth git-credential" {
				t.Errorf("run %d: credential.%s.helper = %q", run+1, h, got)
			}
		}
		if got := guestGit("--get-all", "url.https://github.com/.insteadOf"); strings.Join(got, " ") != "git@github.com: ssh://git@github.com/" {
			t.Errorf("run %d: insteadOf = %q", run+1, got)
		}
	}
}

// A row's note is its one parenthetical: the mcp row read "MCP servers
// (tokens stay on the laptop) (none on this laptop)" (I-633).
func TestLoginRowsHaveOneParenthetical(t *testing.T) {
	for _, it := range loginItems() {
		if strings.ContainsAny(it.What, "()") {
			t.Errorf("%s: %q has its own parentheses, and the found note adds one", it.Name, it.What)
		}
	}
}
