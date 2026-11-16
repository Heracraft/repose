package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestPresencePrintsAttachedAndAway (DECISIONS I-1098): repose-presence
// prints one word and exits 0 in both states, and exits 1 with nothing on
// stdout when guestd does not answer, so an agent can branch on the word
// and tell the difference between "away" and "I could not ask".
func TestPresencePrintsAttachedAndAway(t *testing.T) {
	g := newGuestSide(t)
	run := func() (int, string, string) {
		var out, errb bytes.Buffer
		code := runPresence([]string{"--socket", g.socket}, &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, errs := run()
	if code != 0 || out != "away\n" || errs != "" {
		t.Fatalf("with nobody there: exit %d out %q err %q", code, out, errs)
	}

	g.mu.Lock()
	g.attached = true
	g.mu.Unlock()
	if code, out, errs = run(); code != 0 || out != "attached\n" {
		t.Fatalf("with a client: exit %d out %q err %q", code, out, errs)
	}

	var out2, err2 bytes.Buffer
	code = runPresence([]string{"--socket", filepath.Join(t.TempDir(), "gone.sock")}, &out2, &err2)
	if code != ExitError || out2.Len() != 0 || !strings.Contains(err2.String(), "not answering") {
		t.Fatalf("with no guestd: exit %d out %q err %q", code, out2.String(), err2.String())
	}

	var out3, err3 bytes.Buffer
	if code = runPresence([]string{"--socket", g.socket, "please"}, &out3, &err3); code != ExitUsage || out3.Len() != 0 {
		t.Fatalf("with an argument: exit %d out %q", code, out3.String())
	}
}
