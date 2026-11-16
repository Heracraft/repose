package cli

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// I-1064: the prefilled form asks for Contents and Pull requests write and
// nothing else, GitHub's longest expiry, and the checkout's owner as the
// resource owner. The parameter names are GitHub's own (docs:
// "Pre-filling fine-grained personal access token details using URL
// parameters"); a wrong name is silently ignored by the form, so they are
// pinned here.
func TestGithubTokenURL(t *testing.T) {
	raw := githubTokenURL("acme")
	base, args, ok := strings.Cut(raw, "?")
	if !ok || base != "https://github.com/settings/personal-access-tokens/new" {
		t.Fatalf("url = %q", raw)
	}
	q, err := url.ParseQuery(args)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"name":          "coding machines",
		"target_name":   "acme",
		"expires_in":    "366",
		"contents":      "write",
		"pull_requests": "write",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	for k := range q {
		if _, named := want[k]; !named {
			t.Errorf("unnamed parameter %s: every permission the token gets is in features/secrets.md", k)
		}
	}
	// Nothing that deletes a repository, changes its visibility, reads its
	// secrets or writes its workflows.
	for _, perm := range []string{"administration", "repository_creation", "secrets", "workflows", "actions"} {
		if q.Has(perm) {
			t.Errorf("%s must not be asked for", perm)
		}
	}
	// No owner: GitHub defaults the resource owner to the user's account.
	if q := mustQuery(t, githubTokenURL("")); q.Has("target_name") {
		t.Errorf("target_name with no owner: %q", q.Get("target_name"))
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestGithubOwnerOf(t *testing.T) {
	for remote, want := range map[string]string{
		"github.com/heracraft/todo-app": "heracraft",
		"github.com/acme/a/b":           "acme",
		"gitlab.com/heracraft/x":        "",
		"github.com/heracraft":          "",
		"":                              "",
	} {
		if got := githubOwnerOf(remote); got != want {
			t.Errorf("%q: got %q, want %q", remote, got, want)
		}
	}
}

// `repose github setup` stores the token as the account's GH_TOKEN, which
// gh reads before any login left on the machine, and turns the gh row off
// so the broad login stops travelling (a broad token beside a narrow one
// protects nothing).
func TestGithubSetupStoresTokenAndLeavesGhLogin(t *testing.T) {
	t.Setenv(envProject, "")
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	const before = "# keep me\ndefault_agent = \"codex\"\n"
	if err := os.WriteFile(configPath(e.Dir), []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Cfg = cfg
	ctx := context.Background()
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Token: []byte("github_pat_11ABCDE")}); err != nil {
		t.Fatal(err)
	}
	secrets, err := e.Client.ListAccountSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 || secrets[0].Name != githubTokenSecret {
		t.Fatalf("account secrets = %+v", secrets)
	}
	b, err := os.ReadFile(configPath(e.Dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.HasPrefix(got, before) || !strings.Contains(got, "[logins]\nskip = [\"gh\"]\n") {
		t.Fatalf("config.toml:\n%s", got)
	}
	want := "Set GH_TOKEN for every project; 1 running machine has it now.\n" +
		"Your gh login stays on your laptop, for every project.\n"
	if got := out.buf.String(); got != want {
		t.Fatalf("output:\ngot  %q\nwant %q", got, want)
	}
	// Run again: the gh row is already off, so nothing is said about it.
	cfg, err = loadConfig(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Cfg = cfg
	out.buf.Reset()
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Token: []byte("github_pat_22FGHIJ")}); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); strings.Contains(got, "stays on your laptop") {
		t.Fatalf("second run says it again: %q", got)
	}
	// A token that is not fine-grained is stored, with one line saying it
	// is not limited to the repositories picked.
	out.buf.Reset()
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Token: []byte("ghp_classic")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "not a fine-grained token") {
		t.Fatalf("classic token: %q", out.buf.String())
	}
	// No token at all is a usage error, before anything is stored.
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Token: []byte("  ")}); exitCodeFor(err, out) != ExitUsage {
		t.Fatalf("empty token: %v", err)
	}
}

// With --project the token is that project's own secret and only that
// project stops copying the login.
func TestGithubSetupProjectScope(t *testing.T) {
	t.Setenv(envProject, "")
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	ctx := context.Background()
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Project: p.Slug, Token: []byte("github_pat_11ABCDE")}); err != nil {
		t.Fatal(err)
	}
	if ms, err := e.Client.ListSecrets(ctx, p.ID); err != nil || len(ms) != 1 || ms[0].Name != githubTokenSecret {
		t.Fatalf("project secrets = %+v %v", ms, err)
	}
	if ms, err := e.Client.ListAccountSecrets(ctx); err != nil || len(ms) != 0 {
		t.Fatalf("account secrets = %+v %v", ms, err)
	}
	b, _ := os.ReadFile(configPath(e.Dir))
	if got := string(b); !strings.Contains(got, "[projects."+p.Slug+".logins]\nskip = [\"gh\"]\n") {
		t.Fatalf("config.toml:\n%s", got)
	}
	if got := out.buf.String(); !strings.Contains(got, "Your gh login stays on your laptop, for "+p.Slug+".") {
		t.Fatalf("output: %q", got)
	}
}

// I-1064: the SSH-URL rewrites go whether the gh login travels or is left
// at home, so a checkout whose remote is git@github.com reaches GitHub
// over HTTPS with the GH_TOKEN secret. With no login and no token git
// then asks for a username, where before it failed on a key the machine
// never had (I-247).
func TestSyncCredentialsGhHelpersWithoutLogin(t *testing.T) {
	f := newSyncFixture(t)
	home := t.TempDir()
	hosts := filepath.Join(home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(hosts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hosts, []byte("github.com:\n    oauth_token: NEVER-GH-TOKEN\n    user: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := credSyncOptions{Skip: map[string]bool{"gh": true}}
	if _, _, err := syncCredentialsAndCarry(context.Background(), f.target, home, f.local, opts, carryOptions{}); err != nil {
		t.Fatal(err)
	}
	guestGit := func(args ...string) string {
		t.Helper()
		out, _ := exec.Command("git", append([]string{"config", "--file", filepath.Join(f.guestHome, ".gitconfig")}, args...)...).Output()
		return strings.Join(strings.Fields(strings.TrimSpace(string(out))), " ")
	}
	if got := guestGit("--get-all", "url.https://github.com/.insteadOf"); got != "git@github.com: ssh://git@github.com/" {
		t.Errorf("insteadOf = %q", got)
	}
	if got := guestGit("--get-all", "credential.https://github.com.helper"); got != "!gh auth git-credential" {
		t.Errorf("helper = %q", got)
	}
	// The login itself did not travel.
	if fileExists(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")) {
		t.Error("the gh login travelled although it is off")
	}
}

// A project with a list of its own replaces the shared one (I-422), so the
// table setup wrote does not reach it and its machine keeps a hosts.yml an
// agent can read. Setup names those projects.
func TestGithubSetupNamesProjectsStillCopyingGh(t *testing.T) {
	t.Setenv(envProject, "")
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	cfgText := "[projects.api.logins]\nskip = [\"env\"]\n\n[projects.web.logins]\nskip = [\"gh\"]\n\n[projects.docs.logins]\nskip = []\n"
	if err := os.WriteFile(configPath(e.Dir), []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Cfg = cfg
	if err := GithubSetupCmd(context.Background(), e, githubSetupOpts{Token: []byte("github_pat_11ABCDE")}); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	const want = "api and docs have lists of their own that still copy the gh login; run this with --project to cover it.\n"
	if !strings.Contains(got, want) {
		t.Fatalf("output:\ngot  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "web") {
		t.Fatalf("web already leaves gh and must not be named: %q", got)
	}
}

// $REPOSE_PROJECT means the same as --project here, for the secret and for
// the login's row together: an account token with one project's row off
// would leave every other project sending the broad login.
func TestGithubSetupFollowsProjectEnv(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	t.Setenv(envProject, p.Slug)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	ctx := context.Background()
	if err := GithubSetupCmd(ctx, e, githubSetupOpts{Token: []byte("github_pat_11ABCDE")}); err != nil {
		t.Fatal(err)
	}
	if ms, err := e.Client.ListAccountSecrets(ctx); err != nil || len(ms) != 0 {
		t.Fatalf("the account must hold no secret: %+v %v", ms, err)
	}
	if ms, err := e.Client.ListSecrets(ctx, p.ID); err != nil || len(ms) != 1 || ms[0].Name != githubTokenSecret {
		t.Fatalf("project secrets = %+v %v", ms, err)
	}
	b, _ := os.ReadFile(configPath(e.Dir))
	if got := string(b); !strings.Contains(got, "[projects."+p.Slug+".logins]") {
		t.Fatalf("config.toml:\n%s", got)
	}
}

// A paste carries a space or a newline often enough; a token holds
// neither. Untrimmed, the value stored is broken and the prefix check
// fires on a good token (found in review).
func TestGithubSetupTrimsThePastedToken(t *testing.T) {
	t.Setenv(envProject, "")
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	if err := GithubSetupCmd(context.Background(), e, githubSetupOpts{Token: []byte(" github_pat_11ABCDE \n")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.buf.String(), "not a fine-grained token") {
		t.Fatalf("the paste was not trimmed before the check: %q", out.buf.String())
	}
}

// A project with no list of its own follows the shared one, so the table
// --project writes has to carry the shared rows over with gh, or turning
// the token on would start copying what the user had turned off.
func TestGithubSetupProjectKeepsTheSharedRows(t *testing.T) {
	t.Setenv(envProject, "")
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	out := &discardWriter{}
	e.Out, e.ErrOut = out, out
	if err := os.WriteFile(configPath(e.Dir), []byte("[logins]\nskip = [\"env\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Cfg = cfg
	if err := GithubSetupCmd(context.Background(), e, githubSetupOpts{Project: p.Slug, Token: []byte("github_pat_11ABCDE")}); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	skip, _, _ := cfg.loginSkip(p.Slug)
	if !skip["gh"] || !skip["env"] {
		t.Fatalf("the project's own list dropped a shared row: %v", skip)
	}
}
