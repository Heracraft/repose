# Design 2 shipped on the Mac: the 14 UX measures, 2026-11-27

d2-lab (measurer) for the design-2 review (`~/briefs/design2-review-2026-11-27.md`).
This pass measures what shipped, installed as a customer installs it, against the
D1 and D2 columns of `docs/reports/2026-11-26-design2-ux.md` (the bar). Every number
here was measured on one Mac at the UTC time given, with the instrument named. A
number with no run behind it is not in this report.

Raw data `/mnt/nixstore/sharing/d2-lab5/data/`, scripts
`/mnt/nixstore/sharing/d2-lab5/bin/` (the D2 scripts, with the paths, host and
machine names changed and the additions named below).

## Headline: shipped against D1 and D2

Of the 14 measures, against D1 on the same Mac: ten better (1, 2, 3, 7, 8, 10, 11,
12, 13, and 5 for a new connection), two the same (6, 9), two worse (4 at the first
touch, 14). Against the D2 column (the bar), every row is met or beaten except the
first touch after a rest (4) and the restart's time to back (11).

- **Install (1), measured for the first time.** `repose host add` to the first
  machine ready 74.5-119.0 s (n=4, median 88), against D1's 113-155 s; `repose host
  rm` 0.62 s (n=3) and leaves nothing.
- **Better.** Warm create 8.5-13.6 s (n=5, median 10.3; D1 19.9-23.1, D2 median
  16.2). Start 1.57-1.99 s to return and 2.33-2.76 s to ssh (n=9; D1 11.7-14.9, D2
  4.5-5.7). Builds: H three at once 52.4-63.2 s (D1 368-558), P three at once
  41.6-70.5 s (D1 171-175, d62bdf73's 775-811 gone), P two at once 31.0-38.7 s with no
  slowdown across rounds; swap 0. Snapshots: restore in place to the file 2.75 s (D1
  67-83, D2 4.9-5.3), restore --as 2.6-3.1 s, +15 MB each. `repose host restart` with
  three machines kept memory **9 of 9** in three runs back to back, none refused (rc5:
  1 of 3). A killed VM back on ssh in 11.9 s by itself; a hostd crash or restart keeps
  every machine.
- **Memory at rest is back.** Rest by save, the shipped default: idle machines rest
  11 of 11 after ~15 min and give macOS every byte (0 GB held, free 91 percent).
- **Worse: the first touch of a rested machine.** After real work (images 1.1-2.2 GB)
  hostd's restore takes 9.1-31.2 s. A plain `ssh SLUG.repose` **fails** 4 of 4, rc
  255 after ~8.1 s: `environment is frozen and did not wake; try again shortly`. With
  the CLI's early wake, `repose exec` works but takes 19.4-37.6 s; `repose attach`
  19.5-21.7 s; `repose browser` 21.0 s. D1 answered in 1.1-1.3 s (never rested); D2's
  rest by save 5.5 s, rest by pause 0.9 s.
- **Worse, unchanged: disk (14).** A live grow is refused; stop, resize, start takes
  ~3.7 s and the machine's memory (D1 grew live in 1 s).
- **Restart is slower than the docs say.** Back in 78-84 s (docs: 54 to 70 s);
  parked in 11.7-11.9 s (docs: 8 to 11 s).

## What was measured

- Mac: the same AWS mac-m4.metal as the D1 and D2 passes (host h-02930697783cd33c2,
  eu-west-1b), relaunched on a fresh macOS 15.8.3 (24H241) since; M4, 10 cores,
  24 GiB; public 108.131.244.7. GUI auto-login as ec2-user and the screen lock as
  d2-core left them (macOS defaults, set 04:27Z); the screen was not locked during
  this pass (every rest below is a rest by save, which macOS does only unlocked).
  Data volume 121 GiB free at the start.
- **Shipped** = main 8e695642 (batch r20261127-3, design 2), the api and dashboard
  live on repose.run, CLI **v0.1.67** as released, the Mac bundle
  **hostd-2026.11.28.1** as `repose host add` fetched it
  (`/Library/Application Support/repose/install.json`: version 2026.11.28.1), base
  2026.11.24.1. hostd as installed: `--owned-host --park --pause-idle`, no
  `--mac-rest-by-save` flag, so hostd's default, rest by save (I-1682, ENG-273). A
  machine gets half the Mac's cores and half its memory up to 8 GiB: **5 vCPU and 8
  GiB** (`nproc` 5, `free -m` total 7924 MB), and a machine started while two others
  run gets what is left: **2 vCPU and 4 GiB** (3907 MB), which `repose start`, `run`
  and `ls` say in one line.
- Install as a customer: on the Mac `curl -fsSL https://repose.run/install.sh | sh`
  (v0.1.67 darwin/arm64 into `~/.local/bin`), then `repose host add e2e-d2lab`. On
  woker-1 (the "laptop") the same install.sh with `--version v0.1.67` into
  `/mnt/nixstore/sharing/d2-lab5/t/home/.local/bin`. Never a branch build.
- Owner CLI session: one-hour private sessions minted with the
  `~/briefs/mint-owner-cli-session.sh` python into `XDG_CONFIG_HOME`
  `/mnt/nixstore/sharing/d2-lab5/cfg` on woker-1 and `~/d2-lab5/cfg` on the Mac,
  re-minted every 40 min, shredded at the end, never printed.
- Machines, all mine, all on host `e2e-d2lab`: `e2e-li4` (recruiting, the fourth
  install's cold create), `e2e-luw` (unwrap), `e2e-lpm` (pocketmail); creates and
  restores `e2e-li1..3`, `e2e-lw1..3`, `e2e-las1..3`, each destroyed after its
  measure. `e2e-luw` was the third machine started, so it ran sized down (2 vCPU / 4
  GiB) for the builds, the freezes and the restarts; `e2e-li4` and `e2e-lpm` 5 vCPU
  / 8 GiB. Repos: the same clones of `recruiting`, `unwrap`, `pocketmail` as D1 and
  D2 (push disabled).
- Workloads (unchanged): **H** = recruiting turbo build + unwrap `next build` +
  pocketmail tsc and vite, clean each time, warm package caches; **P** = `go build
  -a -p $(nproc) std cmd` with an empty GOCACHE. One warm-up H per machine, not
  counted.
- Instruments (unchanged): bash `$EPOCHREALTIME` on woker-1, perl `Time::HiRes` on
  the Mac, the pty timers `attachtime.py` and `firstscreen.py`, `repose ls` every 30 s
  (`states.log`), the Mac sampler every 5 s (`mac.csv`), hostd's own log
  (`/Library/Logs/repose/hostd.log`: `save_ms`, `restore_ms`, `wake_ms`,
  `power_off_ms`). New: `install5.sh`/`hostrm5.sh` (install through the released
  CLI), `freeze2.sh` (a first touch per machine by plain ssh, `repose exec`, attach
  or browser, with the ssh error kept and the time to the first touch that works),
  `vmkill.sh` (kills exactly one `repose-vm`, found by its guest folder's
  `project_slug`; refuses unless exactly one process matches).
- Sharing: nothing of another session ran on the Mac in this window (holder file
  `~/briefs/d2/mac-holder`). One stray process was found and left alone: a
  `security add-generic-password -s repose ... -w r -U` from 03:35Z (pid 6445, parent
  launchd), a test's leftover; the CLI with a session file and no refresh token does
  not call the Keychain.

## Findings first

1. **The first ssh to a rested machine fails on the shipped default.** Rest by save
   (ENG-273, I-1682) makes the wake a restore of the saved memory. For machines that
   had run builds (VZ footprints 4.1-7.5 GB each; images 1.07-2.23 GB), hostd's
   restore took 9.1-31.2 s (n=11 wakes), the gateway gave up at ~8.1 s, and plain
   `ssh SLUG.repose true` exited 255 with `environment is frozen and did not wake;
   try again shortly` **4 of 4** times. An agent or an editor (VS Code Remote, an ssh
   alias) that connects with ssh and not with the CLI sees a failure. own-hardware.md
   says "a machine with 8 GB answered its first command 3.3 to 3.7 seconds after it
   was asked", which d2-core measured on fresh machines; it did not hold for machines
   that had worked. `environment` is also a word for a machine the names rule does
   not have.
2. **The early wake works, the wait is long.** `repose exec` sent POST
   /projects/:id/wake (hostd `reason: cli` at the command's start, 3 of 3) and
   succeeded 3 of 3, in 19.4-37.6 s; attach 19.5-21.7 s and browser 21.0 s, which go
   through the gateway's wake, also succeeded. The command returns 8.6-10.0 s after hostd's
   wake is done (unverified where that goes).
3. **Everything else that changed beats the bar.** Install, create, start, builds at
   once, snapshots and host life are better than D2 as well as D1, several by 2x
   (start 1.7 s vs 4.0; restore to the file 2.75 s vs 5.1; warm create median 10.3 vs
   16.2). The d62bdf73 Go-build collapse did not recur (P three at once 41.6-70.5 s,
   guest system time 14-24 s).
4. **Host restart keeps memory 9 of 9**, three runs back to back with three machines
   (ENG-263 holds), but takes 78-84 s to all three answering, against 54-70 s in
   d2-core's runs and in own-hardware.md; reading three parked images at once took
   28.1-29.3 s, the machines holding their whole configured memory after a wake.
5. **Small things a user reads.** `repose snapshots restore --as` ends `it is running
   (large).` on own hardware, where the docs say no line names a size. `repose stop`
   printed the refusal of an earlier live resize as a `Note:`. `repose host rm` right
   after `repose rm` of the last machine is refused until the destroy finishes (~8 s).
   A machine with a `repose browser` view open did not rest in 19 min.

## 1. Install: `repose host add` to first machine ready

`install.csv`. On the Mac, timed by `Time::HiRes`: `repose host add e2e-d2lab` returns
when the Mac has joined (it prints `e2e-d2lab joined your account in 64s.`); "online"
is `repose host ls` from woker-1; "first ready" is the cold create (`repose run -d`
of recruiting, base fetched) to the first `ssh SLUG.repose true`, counted from the
`host add`.

| run (UTC) | host add (joined) | add -> online | cold create | add -> first ready |
|---|---|---|---|---|
| 08:20:47Z | 63.96 s | 64.09 s | 54.93 s | 119.04 s |
| 08:23:16Z | 32.44 s | 32.56 s | 41.97 s | 74.54 s |
| 08:24:43Z | 37.50 s | 37.63 s | 39.94 s | 77.58 s |
| 08:26:24Z | 58.82 s | 58.95 s | 39.58 s | 98.54 s |

| measure | D1 (n=3) | D2 (d2-install / d2-core, branch CLI, lab bundle) | shipped (n=4) | verdict vs D1 |
|---|---|---|---|---|
| `repose host add` to first machine ready | 113-155 s | 149.8/125.1/83.9 s, then 94.7/78.1/93.6 s with the join-speed fix | **74.5, 77.6, 98.5, 119.0 s** (median 88.0) | **better** (median 88 vs 151 s) |
| `host add` to joined | 46.7-86.2 s (to online) | 43.4-84.1 s, then 38.6-53.7 s | 32.4, 37.5, 58.8, 64.0 s | **better** at the median (48 vs 53 s); the spread is the edge's 30 s WireGuard sync (ENG-274, not switched) |
| cold create (base fetched) | 64.4-101.6 s | not comparable | 39.4-54.8 s (n=4) | **better** |
| `repose host rm` | 5.5-25.6 s | 0.6 s (d2-install) | **0.62, 0.62, 0.62 s** (n=3); nothing left (`/Library/Application Support/repose`, `/Library/Logs/repose`, both LaunchDaemons gone, no hostd or repose-vm), +4.0 GB free each time | **better** |

`repose host rm` right after `repose rm` of the host's last machine was refused once
(08:22:52Z, 0.38 s, rc 1): `e2e-d2lab still has e2e-li1 on it. repose rm each of them
first.`; `repose rm -y` had returned in 0.09 s and the machine was gone from `repose
ls` ~8 s later. A script that runs the two in a row needs to wait. The host add
output is four lines (`installing repose on this Mac`, `fetching repose 2026.11.28.1
for this Mac`, `waiting for e2e-d2lab to join your account`, `joined ... in 64s`); no
password prompt (ec2-user has passwordless sudo, so the docs' "asks for your password
once" was not exercised). The Mac's `~/.zshrc` is root-owned on this AMI, so
install.sh could not add `~/.local/bin` to PATH and said so with the fix.

## 2. Create: `repose run` from a real checkout to ready

`create.csv`. "ready" = the first `ssh SLUG.repose true` that answers after `run -d`.

| case | D1 | D2 (d62bdf73) | shipped | verdict vs D1 |
|---|---|---|---|---|
| cold, first machine on a new host | 64.4-101.6 s (n=3) | not comparable | 39.6, 40.0, 42.0, 54.9 s (n=4, section 1) | **better** |
| warm, a further machine | 19.9, 20.9, 23.1 s (n=3) | 12.2-33.9 s (n=6, median 16.2) | **13.6, 8.5, 10.3, 12.1, 8.7 s** (n=5, median 10.3; 08:28-08:30Z) | **better** (-51 percent at the median); no tail |
| a fourth machine while three run | - | - | refused in 0.64 s, exit 8, nothing created: `Could not start e2e-lw0: the machines running on this host hold the memory it gives its machines; stopping a running machine frees room.` | - |
| `repose rm` until gone from `ls` | 18-19 s | 6.3-8.7 s | 2.3, 4.7, 10.0 s (n=3; the command itself returns in 0.1 s) | **better** |

e2e-lpm and e2e-lw1..3 were created third, beside two running machines, and got 2
vCPU / 4 GiB; the create's last line said so.

## 3. Start and stop

`stopstart.csv`; hostd's `guest stopped` lines give `power_off_ms`.

| operation | D1 | D2 (d62bdf73 / rc5) | shipped | verdict vs D1 |
|---|---|---|---|---|
| stop with snapshot, D1's snapshot size | 5.4-6.7 s (n=3; 307-425 MB) | 3.7-4.4 s | **3.72, 4.20, 5.69 s** (n=3; 325, 428, 397 MB; 08:30-08:31Z) | **better** (4.2 vs 6.1 s median) |
| stop without snapshot | 4.0-4.1 s (n=3) | 1.65-2.47 s | **1.71-2.46 s** in 5 of 6, **5.96 s** once (e2e-lpm 8 min after its create, first-boot tool installs running; `power_off_ms` 3420) (n=6, median 2.2) | **better** |
| start, command returns | 10.3-13.5 s (n=6) | 3.69-4.93 s; rc5 3.79-5.40 | **1.57-1.99 s** (n=9, median 1.70) | **better**, 6.6x |
| start, to first ssh | 11.7-14.9 s (n=6) | 4.46-5.68 s; rc5 4.61-6.16 | **2.33-2.76 s** (n=9, median 2.51) | **better**, 5x |
| stop of two at once | 5.2-5.6 s each (3 at once) | 1.8-2.9 s each | 2.1 and 1.8 s (`repose stop e2e-li4 e2e-luw`, n=1) | **better** |
| the 61.5 s power-off tail | - | 0 of 55 | 0 of 25 (`escalated` none every time) | - |

`repose stop` of e2e-lpm at 08:40:33Z printed a stray line: `Note: a machine on this
host gets a bigger disk while it is stopped: stop it, resize, and start it again.`,
the refusal of the live resize a few seconds earlier (section 14), on a stop that had
nothing to do with it.

## 4. Rest and wake (rest by save, the shipped default)

`freeze.csv`, `freeze.log`, `states.log`, hostd.log. `freeze2.sh` closes the CLI's ssh
masters, waits for `frozen` in `repose ls`, waits 60 s more, then touches each machine
once, one after another. The machines had just run the builds of section 7 (VZ footprints 4.1-7.5 GB
each).

| measure | D1 | D2 (d62bdf73 by save / rc5 by pause) | shipped (by save) | verdict vs D1 |
|---|---|---|---|---|
| idle machine shows `frozen` | **never**, 6 machine-waits of 17-27 min | 7 of 7, 15.5-17 min | **11 of 11** machine-waits, 884-944 s after the last touch (09:13-10:06Z); one machine with a `repose browser` view open did not rest in 19 min (the view sleeps after 30 idle minutes) | **better** |
| how it rests | - | by save 3.3-9.1 s, images 263-276 MB (fresh machines); rc5 by pause | by save, 4.9-11.6 s (`save_ms`, n=11), images **1.07-2.23 GB** (machines after builds); each VM process ends | - |
| memory while resting | never returned | d62bdf73: 0; rc5: kept (11.9 GB for three) | **0**: no VZ process, free 91 percent (09:13:50-09:14:40Z) | **better** |
| first plain `ssh SLUG.repose true` | 1.08-1.32 s (n=5, not frozen) | 5.50-5.56 s (n=3); rc5 0.91-0.94 s | **fails 4 of 4**: rc 255 after 8.10, 8.11, 8.22, 8.14 s, ssh prints `environment is frozen and did not wake; try again shortly`; hostd restored the machine 9.1, 9.2, 31.2 and 10.0 s after the touch (`restore_ms`, WakeGuest reason connect); the next ssh worked 14.9 s after the first touch (09:49:52Z) | **worse**: the first connection fails |
| first `repose exec SLUG true` (the CLI's early wake, POST /projects/:id/wake) | - | d2-core, fresh machines, before the wake route: 3.68-3.99 s | **19.35, 19.43, 37.63 s**, rc 0 (n=3, 09:31-09:33Z); hostd got WakeGuest reason `cli` the same second as the command, and restored in 10.3, 9.2, 28.8 s | **worse** (+18-36 s), but it does not fail |
| first `repose attach` to the drawn screen | attach on a running machine 0.58-0.85 s | 6.0-9.3 s; rc5 not run | **19.50, 19.64, 21.74 s** (n=3; restores 13.5, 15.5, 20.1 s) | **worse** |
| `repose browser` of a rested machine, to HTTP 200 | 1.67-3.22 s running | 8.10 s (n=1) | **21.02 s** (n=1, command 20.86 s; restore 14.5 s) | **worse** |
| fresh machines on a locked session (after `sudo shutdown -r`) | - | - | rest **by pause** (no save line; hostd `wake_ms` 21, 24; VZ footprints 0.81 and 0.83 GB held while resting): first plain ssh **0.94 s**, first `repose exec` **1.20 s** (n=1 each, 10:51Z) | - |

What the time is: the restore of the machine's saved memory. hostd's `restore_ms`
ran 9.1-31.2 s for 1.1-2.2 GB images, against d2-core's 1.84-1.87 s for fresh
machines (04:27Z, 05:12Z READY lines) and d62bdf73's 4.2-4.6 s for 263-276 MB
images. It does not follow the image size alone: the two 2.2 GB images restored in
9.1-20.1 s on e2e-li4 and 10.0-31.2 s on e2e-lpm, and in each of the four waits the
machine touched last restored slowest (unverified why). The plain ssh fails
because the gateway gives up at ~8 s, before the restore is done (the message is
the gateway's; its timeout was not read, unverified). `repose exec` does not fail,
but returns 8.6-10.0 s after hostd's wake is done (`wake_ms` 9.4-29.0 s vs the
command's 19.4-37.6 s); where that time goes, the gateway's first attempt or the
CLI's retry, is unverified.

## 5. Attach and ssh

`shells.csv`, `warmssh.csv`, 08:32Z, the three machines just started.

| measure | D1 | D2 (d62bdf73) | shipped | verdict vs D1 |
|---|---|---|---|---|
| `ssh SLUG.repose true`, new connection | 0.95, 1.00 s (n=2) | 0.757-0.832 s (n=6) | **0.767, 0.779, 0.796 s** (n=3) | **better** |
| `ssh SLUG.repose true`, warm (master open) | 0.155-0.158 s (n=7) | 0.154-0.418 s (median 0.164) | **0.156-0.211 s** (n=15, median 0.157) | **same** |
| attach to drawn tmux screen | 0.58-0.85 s (n=9) | 0.586-0.809 s (n=18) | **0.602-0.687 s** (n=9) | **same** |
| attach across a minute of Mac sleep | not run | not run | not run (an EC2 Mac cannot be put to sleep and woken from here) | not compared |

## 6. Agent start

`agentscreen.csv`, `agentp.csv`, on e2e-lpm (2 vCPU / 4 GiB at the time), 08:32-08:33Z.

| measure | D1 | D2n | shipped | verdict vs D1 |
|---|---|---|---|---|
| Claude Code 2.1.330 first screen (to the trust prompt) | 0.43-0.51 s (n=3) | 0.40-0.41 s (n=4) | **0.399, 0.402, 0.455 s** (n=3) | **same** |
| opencode v2.3.2 first screen (to `Build`) | 1.52-2.29 s (n=3) | 1.43-1.49 s (n=3) | **1.44, 1.48, 2.06 s** (n=3) | **same** |
| `repose run -d --agent opencode -p` returns | 1.03-1.04 s (n=3) | 0.97-1.28 s (n=3) | **0.93, 0.98, 1.21 s** (n=3) | **same** |
| ... to the file the agent wrote existing | 41.7, 10.4, 9.3 s | 8.4, 10.4, 26.8 s | **5.7, 5.8, 7.2 s** | same (model latency dominates) |
| `repose run -p` with Claude Code to acting | not run: no Claude login | not run | not run: Claude Code is not logged in on these machines | not compared |

## 7. Builds

`runs.jsonl`, `builds5.log` (wall seconds from `time` in the machine; rc 0 every
run). 08:41-08:58Z, only the three machines running, Mac swap 0 and pressure level 1
throughout. Alone and two at once ran on the two 5 vCPU / 8 GiB machines; three at
once added e2e-luw, which the guard had sized down to 2 vCPU / 4 GiB.

| build | D1 | D2 (d62bdf73; rc5 where named) | shipped | verdict vs D1 |
|---|---|---|---|---|
| H alone | 62.3, 64.2, 73.7 s | 40.6-47.9 s | **41.9, 43.4, 43.6 s** (n=3) | **better** (-32 percent) |
| H, 2 at once | 79.7-116.5 s (n=6, median 93.4) | 44.1-54.4 s | **45.4-49.7 s** (n=6, median 47.9) | **better** (-49 percent) |
| H, 3 at once | 367.8-558.3 s (8 of 9; 1 stopped by the guard) | 52.9-59.2 s; rc5 49.7-63.2 s | **52.4-56.1 s** on the 5-vCPU machines, **59.0-63.2 s** on the 2-vCPU one (n=9, 9 of 9) | **better**, 7-10x, none stopped |
| P alone | 53.7-54.7 s (n=3) | 25.5-30.0 s | **23.4, 23.7, 29.0 s** (n=3) | **better**, 2.2x |
| P, 2 at once | 68.3-76.9 s (n=6) | d62bdf73 53.9-205.4 s; rc5 32.5-38.9 s | **31.0-38.7 s** (n=6, three rounds: 31.0/38.7, 35.1/35.2, 34.6/34.9; rounds do not slow down) | **better**, 2x |
| P, 3 at once | 171.0, 175.0 s (2 of 3; 1 stopped) | d62bdf73 774.9-811.2 s; rc5 38.9-39.8 s and 66.5-69.1 s | **41.6-43.8 s** on the 5-vCPU machines, **67.5-70.5 s** on the 2-vCPU one (n=9, three rounds) | **better**, 2.4-4x, none stopped |

Guest `sys_s` in P three at once 14-24 s against `user_s` 100-150 s: the d62bdf73
collapse (system time 4118-5041 s) did not recur.

## 8. Memory

`mac.csv`: footprint of each VZ process every 5 s (`top`), `memory_pressure` free
percentage, swap.

| state | D1: macOS gives up | D2 (d62bdf73 / rc5) | shipped | verdict vs D1 |
|---|---|---|---|---|
| 1 machine, fresh | 7.9 GB (the appliance) | 0.91-0.95 GB | 0.80 GB for a started machine (08:40Z, e2e-las1/2); 3.4 GB for the cold-created e2e-li4 while its first-boot tool installs ran (08:28Z) | **better** |
| 3 machines idle, fresh | 15 GB; 4.1 GB after a fresh boot | 2.80 GB; rc5 2.32 GB | 2.28-2.81 GB (08:41:00-05Z, just started) | **better** |
| 3 machines, busy and after builds | 18 GB (the whole 18432 MiB) | 22.2 GB; rc5 17.4-18.3 GB | peak **18.5 GB** (7.46 + 7.40 + 4.11 GB; 08:58Z), swap 0, pressure 1, free 58-94 percent | **same** in sum, nothing stopped, no swap |
| ... comes back while running and idle | never | never | never: 18.52 GB from 08:58:31 to 09:13:30Z | **same** |
| ... comes back at the rest | never (no rest) | d62bdf73 yes; rc5 no (pause) | **yes**: 0 VZ processes, 0 GB, free 91 percent from 09:13:50Z (each machine 15 min idle, saved in 6.7-11.6 s) | **better** |
| ... after a wake | - | the whole configured size | 20.5-20.6 GB, the whole 8 + 8 + 4 GiB (09:16-09:17Z), as the docs say | - |
| after stopping every machine | 7.63 GB | 0 | 0: no VZ process 3 s after `repose stop e2e-li4 e2e-luw e2e-lpm` returned (10:34:24Z); free 95 percent, PhysMem 8.5 GB used, 15 GB unused, swap 0 | **better** |
| `repose host rm` | 0 at once | not run | +4.0 GB disk back, no process left (section 1) | **same** |

## 9. Networking

`net.csv`, `net-ports.txt`, inside e2e-luw (5 vCPU at the time), 08:34-08:37Z.

| measure | D1 | D2n | shipped | verdict vs D1 |
|---|---|---|---|---|
| download, proof.ovh.net 100 MB | 779-790 Mbit/s | 804-814 Mbit/s | **829, 844, 845 Mbit/s** (n=3) | **better** |
| download, ash-speed.hetzner.com 100 MB | 219-224 Mbit/s | 244-257 Mbit/s | **229, 231, 259 Mbit/s** (n=3) | **same** |
| upload, speed.cloudflare.com 100 MB | 213-219 Mbit/s | 205-209 Mbit/s | **160, 193, 213 Mbit/s** (n=3) | **same** to slightly worse (the 200 Mbit/s policer; one low run) |
| tcp 25, 3333, 5555, 7777, 14433, 14444 | blocked 3/3 | blocked 3/3 | blocked 3/3 each | **same** |
| tcp 587, 443 | open 3/3 | open 3/3 | open 3/3 | **same** |
| DNS uncached / NXDOMAIN | 5-14 / 28-34 ms | 7-15 / 12-16 ms | 7-17 / 12-15 ms (n=3) | **same** / better |
| IPv6 | none | none | none (0 global addresses, `curl -6` rc 7) | **same** |
| guest -> Mac (VPC address, gateway :22) | unreachable | unreachable | unreachable (both time out) | **same** |
| ICMP beyond the gateway | not measured | 100 percent loss | 0 percent loss, 1.7-3.7 ms (10 pings to 1.1.1.1) | - |
| `repose open 18001:3000` to first HTTP 200; 20 requests | 1.49-1.57 s; 4.49-4.50 s | 1.30-1.69 s; 3.06-3.08 s | **1.18, 1.29, 1.42 s; 2.94-2.96 s** (n=3) | **better** |
| speed.cloudflare.com `__down` 200 MB | - | - | HTTP 403 3 of 3 (the site refuses the request; not a repose result) | - |

## 10. Snapshots

`snap.csv`, `snaps.sh` on e2e-lpm (pocketmail checkout plus the 500 MB random file,
2.0 GB in use; 5 vCPU / 8 GiB at the time), 08:38-08:40Z.

| measure | D1 | D2 (d62bdf73 / rc5) | shipped | verdict vs D1 |
|---|---|---|---|---|
| `snapshots create` (running) | 16.3-25.4 s, 1.5 GB each (n=3) | 5.3-5.6 s at 785 MB; rc5 10.7-13.1 s at 1.1 GB | **5.7, 6.3, 6.3 s**, 858-873 MB each (n=3) | **better** |
| `snapshots restore -y` in place, to the file over ssh | 67.4-82.9 s (n=3) | 4.9-5.3 s; rc5 5.31-6.02 s | **2.75, 2.75, 2.76 s** (command 1.99 s; the later file gone 3/3) | **better**, 27x |
| `snapshots restore --as NEW`, to the file over ssh | 66.8-69.1 s (n=3) | 5.1-5.6 s | **2.64, 2.71, 3.06 s** (command 1.86-2.22 s) | **better**, 24x |
| disk cost of each --as machine | +2.3 GB for three | +10-17 MB | **+14.6, +14.9, +15.0 MB** (Mac data volume, 20 s after) | **better** |

`snapshots restore --as` ends `it is running (large).`: a size class named for a
machine on own hardware, where own-hardware.md says "no line about the machine names
a size".

## 11. Park across a Mac restart

`rwatch-hr*.log` (each machine's ssh every ~3 s), `hr-*.before/after` (boot_ids),
hostd.log. `repose host restart -y` run on the Mac with the released CLI. The three
machines were running, two of them just woken from a rest (each holding its whole
configured memory, section 8).

| path | D1 | D2 (d2-core 03:33Z / rc5) | shipped | verdict vs D1 |
|---|---|---|---|---|
| `repose host restart -y`, three running machines, three times back to back | no such command | d2-core: 9 of 9 kept, parked 7.9-11.0 s, back 54-70 s; rc5: 1 of 3 (60.5 s), 2 refused `no_room` | **memory kept 9 of 9** (boot_ids same 3 of 3 each time); `Parked 3 machines in 11.9s / 11.7s / 11.7s; restarting this Mac.`; hostd `parked guests restored` 76, 73, 71 s after the command (image read 28.1-29.3 s); all three answering ssh **84, 78, 79 s** after the command (10:11:11Z, 10:15:30Z, 10:19:42Z) | **better** (memory kept on every run; nothing refused); **slower** than d2-core's 54-70 s |
| `sudo shutdown -r now`, logged in | memory kept 0 of 6; 51-72 s (n=2) | 0 of 3; 49-78 s | memory kept **0 of 3** (new boot_ids); all three on ssh **58 s** after the command (n=1, 10:24:57Z) | **same** |
| Apple menu (System Events `restart`) | memory kept 0 of 6; 57-61 s | 0 of 3; 70.2 s | not run (d2-surface 04:35Z: `osascript` to System Events from ssh blocks on an Automation consent prompt on this Mac) | not compared |

The first start after the Mac restart was slower: `repose start` reported ready in
4.1 and 5.1 s at 10:34:5xZ, against 1.6-2.0 s before (section 3).

## 12. Host life

`hostlife.csv`. "ssh" = a new connection (no master) until it answers.

| event | D1 | D2 (d62bdf73) | shipped | verdict vs D1 |
|---|---|---|---|---|
| `kill -9` of the machine's `repose-vm` | vfkit relaunched; 19-25 s; memory lost (n=1) | hostd boots it again; ssh 11.90-11.94 s | hostd boots it again; ssh **11.87, 11.88, 11.92 s** (n=3, e2e-lpm, 10:29-10:30Z), new boot_id, memory lost, `repose ls` running | **better** |
| `kill -9` of hostd | - | same boot_id 3/3; ssh 0.99-1.85 s | same boot_id **3/3**; ssh 0.83, 1.85, 1.82 s; launchd started hostd again (new pid each time) | **better** |
| hostd restart (`launchctl kickstart -k system/run.repose.hostd`) | not run | binary replaced + kickstart: same boot_id 3/3; ssh 1.81-1.86 s | same boot_id **3/3**; ssh 1.82, 1.84, 1.82 s | **better** |
| the updater | - | - | `run.repose.hostd-update` every 30 min: `event=current version=2026.11.28.1 newest=2026.11.28.1` (08:56, 09:26, 09:56Z, and once by hand 10:33Z); no newer release to update to, so an update itself was not run | not compared |
| power loss, disk nearly full | not run | not run | not run (no AWS calls beyond reads; it would fill the volume other sessions use) | not compared |

## 13. Browser view, sync, logins

`m13.csv`, on e2e-lpm, 08:33-08:34Z.

| measure | D1 | D2n | shipped | verdict vs D1 |
|---|---|---|---|---|
| `repose browser --no-browser` to HTTP 200 | 3.22 s (first), 1.71, 1.67 s | 2.37, 1.40, 1.34 s | **1.99 s (first), 1.38, 1.36 s** (n=3) | **better** |
| dirty checkout `repose sync` | 1.23-1.37 s | 1.26-1.69 s | **1.20, 1.33, 1.37 s** (n=3) | **same** |
| logins copied at create | gh, git | gh, git | gh, git (every create) | **same** |
| Claude login share (I-278) | not run | not run | not run: no Claude login | not compared |

## 14. Disk

`m14.csv`, on e2e-lpm, 08:40Z.

| measure | D1 | D2 (d62bdf73) | shipped | verdict vs D1 |
|---|---|---|---|---|
| `repose resize` 80/100/120G, live | 0.99-1.21 s each, visible at once | refused 3 of 3 in 0.39-0.40 s | refused 3 of 3 in 0.28-0.39 s, nothing changed (`df` 40G after), rc 1: `Could not resize e2e-lpm: a machine on this host gets a bigger disk while it is stopped: stop it, resize, and start it again (invalid_argument)` | **worse** (a stop and a start), no longer harmful |
| `repose resize` stopped, 50/60/70G | not measured | 0.29-0.36 s | **0.27, 0.27, 0.28 s** (n=3); start 1.59 s; `df` 69G, `lsblk` vdb 70G | - |
| a grow the design-2 way: stop, resize, start | - | ~8 s, memory lost | **~3.7 s** (stop 1.8 s, resize 0.27 s, start 1.59 s), memory lost | **worse** than D1's live 1 s, half D2's |

## Verdict per measure

| # | measure | D2 column (the bar) | shipped vs D1 | shipped vs D2 |
|---|---|---|---|---|
| 1 | install | not compared (no installer); d2-install/d2-core branch runs 78-150 s | **better** (74.5-119.0 vs 113-155 s; host rm 0.62 s) | met |
| 2 | create | warm median 16.2 s, one 33.9 s | **better** (warm median 10.3 s; cold 39-55 vs 64-102 s) | beaten |
| 3 | stop / start | start to ssh 4.5-5.7 s; stop 1.7-2.5 s | **better** (start to ssh 2.3-2.8 s; stop 1.7-2.5 s, one 6.0 s) | beaten (start), met (stop) |
| 4 | rest / wake | rest by save, ssh 5.5 s; by pause 0.9 s | rest **better** (11 of 11, memory 0); first touch **worse** (ssh fails 4 of 4; exec 19-38 s; attach 19.5-21.7 s) | **not met** |
| 5 | attach / ssh | new ssh 0.76-0.83 s, attach 0.59-0.81 s | **better** new ssh (0.77-0.80 vs 0.95-1.00 s); attach and warm ssh **same** | met |
| 6 | agent start | Claude 0.40 s, opencode 1.43-1.49 s | **same** | met |
| 7 | builds | rc5: P2 32.5-38.9, P3 38.9-69.1, H3 49.7-63.2 s | **better** (H3 52-63 s, P3 42-71 s, P2 31-39 s; swap 0) | met |
| 8 | memory | 0.9 GB per idle machine, 0 at rest (d62bdf73) | **better** (0.8 GB per idle machine, 0 at rest and after a stop; 18.5 GB busy, same as D1) | met |
| 9 | networking | D2n figures | **same** (ovh better, upload one low run) | met |
| 10 | snapshots | restore 4.9-6.0 s | **better** (restore to file 2.75 s, --as 2.6-3.1 s, +15 MB) | beaten |
| 11 | park | d2-core 9 of 9 kept, 54-70 s | **better** (memory kept 9 of 9; shutdown -r same) | met for memory, **not met** for time (78-84 s) |
| 12 | host life | VM back 11.9 s; hostd crash/update keep machines | **better** (11.9 s; 6 of 6 kept) | met |
| 13 | browser, sync, logins | browser 1.34-2.37 s, sync 1.26-1.69 s | **better** browser, sync **same** | met |
| 14 | disk | live grow refused, ~8 s the D2 way | **worse** (live grow refused; ~3.7 s stop-resize-start) | beaten |

## Not run, and why

- Apple menu restart (11): `osascript` to System Events from ssh blocks on an
  Automation consent prompt on this Mac (d2-surface, 04:35Z); not retried.
- A hostd update to a newer release (12): there is none; the updater ran 4 times and
  found 2026.11.28.1 current. `launchctl kickstart -k` stands in for the restart part.
- Power loss, disk nearly full, pool full (12, 14): no AWS calls beyond reads; it
  would fill the volume other sessions use.
- Attach across Mac sleep (5): an EC2 Mac cannot be put to sleep and woken from here.
- Claude Code `run -p` and the login share (6, 13): Claude Code is not logged in.
- A rest on a locked screen, the 6-hour limit on a rest save, keep-awake and lid:
  review-mac's part of this review.
- The browser view of a rested machine is n=1 (the second wait was spent on a
  machine the open view kept awake).
- The P and H builds on D1's appliance size: the shipped build's machines are
  compared as a customer gets them (5 vCPU / 8 GiB, the third 2 vCPU / 4 GiB), not
  at equal vCPUs.

## Landing figures (`apps/web/src/lib/data/mac-figures.ts`)

Measured by d2-lab on the M4 above, through the production api from woker-1 with the
released CLI v0.1.67, on the shipped build (main 8e695642, bundle hostd-2026.11.28.1).
Own = design 2 as shipped; nested = repose 0.1.66 (D1, 2026-11-26, unchanged). Seconds
unless said; `n` = runs behind the range. Raw data `/mnt/nixstore/sharing/d2-lab5/data/`.
The page is not changed on this branch.

```ts
// MEASURED: machine 'an M4 Mac mini with 10 cores and 24 GB', date '27 November 2026',
//   iso '2026-11-27' (every own row below; host e2e-d2lab, report
//   docs/reports/2026-11-27-design2-shipped.md)
// START
//   'New sandbox from your repo' (warm create, `repose run -d` to the first
//   `ssh SLUG.repose true`; 08:28-08:30Z, create.csv)
own:    { lo: 8.5, hi: 13.6, n: 5 }    // 13.6, 8.5, 10.3, 12.1, 8.7; median 10.3; no tail
nested: { lo: 19.9, hi: 23.1, n: 3 }
//   'Start a stopped one' (`repose start`, command returns; 08:30-08:31Z, stopstart.csv)
own:    { lo: 1.6, hi: 2.0, n: 9 }     // 1.57-1.99; to first ssh 2.33-2.76
nested: { lo: 10.3, hi: 13.5, n: 6 }
//   The START note ('One new sandbox of six took 34 s') is no longer true: drop it.
// SNAPSHOT
//   'Take a snapshot' (`repose snapshots create`, running machine, 2.0 GB in use,
//   858-873 MB stored each; 08:38Z, snap.csv)
own:    { lo: 5.7, hi: 6.3, n: 3 }
nested: { lo: 16.3, hi: 25.4, n: 3 }   // 1.5 GB each
//   'Restore one' (`repose snapshots restore -y` in place, to the command's return;
//   to the file over ssh 2.75-2.76; 08:38-08:40Z)
own:    { lo: 2.0, hi: 2.0, n: 3 }     // 1.986, 1.992, 1.994
nested: { lo: 66.0, hi: 80.9, n: 3 }
//   SNAPSHOT note: 'Each own-VM snapshot stored 0.9 GB of a sandbox using 2.0 GB;
//   each one inside the Linux VM stored 1.5 GB.'
// REST (idle memory): rest by save is the shipped default (I-1682, ENG-273)
//   'Memory held once stopped': own { lo: 0, hi: 0, n: 1 } (10:34:24Z, three machines
//   stopped, no VZ process 3 s later); nested { lo: 7.63, hi: 7.63, n: 1 }
//   'Memory held while resting' (new row if wanted): own { lo: 0, hi: 0, n: 4 }
//   (four rests of three machines each, 09:13-10:06Z: no VZ process); nested
//   { lo: 7.63, hi: 7.63, n: 1 } (the appliance never rests or gives memory back)
//   wake: do NOT show REST.wake (0.91-0.94 s was rest by pause, rc5). The shipped wake,
//   first `repose exec SLUG true` of a rested machine after work:
wake: { lo: 19.4, hi: 37.6, n: 3 }     // and a plain ssh fails (rc 255 at ~8.1 s) 4 of 4
// RESTART ('Restart to sandboxes back', `repose host restart -y`, three machines,
//   command to all three answering ssh, same boot_ids; 10:11-10:21Z, rwatch-hr1..3)
own:    { lo: 78, hi: 84, n: 3 }       // 84, 78, 79; memory kept 9 of 9, parked 11.7-11.9 s
nested: { lo: 51, hi: 72, n: 2 }       // shutdown -r, memory lost
//   RESTART note: drop 'two of three tries were refused'; it now ran 3 of 3. Keep 'A
//   restart from the Apple menu, a macOS update or a power cut still loses memory.'
//   (shutdown -r on the shipped build: 0 of 3 kept, 58 s).
```

What the page must not say from these: that a resting sandbox answers fast (its first
ssh fails and its first command takes 19-38 s), or that a restart through repose is
faster than the nested layout's (78-84 s against 51-72 s; its gain is the kept
memory). own-hardware.md says the same two things today ("3.3 to 3.7 seconds"; "54 to
70 seconds") and needs the same change.

## State at the end

At 10:54Z, 27 November:

- Host **e2e-d2lab** stays installed and online for the reviewers (`repose host ls`:
  `online 24 GB 0 0 just now`), bundle 2026.11.28.1, hostd pid 6207. **No machine on
  it**: e2e-li4, e2e-luw, e2e-lpm destroyed at 10:53Z (their final snapshots are kept
  on the host, as `repose rm` said); every other machine of mine was destroyed after
  its measure. No `repose-vm` running; data volume 94 GiB free; macOS 94 percent
  free.
- **The Mac's session is locked** since the 10:24:57Z `sudo shutdown -r`, so rests
  there are by pause until someone unlocks it (section 4).
- Owner sessions: `~/d2-lab5/cfg/repose/credentials.json` on the Mac removed (`rm
  -P`), `/mnt/nixstore/sharing/d2-lab5/cfg/repose/credentials.json` on woker-1
  shredded; the re-minter, state watcher and Mac sampler stopped. Data and logs kept
  (ids only, no secrets), including a copy of hostd.log (`data/hostd.log`).
- The stray `security` process from 03:35Z (pid 6445) went with the first Mac restart (10:11Z).
