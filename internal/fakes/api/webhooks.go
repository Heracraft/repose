package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The webhook routes of docs/interfaces/api.md (DECISIONS I-1110): a
// public URL into a project's machine. The fake keeps the shapes the CLI
// reads -- the URL, the target, --start and the last delivery -- and
// mints a token of the real length, so a test that checks what `repose
// webhooks` prints sees what the api would print.

// FakeWebhookHost is the hostname the fake builds URLs on.
const FakeWebhookHost = "hooks.repose.test"

// maxFakeRoutes mirrors webhooks.MaxRoutes.
const maxFakeRoutes = 20

var webhookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// WebhookDelivery is one record of a provider's call.
type WebhookDelivery struct {
	At         time.Time `json:"at"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	DurationMs int       `json:"duration_ms"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

// WebhookRoute is one route as the api returns it.
type WebhookRoute struct {
	RouteID    string            `json:"route_id"`
	Name       string            `json:"name"`
	Port       int               `json:"port"`
	Path       string            `json:"path"`
	AllowStart bool              `json:"allow_start"`
	URL        string            `json:"url"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Last       *WebhookDelivery  `json:"last_delivery,omitempty"`
	Deliveries []WebhookDelivery `json:"deliveries,omitempty"`

	token string
}

func fakeWebhookToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func webhookURL(projectID, name, token string) string {
	return "https://" + FakeWebhookHost + "/" + projectID + "/" + name + "/" + token
}

// AddDelivery records a delivery against a route, for a test that wants
// a listing with a status in it.
func (f *Fake) AddDelivery(projectID, name string, d WebhookDelivery) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	if p == nil {
		return
	}
	for _, r := range p.webhooks {
		if r.Name == name {
			r.Deliveries = append([]WebhookDelivery{d}, r.Deliveries...)
			if len(r.Deliveries) > 20 {
				r.Deliveries = r.Deliveries[:20]
			}
			copyD := d
			r.Last = &copyD
			return
		}
	}
}

func parseFakeTarget(s string) (int, string, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), ":")
	portPart, rest, hasPath := strings.Cut(s, "/")
	port, err := strconv.Atoi(portPart)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", false
	}
	if !hasPath {
		return port, "/", true
	}
	return port, "/" + rest, true
}

func (f *Fake) listWebhooks(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	out := []WebhookRoute{}
	for _, route := range p.webhooks {
		out = append(out, *route)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (f *Fake) putWebhook(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Name       string  `json:"name"`
		Target     string  `json:"target"`
		AllowStart *bool   `json:"allow_start"`
		Path       *string `json:"path"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	if !webhookNameRe.MatchString(body.Name) {
		return invalid("name: a route name matches [a-z0-9-]{1,32}")
	}
	port, path, ok := parseFakeTarget(body.Target)
	if !ok {
		return invalid("target: a target is PORT or PORT/PATH")
	}
	now := f.now()
	for _, route := range p.webhooks {
		if route.Name != body.Name {
			continue
		}
		route.Port, route.Path, route.UpdatedAt = port, path, now
		if body.AllowStart != nil {
			route.AllowStart = *body.AllowStart
		}
		writeJSON(w, http.StatusOK, route)
		return nil
	}
	if len(p.webhooks) >= maxFakeRoutes {
		return invalid("a project may have %d webhook routes", maxFakeRoutes)
	}
	token := fakeWebhookToken()
	route := &WebhookRoute{RouteID: f.nextID(), Name: body.Name, Port: port, Path: path,
		AllowStart: body.AllowStart != nil && *body.AllowStart,
		URL:        webhookURL(p.ID, body.Name, token), CreatedAt: now, UpdatedAt: now, token: token}
	p.webhooks = append(p.webhooks, route)
	writeJSON(w, http.StatusCreated, route)
	return nil
}

func (f *Fake) getWebhook(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	for _, route := range p.webhooks {
		if route.Name == r.PathValue("name") {
			writeJSON(w, http.StatusOK, route)
			return nil
		}
	}
	return notFound("webhook route")
}

func (f *Fake) rotateWebhook(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	for _, route := range p.webhooks {
		if route.Name == r.PathValue("name") {
			f.webhookTombstones = append(f.webhookTombstones, hashWebhookToken(route.token))
			route.token = fakeWebhookToken()
			route.URL = webhookURL(p.ID, route.Name, route.token)
			route.UpdatedAt = f.now()
			writeJSON(w, http.StatusOK, route)
			return nil
		}
	}
	return notFound("webhook route")
}

func (f *Fake) deleteWebhook(w http.ResponseWriter, r *http.Request) *apiError {
	p, e := f.project(userFrom(r), r.PathValue("id"))
	if e != nil {
		return e
	}
	name := r.PathValue("name")
	for i, route := range p.webhooks {
		if route.Name == name {
			f.webhookTombstones = append(f.webhookTombstones, hashWebhookToken(route.token))
			p.webhooks = append(p.webhooks[:i], p.webhooks[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return nil
		}
	}
	return notFound("webhook route")
}

// The edge's side of the webhook routes (DECISIONS I-1110). The fake
// serves them so the contract test and anything driving the fake edge
// has the same shapes the api sends.

func (f *Fake) internalWebhookRoutes(w http.ResponseWriter, r *http.Request) *apiError {
	type edgeRoute struct {
		RouteID    string `json:"route_id"`
		ProjectID  string `json:"project_id"`
		Name       string `json:"name"`
		Port       int    `json:"port"`
		Path       string `json:"path"`
		AllowStart bool   `json:"allow_start"`
		TokenHash  string `json:"token_sha256"`
	}
	out := struct {
		Version    string      `json:"version"`
		Routes     []edgeRoute `json:"routes"`
		Tombstones []string    `json:"tombstones"`
		Unchanged  bool        `json:"unchanged,omitempty"`
	}{Routes: []edgeRoute{}, Tombstones: f.webhookTombstones}
	n := 0
	for _, p := range f.projects {
		if p.destroyed {
			continue
		}
		for _, route := range p.webhooks {
			n++
			out.Routes = append(out.Routes, edgeRoute{RouteID: route.RouteID, ProjectID: p.ID, Name: route.Name,
				Port: route.Port, Path: route.Path, AllowStart: route.AllowStart,
				TokenHash: hashWebhookToken(route.token)})
		}
	}
	out.Version = strconv.Itoa(n) + "." + strconv.Itoa(len(f.webhookTombstones))
	if since := r.URL.Query().Get("since"); since != "" && since == out.Version {
		writeJSON(w, http.StatusOK, map[string]any{"version": out.Version, "unchanged": true,
			"routes": []edgeRoute{}, "tombstones": []string{}})
		return nil
	}
	if out.Tombstones == nil {
		out.Tombstones = []string{}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func hashWebhookToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (f *Fake) internalWebhookStart(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		ProjectID string `json:"project_id"`
		RouteID   string `json:"route_id"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	p, ok := f.projects[body.ProjectID]
	if !ok || p.destroyed {
		return notFound("project")
	}
	if p.State != "stopped" {
		writeJSON(w, http.StatusOK, map[string]any{"started": false, "reason": "not_stopped"})
		return nil
	}
	p.State = "starting"
	writeJSON(w, http.StatusOK, map[string]any{"started": true, "reason": "started"})
	return nil
}

func (f *Fake) internalWebhookDeliveries(w http.ResponseWriter, r *http.Request) *apiError {
	var body struct {
		Deliveries []WebhookDelivery `json:"deliveries"`
	}
	if e := decodeBody(r, &body, false); e != nil {
		return e
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
