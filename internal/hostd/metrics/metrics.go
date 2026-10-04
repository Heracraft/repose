// Package metrics is hostd's Prometheus surface: every series named in
// docs/workstreams/03-hostd.md §5.14 and the host family in
// docs/workstreams/10-observability.md, under the repose_host_ prefix.
//
// The registry comes from internal/obs/metrics, which refuses a name outside
// the repose_ namespace and a label outside the low-cardinality list, so a
// series added here cannot break §5 without failing at startup.
package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// M holds the registry and every instrument.
type M struct {
	obs *obsmetrics.Metrics

	Guests                *prometheus.GaugeVec
	CommandsTotal         *prometheus.CounterVec
	CommandDuration       *prometheus.HistogramVec
	BuildQueueDepth       prometheus.Gauge
	BuildsRunning         prometheus.Gauge
	BuildDuration         *prometheus.HistogramVec
	BuildPhaseDuration    *prometheus.HistogramVec
	SnapshotBytesTotal    prometheus.Counter
	SnapshotBytes         prometheus.Gauge
	SnapshotFreezeSeconds prometheus.Histogram
	SnapshotDuration      *prometheus.HistogramVec
	StreamConnected       prometheus.Gauge
	StreamReconnectsTotal prometheus.Counter
	SamplesDroppedTotal   prometheus.Counter
	PoolFreeBytes         prometheus.Gauge
	PoolBytes             prometheus.Gauge
	StoreBytes            prometheus.Gauge
	MemFreeBytes          prometheus.Gauge
	MemReservedBytes      prometheus.Gauge
	GuestdLost            prometheus.Gauge
	GuestCPUSecondsTotal  *prometheus.CounterVec
	GuestNetBytesTotal    *prometheus.CounterVec
	EventsPending         prometheus.Gauge
	GuestNotifyDropped    *prometheus.CounterVec
	// EgressBlockedTotal and EgressBlockedGuests are the per-guest
	// nftables blocks summed per host (DECISIONS I-238..I-240): packets
	// dropped by reason, and guests over their reason's threshold right
	// now. Which guest is in the egress_blocked log line; guest_id is
	// never a label.
	EgressBlockedTotal  *prometheus.CounterVec
	EgressBlockedGuests *prometheus.GaugeVec
}

// BlockedReasons is the `reason` enum of the two series above.
var BlockedReasons = []string{"smtp", "stratum", "flows"}

// NotifyDropReasons is the `reason` enum of repose_host_guest_notify_dropped_total.
var NotifyDropReasons = []string{"rate_limited", "invalid", "queue_full"}

// New registers every instrument on a fresh registry.
func New() *M { return NewVersion("dev") }

// NewVersion is New with the binary's version for repose_build_info.
func NewVersion(version string) *M {
	om := obsmetrics.NewVersion(obs.ComponentHostd, version)
	f := promauto{om}
	m := &M{
		obs:             om,
		Guests:          f.gaugeVec("guests", "Guests on this host by state and class.", "state", "class"),
		CommandsTotal:   f.counterVec("commands_total", "Commands handled by kind and result.", "kind", "result"),
		CommandDuration: f.histVec("command_duration_seconds", "Command execution time by kind.", []float64{.1, .5, 1, 2, 5, 10, 30, 60, 120, 300, 600, 1800}, "kind"),
		BuildQueueDepth: f.gauge("build_queue_depth", "Builds waiting for a worker."),
		BuildsRunning:   f.gauge("builds_running", "Builds executing now."),
		BuildDuration:   f.histVec("build_duration_seconds", "Build wall time by result.", []float64{5, 15, 30, 60, 120, 300, 600, 1200, 1800}, "result"),
		// Eval and build have different caps (60 s and 30 minutes) and fail
		// for different reasons, so the Builds dashboard shows them apart.
		BuildPhaseDuration:    f.histVec("build_phase_duration_seconds", "Wall time of one build phase.", []float64{.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1200, 1800}, "phase"),
		SnapshotBytesTotal:    f.counter("snapshot_bytes_total", "Bytes uploaded to the snapshot store."),
		SnapshotBytes:         f.gauge("snapshot_bytes", "Bytes of the most recent snapshot upload."),
		SnapshotFreezeSeconds: f.hist("snapshot_freeze_seconds", "Freeze window per snapshot.", []float64{.05, .1, .25, .5, 1, 2, 5, 10}),
		SnapshotDuration:      f.histVec("snapshot_duration_seconds", "Snapshot wall time by reason and result.", []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800}, "reason", "result"),
		StreamConnected:       f.gauge("stream_connected", "1 while the Session stream to the api is up."),
		StreamReconnectsTotal: f.counter("stream_reconnects_total", "Session stream reconnects."),
		SamplesDroppedTotal:   f.counter("samples_dropped_total", "Samples dropped from the outage buffer."),
		PoolFreeBytes:         f.gauge("pool_free_bytes", "Thin pool free bytes."),
		PoolBytes:             f.gauge("pool_bytes", "Thin pool size."),
		StoreBytes:            f.gauge("store_bytes", "Bytes used by the host store filesystem."),
		MemFreeBytes:          f.gauge("mem_free_bytes", "Host free memory after the reserve."),
		MemReservedBytes:      f.gauge("mem_reserved_bytes", "Memory reserved by running guests."),
		GuestdLost:            f.gauge("guestd_lost", "Count of guests with guestd_ok=false."),
		GuestCPUSecondsTotal:  f.counterVec("guest_cpu_seconds_total", "Guest CPU time summed by class.", "class"),
		GuestNetBytesTotal:    f.counterVec("guest_net_bytes_total", "Guest network bytes by direction.", "direction"),
		EventsPending:         f.gauge("events_pending", "Events awaiting an api ack."),
		GuestNotifyDropped:    f.counterVec("guest_notify_dropped_total", "Guest notifications hostd did not forward, by reason: rate_limited (over the per-guest rate), invalid (a kind, id or state a guest may not send), queue_full (the guest events awaiting an ack are at their cap).", "reason"),
		EgressBlockedTotal:    f.counterVec("egress_blocked_total", "Outbound packets guests sent into a block, by reason: smtp (tcp 25), stratum (mining-pool ports), flows (new flows over the per-guest rate).", "reason"),
		EgressBlockedGuests:   f.gaugeVec("egress_blocked_guests", "Guests whose blocked packets in the last 10 minutes passed the reason's threshold.", "reason"),
	}
	// Every reason exists from startup, so the alert reads 0 rather than
	// nothing on a host where no guest was ever blocked.
	for _, r := range NotifyDropReasons {
		m.GuestNotifyDropped.WithLabelValues(r)
	}
	for _, r := range BlockedReasons {
		m.EgressBlockedTotal.WithLabelValues(r)
		m.EgressBlockedGuests.WithLabelValues(r).Set(0)
	}
	return m
}

// Handler serves the registry.
func (m *M) Handler() http.Handler { return m.obs.Handler() }

// Registry is the registry to gather from, for tests and for hostdev.
func (m *M) Registry() *prometheus.Registry { return m.obs.Registry() }

// Serve runs the /metrics endpoint until ctx is done. The address must name
// an interface: docs/ops/OBSERVABILITY.md requires the WireGuard address.
func (m *M) Serve(ctx context.Context, addr string) error { return m.obs.Serve(ctx, addr) }

type promauto struct{ reg *obsmetrics.Metrics }

// Every series is repose_host_<name>; obsmetrics.Namespace is the one
// definition of the prefix, and the registry checks it again at registration.
const sub = "host"

func (p promauto) gauge(name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help})
	p.reg.MustRegister(g)
	return g
}

func (p promauto) gaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help}, labels)
	p.reg.MustRegister(g)
	return g
}

func (p promauto) counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help})
	p.reg.MustRegister(c)
	return c
}

func (p promauto) counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help}, labels)
	p.reg.MustRegister(c)
	return c
}

func (p promauto) hist(name, help string, buckets []float64) prometheus.Histogram {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help, Buckets: buckets})
	p.reg.MustRegister(h)
	return h
}

func (p promauto) histVec(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: obsmetrics.Namespace, Subsystem: sub, Name: name, Help: help, Buckets: buckets}, labels)
	p.reg.MustRegister(h)
	return h
}
