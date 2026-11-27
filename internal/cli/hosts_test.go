package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// On a Linux x86 machine `repose host add` prints the x86 installer,
// whatever line the api recorded for a Mac, and the argument the api
// minted, hub and all, goes into a 0600 file the line names: no
// terminal, log or agent's transcript holds the key (DECISIONS I-1498,
// ENG-42, I-1616).
func TestHostAddOnLinuxX86(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut
	onLinuxX86 = func() bool { return true }
	t.Cleanup(func() { onLinuxX86 = func() bool { return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" } })

	for i, line := range []string{"repose host add --join %s", ""} {
		// With no line recorded the token is still made into the argument.
		fake.SetHostInstall(line)
		fake.SetHostLimit(2)
		name := []string{"box", "box2"}[i]
		out.buf.Reset()
		errOut.buf.Reset()
		if err := HostAddCmd(context.Background(), e, name); err != nil {
			t.Fatal(err)
		}
		dir, _ := configDir()
		path := filepath.Join(dir, "join-"+name)
		if got := out.buf.String(); got != hostInstallX86FromFile(path)+"\n" {
			t.Fatalf("stdout is the x86 install step with the file alone: %q", got)
		}
		const want = "curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file '"
		if !strings.HasPrefix(out.buf.String(), want) {
			t.Fatalf("the installer line: %q", out.buf.String())
		}
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("join file: %v %v", fi, err)
		}
		b, _ := os.ReadFile(path)
		arg := strings.TrimSpace(string(b))
		if bundle, err := decodeJoinBundle(arg); err != nil || bundle.Token == "" {
			t.Fatalf("the file holds no join bundle: %v", err)
		}
		if strings.Contains(out.buf.String()+errOut.buf.String(), arg[:40]) {
			t.Fatalf("the argument was printed: %q %q", out.buf.String(), errOut.buf.String())
		}
		if got := errOut.buf.String(); !strings.HasPrefix(got, name+": run this on the machine you are adding. It reads "+path+", which works once, before ") {
			t.Fatalf("stderr: %q", got)
		}
	}
}

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
	// The api's line, as anywhere but a Linux x86 machine prints it;
	// TestHostAddOnLinuxX86 has the installer's.
	onLinuxX86 = func() bool { return false }
	t.Cleanup(func() { onLinuxX86 = func() bool { return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" } })

	// Nothing of the user's own yet.
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "You have no hardware of your own.\n" {
		t.Fatalf("empty listing: %q", got)
	}

	// With an install step recorded, stdout is that line and nothing
	// else, so it pipes to the machine being added.
	fake.SetHostInstall("repose host add --join %s")
	out.buf.Reset()
	if err := HostAddCmd(ctx, e, "mac-mini"); err != nil {
		t.Fatal(err)
	}
	step := strings.TrimRight(out.buf.String(), "\n")
	if strings.Contains(step, "\n") || !strings.HasPrefix(step, "repose host add --join ") || len(step) < 40 {
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
	// The plan's limit is the api's number, not the CLI's. No plan sells
	// a limit since I-1594, but an api from before it still answers one.
	fake.SetHostLimit(1)
	if err := HostAddCmd(ctx, e, "spare"); err == nil || !strings.Contains(err.Error(), "Your plan takes one host.") {
		t.Fatalf("limit: %v", err)
	}
	// A plan that sells no host at all is 0; a figure below 0 puts the
	// fake's default plan back, which sells any number (I-1390's knob).
	fake.SetHostLimit(0)
	if err := HostAddCmd(ctx, e, "spare"); err == nil || !strings.Contains(err.Error(), "needs the Own hardware plan") {
		t.Fatalf("no plan: %v", err)
	}
	fake.SetHostLimit(2)

	// An api with no install step recorded hands over the token instead,
	// with the page that says what to do with it (19f records the line).
	fake.SetHostInstall("")
	out.buf.Reset()
	errOut.buf.Reset()
	if err := HostAddCmd(ctx, e, "spare"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimRight(out.buf.String(), "\n"); got == "" || strings.ContainsAny(got, " \n") {
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
	// A host that keeps no snapshot costs nothing more to remove; the
	// count when it keeps some is TestOwnHostSnapshotsGoWithTheHost's.
	if asked != "Remove spare? [y/N] " {
		t.Fatalf("prompt: %q", asked)
	}
	yes := func(string) (bool, error) { return true, nil }
	if err := HostRmCmd(ctx, e, "spare", false, yes); err != nil {
		t.Fatal(err)
	}
	// Removing it again is not an error: the api answers 204 either way,
	// and the retired row the api keeps is not listed, so the second
	// `host rm` cannot find it by name.
	if err := HostRmCmd(ctx, e, "spare", true, nil); err == nil || !strings.Contains(err.Error(), "You have no host named spare") {
		t.Fatalf("rm twice: %v", err)
	}
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
	// ENG-119, DECISIONS I-1418: it named `repose snapshots restore` as
	// the other way out, and that is not one. On the owner's account the
	// refusal was shown for rig1, which had no snapshot row at all
	// (`count(*) from snapshots` was 0 and never anything else), so the
	// command it sent them to had no id to take. It is not needed either:
	// `repose rm` keeps the final snapshot for 30 days, so removing a
	// machine does not lose the work a restore would have saved.
	if strings.Contains(err.Error(), "snapshots restore") {
		t.Errorf("the refusal names a restore, which has nothing to restore for a machine with no snapshot: %v", err)
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
	if got := out.buf.String(); !strings.Contains(got, "mac-mini  offline") || !strings.Contains(got, "14m ago") || !strings.Contains(got, "24 GB") {
		t.Fatalf("host ls:\n%s", got)
	}

	out.buf.Reset()
	if err := ProjectsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "todo-app: your host mac-mini has not answered for 14m.\n") {
		t.Fatalf("repose ls:\n%s", got)
	}

	out.buf.Reset()
	if err := StatusCmd(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); !strings.Contains(got, "your host mac-mini has not answered for 14m") {
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

// TestHostLsAgreesWithItsOwnJSON is ENG-37, from the customer review: the
// table printed "You have no hardware of your own." while `--json` on the
// same account returned four hosts, all removed, and a script reading the
// JSON to decide whether to add one concluded it already had four.
//
// The assertion is that the two outputs answer the same question, not
// that either prints a particular string: a test that pinned today's
// table and today's JSON separately is exactly what let them drift. Both
// are read back and compared — the names each lists, and the words each
// uses for a state.
func TestHostLsAgreesWithItsOwnJSON(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	out, errOut := &discardWriter{}, &discardWriter{}
	e.Out, e.ErrOut = out, errOut

	// One of each: a host that has joined, one that never did, and one
	// the customer removed. The removed one is the whole finding.
	live := fake.AddOwnedHost("mac-mini")
	fake.SetHostLimit(9)
	if err := HostAddCmd(ctx, e, "waiting-one"); err != nil {
		t.Fatal(err)
	}
	gone := fake.AddOwnedHost("old-mac")
	fake.SetOwnedHostState("old-mac", "retired")
	_ = live
	_ = gone

	out.buf.Reset()
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	table := out.buf.String()

	out.buf.Reset()
	e.JSON = true
	if err := HostLsCmd(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.JSON = false
	var fromJSON []Host
	if err := json.Unmarshal([]byte(out.buf.String()), &fromJSON); err != nil {
		t.Fatalf("--json: %v: %s", err, out.buf.String())
	}

	// Every host the JSON lists is in the table, and the other way round.
	for _, h := range fromJSON {
		if !strings.Contains(table, h.Name) {
			t.Errorf("--json lists %s (%s) and the table does not:\n%s", h.Name, h.State, table)
		}
	}
	for _, name := range []string{"mac-mini", "waiting-one"} {
		if !strings.Contains(table, name) {
			t.Errorf("the table does not list %s:\n%s", name, table)
		}
		if !slices.ContainsFunc(fromJSON, func(h Host) bool { return h.Name == name }) {
			t.Errorf("--json does not list %s: %s", name, out.buf.String())
		}
	}
	if slices.ContainsFunc(fromJSON, func(h Host) bool { return h.Name == "old-mac" }) {
		t.Errorf("--json lists a host the customer removed: %s", out.buf.String())
	}

	// And they say the same word for a state. The docs name five
	// (cli.md "repose host"); `registering` is not one of them, and a
	// script that switched on it would be switching on a word no
	// customer is ever shown.
	for _, h := range fromJSON {
		if got := hostStateWord(h.State); got != h.State {
			t.Errorf("--json says %q for %s where the table says %q", h.State, h.Name, got)
		}
		if !strings.Contains(table, h.State) {
			t.Errorf("--json says %q for %s and the table does not use that word:\n%s", h.State, h.Name, table)
		}
	}
}

// Removing a host that has stopped answering is now allowed even though
// machines are on it (I-1420), because refusing was a dead end. It is
// also the one path on which a user loses machines by removing a host,
// so the confirmation says so and names them. ENG-119: the account it
// was found on could neither remove the host nor destroy the machine,
// and the way out has to be a command whose question tells the truth.
func TestRemovingAHostThatIsGoneSaysWhatGoesWithIt(t *testing.T) {
	quiet := &Host{Name: "mac-rig2", State: "unreachable", Projects: 1}
	p := hostRemovePrompt(quiet)
	if !strings.Contains(p, "the machine on it cannot be recovered") {
		t.Errorf("the question does not say the machine goes with it: %q", p)
	}
	if !strings.Contains(p, "not answering") {
		t.Errorf("the question does not say why this one is allowed: %q", p)
	}
	if !strings.Contains(p, "mac-rig2") {
		t.Errorf("the question does not name the host: %q", p)
	}
	// A host that is answering holds no machines it can lose this way:
	// the api still refuses that, so the question must not threaten it.
	live := &Host{Name: "mac-mini", State: "ready", Projects: 0}
	if got := hostRemovePrompt(live); strings.Contains(got, "machine") {
		t.Errorf("a host with nothing on it threatens machines: %q", got)
	}
	// Two machines read as two.
	if got := hostRemovePrompt(&Host{Name: "mac-rig2", State: "lost", Projects: 2}); !strings.Contains(got, "the 2 machines on it cannot be recovered") || !strings.Contains(got, "their disks are") {
		t.Errorf("two machines: %q", got)
	}
}

// ENG-204, DECISIONS I-1590: an x86 box keeps hostd and its disk until
// its installer's --uninstall runs there, so removing the host says the
// one line to run; a Mac's does not, since the appliance is the CLI's.
func TestHostRmOfAnX86BoxSaysHowToUninstall(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e, _ := newRoundtripEnv(t, fake)
	ctx := context.Background()
	onAppleSilicon = func() bool { return false }
	t.Cleanup(func() { onAppleSilicon = func() bool { return false } }) // tests never act on the Mac they run on

	for _, c := range []struct{ arch, want string }{
		{"x86_64", "Repose still runs on box until this runs there as root:\n  " + hostUninstallX86 + "\n"},
		{"aarch64", ""},
	} {
		errOut := &discardWriter{}
		e.Out, e.ErrOut = &discardWriter{}, errOut
		id := fake.AddOwnedHost("box")
		fake.SetOwnedHostArch(id, c.arch)
		if err := HostRmCmd(ctx, e, "box", true, nil); err != nil {
			t.Fatalf("%s: %v", c.arch, err)
		}
		if got := errOut.buf.String(); got != c.want {
			t.Errorf("%s: stderr %q, want %q", c.arch, got, c.want)
		}
	}
}

// ENG-204: on Linux, `repose host rm --local` said "There is no appliance
// on this Mac." It now names the installer's uninstall on a box the
// installer made, and says there is no host anywhere else.
func TestHostRmLocalOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux answer")
	}
	onAppleSilicon = func() bool { return false }
	t.Cleanup(func() { onAppleSilicon = func() bool { return false } }) // tests never act on the Mac they run on
	marker := filepath.Join(t.TempDir(), "hostd.env")
	old := x86HostMarker
	x86HostMarker = marker
	t.Cleanup(func() { x86HostMarker = old })
	e := &Env{Out: &discardWriter{}, ErrOut: &discardWriter{}}

	err := HostRmLocalCmd(context.Background(), e, true, nil)
	if err == nil || err.Error() != "There is no repose host on this machine." {
		t.Fatalf("no install: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err = HostRmLocalCmd(context.Background(), e, true, nil)
	if err == nil || !strings.Contains(err.Error(), hostUninstallX86) || strings.Contains(err.Error(), "Mac") {
		t.Fatalf("an x86 install: %v", err)
	}
}
