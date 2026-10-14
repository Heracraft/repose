package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// Account secrets (I-697): set, list, rm and import with --global act on
// the account's secrets, which every project gets; a project's own of the
// same name wins and is named.
func TestSecretsGlobal(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if _, err := e.Client.PutSecret(ctx, p.ID, "STRIPE_KEY", []byte("own")); err != nil {
		t.Fatal(err)
	}
	if err := SecretsSetGlobalCmd(ctx, e, "COOLIFY_BASE_URL", []byte("https://c.example")); err != nil {
		t.Fatal(err)
	}
	if err := SecretsSetGlobalCmd(ctx, e, "STRIPE_KEY", []byte("account")); err != nil {
		t.Fatal(err)
	}
	want := "Set COOLIFY_BASE_URL for every project; 1 running machine has it now.\n" +
		"Set STRIPE_KEY for every project; each machine gets it at its next start.\n" +
		"todo-app keeps its own STRIPE_KEY.\n"
	if got := out.buf.String(); got != want {
		t.Fatalf("set:\ngot  %q\nwant %q", got, want)
	}

	// Piped, list --global is one NAME\tDATE line each.
	out.buf.Reset()
	if err := SecretsListGlobalCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.buf.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "COOLIFY_BASE_URL\t") || !strings.HasPrefix(lines[1], "STRIPE_KEY\t") {
		t.Fatalf("list --global: %q", out.buf.String())
	}
	// A project's piped list stays its own.
	out.buf.Reset()
	if err := SecretsListCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasPrefix(got, "STRIPE_KEY\t") || strings.Count(got, "\n") != 1 {
		t.Fatalf("project list piped: %q", got)
	}
	e.JSON = true
	out.buf.Reset()
	if err := SecretsListGlobalCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), `"name": "COOLIFY_BASE_URL"`) {
		t.Fatalf("list --global --json: %s", out.buf.String())
	}
	e.JSON = false

	// The OAuth token as an account secret counts for Claude's login check.
	if has, err := hasOAuthSecret(ctx, e.Client, p.ID); err != nil || has {
		t.Fatalf("before: %v %v", has, err)
	}
	if _, err := e.Client.PutAccountSecret(ctx, "CLAUDE_CODE_OAUTH_TOKEN", []byte("tok")); err != nil {
		t.Fatal(err)
	}
	if has, err := hasOAuthSecret(ctx, e.Client, p.ID); err != nil || !has {
		t.Fatalf("account CLAUDE_CODE_OAUTH_TOKEN: %v %v", has, err)
	}

	out.buf.Reset()
	if err := SecretsRmGlobalCmd(ctx, e, "STRIPE_KEY"); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "Removed STRIPE_KEY from your account.\n" {
		t.Fatalf("rm: %q", got)
	}
	err := SecretsRmGlobalCmd(ctx, e, "STRIPE_KEY")
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGeneric || ee.msg != "Your account has no secret STRIPE_KEY." {
		t.Fatalf("second rm: %v", err)
	}
	// The project's own is still there.
	if ms, err := e.Client.ListSecrets(ctx, p.ID); err != nil || len(ms) != 1 || ms[0].Name != "STRIPE_KEY" {
		t.Fatalf("project secrets after: %+v %v", ms, err)
	}
	// A bad name is refused before any request.
	fake.Fail("PUT /me/secrets/{name}", "internal")
	if err := SecretsSetGlobalCmd(ctx, e, "BASH_ENV", []byte("x")); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved: %v", err)
	}
}

func TestAccountReachLines(t *testing.T) {
	for n, want := range map[int]string{0: "each machine gets it at its next start", 1: "1 running machine has it now", 3: "3 running machines have it now"} {
		if got := accountReach(n); got != want {
			t.Fatalf("%d: %q", n, got)
		}
	}
	var b strings.Builder
	writeOverridden(&b, "K", nil)
	writeOverridden(&b, "K", []string{"a", "b", "c"})
	if got := b.String(); got != "a, b and c keep their own K.\n" {
		t.Fatalf("%q", got)
	}
}

// On a terminal a project's list shows the account's secrets too, and
// marks the one the project's own replaces.
func TestWriteAccountSecretsTerminal(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local)
	secrets := []SecretMeta{{Name: "COOLIFY_BASE_URL", UpdatedAt: at}, {Name: "STRIPE_KEY", UpdatedAt: at}}
	var b strings.Builder
	writeAccountSecrets(&b, true, secrets, map[string]bool{"STRIPE_KEY": true}, "todo-app", false)
	want := "Stored by repose for every project:\n" +
		"  COOLIFY_BASE_URL\t2026-10-13 12:00\n" +
		"  STRIPE_KEY\t2026-10-13 12:00\t(todo-app's own is used)\n"
	if b.String() != want {
		t.Fatalf("got %q", b.String())
	}
	b.Reset()
	writeAccountSecrets(&b, true, nil, nil, "todo-app", false)
	if b.String() != "" {
		t.Fatalf("an empty account under a project's list: %q", b.String())
	}
	writeAccountSecrets(&b, true, nil, nil, "", true)
	if b.String() != "Stored by repose for every project: none\n" {
		t.Fatalf("list --global, none: %q", b.String())
	}
}

func TestSecretsGlobalRefusesAProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	fake.Fail("PUT /me/secrets/{name}", "internal")
	for _, args := range [][]string{{"set", "--global", "--from-env", "K"}, {"rm", "--global", "K"}, {"list", "--global"}, {"import", "--global"}} {
		cmd := newSecretsCmd(func() (*Env, error) { return e, nil }, &globalFlags{project: "todo-app"})
		cmd.SetArgs(args)
		cmd.SetOut(&discardWriter{})
		cmd.SetErr(&discardWriter{})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--global takes no project (todo-app)") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestSecretsImportGlobal(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var out strings.Builder
	e.Out = &out
	ctx := context.Background()
	if _, err := e.Client.PutAccountSecret(ctx, "OLD_KEY", []byte("old")); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "shared.env")
	if err := os.WriteFile(envFile, []byte("OLD_KEY=new\nCOOLIFY_BASE_URL=https://c.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No project is needed: the folder has none.
	if err := SecretsImportCmd(ctx, e, SecretsImportOptions{File: envFile, Global: true, DryRun: true}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would set 2 for every project from "+envFile+": OLD_KEY (replaced), COOLIFY_BASE_URL") {
		t.Fatalf("dry run: %s", out.String())
	}
	out.Reset()
	if err := SecretsImportCmd(ctx, e, SecretsImportOptions{File: envFile, Global: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Set 2 for every project from "+envFile+" (delivered at each machine's next start): OLD_KEY (replaced), COOLIFY_BASE_URL\n" {
		t.Fatalf("import: %q", got)
	}
	ms, err := e.Client.ListAccountSecrets(ctx)
	if err != nil || len(ms) != 2 {
		t.Fatalf("account after import: %+v %v", ms, err)
	}
}

// An api from before I-697 has no /me/secrets route: its plain 404 reads
// as that, not as a missing secret or a retry.
func TestSecretsGlobalOlderAPI(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok")), Out: &discardWriter{}, ErrOut: &discardWriter{}}
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"set":  func() error { return SecretsSetGlobalCmd(ctx, e, "K", []byte("v")) },
		"list": func() error { return SecretsListGlobalCmd(ctx, e) },
		"rm":   func() error { return SecretsRmGlobalCmd(ctx, e, "K") },
	} {
		err := run()
		if ee, ok := err.(*exitError); !ok || ee.msg != "This repose api does not have account secrets yet." {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if s, err := accountSecretsForList(ctx, e); err != nil || s != nil {
		t.Fatalf("a project's list beside an older api: %v %v", s, err)
	}
}

// A project's `secrets rm` names the account's secret of the same name:
// on a name only the account has, and when the project's own goes and
// the account's takes its place.
func TestSecretsRmProjectWithAccount(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if _, err := e.Client.PutAccountSecret(ctx, "COOLIFY_BASE_URL", []byte("https://c.example")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.PutAccountSecret(ctx, "STRIPE_KEY", []byte("account")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.PutSecret(ctx, p.ID, "STRIPE_KEY", []byte("own")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Client.PutSecret(ctx, p.ID, "ONLY_MINE", []byte("own")); err != nil {
		t.Fatal(err)
	}

	err := SecretsRmCmd(ctx, e, "", "COOLIFY_BASE_URL")
	want := "todo-app has no secret COOLIFY_BASE_URL of its own; it gets your account's.\n" +
		"repose secrets rm --global COOLIFY_BASE_URL removes that from every project."
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGeneric || ee.msg != want {
		t.Fatalf("account-only rm:\ngot  %v\nwant %q", err, want)
	}
	if err := SecretsRmCmd(ctx, e, "", "STRIPE_KEY"); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "Removed STRIPE_KEY from todo-app; it gets your account's now.\n" {
		t.Fatalf("own over account rm: %q", got)
	}
	out.buf.Reset()
	if err := SecretsRmCmd(ctx, e, "", "ONLY_MINE"); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "Removed ONLY_MINE from todo-app.\n" {
		t.Fatalf("plain rm: %q", got)
	}
	err = SecretsRmCmd(ctx, e, "", "NOWHERE")
	if ee, ok := err.(*exitError); !ok || ee.msg != "todo-app has no secret NOWHERE." {
		t.Fatalf("missing rm: %v", err)
	}
	// An api from before I-697 answers 404 on /me/secrets: the plain lines.
	fake.Fail("GET /me/secrets", "not_found")
	err = SecretsRmCmd(ctx, e, "", "COOLIFY_BASE_URL")
	if ee, ok := err.(*exitError); !ok || ee.msg != "todo-app has no secret COOLIFY_BASE_URL." {
		t.Fatalf("older api rm: %v", err)
	}
}
