package control

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The VM test greps the journal for a fragment of ErrNotRunning, and
// nothing connected the two: I-990 rewrote the message and left
// `nix/hosts/tests/default.nix` grepping "hostd is not running", so
// `host-services` went red in the same release that added
// TestNotRunningDoesNotClaimHostdIsDown -- one test in this package
// forbidding the exact string another test in this repository required
// (DECISIONS I-1024).
//
// This reads the literal out of the nix file rather than restating it, so
// the next rewording fails here, in Go, in a second, instead of in an
// eight-minute VM build nobody runs on a message change.

var snapshotGrep = regexp.MustCompile(`journalctl -u repose-snapshot[^"]*grep -q '([^']*)'`)

func nixHostTests(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	p := filepath.Join(root, "nix", "hosts", "tests", "default.nix")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return p, string(b)
}

func TestVMTestGrepsWordingWeStillPrint(t *testing.T) {
	p, src := nixHostTests(t)
	m := snapshotGrep.FindAllStringSubmatch(src, -1)
	if len(m) == 0 {
		// A rename of the unit or a rewrite of the subtest lands here. It
		// is a failure and not a skip: a guard that silently matches
		// nothing is the thing this file exists to prevent.
		t.Fatalf("%s: no `journalctl -u repose-snapshot ... grep -q '...'` line; if the subtest moved, point this test at it", p)
	}
	msg := ErrNotRunning.Error()
	for _, g := range m {
		lit := g[1]
		// Only the lines quoting this error. The same subtest greps for
		// `queued 0 snapshot`, which hostd prints on a *successful* run and
		// which no constant here owns.
		if !strings.Contains(lit, "control socket") && !strings.Contains(lit, "hostd is") {
			continue
		}
		if !strings.Contains(msg, lit) {
			t.Errorf("%s greps for %q, which control.ErrNotRunning no longer says:\n%s", p, lit, msg)
		}
	}
}

func TestTheOldDiagnosisIsGoneFromTheVMTests(t *testing.T) {
	p, src := nixHostTests(t)
	// Comment lines are skipped, and that is not a loophole: this asks
	// whether anything *asserts* the words, and the line above the fixed
	// grep says what the old wording was and why it went, which is worth
	// keeping. The first draft of this test failed on its own comment --
	// as a check for orphaned decision citations did on its examples
	// earlier the same day -- so a guard that reads prose as evidence is
	// its own small instance of the fault here.
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "hostd is not running") {
			t.Errorf("%s:%d requires the wording I-990 removed: an absent control socket is equally a hostd that is still starting\n%s", p, i+1, line)
		}
	}
}
