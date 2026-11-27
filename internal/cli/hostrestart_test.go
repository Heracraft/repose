package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// fakeHostd answers hostd's control socket's park and unpark with fixed
// results, as hostd's JSON.
func fakeHostd(t *testing.T, park, unpark []map[string]any) (sock string, calls *[]string) {
	t.Helper()
	sock = filepath.Join(t.TempDir(), "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /park", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, "park "+r.URL.Query().Get("reason"))
		_ = json.NewEncoder(w).Encode(park)
	})
	mux.HandleFunc("POST /unpark", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, "unpark")
		_ = json.NewEncoder(w).Encode(unpark)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock, &got
}

func restartWith(t *testing.T, sock string) (restarted *bool) {
	t.Helper()
	oldSock, oldMac, oldRestart := hostdControlSock, onMac, macRestart
	t.Cleanup(func() { hostdControlSock, onMac, macRestart = oldSock, oldMac, oldRestart })
	r := false
	hostdControlSock = func() string { return sock }
	onMac = func() bool { return true }
	macRestart = func(context.Context) error { r = true; return nil }
	return &r
}

// Every machine parked (or parked already, or stopped): the Mac restarts.
func TestHostRestartParksThenRestarts(t *testing.T) {
	sock, calls := fakeHostd(t, []map[string]any{
		{"guest_id": "a", "ok": true, "outcome": "parked"},
		{"guest_id": "b", "outcome": "skipped", "reason": "guest already parked"},
		{"guest_id": "c", "outcome": "skipped", "reason": "guest is not running"},
	}, nil)
	restarted := restartWith(t, sock)
	var errOut bytes.Buffer
	if err := HostRestartCmd(context.Background(), &Env{ErrOut: &errOut}, false); err != nil {
		t.Fatal(err)
	}
	if !*restarted || strings.Join(*calls, ",") != "park operator" {
		t.Fatalf("restarted %v calls %v", *restarted, *calls)
	}
	if !strings.Contains(errOut.String(), "Parked 2 machines") {
		t.Fatalf("stderr %q", errOut.String())
	}
}

// A machine that cannot be parked stops the restart and the parked ones
// are unparked; when they could not be restored (a locked screen) the
// answer says when they run again. --force restarts.
func TestHostRestartStopsOnAMachineThatCannotPark(t *testing.T) {
	park := []map[string]any{
		{"guest_id": "a", "ok": true, "outcome": "parked"},
		{"guest_id": "b", "outcome": "failed", "reason": "pause failed"},
	}
	sock, calls := fakeHostd(t, park, nil)
	restarted := restartWith(t, sock)
	err := HostRestartCmd(context.Background(), &Env{ErrOut: &bytes.Buffer{}}, false)
	if err == nil || *restarted || strings.Join(*calls, ",") != "park operator,unpark" {
		t.Fatalf("err %v restarted %v calls %v", err, *restarted, *calls)
	}
	if !strings.Contains(err.Error(), "(b)") || !strings.Contains(err.Error(), "unlocked") {
		t.Fatalf("answer %q", err.Error())
	}
	sock2, _ := fakeHostd(t, park, nil)
	restarted = restartWith(t, sock2)
	if err := HostRestartCmd(context.Background(), &Env{ErrOut: &bytes.Buffer{}}, true); err != nil || !*restarted {
		t.Fatalf("--force: %v restarted %v", err, *restarted)
	}
}

func TestHostRestartOffAMac(t *testing.T) {
	old := onMac
	t.Cleanup(func() { onMac = old })
	onMac = func() bool { return false }
	err := HostRestartCmd(context.Background(), &Env{}, false)
	var ee *exitError
	if err == nil || !errors.As(err, &ee) || ee.code != ExitUsage {
		t.Fatalf("%v", err)
	}
}

// On a Mac whose screen is locked hostd saves nothing (a save there fails
// and can end the VM, I-1682): every machine comes back skipped as locked,
// and the answer says to unlock and run it again, naming no guest id and
// asking for no unpark.
func TestHostRestartOnALockedMac(t *testing.T) {
	sock, calls := fakeHostd(t, []map[string]any{
		{"guest_id": "g-a", "project": "e2e-a", "outcome": "skipped", "reason": "this Mac's screen is locked"},
		{"guest_id": "g-b", "project": "e2e-b", "outcome": "skipped", "reason": "this Mac's screen is locked"},
	}, nil)
	restarted := restartWith(t, sock)
	err := HostRestartCmd(context.Background(), &Env{ErrOut: &bytes.Buffer{}}, false)
	if err == nil || *restarted || strings.Join(*calls, ",") != "park operator" {
		t.Fatalf("err %v restarted %v calls %v", err, *restarted, *calls)
	}
	if want := "This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and run `repose host restart` again."; err.Error() != want {
		t.Fatalf("answer %q", err.Error())
	}
}

// A machine that could not be parked is named by its project, not its
// guest id, which its user has never seen.
func TestHostRestartNamesTheMachine(t *testing.T) {
	sock, _ := fakeHostd(t, []map[string]any{
		{"guest_id": "g-b", "project": "e2e-b", "outcome": "failed", "reason": "pause failed"},
	}, nil)
	restartWith(t, sock)
	err := HostRestartCmd(context.Background(), &Env{ErrOut: &bytes.Buffer{}}, false)
	if err == nil || !strings.Contains(err.Error(), "(e2e-b)") || strings.Contains(err.Error(), "g-b") {
		t.Fatalf("answer %v", err)
	}
}

// review-cli 2026-11-27: `host restart` had no --json, and off a Mac it
// asked for a confirmation before it said it runs only on a Mac.
func TestHostRestartJSONAndCheckOrder(t *testing.T) {
	sock, _ := fakeHostd(t, []map[string]any{{"guest_id": "a", "project": "api", "ok": true, "outcome": "parked"}}, nil)
	restartWith(t, sock)
	var out, errOut bytes.Buffer
	if err := HostRestartCmd(context.Background(), &Env{Out: &out, ErrOut: &errOut, JSON: true}, false); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res) != 1 || res[0]["project"] != "api" || res[0]["outcome"] != "parked" {
		t.Fatalf("--json %q: %v", out.String(), err)
	}
	if !strings.Contains(errOut.String(), "Parked 1 machines") {
		t.Errorf("stderr %q", errOut.String())
	}

	onMac = func() bool { return false }
	cmd := newHostCmd(func() (*Env, error) { return &Env{Out: &out, ErrOut: &errOut}, nil }, &globalFlags{})
	cmd.SetArgs([]string{"restart"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "runs on the Mac it restarts") {
		t.Errorf("host restart off a Mac, no -y: %v", err)
	}
}

func TestHostRmRefusesABadNameBeforeAsking(t *testing.T) {
	var out bytes.Buffer
	cmd := newHostCmd(func() (*Env, error) { return &Env{Out: &out, ErrOut: &out}, nil }, &globalFlags{})
	cmd.SetArgs([]string{"rm", "Bad Name"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if err == nil || strings.Contains(err.Error(), "confirmation") || !strings.Contains(err.Error(), "NAME must match") {
		t.Errorf("host rm of a bad name: %v", err)
	}
}

// Review pass 2, P2-1: `repose host restart` parked through the Mac's own
// socket, so the api never knew the Mac was restarting and `repose
// status` called the machines broken and offered `repose start`. Logged
// in, it restarts through the api, which then reads them parked.
func TestHostRestartGoesThroughTheAPI(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e := newLifecycleEnv(t, fake)
	var errOut bytes.Buffer
	e.ErrOut = &errOut
	fake.AddOwnedHost("mac-mini")
	fake.SetOwnedHostRestart("mac-mini", true, nil, false)
	ctx := context.Background()
	p, err := e.Client.CreateProject(ctx, CreateProjectRequest{Name: "api", Class: "small"})
	if err != nil {
		t.Fatal(err)
	}
	sock, calls := fakeHostd(t, nil, nil)
	restarted := restartWith(t, sock)
	root := t.TempDir()
	macFS = func(path string) string { return filepath.Join(root, path) }
	t.Cleanup(func() { macFS = func(p string) string { return p } })
	_ = os.MkdirAll(filepath.Join(root, macHostPaths.Root), 0o755)
	_ = os.WriteFile(filepath.Join(root, macHostPaths.Record()), []byte(`{"host":"mac-mini"}`), 0o644)

	if err := HostRestartCmd(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || *restarted {
		t.Errorf("the restart went through the Mac's socket too: calls %v, restarted %v", *calls, *restarted)
	}
	if !strings.Contains(errOut.String(), "Parked 1 machines") {
		t.Errorf("stderr %q", errOut.String())
	}
	q, err := e.Client.GetProject(ctx, p.ID)
	if err != nil || q.Parked == nil || q.Parked.WaitsFor != "restart" {
		t.Errorf("after the restart the api reads %+v, want parked waits_for restart", q.Parked)
	}
	out := parkedLine(q)
	if out != "parked; mac-mini is restarting, and its memory is kept." {
		t.Errorf("status line %q", out)
	}
}
