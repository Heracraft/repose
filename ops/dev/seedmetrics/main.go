// Command seedmetrics serves plausible values for every repose metric family
// on the ports the dev stack scrapes, so that the dashboards can be looked at
// without a host.
//
// It is a development tool, not part of the product: it imports the real
// metric definitions (internal/hostd/metrics for the host family,
// internal/api/metrics for the api's, internal/obs/metrics for the gateway's)
// and moves them around, so a
// panel that queries a series nobody defines renders empty here too. What it
// cannot tell you is whether a real host produces sensible values; that is a
// real-host checklist item.
//
//	go run ./ops/dev/seedmetrics            # :9101 hostd, :9102 gateway, :9103 api
//	docker compose -f ops/dev/docker-compose.yml up -d
//	# then http://<tailscale ip>:3000, folder "repose"
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apimetrics "github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/hostd/metrics"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

func main() {
	hostPort := flag.Int("hostd-port", 9101, "port for the hostd family")
	gwPort := flag.Int("gateway-port", 9102, "port for the gateway family")
	apiPort := flag.Int("api-port", 9103, "port for the api family")
	flag.Parse()

	host := metrics.NewVersion("dev-seed")
	gwM := obsmetrics.NewVersion(obs.ComponentGateway, "dev-seed")
	gw := obsmetrics.NewGatewayMetrics(gwM)
	apiM := obsmetrics.NewVersion(obs.ComponentAPI, "dev-seed")
	api := apimetrics.New(apiM)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serve := func(port int, h http.Handler) {
		mux := http.NewServeMux()
		mux.Handle("/metrics", h)
		srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			_ = srv.Close()
		}()
		go func() {
			if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "seedmetrics:", err)
			}
		}()
	}
	// Bound to every interface on purpose: the Prometheus container reaches
	// this through host.docker.internal, and the owner reaches Grafana over
	// Tailscale. A real exporter binds the WireGuard address and obs.Metrics
	// refuses anything else.
	serve(*hostPort, host.Handler())
	serve(*gwPort, gwM.Handler())
	serve(*apiPort, apiM.Handler())
	fmt.Printf("seedmetrics: hostd :%d, gateway :%d, api :%d; ctrl-c to stop\n", *hostPort, *gwPort, *apiPort)

	// One host worth of values, moved every 5 seconds so the timeseries
	// panels have a shape.
	const totalMem = 64 << 30
	const poolBytes = 512 << 30
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for step := 0; ; step++ {
		reserved := float64(int64(34<<30) + int64(rand.N(4<<30)))
		host.MemReservedBytes.Set(reserved)
		// Eight physical cores, as host-01 has, so HostCPUSaturated's
		// expression is a number rather than NaN in the local stack
		// (DECISIONS I-738).
		host.CPUCores.Set(8)
		host.Load1.Set(6 + 4*math.Sin(float64(step)/8))
		host.MemFreeBytes.Set(totalMem - 8<<30 - reserved)
		host.PoolBytes.Set(poolBytes)
		host.PoolFreeBytes.Set(poolBytes * (0.35 + 0.02*math.Sin(float64(step)/10)))
		host.StoreBytes.Set(float64(180<<30) + float64(rand.N(4<<30)))
		host.Guests.WithLabelValues("running", "large").Set(4)
		host.Guests.WithLabelValues("running", "small").Set(3)
		host.Guests.WithLabelValues("stopped", "large").Set(2)
		host.Guests.WithLabelValues("building", "xl").Set(float64(step % 2))
		host.BuildsRunning.Set(float64(step % 3))
		host.BuildQueueDepth.Set(float64(step % 2))
		host.StreamConnected.Set(1)
		host.GuestdLost.Set(0)
		host.SnapshotBytes.Set(float64(3 << 30))
		host.CommandsTotal.WithLabelValues("CreateGuest", "ok").Add(float64(rand.N(2)))
		host.CommandsTotal.WithLabelValues("Sample", "ok").Add(float64(3 + rand.N(3)))
		host.CommandsTotal.WithLabelValues("StopGuest", "error").Add(float64(rand.N(2) / 2))
		host.GuestCPUSecondsTotal.WithLabelValues("large").Add(rand.Float64() * 8)
		host.GuestCPUSecondsTotal.WithLabelValues("small").Add(rand.Float64() * 2)
		host.GuestNetBytesTotal.WithLabelValues("tx").Add(rand.Float64() * 5e6)
		host.GuestNetBytesTotal.WithLabelValues("rx").Add(rand.Float64() * 9e6)
		if step%4 == 0 {
			d := 20 + rand.Float64()*400
			host.BuildDuration.WithLabelValues("ok").Observe(d)
			host.BuildPhaseDuration.WithLabelValues("eval").Observe(2 + rand.Float64()*8)
			host.BuildPhaseDuration.WithLabelValues("build").Observe(d - 5)
			host.SnapshotDuration.WithLabelValues("scheduled", "ok").Observe(40 + rand.Float64()*120)
			host.SnapshotFreezeSeconds.Observe(0.05 + rand.Float64()*0.4)
			host.SnapshotBytesTotal.Add(3 << 30)
		}
		if step%9 == 0 {
			// The interesting cases, so that the "failures" panels have
			// something in them on this box: a fragment that does not
			// evaluate, a snapshot that failed, an overage charge that errored.
			host.BuildDuration.WithLabelValues("eval_failed").Observe(3 + rand.Float64()*4)
			host.SnapshotDuration.WithLabelValues("stop", "error").Observe(12 + rand.Float64()*20)
			api.BillingOverageChargesTotal.WithLabelValues("error").Inc()
		}

		gw.Sessions.Set(float64(2 + rand.N(4)))
		gw.SessionsTotal.Add(float64(rand.N(3)))
		gw.AuthFailTotal.WithLabelValues("expired").Add(float64(rand.N(2)))
		if step%7 == 0 {
			gw.AuthFailTotal.WithLabelValues("bad_cert").Inc()
			gw.DialFailTotal.Inc()
		}
		gw.RouteDuration.Observe(0.01 + rand.Float64()*0.05)

		api.Hosts.WithLabelValues("ready").Set(1)
		api.Hosts.WithLabelValues("unreachable").Set(0)
		api.Projects.WithLabelValues("running", "large").Set(4)
		api.Projects.WithLabelValues("running", "small").Set(3)
		api.Projects.WithLabelValues("stopped", "large").Set(2)
		api.RequestsTotal.WithLabelValues("GET /projects", "GET", "200").Add(float64(rand.N(5)))
		api.RequestsTotal.WithLabelValues("POST /projects/{id}/start", "POST", "200").Add(float64(rand.N(2)))
		api.RequestsTotal.WithLabelValues("GET /projects/{id}", "GET", "404").Add(float64(rand.N(2) / 2))
		api.RequestDuration.WithLabelValues("GET /projects").Observe(0.01 + rand.Float64()*0.1)
		api.CertsIssuedTotal.Add(float64(rand.N(2)))
		api.RollupLagSeconds.Set(float64(600 + rand.N(1200)))
		api.SnapshotAgeSeconds.Set(float64(20*3600 + rand.N(3600)))
		api.NotifyTotal.WithLabelValues("email", "ok").Add(float64(rand.N(2)))
		api.NotifyTotal.WithLabelValues("ntfy", "ok").Add(float64(rand.N(2)))
		api.BillingWebhookTotal.WithLabelValues("subscription.updated", "ok").Add(float64(rand.N(2)))
		api.BillingGateRefusedTotal.WithLabelValues("plan_limit").Add(float64(rand.N(2)))
		api.EgressAlertProjects.Set(0)

		select {
		case <-ctx.Done():
			fmt.Println("seedmetrics: stopping")
			return
		case <-tick.C:
		}
	}
}
