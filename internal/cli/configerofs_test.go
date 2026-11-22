package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// newErofsEnv is newRoundtripEnv for a machine that boots the Ubuntu base
// (DECISIONS I-1462): the fake's default runtime set to erofs before the
// create.
func newErofsEnv(t *testing.T) (*fakeapi.Fake, *Env, *Project, *discardWriter, *discardWriter) {
	t.Helper()
	return newErofsEnvWith(t, fakeapi.Options{})
}

func newErofsEnvWith(t *testing.T, opts fakeapi.Options) (*fakeapi.Fake, *Env, *Project, *discardWriter, *discardWriter) {
	t.Helper()
	fake := fakeapi.New(opts)
	t.Cleanup(fake.Close)
	fake.SetDefaultGuestRuntime("erofs")
	e, p := newRoundtripEnv(t, fake)
	if p.GuestRuntime != "erofs" {
		t.Fatalf("created %q, want erofs", p.GuestRuntime)
	}
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut
	return fake, e, p, out, errOut
}

// `repose config add` on an erofs machine: the same first line as on a
// Nix machine without "Building revision", and the same last line
// (DECISIONS I-1488), with the machine's ApplyMenu lines streamed as the
// steps between them.
func TestConfigAddErofsStreamsApplyMenuLines(t *testing.T) {
	_, e, p, out, errOut := newErofsEnvWith(t, fakeapi.Options{ConfigDelay: 300 * time.Millisecond})
	ctx := context.Background()
	if err := ConfigAddCmd(ctx, e, "", []string{"postgresql", "bun"}); err != nil {
		t.Fatalf("add: %v\n%s", err, errOut.buf.String())
	}
	got := out.buf.String()
	if !strings.HasPrefix(got, "Added postgresql and bun to todo-app.\nApplied revision ") || strings.Contains(got, "Building") {
		t.Fatalf("stdout:\n%s", got)
	}
	steps := errOut.buf.String()
	last := -1
	for _, want := range []string{"installing postgresql...\n", "✓ postgresql ready  ", "installing bun...\n", "✓ bun ready  "} {
		i := strings.Index(steps, want)
		if i < 0 || i < last {
			t.Fatalf("step %q missing or out of order in:\n%s", want, steps)
		}
		last = i
	}
	for _, never := range []string{"Building", "Evaluating", "Fetching", "Switching", "nix ›"} {
		if strings.Contains(steps, never) {
			t.Fatalf("steps say %q:\n%s", never, steps)
		}
	}
	if sel := menuOf(t, e, p.ID); len(sel) != 2 || sel[0].ID != "postgresql" || sel[1].ID != "bun" {
		t.Fatalf("selection %+v", sel)
	}

	// A package name is an apt name there; removing names the removal.
	out.buf.Reset()
	errOut.buf.Reset()
	if err := ConfigAddCmd(ctx, e, "", []string{"python3.12-venv"}); err != nil {
		t.Fatalf("add apt name: %v", err)
	}
	if got := out.buf.String(); !strings.HasPrefix(got, "Added python3.12-venv to todo-app.\n") {
		t.Fatalf("stdout:\n%s", got)
	}
	out.buf.Reset()
	errOut.buf.Reset()
	if err := ConfigRemoveCmd(ctx, e, "", []string{"bun"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := out.buf.String(); !strings.HasPrefix(got, "Removed bun from todo-app.\nApplied revision ") {
		t.Fatalf("stdout:\n%s", got)
	}
}

// A nixpkgs attribute path is not a package name on an erofs machine,
// and the refusal says what is.
func TestConfigAddErofsRefusesNixAttribute(t *testing.T) {
	_, e, _, _, _ := newErofsEnv(t)
	err := ConfigAddCmd(context.Background(), e, "", []string{"python312Packages.black"})
	if exitCode(err) != ExitUsage || !strings.Contains(err.Error(), `"python312Packages.black" is not an apt package name`) {
		t.Fatalf("got %v (exit %d)", err, exitCode(err))
	}
}

// An item that does not install fails the command with the machine's own
// line, and says nothing about the previous revision, since the items
// before it stay installed.
func TestConfigAddErofsFailure(t *testing.T) {
	_, e, _, out, errOut := newErofsEnvWith(t, fakeapi.Options{ConfigDelay: 300 * time.Millisecond})
	err := ConfigAddCmd(context.Background(), e, "", []string{"bun", fakeapi.FailMenuPackage})
	if exitCode(err) != ExitBuildFailed {
		t.Fatalf("got %v (exit %d)", err, exitCode(err))
	}
	got := out.buf.String()
	if !strings.Contains(got, "error: ") || !strings.Contains(got, fakeapi.FailMenuPackage+" failed: ") || strings.Contains(got, "Nothing changed") {
		t.Fatalf("stdout:\n%s", got)
	}
	steps := errOut.buf.String()
	if !strings.Contains(steps, "✓ bun ready") || !strings.Contains(steps, fakeapi.FailMenuPackage+" failed: ") || strings.Contains(steps, "✓ "+fakeapi.FailMenuPackage) {
		t.Fatalf("steps:\n%s", steps)
	}
}

// Every Nix file is refused on an erofs machine with the api's sentence
// and exit 1, the code of every refusal, and nothing more (I-1488).
func TestErofsRefusesNixFiles(t *testing.T) {
	_, e, _, out, _ := newErofsEnv(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "repose.nix")
	if err := os.WriteFile(path, []byte("{ pkgs, ... }: { home.packages = [ pkgs.gcc ]; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := func(name string, err error) {
		t.Helper()
		if exitCode(err) != ExitGeneric || err.Error() != fakeapi.MsgNixConfigRetired {
			t.Fatalf("%s: got %q (exit %d)", name, err, exitCode(err))
		}
	}
	check("apply", ConfigApplyCmd(ctx, e, "", path))
	edited := false
	check("edit", ConfigEditCmd(ctx, e, "", func(string) error { edited = true; return nil }))
	if edited {
		t.Fatal("edit opened an editor on a machine that takes no Nix file")
	}
	check("off", PersonalSwitchCmd(ctx, e, "", false))
	check("on", PersonalSwitchCmd(ctx, e, "", true))
	if out.buf.Len() != 0 {
		t.Fatalf("stdout:\n%s", out.buf.String())
	}
	if nixConfigRetired != fakeapi.MsgNixConfigRetired {
		t.Fatalf("the CLI's copy of the sentence drifted from the api's:\n%s\n%s", nixConfigRetired, fakeapi.MsgNixConfigRetired)
	}
}

// `repose config show` prints the menu on an erofs machine, which has no
// repose.nix.
func TestConfigShowErofs(t *testing.T) {
	_, e, _, out, _ := newErofsEnv(t)
	ctx := context.Background()
	if err := ConfigAddCmd(ctx, e, "", []string{"postgresql", "ffmpeg"}); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := ConfigShowCmd(ctx, e, "", false); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "postgresql\nffmpeg\n" {
		t.Fatalf("show:\n%q", got)
	}
}

// A create's view does not know the runtime: the first line only
// ApplyMenu writes switches it to the menu's steps.
func TestBuildViewTakesApplyMenuLines(t *testing.T) {
	w := &discardWriter{}
	pr := newProgress(w, false)
	v := &buildView{pr: pr}
	for _, l := range []string{"installing redis", "redis ready", "installing ffmpeg", "ffmpeg failed: E: Unable to locate package ffmpeg", "switching the machine"} {
		v.Line(l)
	}
	pr.End()
	got := w.buf.String()
	for _, want := range []string{"installing redis...\n", "✓ redis ready  ", "installing ffmpeg...\n", "ffmpeg failed: E: Unable to locate package ffmpeg\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Switching") || strings.Contains(got, "✓ ffmpeg") {
		t.Fatalf("view:\n%s", got)
	}
	// A Nix log is untouched.
	w2 := &discardWriter{}
	pr2 := newProgress(w2, false)
	nv := &buildView{pr: pr2}
	nv.Line("evaluating configuration")
	if nv.menu || !strings.Contains(w2.buf.String(), "Evaluating your config...") {
		t.Fatalf("nix view: menu=%v\n%s", nv.menu, w2.buf.String())
	}
}

// The lines of an erofs config op, as the fake writes them and guestd's
// ApplyMenu does (vsock-guestd.md): each item's step is its own lines,
// and nothing of a Nix build shows, -v included.
var applyMenuLines = []string{
	"switching the machine",
	"installing postgresql",
	"postgresql 16 ready",
	"installing bun",
	"bun ready",
	"removing redis",
	"redis removed",
}

func TestConfigOpErofsShowsApplyMenuLines(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		c := configOpServer(t, applyMenuLines, `{"state":"done"}`, false)
		errOut, out := &discardWriter{}, &discardWriter{}
		e := &Env{Client: c, Out: out, ErrOut: errOut, TTY: true, Verbose: verbose}
		p := &Project{ID: "p", Slug: "todo-app", GuestRuntime: "erofs"}
		op, pr, err := waitConfigOp(context.Background(), e, p, "o")
		if err != nil {
			t.Fatal(err)
		}
		printApplied(e, p, op, "0199b1c2-0000-7000-8000-00004f1c2a9e", pr)
		got := errOut.buf.String()
		last := -1
		for _, want := range []string{"✓ postgresql 16 ready  ", "✓ bun ready  ", "✓ redis removed  "} {
			i := strings.Index(got, want)
			if i < 0 || i < last {
				t.Fatalf("-v=%v: step %q missing or out of order in:\n%s", verbose, want, got)
			}
			last = i
		}
		for _, never := range []string{"Switch", "Build", "Evaluat", "nix ›"} {
			if strings.Contains(got, never) {
				t.Fatalf("-v=%v: %q in:\n%s", verbose, never, got)
			}
		}
		if !strings.HasPrefix(out.buf.String(), "Applied revision 4f1c2a9e in ") {
			t.Fatalf("result line %q", out.buf.String())
		}
	}
}

// `repose config revisions` on an erofs machine has no MACHINE.NIX
// column: no machine.nix ever reaches one.
func TestConfigRevisionsErofs(t *testing.T) {
	_, e, _, out, _ := newErofsEnv(t)
	ctx := context.Background()
	if err := ConfigAddCmd(ctx, e, "", []string{"redis"}); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := ConfigRevisionsCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	if !strings.HasPrefix(got, "REVISION  CREATED") || strings.Contains(got, "MACHINE.NIX") || !strings.Contains(got, "applied") {
		t.Fatalf("revisions:\n%s", got)
	}
}

// On an erofs machine `repose run` and `repose sync` leave the checkout's
// repose.nix unsent and say nothing: the api would refuse it on every run
// (I-1488). Only `repose config apply` meets the refusal.
func TestRunAndSyncSkipRepoConfigOnErofs(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	fake.SetDefaultGuestRuntime("erofs")
	f := newRunFixture(t, fake)
	ctx := context.Background()
	errOut := &discardWriter{}
	f.env.ErrOut = errOut
	if err := os.WriteFile(filepath.Join(f.local, "repose.nix"), []byte("{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(ctx, f.env, RunOptions{Name: testSlug, NoAttach: true}, false); err != nil {
		t.Fatalf("run: %v\n%s", err, errOut.buf.String())
	}
	if err := runRun(ctx, f.env, RunOptions{NoAttach: true, Sync: true}, false); err != nil {
		t.Fatalf("sync: %v\n%s", err, errOut.buf.String())
	}
	projects, err := f.env.Client.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].GuestRuntime != "erofs" {
		t.Fatalf("projects: %+v %v", projects, err)
	}
	if n := fake.Calls("PUT /v1/projects/{id}/config"); n != 0 {
		t.Fatalf("%d config PUTs on an erofs machine", n)
	}
	if strings.Contains(errOut.buf.String(), "repose.nix") {
		t.Fatalf("run or sync named repose.nix:\n%s", errOut.buf.String())
	}
}
