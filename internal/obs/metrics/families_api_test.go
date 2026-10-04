// The api's family lives in internal/api/metrics (workstream 05 implements
// it), so the check that it matches §5 is here, in an external test package,
// beside the same check for hostd's. This workstream owns the names; the two
// components own the registrations, and these tests are what keeps the three
// from drifting (DECISIONS I-59).
package metrics_test

import (
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	apimetrics "github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// TestAPIFamily is the api half of the metric list in
// docs/workstreams/10-observability.md §5: every name, with the labels §5
// gives it.
func TestAPIFamily(t *testing.T) {
	m := obsmetrics.New(obs.ComponentAPI)
	a := apimetrics.New(m)

	// One series per vector, so the scrape carries its labels.
	a.RequestsTotal.WithLabelValues("GET /projects", "GET", "200").Inc()
	a.RequestDuration.WithLabelValues("GET /projects").Observe(0.01)
	a.Hosts.WithLabelValues("ready").Set(1)
	a.Projects.WithLabelValues("running", "large").Set(1)
	a.ScheduleTotal.WithLabelValues("ok").Inc()
	a.NotifyTotal.WithLabelValues("email", "ok").Inc()
	a.BillingWebhookTotal.WithLabelValues("subscription.created", "ok").Inc()
	a.BillingGateRefusedTotal.WithLabelValues("plan_limit").Inc()
	a.BillingSubscriptions.WithLabelValues("solo", "active").Inc()

	want := map[string][]string{
		"repose_api_requests_total":                {"method", "route", "status"},
		"repose_api_request_duration_seconds":      {"route"},
		"repose_api_hosts":                         {"state"},
		"repose_api_projects":                      {"class", "state"},
		"repose_api_schedule_total":                {"result"},
		"repose_api_certs_issued_total":            nil,
		"repose_api_certs_revoked_total":           nil,
		"repose_api_rollup_lag_seconds":            nil,
		"repose_api_notify_total":                  {"channel", "result"},
		"repose_api_billing_webhook_total":         {"kind", "result"},
		"repose_api_billing_overage_charges_total": {"result"},
		"repose_api_billing_gate_refused_total":    {"reason"},
		"repose_api_billing_subscriptions_total":   {"plan", "status"},
		"repose_api_billing_stops_total":           {"reason"},
		"repose_api_snapshot_age_seconds":          nil,
		"repose_api_egress_alert_projects":         nil,
		"repose_api_partition_drop_fail_total":     nil,
		// I-239: the abuse alerts' inputs.
		"repose_api_abuse_stops_total":              {"kind"},
		"repose_api_abuse_held_projects":            nil,
		"repose_api_abuse_busy_unattended_projects": nil,
		// I-445..I-447: what the api dropped or could not store from hosts.
		"repose_api_host_reports_refused_total": {"reason"},
		"repose_api_samples_failed_total":       {"reason"},
	}

	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	got := map[string][]string{}
	for _, f := range fams {
		var labels []string
		if len(f.GetMetric()) > 0 {
			for _, l := range f.GetMetric()[0].GetLabel() {
				labels = append(labels, l.GetName())
			}
		}
		sort.Strings(labels)
		got[f.GetName()] = labels
	}
	for name, labels := range want {
		have, ok := got[name]
		if !ok {
			t.Errorf("metric %s does not exist", name)
			continue
		}
		if strings.Join(have, ",") != strings.Join(labels, ",") {
			t.Errorf("metric %s has labels [%s], want [%s]",
				name, strings.Join(have, ","), strings.Join(labels, ","))
		}
	}
}

// TestAPIFamilyHasNoPerProjectLabel: the api serves the dashboards that need
// per-project figures from Postgres, never from a label (§5).
func TestAPIFamilyHasNoPerProjectLabel(t *testing.T) {
	m := obsmetrics.New(obs.ComponentAPI)
	apimetrics.New(m)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, bad := range []string{"project_id=", "guest_id=", "user_id=", "handle="} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("api metrics carry the label %s", bad)
		}
	}
}

// TestAPIMetricsUseTheCheckedRegistry: every api series is in the namespace,
// which is what makes the label rule bind at startup rather than at review.
func TestAPIMetricsUseTheCheckedRegistry(t *testing.T) {
	m := obsmetrics.New(obs.ComponentAPI)
	apimetrics.New(m)
	fams, err := m.Registry().Gather()
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
