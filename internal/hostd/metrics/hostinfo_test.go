package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// repose_host_info names the runtimes a hostd resolved at start, one
// series, so PR 8's alerts can scope away from hosts without Nix
// (DECISIONS I-1484).
func TestHostInfo(t *testing.T) {
	m := New()
	m.SetHostInfo(false, true, "abc123")
	want := `
# HELP repose_host_info 1 for this hostd's guest runtimes and version: nix_mode and erofs are on or off (I-1484).
# TYPE repose_host_info gauge
repose_host_info{erofs="on",nix_mode="off",version="abc123"} 1
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want), "repose_host_info"); err != nil {
		t.Fatal(err)
	}
	m.SetHostInfo(true, true, "abc123")
	if n := testutil.CollectAndCount(m.HostInfo); n != 1 {
		t.Fatalf("%d series after a second set, want 1", n)
	}
}
