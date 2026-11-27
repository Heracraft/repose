# Design 2 customer review: the Mac, the commands, the dashboard (2026-11-27)

Three customer reviewers covered the shipped build: main 8e695642 (batch
r20261127-3), CLI v0.1.67 as released, Mac bundle hostd-2026.11.28.1, api and
dashboard live on repose.run. They worked on one lab Mac (AWS mac-m4.metal, M4,
10 cores, 24 GiB, macOS 15.8.3) as an owned host of the owner's account. The brief is
`~/briefs/design2-review-2026-11-27.md`.

| Reviewer | Surface | Their own report |
|---|---|---|
| review-mac | the Mac as a customer, from repose.run/docs/own-hardware alone: install, remove, lock, sleep, rest and wake, many machines, host restart, updates | this file, section "review-mac" |
| review-cli | every command a Mac host's customer types | `/mnt/nixstore/sharing/review-cli/report.md` |
| review-dash | the dashboard for a Mac host, at 1440 and 390, light and dark | `/mnt/nixstore/sharing/review-dash/report.md` (the conductor saved it from review-dash's final message) |

This report does not file again what d2-lab measured
(`docs/reports/2026-11-27-design2-shipped.md`, branch d2-lab-shipped, PR #239), or what
is known and queued for the next release:

- plain ssh to a machine rested by save fails at 8 s (PR #238);
- slow restores after builds (read-ahead in #238);
- `restore --as` printing `(large)`;
- the stray `Note:` on stop;
- `host rm` refused right after `rm` (#240).

It cites them where a new finding depends on them.

## What would make a user scream

1. **The Mac locks itself, and then the machines take back more memory than they gave
   up.** On macOS defaults a Mac locks about 20 minutes after login. At the lock,
   repose reads every machine that rests by save back into memory, each at its whole
   size. Three machines that held 10.0 GB before their rest held 20.5 GB after the
   lock, on a 24 GB Mac, and every new machine was refused. On a locked Mac,
   `repose host restart` is refused too, and so is the dashboard's Restart from "any
   computer". A Mac mini on a desk that nobody touches is locked most of the time. So
   "memory back at rest" and "restart from anywhere" stop working after about 20
   minutes. (P1-1, review-mac)
2. **Restart from any computer can close the Mac's own apps without saving, and the
   question does not say so.** The dashboard asks "Restart e2e-d2lab? Its 3 running
   machines are parked with their memory, then the Mac restarts; ...". The CLI asks
   "Restart this Mac? Its machines keep their memory." Only the docs say "repose may
   restart the Mac without waiting for them". (P1-2, review-dash; review-mac found the
   same in the CLI)
3. **The first plain ssh to a machine that rested by save fails**, rc 255 after about
   8 s, 4 of 4 runs (d2-lab). This is known and the fix is queued in PR #238. It is
   listed here because a customer meets it first. It is not filed again.
4. **The numbers for memory say the opposite of what the Mac gives.** `repose ls` and
   the dashboard show "346 MB used" for a machine that holds 3.9 GB on the Mac. A
   machine that rests by save holds 0 GB and still shows the same 346 MB. (P2-1,
   review-mac and review-dash)
5. **A restore of a fleet snapshot `--as` a new name, once you own a Mac, makes a
   broken project on the Mac.** The command's own advice is then refused. The fix is
   on #238 and is not released. (P1-3, review-cli)
6. **Destroyed machines keep their whole disks on the Mac for 30 days.** After one
   morning of creates and destroys, the Mac held 36.8 GiB of disks of destroyed
   machines and 15 GB of snapshots. The docs say `repose rm` frees a machine's disk.
   (P2-2, review-mac)

No finding in this pass loses a machine's work or charges wrongly, so P0 is empty. Item
3 would be P0 if it were not already queued.

## Ranked findings

The ranks follow how a finding reaches a customer, not how hard it is to fix. Each
finding names the reviewer whose evidence holds it. The evidence for review-cli and
review-dash findings is in their own files, which this report cites and does not
replace.

### P0

None new. The known P0 (the first ssh after a rest by save, d2-lab §4) is queued in
PR #238.

### P1

**P1-1. A screen lock reads resting machines back at their full size and blocks new
machines; a Mac at defaults locks itself about 20 minutes after login.** (review-mac,
host e2e-d2rm, 12:02-12:30Z)

1. Restart the Mac unlocked, with automatic login on. The session started at 12:02Z.
   `sysadminctl -screenLock status` reports a delay of 300 s. The screensaver has the
   macOS default (no `com.apple.screensaver` domain).
2. Create e2e-m1, e2e-m2 and e2e-m3. They got 5 vCPU / 7924 MB, 5 / 7924 and 2 /
   3907 (the sized-down line said so). Make them touch memory (`dd` of 3000, 3000 and
   1500 MB to /dev/shm, then remove it). The VZ footprints, read with `top` every 5 s,
   were 3864, 3860 and 2248 MB, 10.0 GB in all.
3. Leave them idle. At 12:21:36-38Z hostd logged `guest rested by save` three times,
   `save_ms` 3712, 5376 and 5386, `image_bytes` 235-252 MB. No VZ process remained.
   `caffeinate` (on behalf of hostd) stayed.
4. At 12:22:10Z, 20 minutes after login, with nobody touching the Mac: hostd logged
   `screen locked: machines resting as saves restored; they rest paused` (`guests` 3,
   `duration_ms` 2733). ioreg showed `CGSSessionScreenIsLocked=Yes`. The VZ footprints
   became **8205, 8205 and 4107 MB (20.5 GB)**, twice what the same machines held before
   their rest. Free memory went from 95 to 89 percent in memory_pressure's terms.
5. `repose run e2e-m4 -d`, then e2e-m5 and e2e-m6, at 12:22:28Z: all refused, rc 8, in
   0.64-1.22 s, with `Could not start e2e-m4: the machines running on this host hold
   the memory it gives its machines; stopping a running machine frees room.` hostd
   answered `insufficient_capacity`. `repose ls` meanwhile showed all three as
   `running frozen` and `346.1 MB used` / `349.6 MB used` / `302.3 MB used`.
6. `repose host restart -y` on the locked Mac: refused in 0.59 s, rc 1: `This Mac's
   screen is locked, and a locked Mac cannot save a machine's memory. Nothing
   restarted; unlock it and run `repose host restart` again.`

What the docs promise: "When the screen locks, repose reads every resting machine back
at once ... and they rest with their memory held until a minute after you unlock it."
That happened. The docs do not say that the hold is each machine's whole size, more
than it used. They do not say that macOS's default lock comes by itself after about 20
minutes. They do not say that on a locked Mac no machine starts beyond what its
memory already holds, and no restart through repose works. The earlier boot showed the
same: d2-core's 10:55Z restart came back locked, and from 11:36 to 11:44Z every rest was
by pause, with memory held (review-cli's e2e-rc1/rc2, review-mac's sampler).

Who it reaches: every Mac owner who leaves the Mac alone, which is the use the product
sells ("agents keep running after the laptop closes"). The bar is a Linux box over ssh,
which never locks. The docs could recommend a setting (screen lock off, or a long
delay), or repose could ask for one. That is the owner's choice, and this report does
not make it. Not run: whether an unlock gives the memory back a minute later. An unlock
from ssh is not possible, and a reboot loses the machines' memory.

**P1-2. The Restart question does not warn that the Mac's own apps can close without
saving.** (review-dash: P1 #1, Settings -> Hosts -> Restart..., 10:36Z, shots
`restart-ask-3run-*`; review-mac: the CLI's question under a pty, 12:06Z, `Restart this
Mac? Its machines keep their memory.`) own-hardware.md says "Save your work in the
Mac's own apps first: repose may restart the Mac without waiting for them." The
dashboard button works from any computer, where the person pressing it cannot see the
Mac's screen. Not run: whether an unsaved document in a Mac app is lost. Nobody pressed
Restart with one open, so this stays P1 and is not P0.

**P1-3. `snapshots restore --as` of a fleet snapshot, once you own a Mac, makes a
broken project on the Mac.** (review-cli: R1, n=1, 08:51Z.) The command failed with
`not in the store (not_found)` and left `e2e-rc-f2` in `error` on e2e-d2lab. Its own
advice, `repose start`, is refused, and `ls --destroyed` has no row after `rm`. d2-core
fixed this on d2-core-4 (PR #238, TestRestoreAsNewGoesWhereTheSnapshotIs). It is still
in the released build.

### P2

**P2-1. MEMORY shows what the guest uses, not what the Mac gives up.** (review-mac,
review-dash P2 #4.) review-mac's machines at 12:15Z: `repose ls` showed 349.5, 347.8 and
303.4 MB used, while the VZ footprints were 3864, 3860 and 2248 MB. While resting by save
(0 GB on the Mac) the rows still said 346.1 / 349.6 / 302.3 MB used. After the lock's
restore (20.5 GB on the Mac) they said the same. review-dash saw the same on the list,
the machine page and the host row ("3 machines, 3 running" while all rested), from
09:13Z to 11:41Z. own-hardware.md says "repose ls and repose status show what each
machine uses, under MEMORY". On a Mac, the number a customer needs is what macOS lost,
because that decides whether the next machine starts (P1-1).

**P2-2. A destroyed machine's whole disk stays on the Mac for 30 days.** (review-mac,
e2e-d2lab as d2-lab, review-cli and review-dash left it, 11:27Z.) `du -k` of
`/Library/Application Support/repose/volumes` showed 40.1 GiB: 22 `kept-*.img` files
(destroyed machines' disks, 0.28-4.19 GiB each, 36.8 GiB in all) and 3 live disks
(3.3 GiB). `snapshots/` held 15 GB more. Removing the host at 12:01Z freed 38.3 GiB of
the data volume (`df -k`, 86281872 -> 126470492 KB). troubleshooting.md says "repose rm
frees a machine's disk, and its final snapshot stays on the host's disk for 30 days".
The final snapshot is the whole disk, so `rm` frees nothing for 30 days. On a 256 GB
Mac mini, a week of throwaway machines fills the disk. The docs name no command that
deletes a kept disk early. review-mac did not look for one in the CLI.

**P2-3. The dashboard never showed `parked` during three restarts through repose.**
(review-dash: P2 #2, 13 readings at 10:11-10:21Z, each `running`, with Stop offered.)

**P2-4. `repose rm` during a hostd restart fails with `systemctl kill: launchctl:
context canceled (internal)` and leaves the machine in `error`.** (review-cli: R2,
n=1, 10:56Z; a retry destroyed it in 3.97 s.)

**P2-5. An in-place restore of an older snapshot takes 10.8-16.1 s to the file**,
against 5.2-5.3 s for the newest, and it says `running` seconds before the machine
answers. (review-cli: R3, n=3 against n=2.)

**P2-6. `repose host rm --help` says the snapshots stay on the host's disk; the docs
say removing the host deletes them.** (review-cli: R5, text only.) review-mac's runs
match the docs: the question names the count (`This deletes the 16 snapshots kept on
it; repose has no copy.`), and afterwards a restore is refused with `That snapshot was
kept on your host e2e-d2lab, which was removed, so it is gone; repose had no copy`. A
customer who trusts `--help` can believe the snapshots outlive the host.

**P2-7. `repose restore NAME` while a live project has that name says `has no snapshot
left to restore` (rc 4).** cli.md says it asks for a name or exits 2 naming `--as`.
(review-cli: R4, n=1.)

**P2-8. The dashboard's Add a host step for a Mac does not say the CLI must be
installed first**, and its Remove on a Mac does not say that repose stays on the Mac or
name the machines on it. (review-dash: P2 #3 and #5, text and shots.) In the CLI, `repose
host rm` run on the Mac removes everything (review-mac, P3 below).

### P3

review-mac:

- **`repose exec` to a machine on an offline host waits 15 s and then prints
  `Permission denied (publickey)`.** With hostd stopped by SIGSTOP (12:24:11Z), the
  output was: `e2e-m1 is running but did not answer ssh in 15 s. Last error from ssh:
  e2e-m1 is on a host the control plane cannot reach right now; try again shortly;
  ...: Permission denied (publickey).` The first half is right. The second half sends
  a customer to their keys. The docs say run and attach wait up to 10 minutes for the
  host.
- **The ControlPath advice does not apply to the CLI's own ssh.** With a long HOME,
  `repose run` printed `set a shorter one for Host *.repose in ~/.ssh/config, above its
  Include line`. That line was added, and `repose exec` failed the same way 3 of 3
  times: the CLI sets its own path. Rare (a home path over about 60 characters), but
  the message names a fix that does not work.
- **`repose stop` returns while the machine is `stopping`, and a create right after
  is refused for room.** The refusal says "stopping a running machine frees room", which
  the user has just done (12:27Z; 6 s later the create worked in 6.8 s). This is the
  same class as the queued `host rm` right after `rm` (#240).
- **`repose host rm` on the Mac prints nothing when it works** (rc 0, 2.48 s and
  0.67 s). Its question names the snapshots it deletes. It does not name repose itself,
  the machines' disks or the memory, which also go.
- **Login Items shows `hostd` and `repose-hostd-update` from "Unknown Developer"**
  (`sfltool dumpbtm`). The binaries are ad hoc signed (`codesign`: Signature=adhoc, no
  TeamIdentifier). Ad hoc signing is by decision (DECISIONS, the design 2 Mac bundle
  entries), so this is the label a customer reads, not a defect in the decision.
- **own-hardware.md "Starting a machine takes a few seconds"** still gives the
  2026-11-26 figures (start 3.8-5.4 s, create 12-34 s). Shipped is faster (d2-lab: start
  1.57-1.99 s, warm create 8.5-13.6 s). review-dash's #11 lists more stale lines
  (troubleshooting's "three machines at once on eight cores", lifecycle's "a second or
  two").

review-cli (details in its report): `start --json` prints `ready in 0.0s`; size names on
the Mac (`(large)`, `"class": "large"`, and `resize --size` says a Mac machine "uses
the memory it needs"); error codes in brackets (`(invalid_argument)`, `(internal)`); a
capitalised `E2e-rc-f2`; the no-room refusal on a create says "start" and names neither
the host nor the machines; the waiting-host refusal offers no way out; check order off a
Mac; help lines broken mid-sentence; `stop --help` contradicts cli.md about the fetch;
snapshot times to the minute; MEMORY `-` for a just-started machine; the ssh config
follows the last CLI that ran; `host restart` has no `--json`.

review-dash (details in its report): Escape throws away the one-time join step; Memory
"not running" for a running machine's first 15-45 s; sized-down shown only in the
Machine panel; the cold-restart event calls the Mac "the server" and does not say memory
was lost; "about 20 MB" in the dialog against 49 MB installed (review-mac measured
`current/` at 49 MB, the docs say about 50 MB); "e2e-d2lab ?" with a stray space;
"aarch64" under the host name; "gone with host" is ambiguous once a host of the same name
is back; start click to `running` 4.3-8.2 s against the CLI's 1.6-2.0 s.

## What works, measured

review-mac, on the lab Mac, from the docs alone:

| What | Measured | The docs, or the bar |
|---|---|---|
| CLI install on the Mac (`curl ... install.sh \| sh`) | v0.1.67 darwin/arm64, checked, into ~/.local/bin | as written |
| `repose host add e2e-d2rm` to joined | **49.1 s** (12:03Z); `e2e-d2lab` again **33.0 s** (12:29Z); four lines: installing, fetching 2026.11.28.1, waiting, `joined your account in 49s.` | d2-lab 32.4-64.0 s; D1 46.7-86.2 s to online |
| "Nothing of yours is touched" | `defaults read` of loginwindow and `pmset -g custom` before and after the install: no change (the one diff was review-mac's own lab write) | as written |
| Install size | `current/` 49 MB, base image 2.7 GB | "about 50 MB", "about 3 GB" |
| Sizes | 5 vCPU / 7924 MB twice, then 2 / 3907 MB with the docs' sized-down line in `run`, `ls`, `status`; 40G disk | as written |
| A machine created while two run, after one stops | 5 / 7924 MB, ready in 6.8 s | "a start after one of them stops gives it all" |
| Keep-awake | caffeinate on behalf of hostd: PreventUserIdleSystemSleep while running, and while resting by save (12:22Z); **none** with 0 machines (before the install, and after `host rm`) | "while a machine runs or rests" |
| Rest by save, unlocked | 3 of 3 at 15.5-16.5 min idle; saves 3.7-5.4 s; VZ processes gone | 0 GB at rest |
| First `repose exec` of a machine resting by pause, locked | 1.34 s | D2 pause 0.91-0.94 s |
| Restart refused while locked | 0.59 s, the docs' sentence, nothing changed | as written |
| Host offline (hostd stopped by SIGSTOP 150 s) | `offline` between 90 and 105 s (15 s readings); `ls` said `your host e2e-d2rm has not answered for 2m.`; back `online` 30-40 s after SIGCONT; the 3 VMs kept running | "after 90 seconds" |
| `host rm` with machines on it | refused, rc 1: `e2e-d2rm still has e2e-m1, e2e-m2, e2e-m3 and e2e-m4 on it. `repose rm` each of them first.` | as written |
| `host rm` on the Mac | 2.48 s and 0.67 s; afterwards no `/Library/Application Support/repose`, no `/Library/Logs/repose`, no LaunchDaemons, no hostd, no caffeinate; +38.3 GiB free | "all of it goes" |
| Reinstall after rm | works (33.0 s), the old name reused | as written |
| A destroyed machine of a removed host | `ls --destroyed` says `gone with host e2e-d2lab`; restore refused with the docs' sentence | as written |
| `host add` on a Mac that is a host already | refused: `This Mac is e2e-d2rm already; `repose host rm e2e-d2rm` takes it off first.` | as written |
| hostd.log (1572 lines) | keys are ids, states, sizes, durations, reasons; no guest paths, emails or tokens | OBSERVABILITY.md |
| Updater | `run.repose.hostd-update` StartInterval 1800; `event=current version=2026.11.28.1 newest=2026.11.28.1` four times | "every 30 minutes" |

review-cli and review-dash list what worked for them, measured, in their own reports.
In short: every refusal changes nothing; start, stop, snapshots, attach and ssh match or
beat D2; the dashboard's create takes 10.7 s; and an Overview page does not wake a
resting machine.

## Method

review-mac ran as a customer. It read the public docs (repose.run/docs/own-hardware,
install, troubleshooting, cli, limits, fetched at 11:24Z) and no code. It installed the
released CLI v0.1.67 with install.sh, on woker-1 (as the laptop, in
`/mnt/nixstore/sharing/review-mac/home`) and on the Mac. It removed d2-lab's host
e2e-d2lab (16 snapshots, all of e2e-* machines), installed its own e2e-d2rm, made
e2e-m1..m4 on it, removed all of it, and installed e2e-d2lab again for the others
(12:01-12:30Z, an exclusive window the conductor granted at 11:29Z, which started early
once both other reviewers had no machine on the host).

Instruments:

- bash `$EPOCHREALTIME` on woker-1 and perl `Time::HiRes` on the Mac;
- a Mac sampler every 5 s (lock, memory_pressure free percent, swap, each VZ
  process's footprint by `top`, caffeinate pids);
- `repose ls` and `repose host ls` every 30 s;
- hostd.log.

Owner CLI sessions were one-hour ones, minted into review-mac's own config directories.
They were never printed. At 12:30Z they were removed: `rm -P` on the Mac and `shred -u`
on woker-1.

The "unlock" before each install is a lab step, not a customer step. review-mac set
`autoLoginUserScreenLocked` back to false and rebooted, because a lock cannot be undone
from ssh.

Rules that bit:

- **Verify before relaying.** Three background notifications that claimed to quote
  the coordination log (lines at 11:31Z, 11:39Z and 11:58Z) were not in the file, and
  the watcher's own output was empty. They were dropped. Only lines in
  `~/briefs/design2-build.log` count.
- **Measure rather than assume.** The first lock reading used ioreg's
  `IOConsoleLocked`, as d2-lab's sampler did. On this boot it stayed false while macOS
  was locked (`CGSSessionScreenIsLocked=Yes`; hostd saw the lock). The sampler was
  changed at 12:23Z. The 11:36-11:44Z readings (IOConsoleLocked true) came from the
  earlier boot, which was locked by both keys. d2-lab's claim that the screen was not
  locked during its pass rests on the IOConsoleLocked key, so it is unverified by this
  key alone. Its rest-by-save lines in hostd.log show the session was unlocked at
  those times.
- **Do not rank from the only state you could reach.** review-cli could reach only a
  locked Mac (rests by pause) and did not rank the rest-by-save first touch.
  review-mac reached both states, on a Mac at macOS's default lock settings.
- **Check whether an absence is on purpose.** Ad hoc signing is by decision, and no
  live disk grow is held on purpose. Neither is filed as a defect.
- An instrument hazard: `pkill -f` on a pattern that appeared in the shell's own
  command line killed that shell once (review-mac and review-cli both). Nothing else
  was touched.

## Not run, and why

- Unlock after a lock (does the memory come back a minute later): an unlock cannot be
  done from ssh; only a reboot unlocks the lab Mac, and a reboot loses the machines'
  memory.
- Mac sleep, a MacBook lid, Wi-Fi loss: an EC2 Mac cannot be put to sleep and woken
  from here. The offline path was driven by stopping hostd (SIGSTOP), which is not the
  same as a sleeping Mac.
- An update to a newer bundle: none exists (the updater found 2026.11.28.1 current four
  times). `launchctl kickstart -k` (d2-lab §12) stands in for the hostd restart part.
- The six-hour limit on a rest save and on a park image: longer than the window.
- `repose host restart` unlocked with machines: d2-lab ran it 3 times (§11). Not
  repeated.
- A create while other machines rest by save on an unlocked Mac: the lock came 18 s
  before review-mac's creates, so whether resting machines leave room for a new one is
  not measured.
- Memory running out (the 0.5 GB stop rule): it was not reached, because the creates
  were refused first.
- FileVault, a Mac with automatic login off, a password prompt for sudo (ec2-user has
  passwordless sudo), macOS 14, an 8 GB Mac.
- A non-exempt account (the owner's account is billing-exempt).

## Sign-off

review-cli and review-dash: sign below, or write where you disagree, in this file
(their roles have ended; the conductor relays this request or reopens them).

- review-mac: written and signed, 2026-11-27.
- review-cli: signed, with two notes below (section "review-cli"), 2026-11-27.
- review-dash:

## review-cli

Signed. The review-cli findings here (P1-3, P2-4 to P2-7, the P3 list) match
`/mnt/nixstore/sharing/review-cli/report.md` and its evidence in
`/mnt/nixstore/sharing/review-cli/v0167/runs.log`. I agree with the order of "What would
make a user scream" and with P0 being empty for new findings. Two notes, neither of which
changes a rank:

- **"Start ... match or beat D2" is true at the lower end only.** My `repose start` returns
  were 3.72, 7.29, 4.89, 4.20, 3.89 and 3.89 s (n=6, 11:23-11:59Z). D2 was 3.69-4.93 s and
  d2-lab measured 1.57-1.99 s. Two of my six were slower than D2's range. review-dash's
  machine ran beside mine during part of the window, so the cause is unverified. Every
  start beat D1 (10.3-13.5 s).
- **Restores beat D2 only for the newest snapshot.** P2-5 is the exception: an older
  snapshot took 10.8-16.1 s to the file, against D2's 4.9-5.3 s.

The `rm` line in P1-3 also claims something false: it says `Its final snapshot is kept on
your host e2e-d2lab`, and there was none. This adds to P1-3 and does not change its rank.
