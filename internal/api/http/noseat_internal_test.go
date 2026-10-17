package httpapi

import (
	"strings"
	"testing"
)

// DECISIONS I-756, completing I-724: the upgrade refused for want of a
// seat told the user to try again later, which is not how a seat is got —
// each one that frees is handed to the oldest account on the waitlist
// (waitlist.Inviter.Run). It names the waitlist instead, and promises no
// place on it: the answer carries only detail.reason "no_seat", and an
// upgrade does not join the list.
func TestNoSeatRefusalNamesTheWaitlistAndNoWait(t *testing.T) {
	if strings.Contains(strings.ToLower(msgNoSeat), "try again") || strings.Contains(strings.ToLower(msgNoSeat), "later") {
		t.Errorf("the refusal tells the user to wait: %q", msgNoSeat)
	}
	if !strings.Contains(msgNoSeat, "waitlist") {
		t.Errorf("the refusal does not name the waitlist: %q", msgNoSeat)
	}
	for _, word := range []string{"number", "position", "place"} {
		if strings.Contains(msgNoSeat, word) {
			t.Errorf("the refusal promises a place the answer does not carry (%q): %q", word, msgNoSeat)
		}
	}
}
