package project

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/sysdep"
)

func quietLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func newHandler(t *testing.T) (*Handler, sysdep.Paths, *sysdep.FakeRunner) {
	t.Helper()
	p := sysdep.Paths{Root: t.TempDir()}
	run := sysdep.NewFakeRunner()
	h := New(p, run, quietLog())
	h.uid, h.gid = -1, -1
	return h, p, run
}

func req() *guestdv1.SetupProject {
	return &guestdv1.SetupProject{
		ProjectSlug: "todo-app",
		RemoteUrl:   "https://github.com/heracraft/todo-app",
		Tz:          "Africa/Nairobi",
		Lang:        "C.UTF-8",
	}
}

func TestSetupWritesEverything(t *testing.T) {
	h, p, run := newHandler(t)
	if err := h.Setup(context.Background(), req()); err != nil {
		t.Fatalf("setup: %v", err)
	}

	b, err := os.ReadFile(p.ProjectJSON())
	if err != nil {
		t.Fatalf("project.json: %v", err)
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatalf("project.json is not JSON: %v", err)
	}
	if info.Slug != "todo-app" || info.TZ != "Africa/Nairobi" {
		t.Fatalf("project.json = %+v", info)
	}

	env, err := os.ReadFile(p.EtcEnv())
	if err != nil {
		t.Fatalf("/etc/repose/env: %v", err)
	}
	for _, want := range []string{"REPOSE_PROJECT=todo-app", "TZ=Africa/Nairobi", "LANG=C.UTF-8"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("/etc/repose/env is missing %q:\n%s", want, env)
		}
	}

	// A new machine has no checkout until the CLI's first sync makes one
	// (I-368): no ~/<slug>, no git init.
	if _, err := os.Lstat(p.ProjectDir("todo-app")); !os.IsNotExist(err) {
		t.Fatalf("~/todo-app was made: %v", err)
	}
	if _, ok := run.Ran("git init"); ok {
		t.Fatalf("git init ran with no checkout; calls: %v", run.Calls())
	}
	if _, ok := run.Ran("start " + TmuxUnit); !ok {
		t.Fatalf("%s was not started; calls: %v", TmuxUnit, run.Calls())
	}
	if h.Slug() != "todo-app" {
		t.Fatalf("Slug() = %q", h.Slug())
	}
}

func TestSetupUsesTheProjectJSONHostdSent(t *testing.T) {
	h, p, _ := newHandler(t)
	r := req()
	r.ProjectJson = []byte(`{"project_id":"01931f0e-0000-7000-8000-000000000001","slug":"todo-app","class":"large"}`)

	if err := h.Setup(context.Background(), r); err != nil {
		t.Fatalf("setup: %v", err)
	}
	b, _ := os.ReadFile(p.ProjectJSON())
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	if info.ProjectID == "" || info.Class != "large" {
		t.Fatalf("the api's record was not written through: %+v", info)
	}
}

func TestSetupRejectsMalformedProjectJSON(t *testing.T) {
	h, _, _ := newHandler(t)
	r := req()
	r.ProjectJson = []byte("{not json")
	if err := h.Setup(context.Background(), r); sysdep.CodeOf(err) != sysdep.CodeInvalidArgument {
		t.Fatalf("code = %s, want invalid_argument", sysdep.CodeOf(err))
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	h, p, run := newHandler(t)
	ctx := context.Background()
	if err := h.Setup(ctx, req()); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	// Pretend the repository now exists and the tmux unit is up.
	if err := os.MkdirAll(filepath.Join(p.ProjectDir("todo-app"), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	run.Reset()

	if err := h.Setup(ctx, req()); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if _, ok := run.Ran("git init"); ok {
		t.Fatal("git init ran again over an existing repository")
	}
	// One `start`, which systemd does not repeat for an active unit; never
	// a restart, and no separate is-active login first (I-231).
	var argvs []string
	for _, c := range run.Calls() {
		argvs = append(argvs, strings.Join(c.Argv, " "))
	}
	calls := strings.Join(argvs, "\n")
	if strings.Contains(calls, "restart") || strings.Contains(calls, "is-active") {
		t.Fatalf("second setup ran %q", calls)
	}
	if n := strings.Count(calls, "start "+TmuxUnit); n != 1 {
		t.Fatalf("second setup started the tmux unit %d times; calls: %q", n, calls)
	}
}

func TestSetupRejectsSlugsThatCouldEscape(t *testing.T) {
	h, _, _ := newHandler(t)
	for _, slug := range []string{"", "..", "../etc", "has space", "UPPER", "/abs", strings.Repeat("x", 65)} {
		r := req()
		r.ProjectSlug = slug
		if err := h.Setup(context.Background(), r); err == nil {
			t.Errorf("%q was accepted as a slug", slug)
		} else if sysdep.CodeOf(err) != sysdep.CodeInvalidArgument {
			t.Errorf("%q: code = %s, want invalid_argument", slug, sysdep.CodeOf(err))
		}
	}
}

func TestSlugIsLoadedFromDiskAtStart(t *testing.T) {
	h, p, _ := newHandler(t)
	if err := h.Setup(context.Background(), req()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A guestd restart: a new handler over the same root must know the
	// session name before hostd sends SetupProject again.
	fresh := New(p, sysdep.NewFakeRunner(), quietLog())
	if fresh.Slug() != "todo-app" {
		t.Fatalf("Slug() after restart = %q, want todo-app", fresh.Slug())
	}
}

func TestSetupReportsAFailedTmuxStart(t *testing.T) {
	h, _, run := newHandler(t)
	run.Match["start "+TmuxUnit] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("Failed to start")}
	if err := h.Setup(context.Background(), req()); err == nil {
		t.Fatal("a failed tmux unit start was reported as success")
	}
}

// The CLI's sync runs `git fetch origin` in the guest; a git init with no
// origin is the failure the first real run hit (DECISIONS I-107).
func TestSetupPointsOriginAtTheRemote(t *testing.T) {
	h, p, run := newHandler(t)
	if err := os.MkdirAll(p.ProjectDir("todo-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := req()
	r.RemoteUrl = "github.com/heracraft/todo-app"
	if err := h.Setup(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, ok := run.Ran("git remote add origin git@github.com:heracraft/todo-app.git"); !ok {
		t.Fatalf("origin was not added; calls: %v", run.Calls())
	}
}

func TestOriginURL(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/heracraft/todo-app":     "git@github.com:heracraft/todo-app.git",
		"github.com/heracraft/todo-app.git": "git@github.com:heracraft/todo-app.git",
		"git@github.com:heracraft/todo.git": "git@github.com:heracraft/todo.git",
		"https://gitlab.com/x/y.git":        "https://gitlab.com/x/y.git",
		"":                                  "",
		"github.com":                        "",
	} {
		if got := originURL(in); got != want {
			t.Errorf("originURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The checkout the CLI recorded (I-368) is the one Setup works on, git
// init and origin included; ~/<slug> is never made beside it.
func TestSetupUsesTheRecordedCheckout(t *testing.T) {
	h, p, run := newHandler(t)
	if err := os.MkdirAll(p.ProjectDir("factory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.CheckoutFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CheckoutFile(), []byte("factory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := req()
	r.RemoteUrl = "github.com/heracraft/todo-app"
	if err := h.Setup(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	c, ok := run.Ran("git init")
	if !ok || c.Dir != p.ProjectDir("factory") {
		t.Fatalf("git init in %q (%v); calls: %v", c.Dir, ok, run.Calls())
	}
	if c, ok := run.Ran("git remote add origin"); !ok || c.Dir != p.ProjectDir("factory") {
		t.Fatalf("origin set in %q (%v)", c.Dir, ok)
	}
	if _, err := os.Lstat(p.ProjectDir("todo-app")); !os.IsNotExist(err) {
		t.Fatalf("~/todo-app was made: %v", err)
	}
}

// A checkout file that names something other than one directory of the
// home is ignored, never followed.
func TestRecordedCheckoutStaysInTheHome(t *testing.T) {
	h, p, _ := newHandler(t)
	if err := os.MkdirAll(p.ProjectDir("real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.CheckoutFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, p.ProjectDir("escape")); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]string{
		"real\n":        "real",
		"missing\n":     "",
		"../etc\n":      "",
		".ssh\n":        "",
		"a/b\n":         "",
		"escape\n":      "",
		"\n":            "",
		"real\nother\n": "",
	} {
		if err := os.WriteFile(p.CheckoutFile(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := h.recordedCheckout(); got != want {
			t.Errorf("checkout file %q: got %q, want %q", body, got, want)
		}
	}
}

// ensureSession starts the unit of the multiplexer project_json names
// (DECISIONS I-503): herdr's for "herdr", tmux's for no key, "tmux" and
// anything else. Exactly one unit is started, and a restarted guestd
// reads the same choice from the file.
func TestSetupStartsTheSessionUnitProjectJSONNames(t *testing.T) {
	for _, c := range []struct{ json, unit, mux string }{
		{`{"slug":"todo-app","multiplexer":"herdr"}`, HerdrUnit, "herdr"},
		{`{"slug":"todo-app"}`, TmuxUnit, "tmux"},
		{`{"slug":"todo-app","multiplexer":"tmux"}`, TmuxUnit, "tmux"},
		{`{"slug":"todo-app","multiplexer":"screen"}`, TmuxUnit, "tmux"},
	} {
		h, p, run := newHandler(t)
		r := req()
		r.ProjectJson = []byte(c.json)
		if err := h.Setup(context.Background(), r); err != nil {
			t.Fatalf("%s: setup: %v", c.json, err)
		}
		var started []string
		for _, call := range run.Calls() {
			if len(call.Argv) > 0 && call.Argv[0] == "systemctl" {
				started = append(started, strings.Join(call.Argv, " "))
			}
		}
		want := "systemctl --user -M dev@ start " + c.unit
		if len(started) != 1 || started[0] != want {
			t.Errorf("%s: systemctl calls = %q, want [%q]", c.json, started, want)
		}
		if h.Multiplexer() != c.mux {
			t.Errorf("%s: Multiplexer() = %q, want %q", c.json, h.Multiplexer(), c.mux)
		}
		if fresh := New(p, sysdep.NewFakeRunner(), quietLog()); fresh.Multiplexer() != c.mux {
			t.Errorf("%s: Multiplexer() after a restart = %q, want %q", c.json, fresh.Multiplexer(), c.mux)
		}
	}
}

func TestSetupReportsAFailedHerdrStart(t *testing.T) {
	h, _, run := newHandler(t)
	run.Match["start "+HerdrUnit] = sysdep.RunResult{ExitCode: 1, Stderr: []byte("Failed to start")}
	r := req()
	r.ProjectJson = []byte(`{"slug":"todo-app","multiplexer":"herdr"}`)
	err := h.Setup(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "herdr session unit") {
		t.Fatalf("err = %v, want the herdr unit's failure", err)
	}
}
