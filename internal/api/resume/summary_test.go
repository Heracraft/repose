package resume

import (
	"testing"
	"time"
)

// A host of the account's own is named as theirs, and the line says the
// memory is lost; a server of repose's keeps its words (review-dash P3 #9).
func TestRestartedSummaryNamesAnOwnHost(t *testing.T) {
	at := time.Date(2026, 11, 27, 10, 25, 0, 0, time.UTC)
	if got := restartedSummary(nil, at, "mac-mini"); got != "your host mac-mini restarted, and what this machine held in memory is lost; it was back at 10:25 UTC" {
		t.Fatalf("own host: %q", got)
	}
	if got := restartedSummary(nil, at, ""); got != "the server running this machine restarted; it was back at 10:25 UTC" {
		t.Fatalf("fleet: %q", got)
	}
}
