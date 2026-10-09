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
	// The revision reapplied is the newest that is applied, as
	// reapplyRevision reads the list; shortRev shows each id's random end
	// since I-619, so it must be that one.
	revs, err := e.Client.ListRevisions(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
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
	if got := sinceArg("1h", ts); got != "2026-09-28T00:02:03Z" {
		t.Fatalf("1h: %q", got)
	}
	if got := sinceArg("2026-09-27T00:00:00Z", ts); got != "2026-09-27T00:00:00Z" {
		t.Fatalf("time: %q", got)
	}
}
