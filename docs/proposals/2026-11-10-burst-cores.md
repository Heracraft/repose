# Burst cores: automatic by class, metered above an allowance (proposal, 2026-11-10)

**Status: analysis and design. Nothing here is decided and no code is
written.** This covers build items 11 and 12, which the owner chose on
2026-11-04 (`reports/2026-11-04-product-value-council.md`, "Owner
decision: automatic burst by class" and "Decision: metered burst,
overage off by default"). Item 11: a machine uses more cores than its
class sells when the host has idle cores, and the user never asks for
them. Item 12: hostd measures the CPU time above each machine's base
cores, each plan includes some of it, and usage above the allowance is
billed only when the user turns overage on, with a dollar cap. Each part
becomes real only through the `DECISIONS.md` entries in section 11.

Repo facts carry a `file:line` from branch `proposals-2026-11-10` at
`a0acc312`. Prod facts are quoted from the decision or proposal that
measured them. This session had no prod or host access, so every figure
marked *unverified* is either arithmetic on a quoted rate or a claim that
the measurement in section 6 must settle. Nothing in this file was run on
a host.

## Summary

| Question | Answer |
|---|---|
| Ceilings | small 1 base core up to 4, large 2 up to 6, xl 4 up to 8 (owner, 2026-11-04) |
| Mechanism | Boot each guest at its ceiling (`--cpus boot=4`, `6` or `8`). The unit starts with `CPUQuota` at the base, as today. A controller in hostd raises each guest's `cpu.max` toward its ceiling while the host is under a gate, and cuts every guest back to its base in one tick when the host passes the gate |
| Hotplug instead? | No, for the main path. A hotplugged guest still needs the same `cpu.max` gate, needs a guest change to online CPUs, and its restore after a resize is untested. Keep hotplug for one case: shrink a guest to its base for the rest of a period when its burst is used up (decision 5) |
| The gate | Busy CPU on the host at most 0.75 of its physical cores, to be set from the host-02 curve. Measured on prod: 0.5 busy guest vCPUs per core typed at 2.7 ms p50, 1.2 per core at 42 ms p90 (I-738) |
| Placement and seats | Unchanged. `ClassVCPU`, I-736's 1.5 rule and I-739's seat count keep reading base vCPUs. host-02 still sells 24 seats |
| The hard part | `nproc` in the guest says the ceiling at all times. A `make -j$(nproc)` on a small then runs 4 jobs in 4 GB, 1 GB a job where today it is 4 GB. When the gate is shut, 4 vCPUs share 1 core's quota and the guest stalls for up to 75 ms of each 100 ms period |
| Metering | hostd adds up `max(0, used - base x time)` each second per guest and sends it as a new optional `GuestSample` field. The api rolls it into `usage_hours` and counts it per account and period, as egress |
| Allowance and price | Council's 20, 50 and 120 core-hours (Solo, Plus, Pro) and about $0.04 a core-hour are starting points. Measure first. 20 core-hours is 5 hours of a large at its full ceiling |
| Overage | Off by default. At the allowance, burst stops and machines run on their base cores; nothing stops. A user turns overage on, with a cap, on the Billing page. Billed through a second Polar meter beside "Egress overage" |
| Money | Near zero cost: burst sells host time that placement already leaves idle. Revenue is small: one heavy large user pays about $13 a month. The value is how the machine feels and a reason to upgrade |
| Before building | Read-only queries on `meter_samples` and `proc_samples` (section 6.1), then the latency curve on host-02 with throwaway guests (6.2) |

---

## 1. The repo today

**Cloud Hypervisor gets one vCPU count.** `ch.Spec.Args` passes
`"--cpus", fmt.Sprintf("boot=%d", s.VCPUs)` and nothing else
(`internal/hostd/ch/ch.go:123`). `s.VCPUs` comes from `class.VCPUs` in
`create.go:268`. There is no `max=`, so a running guest cannot gain a
vCPU. The `ch.Client` interface has `Pause`, `Resume`, `Info`,
`ResizeDisk` and `Version` (`ch.go:148-157`) and no vCPU resize.

**The classes.** small 1 vCPU 4 GB, large 2 vCPU 8 GB, xl 4 vCPU 16 GB
(`internal/hostd/guest/manager.go:103-105`), 4 GB per vCPU since I-784.

**The cgroup.** `GuestUnitProps` sets `CPUQuota=<vcpus x 100>%` and
`CPUWeight=<vcpus x 50>` (`internal/hostd/guest/unit.go:31-32`,
`CPUWeightPerVCPU = 50` at `manager.go:142`). The weights are 50, 100 and
200 by class (I-737). The props apply only at a unit's start; I-737
rejected changing them on a running guest ("needs a `SetProperty` on the
`Systemd` interface and a reconcile that re-asserts it, and is real scope
for a gain the next start already delivers"). This proposal needs exactly
that scope.

**A guest with more vCPUs than quota has run on prod already.** After
I-784, an `xl` that was never cold-started kept 8 vCPU threads inside a
unit capped at `CPUQuota=400%`, because `ch.RestoreArgs` has no `--cpus`
and the count comes from the saved state (`manager.go:96-102`). I-784
recorded the symptom: "the guest's load average can sit near 8 while four
cores' worth runs". That is the gated state of this design. Nobody
measured typing inside such a guest. *Unverified* whether any such guest
is still running.

**Placement counts class vCPUs.** `scheduler.ClassVCPU`
(`internal/api/scheduler/scheduler.go:49`) feeds the CPU term of
`PickHost` (`scheduler.go:162`) and the `host_reservations` view. The
seat count uses `SeatVCPUs = 2` and `DutyCycle = 0.4`
(`internal/api/waitlist/hostseats.go:48,58`).

**Samples.** Each minute hostd reads the unit's `CPUUsageNSec`
(`internal/hostd/guest/samples.go:256-260`) and sends `cpu_ns_delta`.
It also sends in-guest CPU pressure and the unit's host CPU wait (I-493).
`meter_samples` keeps 90 days and `proc_samples` 30 days
(`internal/db/db.go:152-159`). No sample says how many cores a guest
*wanted*: the quota caps `cpu_ns` at the base, so demand above the base
is invisible today. Section 6.1 works around that.

**The Usage chart clips at 100 percent.** The api divides `cpu_ns` by the
class's vCPUs and wraps it in `least(..., 1)`
(`internal/api/http/samples.go:53`), and returns `vcpus` as the class
figure (`samples.go:116`). A bursting guest would draw a flat 100 percent.

**Host builds share the host.** Tenant `nix build` of a machine's closure
runs on the host in a scope with `CPUQuota=800%` (`internal/hostd/nixbuild/nixbuild.go:325-329`,
R5-4's "8 cores"), at most 2 at once (`manager.go:266`), under
`nix-daemon.service` at `CPUWeight=50`
(`docs/interfaces/host-conventions.md:248`). Two builds can take 16 of
host-02's 32 cores. None of that shows in `meter_samples`.

**Inside the guest.** nix runs `max-jobs = "auto"` and `cores = 0`
(`nix/guest/base/store.nix:351-352`), so a guest's own nix build starts
`nproc` jobs, each told to use every CPU. The SSH sessions and the tmux
server run at `CPUWeight=1000` against a pane's 100
(`nix/guest/base/keystroke-path.nix:14`), which keeps typing first inside
the guest, as long as the guest's vCPUs run at all.

**Billing.** The egress overage is the model to copy. Hourly, within 3
hours of `period_end`, the api records one line in `overage_charges` and
sends Polar one `egress_overage` event with external id
`overage:<subscription>:<period start>` (`internal/billing/overage.go:20-32,165-225`),
priced by `OveragePerGBCents = 5` (`internal/billing/plans.go:76`). Polar
bills it on the renewal order (I-604 (5)). The meter is created by
`repose-admin billing polar-bootstrap` (I-604 (2)). The plans are Solo
$49 (founding $39), Plus $89, Pro $169 with 1, 2 and 4 seats
(`plans.go:45-47`).

**A correction to the brief.** I-1063 is "Egress is what a guest sends the
internet, counted by nft, not by its tap" (commit `48c22c8c`, on branch
`run-sync-fixes`, not yet on this branch). It is the right precedent for
the meter: an optional `GuestSample` field (`net_egress_bytes_delta = 18`),
a new `meter_samples` column (migration 0035) and a rollup that reads it.
The overage charge itself is I-289 and I-604 code.

**What users read today.** `/docs/machine` says "A core is a physical
core, not a hyperthread. `nproc` says the same number"
(`apps/web/src/content/docs/machine.md:16-18`) and "Nothing is capped: on
an idle machine a build gets everything" (`machine.md:239`). Both stop
being true. The agent guide says "Several builds or test runs at once can
keep every core busy" (`nix/guest/base/agent-guide.md:70`). No
notification warns before an allowance runs out: `egress_stopped` is the
only allowance email (`internal/api/notify/notify.go:745`).

## 2. Two mechanisms: boot at the ceiling, or hotplug

Cloud Hypervisor supports `--cpus boot=N,max=M` and `vm.resize` with
`desired_vcpus`, on x86 only. A guest must online added CPUs itself
("`echo 1 | tee /sys/devices/system/cpu/cpu[4,5,6,7]/online`"); removed
CPUs are offlined inside the guest automatically. The doc names no limit
for snapshots or vhost-user, and says nothing either way about them
(`docs/hotplug.md` on main, read 2026-11-10).

| | A. Boot at the ceiling, gate with `cpu.max` | B. Hotplug on demand (`boot=base,max=ceiling`) |
|---|---|---|
| `nproc` | the ceiling, always | the count at the moment a tool starts; a `-j` chosen then stays |
| How fast burst changes | the next CFS period: 100 ms by default, 10 ms with `CPUQuotaPeriodSec=10ms` | an ACPI event, a guest online, then the next period. Seconds, *unverified* |
| Guest change | none | a udev rule in the base to online new CPUs, and every running guest needs a rebuild to get it |
| Host gate | the gate is this mechanism | still needed: added vCPUs can fill the host until hostd removes them, so `cpu.max` stays |
| Host-restart freeze | restore keeps the vCPU count (I-784); the controller sets `cpu.max` again after restore | a restore after a resize is untested |
| Code | `ch.go:123` takes a second number; a controller in hostd | the same controller, plus `vm.resize` in `ch.Client`, a demand detector, and the guest rule |
| hostd dies | caps stay where they were until hostd restarts and resets them | the same, and vCPUs stay added |

**Recommendation: A.** B does not remove the gate, so it adds a second
moving part and a guest change to get a more truthful `nproc` for tools
that start while the count is high, which B then cannot take back. A
gives the owner's goal ("extra cores feel like the machine") with one
kernel knob that a test can read.

**One place where B helps.** When a user's allowance is used up and
overage is off, the gate holds that user's machines at the base for days.
In A, a small then runs 4 vCPUs on 1 core's quota for the rest of the
period, with the costs in section 4. A one-time `vm.resize` down to the
base at that moment, and back up at the next period, gives a true `nproc`
for that stretch. It runs twice a period at most. It needs
section 6.3's check that a resized guest survives a host-restart freeze.
This is decision 5.

## 3. The gate: keeping typing smooth

**What is measured.** On host-01, 2026-10-14, into an idle `small`: 0.5
busy guest vCPUs per physical core typed at 2.7 ms p50; 1.8 per core at
17.0 ms p50 and 44.5 ms p90; the sampler had p90 at 42 ms by 1.2 per core
(I-738, `proposals/2026-10-14-host-cpu-capacity.md` §1). The boundary was
read as busy guest vCPUs about equal to usable cores, 0.9 of them (I-734).
**Nothing between 0.5 and 1.2 per core was measured.** I-784 adds a
second open point: the boundary may follow guest count or vCPU-thread
count, and boot at the ceiling multiplies vCPU threads by 3 to 4. A full
host-02 of 24 `large` guests has 48 vCPU threads today and 144 at the
ceiling; 48 `small` guests have 48 today and 192 at the ceiling. Idle
vCPU threads halt and cost little. That is an assumption until 6.2
measures it.

**The controller.** One loop in hostd, every second:

1. Read the root cgroup's `cpu.stat` `usage_usec` and each `guest@<id>`'s.
   The host figure includes host builds, virtiofsd, WireGuard and hostd,
   which is the quantity the latency depends on.
2. Target `T = g x physical cores`, with `g` from 6.2. The starting
   proposal is `g = 0.75`: on host-02, 24 of 32 cores.
3. Burst now: `b_i = max(0, used_i - base_i)` per guest. Headroom
   `H = T - (host_used - sum of b_i)`.
4. If `H <= 0`, every guest's `cpu.max` goes to its base in this tick.
5. Else the guests that may burst (the api allows it, section 8) and
   used at least 90 percent of their cap last tick share `H` in
   proportion to their I-737 weight. Each cap rises by at most 1 core a
   tick and never passes the ceiling. Every other guest stays at its
   base.
6. Write each changed cap. Count the burst time (section 8).

Cut at once, raise slowly: the failure that matters is a host above the
line, and a one-tick cut bounds it at about 1 second.

**Base cores stay sold.** No cap ever goes below the base. With the gate
shut, the host is where it is today: every guest capped at its base, and
I-734's 1.5 ratio and the I-738 alert as the protection. Burst only fills
time that base use leaves under `T`.

**How to write the cap.** Two ways, to be chosen in stage 1:
`SetUnitProperties` with `runtime=true` and `CPUQuota` over D-Bus, which
systemd keeps; or a direct write of `cpu.max`, which systemd can undo on
a daemon reload. A direct write fails safe: an undo puts the guest back
at its base. Either way, hostd sets every running guest to its base when
it starts, before the first tick, so a restart cannot leave a stale burst
in place for long. *Unverified:* how hostd's own unit restarts after a
crash; a guest keeps a raised cap until then. An alert on the controller
loop stalling (section 13) covers it.

**The CFS period.** With `CPUQuota` at the base and more vCPUs than the
base, a guest whose vCPUs are all busy uses its quota early in each
period and then stops until the next one. At systemd's default 100 ms, a
small at 4 vCPUs and 1 core of quota runs 25 ms and stalls 75 ms. During
the stall nothing in the guest runs, sshd and tmux included, so the
in-guest `CPUWeight=1000` cannot help. A 10 ms period
(`CPUQuotaPeriodSec=10ms`) bounds the stall at 7.5 ms. It costs more
throttle events. `cpu.max.burst` can let a guest save unused quota for a
short spike such as a keystroke. 6.2 measures the three.

**Today's throttling as a baseline.** On 2026-10-14 the busiest guest had
`nr_throttled` 298 of 3,082,823 periods (0.01 percent), because vCPUs
equalled the quota. In this design throttling is how the gate works, so
`nr_throttled` rises by design and stops being a sign of trouble.

## 4. Inside the guest: more vCPUs than its share

**`nproc` says the ceiling.** It reads the CPUs the guest kernel has
online. Nothing inside the guest can see the host's `cpu.max`, so
`os.availableParallelism()` in Node, `GOMAXPROCS` in Go, `make
-j$(nproc)`, cargo, ninja, jest's workers, `pytest -n auto` and the
guest's nix `max-jobs = auto` all start ceiling-many workers. Go's newer
cgroup-aware `GOMAXPROCS` reads the guest's own cgroups, which carry no
limit.

**When the gate is open,** that is what the user wants: a build on a
`large` runs 6 jobs on 6 cores.

**When the gate is shut,** throughput stays about the same, because the
same CPU time is shared by more threads. Three things get worse:

1. **Memory per job.** This is the largest risk and the reason I-784
   chose 4 GB per vCPU.

   | Class | Memory | Today: GB per `nproc` | At the ceiling | Ran on prod before? |
   |---|---|---|---|---|
   | small | 4 GB | 4.0 | 1.0 | no |
   | large | 8 GB | 4.0 | 1.33 | no (pre-I-784 large was 4 vCPU, 2.0) |
   | xl | 16 GB | 4.0 | 2.0 | yes, pre-I-784 xl was 8 vCPU in 16 GB |

   A C++ or Rust link takes 1 to 2 GB a job. At 1 GB a job a small
   reaches the in-guest OOM killer, which ranks by size and protects
   tmux, sshd and dev's user manager (`keystroke-path.nix`), so it kills
   a compiler or an agent. This happens whether the gate is open or
   shut: memory does not burst.
2. **Stalls.** Section 3: up to 75 ms of each 100 ms period, inside the
   guest that is bursting, its own typing included. Neighbours are not
   affected.
3. **Lock-holder preemption.** When a vCPU that holds a guest kernel
   lock is throttled, the others spin on it. host-01 runs nested with
   `ple_gap=0` (`2026-10-14-host-cpu-capacity.md` §2), so KVM cannot see
   the spinning and stop it. Paravirtual spinlocks avoid most of this,
   if the guest gets `KVM_FEATURE_PV_UNHALT` from Cloud Hypervisor.
   *Unverified*; `dmesg | grep -i spinlock` in a guest answers it.

**What the guest sees.** `top` shows steal time while the gate is shut,
and load average near the ceiling. *Unverified:* whether time throttled
by `cpu.max` shows as steal in the guest and as `host_cpu_wait` in the
unit's `cpu.pressure`. The Usage chart's "Waiting for the server" line
then explains a slow minute, which is its job (`machine.md:233`).

**What the design does about it.**

- **Say the truth where an agent can read it.** guestd writes
  `/run/repose/cores` with three numbers: base, ceiling, cores it may use
  now (from the last cap hostd sent it). The agent guide names the file.
  An agent that plans `-j` can read it; a tool that reads `nproc` cannot,
  which the docs say.
- **Change the guest's own nix.** `max-jobs = auto` with `cores = 0`
  lets a small start 4 jobs of 4 threads. Proposed: `max-jobs = 2`,
  `cores = 0`, so nix uses every core in fewer, larger jobs.
- **Keep the small ceiling an open question.** 4 vCPUs in 4 GB is a new
  shape for repose. Decision 2 asks whether small bursts at once.
- **Use hotplug for the long shut state** (section 2, decision 5).

## 5. Interactions

**The idle freeze (I-818 to I-828).** A frozen guest's vCPUs are paused,
so it uses no CPU and bursts nothing, and the cores it frees go to other
guests' burst. The rest rule reads `cpu_ns` under 6 s a minute (I-818);
an idle guest uses well under its base, so burst does not change which
guests rest. One open point: more vCPUs can raise an idle guest's own CPU
through KVM halt polling, measured at up to 2.4 percent of one CPU per VM
(`2026-10-14-host-cpu-capacity.md` §1). If that grows with vCPU count, an
idle guest moves toward the 6 s line and rests less. 6.2 measures it.

**The host-restart freeze (I-652, I-653, I-686).** The image holds the
vCPU count, and restore keeps it (I-784). The restored unit gets
`GuestUnitProps` again, so its quota is the base until the controller's
first tick raises it, which is the safe order. The image grows by the
state of the extra vCPUs, a few KB each, which is nothing against the
96 GB park budget in I-1040. A guest booted before burst is switched on
keeps its base vCPU count across any number of host restarts and gains
the ceiling only at its next cold start. A rollback is the same in
reverse: guests keep the ceiling and run at their base quota, the I-784
`xl` state.

**The balloon (I-1040).** Free page reporting does not depend on vCPU
count. Each guest CPU holds per-CPU page lists and slab caches, so a
guest at 8 vCPUs keeps a few MB more of its memory busy. *Unverified*,
small, and inside the guest's own memory.

**Host builds (R5-4).** The 8-core cap applies to the host's builds of
guest closures. Builds inside a guest are outside it. Two host builds can take 16
of host-02's 32 cores. The controller counts them through the root
cgroup, so burst closes during a fleet-wide `machine.nix` rebuild. That
is correct: the host is busy. Inside a guest, a build gets the guest's
ceiling.

**Placement and seats (I-734, I-736, I-739, I-784).** No change.
`ClassVCPU` stays the base and remains the only number placement reads.
The ceiling is a hostd figure next to `class.VCPUs`. If placement counted
the ceiling, host-02 would take 48 / 6 = 8 `large` guests instead of 24.

**The CPU alert (I-738).** `HostCPUSaturated` fires at busy guest vCPUs
above 1 per core for 15 minutes. With the gate at 0.75, burst alone can
never trip it, so a firing alert means base use did, or the controller
failed. The alert gains a meaning; its rule does not change.

## 6. Measuring before building

> **Measured 2026-11-11 on host-stg-01**: `docs/research/burst-staging-2026-11-11.md`. The arithmetic holds; typing lag at the gate stayed under 0.5 ms p50 / 1.4 ms p90 in-guest; the base share held at 95 % under contention. The gate value is still uncompared with alternatives.


### 6.1 Read-only, from `meter_samples` and `proc_samples`

Each query runs in `begin read only; ... rollback;`, as in
`2026-10-11-pause-idle-machines.md`. Use samples from 2026-10-25 on, so
guests that kept pre-I-784 vCPU counts drop out of most of the window.

Burst demand cannot be read directly, because the quota caps `cpu_ns` at
the base. Two bounds bracket it:

- **Upper:** every minute where a guest ran at 90 percent of its base or
  more would have used its whole ceiling.
- **Lower:** in such a minute, at least one more thread waited for the
  share of the minute that the guest's own CPU pressure (I-493) says
  something waited. That is at least one extra core for that time.

```sql
begin read only;
with s as (
  select m.project_id, p.user_id, m.ts, m.cpu_ns, m.cpu_pressure_us, m.mem_used,
         case m.class when 'small' then 1 when 'large' then 2 when 'xl' then 4 end as base,
         case m.class when 'small' then 4 when 'large' then 6 when 'xl' then 8 end as ceil
  from meter_samples m join projects p on p.id = m.project_id
  where m.ts >= '2026-10-25' and m.state = 'running' and not coalesce(m.resting, false)
)
select user_id,
       count(*)                                                     as running_min,
       count(*) filter (where cpu_ns >= 0.9 * base * 60e9)          as at_base_min,
       sum((ceil - base) / 60.0) filter (where cpu_ns >= 0.9 * base * 60e9) as burst_core_h_upper,
       sum(least(ceil - base, 1) * least(cpu_pressure_us / 60e6, 1) / 60.0)
           filter (where cpu_ns >= 0.9 * base * 60e9)               as burst_core_h_lower
from s group by user_id order by burst_core_h_upper desc;
rollback;
```

Scale each account to 30 days. The allowance should cover most accounts
at the lower bound and the median account at the upper one; the owner
sets the percentile (decision 3).

Three more:

1. **Supply.** Per host and minute, `sum(cpu_ns) / 60e9` against
   `hosts.cores x 0.75`, by hour of day. This gives the core-hours the
   gate would have offered and the share of minutes it would have been
   shut. It misses host builds and host overhead, so it overstates
   supply; `hosts.load1` gives a rough check.
2. **What burst would run.** `proc_samples` (30 days) joined to the
   at-base minutes: the top `comm` values by CPU. Build tools (`cc1plus`,
   `rustc`, `ld`, `node`, `go`, `esbuild`) argue for burst; a single
   `.claude-wrapped` or `chromium` near its base argues that the minute
   was one busy thread, which burst does not speed up.
3. **Memory at the cap.** The distribution of `mem_used / class memory`
   in at-base minutes. Many minutes above 0.6 on smalls mean the ceiling
   of 4 will OOM (section 4).

The earlier sample was 3 accounts (`2026-10-11-pause-idle-machines.md`
§5). If the window still holds that few, treat every figure as a first
look and set the allowance from stage 2's real burst data instead.

### 6.2 The latency curve on host-02, with throwaway guests

The owner named this as the release gate. Method: the 2026-10-14 harness
(`2026-10-14-host-cpu-capacity.md` §9), run from woker-1 through the
gateway into an idle throwaway `small`. Load comes from throwaway `xl`
guests running `stress-ng --cpu N`. No tenant guest is touched.

| Step | Code needed | What it gives |
|---|---|---|
| a. p50, p90, p99 keystroke echo, session open and ICMP at 0.5, 0.6, 0.75, 0.9 and 1.0 busy guest vCPUs per physical core | none: today's guests at base | the missing part of the curve, and `g` |
| b. the same at the chosen `g`, with the load from guests booted at their ceiling | stage 1 build | whether 3 to 4 times the vCPU threads moves the curve (I-784's open point) |
| c. inside one small at 4 vCPUs and 1 core of quota running `make -j4`: its own keystroke echo at a period of 100 ms, 20 ms and 10 ms, with and without `cpu.max.burst` | stage 1 build | the stall cost, and the period to ship |
| d. an idle guest's `cpu_ns` a minute at base vCPUs and at the ceiling | stage 1 build | whether halt polling grows with vCPUs (section 5) |
| e. `dmesg` for paravirtual spinlocks; steal in `/proc/stat` and the unit's `cpu.pressure` while throttled | stage 1 build | section 4 item 3, and what the charts will show |
| f. a timed build (a Rust crate, a large `tsc`) on a large at base, at the ceiling on a quiet host, and with the gate shut | stage 1 build | the speed-up users get, for the docs and the landing |

Steps b to f run first on host-stg-01, then on host-02 behind its host
flag with only throwaway projects allowed to burst (stage 1).

### 6.3 Hotplug probe, host-stg-01

Only if the owner wants option B for the used-up state (decision 5):
boot a guest at `boot=4,max=4`, `vm.resize` to 1 and back to 4, check
that removed CPUs go offline and added ones need the online rule, then
park and restore it across a host restart with 1 vCPU online. A failure
on the restore keeps option B out.

## 7. Seats, cost and revenue

**Seats.** None change: placement and I-739 read base vCPUs. host-02
sells 24 (I-786).

**Supply on host-02.** 32 cores x 730 hours = 23,360 core-hours a month;
under a 0.75 gate, 17,520. What base use leaves is the burst supply, and
nobody has measured it on host-02:

| Base use assumed | Source | Burst supply a month |
|---|---|---|
| 24 large x 2 vCPU x 0.33 = 15.8 cores | the fleet's 33 percent "busy" (I-734) read as CPU at the cap, which overstates it | (24 - 15.8) x 730 = 5,986 core-hours |
| 24 x 0.05 = 1.2 cores | I-784: "mean consumption around 0.05 cores" a guest | (24 - 1.2) x 730 = 16,644 core-hours |

Daytime peaks cut both. 6.1 item 1 replaces this table.

**Allowance against supply.** A full host-02 holds 24 seats. All Solo:
24 x 20 = 480 core-hours. All Pro: 6 accounts x 120 = 720. That is 3 to
12 percent of the supply above, so the allowance is not what limits
burst; the gate is.

**What 20 core-hours buys.** A large at its ceiling bursts 4 cores, so 5
hours a month. A 10-minute build at full burst each working day is 4 x
1/6 x 22 = 14.7 core-hours. A user who runs four agents type-checking all
afternoon passes 20 in the first week. Per seat, the council's numbers
are 20 on Solo, 25 on Plus and 30 on Pro, so Plus and Pro get a small
edge per seat.

**Cost.** Marginal cost is near $0: these cores are paid for and idle,
and placement never sells them. List cost per core-hour: a `D64s_v7` is
$3,091 a month / 32 cores / 730 hours = **$0.132**; a Hetzner AX162-2 is
$992.10 / 48 / 730 = **$0.028**
(`2026-10-14-host-cpu-capacity.md` §3, `docs/PRICING.md` "The cost
floor"). At list price a fully used allowance would be $2.65 (Solo), $6.62
(Plus) and $15.88 (Pro) on Azure, and $0.57, $1.42 and $3.40 on Hetzner.

**Price.** $0.04 a core-hour is above the Hetzner list cost and below
Azure's. On Azure the hosts run on credit (I-734 context, PRICING.md), so
the price only has to beat Hetzner.

**Revenue, worked.** The report's example ("6 extra cores for 4 hours a
day for 22 days come to about $21") does not fit one machine: no class
bursts 6 extra cores (small 3, large 4, xl 4). One large at full burst
4 hours a day for 22 days is 352 core-hours; less 20 included, 332 x
$0.04 = **$13.28**. Polar's 5 percent takes $0.66; its 50 cents is
charged once per order, which the subscription already pays. If one in
ten seats turns overage on at that use, host-02 earns about 2.4 x $13 =
**$32 a month** against its $3,379 (I-786). Burst pays for its meter. It
will not move the cost floor. Its value is the feel of the machine, and a
reason to choose Plus or Pro, which the owner's goal (reach, recognition)
weighs above margin.

## 8. Metering and billing (item 12)

**Count the time above the base each second.** The controller already
reads each guest's `usage_usec` each second. It adds
`max(0, delta_used - base x delta_t)` to a per-guest counter and sends the
minute's total as `GuestSample.burst_cpu_ns_delta`, an optional field
(the next free number; 18 is `net_egress_bytes_delta` on `run-sync-fixes`).
Bound it like `maxPressureDelta` (`samples.go`): at most `(ceiling -
base) x 66 s`. Minute averages of `cpu_ns` would undercount: a guest that
uses 3 cores for 30 s and none for 30 s averages 1.5, so a small would be
billed 0.5 core-minutes for 1 core-minute of burst. Per-second counting
is the true figure and costs nothing extra (decision 6).

**The api.** A migration adds `meter_samples.burst_cpu_ns` (null from an
older hostd) and `usage_hours.burst_cpu_ns`; `Rollup.Hour` sums it beside
`net_tx` (`internal/billing/rollup.go:89`). `PeriodBurst` mirrors
`PeriodEgress` (`internal/billing/usage.go:70`), per account, over every
project. Like egress, the total can trail by up to an hour.

**The allowance and the cap.** A plan field `BurstCoreHours` (20, 50, 120,
pending decision 3) beside `EgressGB` in `plans.go`, and a constant
`BurstPerCoreHourCents = 4`. An account column `burst_cap_cents`: null is
overage off, a value is overage on with that cap. The burst limit for a
period is the allowance with overage off, and `allowance + cap / price`
with it on.

**Stopping burst.** A tick (the overage job's hour, or every 15 minutes
for less overrun) compares `PeriodBurst` with the limit. Past it, the api
sends each of the account's running guests a new `SetBurst` command with
`allowed = false`; at a period's start, or when the user raises the cap,
it sends `allowed = true`. hostd keeps the flag in bbolt, sends it in
`Hello` for reconcile, and the controller holds such a guest at its base.
A guest created or started while the flag is false starts with it false.
This is a state the api sets, so a resend is harmless and the command id
rule holds. Machines are never stopped for burst: there is no 4x stop as
egress has, because burst past the limit cannot happen.

**The charge.** A second meter, "Burst overage", events named
`burst_overage` summing `metadata.core_hours`, and a metered price of
4 cents a unit on each plan product, made by `polar-bootstrap`. Within 3
hours of `period_end` the overage job sends one event of the period's
whole core-hours over the allowance, rounded up, capped by the cap, with
external id `burst:<subscription>:<period start>`. A new table
`burst_charges` keeps the egress table and its path untouched. Account
deletion sends it first, as it does egress (I-604 (5)).

**Unverified on Polar:** whether a metered price added to an existing
product reaches subscriptions that started before it. If it does not,
every current subscription needs a product update before its overage can
bill. `TestPolarSandbox` on the sandbox organization answers it before
stage 4.

## 9. What users read

The owner's condition: users must be told clearly that they can turn
overage on. One word for the sold cores everywhere: **base cores**, the
owner's word. "Burst" for the rest. A second word for either is how a
grep misses half the uses (CLAUDE.md, "Names are fixed").

| Place | Text (report's text, refined) |
|---|---|
| `repose status`, a row for a running machine | `cores      up to 6 now (2 base)`; with the gate shut, `cores      2 base, the server is busy`; with burst used up, `cores      2 base, burst used up until 1 December` |
| `repose status` and `repose ls`, the plan line | appends `, 14.2 of 20 burst core-hours, overage off`. With overage on: `, 26.0 burst core-hours, 6.0 over: $0.24 of your $10 cap` |
| plan warning, overage off, used up | `burst used up: machines run on their base cores until 1 December; turn on burst overage at https://repose.herakraft.co/billing` |
| notification at 80 percent, once a period | Subject `Your machines used 80% of their burst core-hours`. `Your machines used 16 of this period's 20 burst core-hours. When the rest are used, they run on their base cores until 1 December. To keep bursting past 20, turn on burst overage with a monthly cap at https://repose.herakraft.co/billing.` |
| notification at 100 percent, once a period | Subject `Your machines' burst core-hours are used up`. `Your machines used this period's 20 burst core-hours. Until 1 December they run on their base cores: 1 on a small, 2 on a large, 4 on an xl. Builds and agents keep running. To burst again now, turn on burst overage with a monthly cap at https://repose.herakraft.co/billing.` |
| notification at the cap, overage on, once a period | Subject `Your machines reached your burst cap`. `Your machines used $10 of burst overage, your cap. Until 1 December they run on their base cores. You can raise the cap at https://repose.herakraft.co/billing.` (new; decision 9) |
| Billing page | A Burst row beside Egress: a bar `14.2 of 20 core-hours`, an Overage switch (off), and a cap field the switch requires, with one line: `$0.04 a core-hour past 20, up to your cap.` |
| Agent guide, under "Memory and disk" | `- This machine has base cores and may use more when the server has idle ones; nproc counts the most it may use. /run/repose/cores holds its base, its most and what it may use now. When the plan's burst core-hours are used up, it has its base cores until the period ends, and the user can turn on burst overage on the Billing page.` |
| `/docs/machine` | the size table gains a column: small 1 base core up to 4, large 2 up to 6, xl 4 up to 8. The `nproc` sentence becomes: `nproc says the most the machine may use. /run/repose/cores says what it may use now.` The "Nothing is capped" sentence goes |
| `/docs/limits` | a Cores section: base cores are what a size buys; burst is best-effort, only while the server has idle cores, and never slows other machines' typing |
| `/docs/billing` | a Burst bullet in "What a plan means": what is included, $0.04 a core-hour, off by default, the cap, and that the total can trail by up to an hour |

Each line either explains a slow build or prevents a charge the user did
not choose, so each passes "Trust the reader" (I-485). The 80 and 100
percent emails are the first allowance warnings repose sends; egress has
only its stop email.

## 10. Contract, schema and docs changes

Each ships in the commit that changes the code, and old shapes stay
accepted one release (CLAUDE.md).

| File | Change |
|---|---|
| `docs/interfaces/grpc-hostd.md`, `proto/repose/hostd/v1/hostd.proto` | `Hello.features` names `burst`; `Command.set_burst = 26`, `SetBurst {guest_id, allowed}`; `GuestSample.burst_cpu_ns_delta` and `burst_cap_millicores` (optional); `GuestStatus.burst_allowed` for reconcile |
| `docs/interfaces/host-conventions.md` | §"Cloud Hypervisor invocation": `--cpus boot=<ceiling>`; `CPUQuota` stays the base at start, set at run time by the controller; `CPUQuotaPeriodSec` if 6.2 picks one; the gate in the CPU paragraph at line 248 |
| `docs/interfaces/guest-conventions.md` | `/run/repose/cores`; nix `max-jobs = 2` |
| `docs/interfaces/vsock-guestd.md` | how hostd tells guestd the cap for that file (a field on an existing call, or a new one) |
| `docs/interfaces/db-schema.md` | `meter_samples.burst_cpu_ns`, `usage_hours.burst_cpu_ns`, `users.burst_cap_cents`, `burst_charges`, `subscriptions.burst_warned_for` and `burst_used_for` (once-a-period markers, as `overage_charged_for`) |
| `docs/interfaces/api.md` | `GET /billing` `usage.burst_core_hours`, `burst_included_core_hours`, `burst_cap_cents`, `burst_cents`; `plans[].burst_core_hours`; `PUT /billing/burst {cap_cents|null}`; the project's `cores {base, ceiling, now}` |
| `internal/api/http/samples.go` | CPU divided by the ceiling, or a second series for burst; `vcpus` kept, `ceiling` added |
| `docs/PRICING.md`, `docs/features/pricing.md` | the allowance, price, default and cap; "What is deliberately not priced" keeps builds |
| `docs/DESIGN.md` §5 | the class table gains the ceiling |
| `docs/features/status-and-logs.md`, `docs/features/notifications.md` | the cores row, the plan line, the three new kinds |
| `apps/web/src/content/docs/` | `billing.md`, `limits.md`, `machine.md`, `notifications.md`, and `cli.md` if the status output is listed there |
| `nix/guest/base/agent-guide.md` | the line in section 9 |

## 11. DECISIONS entries needed

No ids are reserved here. Each entry names what it amends:

1. **Burst cores: boot at the ceiling, gated by `cpu.max`.** Amends R3-2
   and I-784 (each class gains a ceiling; `nproc` stops equal to the
   class's cores, and `machine.md` says so), I-737 (the quota is set on a
   running guest; I-737 rejected that), and records the gate fraction
   and the CFS period from 6.2.
2. **Placement and seats count base vCPUs only.** Confirms I-734, I-736
   and I-739 unchanged in arithmetic, and amends I-734's one-line promise
   ("a machine gets its class's vCPUs") to "a machine gets its class's
   base cores, and more when the host has idle ones". Notes I-738's
   changed meaning.
3. **Burst is metered above a plan allowance, with overage off by
   default.** Amends I-289 and I-604 (what a plan sells, a second Polar
   meter), adds a row to PRICING.md, and follows I-1063's shape for the
   meter field and column.
4. **The burst emails.** Amends I-291's set of account emails.
5. **Burst and the freezes.** Records section 5: a restore keeps the
   vCPU count (I-652, I-686, I-784), a frozen guest bursts nothing
   (I-818), and R5-4's 8 cores are host builds, which shut the gate.
6. If decision 5 is taken: **a used-up guest shrinks to its base with
   `vm.resize`**, after 6.3.

## 12. Staged rollout

| Stage | What | Where | Gate to the next |
|---|---|---|---|
| 0 | 6.1 queries; 6.2 step a | read-only; throwaway guests on host-02 | the curve gives `g`; owner answers decisions 1 and 2 |
| 1 | ceilings and the controller, no metering. Host flag `repose.host.burst` (`--burst`, as `pauseIdle` at `nix/hosts/hostd.nix:27`) puts `burst` in `Hello.features`; the api sends `SetBurst allowed=true` only to projects an operator lists (`repose-admin projects burst`) | host-stg-01, then host-02 with throwaway projects and the owner's own | 6.2 steps b to f pass; `HostCPUSaturated` silent for a week with burst on |
| 2 | metering recorded, shown nowhere, nothing enforced; every project on host-02 allowed | host-02 | two weeks of real `burst_cpu_ns`; allowance and price set from it in entry 3 |
| 3 | public: docs, status rows, Billing row, the allowance with overage off, the 80 and 100 percent emails | all hosts | the docs and the CLI agree (`docs_test.go`); one period runs with no charge |
| 4 | the overage switch, the cap and the Polar meter | Polar sandbox (I-604 (8)), then live | `TestPolarSandbox` bills a burst line on an existing subscription |

**Rollback** at any stage: turn the host flag off. The controller holds
every guest at its base; guests keep the ceiling vCPU count until their
next cold start (section 5).

**Which guests get the ceiling.** A guest boots at the ceiling only at a
cold start on a host with the flag. Running guests change at their next
stop and start; nothing restarts them, as with I-737 and I-784.

## 13. Tests and closing evidence

| Item | Evidence that closes it |
|---|---|
| CH argv | `ch.Spec.Args` golden shows `boot=6` for a large on a burst host and `boot=2` without the flag |
| Unit props | `unit.golden` still shows `CPUQuota` at the base |
| Controller | fake-cgroup tests: every cap at base in one tick when host use passes `T`; never below base, never above the ceiling; raise at most 1 core a tick; all caps reset at hostd start; a guest with `allowed = false` never rises |
| Live gate | on host-02: `cat /sys/fs/cgroup/guests.slice/guest@<id>.service/cpu.max` at base with the host busy and raised with it quiet, and the keystroke p90 table at the chosen `g`, pasted |
| `nproc` | `nproc` and `cat /run/repose/cores` in a cold-started large print 6 and `2 6 <now>` |
| Meter | a throwaway large running `stress-ng --cpu 6` for 10 minutes on a quiet host records 4 x 10 / 60 = 0.67 core-hours, within 5 percent, in `usage_hours` |
| Bound | a `GuestSample` with a burst delta above the bound is clamped (test) |
| Allowance | api test: past the limit, `SetBurst allowed=false` goes to each running guest of the account, once; at the next period, `allowed=true` |
| Emails | each kind sends once a period (test with two ticks), and the HTML and text twins render (`internal/api/notify/testdata`) |
| Charge | `burst_charges` row and `burst_overage` event with the external id; a resend changes nothing; the sandbox renewal order shows the line |
| CLI | `status_view_test.go` cases for the three cores rows and the plan line; `docs_test.go` passes |
| Chart | the Usage chart draws a bursting large above its base without clipping |
| Alert | a new rule on the controller loop stalling, with a promtool case in `ops/alerts_test.yaml` |
| Checklist | the greps in `docs/CHECKLIST.md`, output pasted |

The guest NixOS VM tests do not boot on kanali (auto-memory "Testing on
kanali"); the guest base change for `/run/repose/cores` runs on
host-stg-01.

## 14. Risks

- **Typing lag returns** if `g` is set too high, if the controller
  stalls with caps raised, or if 3 to 4 times the vCPU threads moves the
  boundary (I-784's open point). 6.2 b and the alert answer the first and
  third; the stall alert the second.
- **OOM inside smalls and larges.** 1 GB and 1.33 GB per `nproc` are
  shapes repose never ran. Memory does not burst.
- **The burster's own stalls** at the gate: up to 75 ms of each 100 ms
  at the default period, and lock-holder spinning under nested
  virtualization with `ple_gap=0`.
- **Users read the ceiling as a promise.** A build that ran in 3 minutes
  at 9 in the morning takes 8 at noon. The docs say best-effort, and the
  status row says why.
- **A used-up allowance with overage off** leaves a small at 4 vCPUs on 1
  core for days, with every risk above and no burst to show for it
  (decision 5).
- **Demand is unseen.** The quota hides burst demand today, so the
  allowance and the price come from bounds until stage 2.
- **Polar** may not add a new metered price to existing subscriptions.
- **Abuse.** A miner gets up to 4 times the cores. The allowance limits
  it with overage off; with overage on, the cap does. I-239's miner stop
  is unchanged.
- **Charging for host overhead.** The unit's CPU time includes Cloud
  Hypervisor's own threads, so their time above the base is billed as
  burst. It is small; the docs should not call burst "your programs'"
  time.
- **Revenue is small**, about $32 a month a host at the assumed opt-in.
  The case for building rests on how the machine feels.

## Decisions for the owner, in order

1. **The gate.** Burst only while host CPU stays under `g` of physical
   cores. Proposed `g = 0.75`, set from 6.2's curve.
2. **Smalls.** Do smalls burst to 4 in the same stage as the others, or
   later? Recommended: the same stage, after 6.2 passes, and with 6.1
   item 3 showing smalls rarely sit above 60 percent memory while busy.
   The report left this open.
3. **The allowance.** 20, 50 and 120 core-hours, or the numbers stage 2
   measures; and which share of accounts the allowance should cover.
4. **The price and the cap.** $0.04 a core-hour; whether the cap field
   offers a starting value or a minimum.
5. **The used-up state.** Keep the ceiling vCPUs throttled to the base
   until the period ends (simple), or shrink the guest to its base with
   `vm.resize` and grow it back at the next period (true `nproc`, needs
   6.3).
6. **Billing granularity.** Count time above the base each second in
   hostd (recommended), or from minute averages, which undercount in the
   user's favour.
7. **Public wording.** "up to" cores in the size table, and whether the
   pricing table and the landing show a burst column.
8. **Trial and founding plans.** Recommended: the free week and Solo's
   founding price get the same allowance, and the overage switch opens
   after the first payment.
9. **A third email at the cap** with overage on, which the report did
   not list.
10. **The guest's own nix:** `max-jobs = 2`, `cores = 0` in place of
    `auto`.

## Sources

- `reports/2026-11-04-product-value-council.md`, "Cores: the power host
  question" to "Decision: metered burst, overage off by default", and the
  four members' Cores answers in the appendix.
- Owner decisions of 2026-11-04, as recorded in the report and the
  project memory note `burst-cores-automatic`.
- `docs/proposals/2026-10-14-host-cpu-capacity.md` §1 to §3 and §9:
  latency figures, `ple_gap=0`, halt polling, throttling counts, the
  harness, SKU prices.
- `docs/proposals/2026-10-11-pause-idle-machines.md` §5: the query
  method and the 3-account sample.
- DECISIONS R3-2, R5-4, I-289, I-291, I-485, I-493, I-604, I-652, I-653,
  I-686, I-734, I-735, I-736, I-737, I-738, I-739, I-784, I-786, I-818,
  I-820, I-824, I-826, I-828, I-1040; I-1063 on branch `run-sync-fixes`
  (commit `48c22c8c`).
- Cloud Hypervisor CPU hotplug:
  https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/hotplug.md,
  read 2026-11-10.
- Code: `internal/hostd/ch/ch.go`, `internal/hostd/guest/{unit,manager,samples,create}.go`,
  `internal/hostd/nixbuild/nixbuild.go`, `internal/api/scheduler/scheduler.go`,
  `internal/api/waitlist/hostseats.go`, `internal/api/http/samples.go`,
  `internal/billing/{plans,overage,rollup,usage}.go`, `internal/cli/status.go`,
  `internal/api/notify/notify.go`, `internal/db/db.go`,
  `nix/guest/base/{store,keystroke-path,agent-guide}.nix|md`,
  `nix/hosts/{host-02,hostd,prod}.nix`, `proto/repose/hostd/v1/hostd.proto`.
- Public text: `apps/web/src/content/docs/{machine,billing,limits}.md`;
  `docs/interfaces/{api,grpc-hostd,host-conventions}.md`; `docs/PRICING.md`.
