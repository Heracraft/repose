package schedev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/heracraft/repose/internal/obs"
)

type host struct {
	mu                    sync.Mutex
	parks, unparks, syncs int
}

func (h *host) ParkForEvent(context.Context) { h.mu.Lock(); h.parks++; h.mu.Unlock() }
func (h *host) UnparkNow(context.Context)    { h.mu.Lock(); h.unparks++; h.mu.Unlock() }
func (h *host) SyncClocks(context.Context)   { h.mu.Lock(); h.syncs++; h.mu.Unlock() }

type imds struct {
	mu       sync.Mutex
	doc      Document
	approved []string
}

func (s *imds) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Metadata") != "true" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodPost {
		b, _ := io.ReadAll(r.Body)
		var req struct{ StartRequests []struct{ EventId string } }
		_ = json.Unmarshal(b, &req)
		for _, x := range req.StartRequests {
			s.approved = append(s.approved, x.EventId)
		}
		return
	}
	_ = json.NewEncoder(w).Encode(s.doc)
}

func newListener(t *testing.T, s *imds, h *host) *Listener {
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return &Listener{URL: srv.URL, Resource: "host-01", Host: h, Log: obs.Nop(obs.ComponentHostd)}
}

func (l *Listener) step(t *testing.T) {
	t.Helper()
	d, err := l.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l.Step(context.Background(), d)
}

// A Reboot notice parks once, then approves; Azure need not wait out
// NotBefore. Another VM's event is ignored.
func TestRebootNoticeParksThenApproves(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{DocumentIncarnation: 2, Events: []Event{
		{EventID: "other", EventType: "Reboot", Resources: []string{"host-02"}, EventStatus: "Scheduled"},
		{EventID: "e1", EventType: "Reboot", Resources: []string{"host-01"}, EventStatus: "Scheduled", EventSource: "Platform"},
	}}
	l.step(t)
	l.step(t)
	if h.parks != 1 {
		t.Fatalf("%d parks, want 1", h.parks)
	}
	if len(s.approved) == 0 || s.approved[0] != "e1" {
		t.Fatalf("approved %v", s.approved)
	}
	for _, a := range s.approved {
		if a == "other" {
			t.Fatal("approved another VM's event")
		}
	}
}

// A cancelled restart (the event leaves the list and the host is still
// up) restores the parked guests.
func TestCancelledRebootUnparks(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{Events: []Event{{EventID: "e1", EventType: "Redeploy", EventStatus: "Scheduled"}}}
	l.step(t)
	s.mu.Lock()
	s.doc = Document{DocumentIncarnation: 3}
	s.mu.Unlock()
	l.step(t)
	if h.parks != 1 || h.unparks != 1 {
		t.Fatalf("parks %d unparks %d", h.parks, h.unparks)
	}
}

// A Freeze notice (a pause of seconds) parks nothing; when it is over
// the guests' clocks are set.
func TestPauseEventSyncsClocksAfter(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{Events: []Event{{EventID: "p1", EventType: "Freeze", EventStatus: "Scheduled", DurationInSeconds: 5}}}
	l.step(t)
	if h.parks != 0 || len(s.approved) != 0 {
		t.Fatalf("a pause parked guests (%d) or was approved (%v)", h.parks, s.approved)
	}
	s.mu.Lock()
	s.doc = Document{}
	s.mu.Unlock()
	l.step(t)
	if h.syncs != 1 {
		t.Fatalf("%d clock syncs after the pause", h.syncs)
	}
}

// Preempt and Terminate delete the VM: nothing is parked or approved.
func TestDeletionNoticeParksNothing(t *testing.T) {
	for _, typ := range []string{"Preempt", "Terminate"} {
		s, h := &imds{}, &host{}
		l := newListener(t, s, h)
		s.doc = Document{Events: []Event{{EventID: "d1", EventType: typ, EventStatus: "Scheduled"}}}
		l.step(t)
		l.step(t)
		if h.parks != 0 || len(s.approved) != 0 {
			t.Fatalf("%s: %d parks, approved %v", typ, h.parks, s.approved)
		}
	}
}
