package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// configOpServer serves one op "o" of project "p": running with a log on
// the first read, then final on every later one; the log is lines then
// done. hold, when set, keeps the log open until the request ends.
func configOpServer(t *testing.T, lines []string, final string, hold bool) *Client {
	t.Helper()
	var reads int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/p/ops/o", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&reads, 1) == 1 {
			_, _ = io.WriteString(w, `{"state":"running","phase":"build","log_url":"/v1/projects/p/ops/o/log"}`)
			return
		}
		_, _ = io.WriteString(w, final)
	})
	mux.HandleFunc("/v1/projects/p/ops/o/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i, l := range lines {
			_, _ = fmt.Fprintf(w, "id: %d\ndata: {\"seq\":%d,\"line\":%q,\"ts\":\"2026-09-28T01:02:03.456Z\"}\n\n", i+1, i+1, l)
		}
		w.(http.Flusher).Flush()
		if hold {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "event: done\ndata: {\"state\":\"done\"}\n\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newClient(srv.URL+"/v1", staticToken("t"))
}

var configBuildLines = []string{
	"waiting for a build slot",
	"evaluating configuration",
	"building nixos-system-todo-app",
	"these 2 derivations will be built:",
	"  /nix/store/aaaa-home-manager-files.drv",
	"  /nix/store/bbbb-nixos-system-todo-app.drv",
	"these 3 paths will be fetched (1.5 MiB download, 5.9 MiB unpacked):",
	"  /nix/store/cccc-cloudflared-2025.1.0",
	"copying path '/nix/store/cccc-cloudflared-2025.1.0' from 'https://cache.nixos.org'...",
	"copying path '/nix/store/dddd-x' from 'https://cache.nixos.org'...",
	"building '/nix/store/aaaa-home-manager-files.drv'...",
	"copying path '/nix/store/eeee-y' from 'https://cache.nixos.org'...",
	"building '/nix/store/bbbb-nixos-system-todo-app.drv'...",
	"built /nix/store/ffff-nixos-system-todo-app",
	"switching the machine",
}

// A config build shows run's ✓ steps, with counts read off Nix's own
// lines, and hides Nix's lines on a terminal (I-320).
func TestConfigOpShowsSteps(t *testing.T) {
	c := configOpServer(t, configBuildLines, `{"state":"done"}`, false)
	errOut, out := &discardWriter{}, &discardWriter{}
	e := &Env{Client: c, Out: out, ErrOut: errOut, TTY: true}
	p := &Project{ID: "p", Slug: "todo-app"}
	op, pr, err := waitConfigOp(context.Background(), e, p, "o")
	if err != nil {
		t.Fatal(err)
	}
	printApplied(e, p, op, "0199b1c2-0000-7000-8000-00004f1c2a9e", pr)
	got := errOut.buf.String()
	last := -1
	for _, want := range []string{"✓ Got a build slot", "✓ Evaluated your config", "✓ Fetched 3 paths (1.5 MiB)", "✓ Built 2 derivations", "✓ Switched the machine"} {
		i := strings.Index(got, want)
		if i < 0 || i < last {
			t.Fatalf("step %q missing or out of order in:\n%s", want, got)
		}
		last = i
	}
	if strings.Contains(got, "nix ›") {
		t.Fatalf("nix lines shown without -v:\n%s", got)
	}
	if !strings.HasPrefix(out.buf.String(), "Applied revision 4f1c2a9e in ") { // the id's random end (I-619)
		t.Fatalf("result line %q", out.buf.String())
	}

	// -v shows Nix's lines as well.
	c = configOpServer(t, configBuildLines, `{"state":"done"}`, false)
	errOut = &discardWriter{}
	e = &Env{Client: c, Out: &discardWriter{}, ErrOut: errOut, TTY: true, Verbose: true}
	if _, _, err := waitConfigOp(context.Background(), e, p, "o"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.buf.String(), "nix › these 2 derivations will be built:") || !strings.Contains(errOut.buf.String(), "✓ Built 2 derivations") {
		t.Fatalf("-v output:\n%s", errOut.buf.String())
	}
}

// A kernel change on a running machine is built, not switched to: the
// result says so instead of "Applied" (I-323).
func TestConfigOpRebootRequired(t *testing.T) {
	c := configOpServer(t, configBuildLines[:len(configBuildLines)-1], `{"state":"done","reboot_required":true}`, false)
	out := &discardWriter{}
	e := &Env{Client: c, Out: out, ErrOut: &discardWriter{}}
	p := &Project{ID: "p", Slug: "todo-app"}
	op, pr, err := waitConfigOp(context.Background(), e, p, "o")
	if err != nil {
		t.Fatal(err)
	}
	printApplied(e, p, op, "0199b1c2-0000-7000-8000-00004f1c2a9e", pr)
	if got := out.buf.String(); !strings.HasPrefix(got, "Built revision 4f1c2a9e. It changes the kernel") || strings.Contains(got, "Applied") {
		t.Fatalf("reboot output %q", got)
	}
}

// Ctrl-C during a build says the build goes on and how to see it land,
// and exits 130.
func TestConfigOpInterrupted(t *testing.T) {
	c := configOpServer(t, configBuildLines[:3], `{"state":"done"}`, true)
	errOut := &discardWriter{}
	e := &Env{Client: c, Out: &discardWriter{}, ErrOut: errOut}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	_, _, err := waitConfigOp(ctx, e, &Project{ID: "p", Slug: "todo-app"}, "o")
	if exitCode(err) != ExitInterrupted {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(errOut.buf.String(), "Interrupted. The build keeps going on the machine.") {
		t.Fatalf("stderr %q", errOut.buf.String())
	}
}

// `repose config apply` with no file applies the active revision again
// (I-321).
func TestConfigApplyWithoutFileReapplies(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	if err := ConfigAddCmd(ctx, e, "", []string{"gcc"}); err != nil {
		t.Fatal(err)
	}
	revs, err := e.Client.ListRevisions(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The active revision. Its id differs from the newest one only in
	// the tail, which shortRev shows since I-616; the first 8 characters
	// it cut before were the same for every revision.
	var cfg struct{ RevisionID string }
	for _, r := range revs {
		if r.Status == "applied" {
			cfg.RevisionID = r.ID
			break
		}
	}
	out := &discardWriter{}
	e.Out = out
	if err := ConfigApplyCmd(ctx, e, "", ""); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.buf.String())
	}
	got := out.buf.String()
	if !strings.HasPrefix(got, "No ./repose.nix here; applying the active revision "+shortRev(cfg.RevisionID)+" again.\n") || !strings.Contains(got, "Applied revision "+shortRev(cfg.RevisionID)) {
		t.Fatalf("output:\n%s", got)
	}
}

// Nix's singular lines and KiB sizes are read too.
func TestBuildViewSingular(t *testing.T) {
	v := &buildView{}
	v.Line("building x")
	v.Line("this derivation will be built:")
	v.Line("this path will be fetched (10.3 KiB download, 40.1 KiB unpacked):")
	if v.drvN != 1 || v.fetchN != 1 || v.fetchSize != "10.3 KiB" || v.stage != stageFetch {
		t.Fatalf("%+v", v)
	}
	if l, d := v.fetchLabel(); l != "Fetching 0/1 path (10.3 KiB)" || d != "Fetched 1 path (10.3 KiB)" {
		t.Fatalf("%q %q", l, d)
	}
}

// A build line from an api before I-322 has no time and prints none;
// --since takes a duration.
func TestLogLinesAndSince(t *testing.T) {
	if got := logLineText(LogLine{Line: "evaluating configuration"}, "build"); got != "build evaluating configuration" {
		t.Fatalf("%q", got)
	}
	ts := time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)
	if got := logLineText(LogLine{TS: ts, Kind: "build", Line: "x"}, "build"); got != ts.Local().Format(time.RFC3339)+" build x" {
		t.Fatalf("%q", got)
	}
	if got, err := parseSince("1h", ts); err != nil || apiSince(got) != "2026-09-28T00:02:03Z" {
		t.Fatalf("1h: %q %v", apiSince(got), err)
	}
	if got, err := parseSince("2026-09-27T00:00:00Z", ts); err != nil || apiSince(got) != "2026-09-27T00:00:00Z" {
		t.Fatalf("time: %q %v", apiSince(got), err)
	}
}

// A create that builds shows the build's steps and then the boot as its
// own step, instead of one line for both (DECISIONS I-708). The stub
// answers as the real api does: GET /projects/:id carries no op_id (only
// the create's 201 does), the project moves building, starting, running,
// and the create's log carries nixbuild's lines and stays open through
// the boot. run reaches the wait through ensureRunningFrom, which reads
// the project again after the create.
func TestCreateShowsTheBuildThenTheBoot(t *testing.T) {
	var opReads, projReads atomic.Int32
	var booting atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/p", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state := "building"
		if projReads.Add(1) > 4 {
			booting.Store(true) // a client that never reads the log still sees the boot end
		}
		switch {
		case booting.Load() && projReads.Load() > 2:
			state = "running"
		case booting.Load():
			state = "starting"
		}
		_, _ = fmt.Fprintf(w, `{"id":"p","slug":"todo-app","state":%q,"op_id":null}`, state)
	})
	mux.HandleFunc("/v1/projects/p/ops/o", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := opReads.Add(1)
		switch {
		case !booting.Load():
			_, _ = io.WriteString(w, `{"op_id":"o","kind":"create","state":"running","phase":"build","project_state":"building","log_url":"/v1/projects/p/ops/o/log"}`)
		case n < 4:
			_, _ = io.WriteString(w, `{"op_id":"o","kind":"create","state":"running","phase":"create_guest","project_state":"starting","log_url":"/v1/projects/p/ops/o/log"}`)
		default:
			_, _ = io.WriteString(w, `{"op_id":"o","kind":"create","state":"done","project_state":"running"}`)
		}
	})
	// A config op's lines without its switch: a create's build log is
	// the same nixbuild output, and the boot follows it.
	lines := configBuildLines[:len(configBuildLines)-1]
	mux.HandleFunc("/v1/projects/p/ops/o/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i, l := range lines {
			_, _ = fmt.Fprintf(w, "id: %d\ndata: {\"seq\":%d,\"line\":%q,\"ts\":\"2026-10-13T01:02:03.456Z\"}\n\n", i+1, i+1, l)
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		booting.Store(true)
		time.Sleep(50 * time.Millisecond) // the boot, with the log open
		_, _ = io.WriteString(w, "event: done\ndata: {\"state\":\"done\"}\n\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := newClient(srv.URL+"/v1", staticToken("t"))

	for _, tty := range []bool{true, false} {
		opReads.Store(0)
		projReads.Store(0)
		booting.Store(false)
		errOut := &discardWriter{}
		e := &Env{Client: c, Out: &discardWriter{}, ErrOut: errOut, TTY: tty}
		pr := newProgress(errOut, tty)
		// As createProjectForRun leaves it: the 201's state and op id.
		p := &Project{ID: "p", Slug: "todo-app", State: "creating", OpID: "o"}
		pr.Phase("Creating todo-app", "Created todo-app (large)")
		if err := ensureRunningFrom(context.Background(), e, p, pr, false); err != nil {
			t.Fatalf("tty=%v: %v", tty, err)
		}
		pr.End()
		got := errOut.buf.String()
		t.Logf("tty=%v:\n%s", tty, got)
		last := -1
		for _, want := range []string{"✓ Created todo-app (large)", "✓ Got a build slot", "✓ Evaluated your config", "✓ Fetched 3 paths", "✓ Built 2 derivations", "✓ Booted todo-app"} {
			i := strings.Index(got, want)
			if i < 0 || i < last {
				t.Fatalf("tty=%v: step %q missing or out of order in:\n%s", tty, want, got)
			}
			last = i
		}
		if strings.Contains(got, "Built the configuration") {
			t.Fatalf("tty=%v: the state's label covers the build's steps:\n%s", tty, got)
		}
		if n := strings.Count(got, "✓ Booted todo-app"); n != 1 {
			t.Fatalf("tty=%v: %d Booted lines:\n%s", tty, n, got)
		}
		if tty && strings.Contains(got, "nix ›") {
			t.Fatalf("nix lines shown on a terminal without -v:\n%s", got)
		}
	}
}

// A create whose build finds everything in the host's store prints none
// of Nix's lists, so no fetch or build step ends with a line. The build
// still gets its own line before the boot, or "Booted" would carry the
// build's time and the screen would show less than before I-708.
func TestCreateBuildWithNothingToFetch(t *testing.T) {
	for _, tty := range []bool{true, false} {
		out := &discardWriter{}
		pr := newProgress(out, tty)
		v := &buildView{pr: pr}
		v.built = func() { pr.Phase("Booting todo-app", "Booted todo-app") }
		for _, l := range []string{"evaluating configuration", "building nixos-system-todo-app", "built /nix/store/ffff-nixos-system-todo-app"} {
			v.Line(l)
		}
		pr.End()
		got := out.buf.String()
		last := -1
		for _, want := range []string{"✓ Evaluated your config", "✓ Built the configuration", "✓ Booted todo-app"} {
			i := strings.Index(got, want)
			if i < 0 || i < last {
				t.Fatalf("tty=%v: step %q missing or out of order in:\n%s", tty, want, got)
			}
			last = i
		}
	}
}

// I-755: repose's own step lines are the step and nothing else. Each was
// also copied to the raw stream with the `nix › ` prefix, so without a
// terminal every step showed twice — the copy first, labelled as Nix's
// output, which it never was.
func TestStepLinesAreNotPrintedAsNixOutput(t *testing.T) {
	out := &discardWriter{}
	pr := newProgress(out, false) // no terminal: one line per step
	v := &buildView{pr: pr, raw: pr}
	for _, l := range configBuildLines {
		v.Line(l)
	}
	pr.End()
	got := out.buf.String()
	t.Logf("no terminal:\n%s", got)
	// The step lines of docs/interfaces/api.md "Build log lines" that
	// configBuildLines holds; none of them is Nix's output.
	for _, l := range []string{
		"waiting for a build slot",
		"evaluating configuration",
		"building nixos-system-todo-app",
		"built /nix/store/ffff-nixos-system-todo-app",
		"switching the machine",
	} {
		if strings.Contains(got, "nix › "+l) {
			t.Errorf("step line %q printed as Nix's output:\n%s", l, got)
		}
	}
	if n := strings.Count(got, "Evaluating your config"); n != 1 {
		t.Errorf("the evaluate step shows %d times, want once:\n%s", n, got)
	}
	// Nix's own lines still go to raw, after the step they belong to.
	step := strings.Index(got, "Fetching 0/3 paths")
	nix := strings.Index(got, "nix › these 3 paths will be fetched")
	if step < 0 || nix < step {
		t.Errorf("Nix's fetch list does not follow its step:\n%s", got)
	}
}
