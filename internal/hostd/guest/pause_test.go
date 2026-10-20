package guest

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/ch"
)

// pauseOK pauses the guest through the command path and fails the test if
// it was refused.
func (h *harness) pauseOK(id string) *hostdv1.PauseResult {
	h.t.Helper()
	res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: id, Reason: PauseReasonIdle}))
	p := res.GetPause()
	if p == nil || !p.Paused {
		h.t.Fatalf("pause %s: %+v", id, p)
	}
	return p
}

func (h *harness) restingEvents(id string) []bool {
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	var out []bool
	for _, e := range h.rec.events {
		if r := e.GetGuestResting(); r != nil && r.GuestId == id {
			out = append(out, r.Resting)
		}
	}
	return out
}

// A pause stops the guest's vCPUs, marks the record, raises GuestResting
// and leaves the state `running`: `resting` is a marker, never an
// eleventh state (S2 of docs/workstreams/18-idle-pause.md).
func TestPauseMarksAndDoesNotChangeState(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	before := len(h.rec.states(gid1))
	p := h.pauseOK(gid1)
	if p.At == 0 {
		t.Fatalf("pause result carries no instant: %+v", p)
	}
	g := h.guest(gid1)
	if g.Resting == nil || g.State != StateRunning {
		t.Fatalf("guest record: state %q resting %+v", g.State, g.Resting)
	}
	if got := h.restingEvents(gid1); len(got) != 1 || !got[0] {
		t.Fatalf("guest_resting events %v, want [true]", got)
	}
	if got := h.rec.states(gid1); len(got) != before {
		t.Fatalf("a pause emitted a state change: %v", got)
	}
	if n := testutil.ToFloat64(h.metrics.GuestsResting); n != 1 {
		t.Fatalf("repose_host_guests_resting = %v, want 1", n)
	}
	if n := testutil.ToFloat64(h.metrics.GuestPause.WithLabelValues("idle", "paused")); n != 1 {
		t.Fatalf("pause_total{idle,paused} = %v, want 1", n)
	}
}

// Every refusal is `ok` with paused=false and a lower_snake why_not: a
// machine with work in flight is a refusal, not a failure.
func TestPauseRefusals(t *testing.T) {
	t.Run("not_running", func(t *testing.T) {
		h := newHarness(t, nil)
		h.create(gid1)
		h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
		res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: gid1}))
		if p := res.GetPause(); p.Paused || p.WhyNot != whyNotRunning {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("already_resting", func(t *testing.T) {
		h := newHarness(t, nil)
		h.create(gid1)
		h.pauseOK(gid1)
		res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: gid1}))
		if p := res.GetPause(); p.Paused || p.WhyNot != whyAlreadyResting {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("host_stopping", func(t *testing.T) {
		h := newHarness(t, nil)
		h.create(gid1)
		h.m.SetShuttingDown()
		res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: gid1}))
		if p := res.GetPause(); p.Paused || p.WhyNot != whyHostStopping {
			t.Fatalf("%+v", p)
		}
		if g := h.guest(gid1); g.Resting != nil {
			t.Fatal("a refused pause left a mark")
		}
	})
	t.Run("command_in_flight", func(t *testing.T) {
		h := newHarness(t, nil)
		h.create(gid1)
		// An admitted command for this guest, queued on its worker and
		// not finished: the api has work for the machine.
		other := cmd(&hostdv1.SetPrincipals{GuestId: gid1, Principals: []string{"p"}})
		if _, proceed := h.m.admit(other); !proceed {
			t.Fatal("the other command was not admitted")
		}
		defer h.m.clearInflight(other.CommandId)
		res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: gid1}))
		if p := res.GetPause(); p.Paused || p.WhyNot != whyCommandInFlight {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("timer_due", func(t *testing.T) {
		h := newHarness(t, nil)
		due := time.Now().Add(2 * time.Minute)
		h.gopts.OnExec = timerExec(due)
		h.create(gid1)
		res := h.mustOK(cmd(&hostdv1.PauseGuest{GuestId: gid1}))
		if p := res.GetPause(); p.Paused || p.WhyNot != whyTimerDue {
			t.Fatalf("%+v", p)
		}
		if n := testutil.ToFloat64(h.metrics.GuestPause.WithLabelValues("idle", "refused")); n != 1 {
			t.Fatalf("pause_total{idle,refused} = %v, want 1", n)
		}
	})
}

// timerExec answers the pause's read of the next timer elapse with one
// realtime elapse at t, in the shape `systemctl show` prints.
func timerExec(t time.Time) func([]string) *guestdv1.ExecResult {
	return func(argv []string) *guestdv1.ExecResult {
		if len(argv) < 3 || !strings.Contains(argv[2], ".timer") {
			return nil
		}
		out := "NextElapseUSecRealtime=0\nNextElapseUSecRealtime=" +
			strconv.FormatInt(t.UnixMicro(), 10) + "\n"
		return &guestdv1.ExecResult{ExitCode: 0, Stdout: []byte(out)}
	}
}

// A timer further out than the hold does not stop the pause, and the
// elapse is recorded so the wake can be armed on it.
func TestPauseRecordsTheNextTimer(t *testing.T) {
	h := newHarness(t, nil)
	due := time.Now().Add(90 * time.Minute).Truncate(time.Microsecond)
	h.gopts.OnExec = timerExec(due)
	h.create(gid1)
	h.pauseOK(gid1)
	g := h.guest(gid1)
	if g.Resting == nil || !g.Resting.NextTimer.Equal(due) {
		t.Fatalf("next timer %v, want %v", g.Resting, due)
	}
}

// A wake is idempotent: a machine already awake is answered ok with
// was_resting false, and a resting one comes back with wake_ms and the
// mark cleared.
func TestWakeIsIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	res := h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "attach"}))
	if w := res.GetWake(); w == nil || w.WasResting {
		t.Fatalf("wake of a running guest: %+v", w)
	}
	h.pauseOK(gid1)
	res = h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "attach"}))
	w := res.GetWake()
	if w == nil || !w.WasResting {
		t.Fatalf("wake of a resting guest: %+v", w)
	}
	if g := h.guest(gid1); g.Resting != nil {
		t.Fatalf("the mark survived the wake: %+v", g.Resting)
	}
	// Again: nothing to resume, and still ok.
	res = h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "attach"}))
	if w := res.GetWake(); w == nil || w.WasResting {
		t.Fatalf("second wake: %+v", w)
	}
	if got := h.restingEvents(gid1); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("guest_resting events %v, want [true false]", got)
	}
	if !h.scrape(t).has("repose_host_guest_wake_seconds_count 1") {
		t.Fatal("the wake was not measured into repose_host_guest_wake_seconds")
	}
	if n := testutil.ToFloat64(h.metrics.GuestsResting); n != 0 {
		t.Fatalf("repose_host_guests_resting = %v, want 0", n)
	}
}

// A wake never waits for an op slot: it is what a user is waiting for.
// With the host's only slot held by other work, the wake still answers.
func TestWakeTakesNoOpSlot(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxOps = 1 })
	h.create(gid1)
	h.pauseOK(gid1)
	hold := make(chan struct{})
	held := make(chan struct{})
	h.m.enqueue("host", job{internal: func(context.Context) {
		close(held)
		<-hold
	}})
	<-held // the only op slot is taken
	defer close(hold)
	h.m.Dispatch(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: "attach"}))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if g := h.guest(gid1); g.Resting == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the wake never ran while the host's op slot was held")
}

// S4: a resting guest's sample keeps guestd_ok true, repeats the last
// signals the guest wrote, carries the resting marker and leaves the
// state `running`. Everything that reads `not guestd_ok` as use -- the
// unused notice, `repose start`, the dashboard's Start, the guestd_lost
// gauge -- depends on this.
func TestRestingSampleKeepsGuestdOk(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	// One real sample first, so there are signals to repeat.
	h.m.CollectSamples(context.Background())
	h.pauseOK(gid1)
	// After the pause: the pause itself reads one fresh sample, to refuse
	// a session that appeared since the api's own read (I-818). What this
	// test is about is the SAMPLER, which must ask a paused guest
	// nothing.
	calls := len(h.guestd(gid1).Calls())
	s := h.m.CollectSamples(context.Background())
	var gsm *hostdv1.GuestSample
	for _, g := range s.Guests {
		if g.GuestId == gid1 {
			gsm = g
		}
	}
	if gsm == nil {
		t.Fatal("no sample for the guest")
	}
	if !gsm.Resting {
		t.Fatal("the sample does not say resting")
	}
	if gsm.State != StateRunning {
		t.Fatalf("sample state %q, want running", gsm.State)
	}
	if !gsm.Signals.GuestdOk {
		t.Fatal("guestd_ok is false for a resting guest")
	}
	if len(gsm.Signals.Agents) != 1 || gsm.Signals.Agents[0].Agent != "claude" {
		t.Fatalf("the last signals were not repeated: %+v", gsm.Signals)
	}
	if n := testutil.ToFloat64(h.metrics.GuestdLost); n != 0 {
		t.Fatalf("repose_host_guestd_lost = %v for a resting guest", n)
	}
	// No vsock Sample was asked of a paused guest: that call would have
	// waited out its own 10 s, in series, against a 60 s tick.
	for _, c := range h.guestd(gid1).Calls()[calls:] {
		if c.Kind == "Sample" {
			t.Fatal("the sampler called Sample on a resting guest")
		}
	}
}

// A resting guest survives a hostd restart: the mark is in bbolt, the
// reconcile confirms it against vm.info, and Hello says so.
func TestRestingSurvivesAHostdRestart(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.pauseOK(gid1)
	h.restart()
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	g := h.guest(gid1)
	if g.Resting == nil || g.State != StateRunning {
		t.Fatalf("after the restart: state %q resting %+v", g.State, g.Resting)
	}
	var found *hostdv1.GuestStatus
	for _, gs := range h.m.Hello().Guests {
		if gs.GuestId == gid1 {
			found = gs
		}
	}
	if found == nil || found.RestingAt == 0 || found.State != StateRunning {
		t.Fatalf("Hello: %+v", found)
	}
	if !h.m.Resting(gid1) {
		t.Fatal("the manager's own map lost the mark")
	}
}

// A guest paused with no mark is adopted rather than lost: hostd killed
// between vm.pause returning and the record being written leaves a guest
// whose vCPUs are stopped and whose bbolt row says plain `running`. Left
// that way nothing ever resumes it, the sampler asks it for a sample that
// cannot come, and the user's `repose start` cold-boots it -- which is
// every process the pause was keeping (DECISIONS I-824).
func TestReconcileAdoptsAPausedGuestWithNoMark(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	// The pause, without the record: exactly what a kill in that window
	// leaves behind.
	if err := h.chc.Pause(context.Background(), chSock(h, gid1)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	h.restart()
	if g := h.guest(gid1); g.Resting != nil {
		t.Fatalf("the record was written after all: %+v", g.Resting)
	}
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	g := h.guest(gid1)
	if g.Resting == nil {
		t.Fatal("reconcile left a paused guest unmarked; nothing would ever wake it")
	}
	if g.State != StateRunning {
		t.Fatalf("state %q, want running: resting is a marker, not a state", g.State)
	}
	if !h.m.Resting(gid1) {
		t.Fatal("the manager's own map did not take the adopted mark")
	}
	// And it is wakeable, which is the whole point of adopting it.
	h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: WakeReasonCommand}))
	if h.m.Resting(gid1) {
		t.Fatal("the adopted guest did not wake")
	}
}

// A wake whose resume worked but whose guestd never answered still clears
// the mark: the vCPUs are running, so the guest is not resting, and a mark
// left behind makes the sampler repeat the pre-pause signals for a running
// guest for ever and the api call a working machine frozen (I-824).
func TestWakeClearsTheMarkEvenWhenGuestdDoesNotAnswer(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.pauseOK(gid1)
	h.guestd(gid1).Close() // the resume works; nothing answers after it
	res := h.run(cmd(&hostdv1.WakeGuest{GuestId: gid1, Reason: WakeReasonConnect}))
	if res.Ok {
		t.Fatal("a wake with no answer from the guest must report a failure")
	}
	if h.m.Resting(gid1) {
		t.Fatal("the mark survived a resume that worked")
	}
	if g := h.guest(gid1); g.Resting != nil {
		t.Fatalf("the record kept the mark: %+v", g.Resting)
	}
}

// A mark the hypervisor contradicts goes: something resumed the guest
// (a freeze's fallback, a `hostd thaw`), and a mark nothing cleared
// would silence that guest's samples for good.
func TestReconcileClearsAMarkTheHypervisorContradicts(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.pauseOK(gid1)
	// The guest runs again, with its mark still on the record.
	if err := h.chc.Resume(context.Background(), chSock(h, gid1)); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if g := h.guest(gid1); g.Resting != nil {
		t.Fatalf("the stale mark survived: %+v", g.Resting)
	}
	if h.m.Resting(gid1) {
		t.Fatal("the manager's map kept the stale mark")
	}
}

// hostd wakes the guest itself before anything that needs guestd, so no
// command ever meets a paused guest.
func TestHostdWakesBeforeEveryGuestCall(t *testing.T) {
	cases := map[string]func(h *harness){
		"secrets": func(h *harness) {
			h.mustOK(cmd(&hostdv1.UpdateSecrets{GuestId: gid1, Secrets: []*hostdv1.Secret{{Name: "K", Value: []byte("v")}}}))
		},
		"principals": func(h *harness) {
			h.mustOK(cmd(&hostdv1.SetPrincipals{GuestId: gid1, Principals: []string{"p"}}))
		},
		"exec": func(h *harness) {
			h.mustOK(cmd(&hostdv1.Exec{GuestId: gid1, Argv: []string{"true"}, AuditId: "a-1", TimeoutS: 5}))
		},
		"apply": func(h *harness) {
			h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: h.closure}))
		},
		"snapshot": func(h *harness) {
			h.mustOK(cmd(&hostdv1.Snapshot{GuestId: gid1, Reason: "manual"}))
		},
		"resize": func(h *harness) {
			h.mustOK(cmd(&hostdv1.ResizeVolume{GuestId: gid1, NewBytes: 50 << 30}))
		},
		"start": func(h *harness) {
			h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
		},
	}
	for name, act := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.create(gid1)
			h.pauseOK(gid1)
			act(h)
			if g := h.guest(gid1); g.Resting != nil {
				t.Fatalf("%s met a resting guest and left it resting: %+v", name, g.Resting)
			}
			if got := h.restingEvents(gid1); len(got) < 2 || got[len(got)-1] {
				t.Fatalf("%s: guest_resting events %v, want a false at the end", name, got)
			}
		})
	}
}

// A stop wakes the guest first, so it processes the shutdown instead of
// paying the whole stop timeout.
func TestStopWakesTheGuestFirst(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.pauseOK(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if g := h.guest(gid1); g.State != StateStopped || g.Resting != nil {
		t.Fatalf("after the stop: state %q resting %+v", g.State, g.Resting)
	}
}

// The clock the guest reads is the host's after a wake: its own stopped
// with its vCPUs.
func TestWakeSetsTheGuestClock(t *testing.T) {
	h := newHarness(t, nil)
	var mu sync.Mutex
	var dates [][]string
	h.gopts.OnExec = func(argv []string) *guestdv1.ExecResult {
		if len(argv) == 3 && argv[0] == "/bin/sh" && strings.Contains(argv[2], "date ") {
			mu.Lock()
			dates = append(dates, argv)
			mu.Unlock()
		}
		return nil
	}
	h.create(gid1)
	h.pauseOK(gid1)
	h.mustOK(cmd(&hostdv1.WakeGuest{GuestId: gid1}))
	mu.Lock()
	defer mu.Unlock()
	if len(dates) != 1 {
		t.Fatalf("clock calls %v, want one", dates)
	}
	// The step itself, as the freeze writes it.
	if !regexp.MustCompile(`date -u -s @\d+\.\d+`).MatchString(dates[0][2]) {
		t.Fatalf("clock step script %q", dates[0][2])
	}
	// And the desktop's idle stamps, in the same call: the step is what
	// makes them wrong, and the idle check would otherwise stop the
	// viewer and the agents' browser within a minute of the wake
	// (DECISIONS I-822).
	// The stamps are touched by path and left dev's, not through
	// repose-desktop-stamp: that command is in the desktop units' own
	// runtimeInputs and on no PATH hostd's Exec gets, so calling it by
	// name did nothing at all (DECISIONS I-822, I-727).
	for _, want := range []string{"/run/repose/desktop", "last-client", "last-cdp", "chown dev:dev"} {
		if !strings.Contains(dates[0][2], want) {
			t.Fatalf("the wake did not re-stamp the desktop's idle files (%s missing): %q", want, dates[0][2])
		}
	}
	// The clock step's own status survives the stamps, because the
	// RUNBOOK sends an operator to the line it logs.
	if !strings.Contains(dates[0][2], "rc=$?") || !strings.Contains(dates[0][2], "exit $rc") {
		t.Fatalf("the clock step's exit status is not kept: %q", dates[0][2])
	}
}

func chSock(h *harness, id string) string {
	return ch.APISocket(filepath.Join(h.cfg.GuestsDir, id))
}

// scrape is the host's own /metrics, read as a visitor's Prometheus
// would: the only way to assert what a histogram actually observed.
type scrape string

func (s scrape) has(want string) bool { return strings.Contains(string(s), want) }

func (h *harness) scrape(t *testing.T) scrape {
	t.Helper()
	rec := httptest.NewRecorder()
	h.metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return scrape(rec.Body.String())
}

// Manager.Close holds the manager's own lock while it waits for each
// monitor's goroutine to end, and that goroutine reads the resting mark
// on every dial retry (checkLost). The mark must therefore not sit behind
// the manager's lock: it did once, and a hostd shutdown with a resting
// guest deadlocked, taking the whole test binary with it.
func TestRestingMarkIsNotBehindTheManagerLock(t *testing.T) {
	h := newHarness(t, nil)
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.m.restingMark(gid1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("restingMark waits for the manager lock, so Manager.Close deadlocks against the monitor that reads it")
	}
}
