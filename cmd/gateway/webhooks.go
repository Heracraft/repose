package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/heracraft/repose/internal/gateway"
	"github.com/heracraft/repose/internal/gateway/preview"
	"github.com/heracraft/repose/internal/gateway/webhook"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// The public :443 carries two names (DECISIONS I-1110, amending I-479):
// the webhook hostname, whose certificate Let's Encrypt issues over
// TLS-ALPN-01 on this very listener, and the preview stub's names, which
// still want a file. The listener therefore opens when either side has
// a certificate it can present, where before it opened only for a file
// that was never written.

// webhookTLS is the TLS configuration for :443: autocert for the webhook
// name, the preview key pair for anything else. Either half may be
// absent; nil means neither is there and the listener stays shut.
func webhookTLS(log *slog.Logger, host string, mgr *autocert.Manager, certFile, keyFile string) *tls.Config {
	var previewCert *tls.Certificate
	if certFile != "" && keyFile != "" {
		if c, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
			previewCert = &c
		} else {
			log.Warn("preview certificate does not load", "event", "listen", "listener", lnPreview, "err", err.Error())
		}
	}
	if mgr == nil && previewCert == nil {
		return nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if mgr != nil {
		// acme-tls/1 is how the certificate for the webhook name is
		// issued: the challenge is answered on this listener, so no DNS
		// token and no second port are needed.
		cfg.NextProtos = []string{acme.ALPNProto, "http/1.1"}
	}
	cfg.GetCertificate = func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if mgr != nil && (serverNameIs(hi.ServerName, host) || isALPNChallenge(hi)) {
			return mgr.GetCertificate(hi)
		}
		if previewCert != nil {
			return previewCert, nil
		}
		return nil, errors.New("no certificate for " + hi.ServerName)
	}
	return cfg
}

func serverNameIs(got, want string) bool {
	return want != "" && strings.EqualFold(strings.TrimSuffix(got, "."), want)
}

func isALPNChallenge(hi *tls.ClientHelloInfo) bool {
	for _, p := range hi.SupportedProtos {
		if p == acme.ALPNProto {
			return true
		}
	}
	return false
}

// publicHandler routes :443 by the Host header: the webhook hostname to
// the webhook handler, every other name to the preview stub as before.
func publicHandler(host string, h http.Handler) http.Handler {
	stub := preview.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Host
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		if h != nil && serverNameIs(name, host) {
			h.ServeHTTP(w, r)
			return
		}
		stub.ServeHTTP(w, r)
	})
}

// webhookManager is the autocert manager for the webhook hostname, or
// nil when WEBHOOK_HOST or its cache directory is not configured. The
// cache is a directory the unit must be able to write: the gateway's
// StateDirectory, since its state directory is read-only.
func webhookManager(log *slog.Logger, host, cacheDir string) *autocert.Manager {
	if host == "" {
		return nil
	}
	if cacheDir == "" {
		log.Warn("webhook hostname set without a certificate cache", "event", "listen", "listener", lnPreview, "host", host)
		return nil
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		log.Error("webhook certificate cache unusable", "event", "route_fail", "reason", "acme_cache", "err", err.Error())
		return nil
	}
	m := &autocert.Manager{
		Cache:      autocert.DirCache(cacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(host),
		Email:      os.Getenv("WEBHOOK_ACME_EMAIL"),
	}
	if dir := os.Getenv("WEBHOOK_ACME_DIRECTORY"); dir != "" {
		m.Client = &acme.Client{DirectoryURL: dir}
	}
	return m
}

// newWebhookHandler builds the webhook path, or nil when WEBHOOK_HOST is
// unset. It primes the route table before the listener accepts, so the
// first delivery after a restart is not a 404.
func newWebhookHandler(ctx context.Context, log *slog.Logger, m *obsmetrics.GatewayMetrics, api *gateway.Client, gw *gateway.Gateway, host string) *webhook.Handler {
	if host == "" {
		return nil
	}
	h := webhook.New(webhook.Config{API: api, Dial: gw, Log: log, Metric: m, Host: host})
	if err := h.Prime(ctx); err != nil {
		log.Warn("webhook route table not primed", "event", "webhook_table_fail", "err", err.Error())
	}
	return h
}

// publicListener opens :443 without asking for a certificate file: the
// webhook name's certificate comes from the handshake itself (I-479 as
// amended). It is still closed when neither name can be served, so a
// port open with nothing behind it never holds a client.
func publicListener(ls *listenerSet, log *slog.Logger, addr string) (net.Listener, bool) {
	ln, err := ls.listen(lnPreview, addr)
	if err != nil {
		log.Error("listener stopped", "event", "route_fail", "reason", lnPreview, "err", err.Error())
		return nil, false
	}
	return ln, true
}
