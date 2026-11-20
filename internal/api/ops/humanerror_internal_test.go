package ops

import (
	"strings"
	"testing"
)

// A boot that answered but ran out of time says which phase ran out, and
// a boot that never answered does not promise a minute. Both sentences
// were wrong for every Mac customer: the appliance waits five minutes for
// Ready (nix/hosts/mac.nix, I-1376) while this sentence said one, and the
// registration and the set-up are two deadlines in two processes that
// read as one sentence, so nine starts on an appliance produced two
// error strings and no way to tell which phase had run out (ENG-80,
// DECISIONS I-1408).
func TestUnresponsiveBootSaysWhichPhaseRanOut(t *testing.T) {
	noReady, _ := humanError(KindCreate, PhaseCreateGuest, codeGuestUnresponsive, "create: step 10 (ready) failed: guest did not become ready: no Ready from guestd within 5m0s")
	if strings.Contains(noReady, "within a minute") {
		t.Errorf("a five-minute wait is reported as a minute: %q", noReady)
	}
	if !strings.HasPrefix(noReady, "the environment did not answer while it was starting;") || !strings.Contains(noReady, "`repose start` tries again") {
		t.Errorf("no Ready: %q", noReady)
	}

	// The registration of the closure's paths and the project's set-up
	// are different phases with different deadlines, and a user who
	// reports one is reporting something the next session can act on.
	paths, _ := humanError(KindStart, PhaseStartGuest, codeGuestUnresponsive,
		"create: step 10 (ready) failed: RegisterPaths: guestd: internal: register paths: run /run/current-system/sw/bin/nix-store: context deadline exceeded (the paths phase ran out of its 3m0s)")
	if !strings.Contains(paths, "loading its system") || !strings.Contains(paths, "`repose start` tries again") {
		t.Errorf("registration phase: %q", paths)
	}
	setup, _ := humanError(KindStart, PhaseStartGuest, codeGuestUnresponsive,
		"create: step 10 (ready) failed: SetupProject: context deadline exceeded (the setup phase ran out of its 3m0s)")
	if !strings.Contains(setup, "setting up the project") || !strings.Contains(setup, "`repose start` tries again") {
		t.Errorf("setup phase: %q", setup)
	}
	if paths == setup {
		t.Errorf("two phases, one sentence: %q", paths)
	}
	// An older hostd names no phase and no limit; its wording still has
	// to read as a set-up that ran out of time, for one release.
	old, _ := humanError(KindStart, PhaseStartGuest, codeGuestUnresponsive, "create: step 10 (ready) failed: SetupProject: context deadline exceeded")
	if !strings.Contains(old, "in time") {
		t.Errorf("an older hostd's wording: %q", old)
	}
	// A setup that failed for another reason is not a timeout.
	other, _ := humanError(KindCreate, PhaseCreateGuest, codeGuestUnresponsive, "create: step 10 (ready) failed: SetupProject: guestd: internal: start the tmux session unit: systemctl exited 1")
	if strings.Contains(other, "in time") {
		t.Errorf("a failed setup reads as a timeout: %q", other)
	}
}

// The api's own phase timeout reached the user as the engine wrote it for
// a log: "create_guest did not complete in 10m0s" names an internal phase
// and a Go duration and tells them nothing to do (ENG-80).
func TestHostPhaseTimeoutReadsAsASentence(t *testing.T) {
	got, ok := humanError(KindStart, PhaseStartGuest, "timeout", "start_guest did not complete in 10m0s")
	if !ok {
		t.Fatalf("the api's own timeout is passed through unmapped: %q", got)
	}
	for _, leak := range []string{"start_guest", "10m0s", "did not complete"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaks %q", got, leak)
		}
	}
	if !strings.Contains(got, "host") {
		t.Errorf("the sentence does not say the host did not answer: %q", got)
	}
}

// insufficient_capacity has five producers in hostd and the right thing
// to do differs by producer, so the sentence does (I-771, closing the
// choice I-756 left open). The api is the only place that sees hostd's
// message, so it is the only place that can tell them apart: the test
// pins each producer's own words, taken from the hostd source, against
// the one lever that must not spread -- stopping a machine frees memory
// and frees nothing else.
func TestCapacitySaysWhichKindAndWhatWorks(t *testing.T) {
	// A resize is refused by the same pool check as a create
	// (stop.go calls create.go's poolRoom), and "no machine starts on
	// it" is not what happened to a resize.
	if got, _ := humanError(KindResize, PhaseResize, "insufficient_capacity", "resize: thin pool 96% full"); !strings.Contains(got, "the disk cannot grow") {
		t.Errorf("a refused resize reads as a refused start: %q", got)
	}

	for _, c := range []struct {
		// hostd's message, verbatim from the producer named after it.
		msg   string
		want  string
		lever bool // whether "stopping one frees room" is true here
	}{
		// internal/hostd/guest/create.go:89 and :637
		{"create: not enough free memory for a large guest", "no free memory for a machine this size", true},
		// internal/hostd/guest/create.go:105 and :645
		{"create: thin pool 96% full", "disk pool is full", false},
		// internal/hostd/guest/manager.go:1033
		{"build queue full", "building as many machines at once as it can", false},
		// internal/hostd/guest/build.go:38
		{"host store full", "no disk left to build new systems", false},
		// internal/hostd/guest/create.go:76
		{"host draining", "being emptied", false},
		// create.go:165 and snapshot.go:260 wrap an LVM error in this
		// code; its text is the host's own and stays with operators.
		{"lvcreate: Volume group \"vg\" has insufficient free space", "no room for this project right now", false},
	} {
		got, handled := humanError(KindStart, PhaseStartGuest, "insufficient_capacity", c.msg)
		if !handled {
			t.Errorf("%q was passed through unhandled", c.msg)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%q\n got: %q\nwant it to contain: %q", c.msg, got, c.want)
		}
		if saysStopping := strings.Contains(got, "stopping a running machine frees room"); saysStopping != c.lever {
			t.Errorf("%q: says stopping frees room = %v, want %v\n got: %q", c.msg, saysStopping, c.lever, got)
		}
		// I-623 removed "(We have been alerted.)" everywhere, and that
		// is about reassurance rather than that one parenthetical, so
		// no sentence here reintroduces it in other words.
		if strings.Contains(got, "alerted") {
			t.Errorf("%q reassures instead of saying what works: %q", c.msg, got)
		}
		// I-623: no sentence of ours invites a wait that does not work.
		// The build queue is the one cause that clears on its own, and
		// it says so without "try again".
		if strings.Contains(got, "try again") {
			t.Errorf("%q still invites a retry: %q", c.msg, got)
		}
	}
}
