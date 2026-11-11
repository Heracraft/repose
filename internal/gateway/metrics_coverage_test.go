package gateway

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// TestGatewayMetricsExposed asserts every metric of 06-gateway-edge.md §5.5
// exists under repose_gateway_ (DECISIONS I-81). This is the checklist item
// "Every metric in 5.5 is exposed."
func TestGatewayMetricsExposed(t *testing.T) {
	reg := obsmetrics.New(obs.ComponentGateway)
	obsmetrics.NewGatewayMetrics(reg)
	families, err := reg.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	want := []string{
		"repose_gateway_sessions",
		"repose_gateway_sessions_total",
		"repose_gateway_auth_fail_total",
		"repose_gateway_dial_fail_total",
		"repose_gateway_route_duration_seconds",
		"repose_gateway_relay_bytes_total",
		"repose_gateway_session_seconds",
		"repose_gateway_revocation_cache_age_seconds",
		"repose_gateway_wgsync_peers",
		"repose_gateway_wgsync_errors_total",
		"repose_gateway_hook_events_total",
		"repose_gateway_webhook_requests_total",
		"repose_gateway_webhook_wake_seconds",
		"repose_gateway_webhook_inflight",
		"repose_gateway_webhook_routes",
	}
	var missing []string
	for _, w := range want {
		if !names[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing §5.5 metrics: %s", strings.Join(missing, ", "))
	}
	t.Logf("exposed: %s", strings.Join(want, " "))
}
