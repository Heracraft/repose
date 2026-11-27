# Design 2 customer review, pass 2: the Mac and the commands (2026-11-27)

review-mac-2 reviewed the build that carries pass 1's fixes: main 1483e255, CLI v0.1.68
as released, the Mac bundle hostd-2026.11.28.2, the api, gateway and dashboard live on
repose.run, host-02 and the edge on 1483e255. It used the same lab Mac as pass 1 (AWS
mac-m4.metal, M4, 10 cores, 24 GiB, macOS 15.8.3), as owned host `e2e-d2lab` of the
owner's account. The brief is `~/briefs/design2-review-2026-11-27.md`, section "Pass 2".

| Reviewer | Surface | Evidence |
|---|---|---|
| review-mac-2 | the Mac, and every command a Mac host's customer types (pass 1's review-mac and review-cli surfaces) | this file; raw data in `/mnt/nixstore/sharing/review-mac/p2/data/` |
| review-dash-2 | the dashboard for a Mac host, at 1440 and 390, light and dark | `/mnt/nixstore/sharing/review-dash-2/report.md` (the conductor saved it), with `rows.log`, `fast.log` and `shots/` beside it |

Pass 1 is `docs/reports/2026-11-27-design2-review.md`, and d2-lab's measures are
`docs/reports/2026-11-27-design2-shipped.md`. This pass ran every pass-1 P0..P2 again,
and d2-lab's worse rows. These are held on purpose and are not filed: no live disk
grow (ENG-267), memory lost in a restart that repose did not start, ad hoc signing, and
PR #249 (not in this release).

## What would make a user scream

1. **A Mac mini that nobody touches never gives the memory back.** A resting machine
   saves its memory to disk 30 minutes after its rest begins, so 45 minutes after its
   last use, and only while the screen is unlocked. At macOS defaults the Mac locks
   itself 20 minutes after its last use. So the save never comes. Three machines
   rested on the locked Mac for 35 minutes and held 13.6 GB the whole time. No save
   happened, and a fourth machine was refused. "Idle machines give memory back" is
   true only for a Mac that someone keeps using, or one whose screen lock is off.
   The docs state both rules, but in separate sentences, and they never join them.
   (P1-1)
2. **Some messages leave the user stuck.** A refused create says "stopping a running
   machine frees room", but the machines that hold the room are resting, not running.
   On a locked Mac, `repose exec` and `repose attach` to a machine that waits for the
   unlock spin for 60 s. Then they say the machine "did not answer ssh". The real
   reason was known at once: plain ssh printed it in 0.23 s. On an offline Mac,
   `exec` still ends with `Permission denied (publickey)`. (P1-2; the owner's rule of
   2026-11-27 ranks these P1.)
3. **During a restart through repose, the CLI says the machine is broken and tells
   you to restart it.** For the whole 54 s, and for up to a minute after, `repose
   status` says `repose's service on the machine is not answering ... repose start
   e2e-p2a restarts it`. `repose ls` says `running` and `0 B held`, and never
   `parked`. A `repose start` during the restart printed `e2e-p2c is running, ready
   in 0.0s` while the Mac was down. It did no harm: memory was kept 3 of 3. The
   dashboard is the same (review-dash-2). (P2-1)
4. **The memory figure is sometimes impossible.** After a machine wakes from a save
   or a park and then runs a build, `repose ls` shows `12.0 GB held` for an 8 GB
   machine, and 30.9 GB in all on a 24 GB Mac. A machine that runs but has no new
   reading yet shows `0 B held`. (P2-2)

The worst rows of pass 1 are fixed, measured: the first touch after a rest by save
works for plain ssh, exec and attach in 2.3-3.6 s, and `repose host restart` with
three machines is back in 52-55 s with memory kept 12 of 12. Nothing in this pass
loses a machine's work or charges wrongly. **P0 is empty.**

## Ranked findings

### P0

None.

### P1

**P1-1. At macOS defaults, a resting machine is never saved, so the Mac never gets its
memory back.** (review-mac-2; this changes pass-1 P1-1.)

1. The Mac had locked by itself before 16:27Z. Its settings were as d2-core left them:
   no screensaver preference, a screen lock delay of 300 s, and automatic login.
2. Make e2e-p2a, e2e-p2b and e2e-p2c (16:29Z; 5 vCPU / 8 GB, 5 / 8 and 2 / 4). Run two
   Go builds in each (`go build -a std cmd`, empty GOCACHE; done 16:32:37Z). VZ
   footprints were 5159, 4892 and 3595 MB.
3. hostd paused them at 16:47:09Z and 16:48:09Z (`guest resting changed`, reason
   idle). `repose ls` said `frozen` and `5.0 / 4.8 / 3.5 GB held`.
4. The sampler read every 5 s. From 16:47:03Z to 17:22:50Z it showed `locked=Yes`
   and the same three footprints, 13.6 GB in all. hostd logged no `rested by save`.
5. `repose run e2e-p2d -d --host e2e-d2lab` at 16:48Z was refused, rc 8, in 0.71 s:
   `Could not start e2e-p2d: the machines running on this host hold the memory it
   gives its machines; stopping a running machine frees room.`
6. The lock comes by itself, measured twice. After a reboot with automatic login at
   17:24Z, with nobody using the Mac, it locked at **17:44:23Z, 20.0 min later**.
   Pass 1 measured the same 20 min. `caffeinate -u` every 60 s did not hold it off:
   it does not reset HIDIdleTime, which read 9323 s at 21:26Z. So a lab cannot
   imitate a person at the Mac.

The docs say "After 30 minutes of rest ... and while the Mac's screen is unlocked,
repose saves its memory" and "macOS locks the screen by itself 20 to 25 minutes after
the last use". Both sentences are true. Together they mean that the save comes only
for a machine whose rest began 25 minutes or more before the person left the Mac. On
a Mac mini on a desk, the machine the product is for, the memory stays held. The early
save "when macOS has less than a quarter of its memory free" did not come either. At
13.6 GB held on the locked Mac, `memory_pressure` read 93 percent free. With 20.5 GB
held after a wake (19:43Z), vm_stat showed 0.9 GB of pages free, but
memory_pressure read 91 percent. Which reading hostd uses is unverified.

All the saves measured below (S1-S3) needed a lab step: the screensaver was set to
never start (`idleTime 0`). That is a setting a customer can change, but the docs do
not ask for it. Rank: P1, because it reaches every Mac owner who leaves the Mac
alone. It does not rank P0, because nothing is lost: the machines keep running,
paused, and they wake in about 1 s (see "What works"). The choice is the owner's:
recommend turning off the screen lock or the screensaver, ask for it at `host add`,
or make the save not depend on the lock.

**P1-2. Messages that block the user and do not give the reason or the action.**
(review-mac-2. The owner's rule of 2026-11-27 ranks a message like this P1.)

- **The no-room refusal** (16:48Z, above) names neither the host nor the machines. It
  says "running" for machines that rest, and it says "stopping a running machine frees
  room" on a locked Mac, where the user's real choices are to stop one of the resting
  machines or to unlock the Mac. Pass 1 ranked this P3. d2-install started on a
  plain-messages slice at 19:17Z, which is not in this release.
- **exec and attach to a machine kept saved on a locked Mac** (e2e-p2b, 21:28-21:31Z).
  Plain `ssh e2e-p2b.repose true` answered at once, in 0.23 s, rc 255, with `e2e-p2b
  is resting and wakes once you unlock e2e-d2lab; its memory is kept`. That is what
  own-hardware.md promises: "a connection to one says the same at once". But `repose
  exec e2e-p2b true` spun `Connecting to e2e-p2b... 30s` and returned after 60.3 s,
  rc 1, with `e2e-p2b is running but did not answer ssh in 60 s. Last error from ssh:
  e2e-p2b is resting and wakes once you unlock e2e-d2lab; its memory is kept`.
  `repose attach e2e-p2b` under a pty did the same: a 60 s spinner, then the same
  line. hostd logged `wake held ... waits_for unlock` every 10 s. The answer was known
  in a quarter of a second, and the first half of the final line, "did not answer
  ssh", is false.
- **exec to a machine on a Mac whose hostd is stopped** (SIGSTOP 21:41:13-21:43:14Z).
  In the first 90 s: `e2e-p2a is running but did not answer ssh in 15 s. Last error
  from ssh: environment is not accepting connections yet`. "environment" is an
  internal noun. After `offline` (21:42:57Z): `... e2e-p2a is on a host the control
  plane cannot reach right now; try again shortly; e2e-p2a.user-...@ssh.repose.run:
  Permission denied (publickey).` This is pass-1's P3, unchanged. For about 20 s after
  hostd resumed, `repose ls` said `your host e2e-d2lab has not answered for 0m.` for
  each machine, while `repose host ls` said `online`.

### P2

**P2-1. A restart through repose never shows `parked`. `status` says the machine is
broken and offers `repose start`.** (review-mac-2: CLI. review-dash-2: P2 1,
dashboard. Pass-1 P2-3, not fixed.) `repose host restart -y` on the Mac, four times
(HR1-HR4, 18:43-18:51Z). `repose ls` and `repose status e2e-p2a` were read every
8 s (`hr-ls-HR*.log`):

- `ls` showed `running` and `8.3 GB held` until the park, then `running` and `0 B
  held` until about 60 s after the machines answered. It never showed `parked`. No
  line said the Mac was restarting.
- `status` showed `repose's service on the machine is not answering, so the agents and
  sessions below are old; `repose start e2e-p2a` restarts it`. It said so from 13 s
  into HR2, for the whole of HR3, and in HR1 until 18:45:01-18:45:32Z, which is 20-50
  s after the machine answered ssh (18:44:41Z).
- `repose start e2e-p2c` 25 s into HR4 (18:50:52Z): `e2e-p2c is running, ready in
  0.0s.` rc 0 in 0.10 s, while the Mac was down. The machine came back with the same
  boot_id, so the start did no harm. But the line is false, and a user who does what
  `status` advises learns nothing.

own-hardware.md promises: "While the Mac restarts, `repose ls`, `repose status` and the
dashboard show its machines `parked; mac-mini is restarting, and its memory is kept`."

**P2-2. MEMORY is wrong in two windows.** (review-mac-2; review-dash-2 P3 3-5 for the
dashboard.)

- **Over the machine's size after a wake and a build.** e2e-p2a and e2e-p2b (8 GB
  each) were woken from a park (18:44Z) and from a save (19:41Z), and each then ran
  a build. `repose ls` then showed `12.0 GB held`, `12.0 GB held` and `6.9 GB held` for
  the 4 GB e2e-p2c, which is 30.9 GB on a 24 GB Mac (18:54Z and 19:43Z, n=2). top
  showed a phys_footprint of 12G, 12G and 7115M, of which 4289M, 4281M and 1557M was
  compressed. macOS `footprint` gave 8210, 8212 and 4111 MB. vm_stat accounted for
  22.6 GB used in all. So the figure counts compressed memory at more than it costs.
  That last point is unverified: it rests on these two tools' readings, and I did not
  read the code.
- **`0 B held` for a running machine with no new reading.** 30-40 s after a create
  (16:29:48Z reading); after a start (e2e-p2c, 21:40Z); for about 60 s after each
  restart through repose (P2-1); and about 30 s after the lock read two machines back
  (21:27:05Z: `0 B held` for e2e-p2a and e2e-p2c, while the sampler read 8207 and 4109
  MB; right by 21:27:14Z). "0 B held" is the figure for a saved machine, so here it
  says the opposite of the truth.

The steady state is right. Running machines read 5.0 / 4.8 / 3.5 GB held against VZ
footprints of 5159 / 4892 / 3593 MB (16:32Z). Saved machines read `0 B held` within one
30 s reading of the save.

**P2-3. The dashboard's Add a host step for a Mac still does not say that the CLI and
`repose login` come first** (review-dash-2: P2 2, pass-1 P2-8). The line renders only
when the api sends no install command, and the live api sends one.

**P2-4. An in-place restore takes about 10 s, newest or older.** On e2e-p2c on the
locked Mac (21:36-21:40Z), an older snapshot reached the file in 11.0, 9.8 and 9.8 s
(the command took 8.7-9.3 s, n=5), and the newest in 10.1 s (n=1). `Restored e2e-p2c;
it is running.` came 0.7-1.7 s before the file answered. For comparison: pass 1, older
10.8-16.1 s and newest 5.2-5.3 s; d2-lab 2.75 s; D2 4.9-5.3 s. Whether the lock or
the new base 2026.11.27.1 (fetched at 21:32Z) slows it is unverified.

### P3

review-mac-2:

- On a locked Mac, `repose host restart` with no `-y` asks the question first (`Restart
  this Mac? ... save your work in its own apps first ...`). The lock refusal comes only
  after the answer. With `-y` it is refused in 0.17 s with the docs' sentence, and
  nothing changes. On the Mac, `repose host rm e2e-d2lab` with 3 machines on it also
  asked first (`This deletes the 8 snapshots kept on it`). I answered no, so whether it
  then refuses is unverified.
- No command deletes a destroyed machine's final snapshot before 30 days. After this
  day, `snapshots/` on the Mac held 15.7 GB: the stop and final snapshots of e2e-lk*,
  e2e-rm* and e2e-p2*, from 3 MB to 2.3 GB each. The docs say so. On a 256 GB Mac
  mini, the only way to free this space is `host rm`.
- own-hardware.md "Starting a machine takes a few seconds" still gives the 2026-11-26
  figures (start 3.8-5.4 s, create 12-34 s). Measured now: start 3.59-3.69 s (n=3) and
  create 9.1-9.7 s (n=3). lifecycle.md still says that a frozen machine's memory on a
  Mac "is saved to an encrypted file instead". That is true only after 30 minutes
  (review-dash-2 P3 11 too).
- When a Mac row is listed, the MEMORY column of `repose ls` holds the size name
  (`large`) for machines on repose's servers.
- `/Library/Application Support/repose/install.json` still says `"version":"2026.11.28.1"`
  after the updater moved to 2026.11.28.2 (16:19:52Z). Nothing a customer reads was
  seen to use it.
- The first create after a new base image took 1 min 47 s (21:32:59-21:34:39Z: hostd
  fetched base 2026.11.27.1, 2.9 GB, in 99.5 s). I did not capture the lines it printed
  while it waited (unverified).

review-dash-2 (details and shots in its report): sized-down omitted from the 390 list
row; "Memory held 8.3 GB of 8 GB" over 100 percent; header against card disagreement;
two disk sizes on the disk card; New project counts free memory, not room; the host row
stays "online · just now" through a plain restart; the Remote Control checkbox wraps at
1440; "server restart" event label for a Mac; own-hardware.md does not say hosted plans
include own hosts.

## Pass-1 findings, re-run

| Pass 1 | Now | Evidence (pass 2) |
|---|---|---|
| P0 (known, d2-lab §4): first plain ssh to a machine rested by save fails at ~8 s | **fixed** | 3 of 3 plain `ssh e2e-p2X.repose true` answered, rc 0, in **2.34, 2.83, 2.34 s** 46 min after two Go builds (resting from 15 min, by pause for 30 min, then saved 1.5 min before the touch) (S1 18:42:47Z, S2 19:40:54Z, S3 20:31:01Z). hostd `restore_ms` 1154-1826 for images of 0.95-1.22 GB. d2-lab: rc 255 after 8.1 s, 4 of 4 |
| d2-lab §4: first `repose exec` after a rest by save 19.4-37.6 s | **fixed** | **3.00, 3.19, 3.04 s**, rc 0 (n=3) |
| d2-lab §4: first `repose attach` after a rest by save 19.5-21.7 s | **fixed** | to the drawn screen **3.43, 3.57, 3.36 s** (n=3) |
| d2-lab §11: `repose host restart` with 3 machines back in 78-84 s | **fixed** | memory kept **12 of 12** (same boot_id), parked in 9.0-9.2 s, all three answering ssh **52.4-54.5 s** after the command (n=4, 18:43-18:51Z), two of the three just woken from a save each time |
| P1-1: a screen lock reads resting machines back at full size and blocks new machines | **changed**, see P1-1 below | the lock now keeps what does not fit (21:26:44Z: of three saved machines, the 8 GB e2e-p2a and the 4 GB e2e-p2c were read back in 1.4-1.9 s, e2e-p2b stayed saved; `repose ls` and `status` said `resting; it wakes once you unlock e2e-d2lab, and its memory is kept`; a new machine then started, 5 vCPU / 8 GB). New: at macOS defaults the save never comes, because the Mac locks 20 min after login and the save comes 45 min after the last use |
| P1-2: Restart question omits the Mac's own apps | **fixed** (CLI; review-dash-2 for the dashboard) | `Restart this Mac? Its machines keep their memory; save your work in its own apps first, since it may restart without waiting for them.` |
| P1-3: `snapshots restore --as` of a fleet snapshot lands on the Mac and breaks | **fixed** | `e2e-p2f` on repose's servers, snapshot, `restore --as e2e-p2g`: 7.1 s, `it is running (large)`, HOST `repose` |
| P2-1: MEMORY shows the guest's use, not what the Mac gives up | **changed**, see P2-2 and P3 | steady state right: 5.0 / 4.8 / 3.5 GB held against VZ footprints 5159 / 4892 / 3593 MB (16:32Z); `0 B held` while saved. Wrong after a wake (12.0 GB held for an 8 GB machine) and right after a start (`0 B held` for a running machine) |
| P2-2: a destroyed machine's whole disk stays 30 days | **fixed** for the disk; the final snapshot stays, as documented | `volumes/` holds only the 3 live disks (876 MB); no `kept-*.img`. `snapshots/` holds 14.7 GB of 12 final and earlier snapshots of destroyed machines (0.3-2.3 GB each), kept 30 days as own-hardware.md says, with no command to delete one sooner (P3) |
| P2-3: `parked` never shown during a restart through repose | **not fixed** | P2-1 below (CLI) and review-dash-2's P2 1 (dashboard) |
| P2-4: `repose rm` during a hostd restart leaves the machine in `error` | **fixed** (n=1 in the window) | `repose rm e2e-p2e -y --wait` sent at 21:40:54.9Z, `launchctl kickstart -k` of hostd at 21:40:55.3Z: `Destroyed e2e-p2e in 9.8s.`, rc 0; hostd destroyed it after its restart |
| P2-5: in-place restore of an older snapshot 10.8-16.1 s | **changed**: older 9.8-11.0 s, but the newest is now as slow | on the locked Mac, e2e-p2c: older snapshot to the file 11.0, 9.8, 9.8 s (n=3; the command 8.7-9.3 s, n=5), the newest 10.1 s (n=1). Pass 1: older 10.8-16.1, newest 5.2-5.3; d2-lab 2.75 s; D2 4.9-5.3 s. See P2-4 |
| P2-6: `host rm --help` says snapshots stay | **fixed** | `removing the host deletes them, and repose has no copy` |
| P2-7: `repose restore NAME` over a live NAME says `no snapshot left` | **fixed** | `A project called e2e-p2g already exists. Restore under another name with `repose restore e2e-p2g --as NEW-NAME`.` rc 2, 0.11 s |
| P2-8: Add a host's Mac step does not say the CLI comes first; Remove on a Mac | review-dash-2: Remove **fixed**; Mac step **not fixed** for customers (the line renders only when the api sends no install command) | review-dash-2 report, P2 2 |
| P3: `exec` to an offline host ends with `Permission denied (publickey)` | **not fixed** | P1-2 below, 21:42:57Z |
| P3: `host rm` on the Mac prints nothing; question names only snapshots | **changed** | `host rm --local` now asks `The 3 machines on it cannot be recovered: their disks are on this Mac and repose has no copy.`; not removed in this pass (others used the host) |
| P3: own-hardware.md start and create figures from 2026-11-26 | **not fixed** | "returned in 3.8 to 5.4 seconds ... ready in 12 to 34 seconds" on main; measured now: start 3.59-3.69 s to return and +0.74-0.78 s to ssh (n=3, 21:35Z, locked Mac), creates 9.1-9.7 s (n=3, 16:29Z) |
| P3: `start --json` prints `ready in 0.0s` | **fixed** for `--json` (stdout is JSON only); the human line still says `ready in 0.0s` for a no-op start during a restart (P2-1) | |
| P3: `snapshots list` to the minute | **fixed** | `2026-11-27 16:33:08` |
| P3: Login Items "Unknown Developer" | by decision, not re-filed | |

## What works, measured

| What | Measured (pass 2) | The bar |
|---|---|---|
| First touch after a rest by save: two Go builds, then 46 min idle (paused at 15 min, saved at 45 min, touched 1.5 min later) | plain ssh **2.37, 2.83, 2.34 s**; `repose exec` **3.00, 3.19, 3.04 s**; `repose attach` to the drawn screen **3.43, 3.57, 3.36 s**; all rc 0 (S1-S3, 18:42-20:31Z). hostd `restore_ms` 1154-1826 for images of 0.95-1.22 GB; `save_ms` 2276-3838 | d2-lab: ssh fails 4 of 4, exec 19.4-37.6 s, attach 19.5-21.7 s; D2 by save 5.5 s; docs "about 10 to 15 seconds" after builds (now faster than the docs) |
| First touch of a machine resting paused on a locked Mac, after 35 min of rest | ssh **1.01 s**, exec **1.16 s**, attach **1.50 s** (L1, 17:22Z), hostd `wake_ms` 24-27; after the lock read it back: ssh 1.03 s, exec 1.19 s (21:31Z) | D2 by pause 0.91-0.94 s; docs "under a second" |
| `repose host restart -y`, three running machines, two just woken from a save | **memory kept 12 of 12** (boot_id the same each time, 4 runs back to back); `Parked 3 machines in 9.0s / 9.1s / 9.2s / 9.0s`; all three on ssh **52.4-54.5 s** after the command; hostd image read 9.7-12.8 s | d2-lab 78-84 s; docs 54 to 70 s |
| Lock with three saved machines (21:26:44Z) | read back e2e-p2a (8 GB) and e2e-p2c (4 GB) in 1.4-1.9 s (`guest restored from its rest save at the lock`), kept e2e-p2b saved (`no room to restore it and start another machine`); `ls` and `status`: `resting; it wakes once you unlock e2e-d2lab, and its memory is kept`; plain ssh to it got that sentence in 0.23 s; a **new machine then started** with 5 vCPU / 8 GB | pass 1: everything read back at full size (20.5 GB), new machines refused |
| Restart refused while locked | `-y`: 0.17 s, rc 1, the docs' sentence, nothing changed | as written |
| Restart question | names the Mac's own apps | pass-1 P1-2 |
| Restore `--as` of a fleet snapshot, Mac owned | lands on repose's servers, running in 7.1 s | pass-1 P1-3 |
| `repose rm` during a hostd restart | destroyed in 9.8 s, rc 0 | pass-1 P2-4 |
| `repose rm` frees the disk | 3 live disks (996 MB) to an empty `volumes/`; data volume +2.1 GB; no VZ process, no caffeinate after | pass-1 P2-2 |
| Rest by pause at 15 min | 3 of 3 each round (16:47, 17:43, 18:11, 19:09, 19:59, 20:49Z) | "about 15 minutes" |
| Rest by save at 30 min of rest, unlocked | 3 of 3 each time, 30 min 8-31 s after the pause (18:41, 19:39, 20:29, 21:19Z) | "After 30 minutes of rest" |
| Create on the Mac | 9.3, 9.7, 9.2 s to `Ready` (16:29Z); the third machine sized down with the docs' line | docs 12-34 s; d2-lab 8.5-13.6 s |
| Start | 3.69, 3.59, 3.60 s, ssh +0.74-0.78 s (locked Mac, 21:35Z) | D2 4.5-5.7 s with ssh; d2-lab 1.57-1.99 s (faster than now, unverified why) |
| Self-update | the updater swapped 2026.11.28.1 for 2026.11.28.2 at 16:19:52Z by itself (`event=swapped`, `event=updated`), hostd started on it at 16:20:02Z, then `current` every 30 min; 0 machines ran then | "within about half an hour" |
| A restart repose did not start (`sudo shutdown -r now`, 17:22:56Z) | the machines came back cold, running at 17:23:47Z, memory lost, as documented | as written |
| `host rm --help`, `restore NAME` over a live name, snapshot times to the second, `start --json` | as cli.md says | pass-1 P2-6, P2-7, P3 |
| Offline host | `offline` after about 100 s with hostd stopped; the VMs kept running (uptime 1 h 29 min after); reachable again about 25 s after SIGCONT | "after 90 seconds" |
| hostd.log | ids, states, durations, sizes only | OBSERVABILITY.md |

Builds for scale: two `go build -a std cmd` back to back in three machines at once
took 80.7-90.4 s on the 5 vCPU machines and 129-142 s on the 2 vCPU one (n=6 rounds,
`builds.log`).

## Method

review-mac-2 ran as a customer on the docs (own-hardware, troubleshooting, lifecycle
and cli on main) and read no code. The released CLI v0.1.68 was installed with
install.sh, on woker-1 (the laptop, `HOME=/tmp/rv2`, an empty directory) and on the
Mac. The Mac's host e2e-d2lab updated itself to 2026.11.28.2 at 16:19:52Z, before any
measurement. Machines: e2e-p2a, e2e-p2b and e2e-p2c on the Mac (16:29-21:44Z); e2e-p2d
and e2e-p2e (21:32-21:41Z); e2e-p2f, e2e-p2g and e2e-p2h on repose's servers (16:32-16:56Z).
All are destroyed. Nothing else was touched.

The order of the day: a locked round (L1, 16:29-17:22Z), three save rounds unlocked
(S1-S3, each after two Go builds and 46 min idle, with touch modes rotated so that
each mode ran n=3), four host restarts after S1, and a lock round with three saved
machines (L2, 21:19-21:31Z). After L2 came the CLI checks on the locked Mac: start,
restores, rm during a hostd restart, and an offline host.

Instruments: bash `$EPOCHREALTIME` on woker-1; a Mac sampler every 5 s (lock by ioreg
`CGSSessionScreenIsLocked`, memory_pressure, each VZ footprint by `top`, caffeinate);
`repose ls` and `host ls` every 30 s (`states.log`), and every 8 s during restarts;
hostd.log tailed live and copied at the end (`hostd-final.log`); the d2-lab pty timer
`attachtime.py`; `touch.sh` (waits for `frozen`, or `frozen` with `0 B held`, then 30
s, then one first touch per machine); `hr.sh` (restart, each machine's boot_id before
and after, ssh until each answers).

Lab steps, each one logged in `runs.log`:

- reboots to unlock: 17:22:56Z; 17:24Z again, because macOS had logged in locked, with
  `autoLoginUserScreenLocked` set back to false; 17:53Z; 21:45Z at the end;
- `defaults -currentHost write com.apple.screensaver idleTime 0` from 17:53Z, so that
  any rest by save could happen. It was removed at 21:20Z, and macOS's default is back;
- `pmset displaysleepnow` at 21:26:44Z to reach the lock (it locked at once), because
  the screensaver did not start within 6 min of restoring the default;
- `kill -STOP` / `-CONT` of hostd for the offline path, and `launchctl kickstart -k`
  for the hostd restart.

Owner CLI sessions were minted into review-mac-2's own config directories and re-minted
every 40 min. They were never printed. At the end they were removed with `rm -P` on
the Mac and `shred -u` on woker-1. The aws login for ops/dev/macmini had expired
(16:27Z), so the Mac was reached by ssh to its known address. No AWS call was made.

Rules that bit:

- **Measure rather than assume.** `caffeinate -u` was assumed to imitate a person at
  the Mac. It does not reset HIDIdleTime, and the Mac locked at 17:44:23Z in the
  middle of round S1. That round was redone from 17:53Z.
- **Check a timing window twice.** `repose status`'s "not answering" was read at
  18:45:01Z and then 30 s apart. It cleared by 18:45:32Z after HR1, and it persisted
  through the whole of HR2 and HR3. The same applies to `0 B held`.
- **An instrument hazard, again:** a `pkill -f "touch.sh S1"` matched my own shell's
  command line and killed it (17:52Z), as in pass 1. Nothing else was touched; later
  stops used PIDs.
- **Silent is not deaf.** review-dash-2 asked in the log, at 16:29Z and 17:25Z, for a
  window to create two machines. I read the log only at 19:18Z. The conductor's 17:58Z
  ruling closed it, so review-dash-2's create and Start agent are Not run because of
  me.
- **My own wrong selector:** two of the first five older-snapshot restores waited for a
  file that those snapshots predate. They are excluded, and only the three valid ones
  are reported.

## Not run, and why

- **Unlock after the lock.** It cannot be done from ssh. This leaves two questions
  open: whether the memory is given back a minute later, and what e2e-p2b, kept saved
  at the lock, does at the unlock now that a new 8 GB machine has taken the room it
  would need (20 of 20 GB committed at 21:34Z). The second is a real customer state.
- **The early save when macOS is short.** It never fired. memory_pressure stayed at
  84-96 percent free, even with 0.9 GB of pages free (P1-1).
- **A first touch after a rest by save of an idle machine** (the docs' 3.3-3.7 s).
  Every save round followed builds.
- **A create on an unlocked Mac while three machines are saved.** The lock round did
  it on a locked Mac instead (it worked, 5 / 8 GB).
- **An update with machines running.** The only update came at 16:19Z with 0
  machines.
- **Sleep, a MacBook lid, Wi-Fi loss, FileVault, the Apple menu restart, the 6-hour
  limits, a non-exempt account.** These have the same reasons as pass 1.
- **`host rm` and a reinstall.** Others were using the host, and pass 1 measured it.
- **The dashboard.** It is review-dash-2's surface; see its report and its Not run.

## Sign-off

- review-mac-2: written and signed, 2026-11-27.
- review-dash-2: its role ended before this file existed (DONE 18:50Z). Its findings
  are cited from its report, with its evidence, and none of its ranks was changed. Its
  P0 and P1 were empty, and its P2 1 (parked) and P2 2 (Add a host) are P2-1 and P2-3
  here. The conductor relays the signing request.
- review-dash-2: signed, 2026-11-27, with three notes and no disagreement on rank (relayed by the conductor, verbatim).
  1. P2-1 (parked), the dashboard half, in full: HR2 was read 17 times, 3 s apart,
     18:47:21-18:48:11Z (fast.log). The list showed each machine as "running · 0 B held"
     with Stop offered from 18:47:27Z. The page header said "running · 0 B held" while the
     card on the same page said "Memory held 368 MB of 8 GB" and Disk "40 GB" with no use
     figure. The host row said "online · just now". After the restart the events list shows
     only "machine running" (18:44, 18:48): nothing says the Mac restarted or that the memory
     was kept. HR1 and HR3 read the same in rows.log and fast.log. I did not read HR4. Stop
     offered on a parked machine is part of this finding.
  2. P2-2: I agree it moves up from my P3 3-5 to P2. review-mac-2's evidence (12.0 GB held for
     an 8 GB machine; 0 B held after a create, a start, the lock and each restart) is wider
     than mine. On the dashboard I measured "Memory held 8.3 GB of 8 GB" at 18:47:21Z, and
     "0 B held" for running machines at 17:24:02-17:25:13Z, 17:54:00Z and through HR1-HR3.
  3. P1-1 and P1-2 have a dashboard half that I could not measure as a customer, because I
     created nothing. While the Mac was locked (16:47-17:22Z) the dashboard showed
     "frozen · 5 GB held" (true). No line said that the save waits for the unlock. New
     project said "on e2e-d2lab · 12.4 GB free · 3 running" at 17:58Z, while the three
     machines held all 20 GB of room (my P3 8). So the dashboard invites the create that P1-2
     shows being refused, with the refusal's wrong advice. The dashboard create on a full or
     locked Mac is Not run (no window; conductor ruling 17:58Z).
