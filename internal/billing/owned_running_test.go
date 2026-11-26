package billing_test

import (
	"context"
	"testing"

	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/billing"
	"github.com/heracraft/repose/internal/db/testdb"
)

// A machine on a host the account owns takes none of the plan's memory
// (I-1364, I-1632), so it is left out of the running memory the billing
// page, `repose account` and the gate count. pass2-dash N4: Billing's
// Running read 60 GB with three owned machines running, 8 GB each.
func TestOwnedMachinesTakeNoPlanMemory(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := seedAccount(t, pool, "solo", "active", "large", "running")
	host := store.NewID()
	if _, err := pool.Exec(ctx, `insert into hosts (id, name, state, owner_account_id) values ($1, 'mac-mini', 'ready', $2)`, host, a.UserID); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"own-a", "own-b"} {
		if _, err := pool.Exec(ctx, `insert into projects (id, user_id, name, slug, class, state, volume_bytes, host_id)
			values ($1, $2, $3, $3, 'large', 'running', 1, $4)`, store.NewID(), a.UserID, slug, host); err != nil {
			t.Fatal(err)
		}
	}
	running, gb, err := billing.RunningMemory(ctx, pool, a.UserID, store.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if gb != 8 || len(running) != 1 || running[0].Slug != a.Slug {
		t.Fatalf("running %d GB over %d machines, want 8 GB, the one on repose's servers", gb, len(running))
	}
}
