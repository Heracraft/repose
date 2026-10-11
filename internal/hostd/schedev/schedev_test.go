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
	freezes, thaws, syncs int
}

func (h *host) FreezeForEvent(context.Context) { h.mu.Lock(); h.freezes++; h.mu.Unlock() }
func (h *host) ThawNow(context.Context)        { h.mu.Lock(); h.thaws++; h.mu.Unlock() }
func (h *host) SyncClocks(context.Context)     { h.mu.Lock(); h.syncs++; h.mu.Unlock() }

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

// A Reboot notice freezes once, then approves; Azure need not wait out
// NotBefore. Another VM's event is ignored.
func TestRebootNoticeFreezesThenApproves(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{DocumentIncarnation: 2, Events: []Event{
		{EventID: "other", EventType: "Reboot", Resources: []string{"host-02"}, EventStatus: "Scheduled"},
		{EventID: "e1", EventType: "Reboot", Resources: []string{"host-01"}, EventStatus: "Scheduled", EventSource: "Platform"},
	}}
	l.step(t)
	l.step(t)
	if h.freezes != 1 {
		t.Fatalf("%d freezes, want 1", h.freezes)
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
// up) restores the frozen guests.
func TestCancelledRebootThaws(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{Events: []Event{{EventID: "e1", EventType: "Redeploy", EventStatus: "Scheduled"}}}
	l.step(t)
	s.mu.Lock()
	s.doc = Document{DocumentIncarnation: 3}
	s.mu.Unlock()
	l.step(t)
	if h.freezes != 1 || h.thaws != 1 {
		t.Fatalf("freezes %d thaws %d", h.freezes, h.thaws)
	}
}

// A Freeze notice (a pause of seconds) freezes nothing; when it is over
// the guests' clocks are set.
func TestPauseEventSyncsClocksAfter(t *testing.T) {
	s, h := &imds{}, &host{}
	l := newListener(t, s, h)
	s.doc = Document{Events: []Event{{EventID: "p1", EventType: "Freeze", EventStatus: "Scheduled", DurationInSeconds: 5}}}
	l.step(t)
	if h.freezes != 0 || len(s.approved) != 0 {
		t.Fatalf("a pause froze guests (%d) or was approved (%v)", h.freezes, s.approved)
	}
	s.mu.Lock()
	s.doc = Document{}
	s.mu.Unlock()
	l.step(t)
	if h.syncs != 1 {
		t.Fatalf("%d clock syncs after the pause", h.syncs)
	}
}

// Preempt and Terminate delete the VM: nothing is frozen or approved.
func TestDeletionNoticeFreezesNothing(t *testing.T) {
	for _, typ := range []string{"Preempt", "Terminate"} {
		s, h := &imds{}, &host{}
		l := newListener(t, s, h)
		s.doc = Document{Events: []Event{{EventID: "d1", EventType: typ, EventStatus: "Scheduled"}}}
		l.step(t)
		l.step(t)
		if h.freezes != 0 || len(s.approved) != 0 {
			t.Fatalf("%s: %d freezes, approved %v", typ, h.freezes, s.approved)
		}
	}
}
