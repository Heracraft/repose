//go:build !windows

package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// fakeSSHAnswer puts an ssh on PATH whose session sleeps delay and then
// answers the status script with one worktree holding one file not
// committed. With master, `-O check` finds a master whose sessions hang
// until `-O stop` (one whose connection died while the laptop slept);
// without, there is no master.
func fakeSSHAnswer(t *testing.T, delay string, master bool) {
	t.Helper()
	bin := t.TempDir()
	stopped := filepath.Join(bin, "stopped")
	check, hang := "exit 255", ""
	if master {
		check, hang = "exit 0", "[ -e "+stopped+" ] || exec sleep 30"
	}
	script := `#!/bin/sh
if [ "$1" = -O ]; then
  case "$2" in
  stop) : > ` + stopped + `; exit 0 ;;
  check) ` + check + ` ;;
  esac
  exit 0
fi
cat >/dev/null
` + hang + `
sleep ` + delay + `
printf '#ps\n#df\n#mux\n#git\nwt\tjob\tmain\t-\t1\t\n'
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// I-740: a cold handshake from a laptop took 1.2 to 3.2 s on prod, and
// with the script the read passed status's 4 s in 4 of 13 stops, which
// then said nothing about the file not committed. The stop's read has
// its own budget.
func TestStopProbeOutlastsTheStatusBudget(t *testing.T) {
	fakeSSHAnswer(t, "4.5", false)
	ctx := context.Background()
	if g := guestStatusReadIn(ctx, hostTarget("job"), "job", nil, statusProcsTimeout); g.probeErr != "ssh_timeout" {
		t.Fatalf("status's read of a 4.5 s answer: %+v", g)
	}
	e := &Env{}
	g := e.readGuestFor(ctx, "job", nil, stopProbeTimeout)
	if g.probeErr != "" || len(g.git) != 1 || g.git[0].Uncommitted == nil || *g.git[0].Uncommitted != 1 {
		t.Fatalf("the stop's read of a 4.5 s answer: %+v", g)
	}
	if c := stopClause(g); c != "1 file not committed" {
		t.Fatalf("clause %q", c)
	}
}

// A master whose connection died would hold the stop's read for its
// whole budget: it is stopped first and the read goes cold.
func TestStopProbeSkipsADeadMaster(t *testing.T) {
	fakeSSHAnswer(t, "0", true)
	started := time.Now()
	g := (&Env{}).readGuestFor(context.Background(), "job", nil, stopProbeTimeout)
	if g.probeErr != "" || len(g.git) != 1 {
		t.Fatalf("read past a dead master: %+v", g)
	}
	if d := time.Since(started); d > masterProbeTimeout+3*time.Second {
		t.Fatalf("read past a dead master took %s", d)
	}
}

// When the machine does not answer, the line says so instead of reading
// as a clean checkout, in status's words.
func TestStopLineSaysWhenTheCheckoutIsUnknown(t *testing.T) {
	if l := stoppingLine("job", guestStatus{probeErr: "ssh_timeout"}); l != "Stopping job; checkout unknown (no ssh answer in 15 s)." {
		t.Fatalf("timeout %q", l)
	}
	if l := stoppingLine("job", guestStatus{probeErr: "ssh_failed", probeDetail: "Connection refused"}); l != "Stopping job; checkout unknown (ssh failed: Connection refused)." {
		t.Fatalf("failed %q", l)
	}
	var b bytes.Buffer
	writeGitRowsOr(&b, nil, time.Now(), "ssh_timeout", "", 0)
	if b.String() != "  checkout   unknown (no ssh answer in 4 s)\n" {
		t.Fatalf("status row %q", b.String())
	}
}

// The read of a machine named on the command line starts beside the
// api's read of the project, not after it.
func TestStopProbeStartsBesideTheApi(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{StopDelay: 3 * time.Second, StopPickup: 100 * time.Millisecond})
	defer fake.Close()
	ctx := context.Background()
	e := newLifecycleEnv(t, fake)
	runningProject(t, fake, e, "job")

	// The project list answers two seconds late, as from a far laptop.
	u, _ := url.Parse(fake.URL())
	proxy := httputil.NewSingleHostReverseProxy(u)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/projects" {
			time.Sleep(2 * time.Second)
		}
		proxy.ServeHTTP(w, r)
	}))
	defer slow.Close()
	e.Client = newClient(slow.URL+"/v1", staticToken("tok"))

	var mu sync.Mutex
	reads := 0
	n := 1
	e.readGuest = func(ctx context.Context, slug string, have []string, budget time.Duration) guestStatus {
		mu.Lock()
		reads++
		mu.Unlock()
		if budget != stopProbeTimeout {
			t.Errorf("budget %s", budget)
		}
		time.Sleep(2 * time.Second)
		return guestStatus{mux: "tmux", git: []gitRow{{Worktree: "job", Branch: "main", Uncommitted: &n}}}
	}
	var out bytes.Buffer
	e.Out = &out
	begun := time.Now()
	if err := StopProjectsCmd(ctx, e, StopOptions{Projects: []string{"job"}, Snapshot: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(begun)
	if out.String() != "Stopping job; 1 file not committed.\n" {
		t.Fatalf("out %q", out.String())
	}
	if reads != 1 {
		t.Fatalf("%d reads of the machine", reads)
	}
	// In turn the two take 4 s; beside each other, about 2.2 s.
	if took > 3*time.Second {
		t.Fatalf("stop took %s; the read waited for the api", took)
	}
}

// An early read answered another question when the laptop's commits
// changed since (the fetch brought some) or it is old: it is read again.
func TestStopProbeEarlyAnswerIsCheckedBeforeUse(t *testing.T) {
	e := &Env{Cwd: t.TempDir(), Cache: newProjectsCache()}
	reads := 0
	e.readGuest = func(ctx context.Context, slug string, have []string, budget time.Duration) guestStatus {
		reads++
		return guestStatus{mux: "tmux"}
	}
	p := &Project{ID: "id-1", Slug: "job", State: "running"}
	ctx := context.Background()
	done := func(have []string, at time.Time) map[string]*stopProbe {
		sp := &stopProbe{have: have, started: at, done: make(chan struct{})}
		close(sp.done)
		return map[string]*stopProbe{"job": sp}
	}
	e.stopEarly = done(nil, time.Now())
	probeBeforeStop(ctx, e, p)
	if reads != 0 {
		t.Fatalf("the early answer to the same question was read again (%d)", reads)
	}
	e.stopEarly = done([]string{"0123456789012345678901234567890123456789"}, time.Now())
	probeBeforeStop(ctx, e, p)
	if reads != 1 {
		t.Fatalf("an early answer for other commits was used (%d)", reads)
	}
	e.stopEarly = done(nil, time.Now().Add(-time.Minute))
	probeBeforeStop(ctx, e, p)
	if reads != 2 {
		t.Fatalf("a minute-old answer was used (%d)", reads)
	}
	p.State = "stopping"
	if g := probeBeforeStop(ctx, e, p); reads != 2 || g.probeErr != "" || g.git != nil {
		t.Fatalf("a machine not running was read: %+v (%d)", g, reads)
	}
}

// The script reads stdin only when the laptop sends its commits. With
// nothing to send, ssh's EOF reached the guest's sshd before the exec
// that the gateway relays beside it, the child's stdin never closed,
// and `cat` waited out the read's whole budget: 3 of 8 reads made 3 s
// after a start on prod, 2026-10-14 (I-740).
func TestStatusScriptReadsStdinOnlyWithCommits(t *testing.T) {
	if s := statusScript("job", false); strings.Contains(s, "$(cat)") || !strings.HasPrefix(s, "repose_have=\n") {
		t.Fatalf("script without commits reads stdin:\n%.80s", s)
	}
	if s := statusScript("job", true); !strings.HasPrefix(s, "repose_have=$(cat)\n") {
		t.Fatalf("script with commits does not read them:\n%.80s", s)
	}
}

// fakeSSHScript puts an ssh on PATH that runs body for every session
// and answers `-O` control commands as master says: "dead" is a master
// whose sessions hang until `-O stop`, "" is no master.
func fakeSSHScript(t *testing.T, master, body string) {
	t.Helper()
	bin := t.TempDir()
	stopped := filepath.Join(bin, "stopped")
	check, hang := "exit 255", ""
	if master == "dead" {
		check, hang = "exit 0", "[ -e "+stopped+" ] || exec sleep 30"
	}
	script := "#!/bin/sh\nif [ \"$1\" = -O ]; then\n  case \"$2\" in\n  stop) : > " + stopped + "; exit 0 ;;\n  check) " + check + " ;;\n  esac\n  exit 0\nfi\n" + hang + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The budget the line names bounds the whole read, the dead master's
// check included: with a dead master and a machine that never answers,
// the read used to wait the master's 2 s and then the full budget.
func TestStopProbeBudgetCoversTheMasterCheck(t *testing.T) {
	fakeSSHScript(t, "dead", "exec sleep 30")
	started := time.Now()
	g := (&Env{}).readGuestFor(context.Background(), "job", nil, 3*time.Second)
	took := time.Since(started)
	if g.probeErr != "ssh_timeout" {
		t.Fatalf("read of a machine that never answers: %+v", g)
	}
	if took > 3800*time.Millisecond {
		t.Fatalf("a 3 s read took %s", took)
	}
	if c := stopClause(g); c != "checkout unknown (no ssh answer in 3 s)" {
		t.Fatalf("clause %q", c)
	}
}

// When ssh's own ConnectTimeout gives up first, the line names the time
// the stop waited, not the budget it would have waited.
func TestStopProbeNamesTheTimeItWaited(t *testing.T) {
	fakeSSHScript(t, "", "sleep 1\necho 'Connection timed out during banner exchange' >&2\nexit 255")
	g := (&Env{}).readGuestFor(context.Background(), "job", nil, stopProbeTimeout)
	if c := stopClause(g); c != "checkout unknown (no ssh answer in 1 s)" {
		t.Fatalf("clause %q (%+v)", c, g)
	}
}

// status's read stops a dead master too, so status and the stop read a
// machine the same way after the laptop slept.
func TestStatusReadSkipsADeadMaster(t *testing.T) {
	fakeSSHAnswer(t, "0", true)
	g := guestStatusRead(context.Background(), hostTarget("job"), "job", nil)
	if g.probeErr != "" || len(g.git) != 1 {
		t.Fatalf("status's read past a dead master: %+v", g)
	}
}

// A Ctrl-C while the stop waits for its early read is not an ssh
// timeout, and the line says nothing about the checkout.
func TestStopProbeInterruptedIsNotATimeout(t *testing.T) {
	e := &Env{Cwd: t.TempDir(), Cache: newProjectsCache()}
	p := &Project{ID: "id-1", Slug: "job", State: "running"}
	e.stopEarly = map[string]*stopProbe{"job": {started: time.Now(), done: make(chan struct{})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := probeBeforeStop(ctx, e, p)
	if g.probeErr != "interrupted" || stopClause(g) != "" {
		t.Fatalf("interrupted read: %+v, clause %q", g, stopClause(g))
	}
	dir := t.TempDir()
	saveStopLeft(dir, p.ID, g, time.Now())
	p.State = "stopped"
	if _, ok := stoppedRows(dir, p); ok {
		t.Fatal("an interrupted read was kept for status")
	}
}

// After a stop that could not read the machine, status on the stopped
// machine says the checkout is unknown, as the stop line did, instead
// of showing no checkout row.
func TestStatusAfterAStopThatReadNothing(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-9 * time.Hour)
	saveStopLeft(dir, "id-1", guestStatus{probeErr: "ssh_timeout", waited: stopProbeTimeout}, at)
	p := &Project{ID: "id-1", Slug: "job", State: "stopped", Class: "large"}
	l, ok := stoppedRows(dir, p)
	if !ok {
		t.Fatal("no entry for a stop that read nothing")
	}
	var b bytes.Buffer
	writeStatus(&b, statusView{p: p, mux: "tmux", now: time.Now(), left: l, hasLeft: true})
	if !strings.Contains(b.String(), "\n  checkout   unknown at the stop, 9h ago (no ssh answer in 15 s)\n") {
		t.Fatalf("status:\n%s", b.String())
	}
}
