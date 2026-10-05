package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// dashboardSave stands for a save on the dashboard: a PUT that names no
// base, from source dashboard.
func dashboardSave(t *testing.T, fake *fakeapi.Fake, text string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"fragment": text, "source": "dashboard"})
	req, _ := http.NewRequest(http.MethodPut, fake.URL()+"/v1/me/config", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("dashboard save: %v %v", res, err)
	}
	_ = res.Body.Close()
}

// The run half of machine.nix (DECISIONS I-490): push a changed laptop
// copy, nothing when nothing changed, take a dashboard save into an
// unchanged copy, refuse when both changed, and apply wins over the
// account's copy.
func TestPersonalSyncRules(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	e.HomeDir = filepath.Dir(e.Dir)
	ctx := context.Background()
	if lines, has := e.personalSync(ctx); len(lines) != 0 || has {
		t.Fatalf("nothing anywhere: %q %v", lines, has)
	}
	v1 := "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }\n"
	if err := os.WriteFile(e.machineNixPath(), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, has := e.personalSync(ctx)
	if len(lines) != 1 || lines[0] != "Applying machine.nix (changed) to todo-app in the background." || !has {
		t.Fatalf("first push: %q %v", lines, has)
	}
	acct, _ := e.Client.GetPersonal(ctx)
	if acct.Fragment != v1 {
		t.Fatalf("account = %q", acct.Fragment)
	}
	if cfg, _ := e.Client.GetConfig(ctx, p.ID); cfg.Personal != v1 {
		t.Fatalf("project revision personal = %q", cfg.Personal)
	}
	if lines, has := e.personalSync(ctx); len(lines) != 0 || !has {
		t.Fatalf("unchanged: %q", lines)
	}

	// Saved on the dashboard, laptop copy untouched: the laptop takes it.
	v2 := "{ pkgs, ... }: { home.packages = [ pkgs.fd ]; }\n"
	dashboardSave(t, fake, v2)
	lines, _ = e.personalSync(ctx)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Updated ~/") || !strings.Contains(lines[0], "machine.nix from your account") {
		t.Fatalf("pull: %q", lines)
	}
	if b, _ := os.ReadFile(e.machineNixPath()); string(b) != v2 {
		t.Fatalf("laptop copy after pull = %q", b)
	}
	if lines, _ := e.personalSync(ctx); len(lines) != 0 {
		t.Fatalf("after pull: %q", lines)
	}

	// Both changed: refused, the account keeps the dashboard's copy.
	dashboardSave(t, fake, "{ }\n")
	_ = os.WriteFile(e.machineNixPath(), []byte(v1), 0o644)
	lines, _ = e.personalSync(ctx)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Not pushing ~/") || !strings.Contains(lines[0], "saved on the dashboard") || !strings.Contains(lines[0], "repose config --global apply") {
		t.Fatalf("conflict: %q", lines)
	}
	if acct, _ := e.Client.GetPersonal(ctx); acct.Fragment != "{ }\n" {
		t.Fatalf("refused push changed the account: %q", acct.Fragment)
	}
	// An explicit apply wins, and the next run agrees.
	out := &discardWriter{}
	e.Out = out
	if err := GlobalApplyCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.buf.String(), "Saved machine.nix to your account; todo-app switches in place.") {
		t.Fatalf("apply output: %q", out.buf.String())
	}
	if acct, _ := e.Client.GetPersonal(ctx); acct.Fragment != v1 {
		t.Fatalf("apply: account = %q", acct.Fragment)
	}
	if lines, _ := e.personalSync(ctx); len(lines) != 0 {
		t.Fatalf("after apply: %q", lines)
	}
}

// `repose config --global add|remove|show`, and an error in machine.nix
// rendered against the laptop's copy with its line.
func TestGlobalConfigCommands(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	e.HomeDir = filepath.Dir(e.Dir)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out

	errOut := &discardWriter{}
	e.ErrOut = errOut
	if err := GlobalShowCmd(ctx, e, false); err != nil || out.buf.Len() != 0 || !strings.Contains(errOut.buf.String(), "No machine.nix on your account yet") {
		t.Fatalf("show with none: %v %q %q", err, out.buf.String(), errOut.buf.String())
	}
	if err := GlobalPackagesCmd(ctx, e, []string{"ripgrep", "pkgs.fd"}, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.HasPrefix(out.buf.String(), "Added ripgrep and fd to ~/") {
		t.Fatalf("add output: %q", out.buf.String())
	}
	local, _ := os.ReadFile(e.machineNixPath())
	if !strings.Contains(string(local), "    ripgrep\n    fd\n  ];") || !strings.HasPrefix(string(local), "# machine.nix") {
		t.Fatalf("machine.nix after add:\n%s", local)
	}
	out.buf.Reset()
	if err := GlobalShowCmd(ctx, e, false); err != nil || out.buf.String() != string(local) {
		t.Fatalf("show: %v\n%s", err, out.buf.String())
	}
	out.buf.Reset()
	if err := GlobalPackagesCmd(ctx, e, []string{"fd"}, true); err != nil || out.buf.String() != "fd already in ~/"+strings.TrimPrefix(e.machineNixPath(), e.HomeDir+"/")+".\n" {
		t.Fatalf("add again: %v %q", err, out.buf.String())
	}
	out.buf.Reset()
	if err := GlobalPackagesCmd(ctx, e, []string{"ripgrep"}, false); err != nil || !strings.HasPrefix(out.buf.String(), "Removed ripgrep from ~/") {
		t.Fatalf("remove: %v %q", err, out.buf.String())
	}
	if acct, _ := e.Client.GetPersonal(ctx); strings.Contains(acct.Fragment, "ripgrep") || !strings.Contains(acct.Fragment, "fd") {
		t.Fatalf("account after remove:\n%s", acct.Fragment)
	}
	for _, bad := range []string{"a;b", "${x}", "g cc"} {
		if err := GlobalPackagesCmd(ctx, e, []string{bad}, true); exitCode(err) != ExitUsage {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	// A file add cannot edit is refused with a pointer to edit.
	_ = os.WriteFile(e.machineNixPath(), []byte("{ programs.git.enable = true; }\n"), 0o644)
	if err := GlobalApplyCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if err := GlobalPackagesCmd(ctx, e, []string{"jq"}, true); exitCode(err) != ExitUsage || !strings.Contains(err.Error(), "repose config --global edit") {
		t.Fatalf("add without a list: %v", err)
	}
	// A failing machine.nix names its line in the laptop's copy.
	broken := "{\n  repose-force-eval-error = 1;\n}\n"
	_ = os.WriteFile(e.machineNixPath(), []byte(broken), 0o644)
	out.buf.Reset()
	err := GlobalApplyCmd(ctx, e, "")
	if exitCode(err) != ExitBuildFailed || !strings.Contains(out.buf.String(), "machine.nix:1:3") || !strings.Contains(out.buf.String(), "at machine.nix:1:3") {
		t.Fatalf("broken apply: %v\n%s", err, out.buf.String())
	}
	// --global takes no project.
	root := newRootCmd("test")
	root.SetArgs([]string{"config", "--global", "show", "--project", "x"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "takes no --project") {
		t.Fatalf("--global --project: %v", err)
	}
}

// --no-personal on a new machine creates it opted out.
func TestRunNoPersonalCreatesOptedOut(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoSync: true, NoAttach: true, NoPersonal: true}, false); err != nil {
		t.Fatal(err)
	}
	ps, _ := f.env.Client.ListProjects(context.Background())
	if len(ps) != 1 || !ps[0].PersonalOptOut {
		t.Fatalf("projects: %+v", ps)
	}
}

// A changed laptop machine.nix is pushed before the create, so the new
// machine's first revision carries it.
func TestRunPushesMachineNixBeforeCreate(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	text := "{ pkgs, ... }: { home.packages = [ pkgs.jq ]; }\n"
	if err := os.WriteFile(f.env.machineNixPath(), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut := &discardWriter{}
	f.env.ErrOut = errOut
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.buf.String(), "Applying machine.nix (changed) to new machines in the background.") {
		t.Fatalf("stderr: %q", errOut.buf.String())
	}
	ps, _ := f.env.Client.ListProjects(context.Background())
	if cfg, _ := f.env.Client.GetConfig(context.Background(), ps[0].ID); cfg.Personal != text {
		t.Fatalf("first revision personal = %q", cfg.Personal)
	}
	if !f.env.personalOn {
		t.Fatal("the tool scan was not told the account has a machine.nix")
	}
}

// The scan's precedence (DECISIONS I-490): machine.nix on the account
// skips the laptop's tools, repose.nix at the root skips the scripts'
// commands, and the runtime pins still travel.
func TestToolPrecedence(t *testing.T) {
	_, home := fakeLaptop(t)
	repo := t.TempDir()
	if err := os.CopyFS(repo, os.DirFS(filepath.Join("testdata", "scan", "monorepo"))); err != nil {
		t.Fatal(err)
	}
	full := buildToolsCarry(home, repo, precedenceFor(false, repo))
	var laptop, project int
	for _, it := range full.Wanted.Items {
		if it.From == "laptop" {
			laptop++
		} else {
			project++
		}
	}
	if laptop == 0 || project == 0 {
		t.Fatalf("fixture: %d laptop, %d project items", laptop, project)
	}
	noGlobals := buildToolsCarry(home, repo, precedenceFor(true, repo))
	for _, it := range noGlobals.Wanted.Items {
		if it.From == "laptop" {
			t.Fatalf("machine.nix on the account still carries %s", it.Name)
		}
	}
	writeFile(t, filepath.Join(repo, "repose.nix"), "{ }\n")
	prec := precedenceFor(true, repo)
	if !prec.SkipScripts || !prec.SkipGlobals {
		t.Fatalf("precedence = %+v", prec)
	}
	both := buildToolsCarry(home, repo, prec)
	if both != nil && len(both.Wanted.Items) != 0 {
		t.Fatalf("both skipped, still carried: %+v", both.Wanted.Items)
	}
	if full.Wanted.Node != "" && (both == nil || both.Wanted.Node != full.Wanted.Node) {
		t.Fatal("the node pin went with the scripts' commands")
	}
	var out bytes.Buffer
	printScanWith(&out, repo, readGlobalTools(toolEnv{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: lookPathFast}), scanProject(repo), prec, scanPersonal{Has: true, Why: "your account has a machine.nix"})
	s := out.String()
	if !strings.Contains(s, "  skipped: your account has a machine.nix, which describes your tools, so none of these is installed") ||
		!strings.Contains(s, "  skipped: repose.nix at the checkout root describes this project's tools") {
		t.Fatalf("scan output:\n%s", s)
	}
}

// logout --purge keeps machine.nix, the user's own file, and drops the
// rest, the push state included.
func TestPurgeKeepsMachineNix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "repose")
	writeFile(t, filepath.Join(cfg, "machine.nix"), "{ }\n")
	writeFile(t, filepath.Join(cfg, "machine.nix.state"), "{}")
	writeFile(t, filepath.Join(cfg, "credentials.json"), "{}")
	if err := purgeCLIFiles(cfg, filepath.Join(home, ".ssh", "repose")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cfg)
	if len(entries) != 1 || entries[0].Name() != "machine.nix" {
		t.Fatalf("left behind: %v", entries)
	}
}
