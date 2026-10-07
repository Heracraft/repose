// Package scheduler is the placement function (DESIGN.md §9,
// 05-control-plane-api.md §5.3): among hosts that are ready, not draining
// and heartbeating, with room for the class's memory below the host
// reserve and a thin pool that stays at most PlacementPoolPct used with
// the new volume's first bytes in it, pick the one with the most free
// memory. Placement runs under one advisory transaction lock so
// concurrent creates see each other's reservations.
package scheduler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNoCapacity is the `capacity` error: no host fits.
var ErrNoCapacity = errors.New("no host with capacity")

// HeartbeatWindow is how long a host may be silent and still take work.
const HeartbeatWindow = 90 * time.Second

// LockPlacement is the advisory lock serialising placements.
const LockPlacement int64 = 1100

// ClassRAM is the memory a class reserves (DESIGN.md §5).
func ClassRAM(class string) int64 {
	switch class {
	case "small":
		return 4 << 30
	case "xl":
		return 16 << 30
	default:
		return 8 << 30
	}
}

// ClassVCPU is the vCPU count of a class.
func ClassVCPU(class string) int {
	switch class {
	case "small":
		return 2
	case "xl":
		return 8
	default:
		return 4
	}
}

// DefaultVolume is the default thin volume per class.
func DefaultVolume(class string) int64 {
	switch class {
	case "small":
		return 20 << 30
	case "xl":
		return 80 << 30
	default:
		return 40 << 30
	}
}

// ValidClass reports whether class is one of the three.
func ValidClass(class string) bool {
	return class == "small" || class == "large" || class == "xl"
}

// PlacementPoolPct is how full a host's thin pool may be, with the new
// volume's first bytes, for a new project to be placed there (DECISIONS
// I-586). Volumes are sold by their sizes and counted by what they hold
// (I-585), so the pool is overcommitted by design and the rest is room
// for the guests already there to grow into: lvm autoextends at 80,
// hostd refuses a create, restore or grow at 85, the PoolFull page is at
// 90, and hostd refuses a start at 95.
const PlacementPoolPct = 70

// poolRoomSQL is the pool condition for hosts h, with $2 the bytes the
// new volume holds at first and $4 its size: room under PlacementPoolPct
// when the host reports its pool size, and the whole size free when it
// does not (a hostd older than I-586 reports none in its heartbeat).
var poolRoomSQL = `(case when h.pool_bytes > 0
		        then h.pool_free_bytes - $2 >= h.pool_bytes / 100 * (100 - ` + strconv.Itoa(PlacementPoolPct) + `)
		        else h.pool_free_bytes >= $4 end)`

// HostGuestSlots is how many projects, running or stopped, one host
// takes: hostd gives each guest an address from the host's /22, 1,020 of
// them (manager.maxIndex), and a stopped project keeps its address. With
// the plan's disk counted by what volumes hold (I-585) an account may
// keep up to 100 nearly empty projects, so placement counts them; 20
// short of 1,020 leaves room for creates in flight (DECISIONS I-586).
const HostGuestSlots = 1000

// slotsSQL is the guest slot condition for hosts h, with $5 the slots.
const slotsSQL = `(select count(*) from projects p where p.host_id = h.id and p.destroyed_at is null) < $5`

// HostReserve is the memory kept for the host itself (DESIGN.md §4).
func HostReserve(memBytes int64) int64 {
	if memBytes >= 128<<30 {
		return 16 << 30
	}
	return 8 << 30
}

// Pick is a placement.
type Pick struct {
	HostID    uuid.UUID
	Name      string
	FreeBytes int64
}

// PickHost chooses a host inside tx and takes the placement lock; the
// caller must record the placement (projects.host_id) in the same
// transaction so the next placement sees it. holdBytes is what the new
// volume holds at first (store.Project.HeldBytes: a new project's
// estimate, or a restore's source figure), volumeBytes its size.
func PickHost(ctx context.Context, tx pgx.Tx, class string, holdBytes, volumeBytes int64, now time.Time) (Pick, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", LockPlacement); err != nil {
		return Pick{}, err
	}
	need := ClassRAM(class)
	var p Pick
	err := tx.QueryRow(ctx, `
		select h.id, h.name,
		       least(h.free_mem_bytes, h.mem_bytes - (case when h.mem_bytes >= (128::bigint<<30) then 16::bigint<<30 else 8::bigint<<30 end) - coalesce(r.reserved_bytes, 0)) as free
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		 where h.state = 'ready'
		   and not h.draining
		   and h.last_heartbeat_at is not null
		   and h.last_heartbeat_at > $1
		   and `+poolRoomSQL+`
		   and `+slotsSQL+`
		   and least(h.free_mem_bytes, h.mem_bytes - (case when h.mem_bytes >= (128::bigint<<30) then 16::bigint<<30 else 8::bigint<<30 end) - coalesce(r.reserved_bytes, 0)) >= $3
		 order by free desc, h.name
		 limit 1`, now.Add(-HeartbeatWindow), holdBytes, need, volumeBytes, HostGuestSlots).Scan(&p.HostID, &p.Name, &p.FreeBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Pick{}, ErrNoCapacity
		}
		return Pick{}, err
	}
	return p, nil
}

// Freeing reports whether some host that PickHost could choose would fit
// class once the guests being stopped on it are down: their memory is
// still in the host's free_mem_bytes (hostd counts a stopping guest) and,
// for `stopping`, in its reservations, but is about to come back. A create
// right after `repose rm` of a project on a full host lands in that window
// (dogfood 2026-10-01, DECISIONS I-408). q need not hold the lock: the
// answer only decides whether a placement is tried again.
func Freeing(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, class string, holdBytes, volumeBytes int64, now time.Time) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		select exists (
		select 1
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		  join lateral (
		        select coalesce(sum(case when p.state = 'stopping' then `+classRAMSQL+` else 0 end), 0)::bigint as stopping,
		               coalesce(sum(`+classRAMSQL+`), 0)::bigint as going
		          from projects p
		         where p.host_id = h.id and p.state in ('stopping', 'destroying')) g on true
		 where h.state = 'ready'
		   and not h.draining
		   and h.last_heartbeat_at is not null
		   and h.last_heartbeat_at > $1
		   and `+poolRoomSQL+`
		   and `+slotsSQL+`
		   and g.going > 0
		   and least(h.free_mem_bytes + g.going, h.mem_bytes - (case when h.mem_bytes >= (128::bigint<<30) then 16::bigint<<30 else 8::bigint<<30 end) - coalesce(r.reserved_bytes, 0) + g.stopping) >= $3)`,
		now.Add(-HeartbeatWindow), holdBytes, ClassRAM(class), volumeBytes, HostGuestSlots).Scan(&ok)
	return ok, err
}

// classRAMSQL is ClassRAM for a projects row aliased p.
const classRAMSQL = `(case p.class when 'small' then 4::bigint<<30 when 'xl' then 16::bigint<<30 else 8::bigint<<30 end)`
