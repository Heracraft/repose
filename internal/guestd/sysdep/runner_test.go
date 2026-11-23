package sysdep

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLookPathUsesThePATHTheCommandWillRunWith is the regression test for the
// bug the NixOS VM test found: guestd runs as a systemd unit whose PATH does
// not include /run/current-system/sw/bin, so resolving a command against
// guestd's own environment reported `setpriv`, `git` and `tmux` as missing on
// a guest that has all three.
func TestLookPathUsesThePATHTheCommandWillRunWith(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "only-here")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Not on the process PATH.
	_, err := LookPath("only-here", nil)
	if err == nil {
		t.Fatal("the command was found without being on any PATH")
	}
	// On the PATH the command will run with.
	got, err := LookPath("only-here", []string{"PATH=" + dir})
	if err != nil {
		t.Fatalf("look up: %v", err)
	}
	if got != tool {
		t.Fatalf("resolved to %q, want %q", got, tool)
	}
}

func TestLookPathTakesAPathAsWritten(t *testing.T) {
	got, err := LookPath("/absent/binary", nil)
	if err != nil {
		t.Fatalf("look up: %v", err)
	}
	if got != "/absent/binary" {
		t.Fatalf("resolved to %q", got)
	}
}

func TestLookPathFallsBackToTheGuestPATH(t *testing.T) {
	// Nothing in GuestPATH exists on this machine, so the fallback is
	// exercised by its error rather than a hit; what matters is that the
	// search reaches it rather than stopping at the process PATH.
	_, err := LookPath("definitely-not-a-real-binary-xyz", nil)
	if err == nil {
		t.Fatal("a name that exists nowhere was resolved")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCapsEachStreamAtTheTail(t *testing.T) {
	res, err := ExecRunner{}.Run(context.Background(), sysdepSpec())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Stdout) != 64 {
		t.Fatalf("stdout = %d bytes, want the 64-byte cap", len(res.Stdout))
	}
	if !res.Truncated {
		t.Fatal("Truncated was not set")
	}
	// The tail is what is kept, because the end of a failed command is the
	// part that says why.
	if !strings.HasSuffix(string(res.Stdout), "end") {
		t.Fatalf("stdout tail = %q", res.Stdout)
	}
}

func sysdepSpec() RunSpec {
	return RunSpec{
		Argv:      []string{"sh", "-c", "printf 'x%.0s' $(seq 1 500); printf end"},
		MaxOutput: 64,
	}
}

func TestRunReturnsExitCodeNotError(t *testing.T) {
	res, err := ExecRunner{}.Run(context.Background(), RunSpec{Argv: []string{"sh", "-c", "exit 7"}})
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", res.ExitCode)
	}
}

func TestRunKillsTheProcessGroupOnTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// A shell whose child outlives it: without the process-group kill the
	// context deadline would return while sleep kept the guest's CPU.
	_, err := ExecRunner{}.Run(ctx, RunSpec{Argv: []string{"sh", "-c", "sleep 30 & wait"}})
	if err == nil {
		t.Fatal("the deadline was not reported")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %v; the process group was not killed", elapsed)
	}
}

func TestRunRejectsEmptyArgv(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), RunSpec{})
	if err == nil {
		t.Fatal("an empty argv was accepted")
	}
}

// OnLine sees each line when the command writes it, not when it exits:
// the first line of a command that then sleeps arrives well before the
// command ends (DECISIONS I-1536).
func TestOnLineStreamsWhileTheCommandRuns(t *testing.T) {
	var mu sync.Mutex
	var got []string
	var firstAt time.Time
	start := time.Now()
	res, err := ExecRunner{}.Run(context.Background(), RunSpec{
		Argv: []string{"sh", "-c", "echo one; echo E: two >&2; sleep 1; echo three"},
		Env:  []string{"PATH=/usr/bin:/bin:" + os.Getenv("PATH")},
		OnLine: func(l string) {
			mu.Lock()
			defer mu.Unlock()
			if len(got) == 0 {
				firstAt = time.Now()
			}
			got = append(got, l)
		},
	})
	end := time.Now()
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(got)
	if strings.Join(got, "|") != "E: two|one|three" {
		t.Fatalf("lines %q", got)
	}
	if end.Sub(firstAt) < 700*time.Millisecond {
		t.Fatalf("first line came %s after the start and %s before the end: not streamed", firstAt.Sub(start), end.Sub(firstAt))
	}
	if string(res.Stdout) != "one\nthree\n" {
		t.Fatalf("stdout tail %q", res.Stdout)
	}
	t.Logf("first line %s after start, command took %s", firstAt.Sub(start), end.Sub(start))
}

// RunSpec.Background (DECISIONS I-1560): a root command runs through
// systemd-run's scope, nice and ionice, and its output and exit code
// come back as the command's own. Fakes of the three on PATH record
// their arguments and exec the rest.
func TestBackgroundRunsInALowWeightScope(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("systemd-run", `echo "systemd-run $*" >> `+log+`; while [ "$1" != "--" ]; do shift; done; shift; exec "$@"`)
	write("nice", `echo "nice $1 $2" >> `+log+`; shift 2; exec "$@"`)
	write("ionice", `echo "ionice $1 $2" >> `+log+`; shift 2; exec "$@"`)
	env := []string{"PATH=" + dir + ":/usr/bin:/bin"}
	res, err := ExecRunner{}.Run(context.Background(), RunSpec{Argv: []string{"sh", "-c", "echo hi; exit 3"}, Env: env, Background: true})
	if err != nil || res.ExitCode != 3 || strings.TrimSpace(string(res.Stdout)) != "hi" {
		t.Fatalf("res %+v err %v", res, err)
	}
	b, _ := os.ReadFile(log)
	got := string(b)
	for _, want := range []string{"systemd-run --scope --quiet --collect --slice=repose_menu.slice -- ", "nice -n 10", "ionice -c2 -n7"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapper log %q has no %q", got, want)
		}
	}
	// Without Background nothing wraps it.
	_ = os.Remove(log)
	if res, err := (ExecRunner{}).Run(context.Background(), RunSpec{Argv: []string{"true"}, Env: env}); err != nil || res.ExitCode != 0 {
		t.Fatalf("plain run: %+v %v", res, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("a command without Background went through the wrapper")
	}
}
