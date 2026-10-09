package cli

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// -v logs each request's method, path, status and request id, and never
// its token (DECISIONS I-624).
func TestVerboseLogsRequests(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	var buf bytes.Buffer
	setVerbose(&buf)
	defer setVerbose(nil)
	base := fake.URL() + "/v1"
	c := newClient(base, staticToken("secret-token"))
	c.HTTP = withVerbose(&http.Client{}, base)
	if _, err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetProject(context.Background(), "no-such")
	out := buf.String()
	if !strings.Contains(out, "repose: GET /v1/me -> 200 ") || !strings.Contains(out, "request req-") {
		t.Errorf("no request line: %q", out)
	}
	if !strings.Contains(out, "-> 404 ") || !strings.Contains(out, `"not_found"`) {
		t.Errorf("no refusal line with its body: %q", out)
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "Bearer") {
		t.Errorf("the token reached the log: %q", out)
	}
	buf.Reset()
	verboseSSH([]string{"-t", "todo-app.repose"}, "tmux attach -t todo-app")
	if got := buf.String(); got != "repose: ssh -t todo-app.repose tmux\n" {
		t.Errorf("ssh line %q", got)
	}
}

// ssh failures of the laptop's own end the wait at once with the fix; a
// timeout or a refusal does not (DECISIONS I-625).
func TestLaptopSSHFaults(t *testing.T) {
	cases := []struct {
		stderr string
		fault  bool
		want   string
	}{
		{"unix_listener: path \"/Users/someone/.ssh/x\" too long for Unix domain socket", true, "ControlPath"},
		{"/Users/a/.ssh/config: line 4: Bad configuration option: usekeychainx\n/Users/a/.ssh/config: terminating, 1 bad configuration options", true, "Fix the line"},
		{"ssh: Could not resolve hostname todo-app.repose: nodename nor servname provided, or not known", true, "Include ~/.ssh/repose/config"},
		{"ssh: Could not resolve hostname ssh.repose.herakraft.co: Name or service not known", true, ""},
		{"Host key verification failed.", true, "known_hosts"},
		{"Bad owner or permissions on /home/a/.ssh/config", true, "chmod 600"},
		{"ssh: connect to host 10.64.0.2 port 22: Connection refused", false, ""},
		{"Connection timed out during banner exchange", false, ""},
		{"dev@todo-app: Permission denied (publickey).", false, ""},
	}
	for _, c := range cases {
		fix, ok := laptopSSHFault(&sshError{ExitCode: 255, Stderr: c.stderr})
		if ok != c.fault || !strings.Contains(fix, c.want) {
			t.Errorf("%q: fault %v %q", c.stderr, ok, fix)
		}
	}
	if _, ok := laptopSSHFault(&sshError{ExitCode: 1, Stderr: "Host key verification failed."}); ok {
		t.Error("a remote command's exit is no laptop fault")
	}
	if got := targetName(hostTarget("todo-app")); got != "todo-app" {
		t.Errorf("targetName = %q", got)
	}
}

func TestVersionOlder(t *testing.T) {
	for _, c := range []struct {
		have, want string
		older      bool
	}{
		{"v0.1.20", "v0.1.24", true},
		{"v0.1.24", "v0.1.24", false},
		{"v0.2.0", "v0.1.99", false},
		{"v0.1.9", "v0.1.10", true},
		{"dev", "v0.1.24", false},
		{"v0.1.20-3-gabc", "v0.1.24", false},
		{"v0.1.20", "junk", false},
	} {
		if got := versionOlder(c.have, c.want); got != c.older {
			t.Errorf("versionOlder(%s, %s) = %v", c.have, c.want, got)
		}
	}
}

// An old CLI says so once per newer release, read from the api's header
// (DECISIONS I-626).
func TestNewerCLINoticeOncePerRelease(t *testing.T) {
	t.Setenv(envXDGConfigHome, t.TempDir())
	t.Setenv(envInGuest, "")
	defer func(v string) { cliVersion = v }(cliVersion)
	cliVersion = "v0.1.20"
	reset := func(latest string) {
		latestMu.Lock()
		latestCLI, noticed = "", false
		latestMu.Unlock()
		h := http.Header{}
		h.Set(latestCLIHeader, latest)
		noteLatestCLI(h)
	}
	var buf bytes.Buffer
	reset("v0.1.24")
	noteNewerCLITo(&buf)
	want := "repose v0.1.20 is older than v0.1.24, the latest release. `curl -fsSL https://repose.herakraft.co/install.sh | sh` updates it.\n"
	if buf.String() != want {
		t.Fatalf("notice %q", buf.String())
	}
	buf.Reset()
	reset("v0.1.24")
	noteNewerCLITo(&buf)
	if buf.Len() != 0 {
		t.Fatalf("second notice for the same release: %q", buf.String())
	}
	reset("v0.1.25")
	noteNewerCLITo(&buf)
	if !strings.Contains(buf.String(), "v0.1.25") {
		t.Fatalf("no notice for a newer release: %q", buf.String())
	}
	buf.Reset()
	t.Setenv(envInGuest, "1")
	reset("v0.1.26")
	noteNewerCLITo(&buf)
	if buf.Len() != 0 {
		t.Fatalf("notice on a machine: %q", buf.String())
	}
}

// Refusals of the argument count name what the command takes, and for a
// command that takes PROJECT only as --project, the line that works
// (DECISIONS I-628).
func TestArgRefusalsNameTheFix(t *testing.T) {
	root := docsRoot()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"secrets", "set", "todo-app", "FOO"}, "repose secrets set takes one NAME, got 2 arguments: todo-app FOO. A project goes in --project: repose secrets set FOO --project todo-app"},
		{[]string{"open", "todo-app", "80"}, "repose open takes at most one [LOCAL:]PORT, got 2 arguments: todo-app 80. A project goes in --project: repose open 80 --project todo-app"},
		{[]string{"config", "add"}, "repose config add takes one or more package names, got no arguments"},
		{[]string{"ssh", "todo-app", "uname"}, "repose ssh takes at most one PROJECT and no command, got 2 arguments: todo-app uname. To run a command: repose exec todo-app uname"},
	}
	for _, c := range cases {
		cmd, rest, err := root.Find(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		err = cmd.Args(cmd, rest)
		if err == nil || !isUsage(err) || err.Error() != c.want {
			t.Errorf("%v: %v\nwant %q", c.args, err, c.want)
		}
	}
}

// pflag reads a backticked word in a flag's usage as its value name, so
// the backticks hold one word, never a command (C2).
func TestFlagValueNamesAreOneWord(t *testing.T) {
	walkCommands(docsRoot(), func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if name, _ := pflag.UnquoteUsage(f); strings.ContainsAny(name, " `") {
				t.Errorf("%s --%s: value name %q", c.CommandPath(), f.Name, name)
			}
		})
	})
}

// repose sync has no --no-sync, so its refusals do not name it.
func TestSyncPrecheckNamesNoSyncOnlyForRun(t *testing.T) {
	dir := t.TempDir()
	if err := syncPrecheck(dir, false); err == nil || strings.Contains(err.Error(), "--no-sync") {
		t.Errorf("sync: %v", err)
	}
	if err := syncPrecheck(dir, true); err == nil || !strings.Contains(err.Error(), "or pass --no-sync.") {
		t.Errorf("run: %v", err)
	}
}

// login --status names the account; logged out it exits 3 (I-627).
func TestLoginStatus(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	var out bytes.Buffer
	e := &Env{Out: &out, Cfg: Config{APIURL: fake.URL() + "/v1"}, Client: newClient(fake.URL()+"/v1", staticToken("t"))}
	if err := loginStatus(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(fake.URL(), "http://")
	if got := out.String(); !strings.Contains(got, " on "+host+", ") || strings.Count(got, "\n") != 1 {
		t.Errorf("status %q", got)
	}
	e.Client = newClient(fake.URL()+"/v1", notLoggedInSource{})
	var errOut bytes.Buffer
	if code := exitCodeFor(loginStatus(context.Background(), e), &errOut); code != ExitNotLoggedIn || errOut.String() != "Not logged in.\n" {
		t.Errorf("logged out: exit %d %q", code, errOut.String())
	}
}

// logout says what it revoked; a failed revoke says so and exits 1.
func TestLogoutSaysWhatItRevoked(t *testing.T) {
	withHome(t)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	cfg := defaultConfig()
	cfg.APIURL = fake.URL() + "/v1"
	dir := t.TempDir()
	seed := func() {
		if err := saveCredentials(dir, Credentials{AccessToken: "t", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour), LogtoIssuer: cfg.LogtoIssuer}); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	var out bytes.Buffer
	if err := runLogout(context.Background(), dir, cfg, http.DefaultClient, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Logged out. Your SSH certificates are revoked;") {
		t.Errorf("logout said %q", out.String())
	}
	out.Reset()
	if err := runLogout(context.Background(), dir, cfg, http.DefaultClient, false, &out); err != nil || out.String() != "Not logged in.\n" {
		t.Errorf("second logout: %v %q", err, out.String())
	}
	seed()
	fake.FailNext("POST", "/v1/certs/revoke", "internal")
	err := runLogout(context.Background(), dir, cfg, http.DefaultClient, false, &out)
	var errOut bytes.Buffer
	if code := exitCodeFor(err, &errOut); code != ExitGeneric || !strings.Contains(errOut.String(), "the SSH certificates were not revoked") || !strings.Contains(errOut.String(), "within 24 hours") {
		t.Errorf("failed revoke: exit %d %q", code, errOut.String())
	}
	if _, ok, _ := loadCredentials(dir); ok {
		t.Error("credentials kept after a logout whose revoke failed")
	}
	_ = os.Remove(dir)
}

// Tab completion and the hidden helpers that ssh and the session run
// throw their stderr away, so the once-per-release notice waits for a
// command the user typed (I-631).
func TestNoticeAfterSkipsCompletion(t *testing.T) {
	root := newRootCmd("v0.0.1")
	// cobra adds these two while it executes.
	root.AddCommand(&cobra.Command{Use: cobra.ShellCompRequestCmd}, &cobra.Command{Use: cobra.ShellCompNoDescRequestCmd})
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"ls"}, true},
		{[]string{"__complete", "attach", ""}, false},
		{[]string{"__completeNoDesc", "ls", ""}, false},
		{[]string{"ssh-prepare", "x"}, false},
	} {
		cmd, _, err := root.Find(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := noticeAfter(cmd); got != tc.want {
			t.Errorf("noticeAfter(%s) = %v, want %v", cmd.CommandPath(), got, tc.want)
		}
	}
	if !noticeAfter(nil) {
		t.Error("noticeAfter(nil)")
	}
}
