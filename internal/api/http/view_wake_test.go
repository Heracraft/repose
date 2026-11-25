package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/hostmgr"
	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/resting"
)

// newHTTPProcessEnv is an env whose server is the http process production
// runs (API_MODE=http, internal/api/app/app.go): its own hostmgr, which
// holds no host's stream, its own resting service and its own ops engine,
// which never drives. The harness under it is the grpc process: the host
// is connected there and its engine drives every op. Every other http
// test runs both halves in one process, which is how a wake sent from an
// http route shipped working in tests and failing in production
// (2026-11-25, ENG-242).
func newHTTPProcessEnv(t *testing.T, relay *fakeRelay) *env {
	t.Helper()
	return newEnvWith(t, &httpapi.RateLimits{General: 10000, Certs: 10000, Config: 10000}, func(d *httpapi.Deps) {
		hm := hostmgr.New(d.Pool, d.CA.X509(), "http-1", d.Metrics, d.Log)
		d.Resting = resting.New(d.Pool, hm, d.Log)
		d.Engine = ops.New(d.Pool, hm, d.CA, d.Secrets, d.Logs, d.Events, d.Metrics, d.Log, ops.Config{BaseRef: "deadbeef"})
		d.ViewRelay = relay
		d.ViewOrigins = []string{"https://repose.run"}
	})
}

// restOnFake makes the project's machine rest, on the fake host and in
// its row, as hostd's pause does.
func restOnFake(t *testing.T, e *env, pid string) string {
	t.Helper()
	p := e.h.Project(uuid.MustParse(pid))
	if p.GuestID == nil {
		t.Fatal("the project has no guest")
	}
	gid := p.GuestID.String()
	e.h.Fake.SetResting(gid, true)
	e.h.WaitFor("resting marker", func() bool { return e.h.Project(p.ID).RestingSince != nil })
	return gid
}

func fakeGuestRests(e *env, gid string) bool {
	for _, g := range e.h.Fake.Guests() {
		if g.GuestID == gid {
			return g.Resting
		}
	}
	return false
}

// The browser view of a frozen machine, opened through the http process,
// wakes the machine and opens (ENG-242): the wake reaches the host the way
// `repose start` does, through an op the grpc process drives.
func TestViewWakesAFrozenMachineFromTheHTTPProcess(t *testing.T) {
	e := newHTTPProcessEnv(t, &fakeRelay{})
	tok, pid := newViewProject(t, e, "sub-view-http", "vhttp")
	gid := restOnFake(t, e, pid)

	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 200 {
		t.Fatalf("mint on a frozen machine: %d %s\n%s", r.status, r.raw, wakeLines(e))
	}
	if r.body["resting"] != false {
		t.Fatalf("resting %v after the wake", r.body["resting"])
	}
	if fakeGuestRests(e, gid) {
		t.Fatal("the guest still rests on its host")
	}
	if p := e.h.Project(uuid.MustParse(pid)); p.RestingSince != nil {
		t.Fatal("the row still says the machine rests")
	}
}

// wakeLines is the log's guest_wake_fail lines, which name the cause.
func wakeLines(e *env) string {
	var out []string
	for _, l := range strings.Split(e.logText(), "\n") {
		if strings.Contains(l, "guest_wake_fail") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// A frozen machine whose host is away cannot wake: the mint says so in
// words, as 503 unavailable with detail.reason wake_failed, not as a 500
// the dashboard can only call an api error (ENG-242).
func TestViewOnAFrozenMachineWhoseHostIsAwaySaysSo(t *testing.T) {
	e := newHTTPProcessEnv(t, &fakeRelay{})
	tok, pid := newViewProject(t, e, "sub-view-away", "vaway")
	restOnFake(t, e, pid)
	e.h.DisconnectHost()

	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 503 {
		t.Fatalf("mint with the host away: %d %s", r.status, r.raw)
	}
	er := r.body["error"].(map[string]any)
	if er["code"] != "unavailable" || er["detail"].(map[string]any)["reason"] != "wake_failed" {
		t.Fatalf("refusal %s", r.raw)
	}
	if msg := er["message"].(string); !strings.Contains(msg, "frozen") || !strings.Contains(msg, "host is not connected") {
		t.Fatalf("message %q does not say what happened", msg)
	}
}

// A wake the host refuses names the host's reason.
func TestViewWakeTheHostRefusesSaysWhy(t *testing.T) {
	e := newHTTPProcessEnv(t, &fakeRelay{})
	tok, pid := newViewProject(t, e, "sub-view-refused", "vrefused")
	gid := restOnFake(t, e, pid)
	e.h.Fake.SetFail("WakeGuest", "guest_unresponsive")

	r := e.do(t, tok, "POST", "/projects/"+pid+"/browser/view", nil)
	if r.status != 503 {
		t.Fatalf("mint with the wake refused: %d %s", r.status, r.raw)
	}
	if testing.Verbose() {
		t.Logf("refusal: %s", r.raw)
	}
	er := r.body["error"].(map[string]any)
	if er["detail"].(map[string]any)["reason"] != "wake_failed" || !strings.Contains(er["message"].(string), "did not wake") {
		t.Fatalf("refusal %s", r.raw)
	}
	if !fakeGuestRests(e, gid) {
		t.Fatal("the guest woke although the host refused")
	}
	// The failed wake leaves nothing on the project: no error state, no
	// last_error, and a start is not refused as busy.
	p := e.h.Project(uuid.MustParse(pid))
	if p.State != "running" || p.LastError != nil {
		t.Fatalf("project after a refused wake: state %s last_error %v", p.State, p.LastError)
	}
}

// Turning keep_awake on for a frozen machine, through the http process,
// wakes it (I-820, I-1642): the same route fault as the view's.
func TestKeepAwakeWakesFromTheHTTPProcess(t *testing.T) {
	e := newHTTPProcessEnv(t, &fakeRelay{})
	tok, pid := newViewProject(t, e, "sub-keep-http", "kahttp")
	gid := restOnFake(t, e, pid)

	r := e.do(t, tok, "PATCH", "/projects/"+pid, map[string]any{"keep_awake": true})
	if r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.raw)
	}
	if fakeGuestRests(e, gid) {
		t.Fatalf("the guest still rests on its host\n%s", wakeLines(e))
	}
}
