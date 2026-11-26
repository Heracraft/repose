# Sharing on your own hardware: the customer council

Reviewers, 2026-11-26: review-cli (every command and flag on an owned host,
01:14Z on), review-dash (the dashboard at 1440 and 390, both schemes, at 1x,
01:14Z on), review-mac (a Mac owner's whole day on e2e-mac, 02:08Z on; writes
this file). Brief: ~/briefs/sharing-review-2026-11-26.md. Method:
docs/REVIEW.md. Ranks: P0 blocks launch, P1 is fixed before the plan is sold,
P2 after. Anything that loses a tenant's work is P0 whatever its size.

What was reviewed: main 611068f6 (api, web and CLI; api and web live on
production since about 01:18Z), and on e2e-mac (AWS M4 Mac mini, 24 GB,
10 cores, macOS 15.8.3, appliance at 12 GB and for one run 18 GB) the
combined hostd 62620126, which is not released. Every number below names the
session that measured it. Numbers marked "lab" are the lab's
(~/briefs/sharing-program.log, 17:25Z 2026-11-25 to 02:08Z 2026-11-26); the
reviewers did not re-measure them, as the brief asks.

The catalogue a customer reads: `GET /v1/billing` at about 02:12Z answered the
Own hardware plan `available: true`, `price_cents: 1000`, `trial_days: 7`
(review-mac). So the plan can be bought today, and the public page
`/docs/own-hardware` already describes sharing (fetched about 02:12Z). The gate
for the hostd is its release, not a held commit.

## What would make a user scream

| # | Rank | What the customer sees | Status |
|---|------|------------------------|--------|
| S1 | P0 | On a 16 GB Mac (12 GB appliance), four busy machines, which the page said fit until about 03:30Z, made **every machine stop answering, for good**: no machine stopped, every build lost, CLI and dashboard still `running`. 3 hangs in 3 runs, the third on the memory-guard hostd with 1.3 GiB free. With three busy on owned-cpu 5d6dc807 the guard stopped the newest and the rest finished (2 of 2). | lab 23:49Z, 01:46Z, 02:51Z (hangs); 03:29Z (gate passed at three). The hang's mechanism is not shown. |
| S1b | P0 | One program that eats memory inside one machine hangs that machine for good, on a host with room to spare; the guard does not fire and the guest kernel does not stop the program. `ls` says `running`. | review-cli 03:41Z, one run; an idle machine also stopped answering at 03:51Z, hostd owned-cpu 5d6dc807 (the build the lab passed at 03:29Z). |
| S2 | P0, fixed on main 06d96eca, re-run PASS | The update every existing Mac takes did not move snapshots off the 2 GB system partition on 62620126: the move failed on any host that already had a snapshot. | Failed 01:42Z; PASS 02:17Z on memguard dd2d64c7 with the lost+found fix (lab). Two leftovers below. |
| S3 | P1 | The page a customer can read today promises sharing, "updates itself within about half an hour", but no customer's Mac can get it: the hostd is not released. A customer who buys today gets one large machine on a 16 GB Mac and the second refused. | review-mac, about 02:12Z. |
| S4 | P1 | Several busy machines on a Mac are 4 to 15 times slower each, and the page does not say so. It says agents "start as fast as on repose's servers". | Numbers by the lab; page read by review-mac. |
| S5 | P1 | A create refused because the Mac's cores are taken leaves a dead project behind, whose own advice loops: `restart it` then `the host no longer has the machine ... tries again`, and `repose rm` promises a final snapshot that does not exist. Same in the dashboard. | review-cli, review-dash, lab. Fixed on production since about 02:40Z (r20261126-2, I-1648): the lab's 5th create at 02:49:08Z left nothing. The dashboard's 'Not created' page is not yet re-read. |
| S6 | P1 | While every machine is hung, `repose ls`, `host ls` and every dashboard page say `running` and `online`; the Memory column turns to `-` with no word why. | review-dash 01:57Z, review-cli, lab. |
| S8 | P1 | `repose run --size xl` on your own host says "--size is ignored", then gives the machine an 80G disk instead of 40G. | review-cli 03:04Z. |
| S7 | was P0, settled 02:52Z | main 06d96eca's hostd boots Mac machines with guest RAM in 4 KiB pages (thp=off) unless a flag says otherwise. With it, one node build alone was still in its first step (about 25 s with 2 MiB pages) after 12.7 min, and a Claude agent's start loop took 113 to 150 s. A Mac on that default is unusable. | lab 02:39Z. Conductor 02:52Z: THP default on (owned-cpu 5d6dc807). Closes when the released hostd carries it. |

S7 was a P0 for about 15 minutes and is kept for the record. P1 S2a and the
P2 list follow below. One finding was withdrawn by the lab itself and
is recorded as such (W1): the vCPU count was not shown to cause a hang.

## P0

### S1. A full 12 GB appliance hangs every machine; nothing stops, nothing tells

- **Reproduction (lab, three times).** 12 GB appliance (what a 16 GB Mac gets),
  four machines, a node build in each at once (H). Run 1, 23:47Z, hostd
  85e53826, 4 vCPU each: every guest silent from about 23:49Z. Run 2, 01:44Z,
  combined 62620126 (the build under review), 2 vCPU each: consoles silent
  from 01:46Z (2 min in), ARP to every guest failed, ssh `cannot reach
  environment: no route to host`.
- **Expected** (own-hardware.md, What fits): "When the machines together want
  more than the share, the least used memory is compressed in place: they
  slow down, and keep running." Then: "If memory runs out even so, the kernel
  stops one machine, the one started most recently ... you get a
  notification".
- **Actual** (run 2, lab 01:55Z): guests.slice memory.current 11993468928 =
  memory.max 11999223808, MemAvailable 0.12 GiB, zram used 0 (guests.slice
  swap.current 3 MB), `oom_kill 0`. Nothing compressed, nothing stopped. No
  recovery in 18 min. `repose stop` of one: exit 0 in 3 s, then 69 s to
  `stopped` (freeze timeout, snapshot `guest_unresponsive`, power-off); it
  freed memory (0.12 to 2.45 GiB) but the other three stayed unreachable for
  3 more minutes; stopping them took 72 s each. **All four builds lost.**
- **Run 3 (lab 02:52Z), memguard dd2d64c7 with 2 MiB pages (the fix that
  was meant to close this).** S1 alone fine (H 81.3 / 61.6 s; agent start
  `claude -p` 12.95 s cold, then 4.50 and 3.82 s). Four H builds from
  02:44Z; a 5th create refused cleanly and left nothing (r20261126-2).
  Then all four guests hung a third time (ssh `no route to host` 02:51Z,
  ARP failed), **with memory not full**: MemAvailable 1.31 to 1.34 GiB,
  guests.slice 10.2 of 11.17 GB, zram 0, PSI memory about 0, and the guard
  never fired (free stayed above its 512 MiB). guests.slice was throttled
  by its CPU quota in every period (nr_throttled 16582 to 16682 in 100
  periods). No self-recovery in 3 min; stopping all four took 85 s
  (lab 02:56Z). So the lab's honest reading: the common factor is "four
  busy nested machines on a 12 GB appliance", the mechanism is not shown.
  At 18 GB the same four finished once (lab 01:24Z).
- **The page now (r20261126-3, read 04:00Z):** "a Mac whose virtual
  machine has eight cores runs three at once"; "If memory runs out even so,
  repose stops one machine ... when the host has had under 0.5 GB free for
  five seconds, so the machines never hang waiting for memory." Run 3 is a
  hang the guard cannot see, so the page's promise is not met by the
  hostd it describes. "Compressed in place" is gone from the page since
  r20261126-3 (I-1650: not achievable on a nested Mac with 2 MiB pages).
- **Run 4, the ship gate (lab 03:29Z), owned-cpu 5d6dc807, three busy, n=2:
  no hang.** The guard stopped the newest machine both times (03:14:47Z,
  03:22:20Z, `free_bytes` 422199296 and 478052352) and the CLI said
  `e2e-unwrap: stopped: your host e2e-mac ran out of memory, and the kernel
  stopped e2e-unwrap to keep the others running.` (it was hostd's guard,
  not the kernel). The other two finished (828.9 / 824.6 s, then 454.2 /
  470.5 s) and answered throughout. So on a 16 GB Mac three heavy builds do
  not fit: the third is stopped every time, and the bound of three is
  never what decides.
- **After the gate (review-cli 03:41Z and 03:54Z, same hostd):** S1b, and
  an idle machine (e2e-cli-courses, answered at 03:35:24Z) refusing ssh
  from 03:51Z with `no route to host` every 20 s, `ls` `running`, MEMORY
  `-`, cause not shown. review-mac's two machines beside it answered
  `repose exec` in 0.5 s at 03:59Z and 04:01Z.
- **Is 12 GB with four busy machines a customer state?** Yes: the page sells
  a 16 GB Mac and "runs four at once". At 18 GB the same four builds
  finished (lab 01:24Z, MemAvailable min 2.04 GiB). At 12 GB, three Claude
  agents at once ran with MemAvailable down to 0.83 GiB (lab 00:19Z, N=4).
- **What the customer is told meanwhile:** see S6.
- **Instrument:** lab's samplers on the appliance (cgroup files,
  /proc/meminfo, /proc/vmstat), guest consoles, ARP, ssh, `repose stop`
  timed.
- **Falsifier:** a 12 GB run on the hostd that will ship ending with every
  machine answering, or one `stopped` with its reason and the rest
  answering. Not seen: 62620126 hung 1 of 1, 85e53826 1 of 1, memguard
  dd2d64c7 1 of 1. builder host's owned-cpu 5d6dc807 (no slice CPU quota,
  at most 3 machines at once on 8 vCPUs, THP on) is the next attempt; the
  lab runs 3 busy n=2 in its 02:56Z window. Even if 3 at once never hangs,
  the finding closes only with the page saying what a 16 GB Mac really
  runs at once, and without "compressed in place" and "never hang" unless
  a run shows them.

### S1b. One memory-hungry program hangs its machine, on a host with room

- **Reproduction (review-cli 03:32:14Z to 03:41Z and on, hostd owned-cpu
  5d6dc807, 12 GB appliance, only review-cli's two machines running).** In
  e2e-cli-empty a python loop allocating and touching 256 MiB every 0.2 s,
  aiming at 11000 MiB (guest MemTotal 10551 MiB).
- **Expected:** what a machine on repose's servers does and troubleshooting
  says ("Processes get killed ... `sudo dmesg | grep -i killed` names what
  the kernel stopped"): the guest kernel kills the program, the machine
  keeps answering.
- **Actual:** the last hog line read 1792 MiB at +4 s; from +7 s every
  `repose exec` answers `did not answer ssh in 15 s ... cannot reach
  environment: no route to host`, still at 03:41Z (9 min). `ls` says
  `running`, MEMORY `-`. The other machine answered, `free` 10060 MiB, so
  the host was not full and the guard did not fire.
- **Why P0:** an agent's build that overshoots memory in one machine is an
  ordinary day, and this loses that machine's work with no word, on the
  hostd that passed the ship gate 12 minutes earlier. Single run;
  review-cli asked the lab for the host-side readings (console, cgroup,
  guard lines) at 03:41Z.
- **Instrument:** `repose exec` timings, `free` in the other guest, `ls`.
- **Falsifier:** a second hog run in which the guest kernel kills python
  and the machine answers within a minute. Owed: one run is a margin.

### S2. The update path does not move an existing Mac's snapshots

- **Reproduction (lab 01:42Z).** A 2026.11.22.4-state appliance with two
  real snapshot files (645431763 bytes) on the root's
  /var/lib/repose/snapshots and no snapshot volume. Install the combined
  hostd and restart it, which is what the updater does.
- **Expected** (own-hardware.md, What fits and Snapshots stay on its disk):
  "It updates itself within about half an hour ... nothing to reinstall";
  snapshots live on "a volume of half" the machines' space.
- **Actual:** `Logical volume "repose-snapshots" created`,
  `event=volume_created size_gib=48`, then
  `event=snapshots_not_moved reason=copy_failed`, unit exit 1, nothing
  mounted, files left on the 2 GiB root. Cause, reproduced by hand by the
  lab: the fresh ext4 volume has `lost+found`, so the copy's compare says
  they differ, for any non-empty directory.
- **What it costs a customer:** the released appliance today fills its
  2 GiB root after one large stop and four tiny ones, and every later stop
  failed into `error` (lab 18:40Z). With the hostd under review a stop
  whose snapshot cannot be written still stops (I-1636), so the machine's
  disk is safe, but the customer has no restore points: `undo` is gone.
  Every customer who added a Mac before this release takes this path.
- Earlier blocker on the same path, fixed and re-tested: the LV name
  `snapshots` was refused by LVM (`Names starting "snapshot" are reserved`,
  lab 00:22Z); renamed, PASS at 01:29Z.
- **Instrument:** appliance journal, `findmnt`, file count and sha256
  before and after.
- **Falsifier:** the same setup on hostd with main-green's lost+found fix
  ending with the files on `vg--guests-repose--snapshots`, the root dir
  empty, and a restore of one moved snapshot working. **Run by the lab at
  02:11Z on dd2d64c7 (which carries the fix, now on main 06d96eca): PASS.**
  `event=snapshots_moved files=4`, `event=volume_mounted`, 4 files
  645431763 B with identical sha256, root used 979 MB to 334 MB;
  `repose snapshots restore e2e-cutroom b5f8449e --as e2e-cutroom-moved`
  exit 0, the checkout at the snapshot's commit. So S2 is fixed in the tree
  that ships; it stays in this section because it blocked the release until
  02:17Z and the re-run is its evidence.
- **Leftover S2a (P1, lab 02:17Z):** the first try failed `Cannot create new
  thin volume, free space in thin pool vg-guests/thin reached threshold`
  (pool 82 %, full of the lab's own snapshots). A customer whose pool is
  past LVM's threshold gets no snapshot volume, and host-files retries at
  every start; snapshots stay on the 2 GiB root. Not measured how many
  customers' pools are that full. Falsifier: the volume made, smaller, on a
  pool at 82 %.
- **Leftover S2b (P2, lab 02:17Z):** the restore of a 264 MB snapshot took
  **4 min 48 s** (`Restored ... in 4m48s`). REVIEW.md's reference is about
  8 s for a restore on repose's servers. One run; a second is owed before
  it is more than a margin.

## P1

### S3. The live page promises sharing that no customer's host can have

- **Reproduction (review-mac, about 02:12Z).** `curl https://repose.run/docs/own-hardware`:
  "A machine on your hardware has no size", "machines share that memory",
  "It updates itself within about half an hour, a Mac and an x86 machine
  alike". `GET /v1/billing`: Own hardware `available: true`, $10, 7 days
  free. The conductor's 01:14Z line: "hostd/appliance NOT released (that
  waits for the review)".
- **Expected:** what the page says is what a customer who buys today gets.
- **Actual:** a customer's Mac runs the released hostd 2026.11.22.4, which
  places by size: on 12 GB one large, then `your host e2e-mac has 1.2 GB of
  memory free for machines, and a large machine needs 8 GB` (lab 17:37Z),
  the "terrible UX" the owner named on 2026-11-25. No update reaches it
  within half an hour or any time, because none is published.
- **Instrument:** the live page, the live billing catalogue, the conductor's
  release state.
- **Falsifier:** the appliance hostd published to the update channel, or the
  sharing paragraphs held back from the live page until it is.

### S4. Several busy machines on a Mac are many times slower, and the page does not say so

- **Measured (lab):** one node build (H) alone at 2 vCPU 60.9 to 87.8 s
  (18 GB, first run after boot slowest). Four at once, 2 vCPU each, 18 GB:
  801.8 / 811.3 / 802.8 / 810.4 s, **10 to 13 times** each (01:24Z). Two at
  once: first pair after boot 1.5 to 1.9 times, every later pair 3.7 to
  more than 15 times (20:49Z). The same two builds directly in the
  appliance, not nested: 1.4 to 1.5 times (22:24Z). So the cost is nesting,
  known and listed as deliberately absent ("several times slower",
  2026-11-25).
- **What the page says** (own-hardware.md, live after r20261126-2, read
  02:45Z): "A machine inside the virtual machine runs slower the more cores
  it has, and machines busy at once slow each other more the more cores
  they have" and, under Starting a machine,
  "Agents start as fast as on repose's servers". Nothing says that two or
  more busy machines at once each run several times slower (the measured
  figure is ten times for four), or that x86 does not. "Slow each other
  more the more cores they have" also reads as if fewer cores would fix it;
  the lab's pairs at 2 vCPU were still 3.7 times slower (20:49Z).
- **Why P1 and not P2:** the page's own headline for the feature is "four
  at once". A customer who reads it starts four agents and sees each take
  ten times as long, with nothing on the page to say that is expected. The
  conductor asked for this check (01:24Z).
- **Instrument:** the lab's timed builds; the page text.
- **Falsifier:** a sentence on the page that says, with the lab's numbers,
  that busy machines on a Mac slow each other several times over and that
  x86 does not.

### S5. A create refused for cores leaves a dead project whose advice loops

- **Reproduction (review-cli 01:24:49Z, api 611068f6, hostd 62620126; lab
  01:15Z; review-dash 01:25Z).** Four machines running on e2e-mac. From an
  empty `mktemp -d`: `repose run e2e-cli-fifth -d`: exit 8 in 0.6 s,
  `Creating e2e-cli-fifth...` / `Could not start e2e-cli-fifth: the host has
  as many machines running as its cores allow; stopping a running machine
  frees room (insufficient_capacity).`
- **Expected:** nothing left behind, as the memory refusal does (lab 23:48Z
  and 01:49Z: exit 8, nothing in `repose ls`).
- **Actual:** `e2e-cli-fifth *` in `error`, marked as the folder's project;
  ls footer `repose start e2e-cli-fifth restarts it.` `repose start`: exit 1
  in 0.4 s, `the host no longer has the machine (not_found). repose start
  e2e-cli-fifth tries again.` (a loop; "no longer" for a machine that never
  existed). `repose events`: `No events on e2e-cli-fifth in the last 24h.`
  `repose rm -y`: `Its final snapshot is kept on your host e2e-mac` and
  there is no such snapshot. Dashboard (review-dash): the form gave no sign
  the host was full; 10 to 20 s of `Starting...`, then `error`, a red
  banner with the sentence and a **Restart** button; its **Errors** link
  opens `0 events`; the row stays.
- Also: the CPU sentence names neither the host nor `--host repose`; the
  memory sentence names both.
- **Instrument:** CLI exit codes and wall times (runs.log of review-cli);
  Playwright screens (review-dash shots/fifth-*).
- **Falsifier:** PR #219 (I-1648) on production, and the same fifth
  create leaving nothing in `repose ls` or the dashboard list. **Live since
  about 02:40Z; the lab's 5th create at 02:49:08Z refused for cores and
  left nothing (lab 02:52Z).** Still open: the CPU sentence names neither
  the host nor `--host repose`, and the dashboard form shows nothing about
  the host's room before Create (review-dash).

### S6. Hung machines read `running`, everywhere

- **Reproduction:** during S1's run 2 (guests silent from 01:46Z), read
  every surface.
- **Actual:** review-dash 01:55:16Z and 01:57:37Z: list, sidebar, project
  page all `running` with **Stop** offered; Settings, Hosts `online, 4
  machines, 4 running, just now`; the list's Memory column `-` with no word
  why, while the project page's meter still read `2.4 GB of e2e-mac's
  11.7 GB`. CLI (review-cli, lab 00:19Z): `repose ls` running, host
  `online`, MEMORY `-`. Screens /mnt/nixstore/sharing/review-dash/shots/hung-*.
- **Expected:** a customer whose machines stopped answering is told so,
  somewhere, before they find out by trying ssh. The hosted side reads
  "has not answered for 14m" for a quiet host; a quiet machine on an
  answering host gets nothing.
- **Instrument:** dashboard at 1x, CLI.
- **Falsifier:** with S1 fixed no machine hangs, which removes the case
  this run produced; a guest that hangs for any other reason still reads
  `running`, so the finding stands on its own.

### S8. `--size` on an owned create says "ignored" and doubles the disk

- **Reproduction (review-cli, 02:56 to 03:05Z, api r20261126-2, hostd
  memguard dd2d64c7).** `repose run e2e-cli-empty --size xl -d` on e2e-mac.
- **Expected** (own-hardware.md): "`--size` ... is accepted and changes
  nothing on your hardware; the command says so in one line."
- **Actual:** `--size is ignored: e2e-cli-empty runs on your host e2e-mac,
  where a machine has no size and uses the memory it needs.` then
  `Connected to e2e-cli-empty (xl)`. The machine got an 80G disk: `repose
  status` `disk 5.8 GB of 78.2 GB (80G disk)`, in-guest `df` 79G, `ls
  --json` `class xl`, `volume_bytes 85899345920`; the same create without
  `--size`: `volume_bytes 42949672960`. Memory and cores the same (nproc 2,
  MemTotal 10551 MiB both).
- **Why P1:** a line that says the opposite of what happened, about the
  customer's own disk, on a host where disk is the cap (P2-3).
- **Instrument:** CLI, `status`, `ls --json`, `df` in the guest.
- **Falsifier:** the `--size xl` create with a 40G disk. Not seen.

## P0 pending a decision

### S7. The hostd on main makes every Mac machine crawl unless a flag is set

- **Reproduction (lab 02:39Z).** hostd memguard dd2d64c7 (the hostd that
  r20261126-2 put on main 06d96eca, owned-memguard, I-1650) with its
  default: guest RAM `thp=off` (ch.args `--memory
  size=10880M,shared=on,thp=off`). e2e-citebook alone on a 12 GB
  appliance, the lab's H build.
- **Expected:** a single build as fast as before (75 to 90 s alone at
  12 GB, lab 00:19Z; first step about 25 s).
- **Actual:** still in its first step 12.7 min in; cut. `claude --version`
  then `claude -p --model sonnet "say ok"`, three iterations: 150 / 116 /
  113 s whole. I-1574 had measured 6 to 12 times slower agent starts
  without 2 MiB pages under Apple's nesting; this is worse.
- **Why it is here:** builder host made thp=off the default at the
  conductor's request, to let memory be compressed (S1), and left the
  default to the lab's numbers. If the hostd is released with this default,
  every Mac machine is unusable. With thp on, the page's "compressed in
  place" is not what the kernel does (S1).
- **Instrument:** the lab's timed build and agent loop.
- **Falsifier:** a thp=off run whose build finishes within twice the thp=on
  time. Not seen. Closes when the released hostd's default is thp on and
  the page says what happens when memory runs out with it (the guard).

## P2

| # | Finding | Reproduction, expected vs actual | Instrument | Falsifier | By |
|---|---------|----------------------------------|------------|-----------|----|
| P2-1 | The page's own examples contradict it | own-hardware.md shows `repose ls` with `SIZE` `large` on mac-mini and `✓ Created todo-app on box-a (large)`, under "a machine on your hardware has no size". The CLI prints MEMORY. Live page, about 02:12Z. | page text | page shows MEMORY and no size for an owned machine | review-cli N1, review-dash, review-mac |
| P2-2 | Usage tab's CPU chart is "% of 8 cores" for a 2-core machine | A pegged Mac machine can never pass 25%; peaked 23% during four busy builds. Memory chart scaled to 11.7 GB while the appliance had 18 GB (ENG-244 reaches every chart). | dashboard, 1x | chart scaled to the machine's 2 cores | review-dash |
| P2-3 | Disk refusal a customer cannot act on | review-mac 02:09:39Z, from a clone: `repose run e2e-mac-pocketmail -d` exit 8 in 0.1 s, `your host e2e-mac has 15.7 GB of disk free, and a new machine needs 28.9 GB free.` The page says a machine "costs what it actually writes"; it does not say what a new one needs free, or why 28.9. Troubleshooting answers the disk sentence with "Stop a machine ... to free the memory it uses", which frees no disk. There is no `repose snapshots rm`, so snapshots, which share the pool, cannot be deleted by the customer. The pool here was full of the lab's leftovers (conductor 02:10Z), so how soon a customer reaches it is not measured. | CLI, `snapshots --help`, docs | a troubleshooting line saying what frees host disk, and a figure a customer can predict | review-mac |
| P2-4 | `repose events` order and gaps | `machine stopped` printed before `machine stopping` at the same second (22:57:57Z, 00:31:20Z); a refused create has no event. | CLI | events in time order; refusal recorded | review-cli N5, N7 |
| P2-5 | Snapshot-failed text shows internals | `snapshot upload failed: write /var/lib/repose/snapshots/<uuid>/<uuid>/...img.zst.part: no space left on device` (hostd 85e53826 era): "upload" on a host that uploads nothing, two raw uuids, no next step. I-1636's sentence replaces it once the hostd ships; re-check then. | CLI events | the I-1636 sentence in events after a full store | review-cli N6 |
| P2-6 | `host ls --json` says `"provider": "azure"` for a Mac | review-mac re-read 02:10Z: same. | CLI | provider names the Mac | review-cli N2, review-mac |
| P2-7 | `ls --json` keeps `"class": "large"` beside `"sized": false` | | CLI | no class on an owned machine | review-cli N3 |
| P2-8 | MEMORY units | `763.8 MB used` beside `1.6 GB used`; `1005.6 MB used` | CLI | one unit, 1.0 GB | review-cli N4, N8 |
| P2-9 | `--host` on an existing machine points to a path that does not exist | `e2e-cli-h exists and stays on its host ... A machine moves host only by restoring a snapshot.` No restore takes a host, and the page says a snapshot is readable only where it was taken. | CLI help, docs | a restore that takes `--host` | review-cli N9 |
| P2-10 | Hosted create line does not name its host | `✓ Created e2e-cli-h (tiny) 11s`; the page says "The create line names the host". Lab 17:59Z saw the same on the Mac once (`Created e2e-recruiting (tiny)` without `on e2e-mac`). | CLI | `on repose` in the line | review-cli, lab |
| P2-11 | Download size | `host add` prints "about 200 MB", the page says "about 85 MB" (lab 17:27Z; open since the 2026-11-23 pass). | CLI, page | one figure | lab |
| P2-12 | A never-seen host reads `LAST SEEN 51h20m ago` | `repose host ls` right after `host add` (lab 17:27Z). | CLI | `never` or blank | lab |
| P2-13 | `host ls` memory never refreshes (ENG-244, known) | 12 GB shown after the appliance grew to 18 GB (lab 00:27Z); the dashboard meter `of e2e-mac's 11.7 GB`. Known; listed because it reaches every Usage chart and the meter. | CLI, dashboard | | lab, review-dash |
| P2-14 | A host restart briefly shows sizes | After an appliance reboot, before the updated hostd ran, the list read `tiny · 2 GB` and `large · 8 GB` under Size, sidebar `Running 38 GB` (review-dash 01:27Z); flipped to Memory 9 s after the install (01:28:02Z). In the lab this was the released hostd from the image, the documented "older version" path, so it is noted only. | dashboard | | review-dash, review-cli |
| P2-15 | `repose host --help` says "One host per account" | On the Mac, darwin CLI from 611068f6 (review-mac, about 02:12Z). The page says "any number of hosts" and describes "more than one host"; the catalogue says `hosts: 2147483647` for Own hardware. | CLI help, page, `/v1/billing` | help without the sentence | review-mac |
| P2-16 | Every owned run, start and restore names a size | `e2e-citebook is running (large), ready in 11s.` (lab F11, main 1a04c91f); `Connected to e2e-cli-courses (large)`, `Restored e2e-cli-courses; it is running (large).`, `ls --destroyed` SIZE `large` (review-cli F-C, 03:04Z, main 06d96eca); dashboard's Recently destroyed `large` (review-dash 5). | CLI, dashboard | no size in the line | lab, review-cli, review-dash |
| P2-17 | AGENTS `-` for machines running Claude | `repose ls` showed `-` for machines running `claude -p` and an interactive `claude` (lab F13, 23:06Z, 23:36Z). Re-checked in review-mac's day below. | CLI | `claude: working` | lab, review-mac |
| P2-18 | The appliance never gives memory back to macOS | No balloon in vfkit's argv; the VZ footprint rose 9.9 to 12 GB and never fell, idle included (lab F3). At 18 GB on a 24 GB Mac, macOS compressed 8.2 GiB (lab 01:24Z). The page says the share is held "whether machines run in it or not", so this is documented; listed because 18 GB is the page's own figure for a 24 GB Mac. | Mac `footprint`, `vm_stat` | | lab |
| P2-19 | Park across a Mac restart is unavailable on this Mac | `park-key: event=key_unavailable ... security add-generic-password: exit 195 ... Write permissions error.` every minute in `~/Library/Logs/repose/launcher.log`, still at 02:09:57Z (review-mac read). Deliberately absent as ENG-245 for headless Macs; noted because the log fills with it. | launcher.log | | lab F12, review-mac |
| P2-20 | `--memory` on `repose host add` may not change an installed Mac | The page says it changes the share; the lab read the CLI and found it written only when no config exists (lab F7, code reading only, not run: it needs a join argument and the brief forbids changing e2e-mac's memory). Unreproduced, kept and marked. | code reading | a re-run `host add --memory` that changes `VM_MEMORY_MIB` | lab |
| P2-21 | The join argument stays on the Mac after a good install | `~/Library/Application Support/repose/join-argument`, 340 bytes, mode 0600, dated 2026-11-25 17:27 (the install), still there at 02:10Z. The page says the token works once; whether the file still opens anything after the join is not checked (contents not read). | `ls -la` on the Mac | the file removed once the host is online | review-mac |

## Withdrawn

- **W1. "More guest vCPUs than the appliance has hangs every machine."**
  The lab's 00:19Z reading, on which I-1640's refusal rests, withdrawn by
  the lab at 01:55Z: both 12 GB hangs had the appliance nearly out of
  memory, and the one four-machine run that finished (18 GB, 8 vCPUs on 8)
  had 2.04 GiB free. The page still says "machines with more cores between
  them than the virtual machine has can stop answering". The four-at-once
  limit may still be right for speed (S4); the stated reason is not shown.
  Ranked with P2-1 as a page correction once the lab's memguard runs say
  what the limit is for.

## What is right, measured

- A create refused for memory leaves nothing and says what to do: exit 8,
  `your host e2e-mac has 0.8 GB of memory free, and a machine starts there
  when it has at least 1 GB free. Stopping a machine frees the memory it
  uses. repose run --host repose makes it on repose's servers instead.`
  (lab 23:48Z, 01:49Z).
- Machines share memory: three Claude Sonnet agents at once on a 12 GB
  appliance, 2 vCPU each, all exit 0 in 101, 128 and 325 s (lab 01:43Z);
  the same repos on the released hostd, in the only sizes that fit (tiny,
  2 GB), had one build die of memory with 5 GiB of the host idle (lab
  18:21Z).
- Idle machines give memory back: two idle Claude machines went from 3.35
  and 2.82 GiB to about 0.48 GiB each (`repose ls` MEMORY, lab 00:19Z).
- The update of host files keeps machines running: install and one restart,
  zram and the volume unit up, `--owned-host` written, exactly one self
  restart, machines kept the same boot ids, 46 snapshot files before and
  after (lab 01:29Z). Seen from the dashboard: Size to Memory 9 s after the
  install (review-dash 01:28:02Z).
- Stops under load return in 0.8 to 4.9 s; a start is 11 s on an idle
  host, 33 to 52 s beside a busy build (lab S4, 00:19Z).
- An expired session: `Not logged in. Run repose login.` exit 3
  (review-cli).

## A Mac owner's day (review-mac)

The customer: a developer with an M4 Mac mini (the rented e2e-mac, 24 GB,
appliance at 12 GB as a 16 GB Mac gets it), who reads own-hardware.md
first, then puts two of their own repositories on it and runs Claude Code
on Sonnet in each. CLI built from main 611068f6 (linux/amd64 on woker-1,
darwin/arm64 on the Mac); owner session, billing-exempt. Clones of the
owner's `pocketmail` and `folio` with push set to DISABLED-NO-PUSH. The Mac
was the lab's from 02:15Z to 03:29Z, so the day ran 03:41Z to 04:05Z on
hostd owned-cpu 5d6dc807, beside review-cli's two machines.

Reading the page as a newcomer (about 02:10Z, then again at 02:45Z and
04:00Z as it changed with r20261126-2 and -3): S3, S4, P2-1, W1. The page
changed three times in two hours under me, each time to say something the
lab had just measured; a customer reading it this morning read three
different products.

| Time (Z) | Step | Measured | Page says |
|---|---|---|---|
| 02:09:39 | `repose run e2e-mac-pocketmail -d` from the clone | exit 8, 0.1 s, disk refusal (P2-3) | a machine costs what it writes |
| 03:41:42 | same, host emptied by the lab | exit 0, **52.2 s**: created 36 s, sync 14 s (12 commits) | "a new one is ready in about 40 seconds" |
| 03:42:39 | `repose run e2e-mac-folio -d` | exit 8, 0.7 s, `the host has as many machines running as its cores allow` with 3 running, one of them review-cli's hung machine; nothing left behind (`ls`, `status`: no project) | "runs three at once" (page at 04:00Z; it said four until about 03:30Z) |
| 03:42 to 03:59 | Claude Sonnet in pocketmail, `claude -p` through `repose exec` | test suite installed and run, all pass, one test added, commit `0e48ec8`; `RESULT install=pass test=pass commit=yes`. MEMORY in `ls` 384 to 750 MB used, sampled every 30 s; **AGENTS `-` throughout** (P2-17 reproduced) | |
| 03:51:12 | review-cli's hung machine stopped, a slot free | | |
| 03:51:24 | `repose run e2e-mac-folio -d` (162 MB checkout) | exit 0, **81.2 s**: created 32 s, sync 46 s (history cloned from github.com) | about 40 s |
| 03:52 to 03:58 | Claude Sonnet in folio, the two agents at once | **376 s**, rc 0, three tests added, commit `cf20191`, `RESULT install=pass test=fail commit=yes` (the suite needs a library that is not on disk) | |
| 03:59:24 | `repose stop e2e-mac-pocketmail` on a terminal | returned in 2.0 s (`Stopping e2e-mac-pocketmail; 1 commit on main not fetched.`), `stopped` at 03:59:48 (24 s) | |
| 03:59:50 | `repose start e2e-mac-pocketmail` | exit 0, **24.0 s**, `is running (large), ready in 24s` (P2-16); the commit and the agent's log intact | "starts in about 33 seconds" |
| 04:00 | `git fetch repose` in each clone | both commits back, under 1 s each | |
| 03:58 to 04:01 | folio idle after its agent | MEMORY 728.4 to 532.1 to 388.7 MB used | "Memory a machine stops using goes back" |
| 03:59, 04:01 | `repose exec ... free` in both | 0.5 s each, answering | |

What this customer would say. The two agents did real work on real
repositories, at once, and the commits came back with one command: that
is the product, and it worked. The create times are 30 to 100 % over the
page's figure, and the page's figure is the hosted comparison's twin. A
16 GB Mac ran two of my agents beside someone else's two machines, and the
third was refused while one of those was hung, which I could not see from
anything repose showed me. I would pay $10 for the two-agents case; I would
not trust it with four, after reading S1 and S1b.

Not run on the day, and why: a busy machine on the Mac stopped by the
memory guard (the lab's 03:14Z and 03:22Z runs cover it; review-cli's fill
hung instead, S1b); a fifth machine (the bound is now three, and the fourth
was refused at 03:42:39Z); the dashboard on these machines (review-dash's
surface, its window had closed); idle for 24 h or frozen; a Mac restart (the
lab owns the appliance; ENG-245 is known).

Mac-side reads, read-only, at about 02:10Z: `~/Library/Logs/repose/launcher.log`
writes `park-key: event=key_unavailable ... exit 195` once a minute (P2-19);
`join-argument` still in the support folder (P2-21); `disk.raw` 100 GB
nominal, **81 GB on the Mac's disk** with the Data volume at 45 GiB free,
while the appliance said 15.7 GB free for machines: much of that was the
lab's leftovers (conductor 02:10Z), and whether space comes back to macOS
after snapshots and machines are deleted was not checked.

## Method

- Each reviewer ran on its own surface with its own CLI build and a private
  owner session; review-mac's runs are in
  /mnt/nixstore/sharing/review-mac/runs.log (wall seconds, exit codes),
  review-cli's in /mnt/nixstore/sharing/review-cli/runs.log, review-dash's
  screens in /mnt/nixstore/sharing/review-dash/shots/ and polls in
  watch.log, watch2.log. Lab numbers are quoted from the program log with
  their times, not re-measured.
- Rules that bit: "check a timing window twice": the disk refusal and the
  dashboard's 02:10Z refusal (review-dash) came during the lab's hostd swap;
  one CLI create a minute later succeeded, so P2-3 is ranked on its words,
  not on how often a customer meets it. "One number is a margin": S1b and
  S2b are single runs and say so. "The person holding the measurement
  writes the sentence": every S1 number is the lab's. "Verify before
  relaying": review-mac stamped two log lines by guess (02:13Z, 02:15Z) and
  corrected them in the log; the report uses runs.log times.
- The account is billing-exempt; no finding here is about billing.
- The page under review changed at r20261126-2 (about 02:40Z) and -3
  (about 03:30Z); each quote names when it was read.
- The ssh `Match` line in the shared ~/.ssh/repose/config was rewritten by
  each reviewer's CLI in turn (lab 17:35Z noted it first); review-mac put
  it back to ~/.local/bin/repose at the end.

## What could not be run

- A 12 GB four-busy run on owned-cpu 5d6dc807 with the bound overridden
  (lab: would pass 20 min).
- The x86 path (no x86 host in this review), and the snapshot move on an
  x86 installer host.
- A fresh `repose host add` on a Mac and `--memory` on an installed one
  (brief: never reinstall or change e2e-mac's memory).
- The dashboard's 'Not created' page after r20261126-2, the
  stopped-for-memory banner, and a start refused for cores in the
  dashboard (review-dash's window closed first).
- Host-side readings for S1b and the idle hang (the lab had finished; asked
  in the log at 03:41Z).
- A notification for a guard stop (no reviewer had the owner's channel).

## Signatures

- review-mac: written from the three, 2026-11-26.
- review-cli: PENDING.
- review-dash: PENDING.
