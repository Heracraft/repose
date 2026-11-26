# Sharing on your own hardware: the customer council, second pass

Second pass of the review of 2026-11-26 (first pass:
docs/reports/2026-11-26-sharing-council.md). Method: docs/REVIEW.md, "The
second pass". Brief: ~/briefs/sharing-review-pass2-2026-11-26.md.

Reviewers: **pass2-cli** (the CLI and a Mac owner's day on e2e-mac,
07:57-08:20Z; it replaced pass2-mac, which went silent at 06:14Z after
starting three Claude agents) and **pass2-dash** (the dashboard, 06:06-07:57Z,
section copied below from /mnt/nixstore/sharing/pass2-dash/findings.md).

What was reviewed: production api and web at main 6582471e and later; e2e-mac
(AWS M4 Mac mini, appliance with 12 GB and 8 cores) on the released hostd
2026.11.26.1, taken through its own signed updater at 06:04Z; the CLI built
from main 8dfd0ed0 (linux/amd64 on woker-1, owner session, billing-exempt).

**Gate: no P0 found.** Two P1s, one of them new on the hostd that ships.

## What would make a user scream

| # | Rank | What the customer sees | Evidence |
|---|------|------------------------|----------|
| N1 | P1 | `repose resize NAME 80G`, or the dashboard's Grow, on a **stopped** owned machine says 80G. The filesystem stays 40G after every later start. Status says `(80G disk)`. The same command on a **running** machine grows the filesystem at once (79G). | pass2-cli: CLI, stopped path n=1 through 3 starts; running path n=2. pass2-dash: dashboard, n=2 starts. |
| B1 | P1, new | A runaway program in one machine. The guard stops the newest machine (idle, as designed). Nine to thirteen seconds later the appliance's kernel kills the **runaway machine too**. The CLI calls it `the machine stopped unexpectedly (hypervisor exited 0)`, state `error`, with no word about memory. The page says repose stops one machine, the newest. Two machines lose what they held in memory. The machine in the middle answered throughout, and no machine hung. | pass2-cli, shape B, n=2 (08:10Z, 08:12Z), with the hostd log and the kernel log. |
| W3 | P2 | `repose restore` on a full Mac prints `✓ Restored e2e-p2b-walk 17s` and then `Could not restore ...` with exit 8. A project in state `error` stays behind. A refused create leaves nothing (I-1648). A refused restore does not. | pass2-cli, n=1. |
| W2 | P2 | A start refused for cores leaves the machine in state `error`, which reads as a failure. Nothing failed: the machine is stopped and its disk is safe. | pass2-cli, n=2 (citebook, walk). |

Fixed since pass 1 and confirmed on the build that ships: the S1 shape
(three busy machines, the fourth refused, nothing hangs), S1b as filed (no
hang; see B1 for what happens now), S5 (a refused create leaves nothing),
S6 (not answering shows), S8 (`--size xl` gives 40G), sizes in owned output,
the CPU refusal's wording, event order, and the `--host` hint.

## pass2-cli: the CLI and the Mac owner's day

Instrument for everything below: the CLI from main 8dfd0ed0. Every wall time
is from `date +%s.%N` around the command. Samplers in
/mnt/nixstore/sharing/pass2-cli/: `sample.sh` (every 20 s), `fast.sh` (every
7 s). Each sampler round runs `repose ls`, a timed `repose exec NAME -- true`
for each machine, and reads MemAvailable and the running `guest@*` units
inside the appliance. Host logs come from the appliance's hostd and kernel
journals, read only, through the lab's runner. Logs are in
/mnt/nixstore/sharing/pass2-cli/logs/.

### (a) Shape A: three busy Claude agents at once, the fourth refused. PASS.

- **Reproduction.** Three running machines: e2e-p2-cutroom (SvelteKit, pnpm,
  vitest), e2e-p2-giftbox (Next.js) and e2e-p2-citebook (Python). Each ran
  `repose exec NAME -- claude -p --model sonnet` in its own repo, under a
  900 s cap. Round 1 (08:04:24Z) asked for a clean install, a build, the
  tests, a small change and a local commit. Round 2 (08:06:16Z) asked for
  three clean install-and-build-and-test rounds, then a change and a commit.
  Push URLs were set to DISABLED-NO-PUSH inside the machines first.
- **The fourth.** At 08:04:37Z, with all three busy, `repose run e2e-p2b-four
  -d` from an empty folder exited 8 after 1.13 s. It printed `Could not start
  e2e-p2b-four: your host e2e-mac runs as many machines at once as its cores
  allow, and e2e-p2-citebook, e2e-p2-cutroom and e2e-p2-giftbox are running
  there. Stopping one of them frees room, or `repose run --host repose` makes
  this one on repose's servers.` `repose ls` showed no e2e-p2b project after
  it. Earlier in the walk, the same refusal with `--host e2e-mac` and on
  `repose start`: rc 8 each time, and nothing created.
- **Times (s).** Round 1 agents: citebook 40.9, giftbox 76.2, cutroom 91.0.
  A second citebook agent ran 34.4. Round 2 agents: citebook 71.4, giftbox
  124.0, cutroom 164.4. Per round as the agents reported it:
  - giftbox `npm ci && npm run build`: 35.9, 29.8, 26.6.
  - cutroom install, build and 204 tests: 36.5, 30.3, 27.7.
  - citebook venv, install and pytest: 20, 22, 19.
  - Round 1, giftbox alone-step times: `npm ci` 16.5, `next build` 18.8.
  Every agent exited 0 and committed locally.
- **No machine stopped answering.** `repose exec -- true` answered 39 of 39
  probes from 08:04:24Z to 08:09:00Z, rc 0, at most 0.56 s. The appliance's
  lowest MemAvailable was 2923 MiB. No guard stop and no kernel OOM.
  `repose ls` read 1.0 GB, 1.5 GB and 1.2 GB used at the peak.
- **What a customer would notice.** Under three busy machines, cutroom's
  vitest suite hit its 5 s default test timeout twice in 4 full runs (an
  ffmpeg test, then a migrations test). Both passed on the next run. That
  matches the slowdown the page states.
- **Falsifier:** a probe failing, or a machine stopped, with three busy.
  Not seen (n=1 window, 2 rounds).

### (b) Shape B: a memory hog in the OLDEST running machine. No hang, but two machines stopped (B1, P1).

- **Reproduction, run 1 (08:10:03Z).** Three machines running, oldest first:
  cutroom (started 06:07Z), giftbox (06:12Z), citebook (08:00Z, idle). In
  cutroom: `python3 /tmp/hog.py 11000`, which allocates and touches 256 MiB
  every 0.2 s toward 11000 MiB. The guest's MemTotal is 10551 MiB.
  - 08:10:21Z: hostd `guest_memory_guard` with `free_bytes 122880`, which
    stopped **citebook**, the newest and idle. CLI `ls`, `status` and
    `events` all said `stopped: your host e2e-mac ran out of memory, so
    repose stopped e2e-p2-citebook, the machine started last, to keep the
    others running.`
  - The hog went on, last line `6144 MiB` at +25.3 s.
  - 08:10:30Z kernel: `Memory cgroup out of memory: Killed process 949042
    (cloud-hyperviso) ... task_memcg=/guests.slice/guest@01a21677-...` (that
    is cutroom), `oom_memcg=/guests.slice`, memory usage at its 11717992 kB
    limit. guests.slice memory.swap.max is 0, as I-1656 sets it.
  - 08:10:33Z hostd: `guest died ... reason "hypervisor exited 0"`, state
    `error`.
  - CLI: `e2e-p2-cutroom: the machine stopped unexpectedly (hypervisor exited
    0). `repose start e2e-p2-cutroom` restarts it.` The event list shows a
    bare `machine error`, with no reason.
  - giftbox, the machine in the middle, answered every probe (0.43 to
    1.37 s).
- **Run 2 (08:12:07Z).** The same hog in **giftbox**, now the oldest. Start
  order: giftbox, cutroom (08:11:19Z), citebook (08:11:37Z).
  - 08:12:28Z: the guard stopped citebook again (`free_bytes 201506816`).
  - 08:12:38Z: giftbox `did not answer ssh in 15 s ... no route to host`.
  - ~08:12:40Z: the kernel memcg OOM killed giftbox's cloud-hypervisor.
  - 08:12:41Z: hostd `hypervisor exited 0`. CLI showed the same
    `stopped unexpectedly` line.
  - cutroom, in the middle, answered every probe (0.44 to 0.49 s).
- **Recovery.** `repose start` took 14.4 s for cutroom, 10.8 s for giftbox
  and 10.3 to 10.7 s for citebook. Disks were intact: the agents' local
  commits 2e34312 and f52895c were present. What was in memory was gone.
- **Expected.** own-hardware.md says that if memory runs out, "repose stops
  one machine, the one started most recently ... `repose ls`, `repose
  status` and the project's page say which machine stopped and why". The
  deliberate-absent list (2026-11-26) accepts that the guard stops the
  newest machine even when it is idle. It does not cover a second machine
  being killed, or killed with a message that hides the cause.
- **Why P1 and not P0.** Nothing hung, the machine in the middle kept
  answering, disks were kept, and recovery is one command of about 12 s.
  But a runaway build costs two machines' running agents, not one.
  The customer is told "unexpectedly (hypervisor exited 0)" for the machine
  that caused it, so they cannot tell that their own program did it, and the
  next run does the same. On repose's servers the same program is killed
  inside the machine (machine.md) and the machine keeps running. The guard
  stopped citebook, and the kernel killed the hog's machine 9 to 13 s later
  while the hog still grew. Both runs show this, so the guard's one stop
  does not keep the promise for a fast grower.
- **Instrument:** fast.sh probes, `repose ls/status/events`, the hostd
  journal and the kernel journal in the appliance
  (logs/shapeB-host.txt, logs/shapeB2-host.txt).
- **Falsifier:** a run where only the newest machine stops and the hog's
  machine keeps running, or stops with a memory reason. Not seen in 2 of 2.
  Not tested: a slower grower, which the guard may handle with a second stop
  of its own. pass2-dash's run (hog in the newest machine) is the case the
  guard handles cleanly (37 s, neighbours answered).

### (c) review-cli's pass-1 findings, re-run

| Pass-1 (review-cli) | Status | Evidence (pass2-cli) |
|---|---|---|
| F-D / S1b: a hog hangs its machine for good | **FIXED as filed (no hang), replaced by B1** | 2 of 2 runs: the hog's machine was killed within about 30 s and restarts in about 12 s. No 15-minute hang. |
| F-E: an idle neighbour hangs | not seen | Idle and busy neighbours answered every probe in shapes A and B (39 + about 60 probes). |
| F-A / S5: a refused create leaves a dead project | **FIXED** | 3 refusals (cores; plain, `--host e2e-mac`, during load): rc 8 in 1.1 s, nothing in `ls`. **Residual W3:** a refused *restore* leaves an `error` project. |
| F-B / S8: `--size xl` gives an 80G disk | **FIXED** | `repose run e2e-p2b-walk --size xl -d`: one line (`--size is ignored: ... no size and uses the memory it needs.`), status `(40G disk)`, df 40G, nproc 2. |
| F-C: a size printed for owned machines | **FIXED in text; JSON residual** | Create, start and restart lines carry no `(large)`. `ls --destroyed` SIZE is `-` for owned. `ls --json` still has `"class": "large"` beside `"sized": false` (W5, P2). |
| CPU refusal names no host and no `--host repose`, and carries `(insufficient_capacity)` | **FIXED** | Quoted in (a). The start refusal names the host and the running machines. On a start it gives only "Stopping one of them frees room", which is right because the machine cannot move. |
| N4/N8: units (`1005.6 MB used`) | **FIXED as far as seen** | Peaks read `1.0 GB used`, `1.5 GB used`, `1.2 GB used`. No value between 1000 and 1023 MB was caught, so the exact edge is not proven. |
| N5/N7: events order; a refused create or a hang has no event | **order FIXED**; refusals still have no event | Events show `stopping` 08:14:10Z, then `stopped` 08:14:13Z (and 07:59:30/34Z). No event for the refused start on citebook. A kernel-killed machine has a bare `machine error` event (B1). |
| N9: `--host` on an existing machine points at a restore that cannot move it | **FIXED** | `e2e-p2-giftbox exists and stays on the host it was made on; --host chooses where a new machine is made.` rc 2. |
| N2: `host ls --json` provider `azure` for a Mac | **still there** | `{'name': 'e2e-mac', 'state': 'online', 'provider': 'azure'}`. |
| N1: docs show SIZE `large`; hosted rows print a size under MEMORY | docs **FIXED**; hosted rows still there | own-hardware.md shows MEMORY and `✓ Created todo-app on box-a`. `repose ls` prints `large`/`xl` under MEMORY for hosted machines. |
| N6: snapshot-failed event text | not reached | No failed snapshot. |
| Hosted create line does not name `repose` | not run | No hosted machine was created. |

### (d) `repose resize 80G` on an owned machine (N1, P1, same finding as pass2-dash)

- **Stopped path.** e2e-p2-citebook was stopped. `repose resize
  e2e-p2-citebook 80G` printed `Resized e2e-p2-citebook's disk to 80G.`
  (0.90 s, rc 0).
  - After the start at 08:00:21Z: status `3.5 GB of 39.1 GB (80G disk)`,
    `lsblk` vdb 80G, `df /home/dev` **40G**.
  - Same df after the starts at 08:11:47Z and 08:13:49Z: **40G**. n=1
    machine, 3 starts.
- **Running path.** e2e-p2b-walk, at 07:59Z, and e2e-p2-cutroom, at
  08:14:09Z: `repose resize NAME 80G` (1.3 s), then df **79G at once**, and
  79G after a stop and start. Status `78.4 GB (80G disk)`. n=2.
- So the grow is applied only when the machine is running at the moment of
  the resize. pass2-dash's dashboard Grow was on a stopped machine, which is
  why it never took.
- Also seen: `repose resize e2e-p2b-walk 600G` was accepted (0.91 s, status
  `600.0 GB`). e2e-mac's whole disk.raw is at most 100 GB, and nothing
  checks against the host's disk (W4, P2).
- **Instrument:** CLI, `lsblk` and `df` in the guest.
- **Falsifier:** df about 79G after a start following a stopped resize. Not
  seen in 3 starts.

### pass2-cli P2s from the unprimed walk (07:58-08:07Z)

- W2: a start refused for cores sets the state to `error`. Status reads
  `e2e-p2-citebook  error / error: your host e2e-mac runs as many machines
  ...`, and the next start says `Restarting`. No event is recorded.
- W3: a refused restore. See the scream table.
- W4: resize to 600G is accepted on a host with at most 100 GB.
- W5: `ls --json` has `"class": "large"` for an owned machine.
- W6: `repose start` on a full Mac prints `Booting e2e-p2-citebook...`
  before the refusal (2.1 s).
- W7: a create refused for cores says `Could not start e2e-p2b-four`, but
  nothing existed to start.
- W8: `repose snapshots NAME` exits 2 with "is a project: `repose snapshots
  list NAME`". The CLI knows what you meant and refuses anyway.
- W9: the status disk line changes shape with the state: running `4.1 GB of
  39.1 GB (40G disk)`, stopped `40.0 GB`.
- W10: a restored machine's status says `no snapshot yet` although it came
  from a snapshot.
- Process note, not product: the previous session's machines had the real
  GitHub push URL inside the guests, because sync copies the remote. No push
  happened: the agents' commits stayed ahead of origin/main. pass2-cli set
  DISABLED-NO-PUSH inside each guest before its agents ran.

What worked, measured:

| Action | Time (s) |
|---|---|
| Create from an empty folder | 16.3 (`Created ... 13s`, `Ready in 16s`) |
| Start | 10.3 to 18.3 (the page says about 33) |
| Stop | 5.1 with a 661.6 MB snapshot |
| `rm --wait`, three at once | 27.2 |
| Refusal | 1.1 |

`rm` names where the final snapshot is kept. `--host nosuch` exits 2 and
names `repose host ls`. A shrink is refused with rc 2 in a clear line.

### Not run (pass2-cli)

- The memory-stop notification (it goes to the owner's Google Chat, which
  pass2-cli cannot read).
- A slower memory grower in the oldest machine.
- Four busy machines at 18G.
- An x86 host.
- A paying customer's account (the owner is billing-exempt).
- The darwin CLI on the Mac itself.
- `host add` and `host rm` (forbidden on e2e-mac).
- A hosted create, for the "names repose" line.
- A hosted resize, to compare with N1.

## pass2-dash: the dashboard (copied from /mnt/nixstore/sharing/pass2-dash/findings.md)

# pass2-dash: the dashboard, second pass (2026-11-26, 06:06-07:57Z)

Dashboard https://repose.run (api/web main 6582471e), owner account; e2e-mac on released hostd 2026.11.26.1.
Playwright, nixpkgs chromium, deviceScaleFactor 1, 1440x900 and 390x844, light and dark; every cited capture looked at at 1x.
Screens /mnt/nixstore/sharing/pass2-dash/shots/, scripts pw/, polls watch.log (06:17-07:32Z, 0 flags), hogwatch.log, probe.log.
Machines of mine: e2e-dash2-a (form create 06:09Z, destroyed 07:52Z), e2e-dash2-b (refused create 06:10Z; CLI create 07:53Z for the hog, destroyed 07:56Z).
e2e-p2-citebook stopped by me at 07:41Z with the conductor's ok (07:40Z); left stopped.

## No P0 on the dashboard.

## P1
N1 (new). Dashboard Resize "Grow to 80 GB" on an owned machine says 80 GB; the machine keeps a 40 GB filesystem.
  Repro: e2e-dash2-a (stopped) > Resize... > Grow (80 GB): "80 GB disk" in 2 s, no event. Start (07:41Z): page "2.1 GB of 39.1 GB / 80 GB disk";
  `repose status` "2.0 GB of 39.1 GB (80G disk)"; in guest lsblk vdb 80G, df /home/dev 40G. Same after a second start (07:51Z restart). n=2.
  Expected (own-hardware.md: "40 GB to begin with; `repose resize 80G` grows it"): 79G usable. Instrument: dashboard, CLI status, df/lsblk in guest.
  Falsifier: df ~79G after a start. Not seen. Not checked: whether a hosted grow behaves the same. Same class as pass-1 S8 (a line about the customer's disk that is not what happened).
  Shots: grow-*.png, start-*.png.

## P2 (new)
N2. Create on a full Mac is still clickable and shows a fake start. Form says "3 of 3 running" + reason + Stop per machine + Use repose's servers (good), but Create opens a
  project page "starting" for ~10 s (Connect commands, events, "Destroy... last snapshot is kept on your host e2e-mac"), then "404 / Not created" whose way out is
  "`repose run --host repose`" in literal backticks (CLI words in the dashboard). Nothing left behind (24 projects before and after). Shots full-*.png, fullcreate-1440-light.png, b-0.png, b-reload-390-dark.png.
N3. Machine panel says Memory "not running" for ~47 s after the machine is running while the header says "310 MB used" (running 07:41:31Z, meter 07:42:30Z; same at 06:09Z). Shots a-create-done.png, start-*.png.
N4. Billing "Running" counts each owned machine as 8 GB: 60 GB with 3 owned running (06:15Z), 52 GB with 2 (07:55Z); sidebar "Running 36 GB" (hosted only). A size for an owned machine (I-1654 says none). Owner is billing-exempt, so a paying customer's page was not seen. Shots billing-1440-light.png.
N5. Not answering residuals (S6 is fixed, see below): header keeps "running" with the healthy dot and Stop; sidebar dot unchanged; Machine panel keeps "310 MB of e2e-mac's 11.7 GB" while the list says "-"; Hosts "4 machines, 3 running" with no sign; event text shows literal backticks; banner "for 1m" at 07:45:09Z, "2m" ten seconds later. Shots na-page-*, na-list-*.
N6. Memory-stop event is labelled "failed" (red dot) on the page; CLI calls the same event "stopped, host out of memory". Shot mem-page-1440-light.png.
N7. Usage tab of a stopped machine reads "now 312 MB". Shots a-usage-*.png.
N8. Resize... at 1440x900 opens its panel below the fold: the click shows no visible change. Shot resize-1440-light.png vs resize-open-390-dark.png.
N9. Stop snapshot size "0.0 GB" (Snapshots tab). N10. Agents column truncates to "claude o c" at 1440 once the Host column shows (newp-1440-light.png).
N11. Recently destroyed: Restore does not say where it will run; Size column is all "-" for owned machines (destroyed2-1440-light.png).

## Pass-1 findings, re-run today
| Pass-1 | Status | Evidence |
|---|---|---|
| S6 / dash-1 hung machine reads running | FIXED (residuals N5) | guestd frozen in e2e-dash2-a 07:42:43Z; red banner "Not answering for 1m. Restart starts it again; what was in its memory is lost. [Restart]" at 07:45:09Z (2m26s), list "Not answering for 7m" + Restart at 1440 and 390, CLI "running not answering", event "not answering". Restart 42 s, machine answered. na-page-*, na-list-*, restart-*. |
| S5 / dash-2 create on a full Mac | FIXED in substance, residual N2 | form "on e2e-mac · 5.1 GB free · 3 of 3 running" + sentence + Stop buttons + Use repose's servers at both widths/schemes; form's Stop e2e-dash2-a: stopped in 5 s, form went to "2 of 3" live; refused create leaves nothing; Not created page survives reload. |
| Stopped-for-memory banner (pass-1 not run) | WORKS (n=1) | hog in e2e-dash2-b 07:54:07Z; guard stopped it 07:54:44Z (37 s); amber banner "stopped: your host e2e-mac ran out of memory, so repose stopped e2e-dash2-b, the machine started last, to keep the others running." at 1440/390 both schemes; list row; CLI ls/status/events same sentence. e2e-p2-cutroom and giftbox answered repose exec throughout (10 probes, 0.46-1.64 s). mem-page-*, mem-list-*. Falsifier (a neighbour stops answering, or no reason shown): not seen. Covers the "hog in the newest machine" shape only. |
| P2-2 CPU chart of 8 cores | FIXED | "100% of 2 cores"; peaks 64% (cutroom) and 85% (giftbox). cut-usage-*, gift-usage. |
| P2-13 / ENG-244 stale 11.7 GB | still there (known) | Hosts "11.7 GB · 8 cores", meter "of e2e-mac's 11.7 GB". |
| P2-16 / dash-5 owned "large" in Recently destroyed | FIXED | Size "-". |
| P2-1 docs show SIZE large | FIXED | own-hardware.md on main: `✓ Created todo-app on box-a`, MEMORY. |
| P2-25 stray "·" on host rows at 390 | FIXED | hosts-390-dark.png. |
| P2-25 sticky header shows content through | still there | destroyed-1440-light.png, destroyed-390-light.png. |
| P2-25 /projects/<name> "id is not a valid id" | still there | byname-1440-light.png. |
| P2-25 "1006 MB", "Memory in use" wrap | not seen | no machine passed 1 GB in a list; label now "Memory". |
| P2-24 / dash-7 destroy promises a snapshot that is not there | not reachable as filed (I-1648); seen only inside N2's 10 s fake page | memory-stopped e2e-dash2-b ("no snapshot yet") did get a 199.5 MB final snapshot on rm. |
| P2-23 / dash-6 boot-failure banner | not run | no boot failure. |
| dash-8 disk refusal | not run | |

## Timings (s)
Form create to running 21.2; CLI create 16.6; start 12 (memory meter 59); stop from the full form 5; not answering shown 146 after freeze; Restart 42; refusal page ~10 after Create; memory guard 37 after hog start; rm to gone ~65.

## Not run
Three/four busy machines and the hog in an OLDER machine (pass2-mac silent 06:14-07:57Z); a memory stop while the Mac is also full; the notification for a memory stop (owner's channels not readable here); a paying customer's Billing (owner exempt); boot-failure banner; disk refusal; x86 host; a hosted Resize for comparison with N1; two tabs at once.

(Note by pass2-cli: pass2-dash's "not run" items 1 and 2 are covered by pass2-cli's shapes A and B above.)
