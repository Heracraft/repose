package httpapi_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAccountSecrets covers DECISIONS I-697 at the api: an account secret
// reaches every machine of the account, a running one within seconds and
// a stopped one at its next start; a project's own secret of the same
// name wins and is named in the answer; a remove takes it off the
// running machines that had it; values never come back; another user's
// machines never see it.
func TestAccountSecrets(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn(t, "sub-acct", "acct")
	if r := e.do(t, tok, "GET", "/me/secrets", nil); r.status != 200 || len(r.list) != 0 || string(r.raw) == "null" {
		t.Fatalf("empty list: %d %s", r.status, r.raw)
	}
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "pa", "class": "small"})
	pa := r.body["id"].(string)
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("create pa: %+v", op.Error)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pb", "class": "small"})
	pb := r.body["id"].(string)
	e.waitOp(t, r)
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pb+"/stop", nil)); op.State != "done" {
		t.Fatalf("stop pb: %+v", op.Error)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "pc", "class": "small"})
	pc := r.body["id"].(string)
	e.waitOp(t, r)
	bob := e.signIn(t, "sub-bob2", "bob2")
	r = e.do(t, bob, "POST", "/projects", map[string]any{"name": "bobs", "class": "small"})
	bobs := r.body["id"].(string)
	e.waitOp(t, r)

	own := base64.StdEncoding.EncodeToString([]byte("PROJECT-OWN-VALUE"))
	if r := e.do(t, tok, "PUT", "/projects/"+pc+"/secrets/COOLIFY_TOKEN", map[string]any{"value": own}); r.status != 200 {
		t.Fatalf("project secret: %d %s", r.status, r.raw)
	}
	e.h.WaitIdle(uuid.MustParse(pc))

	val := base64.StdEncoding.EncodeToString([]byte("ACCOUNT-PLANTED-VALUE"))
	r = e.do(t, tok, "PUT", "/me/secrets/COOLIFY_TOKEN", map[string]any{"value": val})
	if r.status != 200 || r.body["name"] != "COOLIFY_TOKEN" {
		t.Fatalf("put: %d %s", r.status, r.raw)
	}
	// pa is running and has no COOLIFY_TOKEN of its own: it got the push.
	// pb is stopped. pc keeps its own.
	if r.body["pushed"] != float64(1) {
		t.Fatalf("pushed: %s", r.raw)
	}
	if own, _ := r.body["overridden_by"].([]any); len(own) != 1 || own[0] != "pc" {
		t.Fatalf("overridden_by: %s", r.raw)
	}
	guestSecret := func(pid, name string) (string, bool) {
		for _, g := range e.h.Fake.Guests() {
			if g.ProjectID == pid {
				v, ok := g.Secrets[name]
				return string(v), ok
			}
		}
		return "", false
	}
	e.h.WaitFor("the account secret on the running machine", func() bool {
		v, _ := guestSecret(pa, "COOLIFY_TOKEN")
		return v == "ACCOUNT-PLANTED-VALUE"
	})
	e.h.WaitIdle(uuid.MustParse(pa))
	e.h.WaitIdle(uuid.MustParse(pc))
	if v, _ := guestSecret(pc, "COOLIFY_TOKEN"); v != "PROJECT-OWN-VALUE" {
		t.Fatalf("pc's own value was replaced: %q", v)
	}
	if _, ok := guestSecret(bobs, "COOLIFY_TOKEN"); ok {
		t.Fatal("another user's machine got the account secret")
	}
	// The stopped one gets it at its next start.
	if op := e.waitOp(t, e.do(t, tok, "POST", "/projects/"+pb+"/start", nil)); op.State != "done" {
		t.Fatalf("start pb: %+v", op.Error)
	}
	if v, _ := guestSecret(pb, "COOLIFY_TOKEN"); v != "ACCOUNT-PLANTED-VALUE" {
		t.Fatalf("pb after start: %q", v)
	}

	// Listing names, never values; the project's list stays its own.
	r = e.do(t, tok, "GET", "/me/secrets", nil)
	if r.status != 200 || len(r.list) != 1 || r.list[0].(map[string]any)["name"] != "COOLIFY_TOKEN" || r.list[0].(map[string]any)["updated_at"] == nil || strings.Contains(string(r.raw), "PLANTED") || strings.Contains(string(r.raw), val) {
		t.Fatalf("list: %d %s", r.status, r.raw)
	}
	if r := e.do(t, tok, "GET", "/projects/"+pa+"/secrets", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("project list: %d %s", r.status, r.raw)
	}
	if r := e.do(t, bob, "GET", "/me/secrets", nil); r.status != 200 || len(r.list) != 0 {
		t.Fatalf("bob's list: %d %s", r.status, r.raw)
	}

	// Names are checked as a project's are.
	for _, bad := range []string{"lower", "user_ca.pub", "BASH_ENV"} {
		if r := e.do(t, tok, "PUT", "/me/secrets/"+bad, map[string]any{"value": val}); r.status != 400 || errCode(r) != "invalid" {
			t.Fatalf("%s: %d %s", bad, r.status, r.raw)
		}
	}
	if r := e.do(t, tok, "PUT", "/me/secrets/X", map[string]any{"value": "not base64!"}); r.status != 400 {
		t.Fatalf("bad base64: %d", r.status)
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, 64<<10+1))
	if r := e.do(t, tok, "PUT", "/me/secrets/BIG", map[string]any{"value": big}); r.status != 400 {
		t.Fatalf("big: %d", r.status)
	}

	// Audited by name, never by value.
	var audited int
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from audit_log where action = 'account_secret_put' and detail->>'name' = 'COOLIFY_TOKEN' and detail::text not like '%PLANTED%'").Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit: %d %v", audited, err)
	}
	if strings.Contains(e.logs.String(), "ACCOUNT-PLANTED-VALUE") || strings.Contains(e.logs.String(), val) {
		t.Fatal("the value reached a log")
	}

	// A configuration carrying the account secret's value is refused, as
	// one carrying a project secret's is: the machine has the value.
	e.h.WaitIdle(uuid.MustParse(pa))
	r = e.do(t, tok, "PUT", "/projects/"+pa+"/config", map[string]any{"fragment": "{ pkgs, ... }: { environment.variables.K = \"ACCOUNT-PLANTED-VALUE\"; }"})
	if r.status != 200 && r.status != 202 {
		t.Fatalf("put config: %d %s", r.status, r.raw)
	}
	if op := e.h.WaitOp(uuid.MustParse(r.body["op_id"].(string))); op.State != "error" || op.Error["message"] != "fragment contains the value of secret COOLIFY_TOKEN" {
		t.Fatalf("account secret in the fragment: %s %+v", op.State, op.Error)
	}

	// Remove: off pa and pb, which are running and had it; pc keeps its own.
	e.h.WaitIdle(uuid.MustParse(pb))
	r = e.do(t, tok, "DELETE", "/me/secrets/COOLIFY_TOKEN", nil)
	if r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	e.h.WaitFor("the account secret off both running machines", func() bool {
		_, a := guestSecret(pa, "COOLIFY_TOKEN")
		_, b := guestSecret(pb, "COOLIFY_TOKEN")
		return !a && !b
	})
	e.h.WaitIdle(uuid.MustParse(pc))
	if v, _ := guestSecret(pc, "COOLIFY_TOKEN"); v != "PROJECT-OWN-VALUE" {
		t.Fatalf("pc after the account's went: %q", v)
	}
	if r := e.do(t, tok, "DELETE", "/me/secrets/COOLIFY_TOKEN", nil); r.status != 404 || errCode(r) != "not_found" {
		t.Fatalf("delete twice: %d %s", r.status, r.raw)
	}
	if err := e.h.Pool.QueryRow(e.h.Ctx, "select count(*) from audit_log where action = 'account_secret_delete' and detail->>'name' = 'COOLIFY_TOKEN'").Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("delete audit: %d %v", audited, err)
	}
	e.h.WaitIdle(uuid.MustParse(pa))
	e.h.WaitIdle(uuid.MustParse(pb))
}
