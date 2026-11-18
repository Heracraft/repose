package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The first-run question (DECISIONS I-1338): one key, three answers, and
// each answer writes the record that stops it being asked again.
func TestGithubChoiceMenuKeys(t *testing.T) {
	for _, c := range []struct {
		in   string
		want githubChoice
	}{
		{"c", githubChoiceCopy},
		{"C", githubChoiceCopy},
		{"t", githubChoiceToken},
		{"n", githubChoiceNo},
		{"\r", githubChoiceNone},
		{"q", githubChoiceNone},
		{"\x1b", githubChoiceNone},
		{"", githubChoiceNone},     // the input ended
		{"zx c", githubChoiceCopy}, // anything else is ignored
	} {
		var out bytes.Buffer
		got, err := githubChoiceMenu(strings.NewReader(c.in), &out)
		if err != nil || got != c.want {
			t.Errorf("githubChoiceMenu(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		// Raw mode turns off the terminal's CR for LF, so every line
		// carries its own (I-1100).
		if strings.Contains(strings.ReplaceAll(out.String(), "\r\n", ""), "\n") {
			t.Errorf("githubChoiceMenu(%q) wrote a bare LF: %q", c.in, out.String())
		}
	}
	var out bytes.Buffer
	if _, err := githubChoiceMenu(strings.NewReader("\x03"), &out); err != errPromptInterrupted {
		t.Errorf("Ctrl-C = %v, want errPromptInterrupted", err)
	}
}

// stubGithubChoice makes the question askable and answers it with key,
// and gives the real menu back.
func stubGithubChoice(t *testing.T, can bool, key githubChoice) func() {
	t.Helper()
	oldCan, oldAsk := canAskGithubChoice, askGithubChoiceTTY
	canAskGithubChoice = func() bool { return can }
	askGithubChoiceTTY = func(io.Writer) (githubChoice, error) { return key, nil }
	return func() { canAskGithubChoice, askGithubChoiceTTY = oldCan, oldAsk }
}

// githubChoiceEnv is an Env with a config directory and a laptop home
// whose gh login is there or not.
func githubChoiceEnv(t *testing.T, ghLogin bool) *Env {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	if ghLogin {
		p := filepath.Join(home, ".config", "gh", "hosts.yml")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("github.com:\n  oauth_token: gho_x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &Env{Dir: dir, HomeDir: home, Cwd: t.TempDir(), Out: io.Discard, ErrOut: io.Discard}
}

func TestGhChoiceWanted(t *testing.T) {
	e := githubChoiceEnv(t, true)
	if !e.ghChoiceWanted() {
		t.Error("a laptop with a gh login and no list must be asked")
	}
	// A laptop with no gh login has nothing to ask about.
	if e := githubChoiceEnv(t, false); e.ghChoiceWanted() {
		t.Error("a laptop with no gh login must not be asked")
	}
	// A list of its own, however it was made, is the record of a choice.
	for _, skip := range [][]string{{}, {"gh"}, {"codex"}} {
		e := githubChoiceEnv(t, true)
		s := skip
		e.Cfg.Logins.Skip = &s
		if e.ghChoiceWanted() {
			t.Errorf("a laptop with skip = %v must not be asked again", skip)
		}
	}
}

// Each answer writes config.toml, and what it writes is what stops the
// question coming back.
func TestGithubChoiceAnswersAreRecorded(t *testing.T) {
	for _, c := range []struct {
		key  githubChoice
		want string // the skip list in config.toml
	}{
		{githubChoiceCopy, "[]"},
		{githubChoiceNo, `["gh"]`},
	} {
		e := githubChoiceEnv(t, true)
		restore := stubGithubChoice(t, true, c.key)
		defer restore()
		if err := e.askGithubChoice(context.Background()); err != nil {
			t.Fatalf("%q: %v", c.key, err)
		}
		b, err := os.ReadFile(filepath.Join(e.Dir, "config.toml"))
		if err != nil {
			t.Fatalf("%q: config.toml: %v", c.key, err)
		}
		if !strings.Contains(strings.ReplaceAll(string(b), " ", ""), "skip="+strings.ReplaceAll(c.want, " ", "")) {
			t.Errorf("%q wrote:\n%s\nwant skip = %s", c.key, b, c.want)
		}
		// Re-read the config the way a command does: the question is
		// then over.
		cfg, err := loadConfig(e.Dir)
		if err != nil {
			t.Fatal(err)
		}
		e.Cfg = cfg
		if e.ghChoiceWanted() {
			t.Errorf("%q is asked again after answering", c.key)
		}
	}
}

// Nothing is asked where there is nobody to ask, and the run goes on.
func TestGithubChoiceAsksNobodyWithoutATerminal(t *testing.T) {
	e := githubChoiceEnv(t, true)
	asked := false
	oldCan, oldAsk := canAskGithubChoice, askGithubChoiceTTY
	t.Cleanup(func() { canAskGithubChoice, askGithubChoiceTTY = oldCan, oldAsk })
	canAskGithubChoice = func() bool { return false }
	askGithubChoiceTTY = func(io.Writer) (githubChoice, error) { asked = true; return githubChoiceNo, nil }
	if err := e.askGithubChoice(context.Background()); err != nil || asked {
		t.Fatalf("asked = %v, err = %v; want neither", asked, err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "config.toml")); err == nil {
		t.Error("a question nobody was asked wrote a choice")
	}
}

// GitHub's expiry header, as GitHub writes it, and only when it says
// something a warning can use (I-1342).
func TestParseGithubExpiry(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	ahead := now.Add(30 * 24 * time.Hour)
	for _, c := range []struct {
		in   string
		want *time.Time
	}{
		{"2026-12-18 12:00:00 UTC", &ahead},
		{"2026-12-18 12:00:00 +0000", &ahead},
		{" 2026-12-18T12:00:00Z ", &ahead},
		{"", nil},
		{"never", nil},
		// A header that answers the current time, which fine-grained
		// tokens are reported to do, is no answer.
		{"2026-11-18 12:00:00 UTC", nil},
		{"2026-11-17 12:00:00 UTC", nil},
	} {
		got := parseGithubExpiry(c.in, now)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("parseGithubExpiry(%q) = %v, want none", c.in, got)
		case c.want != nil && (got == nil || !got.Equal(*c.want)):
			t.Errorf("parseGithubExpiry(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// tokenExpiry asks GitHub with the token and reads the header; anything
// else is no answer and never an error the user hears about.
func TestTokenExpiryReadsGitHub(t *testing.T) {
	now := time.Now()
	var gotAuth string
	old := githubExpiryClient
	t.Cleanup(func() { githubExpiryClient = old })
	githubExpiryClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		h := http.Header{}
		h.Set(githubExpiryHeader, now.Add(300*24*time.Hour).UTC().Format("2006-01-02 15:04:05 UTC"))
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	got := tokenExpiry(context.Background(), []byte("github_pat_11EXAMPLE"))
	if got == nil || got.Before(now.Add(290*24*time.Hour)) {
		t.Fatalf("tokenExpiry = %v", got)
	}
	if gotAuth != "Bearer github_pat_11EXAMPLE" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	// A GitHub that cannot be reached, or says nothing: no expiry, no
	// error.
	githubExpiryClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	if got := tokenExpiry(context.Background(), []byte("x")); got != nil {
		t.Errorf("an unreachable GitHub = %v, want none", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The one-line warning (I-1342): the week before, and after.
func TestGhExpiryLine(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	if l := ghExpiryLine(nil, now); l != "" {
		t.Errorf("no expiry = %q", l)
	}
	if l := ghExpiryLine(at(30*24*time.Hour), now); l != "" {
		t.Errorf("a month out = %q, want nothing", l)
	}
	l := ghExpiryLine(at(5*24*time.Hour), now)
	if !strings.Contains(l, "expires in") {
		t.Errorf("five days out = %q", l)
	}
	l = ghExpiryLine(at(-2*24*time.Hour), now)
	if !strings.Contains(l, "expired") {
		t.Errorf("two days past = %q", l)
	}
	// No next command on a warning (I-484): the line says the fact and
	// stops, and `repose github` is where it prints.
	for _, l := range []string{ghExpiryLine(at(5*24*time.Hour), now), ghExpiryLine(at(-2*24*time.Hour), now)} {
		if strings.Contains(l, "repose ") {
			t.Errorf("the warning names a command: %q", l)
		}
	}
}

// The account's switch subtracts from what the laptop does and never
// adds (I-1340), and a laptop that cannot reach the api keeps copying.
func TestLoginSkipForHonoursTheAccount(t *testing.T) {
	ctx := context.Background()
	write := func(e *Env, copyOK bool) {
		t.Helper()
		b, err := json.Marshal(accountCache{GhLoginCopy: &copyOK, ReadAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(accountCachePath(e.Dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := githubChoiceEnv(t, true)
	write(e, true)
	if skip, _ := e.loginSkipFor(ctx, ""); skip["gh"] {
		t.Error("an account that allows the copy must not turn it off")
	}
	write(e, false)
	if skip, _ := e.loginSkipFor(ctx, ""); !skip["gh"] {
		t.Error("an account that refuses the copy must turn it off")
	}
	// The laptop's own off stays off whatever the account says.
	e2 := githubChoiceEnv(t, true)
	write(e2, true)
	off := []string{"gh"}
	e2.Cfg.Logins.Skip = &off
	if skip, _ := e2.loginSkipFor(ctx, ""); !skip["gh"] {
		t.Error("the laptop's own off was overridden")
	}
	// A laptop with no gh login asks the account nothing and copies
	// nothing either way.
	e3 := githubChoiceEnv(t, false)
	if skip, _ := e3.loginSkipFor(ctx, ""); skip["gh"] {
		t.Error("a laptop with no gh login had the row turned off")
	}
}

// A server from before I-1340 sends no `github`, which must read as
// "copy as before" and never as the zero value's refusal.
func TestGhLoginCopyDefaultsToYes(t *testing.T) {
	var me *Me
	if !me.ghLoginCopy() {
		t.Error("no account read at all must mean yes")
	}
	me = &Me{}
	if !me.ghLoginCopy() {
		t.Error("an api with no github field must mean yes")
	}
	me.GitHub = &GitHubState{LoginCopy: false}
	if me.ghLoginCopy() {
		t.Error("login_copy false must mean no")
	}
}

// `repose github status` (DECISIONS I-1344): the account's row, then only
// the projects whose answer differs from it, with the expiry the account
// holds and the week-ahead warning.
func TestGithubStatusRows(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &bytes.Buffer{}
	var errOut bytes.Buffer
	e.Out, e.ErrOut = out, &errOut
	// A laptop with a gh login and no choice: every project uses it.
	ghHome(t, e)

	rows, err := e.githubRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Scope != "every project" || rows[0].Reach != githubLogin {
		t.Fatalf("with only a gh login: %+v", rows)
	}

	// The account's token wins over the login everywhere.
	if _, err := e.Client.PutAccountSecret(ctx, githubTokenSecret, []byte("github_pat_x")); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(3 * 24 * time.Hour).UTC()
	if err := e.setGhTokenExpiry(ctx, &at); err != nil {
		t.Fatal(err)
	}
	rows, err = e.githubRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Reach != githubToken || rows[0].Expires == nil {
		t.Fatalf("with an account token: %+v", rows)
	}

	// A project that turned the login off and has no token of its own
	// still uses the account's token, so it is not a row.
	off := []string{"gh"}
	e.Cfg.Projects = map[string]ProjectConfig{p.Slug: {Logins: LoginsConfig{Skip: &off}}}
	rows, err = e.githubRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("a project the account's token covers must not be a row: %+v", rows)
	}

	// Take the account's token away: now the project differs, because
	// its own list leaves the login behind and the account's does not.
	if err := e.Client.DeleteAccountSecret(ctx, githubTokenSecret); err != nil {
		t.Fatal(err)
	}
	rows, err = e.githubRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Reach != githubLogin || rows[1].Scope != p.Slug || rows[1].Reach != githubNothing {
		t.Fatalf("a project that differs must be a row: %+v", rows)
	}

	// The command prints the table and warns about the near expiry.
	if _, err := e.Client.PutAccountSecret(ctx, githubTokenSecret, []byte("github_pat_x")); err != nil {
		t.Fatal(err)
	}
	if err := e.setGhTokenExpiry(ctx, &at); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := GithubStatusCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"PROJECT", "REACHES GITHUB WITH", "EXPIRES", "every project", "GH_TOKEN", "(in "} {
		if !strings.Contains(got, want) {
			t.Errorf("`github status` did not print %q:\n%s", want, got)
		}
	}
	if !strings.Contains(errOut.String(), "expires in") {
		t.Errorf("no warning a week before: %q", errOut.String())
	}
}

// ghHome gives the Env a laptop home with a gh login in it.
func ghHome(t *testing.T, e *Env) {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("github.com:\n  oauth_token: gho_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.HomeDir = home
}

// The run that heard the answer is the one that must obey it: the
// config in memory was read before the question, so it is read again
// (seen live, 2026-11-18).
func TestGithubChoiceAppliesToThisRun(t *testing.T) {
	e := githubChoiceEnv(t, true)
	defer stubGithubChoice(t, true, githubChoiceNo)()
	if skip, _ := e.loginSkip(""); skip["gh"] {
		t.Fatal("the gh row is off before the question")
	}
	if err := e.askGithubChoice(context.Background()); err != nil {
		t.Fatal(err)
	}
	if skip, chosen := e.loginSkip(""); !skip["gh"] || !chosen {
		t.Fatalf("after answering no, this run still copies the login: skip=%v chosen=%v", skip, chosen)
	}
}
