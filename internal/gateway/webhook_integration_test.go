package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/gateway/webhook"
	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// webhookTestAPI is the real internal client for the parts the api
// serves in this harness (the project's state and its wake), with the
// route table and the delivery batches held in the test: the fake api
// has no webhook tables, and what this test is for is the path from the
// edge's :443 to a listener on the guest's loopback.
type webhookTestAPI struct {
	*Client
	table   webhook.RouteTable
	batches []webhook.Delivery
}

func (a *webhookTestAPI) WebhookRoutes(context.Context, string) (*webhook.RouteTable, error) {
	t := a.table
	return &t, nil
}

func (a *webhookTestAPI) WebhookStart(context.Context, string, string) (bool, string, error) {
	return false, "not_stopped", nil
}

func (a *webhookTestAPI) ReportWebhookDeliveries(_ context.Context, batch []webhook.Delivery) error {
	a.batches = append(a.batches, batch...)
	return nil
}

// A POST with a provider-style signature header reaches a listener on
// the guest's own loopback through the gateway's SSH connection and a
// direct-tcpip channel, with the body unchanged, and the app's reply
// comes back. This is the whole public path short of TLS: the edge's
// handler, the api's route answer, a real SSH handshake against a guest
// sshd that trusts the Host CA, and the channel `repose open` uses.
func TestWebhookDeliveryReachesTheGuestOverSSH(t *testing.T) {
	h := newHarness(t, harnessOpts{})

	// The app inside the machine: it listens on the guest's loopback,
	// checks a GitHub-style signature over the raw body, and answers.
	const secret = "whsec_integration"
	var gotBody []byte
	var gotSig, gotHost, gotTarget, gotWebhook string
	app := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotSig, gotHost = b, r.Header.Get("X-Hub-Signature-256"), r.Host
		gotTarget, gotWebhook = r.URL.RequestURI(), r.Header.Get("Repose-Webhook")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app.Listener = ln
	app.Start()
	defer app.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	token := "Xq3k9LmZpR2vT8wYb1nC4d"
	api := &webhookTestAPI{Client: h.gw.cfg.API}
	api.table = webhook.RouteTable{Version: "v1", Routes: []webhook.Route{{
		RouteID: "route-1", ProjectID: h.project.ID, Name: "github", Port: port,
		Path: "/api/webhooks/github", TokenHash: webhook.HashToken(token),
	}}}
	wh := webhook.New(webhook.Config{
		API: api, Dial: h.gw, Host: "hooks.test",
		Log:    slog.New(slog.NewJSONHandler(h.logs, nil)),
		Metric: obsmetrics.NewGatewayMetrics(obsmetrics.New(obs.ComponentGateway)),
	})
	if err := wh.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"zen":"Non-blocking is better than blocking.","hook_id":1}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	srv := httptest.NewServer(wh)
	defer srv.Close()
	url := srv.URL + "/" + h.project.ID + "/github/" + token
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	reply, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, reply)
	}
	if string(reply) != `{"ok":true}` {
		t.Fatalf("the app's reply did not come back: %q", reply)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("the body changed: %q", gotBody)
	}
	// The app's own signature check, run here on what it received.
	m2 := hmac.New(sha256.New, []byte(secret))
	m2.Write(gotBody)
	if gotSig != "sha256="+hex.EncodeToString(m2.Sum(nil)) {
		t.Fatalf("the signature does not match the body the app got: %q", gotSig)
	}
	if gotHost != "localhost:"+portStr {
		t.Fatalf("Host is %q, want localhost:%s", gotHost, portStr)
	}
	if gotTarget != "/api/webhooks/github" || gotWebhook != "github" {
		t.Fatalf("target %q, Repose-Webhook %q", gotTarget, gotWebhook)
	}
	if resp.Header.Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Fatalf("reply headers: %v", resp.Header)
	}

	// A second delivery reuses the SSH connection: the guest counts one
	// connection for the two.
	req2, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second delivery: %d", resp2.StatusCode)
	}
	if n := h.guest.connCount(); n != 1 {
		t.Fatalf("two deliveries opened %d SSH connections, want 1 reused", n)
	}

	// The delivery is not a gateway session: the api holds none open.
	if n := len(h.api.SessionReports()); n != 0 {
		t.Fatalf("a delivery reported %d gateway sessions, want 0", n)
	}

	// A wrong token on the same hostname never reaches the guest.
	before := h.guest.connCount()
	bad, _ := http.NewRequest(http.MethodPost, srv.URL+"/"+h.project.ID+"/github/"+strings.Repeat("A", 22), strings.NewReader("{}"))
	respBad, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = respBad.Body.Close()
	if respBad.StatusCode != http.StatusNotFound {
		t.Fatalf("a wrong token answered %d, want 404", respBad.StatusCode)
	}
	if h.guest.connCount() != before {
		t.Fatal("a wrong token reached the guest")
	}
}
