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
	writeGitRowsOr(&b, nil, time.Now(), "ssh_timeout", "")
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

	// The project list answers a second late, as from a far laptop.
	u, _ := url.Parse(fake.URL())
	proxy := httputil.NewSingleHostReverseProxy(u)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/projects" {
			time.Sleep(time.Second)
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
		time.Sleep(time.Second)
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
	if took > 1800*time.Millisecond {
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
