package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestCreateThatFailedAtOnceReportsItsOwnError: a create the api fails at
// placement (no host with capacity) has left the project in error with no
// op by the time run reads it back. run used to start it, and printed the
// start's "project has no guest; create it first" instead (dogfood
// 2026-09-29, I-356).
func TestCreateThatFailedAtOnceReportsItsOwnError(t *testing.T) {
	var starts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1":
			_, _ = w.Write([]byte(`{"id":"p1","slug":"e2e-full","state":"error","class":"large"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1/ops/op1":
			_, _ = w.Write([]byte(`{"state":"error","error":{"code":"capacity","message":"no host with capacity"}}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			starts.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"project has no guest; create it first"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	errOut := &bytes.Buffer{}
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok")), Out: &bytes.Buffer{}, ErrOut: errOut}
	// What createProjectForRun hands on: the POST's answer.
	p := &Project{ID: "p1", Slug: "e2e-full", State: "creating", OpID: "op1"}
	err := ensureRunningFrom(context.Background(), e, p, e.newProgress(), false)
	if err == nil {
		t.Fatal("no error for a failed create")
	}
	if n := starts.Load(); n != 0 {
		t.Fatalf("sent %d start(s) to a project whose create failed", n)
	}
	if msg := err.Error(); !strings.Contains(msg, "no host with capacity") || strings.Contains(msg, "no guest") {
		t.Fatalf("error = %q, want the create's capacity error", msg)
	}
	t.Logf("run says: %s", err)
}

// TestStartOfAProjectWithNoGuestShowsTheCreate is I-406 from the CLI's
// side: start on a project whose create failed answers {create: true}
// and the progress line says Creating, not Starting or Restarting.
func TestStartOfAProjectWithNoGuestShowsTheCreate(t *testing.T) {
	var running atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1":
			if running.Load() {
				_, _ = w.Write([]byte(`{"id":"p1","slug":"recruiting-2","state":"running","class":"large"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"p1","slug":"recruiting-2","state":"creating","class":"large","op_id":"op2"}`))
			}
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1/ops/op2":
			running.Store(true)
			_, _ = w.Write([]byte(`{"op_id":"op2","kind":"create","state":"done"}`))
		case r.Method == "POST" && r.URL.Path == "/v1/projects/p1/start":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"op_id":"op2","restart":false,"create":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	errOut := &bytes.Buffer{}
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok")), Out: &bytes.Buffer{}, ErrOut: errOut}
	p := &Project{ID: "p1", Slug: "recruiting-2", State: "error"}
	pr := newProgress(errOut, false)
	if err := ensureRunningFrom(context.Background(), e, p, pr, true); err != nil {
		t.Fatalf("start: %v", err)
	}
	if out := errOut.String(); !strings.Contains(out, "Creating recruiting-2") || strings.Contains(out, "Restarting") {
		t.Fatalf("printed %q", out)
	}
	if p.State != "running" {
		t.Fatalf("state %s", p.State)
	}
}

// TestRunDoesNotStartACreateThatFailedOnAProjectItFound is the `fresh`
// half of the same bug (DECISIONS I-722). The incident's own path —
// `repose run` creating the project, then re-reading it — was closed by
// I-708's carry of the create's op id. This one was not: when
// `resolveProject` already found the project, `fresh` is true, the whole
// `!fresh` block is skipped (the carry and I-356's guard with it), and
// with no op id the wait fell back to `waitState`, which returns the
// moment the state goes to `error`; the next statement started the
// machine. One user action, two ops, and the create's error never read.
//
// The api now names the op in flight on the single-project read, so the
// project `run` holds carries it and the wait follows the op.
func TestRunDoesNotStartACreateThatFailedOnAProjectItFound(t *testing.T) {
	var starts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1":
			_, _ = w.Write([]byte(`{"id":"p1","slug":"teksafari-org","state":"error","class":"large"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1/ops/op1":
			_, _ = w.Write([]byte(`{"op_id":"op1","kind":"create","state":"error","project_state":"error",` +
				`"error":{"code":"guest_unresponsive","message":"the machine did not answer within a minute of starting"}}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			starts.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"op_id":"op9","restart":true,"create":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	errOut := &bytes.Buffer{}
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok")), Out: &bytes.Buffer{}, ErrOut: errOut}
	// What resolveProject hands on: GET /v1/projects/:id, which since
	// I-722 names the op in flight.
	p := &Project{ID: "p1", Slug: "teksafari-org", State: "creating", Class: "large", OpID: "op1"}
	err := ensureRunningFrom(context.Background(), e, p, e.newProgress(), true)
	if err == nil {
		t.Fatal("no error for a create that failed")
	}
	if n := starts.Load(); n != 0 {
		t.Fatalf("sent %d start(s) after a failed create, the 0.69 s restart of 2026-10-13", n)
	}
	if msg := err.Error(); !strings.Contains(msg, "did not answer within a minute") {
		t.Fatalf("error = %q, want the create's own words", msg)
	}
	t.Logf("run says: %s", err)
}

// TestRunReportsAFailedCreateEvenWithAnotherOpOpen is the hole I-722 left
// (DECISIONS I-728). I-356's guard only fired when the project read back
// with no op id at all, and since I-722 a read names whatever op is in
// flight — a personal build queued behind the create is enough (I-490).
// With the state `error` and that unrelated op named, the guard was
// skipped, `error` matches no case of the switch below it, and the fall
// through started the machine: the 0.69 s restart of 2026-10-13 again,
// one op later. The guard now asks only whether a create of ours ended in
// error.
func TestRunReportsAFailedCreateEvenWithAnotherOpOpen(t *testing.T) {
	var starts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1":
			// The create has settled in error; a personal build queued
			// behind it is the op in flight the read names (I-722).
			_, _ = w.Write([]byte(`{"id":"p1","slug":"teksafari-org","state":"error","class":"large","op_id":"op-build"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/projects/p1/ops/op1":
			_, _ = w.Write([]byte(`{"op_id":"op1","kind":"create","state":"error","project_state":"error",` +
				`"error":{"code":"guest_unresponsive","message":"the machine did not answer within a minute of starting"}}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			starts.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"op_id":"op9","restart":true,"create":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	e := &Env{Client: newClient(srv.URL+"/v1", staticToken("tok")), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	// What a create in this run hands on: the project and its op id, which
	// ensureRunningFrom keeps across the re-read (fresh = false).
	p := &Project{ID: "p1", Slug: "teksafari-org", State: "creating", Class: "large", OpID: "op1"}
	err := ensureRunningFrom(context.Background(), e, p, e.newProgress(), false)
	if err == nil {
		t.Fatal("no error for a create that failed")
	}
	if n := starts.Load(); n != 0 {
		t.Fatalf("sent %d start(s) after a failed create", n)
	}
	if msg := err.Error(); !strings.Contains(msg, "did not answer within a minute") {
		t.Fatalf("error = %q, want the create's own words", msg)
	}
	t.Logf("run says: %s", err)
}
