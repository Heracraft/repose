package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/heracraft/repose/internal/gateway"
	"github.com/heracraft/repose/internal/gateway/preview"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// serve runs the SSH gateway and its side listeners until ctx is cancelled,
// or until it has handed over to a new gateway (DECISIONS I-471) and its
// last relay has ended.
func serve(ctx context.Context) error {
	log := obs.NewLogger(obs.LogOptions{Component: obs.ComponentGateway, Level: logLevel()})
	reg := obsmetrics.NewVersion(obs.ComponentGateway, version)
	m := obsmetrics.NewGatewayMetrics(reg)

	inherited, err := inheritedListeners()
	if err != nil {
		return err
	}
	ls := &listenerSet{inherited: inherited}
	defer ls.closeUnused()

	api, err := apiClient()
	if err != nil {
		return err
	}
	host, err := hostSigner()
	if err != nil {
		return err
	}
	gwKey, err := gatewaySigner()
	if err != nil {
		return err
	}

	gw, err := gateway.New(gateway.Config{
		API:        api,
		HostKey:    host,
		GatewayKey: gwKey,
		GuestPort:  22,
		Log:        log,
		Metrics:    m,
		Dial:       guestDial,
	})
	if err != nil {
		return err
	}
	// Fetch the CA and revocation list before accepting; if the api is down
	// the gateway still binds and refuses connections with the documented
	// message until a refresh succeeds (§6).
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := gw.Prime(pctx); err != nil {
		log.Warn("prime failed; starting with an empty cache", "event", "route_fail", "reason", "prime", "err", err.Error())
	}
	cancel()

	// The ssh socket is systemd's (gateway-ssh.socket, I-470) or the
	// previous gateway's; bound here only outside systemd.
	ln, err := ls.listen(lnSSH, env("GATEWAY_LISTEN", ":22"))
	if err != nil {
		return fmt.Errorf("gateway listen: %w", err)
	}
	log.Info("gateway listening", "event", "listen", "addr", ln.Addr().String())

	// accepting ends on SIGTERM or once a handover is done; relays end on
	// SIGTERM only, so after a handover they run until each ends.
	accepting, stopAccepting := context.WithCancel(ctx)
	defer stopAccepting()
	passOn := []namedListener{{lnSSH, ln}}

	var wg sync.WaitGroup
	run := func(name string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	relaysDone := make(chan struct{})
	refreshing, stopRefresh := context.WithCancel(ctx)
	defer stopRefresh()
	run("refresh", func() { gw.RefreshLoop(refreshing) })
	run("serve", func() {
		defer close(relaysDone)
		go func() {
			<-accepting.Done()
			_ = ln.Close()
		}()
		if err := gw.Serve(ctx, ln); err != nil {
			log.Error("gateway serve stopped", "event", "route_fail", "reason", "serve", "err", err.Error())
		}
	})

	// Metrics on the WireGuard address only (docs/ops/OBSERVABILITY.md).
	if addr := os.Getenv("METRICS_LISTEN"); addr != "" {
		if mln, err := metricsListener(ls, addr); err != nil {
			log.Error("listener disabled", "event", "route_fail", "reason", "metrics", "err", err.Error())
		} else {
			passOn = append(passOn, namedListener{lnMetrics, mln})
			run("metrics", func() { serveHTTP(accepting, log, "metrics", mln, metricsHandler(reg), "", "") })
		}
	}

	// Hook ingest on the WireGuard address :8443 with the edge's internal
	// certificate; guests reach it only over WireGuard (§5.7).
	if addr := os.Getenv("HOOK_LISTEN"); addr != "" {
		hi := gateway.NewHookIngest(api, log, m)
		if hln, ok := tlsListener(ls, log, lnHook, addr, os.Getenv("HOOK_TLS_CERT"), os.Getenv("HOOK_TLS_KEY")); ok {
			passOn = append(passOn, namedListener{lnHook, hln})
			run("hook-ingest", func() {
				serveHTTP(accepting, log, "hook-ingest", hln, hi.Handler(), os.Getenv("HOOK_TLS_CERT"), os.Getenv("HOOK_TLS_KEY"))
			})
		}
	}

	// Preview-proxy stub on :443 with the wildcard certificate (§5.8).
	if addr := env("PREVIEW_LISTEN", ":443"); os.Getenv("PREVIEW_TLS_CERT") != "" {
		if pln, ok := tlsListener(ls, log, lnPreview, addr, os.Getenv("PREVIEW_TLS_CERT"), os.Getenv("PREVIEW_TLS_KEY")); ok {
			passOn = append(passOn, namedListener{lnPreview, pln})
			run("preview", func() {
				serveHTTP(accepting, log, "preview", pln, preview.Handler(), os.Getenv("PREVIEW_TLS_CERT"), os.Getenv("PREVIEW_TLS_KEY"))
			})
		}
	}
	ls.closeUnused()

	ctl, err := listenControl(env("GATEWAY_CONTROL", controlDefault), log, func() []namedListener { return passOn })
	if err != nil {
		// Without the control socket the gateway serves as before and a
		// reload fails, which says so; nothing here is worth not serving.
		log.Error("handover disabled", "event", "listen", "listener", "control", "err", err.Error())
	} else {
		run("control", func() { ctl.serve(accepting) })
	}

	// Serving on every socket: tell the gateway handing over, if any, and
	// systemd (Type=notify).
	signalReady()
	_ = sdNotify("READY=1")

	handedOver := make(chan struct{})
	if ctl != nil {
		handedOver = ctl.handedOver
	}
	select {
	case <-ctx.Done():
		log.Info("gateway shutting down", "event", "session_close", "reason", "signal")
	case <-handedOver:
		log.Info("gateway draining", "event", "handover", "result", "draining", "relays", gw.Open())
		stopAccepting()
		// systemd must have read MAINPID= before this process may end
		// (the barrier usually guarantees it already).
		time.Sleep(drainFloor)
		select {
		case <-relaysDone:
		case <-ctx.Done():
		}
		log.Info("gateway drained", "event", "handover", "result", "drained")
	}
	stopAccepting()
	<-relaysDone
	stopRefresh()
	wg.Wait()
	return nil
}

// guestDial replaces the dial to a guest in this package's process tests;
// nil is the WireGuard dial.
var guestDial func(ctx context.Context, network, addr string) (net.Conn, error)

// drainFloor is the least a gateway that handed over lives on.
const drainFloor = 2 * time.Second

// tlsListener is the listener for a TLS side server, or false (logged)
// when its certificate is not there yet: the relay runs before workstream
// 11 has placed the certs.
func tlsListener(ls *listenerSet, log *slog.Logger, name, addr, certFile, keyFile string) (net.Listener, bool) {
	if certFile == "" || keyFile == "" {
		log.Warn("listener disabled: no certificate", "event", "listen", "listener", name)
		return nil, false
	}
	ln, err := ls.listen(name, addr)
	if err != nil {
		log.Error("listener stopped", "event", "route_fail", "reason", name, "err", err.Error())
		return nil, false
	}
	return ln, true
}

// metricsListener refuses an address that binds every interface, as
// obsmetrics.Serve does: docs/ops/OBSERVABILITY.md requires the WireGuard
// address.
func metricsListener(ls *listenerSet, addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("metrics address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return nil, fmt.Errorf("metrics address %q binds every interface; bind the WireGuard address (docs/ops/OBSERVABILITY.md)", addr)
	}
	return ls.listen(lnMetrics, addr)
}

func metricsHandler(reg *obsmetrics.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// serveHTTP serves h on ln (over TLS when certFile is set) until ctx ends.
// Shutting down closes this process's descriptor only; after a handover
// the new gateway's copy keeps the socket.
func serveHTTP(ctx context.Context, log *slog.Logger, name string, ln net.Listener, h http.Handler, certFile, keyFile string) {
	srv := &http.Server{
		Handler:           h,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listener up", "event", "listen", "listener", name, "addr", ln.Addr().String())
	var err error
	if certFile != "" {
		err = srv.ServeTLS(ln, certFile, keyFile)
	} else {
		err = srv.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listener stopped", "event", "route_fail", "reason", name, "err", err.Error())
	}
}

func logLevel() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
