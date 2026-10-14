# Host CPU capacity, typing latency and what the credits can carry (proposal, 2026-10-14)

**Status: measurement and analysis. Nothing here is decided and no code is
written.** The owner reported that typing into any repose guest has
palpable latency and that attaches take a while. This file records what
was measured on prod on 2026-10-14, the root cause, the capacity model it
corrects, what Azure can actually sell us, and what the Azure credits can
carry. Each step becomes real only through a `DECISIONS.md` entry.

Prod figures come from read-only measurement on host-01, the edge and
four throwaway `small` guests created and destroyed for the purpose
(`lat-probe`, `lat2`..`lat5`). No existing guest was modified. Azure
figures come from `az vm list-skus`, `az vm list-usage`, the consumption
and credits REST APIs and the public retail price API, all read on
2026-10-14. Duty-cycle figures are reused from
[2026-10-11-pause-idle-machines.md](2026-10-11-pause-idle-machines.md) §5.

## Summary

| Question | Answer |
|---|---|
| Why is typing laggy | The host's CPU is saturated. One tenant's agent at full tilt makes typing slow for every guest on the host, including guests doing nothing |
| Is it the gateway or the network | No. The edge was 99% idle throughout and answered in 0.63 ms p50 while a guest answered in 17 ms p50 |
| How bad | Keystroke p50 2.7 ms → 17.0 ms, p90 4.3 ms → 44.5 ms; session open 0.2 s → 2.0 s; `repose status` 4.1 s; a new guest's first boot fails outright |
| Root cause in code | Placement has no CPU term at all. `scheduler.PickHost` filters on memory, pool and slots; `ClassVCPU` is only used for a display field |
| Root cause in the model | R3-2's "CPU 2:1 oversubscribed" is computed against Azure's vCPU count, which is SMT threads. Against physical cores a full `D64s_v7` is 3.75:1 |
| Seats per launch host | Not 30. About 11 at a 40% duty cycle, 14 at 30%, on the shape we should buy |
| Shape to buy | `D<n>ls_v7` (4 GB per core), not `D<n>s_v7` (8 GB per core). Same delivered seats, 12% cheaper, nothing stranded |
| Blocking today | `Total Regional vCPUs` in East US is 65 with 40 used. The documented launch host (64 vCPU) cannot be created |
| What the credits carry | $8,542 estimated. 2× `D128ls_v7` is 27 days of runway. A `D64ls_v7` is 2.7 months, and is also what the $150k milestone needs |
| Is it monitored | No. `ops/alerts.yaml` has `HostMemory80` and no host CPU alert, though hostd already reports `load1` and per-guest CPU pressure (I-493) |

---

## 1. What was measured

host-01 is a `Standard_D16s_v7`: 16 Azure vCPUs, which `lscpu` reports as
**8 physical cores, 2 threads per core**, 64 GB. On it at the time:
`kanali` (xl, 8 vCPU), `toolbar` (xl, 8 vCPU, a Claude agent working) and
`s2-sync-fast-r2` (large, 4 vCPU) — 20 guest vCPUs on 8 cores.

Everything below was measured into **`lat2`, a `small` guest with nothing
running inside it**, over the real gateway path from woker-1 (same Azure
region as the edge, so this is the infrastructure component of latency
with no internet hop).

| | host quiet (load ~4, 7% guest time) | host busy (load ~16, 75–85% guest time) |
|---|---|---|
| keystroke echo p50 | 2.74 ms | **16.96 ms** |
| keystroke echo p90 | 4.26 ms | **44.54 ms** |
| keystroke echo p99 | 41.6 ms | 59.1 ms |
| ssh session open, no multiplexing | 0.19–0.22 s | **1.55–2.00 s** |
| host→guest ICMP avg / max | 1.35 / 11.4 ms | **15.4 / 56.0 ms** |
| `repose status` | — | 2.85–4.05 s |
| new `small` guest first boot | 14 s, ready in 17 s | **failed twice**, 74 s each |

The guest was idle in every column. The difference is entirely other
tenants' load on the same host.

The sampler caught the curve as load rose (each row one sample, 20 s apart):

| host load | guest time | keystroke p50 | keystroke p90 | session open |
|---|---|---|---|---|
| 4.1 | 52% | 23.9 ms | 46.3 ms | 1.50 s |
| 9.6 | 74% | 8.2 ms | 42.4 ms | 1.61 s |
| 11.0 | 77% | 15.9 ms | 28.6 ms | 1.85 s |
| 12.6 | 81% | 22.2 ms | 41.8 ms | 2.10 s |
| 14.5 | 81% | 29.0 ms | 45.3 ms | 2.24 s |
| 16.3 | 83% | 14.2 ms | 30.1 ms | 2.15 s |

Session open rises monotonically with host load. That is what "attaches
take a while" is.

### Ruled out, with evidence

- **The edge and the gateway relay.** At the same moment as the 17 ms
  guest measurement the edge was at load 0.06, the `gateway` process at
  0.0% CPU, and an echo to the edge itself was 0.63 ms p50 / 0.89 ms p99.
  The relay copies with `io.Copy` over `ssh.Channel` (32 KiB buffers,
  `relay.go:467`) and sustains 25–31 MB/s through the gateway against
  62–96 MB/s direct to the host. Nothing per-keystroke is wrong with it.
- **cgroup CPU throttling.** `nr_throttled` was 298 of 3,082,823 periods
  (0.01%) on the busiest guest. The `CPUQuota` is not the binding
  constraint.
- **Memory, OOM, swap.** No OOM kill has ever occurred on host-01;
  `memory.events` is all zeros for every guest; 31 GB available; no swap.
- **KVM halt-polling.** At most 2.4% of one CPU per VM
  (`halt_poll_fail_ns` + `halt_poll_success_ns`).
- **A VM-exit storm.** 12–21 k exits/s per VM, zero `io_exits` and zero
  `mmio_exits`.
- **The network.** Edge→host-01 ICMP min 1.2 ms, no loss.
- **In-guest scheduling priority.** An interleaved A/B of four CPU hogs
  at nice 0 against nice 19 inside a guest produced no reproducible
  difference across two rounds. An earlier apparent win for nice 19 did
  not replicate and was host noise. The decisive measurement is the one
  above, taken on a guest with nothing running in it at all.

## 2. Why

Nothing is overloaded in the throughput sense. Steal inside the guests
was 1.2–3.7% and the guests saw themselves 36–89% idle. Host cgroup
accounting and in-guest `/proc/stat` agree to within 10% over the same
10-second window, so there is no hidden virtualisation overhead either.

What collapses is **scheduling latency**. A keystroke crosses many
wakeups each way: the edge's WireGuard crypt kworker, the tap, Cloud
Hypervisor's userspace virtio-net thread, an irqfd injection, the guest
vCPU thread (which must be scheduled before it can take the interrupt),
the guest's softirq, sshd, tmux and the program — and back. host-01 runs
EEVDF with `base_slice_ns = 2.8 ms` and `RUN_TO_PARITY` on, so with 16–20
runnable CPU-bound vCPU threads on 8 cores every one of those wakeups
queues behind a slice.

Measured directly: at host load 15.8, `kanali`'s eight vCPU threads
accumulated **5.94 s of scheduler wait per 5 s of wall clock** (1.19 s of
wait per second), `toolbar` 3.18 s. Average wait per slice rose from
0.018–0.111 ms at load 3.2 to 0.123–0.333 ms at load 15.8. Multiplied
across a dozen hops in each direction, that is the 15 ms ICMP and the
17–45 ms keystrokes.

Throughput barely suffers, which is exactly why no capacity metric
flagged this and the owner found it by feel.

Two aggravating details, neither the cause: `enable_apicv=N` and
`ple_gap=0`/`ple_window=0` (nested virtualisation exposes no APIC
virtualisation and no pause-loop exiting, so interrupt delivery and
spin-wait handling are both more expensive); and every guest carries
`cpu.weight = 100` regardless of class, so under contention an `xl` and a
`small` get equal shares rather than shares proportional to what the
tenant paid for.

## 3. The capacity model, corrected

Every size class is **2 GB per guest vCPU** (`small` 2/4, `large` 4/8,
`xl` 8/16). So a host's placeable memory fixes its guest-vCPU count
whatever mix is sold: placeable GB ÷ 2.

R3-2 says "CPU 2:1 oversubscribed, memory never".
[2026-10-11-pause-idle-machines.md](2026-10-11-pause-idle-machines.md)
§9 restates it as "1.9:1 on a `D64s_v7` and 1.25:1 on an AX162's 96
threads". **Both compare guest vCPUs to host SMT threads, not to cores.**

| Host | Azure/vendor vCPUs | physical cores | placeable GB | guest vCPUs sold | against threads | against cores |
|---|---|---|---|---|---|---|
| `D16s_v7` (today) | 16 | 8 | 56 | 28 | 1.75:1 | **3.5:1** |
| `D64s_v7` (documented launch) | 64 | 32 | 240 | 120 | 1.9:1 | **3.75:1** |
| AX162-2 (EPYC 9454P) | 96 threads | 48 | 240 | 120 | 1.25:1 | **2.5:1** |

The measurement in §1 puts the smooth/not-smooth boundary at roughly
**busy guest vCPUs ≈ physical cores**, less about 10% for the host's own
work (WireGuard crypt, virtiofsd, hostd, fluent-bit). At 1–2× that, typing
is 17–45 ms. So the policy as written permits about twice the load at
which the product stops feeling good, and the implementation permits
more than the policy, because:

- `scheduler.PickHost` (`internal/api/scheduler/scheduler.go:120`) filters
  on memory, thin-pool room and guest slots. There is **no CPU term**.
- `scheduler.ClassVCPU` (`scheduler.go:42`) is referenced only by
  `internal/api/http/samples.go:116`, for a display field.
- hostd's own admission (`internal/hostd/guest/create.go:595`) checks free
  memory and pool percentage, never CPU.
- `hosts.vcpus` is recorded (`hostmgr.go:331`) and never read for a decision.
- `hostinfo.go:180` reports `runtime.NumCPU()`, so the number it records is
  **threads, not cores** — 2× optimistic before anything uses it.
- `LAUNCH.md` §3 says `SEATS_TOTAL=0` derives the seat count from host
  memory ("30 for one `D64s_v7`"). That derivation is the oversell.

### Seats a host can actually deliver

A seat is 8 GB of running memory = 4 guest vCPUs. Usable cores are taken
as 0.9 × physical cores; at most `usable ÷ 4` seats may be busy at once.
Sellable seats are then the largest *n* for which
P(Binomial(*n*, *p*) > that cap) ≤ 5%, capped by memory.

*p* is the share of running time a machine is actually consuming its
allocation. Measured: **48%** for the owner's own active account over 918
machine-hours, and **33% fleet-wide** across 73,059 running minutes
(48,887 of them idle), from pause-idle §5. Two guests measured directly on
2026-10-14 gave 55% (`toolbar` during an agent session) and 25% (`kanali`
over 86 h). The product's pitch — the agent keeps working after the laptop
closes — selects for the high end, so *p* = 0.4–0.5 is the prudent plan and
*p* = 0.3 the optimistic one.

| SKU | cores | RAM | $/mo | seats memory allows | p=.3 | p=.4 | p=.5 | $/seat at p=.4 |
|---|---|---|---|---|---|---|---|---|
| `D16s_v7` (today) | 8 | 64 | 772 | 7 | 1 | 1 | 1 | 772 |
| `D32ls_v7` | 16 | 64 | 1,368 | 7 | 5 | 4 | 3 | 342 |
| `D64ls_v7` | 32 | 128 | 2,736 | 14 | 14 | 11 | 9 | 249 |
| `D64s_v7` (documented) | 32 | 256 | 3,091 | 30 | 14 | 11 | 9 | 281 |
| `D128ls_v7` | 64 | 256 | 5,473 | 30 | 30 | 25 | 21 | 219 |
| `D192ls_v7` | 96 | 384 | 8,209 | 46 | 46 | 40 | 33 | 205 |
| `D248ls_v7` | 124 | 496 | 10,603 | 60 | 60 | 53 | 44 | 200 |
| `D372s_v7` (biggest Intel) | 186 | 1,488 | 17,964 | 184 | 111 | 85 | 69 | 211 |

Two things fall out:

1. **Buy the low-memory line.** On `D64s_v7` memory allows 30 seats and
   CPU delivers 11, so 19 seats of RAM are paid for and unsellable.
   `D64ls_v7` delivers the same 11 seats for $355 less a month. On the
   `ls` line at the measured fleet duty cycle the memory ceiling and the
   CPU ceiling nearly coincide — the shape is correctly balanced.
2. **Bigger hosts are more efficient**, $249 → $200 per seat from
   `D64ls_v7` to `D248ls_v7`, because a larger host's peak sits closer to
   its mean. Fragmenting costs seats: 2× `D128ls_v7` is 128 cores, 50
   seats, $10,946, against 1× `D248ls_v7` at 124 cores, 53 seats,
   $10,603. The ~6% is the price of a smaller blast radius, which matters
   because a project is pinned to its host for life (DESIGN §4).

## 4. What Azure can actually sell us

Read on 2026-10-14 from `az vm list-skus --location eastus --all`: 1,422
SKUs, 680 creatable (not `NotAvailableForSubscription`).

- **4 GB per core is the floor on Intel.** The only creatable x64 SKUs at
  ≤4 GB/core that declare `SupportedVirtualizationTypes =
  NestedVirtualization` are the `D<n>ls_v7` / `D<n>lds_v7` line (and the
  `D<n>nls_v6` equivalents), at 32, 48, 64, 96 and 124 cores. All
  unrestricted in East US zones 1, 2 and 3.
- **The ideal shape exists and cannot run guests.** `F64als_v7` and
  `F80als_v7` are 2 GB per core with `vCPUsPerCore = 1` — real cores, no
  SMT, exactly the product's ratio, which would be 1:1 at full memory
  sale. They are AMD and declare **no** nested virtualisation. Of 138
  SKUs that declare it, the only AMD ones are `L<n>aos_v5`
  (storage-optimised, 16 GB/core). So I-39's Intel-only conclusion holds
  for a harder reason than the 50–90% nested penalty in RESEARCH.md §1:
  on AMD v7 there is no nested virtualisation to benchmark.
- **Intel F-series is gated.** Every `F<n>s_v2` is
  `NotAvailableForSubscription`, like the v5 and v6 general-purpose sizes
  of I-39.
- **Biggest Intel machine with nested virtualisation:** `D372s_v7`, 372
  vCPU / 186 cores / 1,488 GB, $17,964 a month.

### Quota blocks the launch host today

| Quota, East US | used | limit |
|---|---|---|
| Total Regional vCPUs | 40 | **65** |
| Standard Dsv7 Family vCPUs | 32 | 350 |
| Standard Dlsv7 Family vCPUs | 0 | 350 |
| Standard Dldsv7 Family vCPUs | 0 | 350 |

I-39 recorded the 350-vCPU family grants but the **regional total was
never raised**. `D64s_v7` or `D64ls_v7` needs 104 of a 65 cap, so the
launch host of LAUNCH.md §3 cannot be created. Raising
`Total Regional vCPUs` to 350 (matching the family grants) covers every
option in §3 up to `D248ls_v7`. Portal → Quotas → Compute → East US →
Total Regional vCPUs → New Quota Request. `Microsoft.Quota` is not a
registered provider on the subscription, so the REST and `az quota`
routes need `az provider register --namespace Microsoft.Quota` first.

## 5. What the credits carry

From the credits REST API on 2026-10-14: current balance **$9,357.30**,
estimated **$8,542.80** after this period's $814.50, nothing expired.
Measured burn 1–14 October was $814.50 over 14 days = **$1,745 a month**,
which is 4.9 months of runway.

"Net burn" below is all-in cost minus revenue at *full* occupancy, all
Solo (Solo is $49 for one seat, Plus $44.50 a seat, Pro $42.25 a seat, so
selling only Solo maximises revenue per host). All-in adds the host's
2048 GB P40 data disk at $259, edge and control at $289, and about $380 a
month of non-repose Azure services on the same subscription.

| Host | $/mo all-in | seats | max revenue | net burn | runway now | +$25k | +$150k |
|---|---|---|---|---|---|---|---|
| `D16s_v7` (today) | 1,700 | 1 | 46 | 1,654 | 5.2 mo | 20.3 | 95.9 |
| `D32ls_v7` | 2,296 | 4 | 184 | 2,112 | 4.0 mo | 15.9 | 75.1 |
| `D64ls_v7` | 3,664 | 11 | 507 | 3,157 | 2.7 mo | 10.6 | 50.2 |
| `D64s_v7` (documented) | 4,019 | 11 | 507 | 3,512 | 2.4 mo | 9.5 | 45.1 |
| `D128ls_v7` | 6,401 | 25 | 1,151 | 5,250 | 1.6 mo | 6.4 | 30.2 |
| `D248ls_v7` | 11,531 | 53 | 2,441 | 9,090 | 0.9 mo | 3.7 | 17.4 |
| 2× `D128ls_v7` | 12,133 | 50 | 2,302 | 9,830 | 0.9 mo | 3.4 | 16.1 |

The seat price corrections this forces on PRICING.md "The cost floor":
that section assumes $100 per seat on Azure at 30 seats per host. At 11
deliverable seats a `D64s_v7` seat costs **$281**, and the best-shaped
Azure host, `D248ls_v7`, is **$200**. Solo nets $46.05. Against an
AX162-2, 30 seats gave $33.07 a seat; at 18–30 CPU-deliverable seats it is
**$33–55**, so Solo's +$12.98 margin shrinks or inverts, and the
"break-even once 22 Solo seats are sold" line needs re-deriving.

## 6. The grant path, with dates

The Microsoft for Startups milestones are in
[2026-10-04-solo-at-20.md](2026-10-04-solo-at-20.md): $25,000 for 5 or
more Azure workloads sustained about 60 days (plus a one-time two-year
credit extension), $50,000 for 7, $150,000 for 10 workloads and about
$3,000 a month. A workload is one Azure service with $1 or more of spend;
if one drops below, the count drops and the 60 days restart.

That proposal could not read spend (the tofu principal has no Cost
Management role). Read on 2026-10-14 as the owner, 1–14 October,
projected monthly:

| Service | $/month | counts? |
|---|---|---|
| Virtual Machines | 1,365 | yes |
| Storage | 233 | yes |
| Azure Cognitive Search | 63 | yes |
| NAT Gateway | 53 | yes |
| Virtual Network | 15 | yes |
| Azure Container Apps | 9.50 | yes |
| Container Registry | 4.30 | yes |
| Foundry Models | 2.50 | only 7 of 14 days |
| Key Vault | 0.04 | no |
| Bandwidth | 0.02 | no |
| Log Analytics, Azure Monitor | 0.00 | no |

**Seven workloads clear $1 a month, against a bar of five.** Key Vault
never will as configured: software-protected keys carry no monthly fee,
only $0.03 per 10k operations.

The binding constraint is age, not count. From resource creation dates:

| Service | spending since | 60 days complete |
|---|---|---|
| Virtual Machines, Storage, Virtual Network | 2026-09-13 | 2026-11-12 |
| NAT Gateway, Key Vault | 2026-09-19 | 2026-11-18 |
| Cognitive Search, Container Apps, Container Registry, Log Analytics | 2026-10-02 | **2026-12-01** |

The fifth qualifying workload completes **2026-12-01**. Nothing we own is
60 days old yet, so **$25k cannot land before 2026-11-12 under any
circumstance** and realistically arrives on 1 December. Launching does not
move that date.

Two things would reset it: deleting `rg-waterville-rag`, which carries
Cognitive Search, Container Apps and Container Registry — three of the
seven; and deleting `dev-cluster`/woker-1, which carries the earliest VM,
Storage and Virtual Network spend. The staging teardown pending in
`~/repose-ws/AZURE-RESOURCES.md` would shave the NAT Gateway line
(staging's gateway is about $33 of the $53) without dropping it below $1.

**$150k is the only number that funds a real host.** It needs three more
workloads and $3,000 a month, both for 60 days. Workloads added by
2026-10-16 complete about 2026-12-15, and a `D64ls_v7` produces the
$3,000 by itself. The sequence self-funds: at $3,664 a month from
mid-October the balance reaches about $2,900 on 1 December, +$25,000
lands, then $150,000 about 15 December. $2,900 is the thinnest point.

## 7. What it would take to decide

Nothing here is decided. The entries this would need:

1. **Amend R3-2.** State the oversubscription ratio against physical
   cores, not vendor vCPUs, and name the ratio the product will stand
   behind. §1's measurement says 1:1 busy-to-cores is smooth and 2:1 is
   not.
2. **A CPU term in placement.** `PickHost` counts committed guest vCPUs
   against the host's cores and refuses past the ratio. Requires
   `hostinfo` to report physical cores, not `runtime.NumCPU()`, with the
   old field kept for one release per the interfaces rule.
3. **Per-class `CPUWeight`** in `GuestUnitProps`, so contention divides in
   proportion to the class rather than equally.
4. **A host CPU alert** in `ops/alerts.yaml` on the `load1` and CPU
   pressure hostd already reports.
5. **Correct `SEATS_TOTAL`'s derivation** in LAUNCH.md §3 and in the api,
   from memory to the smaller of the memory and CPU ceilings.
6. **Re-derive PRICING.md "The cost floor"** at the seat counts in §3.
7. Possibly **change the class ratio.** No Azure Intel shape goes below
   4 GB per core while the product sells 2 GB per vCPU, so 2:1 is the
   structural floor on Azure. At 4 GB per guest vCPU the `ls` line
   balances exactly 1:1. That is a product decision, not an infra one.

Items 1–4 are the ones that stop a tenant's agent degrading its
neighbours. Item 5 is the one that matters before launch, and it is an
environment variable, not code.

## 8. Side bugs found while measuring

1. **A guest that fails `create` at step 10 is unroutable for life.** The
   api records the guest's address from the create result (I-42). When
   `lat-probe`'s create failed at step 10, the address was never
   recorded; a later successful `start` did not record it either, and a
   stop/start cycle did not repair it. The gateway then refuses with
   *"lat-probe is on a host the control plane cannot reach right now; try
   again shortly"*, which blames the control plane and tells the user
   nothing they can act on. Only destroy and recreate fixed it.
2. **Creates fail on a busy host with memory to spare.** Twice, 74 s
   apart: `create: step 10 (ready) failed: RegisterPaths: guestd:
   internal: register paths: run /run/current-system/sw/bin/nix-store:
   context deadline exceeded`. The same create took 14 s once the host was
   quiet. That deadline has no headroom for a loaded host.
3. **`~/.ssh/repose/config` can point at a deleted binary.** It stores the
   absolute path of the repose that wrote it (`reposeExecutable()`,
   `internal/cli/sshprepare.go:105`). On woker-1 it held a
   `/tmp/nix-shell.*/go-build*/exe/repose` that no longer exists, so ssh's
   `Match exec` failed silently, certificates were never renewed, and
   `repose status` reported `checkout unknown (ssh failed: ... No such
   file or directory)`. `status` shells out to `ssh <project>.repose` but
   does not call `writeSSHEntry` first, so it cannot self-heal; `repose
   ssh-prepare` fixed it in 0.16 s. Dev-box-specific, but it makes
   `status` lie.

## 9. How to reproduce

```
# host-side: what the guests are doing to the host
ssh host-01 'vmstat 1 5; cat /proc/pressure/cpu'        # gu column, run queue
ssh host-01 'top -H -b -n1 | grep vcpu'                  # per-vCPU-thread CPU

# the mechanism: scheduler wait per vCPU thread, 5 s window
ssh host-01 'snap(){ for p in $(pgrep -f "cloud-hypervisor --api-socket"); do
  n=$(tr "\0" " " </proc/$p/cmdline | grep -o "systemd.hostname=[a-z0-9-]*" | cut -d= -f2)
  for t in /proc/$p/task/*; do read r w s < $t/schedstat; echo "$t $n $w $s"; done
  done | sort; }; snap >/tmp/b1; sleep 5; snap >/tmp/b2; join /tmp/b1 /tmp/b2 |
  awk "{dw=\$6-\$3; dt=\$7-\$4; if(dt>5){S[\$2]+=dw; C[\$2]+=dt}}
       END{for(g in S) printf \"%-12s wait=%8.1fms per-slice=%6.3fms\n\", g, S[g]/1e6, S[g]/C[g]/1e6}"'

# the latency itself, into a throwaway guest, not an existing one
repose run --size small --name lat2 --no-sync --no-attach
ssh host-01 'ping -c 30 -i 0.1 -q <guest ip>'
# keystroke echo: write one byte into `cat` over ssh, time the round trip, 100 times
```

The echo harness and the sampler used on 2026-10-14 are not committed;
both are a dozen lines (write 1 byte to `ssh host cat`, time the read).

## Sources

- `az vm list-skus --location eastus --resource-type virtualMachines --all`, 2026-10-14.
- `az vm list-usage --location eastus`, 2026-10-14.
- Azure retail prices API, `armRegionName eq 'eastus'`, `priceType eq 'Consumption'`, 2026-10-14.
- `Microsoft.Consumption/usageDetails` and `credits/balanceSummary` REST, 2026-10-14.
- `az resource list --query createdTime`, 2026-10-14.
- Microsoft for Startups milestones, via [2026-10-04-solo-at-20.md](2026-10-04-solo-at-20.md) §"Microsoft for Startups", from learn.microsoft.com as updated 2026-08-05. **Re-verify before acting:** read second-hand here, and partner-sourced offers may sit outside the standard ladder.
- Duty cycle: [2026-10-11-pause-idle-machines.md](2026-10-11-pause-idle-machines.md) §5.
