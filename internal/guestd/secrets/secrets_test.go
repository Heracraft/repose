package secrets

import (
	"context"
	"fmt"
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

// I-475: secrets.env is the current set; secrets.refresh unsets a removed
// name where the process holds a value guestd exported for it.
func TestRefreshUnsetsARemovedName(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}))
	refresh := func() string {
		b, err := os.ReadFile(p.SecretsRefresh())
		must(t, err)
		return string(b)
	}
	if strings.Contains(refresh(), "unset B") {
		t.Fatalf("nothing was removed, yet:\n%s", refresh())
	}
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}))
	if env := readEnv(t, p); strings.Contains(env, "B=") || strings.Contains(env, "unset") {
		t.Fatalf("secrets.env after removing B:\n%s", env)
	}
	if !strings.Contains(refresh(), "case ${B+s$B} in s'2') unset B ;; esac\n") {
		t.Fatalf("B has no guarded unset:\n%s", refresh())
	}
	// Setting it again exports it.
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "4")}))
	if r := refresh(); strings.Contains(r, "unset B") || !strings.Contains(r, "case ${B+s$B} in ''|s'2') export B='4' ;; esac\n") {
		t.Fatalf("B set again:\n%s", r)
	}
	// Back to the earlier value: the newer one becomes the earlier one.
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}))
	if r := refresh(); !strings.Contains(r, "case ${B+s$B} in ''|s'4') export B='2' ;; esac\n") {
		t.Fatalf("B set back:\n%s", r)
	}
	for path, want := range map[string]os.FileMode{p.SecretsRefresh(): 0o400, p.SecretsState(): 0o600, p.SecretsEnv(): 0o400} {
		fi, err := os.Stat(path)
		must(t, err)
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", filepath.Base(path), fi.Mode().Perm(), want)
		}
	}
}

// A guestd restart is a new Handler over the same tmpfs: it reads the
// history back and keeps the current generation while nothing changes.
func TestHistorySurvivesAGuestdRestart(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "2")}))
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}))
	g2 := genOf(t, p)

	restarted := New(p, sysdep.NewFakeRunner(), quietLog())
	restarted.uid, restarted.gid = -1, -1
	must(t, restarted.Write(ctx, []*guestdv1.Secret{secret("A", "1")}))
	if g := genOf(t, p); g != g2 {
		t.Fatalf("a restart changed the generation: %s then %s", g2, g)
	}
	st := restarted.readState()
	if st.Gen != g2 || string(st.Current["A"]) != "1" || len(st.Earlier) != 1 || string(st.Earlier["B"][0]) != "2" {
		t.Fatalf("state after restart = %+v", st)
	}
	b, _ := os.ReadFile(p.SecretsRefresh())
	if !strings.Contains(string(b), "unset B ;;") {
		t.Fatalf("a guestd restart forgot the removed name:\n%s", b)
	}
}

// Each name keeps its own last keepValues earlier values, however many
// writes touch other names.
func TestEarlierValuesAreBoundedPerName(t *testing.T) {
	h, _, _ := newHandler(t)
	ctx := context.Background()
	for i := 0; i < 3*keepValues; i++ {
		must(t, h.Write(ctx, []*guestdv1.Secret{secret("STABLE_OLD", "s"), secret("ROT", fmt.Sprint("r", i)), secret("OTHER", fmt.Sprint(i))}))
		if i == 0 {
			must(t, h.Write(ctx, []*guestdv1.Secret{secret("STABLE_OLD", "s"), secret("ONCE", "o"), secret("ROT", "r0"), secret("OTHER", "0")}))
		}
	}
	st := h.readState()
	if n := len(st.Earlier["ROT"]); n != keepValues {
		t.Fatalf("ROT keeps %d earlier values, want %d", n, keepValues)
	}
	if got := string(st.Earlier["ROT"][keepValues-1]); got != fmt.Sprint("r", 3*keepValues-2) {
		t.Fatalf("newest earlier ROT = %q", got)
	}
	if _, ok := st.Earlier["STABLE_OLD"]; ok {
		t.Fatal("a value that never changed has an earlier value")
	}
	if got := st.Earlier["ONCE"]; len(got) != 1 || string(got[0]) != "o" {
		t.Fatalf("a removed name lost its value after %d other writes: %q", 3*keepValues, got)
	}
}

// A state file that is not what guestd writes is a new history, not an
// error, and nothing from it reaches a file shells source.
func TestUnreadableStateStartsANewHistory(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, sysdep.WriteFileAtomic(p.SecretsState(), []byte(`{"gen":"$(evil)","current":{"a b":"MQ==","BASH_ENV":"MQ==","OK":"MQ=="},"earlier":{"__repose_x":["MQ=="],"E":["Mg=="]}}`), 0o600, -1, -1))
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}))
	b, _ := os.ReadFile(p.SecretsRefresh())
	if strings.Contains(string(b), "evil") || strings.Contains(string(b), "a b") || strings.Contains(string(b), "BASH_ENV") ||
		strings.Contains(string(b), "__repose_x") || !strings.Contains(string(b), "in s'1') unset OK ;;") || !strings.Contains(string(b), "in s'2') unset E ;;") {
		t.Fatalf("refresh from a doctored state:\n%s", b)
	}
	must(t, os.WriteFile(p.SecretsState(), []byte("not json"), 0o600))
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "2")}))
	if st := h.readState(); len(st.Earlier) != 0 || string(st.Current["A"]) != "2" {
		t.Fatalf("state = %+v", st)
	}
}

// BASH_ENV, ENV and the generation variable run the refresh; a secret by
// one of those names keeps its file and never becomes a variable.
func TestShellReservedNamesAreNotExported(t *testing.T) {
	h, p, run := newHandler(t)
	must(t, h.Write(context.Background(), []*guestdv1.Secret{
		secret("BASH_ENV", "/tmp/mine"), secret("ENV", "e"), secret("REPOSE_ENV_GEN", "g"), secret("__repose_d", "d"), secret("A", "1"),
	}))
	for _, f := range []string{readEnv(t, p), string(mustRead(t, p.SecretsRefresh()))} {
		if strings.Contains(f, "/tmp/mine") || strings.Contains(f, "ENV='e'") || strings.Contains(f, "'g'") || strings.Contains(f, "'d'") {
			t.Fatalf("a reserved name was exported:\n%s", f)
		}
	}
	for _, c := range run.Calls() {
		if strings.Contains(string(c.Stdin), "/tmp/mine") {
			t.Fatalf("BASH_ENV pushed to tmux: %s", c.Stdin)
		}
	}
	if b := mustRead(t, filepath.Join(p.SecretsDir(), "BASH_ENV")); string(b) != "/tmp/mine" {
		t.Fatalf("file = %q", b)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return b
}

func TestSecretsEnvGenerationChangesOnlyWithTheContent(t *testing.T) {
	h, p, _ := newHandler(t)
	ctx := context.Background()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1")}))
	g1 := genOf(t, p)
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret(ReservedUserCA, "ca")}))
	if g := genOf(t, p); g != g1 {
		t.Fatalf("the same secrets got a new generation: %s then %s", g1, g)
	}
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "2")}))
	if g := genOf(t, p); g == g1 {
		t.Fatal("a changed value kept the generation")
	}
	first, _, _ := strings.Cut(string(mustRead(t, p.SecretsRefresh())), "\n")
	if first != "# repose-env-gen "+genOf(t, p) {
		t.Fatalf("refresh first line = %q", first)
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
	"TILDE":     "~/x",
	"FORMAT":    "#{pane_id} %if 1 {",
	"HIGH":      "\xff\x80 raw",
	"CTRL":      "\x1b[0m\r\t",
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
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("A", "1"), secret("B", "x;")}))
	run.Reset()
	must(t, h.Write(ctx, []*guestdv1.Secret{secret("B", "x;\n$\"\\"), secret(ReservedUserCA, "ca")}))
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
	if strings.Join(tmux[0].Argv, " ") != "tmux source-file -" {
		t.Fatalf("tmux argv = %q: values must not be arguments", tmux[0].Argv)
	}
	want := "set-environment -g B \"x;\\012\\$\\\"\\\\\"\n" +
		"set-environment -gu A\n" +
		"set-environment -g REPOSE_ENV_GEN " + genOf(t, p) + "\n"
	if string(tmux[0].Stdin) != want {
		t.Fatalf("tmux stdin = %q\nwant %q", tmux[0].Stdin, want)
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

// The script pushTmux sends, sourced from stdin by a real tmux on a private
// socket: a window started afterwards sees every tricky value byte for byte,
// values far past tmux's 16 KiB command limit included, and a removed name
// is gone from the global environment.
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

	values := map[string]string{
		"BIG_A": strings.Repeat("a", MaxValueBytes),
		"BIG_B": strings.Repeat("\x01\xff\n", MaxValueBytes/3),
	}
	for n, v := range trickyValues {
		values[n] = v
	}
	env := envFile{unsets: []string{"GONE"}, gen: "00000000000000aa"}
	for n, v := range values {
		env.rows = append(env.rows, envRow{n, v})
	}
	cmd := exec.Command(tmux, "-S", sock, "-f", "/dev/null", "source-file", "-")
	cmd.Stdin = strings.NewReader(string(tmuxScript(env)))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source-file: %v %s", err, out)
	}
	for name, want := range values {
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
