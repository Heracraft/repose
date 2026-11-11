package cli

import (
	"strings"
	"testing"
)

// DECISIONS I-239: a project the platform stopped because a miner was
// running says so on `repose status`, in `repose ls` and when a
// command needs it running; an older reason on a stopped project (a failed
// snapshot) is not shown as if it were one.
func TestAbuseStopReasonIsShown(t *testing.T) {
	le := "abuse_stopped: stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.run/terms"
	p := &Project{Slug: "hashy", Class: "small", State: "stopped", LastError: &le}
	var b strings.Builder
	writeStatusLines(&b, p, nil, nil, nil)
	if !strings.Contains(b.String(), "\n  stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.run/terms\n") {
		t.Fatalf("status: %s", b.String())
	}
	b.Reset()
	writeProjectsTable(&b, []Project{*p})
	if !strings.Contains(b.String(), "hashy: stopped: a cryptocurrency miner (xmrig)") {
		t.Fatalf("projects: %s", b.String())
	}
	if msg := notRunningMessage(p); msg != "hashy is stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.run/terms." {
		t.Fatalf("not running: %s", msg)
	}
	other := "snapshot_failed: the snapshot upload failed"
	q := &Project{Slug: "izma", State: "stopped", LastError: &other}
	b.Reset()
	writeStatusLines(&b, q, nil, nil, nil)
	if strings.Contains(b.String(), "snapshot upload") || abuseStopReason(q) != "" {
		t.Fatalf("a non-abuse last_error shown on a stopped project: %s", b.String())
	}
	running := &Project{Slug: "hashy", State: "running", LastError: &le}
	if abuseStopReason(running) != "" {
		t.Fatal("a running project's stale abuse reason shown")
	}
}
