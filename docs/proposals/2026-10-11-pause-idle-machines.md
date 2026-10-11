# Resting idle machines to sell more seats per host (proposal, 2026-10-11)

**Status: analysis only. Nothing here is decided.** This is direction 4
of the pricing directions. A machine that nobody is attached to and whose
agents are all waiting would have its vCPUs paused and its memory pushed
to host swap, then come back in a few seconds when anything reaches for
it. A host could then carry more subscriptions than it has 8 GB seats,
while users keep paying the same plan prices. Processes, guest memory and
files survive a rest. Outbound connections do not survive one that
outlasts their idle limit: 30 minutes at repose's Azure NAT gateway
(`infra/azure/modules/network/variables.tf:97`), or the remote server's
own timeout, whichever comes first. Any of it becomes real only through
`DECISIONS.md` entries that amend R1-5, R3-2, I-230, I-235, I-262 (which
rejected an opt-in `idle_stop`), I-290 and the "three homes" rule in
`CLAUDE.md`, and through a change to the public seat promise in
`billing.md` and `limits.md`.

Outside facts were checked on 2026-10-11 and carry their URL in
"Sources". Prod numbers come from read-only queries of `meter_samples`
and `proc_samples` over the last 14 days (each run inside a transaction
that was rolled back). The sample is 3 accounts; 79 of their 94 projects
ran in the window. Treat every share below as a first look. Figures
marked *unverified* could not be confirmed; figures marked *estimate* are
arithmetic on a quoted rate.

## Summary

| Question | Answer |
|---|---|
| Mechanism | Pause the vCPUs (`vm.pause`), push the guest's RAM to encrypted host swap with cgroup `memory.reclaim`, resume and prefetch on wake. The Cloud Hypervisor (CH) process, virtiofsd, tap and vsock stay up |
| Snapshot to a file instead? | Rejected for repose: with vhost-user virtio-fs, CH can only restore by copying the whole image first, restore resets vsock and starts a new virtiofsd, and the image is a file of guest RAM on disk (I-233, I-235) |
| Share of running time that could pause | Active account 29.0% (strict rule, 30 min quiet). Forgotten machine 90.1%. All 73,076 running minutes: 47.7% at a 6 s/min CPU limit and 15 min grace, an upper bound that leans on the forgotten machine |
| Wake time | *Estimate* 1 to 3 s with about 1 GiB resident after a cache drop; 4 to 14 s if all 8 GiB must come back. Unmeasured on any repose host |
| Seat effect | On the active account's share, an AX162-2 carries about 37 subscriptions on its 30 seats once a paused guest's leftover memory and an 8 GiB wake reserve are counted. That is Solo's break-even (37) and short of Pro's (43) |
| Cheaper options | The AX162-1-LTD ($26.58 a seat) or the Intel EX131-2-LTD ($20.07) at today's one seat per 8 GB; or free page reporting with no pause, which amends R3-2 and leaves R1-5 alone |
| Cash saved on Azure | None. Azure loss is paid by credit; the first cash saving comes with the first Hetzner host sold past 30 seats |
| Biggest risk | The rule freezes a background job that looked quiet. R1-5 names that failure as worse than a bill |
| Decide first | Does an invisible pause that drops idle outbound connections count as "never stopped" under R1-5? May encrypted host swap hold guest RAM? |

---

## 1. The repo today

**Cloud Hypervisor calls.** `internal/hostd/ch/ch.go:264-272` defines
`Pause` and `Resume` (`vm.pause`, `vm.resume`), and nothing in
`internal/hostd` calls them. No code calls `vm.snapshot` or `vm.restore`.
The VM is launched with `--memory size=%dM,shared=on` (`ch.go:121`), two
vhost-user `--fs` devices (store, `ch.go:136`; login share, `ch.go:145`,
I-278), `--vsock`, a tap and a `direct=on` disk. There is no `--balloon`.
CH 53 comes from nixpkgs (`nix/hosts/virt.nix:50`).

**Memory accounting.** Each `guest@<id>` unit gets `MemoryMax` = class RAM
plus `OverheadMiB` 512 (`internal/hostd/guest/unit.go:28-29`).
`manager.go:100-108` (I-230) calls guest RAM "all shmem, never
reclaimable", which holds only because hosts have no swap (no
`swapDevices` in `nix/hosts`). `FreeMemBytes` (`manager.go:652-680`) and
the api's `PickHost` and `Freeing` (`internal/api/scheduler/scheduler.go:118-176`)
both count class RAM per guest, never resident memory. Hosts set
`vm.overcommit_memory=1` and `transparent_hugepage=madvise`
(`nix/hosts/kernel.nix:31,37`).

**Signals the rule uses.** Per minute, `meter_samples` holds `cpu_ns`,
`net_tx` and `net_rx`, `ssh_sessions`, `tmux_clients`, `agents[]` and
`guestd_ok`, plus `mem_rss` (host-resident) and `mem_used` (guest
MemTotal minus MemAvailable, I-493, since 2026-10-05) for sizing.

**The idle rule.** `internal/api/idle/idle.go` counts a minute as used
when it has an SSH session, a tmux client, guestd not answering, or an
agent outside `idle` and `needs_input` (`usedSQL`, :70-72). After 24 h
unused it warns (`After`, :27) and does nothing else. `repose stop
--unused` stops each machine the notice names.

**Pause is already on the deferred list.** `docs/features/stop-start-destroy.md`
names "Pause (Cloud Hypervisor pause without snapshot) as a cheaper stop".

**Prior measurement, I-233** (CH 53, dev box, `large` guest booted and
idle 20 s):

| Step | Result |
|---|---|
| `vm.pause` | 6 ms |
| `vm.snapshot` | 0.29 s, 0.78 GB sparse memory file |
| Restore and resume, file in page cache | 0.54 s |
| Restore and resume, cold | 1.2 s |
| guestd after resume | answered 10 ms later; tmux kept its session |
| Guest clock after restore | 10 s behind |

I-233 did not adopt snapshots: the image is a fourth home for secrets,
virtiofsd restarts with stale inodes, and the image must be discarded on
any closure, volume or host change.

**I-235 and I-262 point another way.** I-235's option 1, the owner's
first choice, is a pause in RAM triggered by `repose stop`: "`stop`
pauses the VM and leaves its memory resident on the host ... No image, so
no new home for secrets", with paused time priced "since it holds
capacity". Its option 5 makes any of this "opt-in per project ... so a
project that never opts in keeps today's behaviour". I-262 rejected "an
opt-in `idle_stop` (overrides R1-5)". This proposal departs from all
three: the pause is automatic on a machine the user left running, guest
RAM goes to host disk, and section 6 proposes it on by default with an
opt-out key. Decisions 1, 2 and 5 below ask the owner to rule on each
departure.

## 2. Cloud Hypervisor upstream

CH v53.0 shipped on 2026-07-12 and v52.0 on 2026-05-14.

| Feature | Version | Bearing on repose |
|---|---|---|
| Lazy restore through userfaultfd (`memory_restore_mode=ondemand`) | v52 (#7800) | Unusable. `docs/snapshot_restore.md` on main (2026-10-11) says "vhost-user devices are not supported", and repose has two. The v53.0 copy of the doc lists only prefault and page alignment as limits, so the constraint is newer than v53 in writing |
| Guest clock advanced on resume after restore or migration | v53 (#8256) | Covers restore. A plain pause and resume goes through `set_clock` in `SameHostResume` mode |
| `reserve=off` default, RAM mapped `MAP_NORESERVE` | v53 (#8350) | Overcommit relies on swap as its only safety valve |
| Balloon free page reporting | present | Works with `shared=on`: CH punches holes in the memfd (`virtio-devices/src/balloon.rs`) |

The snapshot path (M3) also gained a sparse memory file (v52, #8113),
virtio-fs snapshot support (#7908, #7937) and a vsock reset on restore
(#7958); `copyonwrite` restore is on main and absent from v53.0. KSM is
out, since v52 rejects `mergeable` together with `shared` (#8126).

The clock after a plain pause is unmeasured. I-233's 10 s lag came from
a snapshot and restore, and #8256 changed the restore path since. KVM
advances kvmclock on `KVM_SET_CLOCK` by the elapsed realtime when the
host clocksource is TSC-based, and the dev box uses `tsc`, which predicts
no lag for a pause. A wake should still step the guest clock from hostd
as a precaution, and step 1 measures the drift. Nothing in
`internal/guestd` or `nix/guest` corrects the clock today.

## 3. Mechanisms

| | Frees | Wake | Keeps processes and sockets | Secrets at rest | Fit |
|---|---|---|---|---|---|
| M1. Pause in RAM | CPU only | 6 ms | Yes | None | First step of M2 |
| **M2. Pause, then host swap** | All but resident remainder and the 512 MiB overhead | `vm.resume` plus swap-in | Processes yes; idle outbound sockets past their timeout no | Encrypted swap, key per boot | **Recommended mechanism** |
| M3. Snapshot, kill CH, restore | Everything | Eager copy of the image | Vsock reset, new virtiofsd | Image file | Rejected for now |
| M4. Free page reporting, no pause | Guest free pages and dropped cache | None needed | Yes | None | Do first |
| M6. Count resident memory in placement | Makes M2 and M4 sell seats | | | | Needed by both |

**M2 sequence.** guestd runs `sync` and drops its page cache. hostd calls
`vm.pause`, then writes the class size to
`/sys/fs/cgroup/guests.slice/guest@<id>/memory.reclaim`. On wake, hostd
calls `vm.resume`, asks the kernel to read the RAM range back with
`process_madvise(MADV_WILLNEED)` (which reaches `shmem_swapin_range`),
and sends guestd the wall time. The cgroup interface reclaims through
rmap. `MADV_PAGEOUT` would skip the pages, because virtiofsd maps guest
RAM too and the kernel skips folios mapped by more than one process.

**A paused guest keeps a floor in host RAM:** the 512 MiB overhead, the
resident remainder (about 1 GiB after a cache drop, *estimate*), and the
`virtiofsd@<id>` and `virtiofsd-auth` processes (capped at 1 GiB by
`MemoryMax`; their real RSS is *unverified*). Section 9 counts a floor of
0.5 to 1.5 GiB per paused `large` guest.

**M2 needs** a swap device on each host. NixOS `swapDevices.*.randomEncryption`
gives aes-xts with a random key per boot, held only in host kernel memory,
where I-235 already concedes the host can read live guest RAM. Nothing
survives a host reboot and nothing is a file a backup could pick up. It is
still a new place where guest RAM, secrets included, touches disk, so it
amends `CLAUDE.md` "Secrets have three homes" and `features/secrets.md`.
A per-guest `memory.swap.max` bounds it. zswap in front would cut disk
writes but keep roughly a third to a half of the paused RAM resident
(*unverified* ratio for guest RAM).

**M4 on its own.** In idle minutes the active account's guests used
0.93 GiB (p50, 2,613 samples, all classes) while the host held 6.29 GiB
for them (p90 8.05 GiB). By class, over all minutes:

| Class | Host RSS avg (GiB) | Guest used avg (GiB) | Guest used p90 |
|---|---|---|---|
| small | 1.03 | 0.40 | 0.40 |
| large | 6.07 | 1.65 | 2.01 |
| xl | 10.87 | 2.41 | 4.76 |

`mem_used` excludes guest page cache, and free page reporting returns only
free pages, so the guest must also drop or reclaim cache to close the gap.
The amount that comes back in practice, and whether the guest kernel has
`CONFIG_PAGE_REPORTING` and `virtio_balloon`, are *unverified*.

M4 frees memory with no pause, so R1-5 is untouched. Selling seats on it
is another matter. If the active account's unused minutes (56.1%, the
complement of `usedSQL`) each hold 2 to 2.5 GiB resident out of 8,
placement on resident memory (M6) would carry f of about 1.6 to 1.7
(*estimate*, 1/(1 - 0.561 x (1 - 2.5/8)) to 1/(1 - 0.561 x 0.75)). The
catch is R3-2: a guest that wakes into a build retakes its memory at
once, and on a host with no swap several doing so together meet the host
OOM killer, which ends a guest. A paused guest under M2 instead waits
for room. Any seat sold on M4 alone amends R3-2 even with no pause. A
size-0 balloon with reporting at f = 1.0 takes nothing the guest wants
and amends nothing.

## 4. Size and time for an 8 GB guest

| Disk | Write 8 GiB | Write about 1 GiB after cache drop (*estimate*) |
|---|---|---|
| Azure Premium SSD v2 as provisioned, 600 MB/s (`infra/azure/prod/variables.tf`) | 14.3 s | 1.8 s |
| Premium SSD v2 at its 2,000 MB/s maximum | 4.3 s | 0.5 s |
| Local NVMe at 2,700 MB/s write (a common PM9A3 write rating; Samsung's page gives only reads, the rating varies by capacity, and the AX162's drive model is *unverified*) | 3.2 s | 0.4 s |

On Premium SSD v2 the read and write limits are the same throughput
figure, so the first two rows bound the wake too. The PM9A3 reads at up
to 6,800 MB/s, which puts 1 GiB back in 0.16 s and 8 GiB in 1.3 s. The
wake estimate is 1 to 3 s: `vm.resume` in milliseconds, a `WILLNEED`
prefetch of about 1 GiB (1.8 s at 600 MB/s, 0.2 s on NVMe), a clock step
and a guestd ping. Without prefetch, each fault waits on the disk, and a
large working set touched that way could take tens of seconds
(*unverified*). All guests' disk traffic on a `Standard_D64s_v7` shares
one VM-level cap, listed at 3,776 MB/s by a third-party site
(*unverified* against Microsoft).

For scale, E2B pauses at about 4 s per GiB and resumes in about 1 s,
after which clients must reconnect. Fly.io suspends machines of 2 GB or
less with no swap, resumes in "a few hundred ms", warns that the "clock
can lag a few seconds", and drops the snapshot on deploy. GitHub
Codespaces stops a codespace after 30 minutes idle by default (5 to 240)
and caps its lifetime at 12 hours.

## 5. Pausable time in prod

Account 1 (likely the owner) ran 918 machine-hours in the window. It was
busy 48% of them by the established count (attached or an agent working)
and 43.9% by `usedSQL`. Account 2 ran 299 hours with no use at all.
Neither busy figure feeds the pausable shares below: each share counts
the running minutes that sit inside a stretch passing the named rule for
the stated quiet time.

| Rule | Account 1 | Account 2 |
|---|---|---|
| Running minutes | 55,105 | 17,954 |
| Pausable: agent signals only, 30 min quiet | 52.1% | 99.8% |
| Pausable: plus CPU under 0.10 vCPU and net under 64 KiB a minute, 30 min | 29.0% | 90.1% |
| Same test, 10 min | 33.5% | 94.7% |

**The forgotten machine is the shipped notice's job.** I-262 warns after
24 h unused, and `repose stop --unused` stops the machine. A user who
acts on the first notice recovers about 92% of account 2's 299 hours
((299 - 24) / 299) with no new code. Pause adds savings there only for a
user who ignores the notice. Section 9 therefore sizes seats on account
1's 29.0% and treats the fleet-wide 47.7% as an upper bound.

Idle stretches are long. Of all 48,887 idle minutes, 99.5% sit in
stretches of 15 minutes or more and 27,439 sit in 6 stretches of a day or
longer, so a 15 minute grace gives up 0.5% of idle minutes.

The agent signals alone miss background work. Idle minutes averaged
8.84 s of guest CPU per minute (p95 23.1 s). The heaviest processes in
idle minutes over the 14 days:

| Process | CPU hours in idle minutes |
|---|---|
| `.claude-wrapped` at its prompt (about 2 s/min each) | 12.2 |
| chrome-headless | 7.4 |
| chromium | 6.2 |
| ffmpeg | 4.8 |
| python3.12 | 3.7 |
| guestd | 3.1 |
| containerd and dockerd | 3.1 |
| qemu-system-x86 inside a guest | 1.3 |

Pausing on agent states alone would have frozen ffmpeg, browser and qemu
jobs. The CPU limit decides the share:

| CPU limit per minute | Pausable after 15 min grace | After 60 min |
|---|---|---|
| 3 s | 20.3% of running | 15.3% |
| 6 s (0.10 vCPU) | 47.7% | 41.5% |
| 12 s | 58.6% | 53.7% |

A 3 s limit fails as soon as Claude sits open at its prompt, so 6 s is
the workable line. The forgotten machine's idle CPU p90 is 0.065 vCPU,
under it. The active account's idle p50 is 0.069 vCPU, close enough to
the line that small changes in an agent's idle polling would move the
share.

## 6. The proposed rule

A machine may pause when each of the last 15 one-minute samples shows:

- `guestd_ok`, and no gateway session;
- `ssh_sessions` 0 and `tmux_clients` 0;
- each agent `idle` or `needs_input` (`unknown` counts as working, as in
  `usedSQL` today);
- `cpu_ns` under 6 s;
- `net_rx` plus `net_tx` under 64 KiB a minute (first value; account 1's
  idle p90 is 41 KiB, and step 1 checks it against a long-polling bot and
  an idle MCP connection);
- no user timer due within the next 15 minutes;
- no snapshot, apply or build op in flight.

Docker containers do not block a pause on their own: CPU and network
cover a working container, and an SSH port forward wakes one that serves
a port. 5,322 idle minutes had a container. The recommended default is
to let them pause; a project config key to keep a machine awake covers
the jobs the rule gets wrong, with its own docs page entry.

Pause is opt-out in this draft, which reverses I-235 option 5's opt-in.
Opt-in keeps every existing user's machine as it is today and earns
little, since few users would turn it on; opt-out earns the shares in
section 5 and puts the risk on users who never read the setting. That
choice is decision 5.

hostd should evaluate the rule from its live samples so a pause does not
wait on the api; the api sets each host's budget. Hosts a user brings
under I-647 never pause by default: the owner pays nothing for their
memory, and a frozen job there costs the user with no saving to anyone.

## 7. Paths that must wake a machine

| Path | Wake trigger | Notes |
|---|---|---|
| ssh, attach, `repose exec`, `repose code`, sync, port forwards, `repose browser` | Gateway route lookup (`internal/api/http/routes.go:84`, gateway `cache.go:201`, dial in `relay.go:189`) | The route carries a resting flag; the api wakes the guest and the gateway holds the dial, as I-236's long poll does. The fast attach path (I-223) skips the api and must tolerate a slow dial |
| `repose reply`, a dashboard answer | `AnswerQuestion` (`hostd/vsockclient/client.go:216`) | A paused `needs_input` agent is fine only because the answer wakes it |
| `repose secrets set` | `WriteSecrets` | Wake at once; the agent guide promises a new value "within seconds" |
| Config apply, base switch, `GrowFs`, `SetPrincipals`, admin `Exec` | vsock request | Wake, or queue and apply on the next wake |
| User systemd timers | hostd sets a wake from guestd's report of the next elapse, taken before each pause | `machine.md` promises timers run "with nobody attached". Monotonic timers may stretch by the paused time; the two upstream readings disagree (*unverified*) |
| Nightly snapshot at 03:00, `repose snapshot` | hostd | `sync` before each pause keeps an LVM snapshot of a paused guest consistent; otherwise I-594's crash-consistent path |
| `Sample` every 60 s | none | hostd reports `paused` and leaves the guest asleep |

Some things cannot wake a guest and so must block a pause: a job waiting
on an outbound connection (a bot long-polling an API, `gh run watch`, a
websocket MCP server). No packet comes in to wake it. The network
threshold in section 6 is the only guard. Inbound traffic on the tap does
not wake a paused CH either, which matters once HTTPS previews exist;
`internal/gateway/preview` is a stub today. `repose-notify` and new
`repose-ask` questions start inside the guest, and a paused guest has no
running agent to send one.

## 8. Breakage and reconnects

| Item | Under M2 | Fix |
|---|---|---|
| Outbound TCP | repose's Azure NAT gateway drops a flow idle for 30 min (`nat_idle_timeout_minutes`, `variables.tf:97`; Azure's default is 4, range 4 to 120). Hetzner hosts have no NAT. Remote servers drop idle connections on their own timeouts on either. Host conntrack keeps entries 86,400 s (`kernel.nix`, I-453) | Agents must reconnect. Recovery of Claude Code, Codex and MCP servers from a dead socket is *unverified*; test after 25 and 35 min (either side of the NAT cutoff) and after 60 min or more |
| tmux and SSH | none attached, by the rule | herdr on a laptop holds SSH, which blocks the pause |
| Clock | drift after a plain pause unmeasured; 10 s after a restore (I-233) | New vsock call: hostd sends wall time, guestd steps it on wake |
| vsock to hostd | session survives | hostd's monitor must not raise `guestd_lost` after 60 s (`create.go:816`, `manager.go:287`), and samples must not write `guestd_ok=false`, which `usedSQL` reads as use |
| virtiofsd | same process | none |
| hostd restart mid-pause | | Reconcile reads CH's `vm.info` state (*unverified* field name in CH 53); the paused state goes in bbolt and `Hello` |
| Host reboot | encrypted swap is gone; the guest boots from its volume as after a crash | `sync` before pause limits the loss to RAM state |

## 9. Seats

**The promise oversell changes.** I-290 sells a seat as 8 GB the
subscription holds whether its machines run or not. The public pages say
the same: `billing.md` calls a plan's memory "how much runs at the same
time, around the clock", and `limits.md` says "A seat is 8 GB of memory
for running machines". Once a host carries more subscriptions than seats,
a paying user can find their own pinned host full when their machine
wakes. Plan memory per user stays as it is, and a paused machine still
counts against the plan's 8, 16 or 32 GB. Disclosure in `billing.md` and
`limits.md` is a precondition of step 4, along with the remedy for a wake
that does not fit (decision 8).

**Two levers.** Seat-hours a subscription holds split three ways: no
machine running, running and pausable, running and busy. Counting only
running memory (M6 with no pause) already frees the first share; pause
frees the second. Let r be the share of sold seat-hours with a machine
running, p the pausable share of running time, and φ a paused guest's
floor as a share of 8 GiB. A host with S seats free for running guests
then carries S / (r x (1 - p x (1 - φ))) subscriptions. The prod sample
cannot give r yet: it needs each account's plan seats beside its running
memory, which step 1 adds. With r = 1 (every seat running all month, the
case I-290 prices), pause alone gives:

| Pausable share p | Floor φ | f | Subscriptions on an AX162-2 (29 seats after an 8 GiB wake reserve) | $ a sold seat |
|---|---|---|---|---|
| 0.29 (active account) | 1.5 GiB | 1.31 | 37 | 26.81 |
| 0.29 | 0.5 GiB | 1.37 | 39 | 25.44 |
| 0.477 (fleet, upper bound) | 1.5 GiB | 1.63 | 47 | 21.11 |
| 0.477 | 0.5 GiB | 1.81 | 52 | 19.08 |

Counting running memory alone at r = 0.7 carries 41 subscriptions
($24.20 a seat) with no pause; at r = 0.5, 58 ($17.11). Both values of r
are illustrations, and either one amends I-290 without touching R1-5.

CPU oversubscription does not grow, because resident guests are still
bounded by RAM: 30 resident `large` guests are 120 vCPU, 1.9:1 on a
D64s_v7 and 1.25:1 on an AX162's 96 threads.

**Cost per seat at each subscription count** (host rent only):

| Subscriptions per 30-seat host | f | AX162-2, $992.10/mo | EX131-2 (Intel), $1,297.10 | EX131-2-LTD (Intel), $602.10 | D64s_v7, about $3,000/mo |
|---|---|---|---|---|---|
| 30 | 1.0 | $33.07 | $43.24 | $20.07 | $100.00 |
| 37 | 1.25 | $26.81 | $35.06 | $16.27 | $81.08 |
| 42 | 1.4 | $23.62 | $30.88 | $14.34 | $71.43 |
| 45 | 1.5 | $22.05 | $28.82 | $13.38 | $66.67 |
| 60 | 2.0 | $16.54 | $21.62 | $10.04 | $50.00 |

Revenue per seat after Polar is Solo $27.05 ($18.50 in the intro months),
Plus $27.78, Pro $23.39 (I-642). An AX162-2 at 30 seats cannot cover its
rent on any plan: 30 Solo seats bring $811.50 against $992.10. Covering
it takes 37 Solo seats (f = 1.23), 36 Plus seats (1.20), 43 Pro seats
(1.43) or 54 Solo seats at the intro price (1.80). At f = 1.25 Solo clears
by $0.24 a seat and Plus by $0.97, while Pro loses $3.42. At f = 1.4 Pro
still loses $0.23; at f = 1.5 it clears by $1.34, and Solo and Plus by
$5.00 and $5.73. The active account's 29% supports about f = 1.25, so on
that evidence pause brings Solo and Plus to break-even and leaves Pro
short.

These figures are host rent only. Hetzner's EUR 419 setup fee ($493.52)
adds $1.37 a seat a month over a year at 30 seats; IPv4, disk beyond the
host's drives, Blob snapshots, egress, the edge and the control plane are
left out, and their per-seat cost is *unverified*. Azure loses money at
every factor, which matches the owner's choice to treat Azure credit as
the launch cost.

**Cheaper hosts at f = 1.0.** The AX162-1-LTD (128 GB, $372.10, 14 seats)
costs $26.58 a seat with no pause, no swap, no new home for secrets and
no change to R1-5 or R3-2, which beats the AX162-2 at f = 1.25. The
EX131-2-LTD costs $20.07 a seat and clears all three plans, Pro by $3.32.
Hetzner sells LTD servers only while it has the hardware, so their
availability is *unverified*. Pause on top of an LTD host still helps: the
AX162-1-LTD at f = 1.25 (17 subscriptions) costs $21.89 a seat, and at
1.5 (21) $17.72. The AX162 rows also rest on an AMD host, while
`DESIGN.md` §4 calls for Intel hosts and no entry, I-642 included, has
amended that. The two EX131 columns are the Intel options.

Every bring-your-own host under I-647 takes its user's seats off the
fleet's rent entirely, which serves the money case more directly than
pause does for any user willing to run their own box.

**A wake that does not fit.** R2-3 pins a project to its host, and moving
one is a restore from Blob, 75 s plus 12.5 s to start (RESEARCH.md §11).
The proposed order: the host first pushes another eligible guest to swap
(about 2.8 s to write 7 GiB at 2,700 MB/s); if none qualifies within
30 s, the project moves, and the user sees a message naming the delay.
An 8 GiB wake reserve per host (one `large`) keeps the first wake of a
burst immediate. A credit for a wake past a set time is decision 8.

**Swap per host.** Each paused `large` guest puts about 7 GiB in swap
(8 GiB less a 1 GiB resident remainder, *estimate*). With every
subscription's machine running, 232 GiB for guests after the reserve,
8 GiB per resident guest and 1.5 GiB per paused one, an AX162-2 needs:

| f | Subscriptions | Resident at most | Paused at least | Swap |
|---|---|---|---|---|
| 1.25 | 37 | 27 | 10 | 70 GiB |
| 1.5 | 45 | 25 | 20 | 140 GiB |
| 2.0 | 60 | 21 | 39 | 273 GiB |

A dedicated 160 GiB swap partition covers f up to 1.5. It belongs outside
the LVM thin pool, so I-586's 70/85/95% guards on the pool never see it;
the AX162-2's drive layout is *unverified*. The table also shows the
pausable share a host needs at peak: 27% at f = 1.25 and 44% at 1.5,
against a measured 29% average for the active account.

## 10. Effort, timing and reputation

Engineering days for one developer working with agents (*estimate*):

| Step | Days | Saves cash? |
|---|---|---|
| 1. Measure | 3 | No |
| 2. Free page reporting and resident accounting at f = 1.0 | 4 | No |
| 3. Pause at f = 1.0, all wake paths, clock, reconcile, user docs | 12 | No |
| 4. Oversell with wake reserve, alerts and public seat text | 6 | Only on a Hetzner host |

About 25 days in all. On Azure the loss is credit, so steps 1 to 3 save
the owner no money; the first real saving lands on the first Hetzner host
sold past 30 seats, and only if the owner rents one before choosing an LTD
host or bring-your-own hosts.

Reputation weighs both ways for an owner seeking recognition. One public
report of an agent job frozen overnight by repose would land on the R1-5
promise the landing sells. A measured write-up ("idle machines rest to
encrypted swap and wake on SSH in 2 s, with CH's pause and cgroup
reclaim") is the kind of systems post that gets read by the companies the
owner wants to reach. Steps 1 and 2 carry the write-up's numbers with no
user risk, so they make sense now; steps 3 and 4 make sense once a
Hetzner host is rented.

## 11. User-visible behaviour and docs

State stays `running`. A field beside I-262's `idle` (for example
`resting: {since}` on the Project in `api.md`) shows in `repose ls`,
`repose status` and the dashboard. The word is the owner's call under
"Names are fixed", and it must not become a second name for unused.

Public docs in the same commit: `lifecycle.md` "Unused machines";
`machine.md` "Scheduled jobs" (timers run a few seconds late);
`run-and-attach.md` (a first attach after a rest takes a few seconds, and
idle outbound connections may have dropped); `notifications.md`;
`billing.md` and `limits.md` seat text; troubleshooting for a wake that
failed; and the config key if a keep-awake switch ships.

Contracts that change: `docs/interfaces/grpc-hostd.md` (pause and wake
commands), `vsock-guestd.md` (prepare-pause, timer report, set clock) and
`api.md` (route flag, Project field). The code areas go in a follow-up
plan once decisions 1 to 3 are made.

## 12. Suggested order

1. **Measure, with no user-visible change.** On host-01: free page
   reporting plus an idle cache drop on `large` and `xl` guests, with
   `mem_rss` before and after; time pause, `memory.reclaim` and resume
   with prefetch at 1, 3 and 8 GiB resident; read the guest clock after a
   plain pause of 1, 30 and 120 min; read virtiofsd RSS while paused;
   check an idle MCP connection after 25, 35 and 60+ min. Add r per
   account (plan seats beside running memory), network and timer
   signals, then re-run the section 5 queries on more users and on daily
   peaks.
2. **Free page reporting and resident-memory accounting,** f = 1.0.
3. **Pause only, f = 1.0,** hosted hosts only. Same seats. This proves
   wake on the gateway, on answers, on secrets and on timers, and gives
   real wake latency.
4. **Oversell at 1.25,** then 1.5, after `billing.md` and `limits.md`
   say so, with an 8 GiB wake reserve per host, a 160 GiB swap partition
   and an alert on any wake that waited or failed. Hetzner first, where
   the money changes.

## 13. Risks

- A paused background job that looked quiet. The 6 s line sits close to
  the active account's idle p50.
- A wake that does not fit on a pinned host breaks R1-5 as the user sees
  it, and the fallback costs 87 s or more.
- Wake storms at the start of a workday thrash swap on a shared disk cap.
- Outbound sockets idle past 30 min on Azure, or past a remote server's
  timeout, reset, with unknown agent recovery.
- A host reboot loses the RAM state of each paused guest.
- Guest RAM, secrets included, reaches host disk through swap.
- Shmem reclaim of 30 or more paused guests on one host, with virtiofsd
  mapping the same memory, is untested on any repose host or dev box.
- Every pausable share comes from 3 accounts. The owner is the worst case
  for savings and a forgotten machine the best; real users sit somewhere
  unknown between.
- The active account's 29% gives Solo and Plus break-even and leaves Pro
  short. A lower share among real users leaves every plan short.
- The size and time figures in section 4 are estimates, and none was
  measured on a repose host.

## Decisions for the owner, in order

1. **R1-5.** Does a pause that keeps processes, memory and files, that
   each user path wakes, and that drops outbound connections idle past
   30 min on Azure or past a remote server's timeout, count as "never
   stopped"?
2. **A fourth home.** May encrypted host swap with a key per boot hold
   guest RAM? This amends `CLAUDE.md`, `features/secrets.md`, and I-235's
   preference for RAM.
3. **M2 or M3.** Swap is recommended; snapshot images stay deferred.
4. **Blockers.** Docker containers: recommended not to block. An open
   `repose-ask` or a herdr sidebar: block or not?
5. **Opt-in or opt-out.** I-235 option 5 and I-262 favour opt-in;
   section 6 drafts opt-out with a keep-awake key.
6. **Oversell.** Which factor, Hetzner only, and the wording in
   `billing.md` and `limits.md` (required before step 4).
7. **Host choice beside pause.** An LTD host at f = 1.0, an Intel EX131,
   or an AX162 that needs the Intel rule amended.
8. **A wake that does not fit:** wait up to 30 s then move, as drafted,
   and whether a wake past some limit earns a credit.
9. **The word** shown for a resting machine.
10. **Timing.** Steps 1 and 2 on Azure host-01 now; steps 3 and 4 once a
    Hetzner host is rented (section 10).

## Sources

Checked 2026-10-11.

- Cloud Hypervisor releases: https://api.github.com/repos/cloud-hypervisor/cloud-hypervisor/releases
- Cloud Hypervisor release notes: https://raw.githubusercontent.com/cloud-hypervisor/cloud-hypervisor/main/release-notes.md
- Snapshot and restore modes, main: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/snapshot_restore.md; v53.0, which lacks the vhost-user line: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v53.0/docs/snapshot_restore.md
- virtio-fs needs `shared=on`: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/fs.md
- Balloon and free page reporting: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/balloon.md and `virtio-devices/src/balloon.rs` on main
- Virtio-fs restore issue: https://github.com/cloud-hypervisor/cloud-hypervisor/issues/6931
- Kernel free page reporting: https://docs.kernel.org/mm/free_page_reporting.html
- KSM: https://docs.kernel.org/admin-guide/mm/ksm.html
- cgroup v2 `memory.reclaim` and swap controls: https://docs.kernel.org/admin-guide/cgroup-v2.html
- zswap: https://docs.kernel.org/admin-guide/mm/zswap.html
- `madvise` and `process_madvise`: https://github.com/torvalds/linux/blob/master/mm/madvise.c
- KVM clock: https://github.com/torvalds/linux/blob/master/arch/x86/kvm/x86.c
- TCP keepalive defaults: https://docs.kernel.org/networking/ip-sysctl.html
- NixOS encrypted swap: https://github.com/NixOS/nixpkgs/blob/master/nixos/modules/config/swap.nix
- Azure disk types (Premium SSD v2 limits, page updated 2026-09-16): https://learn.microsoft.com/en-us/azure/virtual-machines/disks-types
- Azure NAT gateway timeouts (4 min default, 4 to 120): https://learn.microsoft.com/azure/virtual-network/nat-gateway-resource; repose's 30 min: `infra/azure/modules/network/variables.tf:94-98`
- D64s_v7 disk throughput (third party, *unverified*): https://instances.vantage.sh/azure/vm/d64s-v7
- Samsung PM9A3 (sequential read up to 6,800 MB/s): https://semiconductor.samsung.com/ssd/datacenter-ssd/pm9a3/
- E2B persistence: https://docs.e2b.dev/sandbox/persistence
- Fly.io suspend and resume: https://docs.fly.io/reference/suspend-resume
- GitHub Codespaces timeout and 12 hour lifetime: https://docs.github.com/en/codespaces/setting-your-user-preferences/setting-your-timeout-period-for-github-codespaces
- Hetzner prices per seat: `docs/PRICING.md` "The cost floor" on branch `hetzner-repricing`
- Public seat text: `apps/web/src/content/docs/billing.md`, `apps/web/src/content/docs/limits.md`
- Repo: DECISIONS R1-5, R2-3, R3-2, I-230, I-233, I-235, I-236, I-262, I-290, I-453, I-493, I-586, I-594, I-617, I-642, I-647; RESEARCH.md §5 and §11; `docs/PRICING.md`; DESIGN.md §4; branches `hetzner-repricing` and `byo-host-shape-a`
