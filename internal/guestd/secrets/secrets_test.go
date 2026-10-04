package secrets

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func newHandler(t *testing.T) (*Handler, sysdep.Paths, *sysdep.FakeRunner) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	run := sysdep.NewFakeRunner()
	h := New(p, run, quietLog())
	// The test process is not root, so it cannot chown to dev; ownership is
	// asserted in the NixOS VM test instead.
	h.uid, h.gid = -1, -1
	return h, p, run
}

func secret(name, value string) *guestdv1.Secret {
	return &guestdv1.Secret{Name: name, Value: []byte(value)}
}

func TestWriteSecretsFileModes(t *testing.T) {
	h, p, _ := newHandler(t)
	err := h.Write(context.Background(), []*guestdv1.Secret{
		secret("OPENAI_API_KEY", "sk-abc"),
		secret("GEMINI_API_KEY", "gm-def"),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, name := range []string{"OPENAI_API_KEY", "GEMINI_API_KEY"} {
		fi, err := os.Stat(filepath.Join(p.SecretsDir(), name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fi.Mode().Perm() != 0o400 {
			t.Errorf("%s mode = %o, want 400", name, fi.Mode().Perm())
		}
	}
	fi, err := os.Stat(p.SecretsEnv())
	if err != nil {
		t.Fatalf("secrets.env: %v", err)
	}
	if fi.Mode().Perm() != 0o400 {
		t.Errorf("secrets.env mode = %o, want 400", fi.Mode().Perm())
	}
}

func TestSecretsEnvQuoting(t *testing.T) {
	h, p, _ := newHandler(t)
	// A value with a single quote and newlines is the case that breaks a naive
	// writer and leaves every login shell broken.
	value := "it's a\nmulti 'line' value\n"
	if err := h.Write(context.Background(), []*guestdv1.Secret{secret("TRICKY", value)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p.SecretsEnv())
	if err != nil {
		t.Fatal(err)
	}
	want := "export TRICKY='it'\\''s a\nmulti '\\''line'\\'' value\n'\n"
	if !strings.Contains(string(b), want) {
		t.Fatalf("secrets.env =\n%q\nwant it to contain\n%q", b, want)
	}
	// And the quoting must actually survive a shell.
	if got := unquote(t, string(b)); got != value {
		t.Fatalf("a shell sourcing the file reads %q, want %q", got, value)
	}
}

// unquote runs the env file through sh and prints the variable back, which is
// the only check that matters for shell quoting.
func unquote(t *testing.T, env string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(path, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := sysdep.ExecRunner{}.Run(context.Background(), sysdep.RunSpec{
		Argv: []string{"sh", "-c", ". " + path + `; printf %s "$TRICKY"`},
	})
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("sh exited %d: %s", res.ExitCode, res.Stderr)
	}
	return string(res.Stdout)
}

func TestReservedNamesGoToRunDirAndReloadSSHD(t *testing.T) {
	h, p, run := newHandler(t)
	err := h.Write(context.Background(), []*guestdv1.Secret{
		secret(ReservedHostKey, "PRIVATE KEY"),
		secret(ReservedHostCert, "ssh-ed25519-cert..."),
		secret(ReservedUserCA, "ssh-ed25519 AAAA..."),
		secret("APP_TOKEN", "t"),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name string
		mode os.FileMode
	}{
		{ReservedHostKey, 0o600},
		{ReservedHostCert, 0o644},
		{ReservedUserCA, 0o644},
	}
	for _, c := range cases {
		fi, err := os.Stat(filepath.Join(p.RunDir(), c.name))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if fi.Mode().Perm() != c.mode {
			t.Errorf("%s mode = %o, want %o", c.name, fi.Mode().Perm(), c.mode)
		}
		if _, err := os.Stat(filepath.Join(p.SecretsDir(), c.name)); !os.IsNotExist(err) {
			t.Errorf("%s was also written into the secrets directory", c.name)
		}
	}

	b, err := os.ReadFile(p.SecretsEnv())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ssh_host") || strings.Contains(string(b), "user_ca") {
		t.Fatalf("sshd material leaked into secrets.env:\n%s", b)
	}
	if _, ok := run.Ran("reload-or-restart sshd.service"); !ok {
		t.Fatalf("sshd was not reloaded; calls: %v", run.Calls())
	}
}

func TestWriteIsTheWholeSetSoRemovalReachesTheGuest(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.SecretsDir(), "B")); !os.IsNotExist(err) {
		t.Fatal("a withdrawn secret was left on the tmpfs")
	}
	b, _ := os.ReadFile(p.SecretsEnv())
	if strings.Contains(string(b), "export B=") {
		t.Fatalf("a withdrawn secret is still exported:\n%s", b)
	}
}

func TestWriteIsAtomicPerRequest(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("GOOD", "1")}); err != nil {
		t.Fatalf("write: %v", err)
	}

	oversized := strings.Repeat("x", MaxValueBytes+1)
	err := h.Write(ctx, []*guestdv1.Secret{secret("ALSO_GOOD", "2"), secret("TOO_BIG", oversized)})
	if err == nil {
		t.Fatal("an oversized value was accepted")
	}
	if sysdep.CodeOf(err) != sysdep.CodeInvalidArgument {
		t.Fatalf("code = %s, want invalid_argument", sysdep.CodeOf(err))
	}
	if _, err := os.Stat(filepath.Join(p.SecretsDir(), "ALSO_GOOD")); !os.IsNotExist(err) {
		t.Fatal("part of a rejected batch was written")
	}
	if _, err := os.Stat(filepath.Join(p.SecretsDir(), "GOOD")); err != nil {
		t.Fatal("the rejected batch disturbed an existing secret")
	}
}

func TestInvalidNames(t *testing.T) {
	h, _, _ := newHandler(t)
	bad := []string{"", "lower-case-dash", "9LEADING", "HAS SPACE", "../escape", "DUP"}
	for _, name := range bad {
		list := []*guestdv1.Secret{secret(name, "v")}
		if name == "DUP" {
			list = append(list, secret("DUP", "v2"))
		}
		if err := h.Write(context.Background(), list); err == nil {
			t.Errorf("%q was accepted", name)
		} else if sysdep.CodeOf(err) != sysdep.CodeInvalidArgument {
			t.Errorf("%q: code = %s, want invalid_argument", name, sysdep.CodeOf(err))
		}
	}
}

func TestNulByteInValueRejected(t *testing.T) {
	h, _, _ := newHandler(t)
	err := h.Write(context.Background(), []*guestdv1.Secret{{Name: "NULLY", Value: []byte("a\x00b")}})
	if sysdep.CodeOf(err) != sysdep.CodeInvalidArgument {
		t.Fatalf("code = %s, want invalid_argument", sysdep.CodeOf(err))
	}
}

func TestReservedNamesAreNotRemovedByALaterWrite(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret(ReservedUserCA, "ca")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.RunDir(), ReservedUserCA)); err != nil {
		t.Fatal("the CA public key was removed by an unrelated secrets update")
	}
}

func TestIsReserved(t *testing.T) {
	for _, n := range []string{ReservedHostKey, ReservedHostCert, ReservedUserCA} {
		if !IsReserved(n) {
			t.Errorf("%s is not reported reserved", n)
		}
	}
	if IsReserved("ANTHROPIC_API_KEY") {
		t.Error("a normal secret name is reported reserved")
	}
}

// A reload that fails while sshd's boot-time start job is still queued is
// retried until it goes through (the faster boot of I-231 made guestd
// answer first, and one failed reload failed the whole start).
func TestSSHDReloadRetriesWhileItsStartIsQueued(t *testing.T) {
	h, _, run := newHandler(t)
	h.reloadRetry = time.Millisecond
	var reloads int
	run.Match["reload-or-restart sshd.service"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("Job for sshd.service canceled.")}
	run.Hook = func(spec sysdep.RunSpec) {
		if strings.Contains(strings.Join(spec.Argv, " "), "reload-or-restart sshd.service") {
			reloads++
			if reloads == 3 {
				run.Match["reload-or-restart sshd.service"] = sysdep.RunResult{}
			}
		}
	}
	if err := h.Write(context.Background(), []*guestdv1.Secret{secret(ReservedHostKey, "PRIVATE KEY")}); err != nil {
		t.Fatalf("write after two failed reloads: %v", err)
	}
	if reloads != 3 {
		t.Fatalf("reloads = %d, want 3", reloads)
	}
}

func readEnv(t *testing.T, p sysdep.Paths) string {
	t.Helper()
	b, err := os.ReadFile(p.SecretsEnv())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// I-475: a name removed since boot is unset in secrets.env, so an agent that
// inherited it loses it in its next command.
func TestSecretsEnvUnsetsARemovedName(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, p); strings.Contains(env, "unset") {
		t.Fatalf("nothing was removed, yet:\n%s", env)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatal(err)
	}
	env := readEnv(t, p)
	if !strings.Contains(env, "\nunset B\n") || strings.Contains(env, "export B=") {
		t.Fatalf("B is not unset:\n%s", env)
	}
	// An unrelated later write keeps the unset line: an agent started
	// before the removal may still hold B.
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("C", "3")}); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, p); !strings.Contains(env, "\nunset B\n") {
		t.Fatalf("the unset line went away after an unrelated write:\n%s", env)
	}
	// Setting it again exports it and drops the unset line.
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "4")}); err != nil {
		t.Fatal(err)
	}
	env = readEnv(t, p)
	if strings.Contains(env, "unset B") || !strings.Contains(env, "export B='4'") {
		t.Fatalf("B set again:\n%s", env)
	}
}

// A guestd restart is a new Handler over the same tmpfs; the names written
// before it are read back from secrets.names.
func TestSecretsEnvRemovedNamesSurviveAGuestdRestart(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}); err != nil {
		t.Fatal(err)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatal(err)
	}

	restarted := New(p, sysdep.NewFakeRunner(), quietLog())
	restarted.uid, restarted.gid = -1, -1
	if err := restarted.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("C", "3")}); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, p); !strings.Contains(env, "\nunset B\n") {
		t.Fatalf("a guestd restart forgot the removed name:\n%s", env)
	}
	fi, err := os.Stat(p.SecretsNames())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("secrets.names mode = %o, want 600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(p.SecretsNames())
	if string(b) != "A\nB\nC\n" {
		t.Fatalf("secrets.names = %q", b)
	}
}

// A guest whose secrets were written by a guestd from before I-475 has no
// names file; the names in the secrets directory stand in for it.
func TestSecretsEnvUnsetsANameRemovedOnAnUpgradedGuest(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("OLD", "2")}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.SecretsNames()); err != nil {
		t.Fatal(err)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, p); !strings.Contains(env, "\nunset OLD\n") {
		t.Fatalf("OLD is not unset:\n%s", env)
	}
}

func TestSecretsEnvGenerationChangesOnlyWithTheContent(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	gen := func() string {
		first, _, _ := strings.Cut(readEnv(t, p), "\n")
		g, ok := strings.CutPrefix(first, "export REPOSE_SECRETS_GEN=")
		if !ok || !genRe.MatchString(g) {
			t.Fatalf("first line = %q", first)
		}
		return g
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}); err != nil {
		t.Fatal(err)
	}
	g1 := gen()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret(ReservedUserCA, "ca")}); err != nil {
		t.Fatal(err)
	}
	if g := gen(); g != g1 {
		t.Fatalf("the same secrets got a new generation: %s then %s", g1, g)
	}
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "2")}); err != nil {
		t.Fatal(err)
	}
	if g := gen(); g == g1 {
		t.Fatal("a changed value kept the generation")
	}
}

// trickyValues are values that break a naive writer, a naive reader or tmux's
// argument parser.
var trickyValues = map[string]string{
	"QUOTE":     "it's",
	"NEWLINES":  "line one\nline 'two'\n",
	"DOLLAR":    "$HOME $(echo no) `echo no` ${X:-y}",
	"BACKSLASH": `a\b\\c\`,
	"SEMI":      "ends;",
	"ESC_SEMI":  `ends\;`,
	"DASH":      "-n",
	"EMPTY":     "",
	"SPACES":    "  two  spaces  ",
	"UNICODE":   "café ☃",
	"GLOB":      "*",
}

func trickyList() []*guestdv1.Secret {
	var list []*guestdv1.Secret
	for n, v := range trickyValues {
		list = append(list, secret(n, v))
	}
	return list
}

// Every tricky value comes back byte for byte from a bash that sources the
// file.
func TestSecretsEnvTrickyValuesSurviveBash(t *testing.T) {
	h, p, _ := newHandler(t)
	if err := h.Write(context.Background(), trickyList()); err != nil {
		t.Fatal(err)
	}
	for name, want := range trickyValues {
		res, err := sysdep.ExecRunner{}.Run(context.Background(), sysdep.RunSpec{
			Argv: []string{"bash", "-euo", "pipefail", "-c", `. "$1"; printf %s "${!2}"`, "bash", p.SecretsEnv(), name},
			Env:  []string{"PATH=" + os.Getenv("PATH")},
		})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%s: %v exit %d: %s", name, err, res.ExitCode, res.Stderr)
		}
		if string(res.Stdout) != want {
			t.Errorf("%s: bash reads %q, want %q", name, res.Stdout, want)
		}
	}
}

func TestSecretsArePushedToTmux(t *testing.T) {
	h, p, run := newHandler(t)
	ctx := context.Background()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "x;")}); err != nil {
		t.Fatal(err)
	}
	run.Reset()
	if err := h.Write(ctx, []*guestdv1.Secret{secret("B", "x;"), secret(ReservedUserCA, "ca")}); err != nil {
		t.Fatal(err)
	}
	var tmux []sysdep.RunSpec
	for _, c := range run.Calls() {
		if c.Argv[0] == "tmux" {
			tmux = append(tmux, c)
		}
	}
	if len(tmux) != 1 {
		t.Fatalf("tmux calls = %d, want 1: %v", len(tmux), run.Calls())
	}
	if tmux[0].User != "dev" {
		t.Errorf("tmux ran as %q, want dev", tmux[0].User)
	}
	first, _, _ := strings.Cut(readEnv(t, p), "\n")
	gen := strings.TrimPrefix(first, "export REPOSE_SECRETS_GEN=")
	want := []string{"tmux",
		"set-environment", "-g", "B", `x\;`, ";",
		"set-environment", "-gu", "A", ";",
		"set-environment", "-g", "REPOSE_SECRETS_GEN", gen,
	}
	if strings.Join(tmux[0].Argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("tmux argv = %q\nwant %q", tmux[0].Argv, want)
	}
}

// No tmux server (before SetupProject), a tmux failure or a runner error
// never fail the write.
func TestTmuxFailureDoesNotFailTheWrite(t *testing.T) {
	for name, set := range map[string]func(*sysdep.FakeRunner){
		"no server": func(r *sysdep.FakeRunner) {
			r.Results["tmux"] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("no server running on /tmp/tmux-1000/default")}
		},
		"exit 1": func(r *sysdep.FakeRunner) { r.Results["tmux"] = sysdep.RunResult{ExitCode: 1} },
		"missing": func(r *sysdep.FakeRunner) {
			r.Errs["tmux"] = os.ErrNotExist
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, p, run := newHandler(t)
			set(run)
			if err := h.Write(context.Background(), []*guestdv1.Secret{secret("A", "1")}); err != nil {
				t.Fatalf("write: %v", err)
			}
			if !strings.Contains(readEnv(t, p), "export A='1'") {
				t.Fatal("secrets.env not written")
			}
		})
	}
}

// Values large enough to pass the kernel's limit for one exec are split over
// several tmux calls, and the generation is in the last one.
func TestTmuxPushIsBatched(t *testing.T) {
	h, _, run := newHandler(t)
	var list []*guestdv1.Secret
	for i := 0; i < 12; i++ {
		list = append(list, secret("BIG_"+string(rune('A'+i)), strings.Repeat("v", MaxValueBytes)))
	}
	if err := h.Write(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, c := range run.Calls() {
		if c.Argv[0] == "tmux" {
			calls = append(calls, c.Argv)
		}
	}
	if len(calls) < 3 {
		t.Fatalf("tmux calls = %d, want the 768 KiB split", len(calls))
	}
	for i, argv := range calls {
		size := 0
		for _, a := range argv {
			size += len(a)
		}
		if size > 300<<10 {
			t.Errorf("call %d carries %d bytes", i, size)
		}
		hasGen := strings.Contains(strings.Join(argv, " "), "REPOSE_SECRETS_GEN")
		if hasGen != (i == len(calls)-1) {
			t.Errorf("call %d of %d: generation present = %v", i, len(calls), hasGen)
		}
	}
}

// The arguments pushTmux builds, run by a real tmux on a private socket: a
// window started afterwards sees every tricky value byte for byte, and a
// removed name is gone from the global environment.
func TestTmuxArgumentsAgainstARealTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	dir, err := os.MkdirTemp("/tmp", "tmx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command(tmux, append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args[0], err, out)
		}
	}
	run("new-session", "-d", "-s", "t", "sleep 30")
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", sock, "kill-server").Run() })
	run("set-environment", "-g", "GONE", "x")

	env := envFile{unsets: []string{"GONE"}, gen: "00000000000000aa"}
	for n, v := range trickyValues {
		env.rows = append(env.rows, envRow{n, v})
	}
	for _, args := range tmuxBatches(env) {
		run(args...)
	}
	for name, want := range trickyValues {
		out := filepath.Join(dir, name)
		run("new-window", "-d", "printenv "+name+" > "+out+"; printenv GONE > "+out+".gone; echo done > "+out+".done")
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(out + ".done"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: window did not finish", name)
			}
			time.Sleep(20 * time.Millisecond)
		}
		got, _ := os.ReadFile(out)
		if string(got) != want+"\n" {
			t.Errorf("%s: a tmux window sees %q, want %q", name, got, want)
		}
		if gone, _ := os.ReadFile(out + ".gone"); len(gone) != 0 {
			t.Errorf("a removed name is still in tmux: %q", gone)
		}
	}
}
