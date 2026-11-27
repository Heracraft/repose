# Design 2 on the Mac: summary for the owner, 2026-11-27

Release conductor's wrap of the design-2 program (26-27 November). Every number
comes from `docs/reports/2026-11-26-design2-ux.md` (d2-lab, the measurer) unless
another source is named. The architecture and the build plan are in
`docs/proposals/2026-11-26-design2.md` (I-1662 and its amendments).

## What design 2 is

Today (D1), a customer's Mac runs one Linux VM, the appliance, and every sandbox
runs nested inside it. The appliance holds up to 18 GB and macOS never gets that
memory back while it runs.

Design 2 removes the appliance. hostd runs natively on macOS as a LaunchDaemon.
Each sandbox is its own Apple Virtualization.framework (VZ) VM, in its own
launchd job. Disks are APFS files, so a snapshot or restore is an APFS clone.
The network is in user space (gVisor netstack and wireguard-go to the edge).
VZ save and restore keep a sandbox's memory across a restart that repose
starts. It is one hostd codebase with a second, darwin substrate. The code is
on branch `design2` (head 66ee08f2). None of it is on `main` or released, except
the api's "parked, waits for login" state (I-1672, live but inert).

## What was proved end to end on the real Mac

The Mac: an AWS mac-m4.metal (M4, 10 cores, 24 GiB, macOS 15.8.3). The lab
host `e2e-d2n` was registered as an owned host of the owner's account and
driven through the production api and gateway with the released CLI (0.1.66),
as a customer would. D1 is repose 0.1.66 on the same Mac.

| What a user does | D1 | Design 2 | Source |
|---|---|---|---|
| New sandbox from a repo (warm, to first ssh) | 19.9-23.1 s, n=3 | 12.2-33.9 s, median 16.2, n=6 | report, measure 2 |
| Start a stopped sandbox (command returns) | 10.3-13.5 s, n=6 | 3.8-5.4 s, n=6 | re-check on d2-runtime-5 |
| Stop | 4.0-4.1 s | 1.7-2.5 s | measure 3 |
| Take a snapshot | 16.3-25.4 s (1.5 GB copy) | 10.7-13.1 s (1.1 GB), n=3 | re-check |
| Restore a snapshot in place | 66-81 s | 4.4-5.1 s, n=3 | re-check |
| Restore as a new sandbox | 67-69 s, ~0.8 GB | 5.1-5.6 s, +10-17 MB | measure 10 |
| Go build, 2 sandboxes at once | 68-77 s | 32.5-38.9 s, n=6, swap 0 | re-check |
| Go build, 3 sandboxes at once | 171-175 s | 38.9-69.1 s, n=9, swap 0 | re-check |
| Node build, 3 at once | 368-558 s, guard stopped one | 49.7-63.2 s, n=9, none stopped | re-check |
| Wake an idle sandbox (first ssh) | 1.1-1.3 s (never rests) | 0.91-0.94 s, n=3 | re-check |
| Memory after every sandbox stops | 7.63 GB held | 0 GB | measure 8 |
| `repose host restart`, memory kept | 0 of 6 on any restart path | 1 of 3 with three sandboxes (60.5 s); 3 of 3 with one (44-49 s) | re-check, measure 11 |
| A killed VM comes back by itself | 19-25 s | 11.9 s | measure 12 |
| A hostd crash or update | - | sandboxes keep running | measure 12 |
| Downloads | 780-790 Mbit/s | 806-810 Mbit/s | d2-net, program log 16:05Z |
| ssh, attach | 0.16 s, 0.58-0.85 s | 0.16 s, 0.59-0.81 s | measure 5 |

Verdict over the 14 measures: better on create, start and stop, builds, memory
at stop, snapshots, park through repose and host life; the same on ssh,
attach, agent start, network, browser and sync; worse on disk (no live grow).
Install was not compared, because design 2 has no installer.

## What is still open (board cards)

| Card | What a customer sees | Column |
|---|---|---|
| ENG-270 | Build design 2 for customers (32-39 builder days) or not | Needs you |
| ENG-266 | Resting sandboxes keep their memory (rest by pause, wake 0.9 s, three idle hold 11.9 GB) or give it back (rest by save, first touch 5.5 s at 12 GiB): pick one | Needs you |
| ENG-263 | `repose host restart` refused 2 of 3 times right after a restore: the park room counts each sandbox's whole memory after a restore (21.3 GB needed, ~17.7 GB room) | Open |
| ENG-264 | A sandbox started smaller than its size (2 vCPU / 4 GiB instead of 5 / 8) and neither `start` nor `ls` said so | Open |
| ENG-265 | A start refused for room leaves the sandbox in `error` | Open |
| ENG-268 | No installer: `repose host add` still installs the D1 appliance | Open |
| ENG-269 | A Mac already on D1 has no way to move to design 2 | Open |
| ENG-267 | No live disk grow on VZ: a grow needs a stop and loses memory (documented loss) | Parked |

Other limits from the proposal (section 6), not carded: a restart that repose
does not start (Apple menu, a macOS update, a power cut) still loses memory;
a parked sandbox comes back only after the Mac user logs in; tried on one Mac,
one macOS version, AWS hardware, never with FileVault or a sleep schedule.

## The build left

The proposal's plan is 32-39 builder days (section 5, items 2-10). The
skeleton on `design2` already has a working version of items 2-8 (launcher,
launchd, APFS, network, edge, park and rest, host restart, admission), built
and proved between 15:00Z on 26 November and 00:35Z on 27 November by
several builders. That is a prototype, not the
product, and nobody has re-estimated the plan against it. Items 9 (install,
update, D1 migration: 3-4 days) and 10 (fakes, tests, public docs: 3-4 days)
are not started.

## What shipping needs

1. The owner's go on ENG-270, and a choice on ENG-266.
2. Fixes for ENG-263, ENG-264 and ENG-265.
3. Items 9 and 10: the installer, the updater and the D1 migration (ENG-268,
   ENG-269), and `own-hardware.md` rewritten for design 2.
4. `design2` merged to `main` through the release queue, with a hostd release
   for owned hosts and an erofs guest base release for the Mac.
5. The landing's Mac charts turned on: set `MAC_DESIGN2_LIVE` to true in
   `apps/web/src/lib/data/mac-figures.ts` once the docs say the same. The
   figures were refreshed to the re-check's numbers in PR #235, with the
   switch off.

## Disclosures

- The lab Mac's throwaway `ec2-user` autologin password was printed into a
  builder's transcript (15:15Z, 26 November). It was not rotated. The Mac is
  to be released.
- 00:11Z, 27 November: four of the owner's machines on host-02 (kanali,
  verista, shule-id, job) were stopped by CLI requests from an owner session
  that a builder minted outside its e2e-only brief. Disks and snapshots were
  kept; running processes were lost. They are still stopped (progress file,
  01:00Z).
- d2-lab's kill script killed every lab VM twice (22:36Z, 22:38Z); all came
  back. The conductor's disk cleanup removed two builders' worktrees (19:30Z);
  no data was lost.

## State at the end

- Lab host `e2e-d2n` is off the owner's account. Its nine lab sandboxes
  (eight `e2e-*` and `d2rt-cite`) were destroyed and `repose host rm e2e-d2n
  -y` succeeded; `repose host ls` says "You have no hardware of your own"
  (01:32Z). The owner's 21 projects on repose's hosts were not touched.
- The AWS Mac is still rented. Its release needs the owner's AWS login
  (ENG-249).
- host-02 runs Standard_D32s_v7 until launch (ENG-262, r20261127-1). It must
  return to D64s_v7 before launch.
