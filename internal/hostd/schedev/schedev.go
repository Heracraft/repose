// Package schedev listens to Azure Scheduled Events (IMDS) for the guest
// freeze (DECISIONS I-652). A Reboot or Redeploy notice gives at least 10
// to 15 minutes: the listener freezes the guests, approves the event so
// Azure need not wait out NotBefore, and the guests are restored when the
// host is back. A Freeze notice (a pause of a few seconds for a
// memory-preserving update or live migration) needs no freeze: the guests
// are paused with the host; once it is over their clocks are set. An event
// that disappears without the host restarting (cancelled) thaws what was
// frozen for it.
//
// Turning the listener on turns Scheduled Events on for the VM: from then
// on Azure holds every reboot, the owner's own `az vm restart` included,
// until it is approved or NotBefore passes. The listener approves User
// events at once (after freezing), and the RUNBOOK says what to do while
// IMDS cannot be reached.
package schedev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// URL is the IMDS Scheduled Events endpoint.
const URL = "http://169.254.169.254/metadata/scheduledevents?api-version=2020-07-01"

// Event is one entry of the Events array.
type Event struct {
	EventID           string   `json:"EventId"`
	EventType         string   `json:"EventType"`
	ResourceType      string   `json:"ResourceType"`
	Resources         []string `json:"Resources"`
	EventStatus       string   `json:"EventStatus"`
	NotBefore         string   `json:"NotBefore"`
	Description       string   `json:"Description"`
	EventSource       string   `json:"EventSource"`
	DurationInSeconds int      `json:"DurationInSeconds"`
}

// Document is the endpoint's answer.
type Document struct {
	DocumentIncarnation int     `json:"DocumentIncarnation"`
	Events              []Event `json:"Events"`
}

// Host is what the listener acts on.
type Host interface {
	// FreezeForEvent freezes every running guest; it returns when done.
	FreezeForEvent(ctx context.Context)
	// ThawNow restores guests frozen for an event that did not happen.
	ThawNow(ctx context.Context)
	// SyncClocks sets every guest's clock after the host was paused.
	SyncClocks(ctx context.Context)
}

// Listener polls the endpoint.
type Listener struct {
	URL      string // default URL
	Resource string // this VM's name as Events name it; empty matches any
	Interval time.Duration
	Client   *http.Client
	Host     Host
	Log      *slog.Logger

	// frozenFor is the event id the guests were frozen for, if any.
	frozenFor string
	// paused holds Freeze events in progress, by id.
	paused map[string]bool
	// logged holds Preempt and Terminate events already logged, by id.
	logged map[string]bool
}

// impact reports whether an event restarts the VM on a host it comes back
// on, which is when an image is worth writing. Preempt and Terminate
// delete the VM (and Preempt gives 30 s), so they are only logged.
func impact(t string) bool {
	switch t {
	case "Reboot", "Redeploy":
		return true
	}
	return false
}

// gone reports whether an event deletes the VM.
func gone(t string) bool { return t == "Preempt" || t == "Terminate" }

func (l *Listener) mine(e Event) bool {
	if l.Resource == "" || len(e.Resources) == 0 {
		return true
	}
	for _, r := range e.Resources {
		if strings.EqualFold(r, l.Resource) {
			return true
		}
	}
	return false
}

// Fetch reads the current document.
func (l *Listener) Fetch(ctx context.Context) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata", "true")
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // body read below
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scheduled events: status %d", resp.StatusCode)
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("scheduled events: %w", err)
	}
	return &d, nil
}

// Approve starts an event now instead of at NotBefore.
func (l *Listener) Approve(ctx context.Context, id string) error {
	body, err := json.Marshal(map[string]any{"StartRequests": []map[string]string{{"EventId": id}}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url(), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Metadata", "true")
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // status only
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scheduled events approve: status %d", resp.StatusCode)
	}
	return nil
}

func (l *Listener) url() string {
	if l.URL != "" {
		return l.URL
	}
	return URL
}

func (l *Listener) client() *http.Client {
	if l.Client != nil {
		return l.Client
	}
	// IMDS must not go through a proxy; its first answer can take up to
	// two minutes after Scheduled Events are switched on.
	return &http.Client{Timeout: 150 * time.Second, Transport: &http.Transport{Proxy: nil}}
}

// Run polls until ctx ends.
func (l *Listener) Run(ctx context.Context) {
	iv := l.Interval
	if iv == 0 {
		iv = time.Second
	}
	if l.paused == nil {
		l.paused = map[string]bool{}
	}
	if l.logged == nil {
		l.logged = map[string]bool{}
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	failing := false
	for {
		d, err := l.Fetch(ctx)
		if err != nil {
			if !failing {
				l.Log.Warn("scheduled events unreachable", "event", "scheduled_event", "err", err.Error())
			}
			failing = true
		} else {
			if failing {
				l.Log.Info("scheduled events reachable", "event", "scheduled_event")
			}
			failing = false
			l.Step(ctx, d)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Step acts on one document. It is exported for the tests.
func (l *Listener) Step(ctx context.Context, d *Document) {
	if l.paused == nil {
		l.paused = map[string]bool{}
	}
	if l.logged == nil {
		l.logged = map[string]bool{}
	}
	seen := map[string]bool{}
	for _, e := range d.Events {
		if !l.mine(e) {
			continue
		}
		seen[e.EventID] = true
		switch {
		case impact(e.EventType):
			if l.frozenFor == "" {
				l.Log.Warn("host restart scheduled; freezing guests", "event", "scheduled_event",
					"event_type", e.EventType, "event_status", e.EventStatus, "source", e.EventSource, "not_before", e.NotBefore)
				l.Host.FreezeForEvent(ctx)
				l.frozenFor = e.EventID
			}
			if e.EventStatus == "Scheduled" {
				if err := l.Approve(ctx, e.EventID); err != nil {
					l.Log.Warn("scheduled event not approved", "event", "scheduled_event", "err", err.Error())
				} else {
					l.Log.Info("scheduled event approved", "event", "scheduled_event", "event_type", e.EventType)
				}
			}
		case gone(e.EventType):
			if !l.logged[e.EventID] {
				l.Log.Warn("host VM to be deleted; guests not frozen", "event", "scheduled_event",
					"event_type", e.EventType, "event_status", e.EventStatus, "source", e.EventSource, "not_before", e.NotBefore)
				l.logged[e.EventID] = true
			}
		case e.EventType == "Freeze":
			if !l.paused[e.EventID] {
				l.Log.Info("host pause scheduled", "event", "scheduled_event", "event_type", e.EventType,
					"event_status", e.EventStatus, "duration_s", e.DurationInSeconds)
				l.paused[e.EventID] = true
			}
		}
	}
	for id := range l.paused {
		if !seen[id] {
			delete(l.paused, id)
			l.Log.Info("host pause over; setting guest clocks", "event", "scheduled_event")
			l.Host.SyncClocks(ctx)
		}
	}
	if l.frozenFor != "" && !seen[l.frozenFor] {
		// Still running after the event left the list: it was cancelled or
		// was a pause after all. The guests come back here.
		l.Log.Warn("scheduled restart did not happen; restoring guests", "event", "scheduled_event")
		l.frozenFor = ""
		l.Host.ThawNow(ctx)
	}
}
