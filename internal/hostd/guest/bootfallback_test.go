package guest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
	"github.com/heracraft/repose/internal/hostd/state"
)

// The console kanali printed at 17:40Z on 2026-10-07 (I-590..I-592).
const stage2Missing = "<<< NixOS Stage 1 >>>\nloading module virtio_blk...\nmounting overlay on /mnt-root/nix/store...\n" +
	"stage 2 init script (/mnt-root/nix/store/1kyz-nixos-system-repose-guest-2026.10.05.1/init) not found\n\n" +
	"An error occurred in stage 1 of the boot process, which must mount the\nroot filesystem on `/mnt-root' and then start stage 2.\n"

func fallbackHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, func(c *Config) { c.ReadyTimeout = 300 * time.Millisecond })
}

// I-590: a start whose closure changed while the guest was stopped and
// never reaches Ready boots the last closure that did, once; the guest
// runs, and the result names the failed closure and the real reason.
func TestStartFallsBackToLastGoodClosure(t *testing.T) {
	h := fallbackHarness(t)
	h.create(gid1)
	if g := h.guest(gid1); g.LastGoodClosure != h.closure {
		t.Fatalf("a guest that reached Ready has last good %q, want %q", g.LastGoodClosure, h.closure)
	}
	if tgt, _ := h.roots.Get(goodRoot(gid1)); tgt != h.closure {
		t.Fatalf("last good closure not rooted: %q", tgt)
	}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	bad := fakeClosure(t, "nixos-system-v2")
	h.badClosure[bad] = stage2Missing
	// The offline apply of 17:40Z: the stopped guest's root moves.
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: bad}))
	start := cmd(&hostdv1.StartGuest{GuestId: gid1})
	res := h.mustOK(start)
	sr := res.GetStart()
	if sr == nil || sr.FailedClosure != bad || sr.BootedClosure != h.closure {
		t.Fatalf("start result %v, want a fallback from %s to %s", sr, bad, h.closure)
	}
	if sr.BootError.GetCode() != CodeBootFailed || !strings.Contains(sr.BootError.GetMessage(), "missing from the machine's store") {
		t.Fatalf("boot error %v", sr.BootError)
	}
	if !strings.Contains(sr.BootError.GetConsoleTail(), "stage 2 init script") {
		t.Fatalf("console tail not carried: %q", sr.BootError.GetConsoleTail())
	}
	g := h.guest(gid1)
	if g.State != StateRunning || g.SystemClosure != h.closure || g.LastGoodClosure != h.closure {
		t.Fatalf("after the fallback %+v", g)
	}
	if tgt, _ := h.roots.Get(gid1); tgt != h.closure {
		t.Fatalf("guest root %q after the fallback, want the closure it runs", tgt)
	}
	if g.BootFallback == nil || g.BootFallback.Failed != bad || g.BootFallback.CommandID != start.CommandId {
		t.Fatalf("fallback not recorded: %+v", g.BootFallback)
	}
	// No console text reached a log line.
	if strings.Contains(h.logs.String(), "mnt-root") || strings.Contains(h.logs.String(), "NixOS Stage 1") {
		t.Fatal("console text was logged")
	}
	// A repeat answers the stored result.
	if again := h.mustOK(clone(start)); again.GetStart().GetFailedClosure() != bad {
		t.Fatalf("repeat answered %v", again.GetStart())
	}
	// hostd restarted after the second boot and before the result was
	// stored: the re-run answers with the same fallback.
	replay := clone(start)
	replay.CommandId = "cmd-replay-start"
	g.BootFallback.CommandID = replay.CommandId
	if err := h.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.StartCommand(stateCommandPtr(replay)); err != nil {
		t.Fatal(err)
	}
	if r := h.mustOK(replay); r.GetStart().GetFailedClosure() != bad || r.GetStart().GetBootedClosure() != h.closure {
		t.Fatalf("replayed start answered %v", r.GetStart())
	}
	// A later start of the good closure is a plain start.
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	if r := h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1})); r.GetStart().GetFailedClosure() != "" {
		t.Fatalf("a plain start reported a fallback: %v", r.GetStart())
	}
	if h.guest(gid1).BootFallback != nil {
		t.Fatal("the old fallback outlived a plain start")
	}
}

// hostd died between recording the fallback and the second boot's end:
// the resent command boots the closure the record names and answers with
// the fallback.
func TestStartFallbackResentMidBoot(t *testing.T) {
	h := fallbackHarness(t)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	bad := fakeClosure(t, "nixos-system-v2")
	start := cmd(&hostdv1.StartGuest{GuestId: gid1})
	g := h.guest(gid1)
	g.State = StateStarting
	g.BootFallback = &state.BootFallback{CommandID: start.CommandId, Failed: bad, Code: CodeBootFailed, Message: "the system it boots is missing from the machine's store (stage 1 found no stage 2 init)"}
	if err := h.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.StartCommand(stateCommandPtr(start)); err != nil {
		t.Fatal(err)
	}
	res := h.mustOK(start)
	if res.GetStart().GetFailedClosure() != bad || res.GetStart().GetBootedClosure() != h.closure {
		t.Fatalf("resent start answered %v", res.GetStart())
	}
	if h.guest(gid1).State != StateRunning {
		t.Fatal("not running")
	}
}

// A guest with no last good closure (its first boot, a restored guest)
// has nothing to fall back to: the start fails with the real reason.
func TestBootFailureWithoutLastGoodReportsTheConsole(t *testing.T) {
	h := fallbackHarness(t)
	h.badClosure[h.closure] = "Kernel panic - not syncing: VFS: Unable to mount root fs\n"
	req := createReq(gid1)
	req.SystemClosure = h.closure
	res := h.mustFail(cmd(req), CodeBootFailed)
	if res.Error.Message != "the guest's kernel panicked while booting" {
		t.Fatalf("message %q", res.Error.Message)
	}
	if !strings.Contains(res.Error.ConsoleTail, "Kernel panic") {
		t.Fatalf("console tail %q", res.Error.ConsoleTail)
	}
	if g := h.guest(gid1); g.State != StateError || g.LastGoodClosure != "" {
		t.Fatalf("after a failed first boot %+v", g)
	}
	if strings.Contains(h.logs.String(), "Kernel panic") {
		t.Fatal("console text was logged")
	}
}

// When the last good closure fails too, the start fails with the first
// failure, once: no third boot.
func TestFallbackThatFailsTooEndsInError(t *testing.T) {
	h := fallbackHarness(t)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	bad := fakeClosure(t, "nixos-system-v2")
	h.badClosure[bad] = stage2Missing
	h.badClosure[h.closure] = "EXT4-fs error\nfsck failed with exit code 4\nAn error occurred in stage 1 of the boot process\n"
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: bad}))
	runs := h.runs(gid1)
	res := h.mustFail(cmd(&hostdv1.StartGuest{GuestId: gid1}), CodeBootFailed)
	if !strings.Contains(res.Error.Message, "missing from the machine's store") || !strings.Contains(res.Error.Message, "did not boot either") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if n := h.runs(gid1) - runs; n != 2 {
		t.Fatalf("%d boots, want 2 (the closure, then the last good one)", n)
	}
	if g := h.guest(gid1); g.State != StateError || g.SystemClosure != h.closure {
		t.Fatalf("after both failed %+v", g)
	}
}

// A forced reboot onto a closure that never reaches Ready (the nightly or
// a start applying a kernel change) leaves the guest running its last
// good closure and answers boot_failed, never guest_unresponsive, which
// the api would answer with a reboot onto the same closure.
func TestForcedRebootFallsBack(t *testing.T) {
	h := fallbackHarness(t)
	h.create(gid1)
	bad := fakeClosure(t, "nixos-system-v2")
	h.badClosure[bad] = "<<< NixOS Stage 1 >>>\n" // nothing hostd recognises
	apply := cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: bad, ForceReboot: true})
	h.gopts.NeedsReboot = map[string]bool{bad: true}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	res := h.mustFail(apply, CodeBootFailed)
	if !strings.Contains(res.Error.Message, "runs its previous system") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if g := h.guest(gid1); g.State != StateRunning || g.SystemClosure != h.closure {
		t.Fatalf("after the fallen-back apply %+v", g)
	}
	// Resent with the same id after a restart: no second reboot.
	replay := clone(apply)
	replay.CommandId = "cmd-replay-apply"
	g := h.guest(gid1)
	g.BootFallback.CommandID = replay.CommandId
	if err := h.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.StartCommand(stateCommandPtr(replay)); err != nil {
		t.Fatal(err)
	}
	runs := h.runs(gid1)
	h.mustFail(replay, CodeBootFailed)
	if h.runs(gid1) != runs {
		t.Fatal("a resent apply rebooted the guest again")
	}
}

// I-591: an apply to a stopped guest checks what the boot will read from
// the host before it moves the root.
func TestOfflineApplyChecksTheClosure(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	broken := fakeClosure(t, "nixos-system-noinit")
	if err := os.Remove(broken + "/init"); err != nil {
		t.Fatal(err)
	}
	res := h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: broken}), CodeNotFound)
	if !strings.Contains(res.Error.Message, "cannot boot") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if tgt, _ := h.roots.Get(gid1); tgt != h.closure {
		t.Fatal("a closure that cannot boot was adopted")
	}
	h.nix.FailRequisites = true
	ok := fakeClosure(t, "nixos-system-v3")
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: ok}), CodeNotFound)
	h.nix.FailRequisites = false
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: ok}))
}

// I-593: guestd's not_found for a closure its store does not show reaches
// the api as not_found, not internal.
func TestSwitchNotFoundIsNotInternal(t *testing.T) {
	h := newHarness(t, nil)
	h.gopts.Fail = map[string]*guestdv1.Error{"Switch": {Code: "not_found", Message: "switch: /nix/store/x-nixos-system is not in the store share"}}
	h.create(gid1)
	other := fakeClosure(t, "nixos-system-v2")
	res := h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: other}), CodeNotFound)
	if !strings.Contains(res.Error.Message, "not in the store share") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if g := h.guest(gid1); g.SystemClosure != h.closure || g.LastGoodClosure != h.closure {
		t.Fatalf("a failed switch moved the guest: %+v", g)
	}
	h.gopts.Fail = map[string]*guestdv1.Error{"Switch": {Code: "something_else", Message: "x"}}
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: other}), CodeInternal)
	// A switch that took makes the closure the last good one.
	h.gopts.Fail = nil
	h.mustOK(cmd(&hostdv1.StopGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.StartGuest{GuestId: gid1}))
	h.mustOK(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: other}))
	if g := h.guest(gid1); g.LastGoodClosure != other {
		t.Fatalf("last good after a switch %q", g.LastGoodClosure)
	}
}

// A guest running across the hostd upgrade that added the last good
// closure gets the one it runs; destroy removes its root.
func TestReconcileGivesARunningGuestItsLastGood(t *testing.T) {
	h := newHarness(t, nil)
	h.create(gid1)
	g := h.guest(gid1)
	g.LastGoodClosure = ""
	if err := h.st.PutGuest(g); err != nil {
		t.Fatal(err)
	}
	_ = h.roots.Remove(goodRoot(gid1))
	if err := h.m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := h.guest(gid1); g.LastGoodClosure != h.closure {
		t.Fatalf("reconcile left last good %q", g.LastGoodClosure)
	}
	if tgt, _ := h.roots.Get(goodRoot(gid1)); tgt != h.closure {
		t.Fatal("reconcile did not root it")
	}
	h.mustOK(cmd(&hostdv1.DestroyGuest{GuestId: gid1}))
	if _, err := h.roots.Get(goodRoot(gid1)); err == nil {
		t.Fatal("destroy left the last good root")
	}
}

func TestClassifyConsole(t *testing.T) {
	for _, c := range []struct{ console, code, has string }{
		{stage2Missing, CodeBootFailed, "stage 2 init"},
		{"Kernel panic - not syncing: Attempted to kill init!\n", CodeBootFailed, "panicked"},
		{"/dev/vda: UNEXPECTED INCONSISTENCY; RUN fsck MANUALLY.\n", CodeBootFailed, "check of the machine's disk"},
		{"fsck on /dev/vda failed.\nAn error occurred in stage 1 of the boot process\n", CodeBootFailed, "check of the machine's disk"},
		{"/dev/vda has unrepaired errors, please fix them manually.\n", CodeBootFailed, "check of the machine's disk"},
		{"mount: mounting /dev/vda on /mnt-root failed\nAn error occurred in stage 1 of the boot process\n", CodeBootFailed, "stage 1"},
		{"<<< Welcome to NixOS 25.11 (x86_64) - ttyS0 >>>\n", CodeGuestUnresponsive, "never answered"},
		{"", CodeGuestUnresponsive, "did not answer"},
	} {
		bf := classifyConsole(c.console)
		if bf.code != c.code || !strings.Contains(bf.message, c.has) {
			t.Errorf("classify(%q) = %+v, want %s containing %q", c.console, bf, c.code, c.has)
		}
	}
}

// runs counts the boots of a guest's hypervisor unit.
func (h *harness) runs(id string) int {
	n := 0
	for _, op := range h.sd.Ops {
		if op == "run guest@"+id {
			n++
		}
	}
	return n
}

// I-589: guestd's refusal of a closure a whiteout in the guest's store
// hides reaches the api with its own code and message, not as not_found
// (whose sentence says the system is missing) or internal.
func TestSwitchStorePathHiddenKeepsItsCode(t *testing.T) {
	h := newHarness(t, nil)
	h.gopts.Fail = map[string]*guestdv1.Error{"Switch": {Code: "store_path_hidden", Message: "switch: 1 of the store paths /nix/store/x-nixos-system needs are hidden in this machine's store"}}
	h.create(gid1)
	other := fakeClosure(t, "nixos-system-v2")
	res := h.mustFail(cmd(&hostdv1.ApplyConfig{GuestId: gid1, SystemClosure: other}), CodeStorePathHidden)
	if !strings.Contains(res.Error.Message, "hidden in this machine's store") {
		t.Fatalf("message %q", res.Error.Message)
	}
	if g := h.guest(gid1); g.SystemClosure != h.closure {
		t.Fatalf("a refused switch moved the guest: %+v", g)
	}
}
