package ops_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
)

// TestOwnRefusalReadsTheRoomAgain is ENG-214: a pick an owned host
// refused for memory, and a heartbeat that reports room before the
// refusal's figures are read. The sentence used to fall back to "has no
// room for a small machine", naming no figure and no longer true; the
// placement now takes the room that appeared. Since I-1630 the figure is
// the host's reported free memory against 1 GB, with no class. Without the heartbeat the
// refusal names the figure. The fake host's own heartbeat is an hour
// apart, so only the hook moves free_mem_bytes.
func TestOwnRefusalReadsTheRoomAgain(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Heartbeat: time.Hour}})
	u := h.NewUser("ola")
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set owner_account_id = $1, name = 'e2e-x86-a', free_mem_bytes = 1 where id = $2", u.ID, h.HostID); err != nil {
		t.Fatal(err)
	}
	place := func(pin uuid.UUID) (*uuid.UUID, error) {
		var got *uuid.UUID
		err := db.InTx(h.Ctx, h.Pool, func(tx db.Tx) error {
			id, err := h.Engine.PlaceNewOn(h.Ctx, tx, &store.Project{ID: store.NewID(), UserID: u.ID, Class: "small", VolumeBytes: 1}, pin)
			got = id
			return err
		})
		return got, err
	}

	for _, pin := range []uuid.UUID{uuid.Nil, h.HostID} {
		_, err := place(pin)
		var refused *ops.PlacementRefusal
		if !errors.As(err, &refused) || !strings.HasPrefix(refused.Message, "your host e2e-x86-a has 0 GB of memory free, and a machine starts there when it has at least 1 GB free") {
			t.Fatalf("pin %v, full host: %v, want the figure", pin, err)
		}
	}

	beat := func(ctx context.Context) {
		if _, err := h.Pool.Exec(ctx, "update hosts set free_mem_bytes = 200::bigint << 30 where id = $1", h.HostID); err != nil {
			t.Error(err)
		}
	}
	for _, pin := range []uuid.UUID{uuid.Nil, h.HostID} {
		if _, err := h.Pool.Exec(h.Ctx, "update hosts set free_mem_bytes = 1 where id = $1", h.HostID); err != nil {
			t.Fatal(err)
		}
		h.Engine.SetAfterOwnRefusalForTest(beat)
		id, err := place(pin)
		h.Engine.SetAfterOwnRefusalForTest(nil)
		if err != nil || id == nil || *id != h.HostID {
			t.Fatalf("pin %v, room after the refusal: %v %v, want placed on %s", pin, id, err, h.HostID)
		}
	}
}
