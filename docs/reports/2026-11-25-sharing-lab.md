# Shared memory on owned hosts: the lab, 2026-11-25

Lab: the sharing program's lab role on woker-1, two sessions (the first stopped by
accident about 19:40Z; the second re-ran what was cut short and carried on). Every
number below names its data file under `/mnt/nixstore/sharing/lab/data/` on woker-1.
Times are UTC. Nothing here is reasoned into a number; what was not run is listed at
the end.

## What the lab found

- **Memory sharing does its job up to the point the appliance is busy.** With no sizes,
  the agent whose build died of memory in a 2 GB `tiny` box got its memory. A machine
  starts only while the host reports 1 GB free, and the refusal is clean. An idle machine
  gives back about half of what it held (5.0 to 2.5 GiB; the first drop 5 minutes after it went quiet, the rest within 11).
- **Two or more busy machines on the Mac appliance are slow, at any vCPU count.** One
  guest's build costs 1.1-2.1x the same build in the appliance itself. Two guests at once
  ran 1.5-1.9x slower on the first build after boot, then 3.7x to more than 15x. The same
  two builds directly in the appliance cost 1.4-1.5x, and the Mac gave the appliance
  fewer cores with two busy guests than with one. At 18G, four builds at once each took
  10-13x as long as one alone.
- **Four busy machines on the 12G appliance hung every machine, 3 times in 3**, with 16
  or 8 guest vCPUs, with memory full or 1.3 GiB free, with and without the memory guard.
  The machines stayed `running` in `repose ls` and the dashboard; only `repose stop`
  (about 70 s each, power-off) got them back, and their work was lost. Neither zram nor
  the OOM killer ever acted on guest memory. At 18G the same four finished.
- **With shared-host's last build (owned-cpu 5d6dc807: no CPU quota, at most 3 machines,
  the memory guard), nothing hung.** Three builds at 12G: the guard stopped the newest
  machine within 1.5-10 minutes, 2 times in 2, the CLI named it, and the other two
  finished. A fourth create was refused and left nothing behind.
- **Guest RAM without huge pages is not an option on the Mac**: a build's first step took
  more than 12.7 minutes instead of about 25 s.
- **Ship blockers found and since fixed on branches:** the snapshot LV name LVM refuses
  (F8) and the snapshot move that always failed on `lost+found` (F14). Both re-tested
  green with shared-host's builds. Still open: F1's root-disk snapshots on appliances
  that cannot get the volume (pool past LVM's threshold, F15), a refused create left
  behind in state `error` on the CPU path before r20261126-2 (F2), `host ls` memory never
  updated (F10), park unavailable on this Mac (F12).

## Setup

- Mac: AWS mac-m4.metal, eu-west-1b, macOS 15.8.3, M4, 10 cores, 24 GiB. Host name
  `e2e-mac`, joined to the owner's production account with CLI v0.1.65 (darwin/arm64,
  from the release), `repose host add e2e-mac --memory 12288`: exit 0 in 6 s, online
  17:27:51Z. Appliance image 2026.11.22.4, 8 vCPUs, MemTotal 12242280 kB.
- Baseline: the released hostd and api (classes, default `large`).
- Sharing: hostd from branch shared-host, swapped into the running appliance, with
  `--owned-host` and `repose-zram` (zram0 5.8 GiB, zstd): 688f1212 (18:41Z), 3bd57e7e
  (`--owned-vcpus N`, 18:50Z), 85e53826 (`--owned-vcpus` + idle threshold 10 %, 19:15Z).
  The api side (r20261125-4, owned placement gated on Hello feature `shared`) is live
  on production since 18:54Z. For `ls MEMORY` and the new refusals the CLI is built
  from main 1a04c91f (source tree checked identical to `git archive 1a04c91f` for cmd,
  internal, proto, go.mod, go.sum).
- Lab-only workaround, disclosed: the released appliance keeps snapshots on its 2 GiB
  root, which filled at the fifth stop (finding F1). A thin LV `lab-snapshots` (30 GiB)
  is mounted over `/var/lib/repose/snapshots` from 18:40Z, grown to 60 GiB at 00:20Z; from
  02:11Z the product's own `repose-snapshots` LV holds the snapshots (the move test).
- Workloads. **H**: one fixed heavy build, clean each time with warm package caches:
  recruiting (pnpm + turbo, 7 tasks), unwrap (`next build`), pocketmail server and web
  (tsc, vite). **P**: CPU-parallel, no network: `go build -a -p $(nproc) std cmd` with an
  empty GOCACHE. **Agent**: Claude Code on Sonnet (`claude -p --model sonnet
  --dangerously-skip-permissions --output-format json`), one fixed task per repo (install,
  build, test, a small change with a test, commit locally, never push), in the machine's
  tmux. Every clone has its push URL set to DISABLED-NO-PUSH.
- Sampling every 5 s: in the appliance (`appliance.csv`: meminfo, zram mm_stat, PSI
  memory, oom_kill, each guest@ cgroup memory.current, CH/virtiofsd/hostd RSS), on the
  Mac (`mac.csv`: VZ VM footprint, swap, vm_stat, pressure level), kernel OOM lines
  (`oom.log`). Per run: start, end, exit, wall/user/sys (`runs.jsonl`).

## Baseline, 12G, today's released pieces (first session)

Classes as released: on a 12 GiB appliance only one `large` fits, or three `tiny`, or
one `small` and two `tiny`.

| Scenario | Machines | Result | Host side (window) |
|---|---|---|---|
| S0 refusal | second `large` while one runs | exit 8: `your host e2e-mac has 1.2 GB of memory free for machines, and a large machine needs 8 GB. A machine holds its memory while it runs, so stopping one frees room for it.` Nothing created. | |
| S1 | H on `large` (2 vCPU) alone | wall 64.2 / 62.9 / 62.0 s, user 61-65, sys 20-21, rc 0 | guest peak 6.16 GiB, MemAvailable min 5.11 GiB, swap 0, PSI 0, oom_kill 0; Mac VZ footprint peak 11.0 GiB, Mac swap 0 (`win-b12-S1.txt`) |
| S2 | 3 Claude agents at once, each on `tiny` (2 GB, 1 vCPU) | cutroom 83 s, 6 turns: install pass, build pass, test fail, change pass, commit. unwrap 201 s, 9 turns: all pass, commit. recruiting 1248 s, 11 turns: install pass, **build fail, test fail**, change pass, commit; the guest kernel OOM-killed 2 processes inside the tiny guest (guest swap 435 MB) | guests.slice peak 5.95 GiB (each tiny pinned at 2.0 GiB), MemAvailable min 5.23 GiB, PSI 0, appliance oom_kill 0 (`win-b12-S2.txt`). The host had more than 5 GiB idle while one agent's build died of memory in its 2 GB box. |
| S3 | H on `small` (1 vCPU, 4 GB) while 2 `tiny` sit with Claude open, 2 stopped | wall 106.8 / 141.0 s; `small` alone 145.1 / 130.7 s | guests.slice peak 7.58 GiB, MemAvailable min 3.69 GiB, PSI some avg10 peak 0.55, oom 0 (`win-b12-S3.txt`) |
| S4 | H on `small` while cutroom (`tiny`) stops and starts | wall 159.1 s; 4 starts 59 / 43 / 48 / 31 s, stops 0.5-2.4 s, all exit 0 (`churn.csv`) | MemAvailable min 5.88 GiB, PSI 0 (`win-b12-S4.txt`) |
| S5 refusal | 4th `tiny` while 3 `tiny` run | exit 8: `Could not start e2e-pocketmail: your host e2e-mac has 2.2 GB of memory free for machines, and a tiny machine needs 2.5 GB there. A machine holds its memory while it runs, so stopping one frees room for it (insufficient_capacity).` The refusal came after `Creating e2e-pocketmail...` and left the project in state `error` (finding F2). | |
| S5 | H on `small` + 2 `tiny` at once (the most that fits) | recruiting (`small`) rc 0, 194.9 s (vs 130.7-145.1 alone); unwrap and cutroom (`tiny`) rc 134 at 174 / 181 s, each log `JavaScript heap out of memory`; no guest kernel OOM kill | guests.slice peak 7.95 GiB, MemAvailable min 3.31 GiB, PSI some avg10 peak 1.85, appliance oom_kill 0, Mac footprint 12.0 GiB, Mac swap 0 (`win-b12-S5.txt`). 3.3 GiB idle while two builds died in 2 GB boxes. |

Reading: on today's classes the failures are inside the boxes, never on the host. The
host never swapped, never reached PSI above 2, and always had 3.3 GiB or more free.

## Measurements for shared-host (first session)

**The owned reserve (I-1628).** 12G appliance, released hostd, one idle `large`
(e2e-citebook), 24 samples x 5 s, 17:36:29Z-17:38:31Z (`reserve-idle1.csv`). VmRSS
peak / median kB: hostd 32936 / 26440 (cgroup anon 12.2 MB; its 2.95 GB of file cache is
the base image, reclaimable); dmeventd 21528; systemd 13956; resolved 13832; networkd
10780; journald 10404; udevd 9600; timesyncd 7692; logind 5008; dbus 2736; 2 agetty
2436. The appliance's own processes sum to about 131 MB at peak. Per guest:
cloud-hypervisor holds 3.7 MB of its own (RssAnon 1076 + RssFile 2660 kB; its RssShmem
is the guest's RAM), virtiofsd-auth 0.8 MB anon + 2.5 MB file. cgroups: system.slice
3.09 GB (anon 39.6 MB, file 3.01 GB, kernel 97.9 MB); init.scope 3.6 MB. meminfo: Slab
142320 kB, KernelStack 3584, PageTables 9884 peak. shared-host set the reserve to 512 MiB
from this.

**Idle guest RAM held on the host (released hostd).** One `large` idle after builds
(`chmem.csv`): CH RssShmem 6.28 GiB. After `sync; drop_caches` in the guest: 4.24 GiB in
10 s, flat for 70 s. The guest then had about 3.6 GB of its free memory in blocks
below 2 MiB (`/proc/buddyinfo`), which free page reporting (order 9) cannot return.
After `compact_memory` in the guest: 1.82 GiB in 5 s, flat after. Appliance MemAvailable
5.00 -> 7.02 -> 9.45 GiB. shared-host's idle drop now runs all three (688f1212).

**Idle reclaim did not fire on 688f1212.** e2e-citebook (8 vCPU) idle 18:49:56Z-18:58:26Z:
RssShmem flat at 4.56 GiB for 100 samples, no `guest_reclaim` line. The idle guest used
2.85 s of host CPU per 60 s (4.7 % of one vCPU; inside the guest 1.3 %), above the 2 %
threshold. shared-host raised it to 10 % (reclaim-idle, 85e53826).

**Idle reclaim on 85e53826 fires** (second session). e2e-citebook, 8 vCPUs, idle from
20:48:49Z (the end of its last build; `chmem.csv` tag `idle-85e5`, 180 x 5 s).
`guest_reclaim reason=idle result=ok` at 20:53:51Z and again at 20:59:50Z. CH RssShmem
5.01 GiB until 20:53:37Z, 3.29 GiB at 20:54:08Z, 2.83 GiB by 20:59Z, 2.47 GiB from
21:00:02Z to the end at 21:04Z. The guest thus held 2.5 GiB of the host's memory idle,
down from 5.0. mac-arch's window ran on the Mac at the same time (IO in the appliance),
so the appliance's MemAvailable from these minutes is not quoted.

**Sharing turned on (688f1212, 18:41Z).** Journal `guests share the host`,
slice_max_bytes 11999223808, reserve_bytes 536870912, vcpus 8; zram0 5.8 GiB zstd. On
today's api, `repose start` of e2e-recruiting, unwrap, cutroom, citebook all accepted,
ready in 11 s each; every guest `boot=8 size=10880M`; CH oom_score_adj 250 / 500 / 750 /
1000 in start order (newest 1000).

## vCPUs: one guest, then two at once

S1 on sharing with all 8 vCPUs (688f1212, 18:42-18:47Z): H wall 104.7 / 82.7 / 79.3 s,
sys 55.9 / 52.5 / 51.3 s, against 62-64 s (sys about 20 s) on the 2-vCPU `large`. Pinned
with taskset in the same 8-vCPU guest, n=1: 2 CPUs 61.1 s (sys 20.0), 4 CPUs 69.9 s
(sys 40.1).

Single-guest sweep, hostd 3bd57e7e `--owned-vcpus N`, e2e-citebook alone, stop+start per
N, run 1 is the first after boot (`runs.jsonl`, `sweep.log`). Wall s / user s / sys s:

| N | H run 1 | H run 2 | H run 3 | P run 1 | P run 2 | P run 3 |
|---|---|---|---|---|---|---|
| 2 | 90.5 / 71.1 / 23.3 | 61.9 / 62.7 / 20.2 | 65.4 / 59.2 / 18.8 | 49.5 / 84.2 / 9.4 | 50.6 / 84.3 / 9.0 | 49.1 / 84.5 / 8.8 |
| 4 | 96.3 / 81.8 / 44.0 | 84.7 / 81.0 / 47.8 | 87.1 / 83.7 / 47.7 | 60.9 / 167.3 / 63.5 | 43.3 / 121.2 / 33.0 | 44.1 / 121.7 / 32.5 |
| 6 | 96.6 / 89.8 / 52.9 | 79.5 / 82.6 / 51.1 | 83.2 / 86.3 / 52.1 | 64.8 / 222.0 / 138.4 | 37.2 / 136.1 / 35.5 | 32.2 / 130.5 / 26.4 |
| 8 | 98.2 / 93.7 / 55.7 | 74.5 / 81.4 / 51.2 | 119.4 / 123.6 / 84.3 | 150.6 / 566.5 / 405.7 | 42.4 / 180.7 / 71.3 | 35.3 / 172.0 / 58.8 |

H (node builds, little parallelism) is fastest at 2 vCPUs. P is fastest at 6 (32-37 s
after the first run); 8 is no better than 6, and its first run took 4x. CPU time per
unit of work rises with the vCPU count. The cause of the extra sys time is unverified
(candidates: nested IPIs and TLB shootdowns across vCPUs; vCPU threads contending with
the appliance's own); perf is not in the appliance.

**Two guests at once** (second session; hostd 85e53826, e2e-citebook and e2e-recruiting,
both at the same N, both restarted per N; files `pair-n4.log`, `pair28.log`, timings in
each guest's `~/lab/p*.time`). Wall s / user s / sys s, each guest:

| N | First pair after boot (H) | Later pairs, H | Later pair, P | H alone at N | P alone at N |
|---|---|---|---|---|---|
| 2 | 115.3 / 85.8 / 27.8; 115.2 / 86.4 / 27.9 | 296.7 / 298.6 / 123.4; 284.2 / 249.7 / 134.1 | 180.9 / 236.0 / 95.9; 188.1 / 222.7 / 127.1 | 61.9-65.4 (90.5 first after boot) | 49.1-50.6 |
| 4 | 157.3 / 120.2 / 79.8; 158.6 / 125.0 / 80.4 | 572.5 / 725.9 / 419.4; 577.2 / 746.0 / 441.8; then 578.5 / 668.8 / 405.5; 582.3 / 818.1 / 438.9 | more than 824 s, both; I cut it at 20:22:50Z | 84.7-87.1 (96.3) | 43.3-44.1 (60.9) |
| 8 | 154.2 / 141.1 / 110.2; 150.4 / 146.1 / 107.6 | not run | 600 s timeout, both rc 124 (recruiting: user 1207, sys 1989 in 600 s) | 74.5-119.4 (98.2) | 35.3-42.4 (150.6) |

The first pair after a boot costs 1.5-1.9x a single build. Every later pair on the same
boots costs 3.7x to more than 15x, at every N. The vCPU count is not what makes two busy
guests slow, and no N makes them fast.

Host side in the slow windows: MemAvailable min 0.95-1.0 GiB at N=4, guests.slice peak
10.0-10.3 GiB, zram 0, oom_kill 0, PSI memory some avg10 peak 3.03. But N=2 P was 3.7x
slower with 4-5 GiB free throughout (20:31-20:34Z), so a full host is not the whole
story. hostd logged `guest_reclaim reason=pressure` three times for both guests:
19:56:50Z and 20:14:50Z `result=failed`, 20:29:04Z `result=ok`.

The Mac side, sampled from 20:04Z (`maccpu.csv`, top's %CPU of the VZ process every
5 s): mean 183-293 %, max 689 %, in every pair window; the Mac was 70-82 % idle at
20:16Z. The appliance reported about 55 % of its 8 vCPUs busy (`/proc/stat`, 10 s, steal
0). So the appliance's vCPUs are not getting the Mac's cores. Cause unverified.
Candidates: re-faulting guest RAM that reclaim and free page reporting gave back, which
under nesting is a stage-2 fault through two hypervisors; THP shmem compaction in the
appliance.

Two checks ruled those two candidates out (`slot.log`):

- **hostd's drop switched off.** shared-host's lab-only build 892f9768 with
  `--owned-reclaim off` (no drop at all), N=4, both guests fresh. The first pair after
  boot took 643.3 / 648.5 s (user 760 / 879, sys 416 / 423); the next pair hit my 700 s
  cap on both (rc 124, 21:39:34-21:51:15Z). I cut the third. So with no drop the pair is
  still more than 7x slower. One confound for the first pair only: mac-arch's 3-VM run
  had pushed the idle appliance into macOS swap (1.3-1.7 GB swap and 2.6 GB compressor at
  21:30Z, against 0 and about 55 MB through 19:40-20:50Z, `mac.csv`). This is also why the
  first pair here was slow when the first pairs at 19:46-20:36Z were not.
- **Forced reclaim, one guest.** e2e-citebook alone, N=4, same build: H 92.3 (first after
  boot) / 78.6 / 75.4 s; then `sync; drop_caches; compact_memory` in the guest and 60 s,
  H 76.8; again, H 76.7. Sys 41-45 s throughout. Re-faulting RAM the guest gave back does
  not slow one guest.

What remains: two nested guests busy at once. Mac side during the reclaim-off pair: P-cores
(CPU 6-9) 221 % of 400 busy, E-cores (CPU 0-5) 25 % of 600 (`pm.csv`, powermetrics);
the appliance runs as launchd ProcessType Interactive, the VZ process at PRI 46, nice 0.
mac-arch measured the same H with each sandbox directly on Apple's hypervisor (design 2,
no nesting): pairs at 4 and 8 vCPUs at single-VM speed (H 47.0-49.6 s), see
`docs/reports/2026-11-25-mac-architectures.md` on branch mac-arch.

## Nesting: the same builds in a guest (L2) and in the appliance (L1)

L1 is the guest's own erofs root plus its home LV in a chroot directly in the 8-vCPU
appliance, the same checkouts and caches, `taskset` to the CPU count
(`share/l1.sh`; rows in `l1.csv`; 22:04-22:23Z). The first L1 attempt ran nothing (rc
127: the LV's home is its `home-dev/` subdirectory); those rows are kept apart as
`l1-rc127-mounterror.csv`. L2 figures are the single-guest sweep above, runs 2 and 3.
Wall s, n=3, with user / sys:

| CPUs | L1 H | L2 H (runs 2-3) | L2/L1 H | L1 P | L2 P (runs 2-3) | L2/L1 P |
|---|---|---|---|---|---|---|
| 2 | 52.9 / 47.5 / 49.5 (29-30 / 33-35) | 61.9 / 65.4 | 1.31 | 55.5 / 46.3 / 45.5 (77-78 / 12-13) | 50.6 / 49.1 | 1.09 |
| 4 | 41.4 / 44.6 / 45.0 (31-32 / 41) | 84.7 / 87.1 | 1.92 | 27.2 / 26.1 / 26.2 (88-89 / 14) | 43.3 / 44.1 | 1.67 |
| 8 | 45.0 / 48.2 / 45.6 (36-37 / 43) | 74.5 / 119.4 | about 2.1 | 23.5 / 19.6 / 19.6 (118-119 / 21) | 42.4 / 35.3 | about 2.0 |

Ratios use the medians of the L1 runs and the mean of the L2 runs 2-3. In L1 the Go
build scales to 8 CPUs (19.6 s); in L2 it stops at 6 (32-37 s).

**L1 pairs**: two chroots at once (two guests' LVs), CPUs 0-3 and 4-7. H 67.0 / 68.5 s,
then 58.7 / 67.2 s; P 40.6 / 40.8 s, then 39.5 / 36.3 s. Against one L1 build at 4 CPUs
(H 41-45, P 26-27) that is 1.4-1.5x. Two builds at once are fine in the appliance; the
collapse comes only with two nested guests.

**Mac cores per window** (`pm.csv` per-CPU active residency summed by cluster;
`maccpu.csv` top's VZ %CPU):

| Window | E-cores (of 600 %) | P-cores (of 400 %) | VZ process |
|---|---|---|---|
| L1 pair 22:19:46-22:23:30Z | 183 % | 284 % | 439 % |
| L1 single 22:05-22:19Z | 45 % | 185 % | 209 % |
| L2 single, N=4, 21:52-22:01Z | 23 % | 193 % | 198 % |
| L2 pair, N=4, reclaim off, 21:39-21:51Z | 20 % | 174 % | 159 % |

With two nested guests busy the appliance gets fewer Mac cores than with one, while
the same two builds directly in the appliance get 4.7 cores. Cause unverified: it
looks like the Mac's handling of nested guests' exits is serialised; nothing in these
runs suggests a hostd setting changes it. The memory side of the macOS footprint is F3.

## S1-S5 on sharing, 12G (second session)

hostd 85e53826 as it ships (reclaim on), `--owned-vcpus 4` (shared-host's answer at
21:27Z; the conductor did not override), api r20261125-4, CLI from main 1a04c91f. Every
machine has no size: four e2e machines plus the fifth created under load in S5. Windows
in `swin.csv`, runs in `runs.jsonl` and `s12.log`.

| Scenario | Result | Host side (window) |
|---|---|---|
| S1: H alone on e2e-citebook | 91.0 (first after boot) / 75.2 / 79.1 s, rc 0 (baseline `large`, 2 vCPU: 62-64) | MemAvailable min 5.40 GiB, guests.slice peak 5.72 GiB, zram 0, PSI 0 |
| S2: 3 Claude agents at once | cutroom 570 s, 15 turns, all pass, commit. unwrap 1306 s, all pass, commit. recruiting hit my 1800 s cap (rc 124, no result). The agents ran a second pass on repos with the first session's commits, so the work differs from the baseline's. | MemAvailable min 0.83 GiB, guests.slice peak 10.43 GiB, zram 0, PSI some avg10 peak 3.2, oom 0. Baseline: recruiting's build died of memory in its 2 GB box. |
| S3: H with 2 machines idle at a Claude prompt, 2 stopped | 94.4 (first after boot) / 109.5 s, rc 0 | MemAvailable min 1.80 GiB, PSI some peak 1.74. The idle reclaim had taken unwrap and cutroom from 3.35 / 2.82 GiB to about 0.48 GiB each (`repose ls` MEMORY 475.0 MB, 485.7 MB at 23:36Z) |
| S4: H while cutroom stops and starts, 6 cycles | H 234.7 s (2.6-3.1x S1). Starts 33-52 s each (11 s on a quiet host), stops 0.8-4.9 s, all exit 0 (`churn.csv`) | MemAvailable min 2.93 GiB, PSI some peak 0.87 |
| S5: 4 H at once, 4 x 4 vCPU = 16 on the appliance's 8 | A fifth machine created under the load (`repose run e2e-pocketmail -d`, 23:48:58Z): exit 8, `your host e2e-mac has 0.8 GB of memory free, and a machine starts there when it has at least 1 GB free. Stopping a machine frees the memory it uses. ` + "`repose run --host repose` makes it on repose's servers instead." Nothing was left behind. Then **all four machines hung** (below). Every build result lost. | MemAvailable min 0.67 GiB, guests 5.06 / 2.25 / 1.63 / 1.74 GiB, zram 0.1 GiB in use, PSI memory some peak 2.94, oom_kill 0: memory nearly, not fully, used |

**The S5 hang.** From about 23:49Z every guest's console went silent (last line
23:49Z). From 23:55Z ssh through the gateway answered `cannot reach environment: no
route to host`; from the appliance, ARP to every guest was FAILED and tcp/22 closed.
The guests were not dead: each guest@ cgroup used about 2 CPUs (19-21 s per 10 s), the
appliance was 100 % busy (user + sys, idle 0 over 5 s), PSI cpu some avg10 94 %, and the
Mac was 80 % busy. `repose ls` and `repose host ls` said running and online throughout;
MEMORY showed `-`. No guest came back in 20 minutes. Stopping cutroom (00:09Z), then
unwrap and recruiting (00:13Z) did not bring the rest back; citebook alone at 4 vCPUs
was still unreachable at 00:17Z. Each `repose stop` answered exit 0 in about 3 s with
`checkout unknown (ssh failed: cannot reach environment: no route to host)`, then took
67-70 s to `stopped`: hostd's snapshot freeze timed out (`snapshot_fail
code=guest_unresponsive`), the guestd shutdown timed out, and hostd powered the guest off
through the hypervisor.

The stop snapshots then failed `no space left on device` on my 30 GiB lab-snapshots LV (29
snapshot files, nothing deleted them: owned-host retention is on PR #214, not live). The
projects still ended `stopped`. I grew the LV to 60 GiB (lab only).

Reading at the time (00:19Z) was "16 vCPUs on 8 hang". The later runs correct it: the
second 12G S5 (8 vCPUs on 8, below) hung the same way with the appliance's memory full,
and the 18G S5 (8 on 8, 2.04 GiB free at worst) finished. In this run the appliance had
0.67 GiB available at worst and guests.slice was 0.6 GiB under its maximum. What the data
shows is "appliance memory nearly full, then every machine hangs"; whether the vCPU count
adds to it is not shown. The refusal was clean and the host never OOM-killed anything.

## host-files on a 2026.11.22.4-state appliance (shared-host's 19:07Z ask)

State made: released hostd and hostd.env (no `--owned-host`), zram off and reset,
repose-zram and repose-snapshots-volume files and units removed. Deviation: my
lab-snapshots LV stayed mounted on `/var/lib/repose/snapshots` (its 29 snapshot files, more
than 11 GB, do not fit the 2 GiB root), so the move itself was not exercised.
e2e-cutroom and e2e-unwrap running. Then hostd-arm64-host-files-5e8a44ab (sha256
d70e943b...) installed as `/usr/local/bin/hostd` and `systemctl restart hostd` (00:20:55Z).

- First start: `host_file` events for the 4 files and `repose-zram.service` started
  (`swapon` shows /dev/zram0 5.8G). Then `host files not complete; the next start tries
  again`: `systemctl enable --now repose-snapshots-volume.service` failed. The unit's log:
  `Names starting "snapshot" are reserved. Please choose a different LV name.` (exit 3).
  `repose-snapshots-volume` runs `lvcreate -q -y -V <n>G -T vg-guests/thin -n snapshots`;
  LVM 2.03.31 (the appliance's) refuses that name. The unit test stubs `lvcreate`, so it
  cannot see this; `ops/install-host-x86.sh:810` runs the same `lvcreate` (its LV name not
  checked by me). hostd.env unchanged, no self-restart.
- Second start (00:22:10Z): no retry of the volume unit; hostd.env written with
  `--owned-host`, `hostd.env changed; restarting with it`, one restart.
- Both machines kept running (same boot_id, uptime 38 -> 136 s). Snapshot files 29 before
  and after.

So, on this branch: every appliance keeps its snapshots on the 2 GiB root (F1 stays),
`--owned-host` arrives only at the second hostd start, and after one failure the volume
step is not tried again. Not run: the move of existing snapshots, and the x86 installer.

**Re-test with the fix** (shared-host's combined build 62620126, sha256 8535dddab4...:
LV renamed `repose-snapshots`, a failed unit retried before `--owned-host` is written).
12G appliance put back to the 2026.11.22.4 state as above, plus the
`vg-guests/repose-snapshots` LV that the combined build had made at 00:30Z removed. Same
deviation (my LV mounted, 46 snapshot files). e2e-cutroom and e2e-unwrap running.
Install and restart at 01:27:53Z:

- `host_file` events for the 4 files; `host unit started` for repose-zram and
  repose-snapshots-volume; then hostd.env written with `--owned-host`, `hostd.env
  changed; restarting with it` (written 5). Two starts in all: mine and its one
  self-restart.
- zram active (/dev/zram0 5.8G); the volume unit active with
  `event=volume_mounted_already`, because the directory was my mount. The LV-creation
  path ran earlier with this build, at 00:30:38Z: `Logical volume "repose-snapshots"
  created.`, `event=volume_created size_gib=48`.
- Both machines kept running (same boot_ids; uptime 35.7 -> 95.6 -> 118.5 s and 14.5 ->
  73.4 -> 96.3 s). Snapshot files 46 before and after.
- A second restart (01:28:52Z): no `host_file` events, one start.

Pass for the LV name and for completing in one pass. Still not run: moving existing
snapshot files off the root, and the x86 installer.

## 18G (second session)

`VM_MEMORY_MIB=18432` in the Mac's appliance config, then `launchctl kickstart -k
system/run.repose.appliance` (00:23:23Z); back on the network at 00:23:48Z with MemTotal
18398548 kB, 8 vCPUs, zram 8.8G, `slice_max_bytes 18303242240`. hostd: shared-host's
combined build 62620126 (I-1640: 2 vCPUs per guest on a nested host, guests.slice
CPUWeight 50 and CPUQuotaPerSecUSec 7s, a start refused when the running guests' vCPUs
plus the new one exceed the appliance's; every guest's ch.args `boot=2`). The conductor
cut 18G to S1, S2 and S5; S3 and S4 also ran (see the mishaps below).

| Scenario | Result | Host side (window) |
|---|---|---|
| S1: H alone | 87.8 (first after boot) / 67.7 / 60.9 s, rc 0. (With 85e53826 `--owned-vcpus 2`, before the swap: 73.6 / 62.7 s.) | MemAvailable min 11.07 GiB, guests.slice peak 6.46 GiB, zram 0, PSI 0 |
| S2: 3 Claude agents | unwrap 62 s, 5 turns, all pass, commit. cutroom 120 s, 6 turns, test fail, commit. recruiting 1219 s, rc 0, build pass, test fail, commit (it ran a Postgres container for its tests). Third pass on these repos. | MemAvailable min 7.13 GiB, guests.slice peak 10.5 GiB, zram 0, PSI 0, oom 0 |
| S3: H with 2 idle Claude machines, 2 stopped | 80.4 (first after boot) / 60.3 s | MemAvailable min 8.89 GiB, PSI 0 |
| S4: H while cutroom stops and starts | H 85.5 s; starts 34 / 31 / 40 s, stops 0.6-1.6 s, exit 0 | MemAvailable min 9.09 GiB, PSI 0 |
| S5: 4 H at once (4 x 2 vCPU = 8 on 8) | All rc 0, no hang (every guest answered ssh): 801.8 / 811.3 / 802.8 / 810.4 s (user 717-814, sys 292-343): 10-13x S1 each. A fifth create at 01:15:35Z: exit 8, `Creating e2e-pocketmail... Could not start e2e-pocketmail: the host has no room for this project right now (insufficient_capacity).`, and the project was left in state `error` (F2 again, on the CPU refusal; the api then live was r20261125-4). | MemAvailable min 2.04 GiB, guests.slice peak 15.02 GiB (each guest 3.80-3.96), zram 0, PSI some avg10 peak 2.17, oom 0. Mac: VZ footprint 18 GB, **macOS compressor peak 8.2 GiB**, swap 170 MB; E-cores 49 %, P-cores 264 %, VZ 265 % |

So at 18G and 2 vCPUs per guest, four busy machines finish and nothing hangs, but each
build takes 10-13x as long as alone, and an 18 GB appliance on a 24 GB Mac makes macOS
compress 8 GB of memory.

**Mishaps in this part, disclosed.** (1) The appliance reboot re-enabled
`repose-hostd-update.timer`, which at 01:00:15Z replaced my swapped hostd with the
released 2026.11.22.4 (`event=updated from=dev to=2026.11.22.4`). Until 01:09Z the host
was on classes again (a start was refused `a small machine needs 4.5 GB there`). I
stopped the timer and reinstalled 62620126; S5 ran after that. (2) My first 18G driver
(85e53826 era, `--owned-vcpus 2`) survived my kill and ran beside the second. Both used
the same S2 agent runs (one set; tmux refused duplicate sessions). Its S3 and S4 ran on
62620126 before 01:00:15Z and are kept above; its S5 is void.

## 12G again, with the I-1640 hostd (second session)

Back to `VM_MEMORY_MIB=12288` (01:26Z), hostd 62620126, api r20261126-1 (live by then).

- **S2, 3 Claude agents**: all rc 0. unwrap 101 s, 8 turns, all pass, commit. cutroom
  128 s, 7 turns, test fail, commit. recruiting 325 s, 12 turns, build pass, test fail,
  commit. Fourth pass on these repos. MemAvailable min 1.71 GiB, guests.slice peak
  9.53 GiB, zram 0, PSI some peak 1.26, oom 0.
- **S5, 4 H at once (4 x 2 vCPU)**: a fifth create at 01:49:24Z: exit 8, `your host e2e-mac
  has 0 GB of memory free, and a machine starts there when it has at least 1 GB free.
  Stopping a machine frees the memory it uses. ` + "`repose run --host repose` makes it on
  repose's servers instead.", nothing left behind. Then **all four guests hung again**:
  consoles silent from 01:46:0xZ, two minutes in; ARP FAILED; ssh `no route to host`;
  each guest burning about 1.75 of its 2 vCPUs; the appliance about 88 % busy.
  guests.slice `memory.current` 11993468928 against `memory.max` 11999223808 (events
  `max 38`, `oom 0`, `oom_kill 0`); MemAvailable 0.11 GiB; Shmem 11.14 GiB, of which
  ShmemHugePages 9.13 GiB and ShmemPmdMapped 9.06 GiB; zram unused (guests.slice
  `swap.current` 3 MB, swappiness 150, `pswpout 677`, `thp_swpout 0`, `thp_split_page 0`);
  `pgscan_direct 571070`, `pgsteal_direct 568167`, `compact_stall 6969`. Guest RAM is
  huge-page shmem (Cloud Hypervisor's memfd with THP, I-1574), and at the slice limit the
  kernel neither moved it to zram nor OOM-killed. No self-recovery in 18 minutes.
  Stopping cutroom took 69 s and freed memory (MemAvailable 0.12 -> 2.45 GiB), but the other
  three stayed unreachable 3 minutes more; stopping them took 72 s. All builds lost. The
  same run at 18G (2.04 GiB free at worst) finished.

The dashboard during this hang (review-dash, 01:55-01:57Z): every machine `running`, the
host `online, 4 machines, 4 running`, the list's Memory column `-`, nothing saying the
machines do not answer.

## The snapshot move, and the memory guard (second session, 02:09-02:56Z)

**Move path, combined 62620126** (before the fix). Two real cutroom snapshots on the root's
`/var/lib/repose/snapshots` (the 18:26Z and 18:42Z stops, `.img.zst` + `.img.zst.meta`, 4
files, 645431763 B), no repose-snapshots LV, my LV unmounted. hostd's start at 01:41:12Z:
`Logical volume "repose-snapshots" created`, `event=volume_created size_gib=48`, then
`event=snapshots_not_moved reason=copy_failed`, unit exit 1, nothing mounted, the root's
files untouched. By hand: `cp -a` rc 0, but `diff -rq $dir $tmp` printed `Only in
/run/...: lost+found` (mkfs.ext4 makes it) and returned 1. So every appliance with
existing snapshots kept them on the root. My first attempt at this was void: the unit was
still `active (exited)` from boot, so host-files counted it done.

**Move path, memguard dd2d64c7** (shared-host's build with the lost+found fix). Same
setup. The first start at 02:09:56Z failed: `Cannot create new thin volume, free space in
thin pool vg-guests/thin reached threshold.` (pool 82 %, mostly my lab LV). I deleted 27 old
lab snapshot files and trimmed (pool 51 %). The next start at 02:11:05Z: `Logical volume
"repose-snapshots" created`, `event=volume_created size_gib=48`, `event=snapshots_moved
files=4`, `event=volume_mounted`. The directory on `/dev/mapper/vg--guests-repose--snapshots`
held 4 files, 645431763 B, sha256 identical; 0 files left under the mount; the root went
from 979 MB to 334 MB used. `repose snapshots restore e2e-cutroom b5f8449e --as
e2e-cutroom-moved`: exit 0, `Restored e2e-cutroom-moved from e2e-cutroom's 264.2 MB
snapshot of 2026-11-25 18:26 in 4m48s; it is running (tiny).`, checkout at the commit that
snapshot holds. Pass. Two notes: a customer whose thin pool is past LVM's threshold gets no
snapshot volume, and host-files retries on every start; and 4m48s to restore 264 MB.

**Guest RAM without huge pages** (dd2d64c7 default, `--memory size=10880M,shared=on,thp=off`
in ch.args), e2e-citebook alone, 12G. The H build started 02:26:02Z was still in its first
step (recruiting, about 25 s with THP) at 02:38:47Z, 12.7 minutes in; I cut it. Three
iterations of `claude --version` then `claude -p --model sonnet "say ok"` took 150 / 116 /
113 s each in all (my per-command timer failed: `bc` is not in the guest). S5 with thp off
was not run: builds that slow do not reach memory pressure in any useful time. I-1574's
finding holds, and for builds it is worse than its 6-12x.

**With `--owned-guest-thp`** (huge pages back), dd2d64c7, 12G:

- One guest: `claude --version` / `say ok` 0.22 / 12.95 s cold, then 0.07 / 4.50 and 0.07
  / 3.82 s. H 81.3 (first after boot) / 61.6 s.
- S5, 4 x 2 vCPU builds from 02:44Z. A fifth create at 02:49:08Z: exit 8, `Creating
  e2e-pocketmail... Could not start e2e-pocketmail: the host has as many machines running
  as its cores allow; stopping a running machine frees room (insufficient_capacity).`, and
  nothing left behind (api r20261126-2). Then **all four guests hung a third time** (ssh
  `no route to host` at 02:51Z, ARP FAILED or INCOMPLETE). This time memory was not full:
  MemAvailable 1.31-1.34 GiB, guests.slice 10.2 GB of its 11.17 GB maximum, zram 0, PSI
  memory about 0, and the guard never fired (free stayed above its 512 MiB). Compaction
  and reclaim counters did not move over 10 s. What did move: each guest burned about 1.75
  of its 2 vCPUs, and guests.slice `nr_throttled` rose 16582 -> 16682 over 100 periods: the
  slice hit its CPUQuota of 700 % in every period. No recovery in 3 minutes; stopping all
  four took 85 s.

**What the four S5 runs on sharing show together:**

| Run | Appliance | Guest vCPUs | CPU quota on guests.slice | Memory at worst | Outcome |
|---|---|---|---|---|---|
| 23:47Z | 12G | 4 x 4 = 16 | none (85e53826) | 0.67 GiB free | all hung |
| 01:10Z | 18G | 4 x 2 = 8 | 700 % | 2.04 GiB free | all finished, 10-13x slower |
| 01:44Z | 12G | 4 x 2 = 8 | 700 % | 0.11 GiB free, slice at max | all hung |
| 02:44Z | 12G | 4 x 2 = 8 | 700 % | 1.31 GiB free | all hung, slice throttled every period |

Four busy nested machines on the 12G appliance hung 3 times in 3; on 18G they finished 1
time in 1. The mechanism is not shown. Candidates: preempting or throttling nested
guests' vCPUs while a guest kernel holds a lock; direct compaction for huge pages near
the memory limit. In no run did zram take guest memory or the kernel OOM-kill a machine,
so ruling 3 (overflow is slowness) and ruling 4 (the least valuable machine is killed and
the user told) were not seen working on this appliance.

## Three busy, owned-cpu 5d6dc807 (second session, 03:04Z-)

shared-host's build after the hangs (I-1640 amended): guest THP on by default, no CPU quota
on guests.slice (`CPUQuotaPerSecUSec=infinity`, `CPUWeight=50`), at most 3 machines of 2
vCPUs on the 8-vCPU appliance. 12G.

- A fourth create while three build (03:09:06Z): exit 8, `Creating e2e-pocketmail... Could
  not start e2e-pocketmail: the host has as many machines running as its cores allow;
  stopping a running machine frees room (insufficient_capacity).`, nothing left behind.
- Run 1, H on citebook, recruiting, unwrap at once from 03:05Z. At 03:14:47Z the memory
  guard fired: `guest stopped: the host is out of memory`, `event=guest_memory_guard
  free_bytes=422199296 running=3`, on e2e-unwrap, the newest; then `reason=oom_killed`,
  state `stopped`. The CLI: `e2e-unwrap: stopped: your host e2e-mac ran out of memory, and
  the kernel stopped e2e-unwrap to keep the others running. ` + "`repose start e2e-unwrap`
  starts it again." (`status` adds `last event 6m ago, stopped, host out of memory`). It
  was hostd's guard, not the kernel, that stopped it. The other two answered ssh
  throughout (03:09Z, 03:14Z, 03:20Z) and finished: citebook 828.9 s, recruiting 824.6 s
  (rc 0, user 861-891, sys 299-345), 10-13x one alone. No hang.
- Run 2 on the same boots (unwrap started again), from 03:21Z: the guard stopped unwrap
  again at 03:22:20Z (`free_bytes 478052352`, `running 3`), 80 s in. citebook 454.2 s,
  recruiting 470.5 s, rc 0, the two answering ssh throughout.
- Host, 03:05-03:29Z: MemAvailable min 0.77 GiB, guests.slice peak 10.73 GiB, zram 0, PSI
  memory some peak 3.96, kernel `oom_kill` 0 (the stops are hostd's).

So on the 12G appliance three heavy builds do not fit: the guard stops the newest every
time (2 of 2), the older two finish, and nothing hangs. The bound of 3 is never reached for
this workload; memory decides first. Not run: four busy with no quota and the bound
overridden (the conductor's knowledge run; it would have taken more than 20 minutes).

## Findings outside the numbers

| # | Rank | Finding | Evidence |
|---|---|---|---|
| F1 | P1, ship-relevant | The released appliance (2026.11.22.4, and main's image) keeps snapshots on its 2 GiB root. After one `large` stop (1.3 GB snapshot) and four `tiny` stops (265 MB) the root was 100 % full and every stop failed: `stopping e2e-recruiting failed: snapshot upload failed: write /var/lib/repose/snapshots/<acct>/<proj>/<ts>.img.zst.part: no space left on device`; three projects ended in state `error`. The VG's thin pool had 93 GB, 13 % used. A customer hits this on the second or third stop. | 18:40Z log line; on branch appliance-snapshots (I-1636); its move tested green with dd2d64c7 (F8, F14), except where F15 applies |
| F2 | P2 | A create refused for memory leaves the project behind in state `error`, after `Creating e2e-pocketmail...`. Then `repose start e2e-pocketmail` answers exit 1 `Could not start e2e-pocketmail: the host no longer has the machine (not_found). ` + "`repose start e2e-pocketmail` tries again." There never was a machine; the message blames the host and invites a retry that cannot work. `repose ls` (v0.1.65 and main 1a04c91f) still says `e2e-pocketmail: the host no longer has the machine. ` + "`repose start e2e-pocketmail` restarts it." at 19:45Z. A `large` refused at create (17:37Z) created nothing, so the two paths differ. On sharing the memory refusal left nothing (23:48Z, 01:49Z), but the CPU refusal left the project in `error` at 01:15Z (api r20261125-4) and for review-cli at 01:25Z (r20261126-1); on r20261126-2 (02:49Z) it left nothing. | 18:09Z, 18:36Z log lines; `r2 ls` 19:45Z; S5 runs |
| F3 | P2 | The appliance never gives memory back to macOS. vfkit's argv has no virtio-balloon device. The VZ process's footprint went 9.9 GB (17:42Z) to 12 GB (18:3xZ) and never lower, including while one idle guest held 4.56 GiB. Sharing inside the appliance helps machines share its 12 GB; macOS loses the full share once touched. | 19:35Z log line; `mac.csv` |
| F4 | P3 | `repose host add` says the image is `about 200 MB`; the docs said 85 MB. `repose host ls` showed `waiting ... LAST SEEN 51h20m ago` for a host that had never answered (a re-used name). | 17:27Z log line |
| F5 | P3 | The first create on the host printed `Created e2e-recruiting (tiny)` without `on e2e-mac`; later ones had it. | 17:59Z log line |
| F6 | note | Guest checkouts made by `repose run` have no git remote, so an agent cannot push from them; the lab's own clones have push set to DISABLED-NO-PUSH. | 17:59Z log line |
| F7 | P3, code reading only | own-hardware.md says "`--memory` at `repose host add` changes the virtual machine's share". `applianceInstall` writes `VM_MEMORY_MIB` only when the config file does not exist (internal/cli/appliance.go, the `os.Stat(p.Config)` branch); on an installed Mac it changes only the host name. Not run: re-running `host add` needs a join argument, which the lab does not handle. | code at main 1a04c91f |
| F8 | P1, ship-relevant | repose-snapshots-volume (branch appliance-snapshots, PR #214, carried by host-files PR #215) asks LVM for an LV named `snapshots`; LVM refuses names starting "snapshot". See the host-files section. Fixed (LV `repose-snapshots`) in shared-host's 62620126; re-tested: LV created at 00:30:38Z and 02:11:05Z. | unit log 00:20:56Z |
| F9 | P1 | All four machines hung in all three 12G S5 runs on sharing (four busy machines; no zram use, no OOM kill, no guard kill), and `repose ls`, `host ls` and the dashboard kept saying running / online, for up to 20 minutes; nothing told the user, and only `repose stop` (about 70 s each, a power-off) ended it. | S5 sections |
| F10 | P2 | After the appliance grew from 12 GiB to 18 GiB (config edited, launchd job restarted, hostd's Hello says `slice_max_bytes 18303242240`), `repose host ls` still says `MEMORY 12 GB` and `--json` has `mem_bytes 12536094720` (the old MemTotal). The api writes `mem_bytes` only at registration (internal/api/hostmgr/hostmgr.go:634 at main 3a531b67); the Hello update (:839) does not touch it. | 00:26Z, `host ls --json`; code |
| F11 | P3 | `repose start` on a sharing host still names a class: `e2e-citebook is running (large), ready in 11s.` (main 1a04c91f CLI). I-1632 says a machine there has no size. | `s12.log` 22:58Z |
| F12 | P2, observed only | On this Mac, hostd logged every minute all evening `image key not fetched; guests cannot be parked yet` with `launcher: wrapping key unavailable: security add-generic-password: exit 195: ... SecKeychainItemCreateFromContent (<default>): Write permissions error.`; the launcher log has `park-key: event=key_unavailable`. So guests on this Mac cannot be parked across a host restart (I-1386). The launchd job runs as ec2-user with nobody logged in; not checked whether a desktop-logged-in Mac differs. | hostd journal, `~/Library/Logs/repose/launcher.log` |
| F13 | note | `repose ls` showed AGENTS `-` for machines running `claude -p` and an interactive `claude` in tmux (the lab's sessions), where the owner's machines show `claude: idle`. Not investigated. | `r2 ls` 23:06Z, 23:36Z |
| F14 | P1, ship-relevant | The snapshot move failed on every appliance with existing snapshots: `diff -rq` of the root dir against the new ext4 LV reports `lost+found` and the unit gives up (`event=snapshots_not_moved reason=copy_failed`). Fixed in dd2d64c7; re-tested: 4 files moved, sha256 equal, a moved snapshot restored. | move sections, 01:41Z and 02:11Z |
| F15 | P2 | When the thin pool is past LVM's threshold, the snapshot volume cannot be made (`Cannot create new thin volume, free space in thin pool vg-guests/thin reached threshold.`); snapshots stay on the 2 GiB root and host-files retries at every start. Seen at 82 % pool use (my lab LV took most of it). | 02:09:56Z |
| F16 | P2 | A restore of a 264.2 MB snapshot took 4m48s. | 02:11-02:16Z |
| F17 | P3 | When hostd's memory guard stops a machine, the CLI says `your host e2e-mac ran out of memory, and the kernel stopped e2e-unwrap to keep the others running.` The kernel did not: hostd's guard did (`event=guest_memory_guard`), and the kernel's `oom_kill` count stayed 0. | 03:14:47Z, 03:22:20Z |

## What could not be run, or was cut

- Pair runs: N=4 P only once (cut at 824 s), N=2 and N=8 one H and one P pair each, N=8
  without a warm-up; the reclaim-off check's third pair cut. Enough to show the size of
  the effect, not a distribution.
- The cause of the nested slowdown and of the S5 hang: not found. No perf in the
  appliance; nothing installed on macOS (powermetrics and top are built in).
- S2 compares badly across runs: each pass gave the agents repos holding the previous
  pass's commits, and recruiting hit my 30-minute cap once (12G, N=4). Agent wall times are what
  happened, not a benchmark.
- 18G S5 with the old vCPU default (all 8 per guest): not run after the 12G hang.
- host-files and the snapshot move on the x86 installer.
- Anything on x86: no x86 host in this lab. The 512 MiB reserve, zram and the vCPU rule
  are measured on the M4 appliance only.
- The OOM-kill path (a machine killed by the kernel, its `oom_killed` reason in the CLI and
  dashboard) and the memory guard's kill: never reached. In every 12G S5 the machines hung
  before either acted; at 18G memory never ran short.
- S5 with guest RAM without huge pages (thp=off): not run, see the guard section.
- Dashboard: not looked at by the lab (the reviewers cover it).
