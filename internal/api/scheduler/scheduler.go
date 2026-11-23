// Package scheduler is the placement function (DESIGN.md §9,
// 05-control-plane-api.md §5.3): among hosts that are ready, not draining
// and heartbeating, with room for the class's memory below the host
// reserve, committed guest vCPUs at most PlacementVCPUPct of the host's
// physical cores, and a thin pool that stays at most PlacementPoolPct
// used with the new volume's first bytes in it, pick the one with the
// most free memory. Placement runs under one advisory transaction lock so
// concurrent creates see each other's reservations.
//
// Since DECISIONS I-1366 a host may belong to an account rather than to
// repose, so every placement also answers an ownership question: an owned
// host takes its owner's projects and nobody else's, and an account whose
// plan sells no fleet memory is never placed on a host repose runs. That
// term is `ownedSQL` and the caller supplies it as a Target.
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
	case "tiny":
		return 2 << 30
	case "small":
		return 4 << 30
	case "xl":
		return 16 << 30
	default:
		return 8 << 30
	}
}

// ClassMilliVCPU is the guest CPU a class sells, in thousandths of a
// vCPU: 4 GB of the class's memory per vCPU, uniformly across the four
// (DESIGN.md §5, DECISIONS I-784 amending R3-2, I-1168 adding `tiny`).
//
// The unit is thousandths because I-784's ratio has no whole answer below
// `small`: `tiny` is 2 GB, so it sells half a core, and four of them are
// one seat's 8 GB and one seat's 2 guest vCPUs. The other three classes
// are whole numbers and stay whole; 1000 is one core.
//
// It is the api's only Go copy of the figure; ClassMilliVCPUSQL is the
// only SQL one, and internal/cli and internal/hostd/guest keep their own,
// pinned to this by TestClassSpecsMatchBillingAndHost.
func ClassMilliVCPU(class string) int {
	switch class {
	case "tiny":
		return 500
	case "small":
		return 1000
	case "xl":
		return 4000
	default:
		return 2000
	}
}

// ClassVCPUs is ClassMilliVCPU as cores, for display and for the API's
// `vcpus` field. It is a fraction on `tiny` (0.5) and whole elsewhere.
func ClassVCPUs(class string) float64 {
	return float64(ClassMilliVCPU(class)) / 1000
}

// DefaultVolume is the default thin volume per class.
func DefaultVolume(class string) int64 {
	switch class {
	case "tiny":
		return 10 << 30
	case "small":
		return 20 << 30
	case "xl":
		return 80 << 30
	default:
		return 40 << 30
	}
}

// ValidClass reports whether class is one of the four.
func ValidClass(class string) bool {
	return class == "tiny" || class == "small" || class == "large" || class == "xl"
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

// Target is who a placement is for (DECISIONS I-1366). A host a user
// owns takes only that user's projects, and a user whose plan sells no
// memory on the hosts repose runs takes only their own hardware, so
// every placement is answered against this rather than against the
// fleet alone.
type Target struct {
	// Account is the project's owner.
	Account uuid.UUID
	// Hosted is what the project asked for: projects.placement
	// `hosted`, the fleet whatever the account owns. False is `own`,
	// the account's own hardware when it owns any and the fleet when it
	// owns none.
	Hosted bool
	// Fleet is whether the account's plan sells memory on the hosts
	// repose runs (billing.Plan.MemoryGB above 0; the Own hardware plan sells none). False keeps
	// every placement of that account on its own hardware, whatever
	// Hosted says: an Own hardware account buys no fleet memory, and
	// placement is the only thing between it and the fleet. An account
	// with no plan is true here, because the billing gate has already
	// refused it with subscription_required before placement runs.
	Fleet bool
	// Runtime is the project's guest_runtime, nix or erofs (DECISIONS
	// I-1464); empty is nix. Arches is, for an erofs project, the host
	// arches (x86_64, aarch64) the newest erofs bases have an image for.
	Runtime string
	Arches  []string
	// Host, when set, is the one host of the account's own the project
	// asked for (POST /projects `host`, DECISIONS I-1584): no other host
	// is chosen, and when it cannot take the project the error is
	// ErrPinnedHost. The caller has already checked that the account owns
	// it; ownedSQL still holds, so a host of another account's is never
	// chosen whatever this says. uuid.Nil is no pin.
	Host uuid.UUID
}

// pinSQL is the pin term for hosts h, with $12 the pinned host or null
// (DECISIONS I-1584).
const pinSQL = `($12::uuid is null or h.id = $12::uuid)`

// args is the parameters every placement query reads, $1 to $12.
func (t Target) args(now time.Time, class string, holdBytes, volumeBytes int64) []any {
	var pin *uuid.UUID
	if t.Host != uuid.Nil {
		h := t.Host
		pin = &h
	}
	a := []any{now.Add(-HeartbeatWindow), holdBytes, ClassRAM(class), volumeBytes, HostGuestSlots, ClassMilliVCPU(class), t.Account, t.Hosted, t.Fleet}
	return append(append(a, t.runtimeArgs()...), pin)
}

// runtimeSQL is the runtime term for hosts h, with $10 the project's
// runtime and $11 the arches an erofs image exists for (DECISIONS
// I-1464). An erofs project goes only to a host that names the erofs
// feature and whose arch has an image; a nix project only to a host that
// names nix or names neither, which is every hostd older than I-1458.
const runtimeSQL = `(case when $10::text = 'erofs'
		      then 'erofs' = any(h.features) and h.arch = any($11::text[])
		      else 'nix' = any(h.features) or not ('erofs' = any(h.features)) end)`

// runtimeArgs is the two parameters runtimeSQL reads.
func (t Target) runtimeArgs() []any {
	r := "nix"
	if t.Runtime == "erofs" {
		r = "erofs"
	}
	arches := t.Arches
	if arches == nil {
		arches = []string{}
	}
	return []any{r, arches}
}

// ErrNoRuntimeHost is ErrNoCapacity when the runtime term is what
// refused: no host can run this project's guest runtime, or none has an
// image for its arch (DECISIONS I-1464). It wraps ErrNoCapacity.
var ErrNoRuntimeHost = fmt.Errorf("%w: no host runs this guest runtime", ErrNoCapacity)

// ownedSQL is the ownership term for hosts h, with $7 the account, $8
// whether the project asked for the fleet and $9 whether its plan sells
// fleet memory (DECISIONS I-1366).
//
// A host the account owns is eligible unless the project asked for the
// fleet and may have it. A host repose runs is eligible only when the
// plan sells fleet memory, and then only when the project asked for it
// or the account owns no host at all: a user who bought a Mac gets their
// machines on the Mac without saying so per project, and a stranger's
// project never reaches it.
//
// `retired` and `lost` hosts do not count as owning one. A user whose
// only machine is gone is released to the fleet rather than trapped;
// every other state does count, so a machine that is merely switched
// off (`unreachable`) answers "your host cannot take this" and not a
// silent placement on hardware they are not paying for.
const ownedSQL = `(
		   (h.owner_account_id = $7 and not ($8 and $9))
		   or (h.owner_account_id is null and $9
		       and ($8 or not exists (select 1 from hosts o
		                               where o.owner_account_id = $7
		                                 and o.state not in ('retired', 'lost')))))`

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

// ErrNoOwnHost is ErrNoCapacity when the ownership term is what refused
// a placement that only the account's own hardware could take: the
// account owns no host that can take work, or the one it owns is full,
// switched off or draining (DECISIONS I-1366). It wraps ErrNoCapacity so
// every caller that tests for that keeps working, and exists so the user
// reads "your machine is not answering" rather than "no host with
// capacity", which would be a sentence about repose's fleet and no help
// at all to someone whose Mac is asleep.
var ErrNoOwnHost = fmt.Errorf("%w: no host of this account's own", ErrNoCapacity)

// ErrPinnedHost is ErrNoOwnHost when the project named its host
// (Target.Host, DECISIONS I-1584) and that host cannot take it: it is
// not ready, not answering, being removed, or full in memory, disk or
// cores. It wraps ErrNoOwnHost, so the refusal names that host and why,
// and nothing falls back to another host the account owns: a user who
// asked for box A and got box B has been told nothing true.
var ErrPinnedHost = fmt.Errorf("%w: the host the project named", ErrNoOwnHost)

// PickHost chooses a host inside tx and takes the placement lock; the
// caller must record the placement (projects.host_id) in the same
// transaction so the next placement sees it. A host whose thin pool is
// unavailable (hosts.pool_unavailable, DECISIONS I-644) is never chosen:
// a create there could only fail. holdBytes is what the new
// volume holds at first (store.Project.HeldBytes: a new project's
// estimate, or a restore's source figure), volumeBytes its size.
//
// t is who the placement is for (DECISIONS I-1366): a host its owner's
// account did not register is never chosen, and an account whose plan
// sells no fleet memory is never placed on a host repose runs. When that
// term is the only reason nothing fit, the error is ErrNoOwnHost.
//
// A host already at PlacementVCPUPct of its cores in committed guest
// vCPUs is not chosen either (cpuRoomSQL, DECISIONS I-736); when that is
// the only reason nothing fit, the error is ErrNoCPUCapacity.
func PickHost(ctx context.Context, tx pgx.Tx, t Target, class string, holdBytes, volumeBytes int64, now time.Time) (Pick, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", LockPlacement); err != nil {
		return Pick{}, err
	}
	args := t.args(now, class, holdBytes, volumeBytes)
	var p Pick
	err := tx.QueryRow(ctx, pickSQL(cpuRoomSQL("coalesce(r.vcpu_milli, 0)", "$6"), ownedSQL), args...).Scan(&p.HostID, &p.Name, &p.FreeBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The runtime term first (I-1464): an erofs project with no
			// erofs host anywhere is not a capacity problem, and the
			// other answers would name the wrong cause.
			var ignored Pick
			if err := tx.QueryRow(ctx, pickSQLWith(cpuRoomSQL("coalesce(r.vcpu_milli, 0)", "$6"), ownedSQL, "($10::text is not null and $11::text[] is not null)"), args...).Scan(&ignored.HostID, &ignored.Name, &ignored.FreeBytes); err == nil {
				return Pick{}, ErrNoRuntimeHost
			}
			// A named host that cannot take it is refused as that host,
			// whichever term refused (I-1584): the user asked for one
			// box, and the sentence is about that box.
			if t.Host != uuid.Nil {
				return Pick{}, ErrPinnedHost
			}
			// A placement that only the account's own hardware may take
			// is refused by that hardware, whichever term refused it, so
			// it gets its own error and its own sentence (I-1366): the
			// user reads which of their hosts and what it lacks, not
			// "repose has no room" about servers they do not use
			// (I-1586). Asked before the CPU term, which never applies
			// to an owned host.
			if !t.Hosted || !t.Fleet {
				var owns bool
				if err := tx.QueryRow(ctx, `select exists (select 1 from hosts
					 where owner_account_id = $1 and state not in ('retired', 'lost'))`, t.Account).Scan(&owns); err != nil {
					return Pick{}, err
				}
				if owns || !t.Fleet {
					return Pick{}, ErrNoOwnHost
				}
			}
			// Which term refused is the difference between buying memory
			// and buying cores, and the failure path is the only place
			// that can say: one more query on a create that has already
			// failed (I-736). `$6 >= 0` holds for every placement and
			// keeps the parameter referenced, which the extended protocol
			// requires: a parameter the query does not mention is a bind
			// error.
			if err := tx.QueryRow(ctx, pickSQL("$6 >= 0", ownedSQL), args...).Scan(&ignored.HostID, &ignored.Name, &ignored.FreeBytes); err == nil {
				return Pick{}, ErrNoCPUCapacity
			}
			return Pick{}, ErrNoCapacity
		}
		return Pick{}, err
	}
	return p, nil
}

// pickSQL is PickHost's query with cpuRoom as its CPU condition and
// owned as its ownership condition, so the same query can be asked again
// with either satisfied trivially, to find out which one refused.
func pickSQL(cpuRoom, owned string) string { return pickSQLWith(cpuRoom, owned, runtimeSQL) }

// pickSQLWith is pickSQL with its runtime condition named too.
func pickSQLWith(cpuRoom, owned, runtime string) string {
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
		   and ` + owned + `
		   and ` + runtime + `
		   and ` + pinSQL + `
		   and least(h.free_mem_bytes, h.mem_bytes - ` + hostReserveSQL + ` - coalesce(r.reserved_bytes, 0)) >= $3
		 order by free desc, h.name
		 limit 1`
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
}, t Target, class string, holdBytes, volumeBytes int64, now time.Time) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		select exists (
		select 1
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		  join lateral (
		        select coalesce(sum(case when p.state = 'stopping' then `+classRAMSQL+` else 0 end), 0)::bigint as stopping,
		               coalesce(sum(`+classRAMSQL+`), 0)::bigint as going,
		               coalesce(sum(case when p.state = 'stopping' then `+ClassMilliVCPUSQL("p.class")+` else 0 end), 0)::integer as stopping_vcpu_milli
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
		   and `+cpuRoomSQL("(coalesce(r.vcpu_milli, 0) - g.stopping_vcpu_milli)", "$6")+`
		   and `+ownedSQL+`
		   and `+runtimeSQL+`
		   and `+pinSQL+`
		   and least(h.free_mem_bytes + g.going, h.mem_bytes - `+hostReserveSQL+` - coalesce(r.reserved_bytes, 0) + g.stopping) >= $3)`,
		t.args(now, class, holdBytes, volumeBytes)...).Scan(&ok)
	return ok, err
}

// Room is what one host has for one more machine, in placement's own
// terms (DECISIONS I-1586): the memory a machine may still take
// (PickHost's free column) and the disk its thin pool has free against
// what PlacementPoolPct asks of it. A refusal on a host the user owns
// names these, so they read the figure that refused them and not "no
// room".
type Room struct {
	// FreeBytes is the memory placement counts free, never below 0.
	FreeBytes int64
	// PoolFreeBytes and PoolNeedBytes are the thin pool's free bytes and
	// what a new volume needs free there: PlacementPoolPct's headroom
	// plus what the volume holds at first, or the volume's whole size
	// from a hostd that reports no pool size.
	PoolFreeBytes, PoolNeedBytes int64
	// PoolUnavailable is hosts.pool_unavailable (I-644).
	PoolUnavailable bool
}

// OwnedRooms is Room for every host account owns, by host id, computed
// by the same expressions PickHost places with.
func OwnedRooms(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, account uuid.UUID, holdBytes, volumeBytes int64) (map[uuid.UUID]Room, error) {
	rows, err := q.Query(ctx, `
		select h.id,
		       greatest(least(h.free_mem_bytes, h.mem_bytes - `+hostReserveSQL+` - coalesce(r.reserved_bytes, 0)), 0),
		       h.pool_free_bytes,
		       case when h.pool_bytes > 0
		            then h.pool_bytes / 100 * (100 - `+strconv.Itoa(PlacementPoolPct)+`) + $2
		            else $3 end,
		       h.pool_unavailable
		  from hosts h
		  left join host_reservations r on r.host_id = h.id
		 where h.owner_account_id = $1`, account, holdBytes, volumeBytes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]Room{}
	for rows.Next() {
		var id uuid.UUID
		var r Room
		if err := rows.Scan(&id, &r.FreeBytes, &r.PoolFreeBytes, &r.PoolNeedBytes, &r.PoolUnavailable); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}

// classRAMSQL is ClassRAM for a projects row aliased p.
const classRAMSQL = `(case p.class when 'tiny' then 2::bigint<<30 when 'small' then 4::bigint<<30 when 'xl' then 16::bigint<<30 else 8::bigint<<30 end)`

// ClassMilliVCPUSQL is ClassMilliVCPU in SQL, over the class column named
// by col ("p.class" for a projects row aliased p, "class" for an
// unaliased one), in the same thousandths. Every query that divides by or
// sums a class's CPU calls this, so the figure has one SQL home as well
// as one Go home: placement here, the Usage charts in
// internal/api/http/samples.go and the busy-unattended signal in
// internal/api/abuse each held their own copy until I-784, and a copy
// that kept the old 2-GB-per-vCPU numbers would have divided a guest's
// CPU by twice the vCPUs it has. The one copy this cannot reach is the
// host_reservations view (migration 0041);
// TestHostReservationsVCPUsMatchClassVCPU is what holds that to this.
func ClassMilliVCPUSQL(col string) string {
	return `(case ` + col + ` when 'tiny' then 500 when 'small' then 1000 when 'xl' then 4000 else 2000 end)`
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
// `least(...) >= 0` holds for any host. And an empty host takes one
// guest whatever its class, so a host with fewer cores than the class
// asks for -- a staging or dev host -- can still run something, since
// every placement this asks about adds exactly one guest.
//
// A host a user owns is exempt as well (DECISIONS I-1586): its cores
// are the customer's, the ratio above was measured for strangers sharing
// repose's servers, and on an x86 box with few cores it refused a third
// of the memory own-hardware.md promises. Memory and disk are what bound
// an owned host, as hostd's own admission already does.
//
// What this does not do: a project keeps its host for life, so `repose
// start` of a stopped machine never asks placement again, hostd's own
// admission checks memory and the pool and never CPU, and an operator
// placing by hand bypasses this. A host can therefore still be driven
// past the ratio by the guests already on it; HostCPUSaturated (I-738) is
// the backstop that says so.
func cpuRoomSQL(committed, want string) string {
	// Both sides are thousandths of a vCPU (I-1168), so the ratio is
	// `cores * pct * 10` and nothing divides: at 150 percent a core
	// admits 1,500 milli-vCPU. The integer division this replaced
	// truncated, which cost a host up to one whole committed vCPU of
	// room on an odd core count.
	return `(h.cores <= 0
		        or h.owner_account_id is not null
		        or ` + want + ` = 0
		        or ` + committed + ` = 0
		        or ` + committed + ` + ` + want + ` <= h.cores * ` + strconv.Itoa(PlacementVCPUPct*10) + `)`
}
