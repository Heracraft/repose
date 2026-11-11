package webhook

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI counts what the edge asked of the control plane, which is how
// "a wrong token never reaches the api or a host" is asserted rather
// than guessed at.
type fakeAPI struct {
	mu         sync.Mutex
	table      RouteTable
	state      map[string]*ProjectState
	calls      atomic.Int64 // every api call
	wakes      atomic.Int64
	starts     atomic.Int64
	startsOK   bool
	deliveries []Delivery
	wakeErr    error
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{state: map[string]*ProjectState{}}
}

func (f *fakeAPI) WebhookRoutes(_ context.Context, since string) (*RouteTable, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.table
	if since != "" && since == t.Version {
		return &RouteTable{Version: t.Version, Unchanged: true}, nil
	}
	return &t, nil
}

func (f *fakeAPI) ProjectState(_ context.Context, projectID string) (*ProjectState, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.state[projectID]
	if !ok {
		return nil, fmt.Errorf("no such project")
	}
	c := *s
	return &c, nil
}

func (f *fakeAPI) WebhookWake(_ context.Context, projectID string) error {
	f.calls.Add(1)
	f.wakes.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return f.wakeErr
	}
	if s, ok := f.state[projectID]; ok {
		s.Resting = false
	}
	return nil
}

func (f *fakeAPI) WebhookStart(_ context.Context, projectID, _ string) (bool, string, error) {
	f.calls.Add(1)
	f.starts.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.startsOK {
		return false, "too_soon", nil
	}
	if s, ok := f.state[projectID]; ok {
		s.State = "starting"
	}
	return true, "started", nil
}

func (f *fakeAPI) ReportWebhookDeliveries(_ context.Context, batch []Delivery) error {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries = append(f.deliveries, batch...)
	return nil
}

func (f *fakeAPI) reported() []Delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Delivery, len(f.deliveries))
	copy(out, f.deliveries)
	return out
}

func (f *fakeAPI) setState(projectID string, s ProjectState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := s
	f.state[projectID] = &c
}

// fakeDialer stands in for the gateway's SSH connection to a guest: it
// dials a loopback listener the test runs, and counts the dials, so
// "no dial" is an assertion and not an assumption.
type fakeDialer struct {
	addr  string
	dials atomic.Int64
	fail  bool
}

func (d *fakeDialer) DialGuestPort(ctx context.Context, _, _, _ string, _ int) (net.Conn, error) {
	d.dials.Add(1)
	if d.fail {
		return nil, fmt.Errorf("connect failed")
	}
	var dl net.Dialer
	return dl.DialContext(ctx, "tcp", d.addr)
}

// guestApp is an HTTP server on loopback standing for the app inside the
// machine, with what each request carried kept for the assertions.
type guestApp struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []seenRequest
	// handle, when set, writes the reply; the default is 200 and `{}`.
	handle func(w http.ResponseWriter, r *http.Request, body []byte)
}

type seenRequest struct {
	method string
	target string
	host   string
	header http.Header
	body   []byte
}

func newGuestApp(t *testing.T) *guestApp {
	t.Helper()
	g := &guestApp{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		g.mu.Lock()
		g.got = append(g.got, seenRequest{method: r.Method, target: r.URL.RequestURI(), host: r.Host, header: r.Header.Clone(), body: buf.Bytes()})
		h := g.handle
		g.mu.Unlock()
		if h != nil {
			h(w, r, buf.Bytes())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *guestApp) addr() string {
	return g.srv.Listener.Addr().String()
}

func (g *guestApp) requests() []seenRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]seenRequest, len(g.got))
	copy(out, g.got)
	return out
}

func (g *guestApp) answer(fn func(w http.ResponseWriter, r *http.Request, body []byte)) {
	g.mu.Lock()
	g.handle = fn
	g.mu.Unlock()
}

// harness is a primed handler with one live route and one tombstone.
type harness struct {
	h     *Handler
	api   *fakeAPI
	dial  *fakeDialer
	app   *guestApp
	logs  *bytes.Buffer
	token string
	gone  string
	route Route
	now   time.Time
}

const testProject = "0193a5e2-7c41-7d0e-9b8a-2f6c1d4e5a90"

func newHarness(t *testing.T) *harness {
	t.Helper()
	app := newGuestApp(t)
	api := newFakeAPI()
	hp := &harness{api: api, app: app, token: "Xq3k9LmZpR2vT8wYb1nC4d", gone: "b8Nf2QeVr6GhK0zTw4mYsA", logs: &bytes.Buffer{}}
	hp.now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	hp.route = Route{RouteID: "route-1", ProjectID: testProject, Name: "stripe", Port: 3000,
		Path: "/api/webhooks/stripe", TokenHash: HashToken(hp.token)}
	api.table = RouteTable{Version: "v1", Routes: []Route{hp.route}, Tombstones: []string{HashToken(hp.gone)}}
	api.setState(testProject, ProjectState{ProjectID: testProject, GuestIP: "10.42.0.2", State: "running"})
	hp.dial = &fakeDialer{addr: app.addr()}
	hp.h = New(Config{API: api, Dial: hp.dial, Log: slog.New(slog.NewJSONHandler(hp.logs, nil)),
		Host: "hooks.test", Clock: func() time.Time { return hp.now }})
	if err := hp.h.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	return hp
}

// do sends one request at the handler and returns the recorded answer.
func (hp *harness) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	hp.h.ServeHTTP(w, r)
	return w
}

// url is the public path of the harness's route.
func (hp *harness) url(rest string) string {
	return "/" + testProject + "/stripe/" + hp.token + rest
}

// post is a POST at the route with a body and headers.
func (hp *harness) post(body []byte, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://hooks.test"+hp.url(""), bytes.NewReader(body))
	r.RemoteAddr = "54.187.174.169:51234"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// flushed sends the batch and returns what the api was told.
func (hp *harness) flushed() []Delivery {
	hp.h.flush(context.Background())
	return hp.api.reported()
}
