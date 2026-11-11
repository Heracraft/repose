package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// DECISIONS I-1100: on a terminal the overlap refusal (I-573) offers
// three one-key answers. These run against the real git checkouts on
// both sides of newSyncFixture, over the in-process sshd.

// chooser is SyncOptions.Choose driven by a list of keys, one per
// refusal, and records the refusals it was shown.
type chooser struct {
	keys  []overlapChoice
	shown []*dirtyTreeError
	err   error
}

func (c *chooser) choose(d *dirtyTreeError) (overlapChoice, error) {
	c.shown = append(c.shown, d)
	if c.err != nil {
		return overlapStop, c.err
	}
	if len(c.keys) == 0 {
		return overlapStop, nil
	}
	k := c.keys[0]
	c.keys = c.keys[1:]
	return k, nil
}

// overlapFixture: the machine changed README.md and agent.txt, the
// laptop changed README.md and notes.md. README.md is the one overlap.
func overlapFixture(t *testing.T) *syncFixture {
	t.Helper()
	f := newSyncFixture(t)
	if _, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	writeAt(t, f.guestRepo(), "README.md", "the agent's README\n")
	writeAt(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	writeAt(t, f.local, "README.md", "laptop README\n")
	writeAt(t, f.local, "notes.md", "laptop notes\n")
	return f
}

func stashList(t *testing.T, dir string) string {
	t.Helper()
	return mustRun(t, dir, "git", "stash", "list")
}

// `l`: the laptop's copy lands, the machine's copy of that file alone is
// in a stash named "repose sync: the machine's copy" whose commit the
// summary names, and the machine's change to a file the laptop did not
// touch is left where it is.
func TestOverlapKeepLaptopStashesTheMachinesCopy(t *testing.T) {
	f := overlapFixture(t)
	c := &chooser{keys: []overlapChoice{overlapLaptop}}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with l: %v", err)
	}
	if len(c.shown) != 1 || len(c.shown[0].files) != 1 || c.shown[0].files[0] != "README.md" {
		t.Fatalf("the menu was shown %d times, for %v", len(c.shown), c.shown)
	}
	if s.StashedCopy != 1 || s.CopyStashRef == "" {
		t.Fatalf("summary %+v", s)
	}
	if want := "; stashed the machine's copy of 1 file (git stash " + s.CopyStashRef + ")"; !strings.Contains(s.String(), want) {
		t.Fatalf("summary line %q, want %q in it", s.String(), want)
	}
	wantFile(t, f.guestRepo(), "README.md", "laptop README\n")
	wantFile(t, f.guestRepo(), "notes.md", "laptop notes\n")
	wantFile(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	if list := stashList(t, f.guestRepo()); !strings.Contains(list, stashCopyMsg) {
		t.Fatalf("stash list %q, want %q in it", list, stashCopyMsg)
	}
	// The named commit brings exactly the machine's README.md back.
	in := mustRun(t, f.guestRepo(), "git", "stash", "show", "--include-untracked", "--name-only", s.CopyStashRef)
	if in != "README.md" {
		t.Fatalf("the stash holds %q, want README.md alone", in)
	}
	if p := mustRun(t, f.guestRepo(), "git", "stash", "show", "-p", s.CopyStashRef); !strings.Contains(p, "+the agent's README") {
		t.Fatalf("the stash's diff:\n%s", p)
	}
}

// `m`: the machine's copy of the overlapping file stays, the rest of the
// laptop's work arrives, no "kept for the machine" stash is left behind,
// and the next sync asks again because the record left that path out.
func TestOverlapKeepMachineSyncsTheRestAndAsksAgain(t *testing.T) {
	f := overlapFixture(t)
	ctx := context.Background()
	c := &chooser{keys: []overlapChoice{overlapMachine}}
	s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with m: %v", err)
	}
	if s.KeptCopy != 1 || s.StashedCopy != 0 {
		t.Fatalf("summary %+v", s)
	}
	if want := "; kept the machine's copy of 1 file"; !strings.Contains(s.String(), want) {
		t.Fatalf("summary line %q, want %q in it", s.String(), want)
	}
	wantFile(t, f.guestRepo(), "README.md", "the agent's README\n")
	wantFile(t, f.guestRepo(), "notes.md", "laptop notes\n")
	wantFile(t, f.guestRepo(), "agent.txt", "the agent's file\n")
	if list := stashList(t, f.guestRepo()); strings.Contains(list, stashKeepMsg) {
		t.Fatalf("stash list %q still holds the kept-for-the-machine entry", list)
	}
	// The record left README.md out, so the next sync, with nothing new
	// on either side, refuses over it again rather than taking the
	// agent's copy for the last sync's own.
	rec := readFile(t, filepath.Join(f.guestRepo(), ".git", "repose-synced-paths"))
	if strings.Contains(rec, "README.md") {
		t.Fatalf("the record still names README.md: %q", rec)
	}
	writeAt(t, f.local, "notes.md", "laptop notes, later\n")
	stop := &chooser{}
	_, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: stop.choose})
	wantDirtyRefusal(t, err)
	if len(stop.shown) != 1 || stop.shown[0].files[0] != "README.md" {
		t.Fatalf("the second sync showed %v", stop.shown)
	}
	wantFile(t, f.guestRepo(), "README.md", "the agent's README\n")
}

// The probe for the record's part in it: with README.md written into the
// record as the machine left it, the second sync takes the agent's copy
// for the last sync's own and stashes it without asking. That is what
// repose_drop_pick_paths prevents.
func TestOverlapKeepMachineRecordProbe(t *testing.T) {
	f := overlapFixture(t)
	ctx := context.Background()
	c := &chooser{keys: []overlapChoice{overlapMachine}}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: c.choose}); err != nil {
		t.Fatalf("sync with m: %v", err)
	}
	rec := filepath.Join(f.guestRepo(), ".git", "repose-synced-paths")
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	hash := mustRun(t, f.guestRepo(), "git", "hash-object", "--no-filters", "--", "README.md")
	if err := os.WriteFile(rec, append(b, []byte(hash+"\x00README.md\x00")...), 0o644); err != nil {
		t.Fatal(err)
	}
	writeAt(t, f.local, "notes.md", "laptop notes, later\n")
	stop := &chooser{}
	s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: stop.choose})
	if err != nil || len(stop.shown) != 0 {
		t.Fatalf("with README.md in the record the second sync still refused (%v, shown %v); the record is not what decides", err, stop.shown)
	}
	if !s.StashedLastSync {
		t.Fatalf("summary %+v, want the agent's copy stashed as the last sync's own", s)
	}
	wantFile(t, f.guestRepo(), "README.md", "laptop README\n")
}

// `m` over a commit: the laptop's commit changes src/a.ts and the
// machine has it dirty. After m the checkout is at the laptop's commit
// and src/a.ts holds the machine's copy, uncommitted.
func TestOverlapKeepMachineOverACommit(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	writeAt(t, f.local, "src/a.ts", "laptop v1\n")
	mustRun(t, f.local, "git", "add", "src/a.ts")
	mustRun(t, f.local, "git", "commit", "-q", "-m", "a.ts")
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	writeAt(t, f.guestRepo(), "src/a.ts", "the agent's a.ts\n")
	writeAt(t, f.local, "src/a.ts", "laptop v2\n")
	mustRun(t, f.local, "git", "commit", "-q", "-a", "-m", "a.ts v2")

	c := &chooser{keys: []overlapChoice{overlapMachine}}
	s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with m: %v", err)
	}
	if s.KeptCopy != 1 {
		t.Fatalf("summary %+v", s)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD = %s, want the laptop's %s", got, want)
	}
	wantFile(t, f.guestRepo(), "src/a.ts", "the agent's a.ts\n")
	if st := mustRun(t, f.guestRepo(), "git", "status", "--porcelain", "--", "src/a.ts"); st != "M src/a.ts" && st != " M src/a.ts" {
		t.Fatalf("status of src/a.ts = %q, want an uncommitted change", st)
	}
}

// An untracked file on both sides at the same path: `m` keeps the
// machine's and leaves it untracked.
func TestOverlapKeepMachineUntracked(t *testing.T) {
	f := newSyncFixture(t)
	writeAt(t, f.guestRepo(), "scratch.txt", "the agent's scratch\n")
	writeAt(t, f.local, "scratch.txt", "the laptop's scratch\n")
	c := &chooser{keys: []overlapChoice{overlapMachine}}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with m: %v", err)
	}
	if s.KeptCopy != 1 {
		t.Fatalf("summary %+v", s)
	}
	wantFile(t, f.guestRepo(), "scratch.txt", "the agent's scratch\n")
	if st := mustRun(t, f.guestRepo(), "git", "status", "--porcelain", "--", "scratch.txt"); st != "?? scratch.txt" {
		t.Fatalf("status of scratch.txt = %q, want it untracked", st)
	}
}

// A second overlapping file that appears between the refusal and the
// key is not stashed: the guard refuses again, naming both, and the
// menu comes back.
func TestOverlapKeepLaptopRefusesAFileNotShown(t *testing.T) {
	f := overlapFixture(t)
	writeAt(t, f.local, "lib.go", "package lib\n")
	c := &chooser{}
	c.keys = []overlapChoice{overlapLaptop, overlapStop}
	// The agent writes lib.go while the user is at the menu.
	first := true
	opts := SyncOptions{Choose: func(d *dirtyTreeError) (overlapChoice, error) {
		if first {
			first = false
			writeAt(t, f.guestRepo(), "lib.go", "the agent's lib.go\n")
		}
		return c.choose(d)
	}}
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, opts)
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree || ee.msg != "" {
		t.Fatalf("err = %#v, want a silent exit %d", err, ExitDirtyRemoteTree)
	}
	if len(c.shown) != 2 {
		t.Fatalf("the menu was shown %d times, want 2", len(c.shown))
	}
	if got := strings.Join(c.shown[1].files, ","); got != "README.md,lib.go" && got != "lib.go,README.md" {
		t.Fatalf("the second refusal named %q, want both files", got)
	}
	wantFile(t, f.guestRepo(), "README.md", "the agent's README\n")
	wantFile(t, f.guestRepo(), "lib.go", "the agent's lib.go\n")
	if list := stashList(t, f.guestRepo()); strings.Contains(list, stashCopyMsg) {
		t.Fatalf("something was stashed: %q", list)
	}
}

// `s` is --stash-machine, to the same stash name and summary.
func TestOverlapStashIsTheFlag(t *testing.T) {
	f := overlapFixture(t)
	c := &chooser{keys: []overlapChoice{overlapStash}}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with s: %v", err)
	}
	if s.StashedFiles != 2 || s.StashRef == "" {
		t.Fatalf("summary %+v", s)
	}
	if want := "; stashed the machine's changes to 2 files (git stash " + s.StashRef + ")"; !strings.Contains(s.String(), want) {
		t.Fatalf("summary line %q", s.String())
	}
	if list := stashList(t, f.guestRepo()); !strings.Contains(list, stashRemoteMsg) {
		t.Fatalf("stash list %q", list)
	}
	wantFile(t, f.guestRepo(), "README.md", "laptop README\n")
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "agent.txt")); err == nil {
		t.Fatal("agent.txt is still there; --stash-machine takes every change")
	}
}

// Enter stops: exit 6 with nothing more printed (the refusal and the
// menu are on the screen already) and nothing on the machine moved.
func TestOverlapEnterStopsAndChangesNothing(t *testing.T) {
	f := overlapFixture(t)
	before := mustRun(t, f.guestRepo(), "git", "status", "--porcelain")
	c := &chooser{}
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree {
		t.Fatalf("err = %v, want exit %d", err, ExitDirtyRemoteTree)
	}
	if ee.msg != "" {
		t.Fatalf("Enter printed %q again", ee.msg)
	}
	if after := mustRun(t, f.guestRepo(), "git", "status", "--porcelain"); after != before {
		t.Fatalf("guest status %q, before %q", after, before)
	}
	wantFile(t, f.guestRepo(), "README.md", "the agent's README\n")
}

// Without a Choose (no terminal, or --json) the refusal is what it was:
// the same text, exit 6, nothing touched.
func TestOverlapWithoutATerminalIsUnchanged(t *testing.T) {
	f := overlapFixture(t)
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
	want := "Not synced: the machine changed 1 file that your laptop changed too:\n  README.md\n" +
		"`repose sync --stash-machine` moves the machine's changes to its git stash first."
	if got := err.(*exitError).msg; got != want {
		t.Fatalf("message:\n%s\nwant:\n%s", got, want)
	}
}

// overlapAsker is nil wherever there is nobody to ask.
func TestOverlapAskerIsNilWhereNobodyCanBeAsked(t *testing.T) {
	old := canAskOverlap
	t.Cleanup(func() { canAskOverlap = old })
	canAskOverlap = func() bool { return true }
	for name, tc := range map[string]struct {
		e    Env
		o    RunOptions
		want bool
	}{
		"sync on a terminal": {Env{}, RunOptions{Sync: true}, true},
		"--json":             {Env{JSON: true}, RunOptions{Sync: true}, false},
		"run":                {Env{}, RunOptions{}, false},
		"--stash-machine":    {Env{}, RunOptions{Sync: true, StashRemote: true}, false},
		"--discard-machine":  {Env{}, RunOptions{Sync: true, DiscardRemote: true}, false},
	} {
		if got := overlapAsker(&tc.e, tc.o) != nil; got != tc.want {
			t.Errorf("%s: asker set = %v, want %v", name, got, tc.want)
		}
	}
	canAskOverlap = func() bool { return false }
	if overlapAsker(&Env{}, RunOptions{Sync: true}) != nil {
		t.Error("no terminal: asker set")
	}
}

// The menu's keys, read one at a time from a fake terminal.
func TestOverlapMenuKeys(t *testing.T) {
	d := &dirtyTreeError{files: []string{"src/session.ts", "notes.md"}}
	for name, tc := range map[string]struct {
		in   string
		d    *dirtyTreeError
		want overlapChoice
		err  error
	}{
		"l":                 {"l", d, overlapLaptop, nil},
		"L":                 {"L", d, overlapLaptop, nil},
		"m":                 {"m", d, overlapMachine, nil},
		"s":                 {"S", d, overlapStash, nil},
		"Enter":             {"\r", d, overlapStop, nil},
		"newline":           {"\n", d, overlapStop, nil},
		"q":                 {"q", d, overlapStop, nil},
		"Esc":               {"\x1b", d, overlapStop, nil},
		"EOF":               {"", d, overlapStop, nil},
		"Ctrl-C":            {"\x03", d, overlapStop, errPromptInterrupted},
		"another key first": {"x9l", d, overlapLaptop, nil},
		"m with a submodule": {"ml", &dirtyTreeError{files: []string{"lib"}, subs: []string{"lib"}},
			overlapLaptop, nil},
	} {
		var w strings.Builder
		got, err := overlapMenu(strings.NewReader(tc.in), &w, tc.d)
		if got != tc.want || err != tc.err {
			t.Errorf("%s: %q, %v; want %q, %v", name, got, err, tc.want, tc.err)
		}
	}
}

// The menu as a user reads it, under the refusal's own list.
func TestOverlapMenuText(t *testing.T) {
	var w strings.Builder
	d := &dirtyTreeError{files: []string{"src/session.ts", "notes.md"}}
	if _, err := overlapMenu(strings.NewReader("\r"), &w, d); err != nil {
		t.Fatal(err)
	}
	want := "Not synced: the machine changed 2 files that your laptop changed too:\r\n" +
		"  src/session.ts\r\n" +
		"  notes.md\r\n" +
		"  l  use the laptop's copy; the machine's goes to its git stash\r\n" +
		"  m  keep the machine's copy; sync the rest\r\n" +
		"  s  move all of the machine's changes to its git stash, then sync\r\n" +
		"Choose l, m or s, or Enter to stop: \r\n"
	if w.String() != want {
		t.Fatalf("menu:\n%q\nwant:\n%q", w.String(), want)
	}

	w.Reset()
	sub := &dirtyTreeError{files: []string{"lib"}, subs: []string{"lib"}}
	if _, err := overlapMenu(strings.NewReader("\r"), &w, sub); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.String(), "  m  ") || !strings.Contains(w.String(), "Choose l or s, or Enter to stop: ") {
		t.Fatalf("with a submodule the menu reads:\n%q", w.String())
	}
}

// Past dirtyListMax the list still names 8 and a count, and the key acts
// on every overlapping file (decision 2 of the proposal).
func TestOverlapKeepLaptopActsOnEveryFile(t *testing.T) {
	f := newSyncFixture(t)
	for i := 0; i < 10; i++ {
		n := string(rune('a'+i)) + ".txt"
		writeAt(t, f.local, n, "laptop "+n+"\n")
		writeAt(t, f.guestRepo(), n, "the agent's "+n+"\n")
	}
	c := &chooser{keys: []overlapChoice{overlapLaptop}}
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	if err != nil {
		t.Fatalf("sync with l: %v", err)
	}
	if !strings.Contains(c.shown[0].list(), "  and 2 more\n") || len(c.shown[0].files) != 10 {
		t.Fatalf("the refusal listed:\n%s", c.shown[0].list())
	}
	if s.StashedCopy != 10 {
		t.Fatalf("summary %+v, want 10 stashed", s)
	}
	for i := 0; i < 10; i++ {
		n := string(rune('a'+i)) + ".txt"
		wantFile(t, f.guestRepo(), n, "laptop "+n+"\n")
	}
}

// A submodule among the overlapping paths is reported as one, so the
// menu can leave `m` out.
func TestOverlapNamesASubmodule(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	addSubmodule(t, f.local, newSubRepo(t), "lib")
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(f.guestRepo(), "lib"), "s", "the agent's sub\n")
	writeAt(t, filepath.Join(f.local, "lib"), "s", "the laptop's sub\n")
	c := &chooser{}
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Choose: c.choose})
	wantDirtyRefusal(t, err)
	if len(c.shown) != 1 || !c.shown[0].hasSubmodule() || c.shown[0].subs[0] != "lib" {
		t.Fatalf("refusal %+v, want lib named as a submodule", c.shown)
	}
}

// reusable: the callbacks `run` builds each read a channel once, so the
// second sync the menu starts would wait on them forever. The probe
// that proves it: without reusable, EnvLater blocks on the second call.
func TestSyncOptionsReusableAnswerTwice(t *testing.T) {
	envs := make(chan []envFile, 1)
	envs <- []envFile{{Rel: ".env"}}
	carries := make(chan *credCarry, 1)
	carries <- &credCarry{}
	probes := make(chan []byte, 1)
	probes <- []byte("probe")
	o := SyncOptions{
		EnvLater: func() []envFile { return <-envs },
		Carry:    func(map[string]string) (*credCarry, error) { return <-carries, nil },
		Probe:    func() ([]byte, error) { return <-probes, nil },
	}.reusable()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			if got := o.EnvLater(); len(got) != 1 || got[0].Rel != ".env" {
				t.Errorf("call %d: EnvLater = %v", i, got)
			}
			if c, err := o.Carry(nil); c == nil || err != nil {
				t.Errorf("call %d: Carry = %v, %v", i, c, err)
			}
			if b, err := o.Probe(); string(b) != "probe" || err != nil {
				t.Errorf("call %d: Probe = %q, %v", i, b, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a second call blocked: the menu's second sync would hang")
	}
}

// A Choose that answers `m` for a submodule overlap anyway stops: the
// menu never offers it, and restoring a submodule from a stash needs a
// commit of its own.
func TestOverlapMachineRefusedForASubmodule(t *testing.T) {
	d := &dirtyTreeError{files: []string{"lib"}, subs: []string{"lib"}}
	o := SyncOptions{Choose: func(*dirtyTreeError) (overlapChoice, error) { return overlapMachine, nil }}
	next, stop, err := overlapAnswer(o, &exitError{code: ExitDirtyRemoteTree, msg: d.Error(), cause: d})
	if !stop || next.KeepMachine != nil {
		t.Fatalf("stop = %v, KeepMachine = %v", stop, next.KeepMachine)
	}
	ee, ok := err.(*exitError)
	if !ok || ee.code != ExitDirtyRemoteTree || ee.msg != "" {
		t.Fatalf("err = %#v", err)
	}
}
