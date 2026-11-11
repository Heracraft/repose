package httpapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/webhooks"
)

// hashOf is the edge's lookup form of a token, as the api sends it.
func hashOf(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func lastSegment(u string) string { return u[strings.LastIndexByte(u, '/')+1:] }

func stringsOf(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func mapsOf(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		m, _ := x.(map[string]any)
		out = append(out, m)
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// timeAt spaces a batch's deliveries a second apart, so "the newest 20"
// is a question with one answer.
func timeAt(i int) string {
	return time.Date(2026, 11, 11, 10, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
}

// newWebhookProject signs a user in and creates a running project.
func newWebhookProject(t *testing.T, e *env, sub, handle string) (token, pid string) {
	t.Helper()
	token = e.signIn(t, sub, handle)
	r := e.do(t, token, "POST", "/projects", map[string]any{"name": "hooked", "class": "small"})
	if r.status != 201 && r.status != 202 && r.status != 200 {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	pid = r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	return token, pid
}

// A route is added, listed and reached by a URL whose token is 22
// characters of base64url on the one public hostname.
func TestWebhookAddPrintsAnUnguessableURL(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-add", "whadd")
	r := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "3000/api/webhooks/stripe"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	url, _ := r.body["url"].(string)
	prefix := "https://hooks.test/" + pid + "/stripe/"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("url %q does not start with %q", url, prefix)
	}
	tokenPart := strings.TrimPrefix(url, prefix)
	if len(tokenPart) != 22 {
		t.Fatalf("token %q is %d characters, want 22 (128 bits of base64url)", tokenPart, len(tokenPart))
	}
	if r.body["path"] != "/api/webhooks/stripe" || r.body["port"].(float64) != 3000 {
		t.Fatalf("target round trip: %v", r.body)
	}
	if r.body["allow_start"] != false {
		t.Fatalf("--start defaults on: %v", r.body["allow_start"])
	}
	l := e.do(t, tok, "GET", "/projects/"+pid+"/webhooks", nil)
	if l.status != 200 {
		t.Fatalf("list: %d %s", l.status, l.raw)
	}
	rows := mapsOf(l.list)
	if len(rows) != 1 || rows[0]["url"] != url {
		t.Fatalf("list: %v", rows)
	}
}

// Adding the same name again changes the target and keeps the token, so a
// URL already pasted into a provider's dashboard goes on working.
func TestWebhookAddAgainKeepsTheToken(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-again", "whagain")
	first := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "3000"})
	if first.status != 201 {
		t.Fatalf("add: %d %s", first.status, first.raw)
	}
	again := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "4000/sms", "allow_start": true})
	if again.status != 200 {
		t.Fatalf("add again: %d %s", again.status, again.raw)
	}
	if again.body["url"] != first.body["url"] {
		t.Fatalf("the token changed: %v then %v", first.body["url"], again.body["url"])
	}
	if again.body["port"].(float64) != 4000 || again.body["path"] != "/sms" || again.body["allow_start"] != true {
		t.Fatalf("the target did not change: %v", again.body)
	}
}

// Rotate replaces the token at once and leaves the old hash a tombstone,
// so the edge answers 410 for the old URL and routes the new one.
func TestWebhookRotateTombstonesTheOldToken(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-rot", "whrot")
	add := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "3000"})
	oldURL := add.body["url"].(string)
	oldToken := oldURL[strings.LastIndexByte(oldURL, '/')+1:]
	before := e.internalDo(t, "GET", "/internal/webhook-routes", nil)
	if before.status != 200 {
		t.Fatalf("routes: %d %s", before.status, before.raw)
	}
	rot := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks/stripe/rotate", nil)
	if rot.status != 200 {
		t.Fatalf("rotate: %d %s", rot.status, rot.raw)
	}
	newURL := rot.body["url"].(string)
	if newURL == oldURL {
		t.Fatal("rotate returned the same URL")
	}
	after := e.internalDo(t, "GET", "/internal/webhook-routes", nil)
	tombs := stringsOf(after.body["tombstones"])
	if !has(tombs, hashOf(oldToken)) {
		t.Fatalf("the old token is not a tombstone: %v", tombs)
	}
	routes := mapsOf(after.body["routes"])
	if len(routes) != 1 || routes[0]["token_sha256"] != hashOf(newURL[strings.LastIndexByte(newURL, '/')+1:]) {
		t.Fatalf("the table does not carry the new token: %v", routes)
	}
	if has(tombs, hashOf(newURL[strings.LastIndexByte(newURL, '/')+1:])) {
		t.Fatal("the new token is a tombstone")
	}
}

// Removing a route tombstones it; destroying a project tombstones every
// route it had, and the live table loses them.
func TestWebhookRemoveAndDestroyTombstone(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-gone", "whgone")
	a := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "one", "target": "3000"})
	b := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "two", "target": "3001"})
	aTok := lastSegment(a.body["url"].(string))
	bTok := lastSegment(b.body["url"].(string))
	if r := e.do(t, tok, "DELETE", "/projects/"+pid+"/webhooks/one", nil); r.status != 204 {
		t.Fatalf("rm: %d %s", r.status, r.raw)
	}
	tbl := e.internalDo(t, "GET", "/internal/webhook-routes", nil)
	if !has(stringsOf(tbl.body["tombstones"]), hashOf(aTok)) {
		t.Fatal("the removed route's token is not a tombstone")
	}
	if len(mapsOf(tbl.body["routes"])) != 1 {
		t.Fatalf("after rm the table has %d routes, want 1", len(mapsOf(tbl.body["routes"])))
	}
	if r := e.do(t, tok, "DELETE", "/projects/"+pid, nil); r.status != 202 {
		t.Fatalf("destroy: %d %s", r.status, r.raw)
	}
	tbl = e.internalDo(t, "GET", "/internal/webhook-routes", nil)
	if n := len(mapsOf(tbl.body["routes"])); n != 0 {
		t.Fatalf("after the destroy the table has %d routes, want 0", n)
	}
	if !has(stringsOf(tbl.body["tombstones"]), hashOf(bTok)) {
		t.Fatal("the destroyed project's token is not a tombstone")
	}
}

// The table's version moves on every change and holds still otherwise, so
// the edge's 5 s poll is one count when nothing happened.
func TestWebhookTableVersionMovesOnlyOnAChange(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-ver", "whver")
	first := e.internalDo(t, "GET", "/internal/webhook-routes", nil)
	v1 := first.body["version"].(string)
	again := e.internalDo(t, "GET", "/internal/webhook-routes?since="+v1, nil)
	if again.body["unchanged"] != true {
		t.Fatalf("an unchanged table was sent again: %v", again.body)
	}
	if n := len(mapsOf(again.body["routes"])); n != 0 {
		t.Fatalf("an unchanged answer carried %d routes", n)
	}
	e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "3000"})
	third := e.internalDo(t, "GET", "/internal/webhook-routes?since="+v1, nil)
	if third.body["unchanged"] == true {
		t.Fatal("the version did not move when a route was added")
	}
	if len(mapsOf(third.body["routes"])) != 1 {
		t.Fatalf("the changed table carried %v", third.body["routes"])
	}
}

// A project may have MaxRoutes routes and no more.
func TestWebhookRouteLimit(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-lim", "whlim")
	for i := 0; i < webhooks.MaxRoutes; i++ {
		name := "r" + string(rune('a'+i))
		if r := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": name, "target": "3000"}); r.status != 201 {
			t.Fatalf("add %s: %d %s", name, r.status, r.raw)
		}
	}
	r := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "toomany", "target": "3000"})
	if r.status != 400 {
		t.Fatalf("the %dth route was accepted: %d %s", webhooks.MaxRoutes+1, r.status, r.raw)
	}
}

// A bad name or target is refused before any row is written, and the
// message names what it got.
func TestWebhookRefusesABadNameOrTarget(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-bad", "whbad")
	for _, tc := range []map[string]any{
		{"name": "Stripe", "target": "3000"},
		{"name": "has space", "target": "3000"},
		{"name": "ok", "target": "0"},
		{"name": "ok", "target": "70000"},
		{"name": "ok", "target": "not-a-port"},
	} {
		r := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", tc)
		if r.status != 400 {
			t.Fatalf("%v was accepted: %d %s", tc, r.status, r.raw)
		}
	}
	if l := e.do(t, tok, "GET", "/projects/"+pid+"/webhooks", nil); len(mapsOf(l.list)) != 0 {
		t.Fatalf("a refused add wrote a row: %v", l.list)
	}
}

// The edge's delivery batch reaches the route's record, the api keeps the
// newest DeliveriesKept, and an outcome word the api does not know is
// refused, so nothing a sender chose reaches a row.
func TestWebhookDeliveriesAreKeptAndBounded(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-del", "whdel")
	add := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "stripe", "target": "3000"})
	rid := add.body["route_id"].(string)
	batch := []map[string]any{}
	for i := 0; i < webhooks.DeliveriesKept+5; i++ {
		batch = append(batch, map[string]any{"route_id": rid, "at": timeAt(i), "status": 200, "outcome": "delivered",
			"duration_ms": i, "bytes_in": 10, "bytes_out": 20})
	}
	if r := e.internalDo(t, "POST", "/internal/webhook-deliveries", map[string]any{"deliveries": batch}); r.status != 204 {
		t.Fatalf("batch: %d %s", r.status, r.raw)
	}
	got := e.do(t, tok, "GET", "/projects/"+pid+"/webhooks/stripe", nil)
	if got.status != 200 {
		t.Fatalf("get: %d %s", got.status, got.raw)
	}
	kept := mapsOf(got.body["deliveries"])
	if len(kept) != webhooks.DeliveriesKept {
		t.Fatalf("%d deliveries kept, want %d", len(kept), webhooks.DeliveriesKept)
	}
	last, ok := got.body["last_delivery"].(map[string]any)
	if !ok || last["outcome"] != "delivered" {
		t.Fatalf("last_delivery: %v", got.body["last_delivery"])
	}
	bad := e.internalDo(t, "POST", "/internal/webhook-deliveries", map[string]any{"deliveries": []map[string]any{
		{"route_id": rid, "status": 200, "outcome": "<script>", "duration_ms": 1},
	}})
	if bad.status != 400 {
		t.Fatalf("an unknown outcome was accepted: %d %s", bad.status, bad.raw)
	}
}

// GET /internal/route?project_id= answers the same body the login form
// does, which is what a delivery reads: it knows the project and no login.
func TestInternalRouteByProjectID(t *testing.T) {
	e := newEnv(t)
	_, pid := newWebhookProject(t, e, "sub-wh-route", "whroute")
	r := e.internalDo(t, "GET", "/internal/route?project_id="+pid, nil)
	if r.status != 200 {
		t.Fatalf("route: %d %s", r.status, r.raw)
	}
	if r.body["project_id"] != pid || r.body["state"] != "running" {
		t.Fatalf("route: %v", r.body)
	}
	if _, ok := r.body["resting"]; !ok {
		t.Fatalf("no resting flag: %v", r.body)
	}
}

// A delivery on a --start route starts a stopped machine once, and the
// next delivery inside the window is refused with too_soon.
func TestWebhookStartOncePerWindow(t *testing.T) {
	e := newEnv(t)
	tok, pid := newWebhookProject(t, e, "sub-wh-start", "whstart")
	add := e.do(t, tok, "POST", "/projects/"+pid+"/webhooks", map[string]any{"name": "gh", "target": "3000", "allow_start": true})
	rid := add.body["route_id"].(string)
	if r := e.do(t, tok, "POST", "/projects/"+pid+"/stop", nil); r.status != 202 {
		t.Fatalf("stop: %d %s", r.status, r.raw)
	}
	e.h.WaitFor("stopped", func() bool { return e.h.Project(uuid.MustParse(pid)).State == "stopped" })
	first := e.internalDo(t, "POST", "/internal/webhook-start", map[string]any{"project_id": pid, "route_id": rid})
	if first.status != 200 || first.body["started"] != true {
		t.Fatalf("first start: %d %s", first.status, first.raw)
	}
	second := e.internalDo(t, "POST", "/internal/webhook-start", map[string]any{"project_id": pid, "route_id": rid})
	if second.status != 200 || second.body["started"] != false || second.body["reason"] == "" {
		t.Fatalf("second start inside the window: %d %s", second.status, second.raw)
	}
}
