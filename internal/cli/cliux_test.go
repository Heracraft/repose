package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// These cover the owner's 2026-09-23 session (DECISIONS I-149..I-155).

const rawGuestdError = "guestd unreachable for guest 01a0c161-4bd8-72fc-a028-b34e9e923376"

// failingDestroyAPI fronts the fake api and makes every destroy fail the
// way age-calculator's did: DELETE answers 202 with an op, and the op
// ends in error while the project stays.
func failingDestroyAPI(t *testing.T, fake *fakeapi.Fake, opErr map[string]any) string {
	t.Helper()
	target, _ := url.Parse(fake.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.Count(r.URL.Path, "/") == 3 && strings.HasPrefix(r.URL.Path, "/v1/projects/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"op_id":"op-destroy","state":"pending"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ops/op-destroy"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "error", "error": opErr})
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDestroyReportsAFailedOp is item 3: v0.1.4 printed "Destroyed." from
// the 202 while the op failed and the project stayed.
func TestDestroyReportsAFailedOp(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   map[string]any
		wants []string
	}{
		{"older api: the host's wording", map[string]any{"code": "guest_unresponsive", "message": rawGuestdError},
			[]string{"Could not destroy todo-app", "stopped responding", "(guest_unresponsive)", "`repose rm todo-app` tries again", "still there"}},
		{"api with I-159 sentences", map[string]any{"code": "internal", "message": "the host could not remove the volume", "detail": "lvremove: exit 5"},
			[]string{"Could not destroy todo-app: the host could not remove the volume (internal).", "`repose rm todo-app` tries again"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeapi.New(fakeapi.Options{})
			defer fake.Close()
			e := newLifecycleEnv(t, fake)
			e.Client = newClient(failingDestroyAPI(t, fake, tc.err)+"/v1", staticToken("tok"))
			var out strings.Builder
			e.Out = &out
			ctx := context.Background()
			p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "todo-app", Class: "large"})
			if err != nil {
				t.Fatal(err)
			}

			err = DestroyCmd(ctx, e, p.ID, true, true, nil)
			ee, ok := err.(*exitError)
			if !ok || ee.code != ExitGeneric {
				t.Fatalf("err = %v, want exit 1", err)
			}
			for _, w := range tc.wants {
				if !strings.Contains(ee.msg, w) {
					t.Errorf("message lacks %q:\n%s", w, ee.msg)
				}
			}
			if strings.Contains(ee.msg, "01a0c161") || strings.Contains(ee.msg, "lvremove") {
				t.Errorf("message leaks a guest id or the host's detail:\n%s", ee.msg)
			}
			if strings.Contains(out.String(), "Destroyed") {
				t.Fatalf("printed Destroyed for a failed destroy: %q", out.String())
			}
			if _, err := e.Client.GetProject(ctx, p.ID); err != nil {
				t.Fatalf("the project should still exist: %v", err)
			}
		})
	}
}

func TestDestroyConfirmationIsYesNo(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "age-calculator", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	var asked string
	no := func(prompt string) (bool, error) { asked = prompt; return false, nil }
	if err := DestroyCmd(ctx, e, p.ID, false, false, no); err != nil {
		t.Fatalf("declined destroy: %v", err)
	}
	if asked != "Destroy age-calculator? A final snapshot is kept for 30 days. [y/N] " {
		t.Fatalf("prompt = %q", asked)
	}
	if !strings.Contains(out.String(), "Nothing destroyed") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := e.Client.GetProject(ctx, p.ID); err != nil {
		t.Fatal("a declined destroy destroyed")
	}

	yes := func(string) (bool, error) { return true, nil }
	if err := DestroyCmd(ctx, e, p.ID, false, false, yes); err != nil {
		t.Fatalf("confirmed destroy: %v", err)
	}
	// I-166: the destroy returns once accepted, and says how long it can
	// come back (I-484: without the command).
	if !strings.Contains(out.String(), "Destroying age-calculator. Its final snapshot is kept for 30 days.\n") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := e.Client.GetProject(ctx, p.ID); !isNotFound(err) {
		t.Fatalf("GET after destroy: %v, want 404", err)
	}
}

// TestNotRunningMessagesSayTheTruth is item 6: attach said "is stopped"
// for a project in error.
func TestNotRunningMessagesSayTheTruth(t *testing.T) {
	le := "guest_unresponsive: " + rawGuestdError
	errProject := &Project{Slug: "age-calculator", State: "error", LastError: &le}
	msg := notRunningMessage(errProject)
	for _, w := range []string{"age-calculator is in an error state", "stopped responding", "`repose start age-calculator`"} {
		if !strings.Contains(msg, w) {
			t.Errorf("error-state message lacks %q: %s", w, msg)
		}
	}
	if strings.Contains(msg, "is stopped") || strings.Contains(msg, "01a0c161") {
		t.Errorf("error-state message: %s", msg)
	}

	sentence := "guest_unresponsive: the environment's agent (guestd) stopped answering; `repose start` restarts it"
	msg = notRunningMessage(&Project{Slug: "izma", State: "error", LastError: &sentence})
	if !strings.Contains(msg, "stopped answering; `repose start` restarts it.") || strings.Count(msg, "repose start") != 1 {
		t.Errorf("api sentence not used as is, or advice doubled: %s", msg)
	}

	if msg := notRunningMessage(&Project{Slug: "izma", State: "stopped"}); !strings.Contains(msg, "`repose start izma`") {
		t.Errorf("stopped: %s", msg)
	}
	if msg := notRunningMessage(&Project{Slug: "izma", State: "building"}); !strings.Contains(msg, "still building") {
		t.Errorf("building: %s", msg)
	}
}

func TestAttachToAnErroredGuestSaysError(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "age-calculator", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "error")
	err = runRun(ctx, e, RunOptions{ProjectArg: "age-calculator"}, true)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitGuestNotRunning || !strings.Contains(ee.msg, "error state") {
		t.Fatalf("err = %v", err)
	}
}

// TestStartFromErrorSaysRestarting is the I-157 contract seen from the
// CLI: a start of a project in error is a restart, and the progress line
// says why.
func TestStartFromErrorSaysRestarting(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var errOut, out strings.Builder
	e.ErrOut, e.Out = &errOut, &out
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetState(p.ID, "error")
	if err := StartCmd(ctx, e, "izma"); err != nil {
		t.Fatalf("StartCmd: %v", err)
	}
	if !strings.Contains(errOut.String(), "Restarting izma (its agent stopped answering)...") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if !strings.Contains(out.String(), "izma is running") {
		t.Fatalf("stdout = %q", out.String())
	}
}

// TestProjectsTable is item 9: a header, a dash for what does not apply,
// and the reason under a project in error; --json unchanged.
func TestProjectsTable(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	for _, n := range []string{"izma", "age-calculator"} {
		if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: n, Class: "large"}); err != nil {
			t.Fatal(err)
		}
	}
	ps, _ := e.Client.ListProjects(ctx)
	for _, p := range ps {
		if p.Slug == "age-calculator" {
			fake.SetState(p.ID, "error")
		}
	}
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.HasPrefix(lines[0], "PROJECT") || !strings.Contains(lines[0], "STATE") || !strings.Contains(lines[0], "MONTH") {
		t.Fatalf("no header row:\n%s", out.String())
	}
	if strings.Contains(lines[0], "LEFT") {
		t.Fatalf("LEFT is a column only while a temporary machine is listed (I-484):\n%s", out.String())
	}
	if !strings.Contains(out.String(), "age-calculator: ") || !strings.Contains(out.String(), "`repose start age-calculator`") {
		t.Fatalf("no reason for the errored project:\n%s", out.String())
	}

	// -q (I-276): the names alone, for xargs.
	out.Reset()
	e.Quiet = true
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "izma\nage-calculator\n" && got != "age-calculator\nizma\n" {
		t.Fatalf("ls -q = %q", got)
	}
	e.Quiet = false

	out.Reset()
	e.JSON = true
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil || len(decoded) != 2 {
		t.Fatalf("--json is not the list: %v\n%s", err, out.String())
	}
	for _, k := range []string{"id", "slug", "state", "class", "cost_month_cents"} {
		if _, ok := decoded[0][k]; !ok {
			t.Fatalf("--json lost %q", k)
		}
	}
}

// TestPositionalProject is item 4, through the real command tree: the
// project as the argument, --project still working, both at once only
// when they agree, and a stray word no longer ignored.
func TestPositionalProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("REPOSE_API_URL", fake.URL()+"/v1")
	t.Setenv("REPOSE_PROJECT", "")
	if err := os.MkdirAll(filepath.Join(cfg, "repose"), 0o700); err != nil {
		t.Fatal(err)
	}
	creds, _ := json.Marshal(Credentials{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour), LogtoIssuer: "https://auth.example"})
	if err := os.WriteFile(filepath.Join(cfg, "repose", "credentials.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	client := newClient(fake.URL()+"/v1", staticToken("tok"))
	ctx := context.Background()
	izma, err := client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := client.CreateProject(ctx, CreateProjectRequest{Name: "other", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) error {
		root := newRootCmd("test")
		root.SetArgs(args)
		root.SetOut(&strings.Builder{})
		root.SetErr(&strings.Builder{})
		return root.ExecuteContext(ctx)
	}

	quietStdout(t)
	if err := run("stop", "izma"); err != nil {
		t.Fatalf("repose stop izma: %v", err)
	}
	if p, _ := client.GetProject(ctx, izma.ID); p.State != "stopped" {
		t.Fatalf("izma is %s after `repose stop izma`", p.State)
	}
	if p, _ := client.GetProject(ctx, other.ID); p.State != "running" {
		t.Fatalf("other is %s; only izma should have stopped", p.State)
	}
	if err := run("start", "--project", "izma"); err != nil {
		t.Fatalf("repose start --project izma: %v", err)
	}
	if err := run("stop", "izma", "--project", "izma"); err != nil {
		t.Fatalf("agreeing positional and --project: %v", err)
	}
	if err := run("status", "izma", "--project", "other"); !isUsage(err) {
		t.Fatalf("conflicting positional and --project: %v", err)
	}
	if err := run("attach", "izma", "other"); !isUsage(err) {
		t.Fatalf("two positionals: %v", err)
	}
	if err := run("projects", "extra"); !isUsage(err) {
		t.Fatalf("projects with a stray word: %v", err)
	}
	// The transcript's `repose attach projects`: now a project lookup.
	err = run("attach", "projects")
	if ee, ok := err.(*exitError); !ok || ee.code != ExitProjectNotFound {
		t.Fatalf("attach projects: %v", err)
	}
	// `ls` and `rm` are the names (I-273); `projects` and `destroy`
	// still work as aliases.
	if err := run("ls", "extra"); !isUsage(err) {
		t.Fatalf("ls with a stray word: %v", err)
	}
	if err := run("ls", "-q", "--json"); !isUsage(err) {
		t.Fatalf("ls -q --json: %v", err)
	}
	if err := run("destroy", "other", "--yes"); err != nil {
		t.Fatalf("repose destroy other --yes: %v", err)
	}
	if _, err := client.GetProject(ctx, other.ID); !isNotFound(err) {
		t.Fatalf("other not destroyed: %v", err)
	}
	third, err := client.CreateProject(ctx, CreateProjectRequest{Name: "third", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if err := run("rm", "third", "-y"); err != nil {
		t.Fatalf("repose rm third -y: %v", err)
	}
	if _, err := client.GetProject(ctx, third.ID); !isNotFound(err) {
		t.Fatalf("third not removed: %v", err)
	}

	// Completion offers the account's slugs for the argument.
	root := newRootCmd("test")
	var comp strings.Builder
	root.SetOut(&comp)
	root.SetArgs([]string{"__complete", "attach", ""})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comp.String(), "izma") {
		t.Fatalf("completion did not offer izma:\n%s", comp.String())
	}
}

// TestProjectArgumentOnSubcommands: the commands I-155 missed take the
// project as their first argument too (snapshots list/create/restore,
// secrets list, config show/edit; the owner's `repose snapshots create
// parth-event`).
func TestProjectArgumentOnSubcommands(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("REPOSE_API_URL", fake.URL()+"/v1")
	t.Setenv("REPOSE_PROJECT", "")
	if err := os.MkdirAll(filepath.Join(cfg, "repose"), 0o700); err != nil {
		t.Fatal(err)
	}
	creds, _ := json.Marshal(Credentials{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour), LogtoIssuer: "https://auth.example"})
	if err := os.WriteFile(filepath.Join(cfg, "repose", "credentials.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	client := newClient(fake.URL()+"/v1", staticToken("tok"))
	ctx := context.Background()
	izma, err := client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateProject(ctx, CreateProjectRequest{Name: "other", Class: "large"}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		root := newRootCmd("test")
		root.SetArgs(args)
		root.SetOut(&strings.Builder{})
		root.SetErr(&strings.Builder{})
		return root.ExecuteContext(ctx)
	}
	quietStdout(t)

	if err := run("snapshots", "create", "izma"); err != nil {
		t.Fatalf("repose snapshots create izma: %v", err)
	}
	snaps, err := client.ListSnapshots(ctx, izma.ID)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("izma's snapshots after `snapshots create izma`: %+v %v", snaps, err)
	}
	for _, args := range [][]string{
		{"snapshots", "list", "izma"},
		{"snapshots", "ls", "izma", "--project", "izma"},
		{"secrets", "list", "izma"},
		{"config", "show", "izma"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("repose %s: %v", strings.Join(args, " "), err)
		}
	}
	for _, args := range [][]string{
		{"snapshots", "create", "izma", "--project", "other"},
		{"snapshots", "create", "izma", "other"},
		{"snapshots", "list", "izma", "other"},
		{"secrets", "list", "izma", "--project", "other"},
		{"config", "show", "izma", "other"},
		{"config", "edit", "izma", "--project", "other"},
		{"config", "--global", "show", "izma"},
		{"snapshots", "restore"},
		{"snapshots", "restore", "izma", snaps[0].ID, "extra"},
		{"snapshots", "restore", "izma", snaps[0].ID, "--project", "other"},
	} {
		if err := run(args...); !isUsage(err) {
			t.Fatalf("repose %s: %v, want a usage error", strings.Join(args, " "), err)
		}
	}
	// Restore takes [PROJECT] SNAPSHOT_ID: the project first, as above,
	// and one argument is still the snapshot.
	if err := run("stop", "izma"); err != nil {
		t.Fatal(err)
	}
	if err := run("snapshots", "restore", "izma", snaps[0].ID, "--yes"); err != nil {
		t.Fatalf("repose snapshots restore izma ID: %v", err)
	}
	if p, _ := client.GetProject(ctx, izma.ID); p != nil && p.State != "stopped" {
		if err := run("stop", "izma"); err != nil {
			t.Fatal(err)
		}
	}
	if err := run("snapshots", "restore", snaps[0].ID, "--project", "izma", "--yes"); err != nil {
		t.Fatalf("repose snapshots restore ID --project izma: %v", err)
	}
}

func isUsage(err error) bool {
	_, ok := err.(cobraUsageError)
	return ok
}

// quietStdout sends os.Stdout to /dev/null for the test: the commands
// print their results there, and the assertions read the fake instead.
func quietStdout(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = devnull
	t.Cleanup(func() { os.Stdout = old; _ = devnull.Close() })
}

func TestRunRefusesAPromptThatIsAProjectName(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	ctx := context.Background()
	if _, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "izma", Class: "large"}); err != nil {
		t.Fatal(err)
	}
	err := runRun(ctx, e, RunOptions{Prompt: "izma", Name: "x"}, false)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "repose run --project izma") {
		t.Fatalf("err = %v", err)
	}
}

func TestReasonFor(t *testing.T) {
	if got := reasonFor("guest_unresponsive", rawGuestdError); strings.Contains(got, "01a0") || !strings.Contains(got, "stopped responding") {
		t.Errorf("raw host wording: %q", got)
	}
	s := "the environment's agent (guestd) stopped answering; `repose start` restarts it"
	if got := reasonFor("guest_unresponsive", s+"."); got != s {
		t.Errorf("api sentence: %q", got)
	}
	if got := reasonFor("", ""); got != "" {
		t.Errorf("empty: %q", got)
	}
	code, msg := splitLastError("guest_unresponsive: " + s)
	if code != "guest_unresponsive" || msg != s {
		t.Errorf("splitLastError = %q, %q", code, msg)
	}
}

// TestSSHErrorsAreSentences is the v0.1.4 message "ssh cd ~/izma && git
// status --porcelain: exit status 255: Connection closed by …" as it
// reads now: no command, a sentence, the useful detail.
func TestSSHErrorsAreSentences(t *testing.T) {
	err := stepFailed("read the guest's checkout", &sshError{ExitCode: 255, Stderr: "Connection closed by 20.102.98.254 port 22\r\n"}, "")
	msg := err.Error()
	if msg != "Could not read the guest's checkout: the SSH connection to the guest failed (Connection closed by 20.102.98.254 port 22)." {
		t.Fatalf("msg = %q", msg)
	}
	msg = stepFailed("sync your checkout to the guest", &sshError{ExitCode: 128, Stderr: "fatal: bad object\n"}, "").Error()
	if strings.Contains(msg, "git status") || !strings.Contains(msg, "exited with status 128 (fatal: bad object)") {
		t.Fatalf("msg = %q", msg)
	}
}

func TestProgressOutput(t *testing.T) {
	var plain strings.Builder
	p := newProgress(&plain, false)
	p.Phase("Creating izma", "Created izma")
	p.Phase("Building the environment", "Built the environment")
	_, _ = p.Write([]byte("nix › building\n"))
	p.End()
	if plain.String() != "Creating izma...\nBuilding the environment...\nnix › building\n" {
		t.Fatalf("non-TTY output = %q", plain.String())
	}

	var tty strings.Builder
	p = newProgress(&tty, true)
	p.Phase("Building the environment", "Built the environment")
	time.Sleep(250 * time.Millisecond)
	p.End()
	got := tty.String()
	if !strings.Contains(got, "\r\033[K") || !strings.Contains(got, "✓ Built the environment  ") {
		t.Fatalf("TTY output = %q", got)
	}
	var nilP *progress
	nilP.Phase("x", "y")
	nilP.End()
	nilP.Fail()
}
