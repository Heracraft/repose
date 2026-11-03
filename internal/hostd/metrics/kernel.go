package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/heracraft/repose/internal/hostd/hostinfo"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// kernelCollector exports the kernel's latched verdict on this host:
// repose_host_kernel_tainted by reason, and repose_host_panic_on_oops
// (DECISIONS I-1010).
//
// It is a Collector rather than two gauges a loop Sets, because the value
// has to be the one the scrape asks for. The 60-second samplesLoop in
// internal/hostd/app is the billing sampler; whether this host's kernel
// can be trusted has no business riding it, and a reader who finds the
// gauge stale cannot tell a quiet host from a stopped loop.
//
// What this is worth is not the page. It is that a latched bit does not go
// green on its own. KernelOops is an event over a window and host-02
// priced it: 8 minutes of alert for 3 h 48 m of untrustworthy host. The
// kernel's taint word stands until the box reboots, so a host that faulted
// at 13:51 still reads tainted at 17:39. Do not improve this into a rate.
type kernelCollector struct {
	tainted     *prometheus.Desc
	panicOnOops *prometheus.Desc
	// Injected so the fixtures can be read without a /proc.
	readTainted func() (map[string]bool, error)
	readPanic   func() (bool, error)
}

// newKernelCollector reads the live /proc paths. The two reads are fields
// rather than calls so this package's own test can hand it a fixture; the
// paths themselves are hostinfo's constants and are not a parameter,
// because nothing but a test has any business naming them.
func newKernelCollector() *kernelCollector {
	return &kernelCollector{
		tainted: prometheus.NewDesc(
			prometheus.BuildFQName(obsmetrics.Namespace, sub, "kernel_tainted"),
			"1 when the kernel has latched this taint on this boot, or when the taint word could not be read, by reason: die, machine_check, bad_page (HostKernelTainted pages on these three), soft_lockup, warn (series only) (I-1010).",
			[]string{"reason"}, nil),
		panicOnOops: prometheus.NewDesc(
			prometheus.BuildFQName(obsmetrics.Namespace, sub, "panic_on_oops"),
			"1 while kernel.panic_on_oops is on, so an oops reboots the host instead of leaving it serving tenants with a kernel it cannot trust; 0 when it is off or unreadable (I-984, I-988).",
			nil, nil),
		readTainted: func() (map[string]bool, error) { return hostinfo.Tainted() },
		readPanic:   func() (bool, error) { return hostinfo.PanicOnOops() },
	}
}

func (c *kernelCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tainted
	ch <- c.panicOnOops
}

// Collect reads /proc at scrape time. Both errors are discarded on
// purpose: hostinfo returns the fail-closed value with the error, so the
// published series already says "unreadable" in the only vocabulary a
// gauge has, and a log line per scrape would say it every 15 seconds for
// as long as the condition lasts.
func (c *kernelCollector) Collect(ch chan<- prometheus.Metric) {
	reasons, _ := c.readTainted()
	for _, r := range hostinfo.TaintReasons {
		ch <- prometheus.MustNewConstMetric(c.tainted, prometheus.GaugeValue, boolGauge(reasons[r]), r)
	}
	on, _ := c.readPanic()
	ch <- prometheus.MustNewConstMetric(c.panicOnOops, prometheus.GaugeValue, boolGauge(on))
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
