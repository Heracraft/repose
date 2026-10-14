package metrics

import "github.com/prometheus/client_golang/prometheus"

// The api and gateway metric families of
// docs/workstreams/10-observability.md §5 live here rather than in
// internal/api and internal/gateway because this workstream owns metric
// names and those two binaries are built later (workstreams 05 and 06). A
// family defined here is a family they cannot accidentally rename: the test
// in metrics_test.go asserts every name and label of §5, and the dashboards
// and alert rules under ops/ query exactly these series.
//
// hostd's family is in internal/hostd/metrics, registered through the same
// checked registry.
//
// The api's family lived here until workstream 05 merged with its own
// implementation of the same names in internal/api/metrics; one
// implementation is the rule, so this package keeps only the gateway's, and
// families_api_test.go holds 05's to §5 (DECISIONS I-59). Two series in it are
// not in §5's list because §5's alert table and failure modes ask for them:
// repose_api_egress_alert_projects (the EgressHigh input, which cannot be a
// per-project series) and repose_api_partition_drop_fail_total (the §6 failure
// mode that says the api "logs partition_drop_fail and alerts").

// gatewayRouteBuckets: a route lookup over a second is a bug, so the high
// buckets exist only to keep the +Inf bucket from swallowing the tail.
var gatewayRouteBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}

// gatewaySessionBuckets span a keepalive interval to the 24 h relay cap
// (docs/workstreams/06-gateway-edge.md §5.4).
var gatewaySessionBuckets = []float64{1, 10, 60, 300, 1800, 3600, 4 * 3600, 12 * 3600, 24 * 3600}

// AuthFailReasons is the `reason` enum of the gateway's auth_fail event and
// of repose_gateway_auth_fail_total (docs/workstreams/10-observability.md §5,
// docs/interfaces/ssh-gateway.md).
var AuthFailReasons = []string{
	"no_cert", "bad_ca", "expired", "revoked", "wrong_principal",
	"stopped", "route_error", "no_address", "rate_limited", "not_found", "bad_login", "busy",
}

// GatewayMetrics is the gateway's family.
type GatewayMetrics struct {
	Sessions           prometheus.Gauge
	SessionsTotal      prometheus.Counter
	AuthFailTotal      *prometheus.CounterVec // reason
	DialFailTotal      prometheus.Counter
	RouteDuration      prometheus.Histogram
	RelayBytesTotal    *prometheus.CounterVec // direction: client_to_guest, guest_to_client
	SessionSeconds     prometheus.Histogram
	RevocationCacheAge prometheus.Gauge
	WGSyncPeers        prometheus.Gauge
	WGSyncErrorsTotal  prometheus.Counter
	HookEventsTotal    *prometheus.CounterVec // result: forwarded, rejected, rate_limited, api_error, not_found
}

// NewGatewayMetrics registers the gateway family. The auth-failure counter
// is initialised at zero for every reason, because
// rate(repose_gateway_auth_fail_total[5m]) in the GatewayAuthSpike alert
// cannot fire on a series that has never appeared.
func NewGatewayMetrics(m *Metrics) *GatewayMetrics {
	f := factory{m, "gateway"}
	g := &GatewayMetrics{
		Sessions:           f.gauge("sessions", "SSH sessions relayed right now."),
		SessionsTotal:      f.counter("sessions_total", "SSH sessions relayed."),
		AuthFailTotal:      f.counterVec("auth_fail_total", "Authentication failures by reason.", "reason"),
		DialFailTotal:      f.counter("dial_fail_total", "Failures dialling a guest's sshd."),
		RouteDuration:      f.histogram("route_duration_seconds", "Time to resolve a login name to a guest address.", gatewayRouteBuckets),
		RelayBytesTotal:    f.counterVec("relay_bytes_total", "Bytes relayed, by direction.", "direction"),
		SessionSeconds:     f.histogram("session_seconds", "Relay duration from accept to close.", gatewaySessionBuckets),
		RevocationCacheAge: f.gauge("revocation_cache_age_seconds", "Seconds since the revocation list last refreshed."),
		WGSyncPeers:        f.gauge("wgsync_peers", "WireGuard peers wgsync last applied."),
		WGSyncErrorsTotal:  f.counter("wgsync_errors_total", "wgsync cycles that failed."),
		HookEventsTotal:    f.counterVec("hook_events_total", "Hook events received on the ingest port, by result.", "result"),
	}
	for _, r := range AuthFailReasons {
		g.AuthFailTotal.WithLabelValues(r)
	}
	for _, d := range []string{"client_to_guest", "guest_to_client"} {
		g.RelayBytesTotal.WithLabelValues(d)
	}
	for _, r := range []string{"forwarded", "rejected", "rate_limited", "api_error", "not_found"} {
		g.HookEventsTotal.WithLabelValues(r)
	}
	return g
}

// factory registers each instrument on a Metrics under repose_<subsystem>_.
// Only the shapes the gateway's family needs are here; the api's family is
// workstream 05's (DECISIONS I-60) and hostd's has its own constructors.
type factory struct {
	m         *Metrics
	subsystem string
}

func (f factory) gauge(name, help string) prometheus.Gauge {
	c := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: Namespace, Subsystem: f.subsystem, Name: name, Help: help})
	f.m.MustRegister(c)
	return c
}

func (f factory) counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: Namespace, Subsystem: f.subsystem, Name: name, Help: help})
	f.m.MustRegister(c)
	return c
}

func (f factory) counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: Namespace, Subsystem: f.subsystem, Name: name, Help: help}, labels)
	f.m.MustRegister(c)
	return c
}

func (f factory) histogram(name, help string, buckets []float64) prometheus.Histogram {
	c := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: Namespace, Subsystem: f.subsystem, Name: name, Help: help, Buckets: buckets})
	f.m.MustRegister(c)
	return c
}
