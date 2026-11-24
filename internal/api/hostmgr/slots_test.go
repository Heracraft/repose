package hostmgr_test

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/hostmgr"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
)

// TestHostAddsReuseFreedAddresses is ENG-211: every host add took the
// next value of one global sequence for its guest range and hub
// address, a thousand for all accounts, and a removed host or one never
// installed kept its value for good. One account adding and removing
// hosts in a loop stopped host adds for every account. A slot is now
// free while no row holds its addresses: the account's add-remove loop
// takes the same one again, and the addresses of hosts that may still
// run something keep theirs.
func TestHostAddsReuseFreedAddresses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		hostmgr.SettingEdgeWGPubkey:   "dyP2l2byrPpr9ehOwYga0Ya9/qkUChpSCR0vJVRa0f8=",
		hostmgr.SettingEdgeWGEndpoint: "edge.example:51820",
	} {
		if err := store.SetSetting(ctx, h.pool, k, v); err != nil {
			t.Fatal(err)
		}
	}
	var uid uuid.UUID
	if err := h.pool.QueryRow(ctx, "insert into users (id, logto_sub, email, handle) values ($1, 'sub-slots', 'slots@example.test', 'slots') returning id", store.NewID()).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	add := func(name string, owner *uuid.UUID) (uuid.UUID, netip.Prefix) {
		t.Helper()
		var id uuid.UUID
		err := db.InTx(ctx, h.pool, func(tx db.Tx) error {
			var err error
			if _, id, err = hostmgr.MintJoinTokenIn(ctx, tx, hostmgr.JoinTokenRequest{Name: name, Provider: "metal", Owner: owner}); err != nil {
				return err
			}
			_, err = hostmgr.PrepareHub(ctx, tx, id)
			return err
		})
		if err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
		row, err := store.GetHost(ctx, h.pool, id)
		if err != nil || row.GuestCIDR == nil || row.WGIP == nil {
			t.Fatalf("add %s: row %+v (%v), want addresses", name, row, err)
		}
		return id, *row.GuestCIDR
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := h.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	retire := func(id uuid.UUID) {
		exec("update hosts set state = 'retired', draining = true, cert_serial = null, prev_cert_serial = null, join_token_hash = null where id = $1", id)
	}
	// Repose's own host, a registered host of the account's, and the
	// host the loop below adds and removes.
	op, opCIDR := add("host-01", nil)
	_, liveCIDR := add("box-a", &uid)
	exec("update hosts set registered_at = now(), state = 'ready', join_token_hash = null where name = 'box-a'")
	loop, loopCIDR := add("box-b", &uid)
	// The high-water mark is never reused, so one more host sits above
	// the loop's: an older api may have just taken the mark.
	_, topCIDR := add("box-c", &uid)

	// Each round a new name, as `repose host add` with no name gives:
	// before ENG-211 this spent the thousand slots by round 996.
	for i := 0; i < 1500; i++ {
		retire(loop)
		id, c := add(fmt.Sprintf("box-b-%d", i), &uid)
		if c != loopCIDR {
			t.Fatalf("round %d: range %s, want %s back", i, c, loopCIDR)
		}
		loop = id
	}
	var mark int64
	if err := h.pool.QueryRow(ctx, "select last_value from host_seq").Scan(&mark); err != nil || mark != 4 {
		t.Fatalf("host_seq after 1500 add-remove rounds: %d (%v), want 4", mark, err)
	}

	// A removed host still holding a project keeps its range: the
	// project keeps a guest address inside it.
	retire(loop)
	exec("insert into projects (id, user_id, name, slug, class, state, host_id, volume_bytes) values ($1, $2, 'left', 'left', 'small', 'stopped', $3, 1)", store.NewID(), uid, loop)
	_, next := add("box-d", &uid)
	if next == loopCIDR || next == opCIDR || next == liveCIDR || next == topCIDR {
		t.Fatalf("box-d took %s, a range a host still holds", next)
	}
	var kept *netip.Prefix
	if err := h.pool.QueryRow(ctx, "select guest_cidr from hosts where id = $1", loop).Scan(&kept); err != nil || kept == nil || *kept != loopCIDR {
		t.Fatalf("removed host with a project: range %v (%v), want %s kept", kept, err, loopCIDR)
	}

	// A host that never joined before its token expired frees its slot,
	// and so does repose's own retired host not: an operator reissues it
	// by name and expects its address.
	exec("update projects set destroyed_at = now(), state = 'destroyed', host_id = null where name = 'left'")
	retire(op)
	_, e := add("box-e", &uid)
	if e != loopCIDR {
		t.Fatalf("box-e took %s, want the freed %s", e, loopCIDR)
	}
	_, f := add("box-f", &uid)
	add("box-h", &uid)
	exec("update hosts set join_token_expires_at = now() - interval '1 second' where name = 'box-f'")
	_, g := add("box-g", &uid)
	if g != f {
		t.Fatalf("box-g took %s, want %s from box-f, whose token expired", g, f)
	}
	var opKept *netip.Prefix
	if err := h.pool.QueryRow(ctx, "select guest_cidr from hosts where id = $1", op).Scan(&opKept); err != nil || opKept == nil || *opKept != opCIDR {
		t.Fatalf("repose's retired host: range %v (%v), want %s kept", opKept, err, opCIDR)
	}

	// A freed row asked for again by name gets addresses again, never
	// the ones another host now holds.
	exec("update hosts set state = 'retired', join_token_hash = null where name = 'box-f'")
	if _, c := add("box-f", &uid); c == g {
		t.Fatalf("box-f again took %s, box-g's", c)
	}
	var dup int
	if err := h.pool.QueryRow(ctx, "select count(*) from (select guest_cidr from hosts where guest_cidr is not null group by guest_cidr having count(*) > 1) d").Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("%d ranges held twice (%v)", dup, err)
	}
	if err := h.pool.QueryRow(ctx, "select count(*) from (select wg_ip from hosts where wg_ip is not null group by wg_ip having count(*) > 1) d").Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("%d hub addresses held twice (%v)", dup, err)
	}
}
