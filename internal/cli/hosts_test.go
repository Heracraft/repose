package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `repose host add|ls|rm` (DECISIONS I-1370): add prints the install
// step alone on stdout and where it goes on stderr; ls is the one table
// a Mac mini owner reads; rm asks once and refuses while machines are
// still there.
func TestHostAddListRm(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut

	// Nothing of the user's own yet.
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "You have no hardware of your own.\n" {
		t.Fatalf("empty listing: %q", got)
	}

	// With an install step recorded, stdout is that line and nothing
	// else, so it pipes to the machine being added.
	fake.SetHostInstallCommand("curl -fsSL https://repose.run/mac | sh -s -- %s")
	out.buf.Reset()
	if err := HostAddCmd(ctx, e, "mac-mini"); err != nil {
		t.Fatal(err)
	}
	step := strings.TrimRight(out.buf.String(), "\n")
	if strings.Contains(step, "\n") || !strings.HasPrefix(step, "curl -fsSL https://repose.run/mac") || !strings.Contains(step, "rjt_") {
		t.Fatalf("stdout is the install step alone: %q", out.buf.String())
	}
	if got := errOut.buf.String(); !strings.HasPrefix(got, "mac-mini: run this on the machine you are adding. It works once, before ") {
		t.Fatalf("stderr: %q", got)
	}

	// The name is checked before any call, and a name the account has is
	// the api's conflict in the CLI's words.
	if err := HostAddCmd(ctx, e, "Mac Mini"); err == nil || !strings.Contains(err.Error(), "NAME must match") {
		t.Fatalf("bad name: %v", err)
	}
	fake.SetHostLimit(2)
	if err := HostAddCmd(ctx, e, "mac-mini"); err == nil || !strings.Contains(err.Error(), "already have a host named mac-mini") {
		t.Fatalf("taken name: %v", err)
	}
	// The plan's limit is the api's number, not the CLI's.
	fake.SetHostLimit(1)
	if err := HostAddCmd(ctx, e, "spare"); err == nil || !strings.Contains(err.Error(), "Your plan takes one host.") {
		t.Fatalf("limit: %v", err)
	}
	fake.SetHostLimit(-1)
	if err := HostAddCmd(ctx, e, "spare"); err == nil || !strings.Contains(err.Error(), "needs the Own hardware plan") {
		t.Fatalf("no plan: %v", err)
	}
	fake.SetHostLimit(2)

	// An api with no install step recorded hands over the token instead,
	// with the page that says what to do with it (19f records the line).
	fake.SetHostInstallCommand("")
	out.buf.Reset()
	errOut.buf.Reset()
	if err := HostAddCmd(ctx, e, "spare"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimRight(out.buf.String(), "\n"); !strings.HasPrefix(got, "rjt_") || strings.Contains(got, "\n") {
		t.Fatalf("stdout is the token alone: %q", out.buf.String())
	}
	if got := errOut.buf.String(); !strings.HasPrefix(got, "spare: this is its join token.") || !strings.Contains(got, "/docs/own-hardware") {
		t.Fatalf("no-install stderr: %q", got)
	}

	// The table: a host that has joined, and one that has not.
	out.buf.Reset()
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	got := out.buf.String()
	if !strings.HasPrefix(got, "HOST") || !strings.Contains(got, "MACHINES  RUNNING  LAST SEEN") {
		t.Fatalf("header:\n%s", got)
	}
	// Neither has registered, so both are waiting with no memory and no
	// heartbeat to report.
	for _, want := range []string{"mac-mini  waiting", "spare     waiting"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q not in:\n%s", want, got)
		}
	}
	if strings.Count(got, "-  ") < 2 {
		t.Fatalf("memory and last seen are unknown before it joins:\n%s", got)
	}

	e.Quiet = true
	out.buf.Reset()
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "mac-mini\nspare\n" {
		t.Fatalf("-q: %q", got)
	}
	e.Quiet = false

	// rm: a name the account does not have, the question, and the no.
	if err := HostRmCmd(ctx, e, "other", false, nil); err == nil || !strings.Contains(err.Error(), "You have no host named other") {
		t.Fatalf("unknown host: %v", err)
	}
	var asked string
	no := func(prompt string) (bool, error) { asked = prompt; return false, nil }
	err := HostRmCmd(ctx, e, "spare", false, no)
	if err == nil || !strings.Contains(err.Error(), "Nothing removed.") {
		t.Fatalf("a no removes nothing: %v", err)
	}
	// The question says the one thing the user cannot discover later.
	if !strings.Contains(asked, "Remove spare?") || !strings.Contains(asked, "stay on its disk, and repose has no copy") {
		t.Fatalf("prompt: %q", asked)
	}
	yes := func(string) (bool, error) { return true, nil }
	if err := HostRmCmd(ctx, e, "spare", false, yes); err != nil {
		t.Fatal(err)
	}
	// Removing it again is not an error: the api answers 204 either way.
	if err := HostRmCmd(ctx, e, "mac-mini", true, nil); err != nil {
		t.Fatal(err)
	}
	out.buf.Reset()
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "You have no hardware of your own.\n" {
		t.Fatalf("after rm: %q", got)
	}
}

// A host still holding machines is not retired: the api refuses and the
// CLI names them, because a machine moves host only by restore.
func TestHostRmRefusedWhileMachinesAreOnIt(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	e.Out, e.ErrOut = &discardWriter{}, &discardWriter{}

	id := fake.AddOwnedHost("mac-mini")
	fake.SetProjectHost(p.ID, id)
	err := HostRmCmd(ctx, e, "mac-mini", true, nil)
	if err == nil || !strings.Contains(err.Error(), "mac-mini still has todo-app on it") {
		t.Fatalf("refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "repose rm") {
		t.Fatalf("the way out is not in the refusal: %v", err)
	}
}

// A host of the user's own that has stopped answering is named, with how
// long it has been quiet, in `repose host ls`, `repose ls` and
// `repose status`. A host of repose's is not theirs to fix and is not
// named.
func TestOfflineOwnHostIsNamedEverywhereItShows(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out := &discardWriter{}
	e.Out, e.ErrOut = out, &discardWriter{}

	id := fake.AddOwnedHost("mac-mini")
	fake.SetProjectHost(p.ID, id)
	fake.SetOwnedHostOffline(id, 14*time.Minute)
	fake.SetHostUnreachable(p.ID, true)

	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "mac-mini  offline") || !strings.Contains(got, "14m ago") {
		t.Fatalf("host ls:\n%s", got)
	}

	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: mac-mini has not answered for 14m.\n") {
		t.Fatalf("repose ls:\n%s", got)
	}

	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "mac-mini has not answered for 14m") {
		t.Fatalf("repose status:\n%s", got)
	}

	// On a host of repose's there is nothing of the user's to name, and
	// the line says whose it is instead.
	fake.SetProjectHost(p.ID, "host-01")
	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: its server is not answering.\n") {
		t.Fatalf("hosted:\n%s", got)
	}
}

// The listing of a project on a reachable host costs no /hosts read:
// the common `repose ls` keeps its one round trip.
func TestLsReadsHostsOnlyWhenOneIsQuiet(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, p := newRoundtripEnv(t, fake)
	ctx := context.Background()
	e.Out, e.ErrOut = &discardWriter{}, &discardWriter{}

	before := fake.Calls("GET /v1/hosts")
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if n := fake.Calls("GET /v1/hosts") - before; n != 0 {
		t.Fatalf("a quiet-free listing read /hosts %d times", n)
	}
	fake.SetHostUnreachable(p.ID, true)
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if n := fake.Calls("GET /v1/hosts") - before; n != 1 {
		t.Fatalf("an unreachable host read /hosts %d times, want 1", n)
	}
}

// An API token with `read` may list the hardware; minting a join token
// or retiring a host hands out, or takes away, a whole machine, so both
// take a login (I-1370).
func TestHostsUnderAnAPIToken(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	e.Out, e.ErrOut = &discardWriter{}, &discardWriter{}
	fake.AddOwnedHost("mac-mini")

	tok, err := e.Client.CreateToken(ctx, CreateTokenRequest{Name: "ci", Scopes: []string{"read", "projects"}})
	if err != nil {
		t.Fatal(err)
	}
	te := newLifecycleEnv(t, fake)
	te.Client = newClient(fake.URL()+"/v1", apiTokenSource(tok.Token))
	out := &discardWriter{}
	te.Out, te.ErrOut = out, &discardWriter{}

	if err := HostLsCmd(ctx, te); err != nil {
		t.Fatalf("host ls under a token: %v", err)
	}
	if got := out.buf.String(); !strings.Contains(got, "mac-mini") {
		t.Fatalf("host ls under a token:\n%s", got)
	}
	if err := HostAddCmd(ctx, te, "spare"); err == nil || !strings.Contains(err.Error(), "log in for that") {
		t.Fatalf("host add under a token: %v", err)
	}
	if err := HostRmCmd(ctx, te, "mac-mini", true, nil); err == nil || !strings.Contains(err.Error(), "log in for that") {
		t.Fatalf("host rm under a token: %v", err)
	}
}

// The states a user reads, against the api's own words.
func TestHostStateWords(t *testing.T) {
	for api, want := range map[string]string{
		"registering": "waiting",
		"ready":       "online",
		"unreachable": "offline",
		"draining":    "removing",
		"retired":     "removed",
		"lost":        "removed",
	} {
		if got := hostStateWord(api); got != want {
			t.Errorf("%s: %s, want %s", api, got, want)
		}
	}
}
