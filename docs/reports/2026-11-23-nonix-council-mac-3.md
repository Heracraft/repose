yes, I'd pay $10

# No-Nix guests, council third pass: the Mac mini path

Reviewer: spark `mac` (fourth session), 2026-11-23, 05:39 to 06:35 UTC. Hardware: the rented AWS Mac mini (mac-m4.metal: M4, 10 cores, 24 GB, macOS 15.8.3, eu-west-1), reached over SSH. CLI v0.1.61 (`repose version update`), appliance and hostd 2026.11.22.4 (installed by `repose host rm` then `repose host add`, the customer's way), Ubuntu base 2026.11.22.13. Account: the owner's, through a minted one-hour session (billing-exempt). Customer: a developer with an M4 mini deciding whether $10 a month beats SSH and tmux. Earlier passes: docs/reports/2026-11-22-nonix-council-mac.md (M1 to M9) and docs/reports/2026-11-23-nonix-council-mac-2.md (N1, N2, M7). Ranks follow docs/REVIEW.md: P0 blocks launch, P1 is fixed before the plan is sold, P2 after.

## What would make a user scream

Nothing new at P0 or P1. Both P1s of the second pass are fixed on the shipped appliance, measured through the CLI a customer installs.

| # | Rank | Now |
|---|------|-----|
| N1 | was P1 | Fixed. opencode's one-shot prompt writes its file 35 to 40 s after `repose run` (was 379 to 406 s). Claude Code's first screen 0.5 s (was 14 s), Gemini CLI's 1.5 s (was 28 to 41 s). The Mac is now as fast as or faster than repose's servers on these. |
| N2 | was P1 | Fixed. After `repose rm` of the last machine the Mac's Virtualization process reads 0.6 to 6.2% at +60 s and under 1.2% soon after, 7 of 7 (was a steady 100 to 300%, 3 of 3). |
| M7 | P1, known (ENG-188) | Not re-hunted. I would pay through it, as in pass two. |
| P2 | P2 | Six of the eight P2s of pass two still reproduce; one is fixed; one was not re-run (list below). One new P2: `repose ls` shows an agent's state about 50 s after `repose ps` does. |

Known and not hunted, as briefed: ENG-194 (opencode's 503 gate), ENG-191 (api and host disagree at the capacity edge), ENG-188 (M7), the "about 200 MB" line.

## Step A, the live checks

Each was recorded with `ops/dev/release-queue live <branch> --ran ...` at the time.

**mac-appliance-pin-4.** `repose version update` at 05:40 gave `repose 0.1.61 (herakraft)`; the binary carries `appliance-2026.11.22.4` and no other appliance tag. `repose host rm --yes e2e-mac` took 2.5 s. `repose host add e2e-mac` printed `fetching the appliance image 2026.11.22.4 (about 200 MB)` and returned in 6.9 s; `repose host ls` said `online` at +51 s (05:41:54). The appliance console shows hostd `started cpu-reset`, the I-1578 part. A second remove and add at 06:15: CLI 2.9 s, `online` at +53 s (pass two: 92 s).

**mac-jit (N1, ENG-195).** One machine on the host at a time. `repose run NAME --temp -d --size S --agent opencode -p "create hello.txt with the word hi"` from a folder with no checkout; the time is the file's mtime minus the command's start.

| Run | Size | `Ready in` | File written | Pass two |
|---|---|---|---|---|
| e2e-mac-s1 (first machine on the new host, base download inside) | small | 1m41s | +126 s | +379 s |
| e2e-mac-s2 | small | 16 s | +35 s | |
| e2e-mac-l1 | large | 17 s | +36 s | +406 s |
| e2e-mac-l2 | large | 16 s | +40 s | |
| repose's servers, pass two | small | 49 s | +63 s | |

First screen of each agent's TUI, from `tmux new-window` to the first frame holding the agent's own text (`~/lab/tui.sh` through `repose exec`, polled every 0.5 s, no logins), three runs each:

| Agent | Small (1 vCPU) | Large (2 vCPU) | Just after a Mac reboot, large / small | Pass two, Mac small | Servers small |
|---|---|---|---|---|---|
| Claude Code | 1.1, 0.5, 0.5 s | 0.5, 0.5, 0.5 s | 1.0 / 1.1 s | 14.3 s | 1.6 s |
| Codex | 1.0, 0.5, 0.5 s | 1.0, 0.5, 0.5 s | 1.5 / 1.0 s | 1.8 s | 1.1 s |
| Gemini CLI | 2.0, 1.5, 1.5 s | 1.5, 1.5, 1.5 s | 2.0 / 2.0 s | 35.9 s | 3.1 s |

`opencode --version`, six runs: small 0.059 to 0.105 s, large 0.047 to 0.118 s (pass two: 3.4 to 4.2 s on the Mac, 0.08 s on the servers). `node -e 0`: 0.009 to 0.019 s. An idle opencode after its task, on a small: 69 busy ticks in 30 s, 2.3% of the CPU (pass two: 1136 ticks, 38%).

**mac-idle (N2, ENG-196).** `com.apple.Virtualization.VirtualMachine` by `top -l 2 -s 2 -o cpu`, the second sample, at +10, +30 and +60 s after `repose rm` returned (`~/lab/rmm.sh`, log `~/lab/n2.log`):

| Removed | +10 s | +30 s | +60 s |
|---|---|---|---|
| e2e-mac-l1 (large, last machine) | 1.8% | 1.1% | 0.6% |
| e2e-mac-s2 (small, last machine) | 1.9% | 1.1% | 1.7% |
| e2e-mac-s3 then e2e-mac-l2 (a small and a large, pass two's 300% case) | 1.6% | 1.4% | 0.6% |
| e2e-mac-b (small; large e2e-mac-a still running, idle) | 10.3% | 6.3% | 5.7% |
| e2e-mac-a (large, last machine, after a Mac reboot) | 7.1% | 0.8% | 1.1% |
| e2e-mac-g (small) | 1.0% | 1.0% | 1.2% |
| e2e-mac-t1 (tiny, pass two's 100% case) | 1.1% | 0.9% | 0.8% |
| e2e-mac-k (small, after a 4-minute opencode task) | 1.7% | 6.3% | 6.2%, then 0.9, 0.7, 0.7, 0.7% from +66 to +112 s |

e2e-mac-s1's readings were lost to a bad grep in my script; one reading at +68 s said 1.0%. With nothing on the host, ten readings 30 s apart from 06:20 to 06:25 alternated between 0.8 to 1.1% and 5.6 to 6.3%, and four readings from 06:30 to 06:31 were 0.7 to 0.9%. Falsifier, from pass two: a reading over 10% a minute after a destroy with nothing running. 0 of 7 (every row but e2e-mac-b's, which had a machine left).

## What the fixes could have broken, measured

**The CPU reset with a machine still running.** Large e2e-mac-a ran a probe that sleeps 10 ms in a loop and records the longest gap, and a busy loop on one CPU; small e2e-mac-b ran a busy loop. `repose rm e2e-mac-b` at 05:55:08. The longest gap on e2e-mac-a stayed 0.013 s, no gap over 0.2 s was logged, `nproc` stayed 2, both of its loops were still running a minute later. The Virtualization process read 104% at +10, +30 and +60 s, which is e2e-mac-a's own busy loop. So the reset leaves a running neighbour alone.

**`repose config add` under an agent.** Same large, opencode working on a Flask task, two busy loops (both CPUs full), load 3.4: `repose config add postgresql` `Applied revision 2bfa7080 in 29s.`, exit 0 (pass two: 7m15s on a small). opencode's processes kept their elapsed time (08:25 after the apply), `repose ps` still said `opencode-2 working`, `pg_isready` accepting. `repose config add redis` with one busy loop: 8.2 s. On an idle small: 9.7 s (pass two: 30 s). The probe's longest gap across both applies: 0.033 s.

**Snapshots.** `repose snapshots create e2e-mac-a` (running large, Redis and Postgres on it): 2.1 s, 216.4 MB. `repose stop e2e-mac-a --yes` 0.9 s, then `repose snapshots restore e2e-mac-a 9b5eff5a --as e2e-mac-f --yes`: `Restored ... in 26s; it is running (large).` The marker file was there, `redis-cli ping` answered PONG, `pg_isready` accepting. `repose start e2e-mac-a` afterwards: `ready in 10s`, marker and both services there.

**Undo on a dirty checkout.** A git repo with one commit, one modified file and one untracked file: `repose run e2e-mac-g --temp -d --size small` printed `Synced: 1 modified, 1 untracked (1 new commit)  1.2s` and `Ready in 16s.` `repose snapshots create e2e-mac-g` 1.4 s (3.2 MB); then `rm -rf ~/proj/*` in the machine; `repose stop`; `repose snapshots restore e2e-mac-g 984b62c7 --yes` 16 s. `calc.py` with its uncommitted line and `notes.txt` were back.

**Reboot with nobody logged in.** A large and a small running, a marker written and synced in each. `sudo shutdown -r now` at 06:07:11, `who` empty before and after, nobody ran `repose host run`.

| Event | Now | Pass two |
|---|---|---|
| Mac answers SSH | +31 s (uptime 16 s, 0 users) | +32 s |
| Host `online` | +33 s | never shown offline |
| Both machines `running` | +50 s | +96 s (large), +136 s (small) |
| Marker read through `repose exec` | +54 s (large), +57 s (small) | +107 s, +143 s |

Agents' first screens just after the reboot are in the table above: 1.0 to 2.0 s, where pass two measured 24.5 s for Claude Code on a large. Redis and Postgres came back with the machine.

**SSH through the gateway.** `ssh e2e-mac-a.repose true` from the Mac: 0.32 s, 5 of 5.

## The second pass's P2 list, re-run

| P2 of pass two | Now | Evidence |
|---|---|---|
| Snapshot id cannot be typed back | Reproduces | `repose snapshots list` shows `9b5eff5a`; `repose snapshots restore e2e-mac-a 9b5e --as e2e-mac-f --yes`: `e2e-mac-a has no snapshot 9b5e.`, exit 2. |
| Destroy line promises a snapshot of a project with no disk | Not re-run | Needs an M7 state (ENG-188), which I did not make. |
| `repose host rm` asks, then refuses | Reproduces | Under a pty with e2e-mac-g on the host: `Remove e2e-mac? This deletes the 3 snapshots kept on it; repose has no copy. [y/N]`; with `--yes`: `e2e-mac still has e2e-mac-g on it. `repose rm` each of them first.`, exit 1. |
| `host ls --json` provider and old id | Reproduces | `"provider": "azure"`, `id` `01a1f585-...`, `created_at` `2026-11-19T20:35:40Z`, after two removes and adds today. |
| `~/Library/Logs/repose` left after `host rm` | Reproduces | After `repose host rm e2e-mac --yes` at 06:15:56, `Application Support/repose` was gone and `Logs/repose` held `console.log` and `launcher.log`; `launcher.log` carries lines from 00:27 onward, across every install today. |
| `config revisions` shows an applied revision as `built` | Reproduces | On e2e-mac-g after `config add redis`: `7026d65d applied`, `05ce062c built`. |
| `park-key ... key_unavailable` every minute in `launcher.log` | Reproduces | 06:16:21, 06:16:21, 06:17:21, 06:18:21, 06:19:21; 310 such lines in the file. |
| Host add to `online` 92 s | Fixed | 51 s and 53 s. |

## New P2

- **`repose ls` trails `repose ps` by about 50 s.** opencode started on e2e-mac-k at +0 s. `repose ps e2e-mac-k` said `working` at +23 s; `repose ls` showed `-` in AGENTS until +71 s, then `opencode: working`. At the end `ps` said `idle` at +227 s and `ls` still said `working` at +236 s. Polled every 8 to 10 s. Not compared on repose's servers, so not shown to be the Mac's.
- **The doc's start times are now pessimistic.** own-hardware.md says a stopped machine starts in about 33 s and a new one is ready in about 40 s. Measured: start of a stopped large 10 s; new machines 15 to 17 s (5 of 5 after the first). Harmless; a customer is pleasantly surprised.

## Value verdicts

**Is it better than SSH into the Mac plus tmux?** Yes, now. The thing that lost to tmux in pass two was speed at the moment the customer waits, and it is gone: agents show their first screen in half a second to two seconds, a one-shot opencode prompt acts in 35 to 40 s, a new machine is ready in about 16 s, a stopped one starts in 10 s. On top of tmux the customer gets isolated Ubuntu machines with sudo, a dirty checkout synced in 1.2 s, an undo that brought back a deleted checkout in 16 s, a fork in 26 s, Postgres in 29 s while an agent works with both CPUs full, a headless reboot that has everything back in under a minute, and SSH from anywhere in 0.32 s. The Mac idles at about 1% when nothing runs.

**Would I pay $10 a month?** Yes. M7 is the one known P1 left, and it has a workaround (`repose rm` the errored row). The P2s I would not notice in the first week, except the snapshot id that cannot be typed back, which I would meet the first time I restore.

**Is it worth selling?** Yes, on the narrow claim: isolated, undoable agent machines on your own Mac. own-hardware.md's line "Agents start as fast as on repose's servers" is true by these numbers.

## Method and what could not run

- Everything ran on the Mac over `ssh macmini`, as a customer would type it in a terminal there. Long commands ran with a stamp of seconds since start (`~/lab/stamp`) and a process-group timeout (`~/lab/to`); guest-side measurements went through `repose exec`. Scripts on the Mac: `~/lab/oc2.sh`, `tui.sh`, `ver.sh`, `n2.sh`, `rmm.sh`, `gap.pl`; the reboot watcher ran on woker-1.
- Machines: e2e-mac-s1, s2, s3, l1, l2, a, b, f, g, k, t1. At most two alive at once; all destroyed. The host e2e-mac was removed and added again at 06:15 and is left online with no machines.
- I am not an unprimed customer: I read the brief and both reports first. Treat "I did not notice" as weak evidence.
- Not run: Claude Code, Codex and Gemini past their first screen (no logins on this Mac). `repose attach` (needs a terminal I did not have over this SSH). The dashboard. A 16 GB Mac. The plan's gates (the account is billing-exempt). A power cut, and FileVault. The capacity edge (ENG-191) and M7 (ENG-188), as briefed. The kernel command line of the appliance: its console log does not print it, so the THP setting is shown only by its effect.
- Evidence rules that bit me: a `nohup` job started over SSH on the Mac died when the SSH session closed (`nohup: can't detach from console`), twice: once a `repose config add` that never reached the api (no revision was created), once a CPU logger. I discarded both and re-ran in the foreground. My first CPU reader grepped for `Virtualization` where `top` prints `com.apple.Virtua`, and logged blanks for e2e-mac-s1; I kept that row marked as lost rather than re-reading it late.
