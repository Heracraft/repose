package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestParsePs(t *testing.T) {
	out := "1000000\n0\tbash\tbash\t999990\t0\n1\tclaude\tclaude\t998800\t1\n2\tcodex-2\tnode\t700000\t0\n"
	l, err := parsePs(out)
	if err != nil {
		t.Fatal(err)
	}
	ws := l.Windows
	if len(ws) != 3 || ws[1].Name != "claude" || !ws[1].Current || ws[0].Current || ws[2].Command != "node" {
		t.Fatalf("parsePs = %+v", ws)
	}
	for i, want := range []string{"now", "20m ago", "3d ago"} {
		if got := idleAgo(ws[i].IdleSecs); got != want {
			t.Errorf("window %d: %q, want %q", i, got, want)
		}
	}
	if _, err := parsePs("not a clock\n"); err == nil {
		t.Fatal("a bad clock line parsed")
	}
}

// I-274: `repose ps` lists the session's windows in one ssh, marks the
// current one, and shows the program's name only.
func TestPsListsWindows(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n agent 'sleep 301' && tmux select-window -t "+testSlug+":agent", nil); err != nil {
		t.Fatal(err)
	}
	out := &discardWriter{}
	f.env.Out = out
	if err := PsCmd(ctx, f.env, testSlug); err != nil {
		t.Fatalf("ps: %v", err)
	}
	got := out.buf.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "WINDOW") || !strings.Contains(lines[0], "COMMAND") || !strings.Contains(lines[0], "STATE") || !strings.Contains(lines[0], "TREE") || !strings.Contains(lines[0], "ACTIVE") {
		t.Fatalf("ps output:\n%s", got)
	}
	var agent string
	for _, l := range lines[1:] {
		if strings.Contains(l, ":agent") {
			agent = l
		}
	}
	if !strings.Contains(agent, ":agent*") || !strings.Contains(agent, "sleep") || !strings.Contains(agent, "now") || !strings.Contains(agent, " ~ ") {
		t.Fatalf("agent window line %q in:\n%s", agent, got)
	}
	if strings.Contains(got, "301") {
		t.Fatalf("ps printed a process argument:\n%s", got)
	}

	out.buf.Reset()
	f.env.Quiet = true
	if err := PsCmd(ctx, f.env, testSlug); err != nil {
		t.Fatal(err)
	}
	if names := nonEmptyLines(out.buf.String()); len(names) != len(lines)-1 || !strings.Contains(out.buf.String(), "agent\n") {
		t.Fatalf("ps -q:\n%s", out.buf.String())
	}

	out.buf.Reset()
	f.env.Quiet, f.env.JSON = false, true
	if err := PsCmd(ctx, f.env, testSlug); err != nil {
		t.Fatal(err)
	}
	var ws []map[string]any
	if err := json.Unmarshal([]byte(out.buf.String()), &ws); err != nil || len(ws) != len(lines)-1 {
		t.Fatalf("ps --json: %v\n%s", err, out.buf.String())
	}
	for _, k := range []string{"name", "agent", "command", "state", "tree", "focused", "idle_seconds", "index", "current", "activity"} {
		if _, ok := ws[0][k]; !ok {
			t.Fatalf("ps --json lacks %q: %v", k, ws[0])
		}
	}
}

// I-606: a window's last lines, by name or number, without attaching;
// a window the session lacks is exit 2 naming ps; -n alone heads each
// window as tail(1) does.
func TestPsTailsAWindow(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runSSH(ctx, f.target, "tmux new-window -d -t "+testSlug+" -n agent-x 'for i in 1 2 3 4 5; do echo out$i; done; exec sleep 300'", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	out := &discardWriter{}
	f.env.Out = out
	if err := PsCmdWith(ctx, f.env, PsOptions{ProjectArg: testSlug, Window: "agent-x", Lines: 2}); err != nil {
		t.Fatalf("ps WINDOW: %v", err)
	}
	if got := out.buf.String(); got != "out4\nout5\n" {
		t.Fatalf("ps agent-x -n 2 = %q", got)
	}
	err := PsCmdWith(ctx, f.env, PsOptions{ProjectArg: testSlug, Window: "agent"})
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitUsage || !strings.Contains(ee.msg, "has no window agent.") {
		t.Fatalf("a prefix of a window's name: %v", err)
	}
	out.buf.Reset()
	if err := PsCmdWith(ctx, f.env, PsOptions{ProjectArg: testSlug, Lines: 1}); err != nil {
		t.Fatalf("ps -n 1: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, ":agent-x <==\nout5\n") || !strings.HasPrefix(got, "==> 0:") {
		t.Fatalf("ps -n 1:\n%s", got)
	}
}

func TestPsLastLines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want []string
	}{
		{"a\nb  \nc\n\n\n   \n", 2, []string{"b", "c"}},
		{"a\n", 5, []string{"a"}},
		{"\n\n", 3, nil},
	} {
		got := psLastLines(tc.in, tc.n)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("psLastLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestTreeOf(t *testing.T) {
	const home, co = "/home/dev", "/home/dev/todo-app"
	for path, want := range map[string]string{
		"/home/dev/todo-app":              "checkout",
		"/home/dev/todo-app/src":          "checkout/src",
		"/home/dev/todo-app-worktree-2":   "worktree-2",
		"/home/dev/todo-app-worktree-2/x": "worktree-2/x",
		"/home/dev/todo-app-worktree-x":   "~/todo-app-worktree-x",
		"/home/dev/api":                   "~/api",
		"/home/dev":                       "~",
		"/tmp":                            "/tmp",
		"":                                "",
	} {
		if got := treeOf(path, home, co); got != want {
			t.Errorf("treeOf(%q) = %q, want %q", path, got, want)
		}
	}
	if got := treeOf("/home/dev", "/home/dev", "/home/dev"); got != "~" {
		t.Errorf("no checkout: %q", got)
	}
}

// I-606: STATE comes from the window's @repose-state, else a fresh api
// sample for that window; a window with no agent has none; the JSON is
// one shape with null where tmux or herdr cannot say.
func TestPsRows(t *testing.T) {
	now := time.Unix(1000000, 0)
	fresh, stale := now.Add(-30*time.Second), now.Add(-5*time.Minute)
	l, err := parsePs("1000000\n#home /home/dev\n#co /home/dev/todo-app\n" +
		"0\tshell\tbash\t999990\t0\t\t/home/dev/todo-app\n" +
		"1\tclaude\tclaude\t999990\t1\tworking\t/home/dev/todo-app\n" +
		"2\tclaude-2\tclaude\t999000\t0\t\t/home/dev/todo-app-worktree-1\n" +
		"3\tapi/codex\tcodex\t999000\t0\t\t/home/dev/api\n" +
		"4\tclaude-3\tbash\t999000\t0\tneeds_input\t/home/dev/todo-app\n")
	if err != nil {
		t.Fatal(err)
	}
	sig := &Signals{SampledAt: &fresh, Agents: []AgentSignal{{Agent: "claude", Window: "claude-2", State: "needs_input"}}}
	rows := psRowsTmux(l, sig, now)
	type row struct{ agent, state, tree string }
	get := func(r PsRow) row {
		var x row
		if r.Agent != nil {
			x.agent = *r.Agent
		}
		if r.AgentState != nil {
			x.state = *r.AgentState
			if r.State == nil || *r.State != x.state {
				t.Errorf("tmux row %s: state differs from agent_state", r.Name)
			}
		}
		x.tree = r.Tree
		return x
	}
	want := []row{{"", "", "checkout"}, {"claude", "working", "checkout"}, {"claude", "needs_input", "worktree-1"}, {"codex", "unknown", "~/api"}, {"claude", "unknown", "checkout"}}
	for i, w := range want {
		if got := get(rows[i]); got != w {
			t.Errorf("row %d = %+v, want %+v", i, got, w)
		}
	}
	sig.SampledAt = &stale
	if got := get(psRowsTmux(l, sig, now)[2]); got.state != "unknown" {
		t.Errorf("a stale sample gave %q", got.state)
	}
	if stateCell("unknown") != "-" || stateCell("") != "-" || stateCell("idle") != "idle" {
		t.Error("stateCell")
	}
}

func TestPsJSONOneShape(t *testing.T) {
	keys := func(v any) []string {
		b, _ := json.Marshal(v)
		var m []map[string]any
		_ = json.Unmarshal(b, &m)
		var ks []string
		for k := range m[0] {
			ks = append(ks, k)
		}
		return ks
	}
	l, _ := parsePs("1\n0\tshell\tbash\t1\t1\t\t/x\n")
	tm := keys(psRowsTmux(l, nil, time.Unix(1, 0)))
	hd := keys(psRowsHerdr([]PsAgent{{Workspace: "checkout", Agent: "claude", Name: "claude", State: "blocked"}}))
	for _, k := range []string{"name", "agent", "command", "agent_state", "state", "tree", "focused", "idle_seconds"} {
		if !slices.Contains(tm, k) || !slices.Contains(hd, k) {
			t.Errorf("%q: tmux %v herdr %v", k, tm, hd)
		}
	}
	b, _ := json.Marshal(psRowsHerdr([]PsAgent{{Workspace: "checkout", Agent: "claude", Name: "claude", State: "blocked"}}))
	// herdr's own value stays in state for one release (I-631).
	if !strings.Contains(string(b), `"agent_state":"needs_input"`) || !strings.Contains(string(b), `"state":"blocked"`) || !strings.Contains(string(b), `"command":null`) || !strings.Contains(string(b), `"workspace":"checkout"`) {
		t.Fatalf("herdr row %s", b)
	}
}

// I-275: exec runs in the checkout, streams output, passes each argument
// through unchanged, gives no stdin without -i, and exits with the
// command's code.
func TestExecRunsInTheCheckout(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	out, errOut := &discardWriter{}, &discardWriter{}
	f.env.Out, f.env.ErrOut = out, errOut
	err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Command: []string{"sh", "-c", `pwd; printf '%s|' "$@"; echo; cat; echo stdin-done`, "sh", "a b", "$HOME", "it's"}}, strings.NewReader("typed input\n"))
	if err != nil {
		t.Fatalf("exec: %v (stderr %s)", err, errOut.buf.String())
	}
	got := out.buf.String()
	if !strings.Contains(got, "/"+testSlug+"\n") {
		t.Errorf("not run in the checkout:\n%s", got)
	}
	if !strings.Contains(got, "a b|$HOME|it's|") {
		t.Errorf("arguments changed on the way:\n%s", got)
	}
	if strings.Contains(got, "typed input") || !strings.Contains(got, "stdin-done") {
		t.Errorf("stdin reached the command without -i:\n%s", got)
	}

	out.buf.Reset()
	if err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Interactive: true, Command: []string{"cat"}}, strings.NewReader("typed input\n")); err != nil {
		t.Fatal(err)
	}
	if out.buf.String() != "typed input\n" {
		t.Errorf("-i did not pass stdin: %q", out.buf.String())
	}

	// I-411: with no --, a first word naming a project is PROJECT; alone,
	// it is refused rather than run as a command.
	out.buf.Reset()
	if err := ExecCmd(ctx, f.env, ExecOptions{MayNameProject: true, Command: []string{testSlug, "pwd"}}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.buf.String(), "/"+testSlug+"\n") {
		t.Errorf("the project word was not taken as PROJECT: %q", out.buf.String())
	}
	err = ExecCmd(ctx, f.env, ExecOptions{MayNameProject: true, Command: []string{testSlug}}, nil)
	if ee, ok := err.(*exitError); !ok || ee.code != ExitUsage || !strings.Contains(ee.msg, "repose exec "+testSlug+" COMMAND") {
		t.Fatalf("a lone project word came back as %v", err)
	}

	err = ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Command: []string{"sh", "-c", "exit 42"}}, nil)
	if ee, ok := err.(*exitError); !ok || ee.code != 42 || ee.msg != "" {
		t.Fatalf("exit 42 came back as %v", err)
	}
	err = ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Command: []string{"no-such-command-here"}}, nil)
	if ee, ok := err.(*exitError); !ok || ee.code != 127 {
		t.Fatalf("a missing command came back as %v", err)
	}
}

// TestExecScriptGolden holds testdata/exec-script.sh, which the guest VM
// test guest-devshell runs over ssh, to what `repose exec todo-app -- sh
// -c '...'` sends. -update rewrites it.
func TestExecScriptGolden(t *testing.T) {
	got := execScript("todo-app", "", []string{"sh", "-c", `echo "flake=$REPOSE_FLAKE_PROBE project=$REPOSE_PROJECT pwd=$PWD"; flake-tool`}) + "\n"
	const path = "testdata/exec-script.sh"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is stale (-update rewrites it):\n got %s\nwant %s", path, got, want)
	}
}

// I-411: the -- is optional. Only a "--" after one word and exec's own
// flags separates PROJECT; any other belongs to the command.
func TestExecSeparated(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		project string
		command []string
		ok      bool
		i, tty  bool
	}{
		{args: []string{"todo-app", "--", "git", "log"}, project: "todo-app", command: []string{"git", "log"}, ok: true},
		{args: []string{"todo-app", "-it", "--", "psql"}, project: "todo-app", command: []string{"psql"}, ok: true, i: true, tty: true},
		{args: []string{"todo-app", "-t", "--"}, project: "todo-app", command: []string{}, ok: true, tty: true},
		{args: []string{"grep", "ADMIN", "prod.env"}},
		{args: []string{"git", "log", "--", "main.go"}},
		{args: []string{"grep", "-n", "x", "--", "y"}},
	} {
		var o ExecOptions
		p, c, ok := execSeparated(tc.args, &o)
		if ok != tc.ok || p != tc.project || strings.Join(c, " ") != strings.Join(tc.command, " ") || o.Interactive != tc.i || o.TTY != tc.tty {
			t.Errorf("%q: got %q %q %v i=%v t=%v", tc.args, p, c, ok, o.Interactive, o.TTY)
		}
	}
}

// I-411: the command's own flags reach it, not exec's parser, and a
// missing command is still a usage error.
func TestExecCommandFlagsPassThrough(t *testing.T) {
	stop := errors.New("parsed")
	for _, args := range [][]string{
		{"grep", "-n", "ADMIN_PASSWORD", "prod.env"},
		{"-it", "psql", "-c", "select 1"},
		{"--", "npm", "test"},
		{"todo-app", "--", "ls", "-la"},
	} {
		cmd := newExecCmd(func() (*Env, error) { return nil, stop }, &globalFlags{})
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); !errors.Is(err, stop) {
			t.Errorf("%q: %v", args, err)
		}
	}
	for _, args := range [][]string{{}, {"--"}, {"todo-app", "--"}} {
		cmd := newExecCmd(func() (*Env, error) { return nil, stop }, &globalFlags{})
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil || errors.Is(err, stop) {
			t.Errorf("%q ran: %v", args, err)
		}
	}
}

func TestWorkdirShell(t *testing.T) {
	for dir, want := range map[string]string{
		"worktree-2":     `"$repose_co"'-worktree-2'`,
		"worktree-2/src": `"$repose_co"'-worktree-2'/'src'`,
		"worktree-x":     `"$repose_co"/'worktree-x'`,
		"checkout":       `"$repose_co"`,
		"checkout/web":   `"$repose_co"/'web'`,
		"~":              `"$HOME"`,
		"~/api":          `"$HOME"/'api'`,
		"/tmp/x y":       `'/tmp/x y'`,
		"src/lib":        `"$repose_co"/'src/lib'`,
	} {
		if got := workdirShell(dir); got != want {
			t.Errorf("workdirShell(%q) = %s, want %s", dir, got, want)
		}
	}
	var o ExecOptions
	if p, c, ok := execSeparated([]string{"todo-app", "--workdir", "worktree-1", "-i", "--", "npm", "test"}, &o); !ok || p != "todo-app" || strings.Join(c, " ") != "npm test" || o.Workdir != "worktree-1" || !o.Interactive {
		t.Fatalf("PROJECT --workdir DIR -- COMMAND: %q %q %v %+v", p, c, ok, o)
	}
}

// I-608: exec --workdir runs in a worktree beside the checkout; one that
// is not there exits 2 with one line and runs nothing.
func TestExecWorkdir(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = runSSH(context.Background(), f.target, "tmux kill-server", nil) })
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.guestRepo()+"-worktree-1/src", 0o755); err != nil {
		t.Fatal(err)
	}
	out, errOut := &discardWriter{}, &discardWriter{}
	f.env.Out, f.env.ErrOut = out, errOut
	if err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Workdir: "worktree-1/src", Command: []string{"pwd"}}, nil); err != nil {
		t.Fatalf("exec --workdir: %v (%s)", err, errOut.buf.String())
	}
	if got := strings.TrimSpace(out.buf.String()); !strings.HasSuffix(got, "-worktree-1/src") {
		t.Fatalf("ran in %q", got)
	}
	out.buf.Reset()
	err := ExecCmd(ctx, f.env, ExecOptions{ProjectArg: testSlug, Workdir: "worktree-9", Command: []string{"echo", "ran"}}, nil)
	if exitCodeOf(err) != ExitUsage || !strings.Contains(errOut.buf.String(), testSlug+" has no folder worktree-9.") || strings.Contains(out.buf.String(), "ran") {
		t.Fatalf("a missing worktree: %v %q %q", err, errOut.buf.String(), out.buf.String())
	}
}

// `repose ps claude-2` and `repose attach claude-2` in a checkout: a word
// that names no project is a window of the folder's project (I-631).
func TestOneWordIsAWindowWhereAProjectResolves(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	f := newRunFixture(t, fake)
	ctx := context.Background()
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	type call struct{ project, window string }
	var calls []call
	fn := func(project, window string) error {
		calls = append(calls, call{project, window})
		if project != "" && project != testSlug {
			return errNoSuchProject(project)
		}
		return nil
	}
	if err := orWindow(ctx, f.env, []string{"claude-2"}, "", "", fn); err != nil || len(calls) != 2 || calls[1] != (call{"", "claude-2"}) {
		t.Fatalf("in the checkout: %v %+v", err, calls)
	}
	calls = nil
	if err := orWindow(ctx, f.env, []string{testSlug}, "", "", fn); err != nil || len(calls) != 1 {
		t.Fatalf("a project's name: %v %+v", err, calls)
	}
	// With -w the word is the project, and stays one.
	calls = nil
	if err := orWindow(ctx, f.env, []string{"nope"}, "2", "", fn); exitCode(err) != ExitProjectNotFound || len(calls) != 1 {
		t.Fatalf("-w: %v %+v", err, calls)
	}
	// Where no project resolves, the word is still a project nobody has.
	e, _, _ := freshEnv(f.env, t.TempDir())
	calls = nil
	if err := orWindow(ctx, e, []string{"claude-2"}, "", "", fn); exitCode(err) != ExitProjectNotFound || len(calls) != 1 {
		t.Fatalf("outside a checkout: %v %+v", err, calls)
	}
}
