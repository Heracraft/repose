package ops_test

import (
	"testing"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Removing a running project's last secret, its own or the account's,
// still sends UpdateSecrets, with the empty set: the empty set is what
// takes the secret off the guest (found live 2026-10-14).
func TestUpdateSecretsSendsTheEmptySet(t *testing.T) {
	for _, account := range []bool{false, true} {
		name := "project"
		if account {
			name = "account"
		}
		t.Run(name, func(t *testing.T) {
			h := apitest.New(t, apitest.Options{})
			u := h.NewUser("alice")
			ctx := h.Ctx
			p := h.NewProject(u, "todo", "large")
			pid := p.ID
			var err error
			if account {
				err = h.Secrets.PutAccount(ctx, u.ID.String(), "ONLY", []byte("v"))
			} else {
				err = h.Secrets.Put(ctx, u.ID.String(), pid.String(), "ONLY", []byte("v"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreate()})); op.State != "done" {
				t.Fatalf("create: %+v", op.Error)
			}
			if account {
				_, err = h.Secrets.DeleteAccount(ctx, u.ID.String(), "ONLY")
			} else {
				_, err = h.Secrets.Delete(ctx, pid.String(), "ONLY")
			}
			if err != nil {
				t.Fatal(err)
			}
			if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindUpdateSecrets, ProjectID: &pid, Phases: ops.PlanUpdateSecrets()})); op.State != "done" {
				t.Fatalf("update_secrets: %+v", op.Error)
			}
			var upd *hostdv1.UpdateSecrets
			for _, c := range h.Fake.Commands() {
				if us := c.GetUpdateSecrets(); us != nil {
					upd = us
				}
			}
			if upd == nil {
				t.Fatal("no UpdateSecrets sent for the empty set")
			}
			if len(upd.Secrets) != 0 {
				t.Fatalf("UpdateSecrets carried %d secrets, want 0", len(upd.Secrets))
			}
			for _, g := range h.Fake.Guests() {
				if _, ok := g.Secrets["ONLY"]; ok {
					t.Fatal("ONLY still on the guest")
				}
			}
		})
	}
}
