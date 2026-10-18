// Package scheduler is the placement function (DESIGN.md §9,
// 05-control-plane-api.md §5.3): among hosts that are ready, not draining
// and heartbeating, with room for the class's memory below the host
// reserve, committed guest vCPUs at most PlacementVCPUPct of the host's
// physical cores, and a thin pool that stays at most PlacementPoolPct
// used with the new volume's first bytes in it, pick the one with the
// most free memory. Placement runs under one advisory transaction lock so
// concurrent creates see each other's reservations.
package scheduler

import (
	"context"
	"errors"
	"fmt"
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

// ClassVCPU is the vCPU count of a class: 4 GB of the class's memory per
// guest vCPU, uniformly across the three (DESIGN.md §5, DECISIONS I-784
// amending R3-2). It is the api's only Go copy of the figure;
// ClassVCPUSQL is the only SQL one, and internal/cli and
// internal/hostd/guest keep their own, pinned to this by
// TestClassSpecsMatchBillingAndHost.
func ClassVCPU(class string) int {
	switch class {
	case "small":
		return 1
	case "xl":
		return 4
	default:
		return 2
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

// HostReserve is the memory kept for the host itself (DESIGN.md §4): an
// eighth of it, never below 2 GiB and never above 16 GiB. The eighth is
// what the two steps it replaced already were at their edges (8 GiB at
// 64 GB, 16 GiB at 128 GB), so no host repose runs today changes by more
// than a rounding of its own size; what changes is the small end, where a
// flat 8 GiB left an 8 GB host unable to start any guest at all
// (DECISIONS I-724). hostd computes the same figure, and so does the SQL
// below and nix/hosts/hostd.nix's guests.slice ceiling: three languages,
// one rule, and the VM test in nix/hosts/tests/guest-memory.nix holds
// them together. Memory is still never oversubscribed (R3-2): the reserve
// is only the host's own share, taken off the top before any guest.
func HostReserve(memBytes int64) int64 {
	r := memBytes / 8
	if r < 2<<30 {
		r = 2 << 30
	}
	if r > 16<<30 {
		r = 16 << 30
	}
	return r
}

// hostReserveSQL is HostReserve for host rows h, kept beside it so the
// two cannot drift (DECISIONS I-724).
const hostReserveSQL = `least(greatest(h.mem_bytes / 8, 2::bigint<<30), 16::bigint<<30)`

// Pick is a placement.
type Pick struct {
	HostID    uuid.UUID
	Name      string
	FreeBytes int64
}

// ErrNoCPUCapacity is ErrNoCapacity when the CPU term is what refused:
// a host would have fit but for its committed guest vCPUs against its
// cores. It wraps ErrNoCapacity, so every caller that tests for that
// keeps working; it exists so the api's schedule_fail log line names
// which term refused, which is the difference between buying memory and
// buying cores (DECISIONS I-736).
var ErrNoCPUCapacity = fmt.Errorf("%w: committed guest vCPUs per physical core", ErrNoCapacity)

// PickHost chooses a host inside tx and takes the placement lock; the
// caller must record the placement (projects.host_id) in the same
// transaction so the next placement sees it. A host whose thin pool is
// unavailable (hosts.pool_unavailable, DECISIONS I-644) is never chosen:
// a create there could only fail. holdBytes is what the new
// volume holds at first (store.Project.HeldBytes: a new project's
// estimate, or a restore's source figure), volumeBytes its size.
//
// A host already at PlacementVCPUPct of its cores in committed guest
// vCPUs is not chosen either (cpuRoomSQL, DECISIONS I-736); when that is
// the only reason nothing fit, the error is ErrNoCPUCapacity.
func PickHost(ctx context.Context, tx pgx.Tx, class string, holdBytes, volumeBytes int64, now time.Time) (Pick, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", LockPlacement); err != nil {
		return Pick{}, err
	}
	need := ClassRAM(class)
	vcpu := ClassVCPU(class)
	var p Pick
	err := tx.QueryRow(ctx, pickSQL(cpuRoomSQL("coalesce(r.vcpus, 0)", "$6", "true")),
		now.Add(-HeartbeatWindow), holdBytes, need, volumeBytes, HostGuestSlots, vcpu).Scan(&p.HostID, &p.Name, &p.FreeBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Which term refused is the difference between buying memory
			// and buying cores, and the failure path is the only place
			// that can say: one more query on a create that has already
			// failed (I-736).
			var ignored Pick
			// `$6 >= 0` holds for every class and keeps the parameter
			// referenced, which the extended protocol requires.
			if err := tx.QueryRow(ctx, pickSQL("$6 >= 0"),
				now.Add(-HeartbeatWindow), holdBytes, need, volumeBytes, HostGuestSlots, vcpu).Scan(&ignored.HostID, &ignored.Name, &ignored.FreeBytes); err == nil {
				return Pick{}, ErrNoCPUCapacity
			}
			return Pick{}, ErrNoCapacity
		}
		return Pick{}, err
	}
	return p, nil
}

// pickSQL is PickHost's query with cpuRoom as its CPU condition, so the
// same query can be asked again with that condition satisfied trivially,
// to find out whether it is what refused.
func pickSQL(cpuRoom string) string {
	return `
		select h.id, h.name,
		       least(h.free_mem_bytes, h.mem_bytes - ` + hostReserveSQL + ` - coalesce(r.reserved_bytes, 0)) as free
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		 where h.state = 'ready'
		   and not h.draining
		   and not h.pool_unavailable
		   and h.last_heartbeat_at is not null
		   and h.last_heartbeat_at > $1
		   and ` + poolRoomSQL + `
		   and ` + slotsSQL + `
		   and ` + cpuRoom + `
		   and least(h.free_mem_bytes, h.mem_bytes - ` + hostReserveSQL + ` - coalesce(r.reserved_bytes, 0)) >= $3
		 order by free desc, h.name
		 limit 1`
}

// FitsOn reports whether host can take count more projects of class,
// each starting (needMem) or not, under PickHost's conditions, with the
// placement lock taken: a fork's copies cloned on their source's host
// (DECISIONS I-701). A clone holds nothing new at first, so the pool is
// asked only to be under PlacementPoolPct. Copies that start are asked
// for their memory and for their guest vCPUs against the host's cores,
// the same two terms PickHost asks (I-734, I-736); copies created
// stopped are asked for neither. A host whose hostd does not say
// local_clone in its Hello never fits.
func FitsOn(ctx context.Context, tx pgx.Tx, host uuid.UUID, class string, count int, needMem bool, now time.Time) (bool, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", LockPlacement); err != nil {
		return false, err
	}
	need := int64(0)
	vcpu := 0
	if needMem {
		need = ClassRAM(class) * int64(count)
		// Copies that start commit their vCPUs on the source's host the
		// same moment they commit their memory, so the CPU term applies
		// to exactly the forks the memory term applies to (I-736). A
		// fork of five xl copies is 40 guest vCPUs; without this the
		// whole fork path placed them against nothing.
		vcpu = ClassVCPU(class) * count
	}
	var ok bool
	err := tx.QueryRow(ctx, `
		select exists (
		select 1
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		 where h.id = $6
		   and h.state = 'ready'
		   and not h.draining
		   and not h.pool_unavailable
		   and 'local_clone' = any(h.features)
		   and h.last_heartbeat_at is not null
		   and h.last_heartbeat_at > $1
		   and `+poolRoomSQL+`
		   and (select count(*) from projects p where p.host_id = h.id and p.destroyed_at is null) + $7 <= $5
		   and `+cpuRoomSQL("coalesce(r.vcpus, 0)", "$8", "$7 <= 1")+`
		   and least(h.free_mem_bytes, h.mem_bytes - `+hostReserveSQL+` - coalesce(r.reserved_bytes, 0)) >= $3)`,
		now.Add(-HeartbeatWindow), int64(0), need, int64(0), HostGuestSlots, host, count, vcpu).Scan(&ok)
	return ok, err
}

// Freeing reports whether some host that PickHost could choose would fit
// class once the guests being stopped on it are down: their memory is
// still in the host's free_mem_bytes (hostd counts a stopping guest) and,
// for `stopping`, in its reservations, but is about to come back. A create
// right after `repose rm` of a project on a full host lands in that window
// (dogfood 2026-10-01, DECISIONS I-408). Their guest vCPUs come back the
// same way: the reservation view counts a `stopping` guest, so the CPU
// term is asked as if the stopping guests were already down. q need not
// hold the lock: the answer only decides whether a placement is tried
// again.
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
		               coalesce(sum(`+classRAMSQL+`), 0)::bigint as going,
		               coalesce(sum(case when p.state = 'stopping' then `+ClassVCPUSQL("p.class")+` else 0 end), 0)::integer as stopping_vcpu
		          from projects p
		         where p.host_id = h.id and p.state in ('stopping', 'destroying')) g on true
		 where h.state = 'ready'
		   and not h.draining
		   and not h.pool_unavailable
		   and h.last_heartbeat_at is not null
		   and h.last_heartbeat_at > $1
		   and `+poolRoomSQL+`
		   and `+slotsSQL+`
		   and g.going > 0
		   and `+cpuRoomSQL("(coalesce(r.vcpus, 0) - g.stopping_vcpu)", "$6", "true")+`
		   and least(h.free_mem_bytes + g.going, h.mem_bytes - `+hostReserveSQL+` - coalesce(r.reserved_bytes, 0) + g.stopping) >= $3)`,
		now.Add(-HeartbeatWindow), holdBytes, ClassRAM(class), volumeBytes, HostGuestSlots, ClassVCPU(class)).Scan(&ok)
	return ok, err
}

// classRAMSQL is ClassRAM for a projects row aliased p.
const classRAMSQL = `(case p.class when 'small' then 4::bigint<<30 when 'xl' then 16::bigint<<30 else 8::bigint<<30 end)`

// ClassVCPUSQL is ClassVCPU in SQL, over the class column named by col
// ("p.class" for a projects row aliased p, "class" for an unaliased one).
// Every query that divides by or sums a class's vCPUs calls this, so the
// figure has one SQL home as well as one Go home: placement here, the
// Usage charts in internal/api/http/samples.go and the busy-unattended
// signal in internal/api/abuse each held their own copy until I-784, and
// a copy that kept the old 2-GB-per-vCPU numbers would have divided a
// guest's CPU by twice the vCPUs it has. The one copy this cannot reach
// is the host_reservations view (migration 0028);
// TestHostReservationsVCPUsMatchClassVCPU is what holds that to this.
func ClassVCPUSQL(col string) string {
	return `(case ` + col + ` when 'small' then 1 when 'xl' then 4 else 2 end)`
}

// PlacementVCPUPct is how many guest vCPUs a host may have committed, as
// a percentage of its physical cores, for a new project to be placed
// there (DECISIONS I-736, amending R3-2). Measured on prod on 2026-10-14
// (docs/proposals/2026-10-14-host-cpu-capacity.md §1): typing stays
// immediate while the guest vCPUs actually busy are at most the host's
// usable cores, nine tenths of them, the rest being the host's own work;
// at one to two times that, a keystroke in a guest with nothing running
// in it takes 17 to 45 ms instead of 3 and an ssh session takes 2 s
// instead of 0.2. A machine is busy 48 percent of its running time on the
// busiest account measured and 33 percent fleet-wide
// (2026-10-11-pause-idle-machines.md §5), so 150 percent committed is
// 0.72 busy vCPUs per core at the worse of those, a fifth under the line.
//
// R3-2's "CPU 2:1 oversubscribed" counted Azure's vCPUs, which are SMT
// threads, so it permitted 4 guest vCPUs per core: 150 percent of cores
// is 0.75:1 against those threads, and no host becomes more permissive
// than R3-2 as written. 2:1 against cores would not have refused the
// guest that caused the incident (two xl on 8 cores is exactly 16); 150
// percent does, at 12.
const PlacementVCPUPct = 150

// cpuRoomSQL is the CPU condition for hosts h: committed is the SQL for
// the guest vCPUs already committed there and want the placeholder for
// the vCPUs this placement adds. Integer arithmetic, after the multiply,
// as PlacementPoolPct's condition is: 8 cores allow 12.
//
// Three escapes, all so that the term refuses less rather than
// everything. A host that has reported no cores is exempt: 0 means
// unknown (hosts.cores, 0027), and refusing every create on a one-host
// fleet because a field is missing is worse than placing on it, which is
// what every release before I-736 did. A placement that adds no running
// guest is never refused, which is how the memory term already behaves:
// FitsOn passes 0 for a fork whose copies are created stopped, and
// `least(...) >= 0` holds for any host. And an empty host takes one
// guest whatever its class, so a host with fewer cores than the class
// asks for -- a staging or dev host -- can still run something; single
// is the condition under which this placement is that one guest, which
// is always true for PickHost and Freeing and is `count = 1` for a
// fork's batch. Writing that escape as `committed = 0` alone would wave
// a whole fork through on an empty host, which is what
// TestFitsOnCountsTheCopiesVCPUs catches.
//
// What this does not do: a project keeps its host for life, so `repose
// start` of a stopped machine never asks placement again, hostd's own
// admission checks memory and the pool and never CPU, and an operator
// placing by hand bypasses this. A host can therefore still be driven
// past the ratio by the guests already on it; HostCPUSaturated (I-738) is
// the backstop that says so.
func cpuRoomSQL(committed, want, single string) string {
	return `(h.cores <= 0
		        or ` + want + ` = 0
		        or (` + committed + ` = 0 and ` + single + `)
		        or ` + committed + ` + ` + want + ` <= h.cores * ` + strconv.Itoa(PlacementVCPUPct) + ` / 100)`
}
