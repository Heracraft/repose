package webhook

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // Twilio's signature is HMAC-SHA1; the algorithm is the provider's
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A token the table does not hold reaches no api call, no wake and no
// dial: the 404 is decided in memory.
func TestUnknownTokenNeverReachesAPIOrHost(t *testing.T) {
	hp := newHarness(t)
	before := hp.api.calls.Load()
	for i := 0; i < 1000; i++ {
		var b [16]byte
		_, _ = rand.Read(b[:])
		token := base64.RawURLEncoding.EncodeToString(b[:])
		r := httptest.NewRequest(http.MethodPost, "https://hooks.test/"+testProject+"/stripe/"+token, strings.NewReader("{}"))
		// A new /24 every 20 tries: the per-source cap on unknown
		// tokens is 20 a second with a burst of 40, and this test is
		// about the table, not about that cap.
		r.RemoteAddr = fmt.Sprintf("203.0.%d.%d:5000", i/20, i%20+1)
		if w := hp.do(r); w.Code != http.StatusNotFound {
			t.Fatalf("token %d: %d, want 404", i, w.Code)
		}
	}
	if n := hp.api.calls.Load() - before; n != 0 {
		t.Fatalf("%d api calls for 1000 unknown tokens, want 0", n)
	}
	if n := hp.api.wakes.Load(); n != 0 {
		t.Fatalf("%d wakes, want 0", n)
	}
	if n := hp.dial.dials.Load(); n != 0 {
		t.Fatalf("%d dials, want 0", n)
	}
	if len(hp.flushed()) != 0 {
		t.Fatal("an unknown token was recorded against a route")
	}
	// The probe: the same request with the real token does reach the
	// guest, so the zero above is the token check and not a dead path.
	if w := hp.do(hp.post([]byte("{}"), nil)); w.Code != 200 {
		t.Fatalf("the live token answered %d", w.Code)
	}
	if hp.dial.dials.Load() != 1 {
		t.Fatalf("the live token made %d dials, want 1", hp.dial.dials.Load())
	}
}

// A path that is not /<project>/<route>/<token> is a 404 before any
// lookup, and the project and route in the path must match the row.
func TestMismatchedPathIs404(t *testing.T) {
	hp := newHarness(t)
	for _, p := range []string{
		"/",
		"/" + testProject,
		"/" + testProject + "/stripe",
		"/" + testProject + "/github/" + hp.token,
		"/0193a5e2-0000-0000-0000-000000000000/stripe/" + hp.token,
	} {
		r := httptest.NewRequest(http.MethodPost, "https://hooks.test"+p, strings.NewReader("{}"))
		r.RemoteAddr = "54.187.174.169:51234"
		if w := hp.do(r); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", p, w.Code)
		}
	}
	if hp.dial.dials.Load() != 0 {
		t.Fatalf("%d dials for malformed paths, want 0", hp.dial.dials.Load())
	}
}

// A token that existed and was rotated, removed or destroyed with its
// project answers 410 while it is a tombstone.
func TestTombstoneAnswers410(t *testing.T) {
	hp := newHarness(t)
	r := httptest.NewRequest(http.MethodPost, "https://hooks.test/"+testProject+"/stripe/"+hp.gone, strings.NewReader("{}"))
	r.RemoteAddr = "54.187.174.169:51234"
	w := hp.do(r)
	if w.Code != http.StatusGone {
		t.Fatalf("a rotated token answered %d, want 410", w.Code)
	}
	if hp.dial.dials.Load() != 0 {
		t.Fatalf("a tombstone dialled the guest")
	}
	// The probe: once the api drops the tombstone, the same token is a
	// 404, which is the only difference the window makes.
	hp.api.table = RouteTable{Version: "v2", Routes: []Route{hp.route}}
	if err := hp.h.cfg.Table.Refresh(t.Context(), hp.api); err != nil {
		t.Fatal(err)
	}
	r2 := httptest.NewRequest(http.MethodPost, "https://hooks.test/"+testProject+"/stripe/"+hp.gone, strings.NewReader("{}"))
	r2.RemoteAddr = "54.187.174.169:51234"
	if w := hp.do(r2); w.Code != http.StatusNotFound {
		t.Fatalf("after the window the token answered %d, want 404", w.Code)
	}
}

// Body bytes reach the app as they were sent: CR, LF, NUL and a gzip
// body with Content-Encoding all arrive with the same sha256.
func TestBodyBytesUnchanged(t *testing.T) {
	hp := newHarness(t)
	raw := make([]byte, 4096)
	_, _ = rand.Read(raw)
	raw = append(raw, "\r\n\x00\r\n--boundary--\r\n"...)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	for _, tc := range []struct {
		name     string
		body     []byte
		encoding string
	}{
		{"plain", raw, ""},
		{"gzip", gz.Bytes(), "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{"Content-Type": "application/json"}
			if tc.encoding != "" {
				headers["Content-Encoding"] = tc.encoding
			}
			before := len(hp.app.requests())
			if w := hp.do(hp.post(tc.body, headers)); w.Code != 200 {
				t.Fatalf("%d", w.Code)
			}
			got := hp.app.requests()[before]
			if sha256.Sum256(got.body) != sha256.Sum256(tc.body) {
				t.Fatalf("the body changed: %d bytes in, %d out", len(tc.body), len(got.body))
			}
			if got.header.Get("Content-Encoding") != tc.encoding {
				t.Fatalf("Content-Encoding is %q, want %q", got.header.Get("Content-Encoding"), tc.encoding)
			}
		})
	}
}

// A chunked body, which has no Content-Length, still arrives whole and
// with a length the app can read.
func TestChunkedBodyArrivesWhole(t *testing.T) {
	hp := newHarness(t)
	body := bytes.Repeat([]byte("ab"), 5000)
	r := httptest.NewRequest(http.MethodPost, "https://hooks.test"+hp.url(""), bytes.NewReader(body))
	r.ContentLength = -1
	r.RemoteAddr = "54.187.174.169:51234"
	if w := hp.do(r); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	got := hp.app.requests()[0]
	if !bytes.Equal(got.body, body) {
		t.Fatalf("chunked body: %d bytes, want %d", len(got.body), len(body))
	}
	if got.header.Get("Transfer-Encoding") != "" {
		t.Fatal("Transfer-Encoding, which is hop-by-hop, was passed on")
	}
}

// Four providers' signatures, computed over what the edge received, are
// checked against what the app received. Each algorithm is the
// provider's own, written out here rather than taken from its SDK.
func TestProviderSignaturesPass(t *testing.T) {
	hp := newHarness(t)
	body := []byte(`{"id":"evt_1","type":"payment_intent.succeeded"}`)
	secret := []byte("whsec_test_secret")
	ts := strconv.FormatInt(hp.now.Unix(), 10)

	hmacHex := func(key, msg []byte) string {
		m := hmac.New(sha256.New, key)
		m.Write(msg)
		return hex.EncodeToString(m.Sum(nil))
	}
	hmacB64 := func(key, msg []byte) string {
		m := hmac.New(sha256.New, key)
		m.Write(msg)
		return base64.StdEncoding.EncodeToString(m.Sum(nil))
	}

	// Stripe signs `t.rawbody`; GitHub the raw body; Svix
	// `id.timestamp.body`; Twilio the full URL plus the POST params,
	// which the app takes from Repose-Webhook-URL.
	headers := map[string]string{
		"Content-Type":        "application/json",
		"Stripe-Signature":    "t=" + ts + ",v1=" + hmacHex(secret, []byte(ts+"."+string(body))),
		"X-Hub-Signature-256": "sha256=" + hmacHex(secret, body),
		"Svix-Id":             "msg_1",
		"Svix-Timestamp":      ts,
		"Svix-Signature":      "v1," + hmacB64(secret, []byte("msg_1."+ts+"."+string(body))),
	}

	var checked []string
	hp.app.answer(func(w http.ResponseWriter, r *http.Request, got []byte) {
		if r.Header.Get("Stripe-Signature") == "t="+ts+",v1="+hmacHex(secret, []byte(ts+"."+string(got))) {
			checked = append(checked, "stripe")
		}
		if r.Header.Get("X-Hub-Signature-256") == "sha256="+hmacHex(secret, got) {
			checked = append(checked, "github")
		}
		if r.Header.Get("Svix-Signature") == "v1,"+hmacB64(secret, []byte(r.Header.Get("Svix-Id")+"."+r.Header.Get("Svix-Timestamp")+"."+string(got))) {
			checked = append(checked, "svix")
		}
		// Twilio: HMAC-SHA1 of the full URL with the POST parameters
		// appended in sorted order. The URL is the public one, which
		// only Repose-Webhook-URL carries.
		url := r.Header.Get("Repose-Webhook-URL")
		if url != "" {
			params := []string{"From+15551234567", "To+15559876543"}
			sort.Strings(params)
			mac := hmac.New(sha1.New, secret)
			mac.Write([]byte(url + strings.Join(params, "")))
			want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			if r.Header.Get("X-Twilio-Signature") == want {
				checked = append(checked, "twilio")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"received":true}`))
	})

	// The sender signs Twilio's URL as the provider would: the public
	// one, with the token in it.
	publicURL := "https://hooks.test" + hp.url("")
	params := []string{"From+15551234567", "To+15559876543"}
	sort.Strings(params)
	mac := hmac.New(sha1.New, secret)
	mac.Write([]byte(publicURL + strings.Join(params, "")))
	headers["X-Twilio-Signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if w := hp.do(hp.post(body, headers)); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	sort.Strings(checked)
	want := []string{"github", "stripe", "svix", "twilio"}
	if strings.Join(checked, ",") != strings.Join(want, ",") {
		t.Fatalf("signatures that passed: %v, want %v", checked, want)
	}
}

// Header values arrive unchanged and duplicates of one name keep their
// order; hop-by-hop fields are dropped; Host is localhost:<port>; a
// sender's own Repose-* header is replaced by the edge's.
func TestHeadersForwardedAndReplaced(t *testing.T) {
	hp := newHarness(t)
	r := hp.post([]byte("{}"), map[string]string{"Content-Type": "application/json"})
	r.Header["X-Custom"] = []string{"first", "second"}
	r.Header.Set("Repose-Webhook", "forged")
	r.Header.Set("Repose-Delivery", "forged")
	r.Header.Set("Te", "trailers")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Proxy-Authorization", "Basic xyz")
	if w := hp.do(r); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	got := hp.app.requests()[0]
	if v := got.header.Values("X-Custom"); len(v) != 2 || v[0] != "first" || v[1] != "second" {
		t.Fatalf("duplicates: %v", v)
	}
	if got.host != "localhost:3000" {
		t.Fatalf("Host is %q, want localhost:3000", got.host)
	}
	for _, h := range []string{"Te", "Upgrade", "Proxy-Authorization"} {
		if got.header.Get(h) != "" {
			t.Fatalf("hop-by-hop %s was forwarded", h)
		}
	}
	if got.header.Get("Repose-Webhook") != "stripe" {
		t.Fatalf("Repose-Webhook is %q, want the route's name", got.header.Get("Repose-Webhook"))
	}
	if got.header.Get("Repose-Delivery") == "forged" || got.header.Get("Repose-Delivery") == "" {
		t.Fatalf("Repose-Delivery is %q", got.header.Get("Repose-Delivery"))
	}
	if got.header.Get("X-Forwarded-Host") != "hooks.test" || got.header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("forwarded headers: %v", got.header)
	}
	if got.header.Get("X-Forwarded-For") != "54.187.174.169" {
		t.Fatalf("X-Forwarded-For is %q", got.header.Get("X-Forwarded-For"))
	}
	if got.target != "/api/webhooks/stripe" {
		t.Fatalf("target is %q", got.target)
	}
}

// Anything after the token is appended to the target path and the query
// passes as it came.
func TestRestAndQueryAppended(t *testing.T) {
	hp := newHarness(t)
	r := httptest.NewRequest(http.MethodGet, "https://hooks.test"+hp.url("/sub/path")+"?hub.mode=subscribe&x=1%202", nil)
	r.RemoteAddr = "54.187.174.169:51234"
	if w := hp.do(r); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	got := hp.app.requests()[0]
	if got.method != "GET" {
		t.Fatalf("method is %q", got.method)
	}
	if got.target != "/api/webhooks/stripe/sub/path?hub.mode=subscribe&x=1%202" {
		t.Fatalf("target is %q", got.target)
	}
}

// The reply rules: HTML is dropped, Set-Cookie is gone, a 302 becomes a
// 502 with no Location, a long JSON reply is cut at 64 KiB, and the
// headers a browser needs are always there.
func TestReplyRules(t *testing.T) {
	t.Run("html body dropped", func(t *testing.T) {
		hp := newHarness(t)
		hp.app.answer(func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Set-Cookie", "session=abc; Domain=.repose.herakraft.co")
			_, _ = w.Write([]byte("<h1>sign in</h1>"))
		})
		w := hp.do(hp.post([]byte("{}"), nil))
		if w.Code != 200 {
			t.Fatalf("%d", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Fatalf("an HTML body came back: %q", w.Body.String())
		}
		if w.Header().Get("Repose-Body-Dropped") != "type" {
			t.Fatal("no Repose-Body-Dropped on a dropped body")
		}
		if w.Header().Get("Set-Cookie") != "" {
			t.Fatal("Set-Cookie came back")
		}
		if w.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("reply headers: %v", w.Header())
		}
	})
	t.Run("redirect becomes 502", func(t *testing.T) {
		hp := newHarness(t)
		hp.app.answer(func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			http.Redirect(w, httptest.NewRequest("GET", "/", nil), "https://evil.example/", http.StatusFound)
		})
		w := hp.do(hp.post([]byte("{}"), nil))
		if w.Code != http.StatusBadGateway {
			t.Fatalf("a 302 came back as %d, want 502", w.Code)
		}
		if w.Header().Get("Location") != "" {
			t.Fatalf("Location came back: %q", w.Header().Get("Location"))
		}
	})
	t.Run("long json cut", func(t *testing.T) {
		hp := newHarness(t)
		big := bytes.Repeat([]byte("x"), 100<<10)
		hp.app.answer(func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(big)
		})
		w := hp.do(hp.post([]byte("{}"), nil))
		if w.Body.Len() != MaxReplyBytes {
			t.Fatalf("the reply was %d bytes, want it cut at %d", w.Body.Len(), MaxReplyBytes)
		}
	})
	t.Run("challenge passes", func(t *testing.T) {
		hp := newHarness(t)
		hp.app.answer(func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"))
		})
		w := hp.do(hp.post([]byte(`{"type":"url_verification"}`), nil))
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "3eZbrw1aBm2") {
			t.Fatalf("Slack's challenge did not come back: %d %q", w.Code, w.Body.String())
		}
	})
}

// A body over the cap is 413 before anything is woken or dialled.
func TestOversize413BeforeWake(t *testing.T) {
	hp := newHarness(t)
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running", Resting: true})
	r := hp.post(nil, nil)
	r.ContentLength = 2 << 20
	r.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 16)))
	if w := hp.do(r); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d, want 413", w.Code)
	}
	if n := hp.api.wakes.Load(); n != 0 {
		t.Fatalf("%d wakes for an oversize body, want 0", n)
	}
	if n := hp.dial.dials.Load(); n != 0 {
		t.Fatalf("%d dials for an oversize body, want 0", n)
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "too_large" {
		t.Fatalf("recorded: %+v", d)
	}
}

// A frozen machine is woken, then gets the delivery, and the delivery is
// not a gateway session: nothing is reported to /internal/sessions.
func TestFrozenWakesThenDelivers(t *testing.T) {
	hp := newHarness(t)
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running", Resting: true})
	w := hp.do(hp.post([]byte("{}"), nil))
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if n := hp.api.wakes.Load(); n != 1 {
		t.Fatalf("%d wakes, want 1", n)
	}
	d := hp.flushed()
	if len(d) != 1 || d[0].Outcome != "woke" || d[0].Status != 200 {
		t.Fatalf("recorded: %+v", d)
	}
	// The probe: a running machine makes no wake, so the one above came
	// from the resting flag and not from every delivery.
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running"})
	hp.now = hp.now.Add(StateTTL + time.Second)
	if w := hp.do(hp.post([]byte("{}"), nil)); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if n := hp.api.wakes.Load(); n != 1 {
		t.Fatalf("a running machine was woken: %d wakes", n)
	}
}

// A wake that fails is a 503 with Retry-After, and the body never names
// what the sender sent.
func TestWakeFailureIs503(t *testing.T) {
	hp := newHarness(t)
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running", Resting: true})
	hp.api.wakeErr = fmt.Errorf("did not wake")
	w := hp.do(hp.post([]byte("{}"), nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("%d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "wake_failed" {
		t.Fatalf("recorded: %+v", d)
	}
}

// A stopped machine answers 503 with Retry-After; a route with --start
// also asks the api to start it, and the answer is still 503.
func TestStoppedAnswers503(t *testing.T) {
	hp := newHarness(t)
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, State: "stopped"})
	w := hp.do(hp.post([]byte("{}"), nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "3600" {
		t.Fatalf("%d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if n := hp.api.starts.Load(); n != 0 {
		t.Fatalf("a route without --start asked for %d starts", n)
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "stopped" {
		t.Fatalf("recorded: %+v", d)
	}

	// With --start the api is asked, and the sender still gets a 503:
	// a start takes longer than every provider's timeout.
	hp2 := newHarness(t)
	hp2.route.AllowStart = true
	hp2.api.table = RouteTable{Version: "v2", Routes: []Route{hp2.route}}
	hp2.api.startsOK = true
	if err := hp2.h.cfg.Table.Refresh(t.Context(), hp2.api); err != nil {
		t.Fatal(err)
	}
	hp2.api.setState(testProject, ProjectState{ProjectID: testProject, State: "stopped"})
	w2 := hp2.do(hp2.post([]byte("{}"), nil))
	if w2.Code != http.StatusServiceUnavailable || w2.Header().Get("Retry-After") != "60" {
		t.Fatalf("--start: %d, Retry-After %q", w2.Code, w2.Header().Get("Retry-After"))
	}
	if n := hp2.api.starts.Load(); n != 1 {
		t.Fatalf("--start asked for %d starts, want 1", n)
	}
	if d := hp2.flushed(); len(d) != 1 || d[0].Outcome != "started" {
		t.Fatalf("recorded: %+v", d)
	}
}

// A machine the api refuses to start records start_refused and still
// answers 503.
func TestStartRefusedIsRecorded(t *testing.T) {
	hp := newHarness(t)
	hp.route.AllowStart = true
	hp.api.table = RouteTable{Version: "v2", Routes: []Route{hp.route}}
	hp.api.startsOK = false
	if err := hp.h.cfg.Table.Refresh(t.Context(), hp.api); err != nil {
		t.Fatal(err)
	}
	hp.api.setState(testProject, ProjectState{ProjectID: testProject, State: "stopped"})
	if w := hp.do(hp.post([]byte("{}"), nil)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", w.Code)
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "start_refused" {
		t.Fatalf("recorded: %+v", d)
	}
}

// A machine between states, in error, or on a host the control plane
// cannot reach, each answer 503 with their own Retry-After.
func TestTransitionalStatesAnswer503(t *testing.T) {
	for _, tc := range []struct {
		state      string
		unreach    bool
		retryAfter string
	}{
		{"creating", false, "30"},
		{"starting", false, "30"},
		{"stopping", false, "30"},
		{"restoring", false, "30"},
		{"error", false, "300"},
		{"running", true, "300"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			hp := newHarness(t)
			hp.api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: tc.state, HostUnreachable: tc.unreach})
			w := hp.do(hp.post([]byte("{}"), nil))
			if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != tc.retryAfter {
				t.Fatalf("%d, Retry-After %q, want 503 and %s", w.Code, w.Header().Get("Retry-After"), tc.retryAfter)
			}
			if hp.dial.dials.Load() != 0 {
				t.Fatal("the guest was dialled")
			}
		})
	}
}

// Nothing listening on the port is 502 with the port named, and the
// outcome is no_listener rather than a timeout.
func TestNoListenerIs502(t *testing.T) {
	hp := newHarness(t)
	hp.dial.fail = true
	w := hp.do(hp.post([]byte("{}"), nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("%d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "port 3000") {
		t.Fatalf("the message does not name the port: %q", w.Body.String())
	}
	if d := hp.flushed(); len(d) != 1 || d[0].Outcome != "no_listener" {
		t.Fatalf("recorded: %+v", d)
	}
}

// One route's rate is its own: past it the sender gets 429, and the cap
// is the route's and the project's, never the source address's, so two
// projects behind one provider's address do not throttle each other.
func TestRouteRateLimitAndOneProjectCannotStarveAnother(t *testing.T) {
	hp := newHarness(t)
	other := "0193a5e2-7c41-7d0e-9b8a-2f6c1d4e5a99"
	otherToken := "ZZZZZZZZZZZZZZZZZZZZZZ"
	otherRoute := Route{RouteID: "route-2", ProjectID: other, Name: "stripe", Port: 3000, Path: "/", TokenHash: HashToken(otherToken)}
	hp.api.table = RouteTable{Version: "v2", Routes: []Route{hp.route, otherRoute}}
	if err := hp.h.cfg.Table.Refresh(t.Context(), hp.api); err != nil {
		t.Fatal(err)
	}
	hp.api.setState(other, ProjectState{ProjectID: other, GuestIP: "10.42.0.3", State: "running"})

	limited := 0
	for i := 0; i < RouteBurst+10; i++ {
		if w := hp.do(hp.post([]byte("{}"), nil)); w.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("a route past its burst was never rate limited")
	}
	// The other project, from the same provider address, still gets
	// through: the cap is per route and per project, not per source.
	r := httptest.NewRequest(http.MethodPost, "https://hooks.test/"+other+"/stripe/"+otherToken, strings.NewReader("{}"))
	r.RemoteAddr = "54.187.174.169:51234"
	if w := hp.do(r); w.Code != 200 {
		t.Fatalf("the second project answered %d while the first was throttled", w.Code)
	}
}

// Every delivery's log line and record carry ids, a status, an outcome,
// a duration and byte counts, and never the token, the query, a header
// value or a body byte.
func TestNoTokenHeaderOrBodyInLogs(t *testing.T) {
	hp := newHarness(t)
	const markToken = "MARKERTOKENMARKERTOKEN"
	const markHeader = "MARKERHEADERVALUE"
	const markQuery = "MARKERQUERYVALUE"
	const markBody = "MARKERBODYVALUE"
	hp.route.TokenHash = HashToken(markToken)
	hp.api.table = RouteTable{Version: "v2", Routes: []Route{hp.route}}
	if err := hp.h.cfg.Table.Refresh(t.Context(), hp.api); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost,
		"https://hooks.test/"+testProject+"/stripe/"+markToken+"?q="+markQuery,
		strings.NewReader(`{"secret":"`+markBody+`"}`))
	r.RemoteAddr = "54.187.174.169:51234"
	r.Header.Set("Stripe-Signature", markHeader)
	if w := hp.do(r); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	// An unknown token's 404 is logged too; plant the marker there as
	// well, since that path has its own log line.
	r2 := httptest.NewRequest(http.MethodPost, "https://hooks.test/"+testProject+"/stripe/"+markToken+"X", strings.NewReader(markBody))
	r2.RemoteAddr = "54.187.174.169:51234"
	hp.do(r2)

	logs := hp.logs.String()
	batch := fmt.Sprintf("%+v", hp.flushed())
	for _, m := range []string{markToken, markHeader, markQuery, markBody} {
		if strings.Contains(logs, m) {
			t.Fatalf("the logs carry %s:\n%s", m, logs)
		}
		if strings.Contains(batch, m) {
			t.Fatalf("the delivery batch carries %s: %s", m, batch)
		}
	}
	// The probe: the fields that may be logged are there, so the zeroes
	// above are not an empty log.
	for _, want := range []string{`"event":"webhook_delivery"`, `"route_id":"route-1"`, `"status":200`, `"outcome":"delivered"`, `"source_prefix":"54.187.174.0/24"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("the logs do not carry %s:\n%s", want, logs)
		}
	}
}

// A delivery is not a gateway session (amends I-818, I-822): the
// webhook path has no way to report one, so a machine that only
// receives webhooks is never held awake past the session window.
//
// Asserted on the interface's method set rather than on a counter
// nothing increments: a counter that cannot go up is green whether the
// property holds or not.
func TestDeliveryIsNotASession(t *testing.T) {
	iface := reflect.TypeOf((*API)(nil)).Elem()
	var names []string
	for i := 0; i < iface.NumMethod(); i++ {
		names = append(names, iface.Method(i).Name)
		if strings.Contains(strings.ToLower(iface.Method(i).Name), "session") {
			t.Fatalf("webhook.API has %s: a delivery could report a session", iface.Method(i).Name)
		}
	}
	// The probe: the method set is the one this test walked, so the
	// absence above is not an empty interface.
	sort.Strings(names)
	want := "ProjectState,ReportWebhookDeliveries,WebhookRoutes,WebhookStart,WebhookWake"
	if strings.Join(names, ",") != want {
		t.Fatalf("webhook.API is %v, want %s", names, want)
	}
}

// The table's refresh takes the api's answer, and an unchanged version
// leaves it as it was.
func TestTableRefresh(t *testing.T) {
	api := newFakeAPI()
	api.table = RouteTable{Version: "v1", Routes: []Route{{RouteID: "r", ProjectID: "p", Name: "n", TokenHash: HashToken("t")}}}
	tbl := NewTable(nil)
	if !tbl.Stale() {
		t.Fatal("a table that was never filled reads fresh")
	}
	if err := tbl.Refresh(t.Context(), api); err != nil {
		t.Fatal(err)
	}
	if tbl.Len() != 1 || tbl.Stale() {
		t.Fatalf("after a refresh: %d routes, stale %v", tbl.Len(), tbl.Stale())
	}
	if _, got := tbl.Lookup(HashToken("t")); got != Found {
		t.Fatalf("lookup: %v", got)
	}
	// An unchanged answer keeps the rows rather than emptying them.
	api.table = RouteTable{Version: "v1"}
	if err := tbl.Refresh(t.Context(), api); err != nil {
		t.Fatal(err)
	}
	if tbl.Len() != 1 {
		t.Fatalf("an unchanged answer emptied the table: %d routes", tbl.Len())
	}
}

// A HEAD reaches the app and its reply comes back with no body, however
// long a Content-Length the app sent: a provider that verifies an
// endpoint with a HEAD gets an answer rather than a hang.
func TestHeadGetsAnEmptyBody(t *testing.T) {
	hp := newHarness(t)
	hp.app.answer(func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "11")
		w.WriteHeader(200)
	})
	r := httptest.NewRequest(http.MethodHead, "https://hooks.test"+hp.url(""), nil)
	r.RemoteAddr = "54.187.174.169:51234"
	w := hp.do(r)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("a HEAD came back with %d body bytes", w.Body.Len())
	}
	if got := hp.app.requests(); len(got) != 1 || got[0].method != "HEAD" {
		t.Fatalf("the app saw %v", got)
	}
}
