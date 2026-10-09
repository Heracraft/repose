package cli

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// The CLI's grammar (DECISIONS I-619..I-622).

// Every --yes has -y, so the flag learned on rm works on the next command.
func TestEveryYesHasY(t *testing.T) {
	walkCommands(newRootCmd("dev"), func(c *cobra.Command) {
		if f := c.LocalNonPersistentFlags().Lookup("yes"); f != nil && f.Shorthand != "y" {
			t.Errorf("%s --yes has no -y", c.CommandPath())
		}
	})
}

// One name per idea: a visible flag spelled one way on one command is
// not spelled another way for the same thing elsewhere.
func TestOneSpellingPerIdea(t *testing.T) {
	banned := map[string]string{"no-open": "--no-browser", "as-new": "--as", "remove": "repose mcp rm", "local-port": "LOCAL:PORT"}
	walkCommands(newRootCmd("dev"), func(c *cobra.Command) {
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if use, ok := banned[f.Name]; ok && !f.Hidden {
				t.Errorf("%s --%s is visible; it is %s now", c.CommandPath(), f.Name, use)
			}
		})
	})
	bridge, _, _ := newRootCmd("dev").Find([]string{"browser", "bridge"})
	if f := bridge.Flags().Lookup("no-browser"); f == nil || !f.Hidden {
		t.Error("browser bridge --no-browser should be the hidden old name of --no-inspect")
	}
}

// --project on a command that acts on no project is a usage error, where
// it used to be ignored.
func TestProjectFlagRefusedOnAccountCommands(t *testing.T) {
	for _, args := range [][]string{
		{"ls", "--project", "x"},
		{"notify", "--project", "x"},
		{"notify", "set", "--project", "x", "--email", "on"},
		{"version", "--project", "x"},
	} {
		root := newRootCmd("dev")
		root.SetArgs(args)
		err := root.ExecuteContext(context.Background())
		var ue cobraUsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "takes no --project") {
			t.Errorf("repose %s: %v, want a usage error", strings.Join(args, " "), err)
		}
	}
}

// A group noun alone runs its listing, and a word after it is still a
// mistyped subcommand.
func TestBareGroupsRunTheirListing(t *testing.T) {
	root := newRootCmd("dev")
	for group, sub := range map[string]string{"secrets": "list", "snapshots": "list", "mcp": "list", "config": "show"} {
		c, _, err := root.Find([]string{group})
		if err != nil || c.Annotations[bareRunsKey] != sub || !c.Runnable() {
			t.Errorf("repose %s: runs %q, want %q", group, c.Annotations[bareRunsKey], sub)
		}
	}
	msg := unknownCommand(newRootCmd("dev"), []string{"secrets", "lsit"}, nil)
	if !strings.Contains(msg, `unknown command "lsit"`) || !strings.Contains(msg, "repose secrets list") {
		t.Errorf("repose secrets lsit: %q", msg)
	}
	// A word that is one of the user's projects gets the subcommand that
	// takes it (I-633); a typo of a subcommand is still a typo, project
	// or not, and the lookup is not asked for it.
	isProject := func(w string) bool { return w == "todo-app" }
	for _, c := range []struct{ args, want string }{
		{"snapshots todo-app", "todo-app is a project: `repose snapshots list todo-app`."},
		{"config todo-app", "todo-app is a project: `repose config show todo-app`."},
		{"mcp todo-app", "todo-app is a project: `repose mcp list todo-app`."},
		{"secrets todo-app", "todo-app is a project: `repose secrets list todo-app`."},
	} {
		if got := unknownCommand(newRootCmd("dev"), strings.Fields(c.args), isProject); got != c.want {
			t.Errorf("repose %s: %q, want %q", c.args, got, c.want)
		}
	}
	if got := unknownCommand(newRootCmd("dev"), []string{"snapshots", "lsit"}, func(string) bool { t.Error("asked about a typo"); return true }); !strings.Contains(got, `unknown command "lsit"`) {
		t.Errorf("repose snapshots lsit: %q", got)
	}
	if got := unknownCommand(newRootCmd("dev"), []string{"snapshots", "other"}, isProject); !strings.Contains(got, `unknown command "other"`) {
		t.Errorf("repose snapshots other: %q", got)
	}
	// browser takes [PROJECT], so a word after it is a project.
	if msg := unknownCommand(newRootCmd("dev"), []string{"browser", "todo-app"}, nil); msg != "" {
		t.Errorf("repose browser todo-app: %q", msg)
	}
	// Each rm answers to remove.
	for _, path := range [][]string{{"secrets", "remove"}, {"mcp", "remove"}, {"config", "rm"}} {
		if c, _, err := newRootCmd("dev").Find(path); err != nil || (c.Name() != "rm" && c.Name() != "remove") {
			t.Errorf("repose %s: %v", strings.Join(path, " "), err)
		}
	}
}

func TestParseOpenPorts(t *testing.T) {
	for _, tc := range []struct {
		arg         string
		local, port int
		bad         bool
	}{
		{"3000", 0, 3000, false},
		{"8080:3000", 8080, 3000, false},
		{"5432:5432", 5432, 5432, false},
		{"abc", 0, 0, true},
		{"0", 0, 0, true},
		{"8080:", 0, 0, true},
		{":3000", 0, 0, true},
		{"70000:3000", 0, 0, true},
		{"todo-app:3000", 0, 0, true},
	} {
		l, p, err := parseOpenPorts(tc.arg)
		if (err != nil) != tc.bad || l != tc.local || p != tc.port {
			t.Errorf("parseOpenPorts(%q) = %d, %d, %v", tc.arg, l, p, err)
		}
	}
}

func TestCheckSecretName(t *testing.T) {
	for name, want := range map[string]string{
		"STRIPE_KEY":           "",
		"STRIPE_KEY=sk_live":   "stays out of your shell history",
		"stripe":               "must match",
		"user_ca.pub":          "must match",
		"BASH_ENV":             "reserved",
		"A=b=c":                "stays out of your shell history",
		"lower=x":              "must match",
		"ssh_host_ed25519_key": "must match",
	} {
		err := checkSecretName(name)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want) || exitCode(err) != ExitUsage) {
			t.Errorf("checkSecretName(%q) = %v, want %q", name, err, want)
		}
	}
	// The value of NAME=VALUE is never repeated back.
	if err := checkSecretName("TOKEN=hunter2"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("NAME=VALUE: %v", err)
	}
}

func TestReadPipedSecret(t *testing.T) {
	for in, want := range map[string]string{
		"sk_live_1\n":    "sk_live_1",
		"sk_live_1\r\n":  "sk_live_1",
		"sk_live_1":      "sk_live_1",
		"line1\nline2\n": "line1\nline2",
		"ends\n\n":       "ends\n",
		"  spaced  \n":   "  spaced  ",
	} {
		got, err := readPipedSecret(strings.NewReader(in), "K")
		if err != nil || string(got) != want {
			t.Errorf("readPipedSecret(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := readPipedSecret(strings.NewReader("\n"), "K"); err == nil || exitCode(err) != ExitUsage {
		t.Errorf("empty pipe: %v", err)
	}
}

func TestMatchID(t *testing.T) {
	ids := []string{"0199b1c2-0000-7000-8000-00000000aa01", "0199b1c2-0000-7000-8000-00000000bb02", "0188ffff-0000-7000-8000-0000000cc003"}
	for part, want := range map[string]int{
		"0000aa01":                             1,
		"aa01":                                 1,
		"0199b1c2":                             2,
		"0188":                                 1,
		"01":                                   0, // too short
		"zzzz":                                 0,
		"0199b1c2-0000-7000-8000-00000000bb02": 1,
	} {
		if got := matchID(ids, part); len(got) != want {
			t.Errorf("matchID(%q) = %v, want %d", part, got, want)
		}
	}
}

// snapshots restore takes the end of an id as the table shows it, and an
// id it lacks is a usage error naming the list.
func TestResolveSnapshotID(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	opID, err := e.Client.CreateSnapshot(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waitOp(ctx, e.Client, p.ID, opID, nil); err != nil {
		t.Fatal(err)
	}
	snaps, err := e.Client.ListSnapshots(ctx, p.ID)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	full := snaps[0].ID
	got, err := resolveSnapshotID(ctx, e, "", shortID(full))
	if err != nil || got != full {
		t.Fatalf("resolve %s = %q, %v; want %s", shortID(full), got, err, full)
	}
	_, err = resolveSnapshotID(ctx, e, "", "ffffffff")
	if exitCode(err) != ExitUsage || !strings.Contains(err.Error(), "todo-app has no snapshot ffffffff. `repose snapshots list todo-app` lists them.") {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestFindRevision(t *testing.T) {
	revs := []Revision{{ID: "0199b1c2-0000-7000-8000-00009c41d2e7", Status: "applied"}, {ID: "0199b1c2-0000-7000-8000-000011112222", Status: "failed"}}
	if r, err := findRevision(revs, "p", "9c41d2e7"); err != nil || r.ID != revs[0].ID {
		t.Errorf("by end: %v %v", r, err)
	}
	if _, err := findRevision(revs, "p", "0199b1c2"); err == nil || !strings.Contains(err.Error(), "names 2 revisions") {
		t.Errorf("ambiguous start: %v", err)
	}
	if _, err := findRevision(revs, "p", "deadbeef"); err == nil || !strings.Contains(err.Error(), "p has no revision deadbeef") {
		t.Errorf("missing: %v", err)
	}
	if shortRev(revs[0].ID) != "9c41d2e7" {
		t.Errorf("shortRev = %q, want the id's random end", shortRev(revs[0].ID))
	}
}

func TestConfigRevisionsAndApplyRevision(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	if err := StartCmd(ctx, e, p.ID); err != nil {
		t.Fatal(err)
	}
	revs, err := e.Client.ListRevisions(ctx, p.ID)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v %v", revs, err)
	}
	out := &discardWriter{}
	e.Out = out
	if err := ConfigRevisionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasPrefix(got, "REVISION") || !strings.Contains(got, shortRev(revs[0].ID)) {
		t.Fatalf("revisions table: %q", got)
	}
	out.buf.Reset()
	if err := ConfigApplyRevisionCmd(ctx, e, "", shortRev(revs[0].ID)); err != nil {
		t.Fatalf("apply --revision: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, "Applied revision "+shortRev(revs[0].ID)) {
		t.Fatalf("apply --revision printed %q", got)
	}
}

func TestPersonalSwitch(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if err := PersonalSwitchCmd(ctx, e, "", false); err != nil {
		t.Fatal(err)
	}
	if err := PersonalSwitchCmd(ctx, e, "", false); err != nil {
		t.Fatal(err)
	}
	if err := PersonalSwitchCmd(ctx, e, "", true); err != nil {
		t.Fatal(err)
	}
	want := "machine.nix is off for todo-app; the machine switches without it in the background.\n" +
		"machine.nix is already off for todo-app.\n" +
		"machine.nix is on for todo-app; the machine switches with it in the background.\n"
	if got := out.buf.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSecretsLinesNameTheProject(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if err := SecretsSetCmd(ctx, e, "", "STRIPE_KEY", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := SecretsRmCmd(ctx, e, "", "STRIPE_KEY"); err != nil {
		t.Fatal(err)
	}
	want := "Set STRIPE_KEY on todo-app; the machine gets it at its next start.\nRemoved STRIPE_KEY from todo-app.\n"
	if got := out.buf.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// One it does not have reads as mcp rm's line, exit 1 (I-633).
	err := SecretsRmCmd(ctx, e, "", "STRIPE_KEY")
	if ee, ok := err.(*exitError); !ok || ee.code != ExitGeneric || ee.msg != "todo-app has no secret STRIPE_KEY." {
		t.Fatalf("second rm: %v", err)
	}
}

func TestNotifyShowAndTest(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out = out
	if err := NotifyShowCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "email: on, to dev@example.com\nntfy: off\n" {
		t.Fatalf("notify: %q", got)
	}
	// --ntfy off clears it, as none did.
	off, url := "off", "https://ntfy.sh/fail-topic"
	out.buf.Reset()
	if err := NotifySetCmd(ctx, e, nil, &url); err != nil {
		t.Fatal(err)
	}
	if err := NotifyTestCmd(ctx, e); exitCode(err) != ExitGeneric {
		t.Fatalf("a failed ntfy exits %d, want 1", exitCode(err))
	}
	if got := out.buf.String(); !strings.HasSuffix(got, "email: sent\nntfy: failed: the server answered 403\n") {
		t.Fatalf("notify test: %q", got)
	}
	out.buf.Reset()
	if err := NotifySetCmd(ctx, e, nil, &off); err != nil {
		t.Fatal(err)
	}
	if err := NotifyTestCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.HasSuffix(got, "ntfy: off\nemail: sent\nntfy: off\n") {
		t.Fatalf("after --ntfy off: %q", got)
	}
}

func TestSetupLine(t *testing.T) {
	if l := setupLine(&Project{AgentDefault: "claude"}, ""); l != "" {
		t.Errorf("defaults: %q", l)
	}
	if l := setupLine(&Project{AgentDefault: "codex", PersonalOptOut: true, HoldBaseUpdates: true}, "claude"); l != "setup: agent codex, machine.nix off, base updates held" {
		t.Errorf("all set: %q", l)
	}
}

func TestForkRemotes(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	// No `repose` remote for the source: nothing is added.
	if got := addForkRemotes(root, "todo-app", []string{"todo-app-fork-1"}); got != nil {
		t.Fatalf("added %v without the source's remote", got)
	}
	git("remote", "add", "repose", "todo-app.repose:~/todo")
	git("remote", "add", "todo-app-fork-2", "git@github.com:me/other.git") // the user's own
	got := addForkRemotes(root, "todo-app", []string{"todo-app-fork-1", "todo-app-fork-2"})
	if len(got) != 1 || got[0] != "todo-app-fork-1" {
		t.Fatalf("added %v", got)
	}
	if u := git("config", "--get", "remote.todo-app-fork-1.url"); u != "todo-app-fork-1.repose:~/todo" {
		t.Fatalf("url %q", u)
	}
	if v := git("config", "--get", "remote.todo-app-fork-1.skipFetchAll"); v != "true" {
		t.Fatalf("skipFetchAll %q", v)
	}
	// Again: nothing new.
	if got := addForkRemotes(root, "todo-app", []string{"todo-app-fork-1"}); got != nil {
		t.Fatalf("added again: %v", got)
	}
	if forgetForkRemote(root, "todo-app-fork-2") {
		t.Fatal("removed the user's remote")
	}
	if !forgetForkRemote(root, "todo-app-fork-1") || remoteURLOf(root, "todo-app-fork-1") != "" {
		t.Fatal("did not remove the fork's remote")
	}
	if forgetForkRemote(root, "repose") {
		t.Fatal("forgetForkRemote touched the repose remote")
	}
}

// A command that refuses --project does not list it under Global Flags
// (I-631); one that takes it does.
func TestHelpHidesProjectWhereRefused(t *testing.T) {
	for args, want := range map[string]bool{"ls": false, "login": false, "notify set": false, "stop": true, "ps": true} {
		root := newRootCmd("test")
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append(strings.Fields(args), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatalf("%s --help: %v", args, err)
		}
		if got := strings.Contains(out.String(), "--project NAME"); got != want {
			t.Errorf("repose %s --help lists --project: %v, want %v\n%s", args, got, want, out.String())
		}
	}
}

// I-635: `run --agent X` without -p makes X the project's agent, at
// creation and on a project that exists, and status names the agent
// when it is not config.toml's default_agent.
func TestRunAgentWithoutPromptSticks(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Agent: "codex", SetAgent: true}, false); err != nil {
		t.Fatal(err)
	}
	p, err := findByName(ctx, f.env.Client, testSlug)
	if err != nil || p == nil || p.AgentDefault != "codex" {
		t.Fatalf("created with %+v (%v)", p, err)
	}
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true, Agent: "gemini", SetAgent: true}, false); err != nil {
		t.Fatal(err)
	}
	if p, _ = findByName(ctx, f.env.Client, testSlug); p.AgentDefault != "gemini" {
		t.Fatalf("after run --agent gemini: %q", p.AgentDefault)
	}
	if l := setupLine(&Project{AgentDefault: "claude"}, "codex"); l != "setup: agent claude" {
		t.Fatalf("claude under default_agent codex: %q", l)
	}
	if l := setupLine(&Project{AgentDefault: "codex"}, "codex"); l != "" {
		t.Fatalf("the default agent: %q", l)
	}
}
