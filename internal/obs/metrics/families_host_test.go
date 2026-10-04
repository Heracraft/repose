// The host family lives in internal/hostd/metrics (workstream 03 registers
// it), so the check that it matches §5 is in an external test package: this
// package cannot import hostd's, but its external test can.
package metrics_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	hostmetrics "github.com/heracraft/repose/internal/hostd/metrics"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// TestHostFamily is the hostd half of the metric list in
// docs/workstreams/10-observability.md §5: every name, with the labels §5
// gives it.
func TestHostFamily(t *testing.T) {
	m := hostmetrics.New()
	// One series per vector, so the scrape carries its labels.
	m.Guests.WithLabelValues("running", "large").Set(1)
	m.CommandsTotal.WithLabelValues("CreateGuest", "ok").Inc()
	m.BuildDuration.WithLabelValues("ok").Observe(1)
	m.SnapshotDuration.WithLabelValues("scheduled", "ok").Observe(1)
	m.GuestCPUSecondsTotal.WithLabelValues("large").Add(1)
	m.GuestNetBytesTotal.WithLabelValues("tx").Add(1)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"repose_host_mem_free_bytes",
		"repose_host_mem_reserved_bytes",
		"repose_host_pool_free_bytes",
		"repose_host_store_bytes",
		`repose_host_guests{class="large",state="running"}`,
		"repose_host_builds_running",
		`repose_host_build_duration_seconds_bucket{result="ok"`,
		`repose_host_snapshot_duration_seconds_bucket{reason="scheduled",result="ok"`,
		"repose_host_snapshot_bytes",
		"repose_host_stream_connected",
		`repose_host_commands_total{kind="CreateGuest",result="ok"}`,
		"repose_host_guestd_lost",
		`repose_host_guest_cpu_seconds_total{class="large"}`,
		`repose_host_guest_net_bytes_total{direction="tx"}`,
		`repose_host_guest_notify_dropped_total{reason="rate_limited"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("host metrics do not contain %s", want)
		}
	}
}

// TestHostFamilyHasNoPerProjectLabel: guest_id and project_id are what §5
// forbids, and hostd once labelled guestd_unreachable by guest_id.
func TestHostFamilyHasNoPerProjectLabel(t *testing.T) {
	rec := httptest.NewRecorder()
	hostmetrics.New().Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, bad := range []string{"guest_id=", "project_id=", "user_id="} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("host metrics carry the label %s", bad)
		}
	}
}

// TestHostMetricsUseTheCheckedRegistry: every series is in the namespace, so
// hostd's own constructor cannot drift from obs.
func TestHostMetricsUseTheCheckedRegistry(t *testing.T) {
	fams, err := hostmetrics.New().Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(fams) == 0 {
		t.Fatal("no metrics registered")
	}
	for _, f := range fams {
		n := f.GetName()
		if strings.HasPrefix(n, obsmetrics.Namespace+"_") || strings.HasPrefix(n, "go_") || strings.HasPrefix(n, "process_") {
			continue
		}
		t.Errorf("series %s is outside the repose_ namespace", n)
	}
}
