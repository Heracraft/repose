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
	PoolAvailable         prometheus.Gauge
	StoreBytes            prometheus.Gauge
	MemFreeBytes          prometheus.Gauge
	MemReservedBytes      prometheus.Gauge
	GuestdLost            prometheus.Gauge
	// CPUCores and Load1 are for CPU what MemReservedBytes and
	// MemFreeBytes are for memory: the capacity figure and the load
	// against it. Cores are physical cores, not SMT threads, because that
	// is the denominator the latency measurement is in — HostCPUSaturated
	// divides the busy guest vCPUs by them (DECISIONS I-738). Neither
	// reaches Prometheus any other way: hosts.load1 is a Postgres column
	// and Prometheus scrapes exporters.
	CPUCores             prometheus.Gauge
	Load1                prometheus.Gauge
	GuestCPUSecondsTotal *prometheus.CounterVec
	GuestNetBytesTotal   *prometheus.CounterVec
	EventsPending        prometheus.Gauge
	GuestNotifyDropped   *prometheus.CounterVec
	// EgressBlockedTotal and EgressBlockedGuests are the per-guest
	// nftables blocks summed per host (DECISIONS I-238..I-240): packets
	// dropped by reason, and guests over their reason's threshold right
	// now. Which guest is in the egress_blocked log line; guest_id is
	// never a label.
	EgressBlockedTotal  *prometheus.CounterVec
	EgressBlockedGuests *prometheus.GaugeVec
	// GuestsInterrupted is the guests a host restart took down in the last
	// 6 h and not running since (DECISIONS I-645): above 0 for long, the
	// api has not started them (ResumeNotStarted).
	GuestsInterrupted prometheus.Gauge
	// GuestFreezeUnfrozen counts the running guests a freeze left with
	// their memory unsaved, by reason (DECISIONS I-796). Every such guest
	// loses what it was running when the host goes down, so this is the
	// series GuestLeftUnfrozen alerts on; the guest_freeze log line at
	// Error names the guest and its class, which is why class is not a
	// label here.
	//
	// GuestThawDiscarded is the other half of the same loss: a guest that
	// was frozen and whose image the next start could not give back loses
	// exactly what an unfrozen one loses. GuestImageDiscarded alerts on
	// it.
	//
	// Both are running totals read from freeze.Tally at each start, not
	// this process's own count: a shutdown freeze's counter is never
	// scraped (the host is going down behind it) and a thaw's is raised
	// before the metrics listener is up, so a per-process count would
	// publish the same number after every reboot and increase() would see
	// nothing.
	GuestFreezeUnfrozen *prometheus.CounterVec
	GuestThawDiscarded  *prometheus.CounterVec
}

// BlockedReasons is the `reason` enum of repose_host_egress_blocked_total
// and repose_host_egress_blocked_guests.
var BlockedReasons = []string{"smtp", "stratum", "flows"}

// NotifyDropReasons is the `reason` enum of repose_host_guest_notify_dropped_total.
var NotifyDropReasons = []string{"rate_limited", "invalid", "queue_full"}

// FreezeUnfrozenReasons is the `reason` enum of
// repose_host_guest_freeze_unfrozen_total: no_room (the thin pool has no
// room for the image), estimate (the snapshot would have outlasted the
// deadline), deadline (the deadline passed before the snapshot started),
// failed (the freeze failed after it started; the guest keeps running),
// no_key (the image key was not available), busy (another freeze or thaw
// held the lock past the deadline).
var FreezeUnfrozenReasons = []string{"no_room", "estimate", "deadline", "failed", "no_key", "busy"}

// ThawDiscardedReasons is the `reason` enum of
// repose_host_guest_thaw_discarded_total: incomplete (the image was never
// finished, so hostd was killed mid-snapshot), too_old (older than the
// restore window), changed (the guest's closure, class, volume or the
// hypervisor moved under the image), no_key (the image key was not
// available), failed (the restore itself failed) and window (the thaw ran
// out of time before this image was read). A guest that stopped, moved
// host or came back running on its own is not counted: it lost nothing.
var ThawDiscardedReasons = []string{"incomplete", "too_old", "changed", "no_key", "failed", "window"}

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
		PoolAvailable:         f.gauge("pool_available", "1 while the thin pool can serve guest volumes; 0 when it is inactive, missing or failed (DECISIONS I-644)."),
		StoreBytes:            f.gauge("store_bytes", "Bytes used by the host store filesystem."),
		MemFreeBytes:          f.gauge("mem_free_bytes", "Host free memory after the reserve."),
		MemReservedBytes:      f.gauge("mem_reserved_bytes", "Memory reserved by running guests."),
		CPUCores:              f.gauge("cpu_cores", "Physical cores on this host (not SMT threads)."),
		Load1:                 f.gauge("load1", "Host one-minute load average."),
		GuestdLost:            f.gauge("guestd_lost", "Count of guests with guestd_ok=false."),
		GuestsInterrupted:     f.gauge("guests_interrupted", "Guests a host restart took down in the last 6 h that have not run since (I-645)."),
		GuestFreezeUnfrozen:   f.counterVec("guest_freeze_unfrozen_total", "Running guests a freeze left unfrozen, so their memory is lost when the host goes down, by reason: no_room, estimate, deadline, failed, no_key, busy (I-796).", "reason"),
		GuestThawDiscarded:    f.counterVec("guest_thaw_discarded_total", "Frozen guests whose memory image a thaw discarded, so they started cold and lost what they were running, by reason: incomplete, too_old, changed, no_key, failed, window (I-796).", "reason"),
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
	// The same from startup for the freeze: GuestLeftUnfrozen reads an
	// increase, which needs a point before the rise, and a host that
	// froze nothing yet has none.
	for _, r := range FreezeUnfrozenReasons {
		m.GuestFreezeUnfrozen.WithLabelValues(r)
	}
	for _, r := range ThawDiscardedReasons {
		m.GuestThawDiscarded.WithLabelValues(r)
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
