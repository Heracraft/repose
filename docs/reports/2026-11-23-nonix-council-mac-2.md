Not yet. Two P1s stop it. N1: on the Mac every JavaScript agent starts 6 to 12 times slower than on repose's servers, so `repose run -p` with opencode acts after six and a half minutes and Claude Code takes 14 s to show its first screen. N2: destroying a machine leaves the appliance burning one to three of the Mac's cores (100%, 200%, 300% measured) with nothing running, until another machine starts. No P0. The third P1 (M7, a refused create or restore leaves an errored project) I would pay through.

# No-Nix guests, council second pass: the Mac mini path

Reviewer: spark `mac` (third session), 2026-11-23, 02:54 to 04:45 UTC. Hardware: the rented AWS Mac mini (mac-m4.metal: M4, 10 cores, 24 GB, macOS 15.8.3, eu-west-1), reached over SSH. CLI v0.1.60 (`curl -fsSL https://repose.run/install.sh | sh`), appliance and hostd 2026.11.22.3, Ubuntu base 2026.11.22.13 on every machine I made. Account: the owner's, through a minted one-hour session (billing-exempt). Customer: a developer with an M4 mini deciding whether $10 a month beats SSH and tmux. First pass: docs/reports/2026-11-22-nonix-council-mac.md (M1 to M9). Ranks follow docs/REVIEW.md: P0 blocks launch, P1 is fixed before the plan is sold, P2 after.

## What would make a user scream

| # | Rank | What the customer sees |
|---|------|------------------------|
| N1 | P1 | Agents start slowly on the Mac. `repose run -d --agent opencode -p` writes its first file at +379 s (small) and +406 s (large), against +63 s on repose's servers. Claude Code's first screen comes after 14 s (24 s on a large just after a reboot), against 1.6 s; Gemini CLI's after 28 to 41 s, against 3.1 s. Codex, a native binary, is near parity. own-hardware.md says the virtual machine "costs nothing we can measure". |
| N2 | P1 | After `repose rm` of a machine, the Mac's Virtualization process runs at a steady whole number of cores, with nothing running, until another machine starts: 300% (02:54 to 02:58), 200% (04:27 to 04:33), 100% (04:36 on). 3 of 3. |
| M7 | P1 | Still reproduces, 3 of 3. A restore or fork the host has no room for, and a restore of a snapshot from repose's servers, each leave a project in `error` whose hint (`repose start X` restarts it) is refused. The servers-snapshot case answers with a storage path and "the snapshot stays restorable", though a customer on this plan cannot restore it anywhere. |
| P2 | P2 | The snapshot id a customer sees cannot be typed back; the destroy line promises a snapshot of a project that never had a disk; and smaller copy items (list below). |

Not hunted, as briefed: ENG-194 (opencode's 503 gate), ENG-191 (api and host disagree at the capacity edge), the "about 200 MB" line.

## The first pass's findings, re-run

| First pass | Now | Evidence |
|---|---|---|
| M1 P0, snapshot promise false | Fixed | `repose rm e2e-mac-f1` (own host): `Its final snapshot is kept on your host e2e-mac; removing the host deletes it.` `repose host rm` asked `This deletes the 3 snapshots kept on it; repose has no copy.` Afterwards `ls --destroyed` showed `gone with host e2e-mac`, and `repose restore e2e-mac-f1 --as e2e-mac-f2` answered `That snapshot was kept on your host e2e-mac, which was removed, so it is gone; repose had no copy (conflict)`, exit 1, and created nothing. |
| M2 P1, no migration | Decided (ENG-183, owner 2026-11-22: no migration between repose's servers and own hardware); the failure mode remains, filed under M7 | `repose restore e2e-mac-opencode --as e2e-mac-r10` placed it on the Mac and failed in 0.6 s with `snapshot 01a0bf7e-.../2026-11-23T01-39-30.568870Z.img.zst not in the store (not_found)`. Unverified that e2e-mac-opencode was on repose's servers: `ls --destroyed --json` carries no host. |
| M3 P1, config add times out | Fixed | Step A3: both adds finished under load, 8.5 min and 7m15s; 30 s idle. |
| M4 P1, opencode prompt never runs | Fixed in effect; slow (N1) | Step A1: 2 of 2 on the Mac wrote the file. |
| M5 P1, menu change kills the agent | Fixed | Step A3: opencode kept its elapsed time through two applies. |
| M6 P1, reboot needs a GUI login | Fixed | Step A2: machines answered at +107 s and +143 s with nobody logged in. |
| M7 P1, refused create leaves debris | Reproduces | Below. |
| M8 P1, capacity | Not hunted (ENG-191). One related instance below (M7, the start refused while a destroy was in flight). own-hardware.md now prints what fits. |
| M9 P2, copy | Partly fixed | The destroy line on a project with no disk, the snapshot id prefix, `provider: azure`, the host id from 2026-11-19 and `~/Library/Logs/repose` after `host rm` all still reproduce (P2 list). |

## N1 (P1). Agents start 6 to 12 times slower on the Mac

The customer's view. `repose run NAME --temp -d --size S --agent opencode -p "create hello.txt with the word hi"`, one machine on the host at a time, polled every 30 s with `repose exec NAME -- cat ~/hello.txt`; the times are the file's mtime against the command's start.

| Where | Size (vCPU) | `Ready in` | File written |
|---|---|---|---|
| Mac, e2e-mac-s4 | small (1) | 69 s | +379 s |
| Mac, e2e-mac-l3 | large (2) | 65 s | +406 s |
| repose's servers, e2e-mac-h1 | small (4 on that host) | 49 s | +63 s |

For five minutes after `Ready`, `repose ps NAME -w 1` shows the prompt typed in opencode's input box and nothing else. A customer who attaches then sees M4's symptom and has no way to tell it apart.

The TUI's first screen, from `tmux new-window` to the first frame that holds the agent's own text (script `tui.sh`, polled every 0.5 s, no login on any of them):

| Agent | Mac small (1 vCPU), warm | Mac small, just after reboot | Mac large (2 vCPU), just after reboot | Servers small (4 vCPU) |
|---|---|---|---|---|
| Claude Code | 14.3 s | 13.1 s | 24.5 s | 1.6 s |
| Codex | 1.8 s | 6.3 s | 10.1 s | 1.1 s |
| Gemini CLI | 35.9 s | 28.0 s | 41.3 s | 3.1 s |

Where the time goes, from the guest's opencode log (`~/.local/share/opencode/log/opencode.log`):

| Step | Mac small | Mac large | Servers small |
|---|---|---|---|
| CLI start to event stream connected | 95 s | 113 s | 3 s |
| MCP servers (chrome-devtools, playwright) | timed out after about 3.5 min | timed out after about 3.5 min | connected in 4 s |
| CLI start to file written | 294 s | 327 s | 7 s |

- Not ENG-194 in these runs: each Mac run logged four 503s on `/api/model` and `/api/provider`, then a `model.updated` event 14 to 15 s later, and the prompt was sent.
- Not the MCP servers: the same prompt on the large with `~/.config/opencode/opencode.json` (the MCP block) moved away took 289 s.
- Not compute in general. On the idle large, a perl loop of 50M additions took 0.65 to 0.68 s; the Mac itself takes 0.53 to 0.55 s. `dd bs=1 count=1000000` took 0.22 s (servers: 0.29 s).
- The JIT. `opencode --version` takes 3.4 to 4.2 s on the Mac guest and 0.08 s on the servers. With `BUN_JSC_useJIT=0` it takes 0.37 to 0.65 s on the Mac (servers: 0.06 s). Raising JSC's JIT thresholds tenfold gives 2.0 s; baseline JIT only, 3.0 s. Node shows the same: `node -e 0` 0.31 s against 0.049 s, `node --jitless -e 0` 0.18 s. Steady-state JIT code is close: a node loop of 3e8 additions, 2.9 s against 1.8 s. So the cost is in compiling and installing JIT code in the nested guest, not in running it. The mechanism inside the hypervisor is unverified. opencode cannot simply run without the JIT: its TUI exits with `bun:ffi requires the JIT`.
- After its task, an idle opencode (TUI plus server) still used 38% of one CPU (1136 ticks in 30 s). On a small that is 38% of the machine.
- Instrument: the guest's opencode log, `tmux capture-pane`, bash `time`, all through `repose exec`. Falsifier: the same `opencode --version` taking under 0.5 s on the Mac guest with the JIT on. It took 3.4 s or more in 5 of 5.
- Why P1 and not P2: the product's promise is "an agent that keeps working". It does now, but against SSH and tmux on the same Mac (where these agents start in a second or two) every agent start costs 10 to 40 s, and a one-shot opencode prompt costs six minutes. The docs say the opposite.

## M7 (P1). A refused create or restore still leaves an errored project

Three new instances, 3 of 3:

1. `repose restore e2e-mac-opencode --as e2e-mac-r9`, with a large and a small running: `Could not restore e2e-mac-r9: your host e2e-mac has no room for a large machine (capacity). ... the snapshot stays restorable.`, exit 8, 0.3 s. `repose ls` then: `e2e-mac-r9 large error` and `` `repose start e2e-mac-r9` restarts it ``. `repose start e2e-mac-r9`: `E2e-mac-r9 has no usable volume because the restore into it did not finish; restore a snapshot into it again, or remove it with `repose rm e2e-mac-r9` (conflict).`, exit 1. The name is still capitalised.
2. `repose snapshots restore e2e-mac-l3 93f3dcb3 --as e2e-mac-f1 --yes` with the large running: `Could not restore 01a2069e-7fd3-7844-bdc7-6b3f93f3dcb3: your host e2e-mac has no room for a large machine (capacity).`, exit 8, and `e2e-mac-f1 large error` with the same `restarts it` hint. This refusal names the snapshot's full id where case 1 named the project.
3. `repose restore e2e-mac-opencode --as e2e-mac-r10` with room: the `not in the store (not_found)` storage path (table above), exit 1, `e2e-mac-r10 large error`, and `` `repose start e2e-mac-r10` restarts it ``. The line ends `the snapshot stays restorable`; on the Own hardware plan nothing can restore it.

A fourth, of the same family: `repose rm e2e-mac-f1` then, 4 s later, `repose start e2e-mac-l3` (stopped): `Could not start e2e-mac-l3: the host has no free memory for a machine this size; stopping a running machine frees room (insufficient_capacity).` Nothing was running; f1 was still `destroying`. The stopped machine was then in `error`. `repose start` 20 s later worked, in 37 s.

- Expected: a refused create leaves no project, as `repose run` does when the api refuses. A refused start leaves the machine `stopped`.
- Instrument: the CLI's text and exit codes, `repose ls`. Falsifier: a refused restore that leaves no row. None did.
- Card: ENG-188, re-ranked P1 to match this report. N1 is ENG-195, N2 is ENG-196.

## N2 (P1). A destroyed machine leaves the appliance burning cores

Reproduction, on a host with one machine and nothing else:

1. `repose run e2e-mac-t1 --temp -d --size tiny --no-sync` (70 s). `top -l 2 -s 3` on the Mac: `com.apple.Virtualization.VirtualMachine` at 62.9%, 82.0%, then 11.9% and 8.8% once the machine settled.
2. `repose rm e2e-mac-t1 --yes` at 04:35:46. Then, every 15 s: 74.0, 100.1, 100.0, 100.1, 99.9, 99.9, 99.9, 99.8%. Nothing is on the host.

The same happened twice before, each time in whole cores. 300% from 02:54 to 02:58 (my first readings) after the previous session destroyed a large (2 vCPU) and a small (1 vCPU) at 02:53; it was gone within two minutes of my next machine starting. 200.0% at every reading from 04:27 to 04:33 (`~/lab/cpu.log`, 30 s apart) after e2e-mac-c2 (large) and e2e-mac-c1 (small) were destroyed; creating e2e-mac-t1 at 04:33 stopped it, as in step 1. Before any of this, right after `host add`, the process was under 1.5%; with one idle large it was 5.4%.

- Expected: an appliance with no machines idles near zero, as it does right after `host add`.
- What the customer sees: nothing, unless they open Activity Monitor. A mini on the desk then runs one to three cores flat out, with the heat, the power and the fan that go with it, for as long as no machine runs. The customer most likely to hit it is the one who has just removed their last machine.
- How long it lasts on its own: at least 6 minutes in each case; I never saw it stop without a new machine. `~/lab/cpulog.sh` keeps logging until about 05:10 UTC.
- What spins: unverified. There is no shell in the appliance, and its console log shows nothing after `guest destroyed`. The figures are whole cores (300% after a large and a small, 200% after a large and a small, 100% after a tiny), so not simply one per vCPU destroyed. That some of the appliance's CPUs stay busy after running a machine's vCPUs, and settle once a machine runs on them again, is a hypothesis for a builder (an idle-poll state in the nested hypervisor, for example), not a finding.
- Instrument: `top -l 2 -s 3 -o cpu` on the Mac. Falsifier: a reading under 10% within a minute of a destroy with nothing else running. 0 of 3.

## P2 list

- **The snapshot id cannot be typed back.** `repose snapshots list` shows `93f3dcb3` (the last 8 characters). `repose snapshots restore e2e-mac-l3 93f3 --as ...`: `e2e-mac-l3 has no snapshot 93f3.`, exit 2. The help says "4 or more of its first or last characters". Unchanged from the first pass.
- **The destroy line promises a snapshot that cannot exist.** `repose rm e2e-mac-r10` on the restore that never got a disk: `Destroying e2e-mac-r10. Its final snapshot is kept on your host e2e-mac; removing the host deletes it.` Unchanged from the first pass, in new words.
- **`repose host rm` asks, then refuses.** With a machine on the host it asks `Remove e2e-mac? This deletes the 3 snapshots kept on it; repose has no copy. [y/N]`, and on `--yes` answers `e2e-mac still has e2e-mac-l3 on it. repose rm each of them first.` The refusal should come before the question.
- **`repose host ls --json`** says `"provider": "azure"` (ENG-173), and a host added again under its old name keeps `id` `01a1f585-...` and `created_at` `2026-11-19T20:35:40Z`.
- **`~/Library/Logs/repose`** is left after `repose host rm` (console.log, launcher.log). own-hardware.md does not say so.
- **`config revisions`** showed the first revision `fcccfe8b` as `applied` before the Redis add and as `built` after it. A customer reads `built` as "not applied".
- **`launcher.log`** gets a `park-key: event=key_unavailable ... Write permissions error` line every minute (160 in two hours). Parking is documented as absent on a Mac; the line every minute is noise in the file a customer opens when something is wrong.
- **Host add to `online`** took 92 s (n=1, 04:19:20), against 59 s and 44 s on the first pass; the CLI's own part took 5.2 s.

## Step A, the live checks

Each was recorded with `ops/dev/release-queue live <branch> --ran ...` at the time.

- **mac-appliance-pin.** v0.1.60's binary carries `appliance-2026.11.22.3` and no other appliance tag. `repose host add e2e-mac` fetched `2026.11.22.3` at 01:59:31 and again at 04:19:20 (`fetching the appliance image 2026.11.22.3 (about 200 MB)`), with no `launchd took no agent` line; the 00:27 add with the earlier CLI fetched 2026.11.22.2 and printed it.
- **mac-host (ENG-187, M6).** `sudo shutdown -r now` at 04:08:44 with a large (marker file written and synced) and a small running; nobody logged in at any point (`who` empty) and nobody ran `repose host run`. +32 s the Mac answered SSH (uptime 17 s) with `repose host run` and vfkit already running as ec2-user. +35 s hostd started in the appliance, +36 s `stream connected`. The host never showed `offline`, since the outage was under 90 s. +96 s the large `running`, +107 s `repose exec e2e-mac-l3 -- cat ~/marker.txt` printed the marker. +136 s the small `running`, +143 s `repose exec` answered. Both came back cold, as documented: the small's opencode window was gone.
- **mac-apply (ENG-184, ENG-186; M3, M5).** Small e2e-mac-a1 (1 vCPU), opencode mid-task on a real prompt (status `opencode working` from 03:46, TUI on `Running write…`), plus a busy loop I added (`perl -e 'while(1){}'`) to stand for an agent's test run; load average 5 to 7. `repose config add redis` at 03:50: applied between 03:59:10 and 03:59:27, about 8.5 min, no timeout (my harness cut the CLI at 170 s; the apply went on). `repose config add postgresql` at 04:00:36: `Applied revision 37ca0407 in 7m15s.`, exit 0 (lists 1m36s, unpack 2m02s, set-up 2m44s, service start 28 s). apt ran in `repose_menu.slice` at 3 to 8% CPU. opencode's TUI and server kept their elapsed times (28:13 and 27:55 at 04:08), so neither restarted. `redis-cli ping`: PONG. `pg_isready`: accepting connections. Reference on this build: the same Redis add on an idle small (e2e-mac-c1) took 30 s. So load is what makes it slow, by I-1560's CPU weight 20, and it finishes.
- **mac-opencode (ENG-185, M4).** See N1: 2 of 2 wrote the file, at +379 s and +406 s. The cause of the slowness is opencode's startup in the nested guest, not ENG-194's gate in these runs. The previous session's small that never wrote the file in 900 s did not reproduce (n=1; that run shared the host with a large).

## Value verdicts

**Is it better than SSH into the Mac plus tmux?** Closer than yesterday. The core loop now holds: a one-shot prompt runs, a package install finishes while an agent works and does not stop it, and a headless restart brings the machines back in about two minutes with nobody at the Mac. On top of tmux the customer gets isolated Ubuntu machines with sudo, snapshots in 1.8 s, a fork in 44 s, SSH from anywhere in 0.31 s (5 of 5) through the gateway, and an honest story about what a snapshot on the Mac is. What still loses to tmux is speed at the moment the customer is waiting: an agent that starts in a second on the bare Mac takes 14 to 40 s in a machine, and opencode's one-shot prompt takes six minutes to act.

**Would I pay $10 a month?** Yes, once N1 is fixed (the agents I use start in a few seconds) and N2 is fixed (my Mac idles when nothing runs). M7 is a nuisance with a workaround (`repose rm`), and I would pay through it. The P2 list I would not notice in the first week.

**Is it worth selling?** Yes, on the narrow claim the first pass gave: isolated, undoable agent machines on your own Mac. Not with "the virtual machine costs nothing we can measure" on the page, which N1 contradicts.

## What is right, measured

- Install: `curl ... install.sh | sh` replaced v0.1.60 in 2.0 s.
- `repose host add e2e-mac`: 5.2 s of work, `online` at +92 s. `repose host rm e2e-mac --yes`: 1.5 s, removed `~/Library/Application Support/repose`, the LaunchDaemon plist, and stopped vfkit.
- First machine on a new host: `Ready in 2m09s` (small, base download inside; the docs say about two minutes). Later creates: small 34 s and 62 s, large booted in 52 s. Start of a stopped large: 37 s (docs: about 33 s).
- Snapshot of a running large: 1.8 s (1.3 MB). Fork with `snapshots restore --as`: 44 s, and the marker file was in it.
- Headless reboot: step A2.
- SSH: `ssh e2e-mac-a1.repose true` 0.31 s, 5 of 5. The gateway's failed handshakes the brief mentions (02:21 to 02:29) did not recur in my window.
- Menu: Redis in 30 s on an idle small; under load, both adds finished and the agent kept working.
- The capacity table in own-hardware.md matches what I could reach on 24 GB: a large and a small ran together; a second large was refused.
- Copy that tells the customer what to do: `e2e-mac-c2 is large and starting; --size sizes a machine this command creates or starts. repose resize e2e-mac-c2 --size small changes it, which restarts it.`; `e2e-mac still has e2e-mac-l3 on it. repose rm each of them first.`; the M1 lines above.

## Method and what could not run

- Everything ran on the Mac over `ssh macmini`, as a customer would type it in a terminal there. Long commands ran detached on the Mac with a perl stamp of seconds since start (`~/lab/stamp`) and a process-group timeout (`~/lab/to`); guest-side measurements went through `repose exec`. Scripts: `~/lab/oc2.sh`, `watch.sh`, `revwatch.sh`, `tui.sh`, `jit.sh`, `jit2.sh` on the Mac.
- Machines: e2e-mac-s4, e2e-mac-l3, e2e-mac-a1, e2e-mac-c1, e2e-mac-c2, e2e-mac-f1, e2e-mac-r9, e2e-mac-r10 on the Mac; e2e-mac-h1 and e2e-mac-h2 on repose's servers for the reference numbers. At most two alive at once; all destroyed. The host e2e-mac was removed and added again at 04:19 and is left online with no machines.
- I am not an unprimed customer: I read the brief and the first report before I started, as the brief asked. docs/REVIEW.md says a fresh session walks first and verifies after; this one did both at once, so treat "I did not notice" as weak evidence.
- Not run: Claude Code, Codex and Gemini past their first screen (no logins on this Mac; logging in as the owner is out of scope). The dashboard. A 16 GB Mac. The Own hardware plan's gates (the account is billing-exempt). A real power cut, and FileVault. What spins in the appliance after a destroy (N2): there is no shell into it; `~/lab/cpulog.sh` on the Mac logs its CPU every 30 s to `~/lab/cpu.log` until about 05:10 UTC.
- Evidence rule that bit me: a "wasm off" run of `opencode --version` came back in 0.05 s and looked like the answer. It was opencode refusing an invalid `BUN_JSC_` variable and exiting. I count only runs whose output I read.
- Evidence rule that bit me twice: the minted session expired at 03:55 in the middle of a watcher, and the watcher logged empty lines for a minute. I re-minted and discarded those lines.
