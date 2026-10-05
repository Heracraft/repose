package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

func TestParsePs(t *testing.T) {
	out := "1000000\n0\tbash\tbash\t999990\t0\n1\tclaude\tclaude\t998800\t1\n2\tcodex-2\tnode\t700000\t0\n"
	ws, err := parsePs(out)
	if err != nil {
		t.Fatal(err)
	}
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
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "WINDOW") || !strings.Contains(lines[0], "COMMAND") || !strings.Contains(lines[0], "ACTIVE") {
		t.Fatalf("ps output:\n%s", got)
	}
	var agent string
	for _, l := range lines[1:] {
		if strings.Contains(l, ":agent") {
			agent = l
		}
	}
	if !strings.Contains(agent, ":agent*") || !strings.Contains(agent, "sleep") || !strings.Contains(agent, "now") {
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
	for _, k := range []string{"index", "name", "command", "current", "activity", "idle_seconds"} {
		if _, ok := ws[0][k]; !ok {
			t.Fatalf("ps --json lacks %q: %v", k, ws[0])
		}
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
