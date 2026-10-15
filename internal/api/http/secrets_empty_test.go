package httpapi_test

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

// Removing a running machine's last secret through the API, its own with
// DELETE /projects/{id}/secrets/{name} or the account's with DELETE
// /me/secrets/{name}, sends that machine UpdateSecrets with no secrets
// (I-732; found live 2026-10-14 with rm --global).
func TestRemovingTheLastSecretSendsTheEmptySet(t *testing.T) {
	for _, tc := range []struct{ name, sub string }{{"project", "sub-empty-p"}, {"account", "sub-empty-a"}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tok := e.signIn(t, tc.sub, "empty"+tc.name)
			r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "only", "class": "small"})
			pid := r.body["id"].(string)
			if op := e.waitOp(t, r); op.State != "done" {
				t.Fatalf("create: %+v", op.Error)
			}
			path := "/projects/" + pid + "/secrets/ONLY"
			if tc.name == "account" {
				path = "/me/secrets/ONLY"
			}
			val := base64.StdEncoding.EncodeToString([]byte("v"))
			if r := e.do(t, tok, "PUT", path, map[string]any{"value": val}); r.status != 200 {
				t.Fatalf("put: %d %s", r.status, r.raw)
			}
			guestID := ""
			e.h.WaitFor("ONLY on the running machine", func() bool {
				for _, g := range e.h.Fake.Guests() {
					if g.ProjectID == pid {
						guestID = g.GuestID
						_, ok := g.Secrets["ONLY"]
						return ok
					}
				}
				return false
			})
			e.h.WaitIdle(uuid.MustParse(pid))
			sent := len(e.h.Fake.Commands())
			if r := e.do(t, tok, "DELETE", path, nil); r.status != 204 {
				t.Fatalf("delete: %d %s", r.status, r.raw)
			}
			e.h.WaitIdle(uuid.MustParse(pid))
			n := 0
			for _, c := range e.h.Fake.Commands()[sent:] {
				us := c.GetUpdateSecrets()
				if us == nil || us.GuestId != guestID {
					continue
				}
				n++
				if len(us.Secrets) != 0 {
					t.Fatalf("UpdateSecrets after the last secret's removal carried %d secrets, want 0", len(us.Secrets))
				}
			}
			if n != 1 {
				t.Fatalf("%d UpdateSecrets after the removal, want 1", n)
			}
			for _, g := range e.h.Fake.Guests() {
				if _, ok := g.Secrets["ONLY"]; ok && g.ProjectID == pid {
					t.Fatal("ONLY still on the machine")
				}
			}
		})
	}
}
