package httpapi_test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/api/pki"
)

// The /internal listener admits the gateway's client certificate and no
// other identity from the same CA: a host's certificate (CN = host id)
// fails the handshake on every route (I-431).
func TestInternalAdmitsOnlyTheGateway(t *testing.T) {
	e := newEnv(t)
	base, err := e.h.HostMgr.TLSConfig(nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(e.srv.InternalHandler())
	srv.TLS = pki.RequireClientName(base, pki.GatewayClientName)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := func(cn string) *http.Client {
		var certs []tls.Certificate
		if cn != "" {
			c, err := e.h.CA.X509().ClientTLS(cn)
			if err != nil {
				t.Fatal(err)
			}
			certs = []tls.Certificate{c}
		}
		roots := x509.NewCertPool()
		roots.AddCert(e.h.CA.X509().Cert)
		return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", Certificates: certs, MinVersion: tls.VersionTLS13}}}
	}

	var routes []string
	for _, r := range e.srv.Routes() {
		if strings.Contains(r, " /v1/internal/") {
			routes = append(routes, r)
		}
	}
	if len(routes) < 7 {
		t.Fatalf("internal routes: %v", routes)
	}
	for _, who := range []string{e.h.HostID.String(), "", "gateway-2", "Gateway"} {
		c := client(who)
		for _, r := range routes {
			method, path, _ := strings.Cut(r, " ")
			req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
			res, err := c.Do(req)
			if err == nil {
				_ = res.Body.Close()
				t.Fatalf("client %q reached %s: %d", who, r, res.StatusCode)
			}
		}
	}

	res, err := client(pki.GatewayClientName).Get(srv.URL + "/v1/internal/ca")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("gateway: %d", res.StatusCode)
	}
}
