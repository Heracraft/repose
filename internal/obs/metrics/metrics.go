// Package metrics is the Prometheus half of the observability rules: a
// registry that refuses a metric outside the repose_ namespace or with a
// label outside the low-cardinality list, and the api and gateway families of
// docs/workstreams/10-observability.md §5.
//
// It is a package of its own rather than part of internal/obs because a guest
// pays for what it imports: guestd has no metrics endpoint at all (it speaks
// vsock and nothing else), and linking the client library into it cost 2.5 MB
// of binary and 700 KB of resident memory against the 20 MB budget of
// docs/workstreams/04-guestd.md §7. See DECISIONS I-59.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/heracraft/repose/internal/obs"
)

// Namespace prefixes every metric repose exports.
const Namespace = "repose"

// allowedLabels is the low-cardinality label list of
// docs/workstreams/10-observability.md §5, plus the labels the families in
// the same section use (`result`, `direction`, `method`, `channel`) and
// `version` for repose_build_info. A label outside this set fails
// registration.
//
// project_id, guest_id and user_id are absent on purpose: 30 guests per host
// is fine as a label, but a label that grows with every project ever created
// makes Prometheus the first thing to fall over. Per-project figures come
// from meter_samples in Postgres, which the billing and per-guest dashboards
// query directly.
var allowedLabels = map[string]bool{
	"component": true, "host_id": true, "class": true, "state": true,
	"kind": true, "reason": true, "route": true, "status": true,
	"result": true, "direction": true, "method": true, "channel": true,
	"version": true,
	// A build has two phases, eval and build, with different caps and
	// different failure modes (docs/workstreams/10-observability.md
	// "Dashboards": "eval vs build time").
	"phase": true,
	// The api counts secrets operations and ops by the operation's name
	// (set, get, rm, rewrap; create, start, stop, ...): a bounded enum from
	// docs/interfaces/api.md, not an identifier.
	"op": true,
	// The plan a subscription is on: solo, plus, pro or own (DECISIONS
	// I-289, I-362, I-1364).
	"plan": true,
	// The guest runtimes a host runs, on repose_host_info: on or off
	// each (DECISIONS I-1484), two values apiece.
	"nix_mode": true, "erofs": true,
}

// AllowedLabels lists the permitted metric label names, sorted.
func AllowedLabels() []string {
	out := make([]string, 0, len(allowedLabels))
	for k := range allowedLabels {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ErrMetricName is returned (and panicked on by MustRegister) when a
// collector breaks the naming rules.
var ErrMetricName = errors.New("obs: metric breaks the repose naming rules")

// Metrics is a component's Prometheus surface: a registry that refuses a
// metric outside the repose_ namespace or with a label outside
// AllowedLabels, the Go and process collectors, and repose_build_info.
type Metrics struct {
	component obs.Component
	registry  *prometheus.Registry
}

// New builds the registry for a component. Panics on an unknown
// component or if the built-in collectors cannot be registered, both of
// which are programming errors visible on the first run.
func New(c obs.Component) *Metrics {
	return newMetrics(c, "dev")
}

// NewVersion is New with the binary's version for repose_build_info.
func NewVersion(c obs.Component, version string) *Metrics {
	return newMetrics(c, version)
}

func newMetrics(c obs.Component, version string) *Metrics {
	if err := checkComponent(c); err != nil {
		// A component name is a constant in the calling binary, so this is a
		// programming error that every run reproduces; failing at startup is
		// better than exporting series nothing scrapes.
		panic(err)
	}
	m := &Metrics{component: c, registry: prometheus.NewRegistry()}
	// The Go and process collectors are the only series outside the repose_
	// namespace, and they are registered on the registry directly rather
	// than through the checked Registerer: go_* and process_* are what every
	// Prometheus dashboard for a Go binary expects, and the alternative
	// (renaming them) would break those dashboards for no gain.
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	if version == "" {
		version = "dev"
	}
	info := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "build_info",
		Help:      "1, labelled with the component and version of the running binary.",
	}, []string{"component", "version"})
	info.WithLabelValues(string(c), version).Set(1)
	m.MustRegister(info)
	return m
}

// checkComponent refuses a component obs does not know; the name is a
// constant in the calling binary, so this fires on the first run.
func checkComponent(c obs.Component) error {
	if !c.Valid() {
		return fmt.Errorf("obs/metrics: unknown component %q; use one of %v", c, obs.Components)
	}
	return nil
}

// Registry is the registry to gather from.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Component is the component this surface belongs to.
func (m *Metrics) Component() obs.Component { return m.component }

// Register adds a collector after checking its names and labels.
func (m *Metrics) Register(c prometheus.Collector) error {
	if err := CheckCollector(c); err != nil {
		return err
	}
	return m.registry.Register(c)
}

// MustRegister adds collectors, panicking on a naming violation or a
// duplicate. This is what a component's metrics constructor calls, so a
// metric that breaks the rules stops the binary at startup and fails that
// component's first unit test rather than shipping.
func (m *Metrics) MustRegister(cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := m.Register(c); err != nil {
			// A metric name and its labels are constants too: this panic is
			// the enforcement of §5, and it fires on the first run and in the
			// component's own unit tests rather than in production.
			panic(err)
		}
	}
}

// Unregister removes a collector.
func (m *Metrics) Unregister(c prometheus.Collector) bool { return m.registry.Unregister(c) }

// Handler serves the registry on /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// Serve runs an HTTP server with /metrics and a /healthz for Coolify's
// health check, until ctx is done. addr is host:port; the host part matters,
// because docs/ops/OBSERVABILITY.md requires every exporter to listen on the
// WireGuard address and nothing to be reachable from the internet. An empty
// host would bind every interface, so it is refused.
func (m *Metrics) Serve(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("metrics address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return fmt.Errorf("metrics address %q binds every interface; bind the WireGuard address (docs/ops/OBSERVABILITY.md)", addr)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut the metrics server down: %w", err)
		}
		return nil
	}
}

var metricName = regexp.MustCompile(`^repose_[a-z0-9]+(_[a-z0-9]+)*$`)

// CheckCollector reports the first naming rule a collector breaks: a metric
// outside the repose_ namespace, a name that is not lower_snake, or a label
// outside AllowedLabels.
func CheckCollector(c prometheus.Collector) error {
	ch := make(chan *prometheus.Desc, 64)
	go func() {
		c.Describe(ch)
		close(ch)
	}()
	for d := range ch {
		if err := checkDesc(d.String()); err != nil {
			// Drain, or Describe blocks on an unbuffered send and leaks the
			// goroutine.
			go func() {
				for range ch {
				}
			}()
			return err
		}
	}
	return nil
}

// descFields pulls the fqName and the label names out of a Desc's String(),
// which is the only accessor the client library exposes. The format is
// stable: Desc{fqName: "x", help: "y", constLabels: {...}, variableLabels: {a,b}}.
var descFields = regexp.MustCompile(`fqName: "([^"]*)".*variableLabels: \{([^}]*)\}`)

func checkDesc(desc string) error {
	mm := descFields.FindStringSubmatch(desc)
	if mm == nil {
		return fmt.Errorf("%w: cannot read %s", ErrMetricName, desc)
	}
	name, labels := mm[1], mm[2]
	if !metricName.MatchString(name) {
		return fmt.Errorf("%w: %q is not repose_<lower_snake> (docs/workstreams/10-observability.md §5)", ErrMetricName, name)
	}
	for _, l := range strings.Split(labels, ",") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !allowedLabels[l] {
			return fmt.Errorf("%w: %q has label %q, which is not in the low-cardinality list %v", ErrMetricName, name, l, AllowedLabels())
		}
	}
	// constLabels appear in the same string; a const label is as much a
	// series dimension as a variable one.
	if i := strings.Index(desc, "constLabels: {"); i >= 0 {
		rest := desc[i+len("constLabels: {"):]
		if j := strings.Index(rest, "}"); j >= 0 {
			for _, kv := range strings.Split(rest[:j], ",") {
				k := strings.TrimSpace(kv)
				if k == "" {
					continue
				}
				if eq := strings.Index(k, "="); eq >= 0 {
					k = k[:eq]
				}
				if !allowedLabels[strings.Trim(k, `"`)] {
					return fmt.Errorf("%w: %q has const label %q, which is not in the low-cardinality list %v", ErrMetricName, name, k, AllowedLabels())
				}
			}
		}
	}
	return nil
}
