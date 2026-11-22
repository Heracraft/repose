# No-Nix guests, council first pass: the Mac mini path

Reviewer: council `nonix-mac`, 2026-11-22, 15:17 to 17:30 UTC. Hardware: a
rented AWS Mac mini (mac-m4.metal: M4, 10 cores, 24 GB, macOS 15.8.3,
eu-west-1), reached over SSH. CLI: v0.1.56 from `curl -fsSL
https://repose.run/install.sh | sh`, which pins appliance 2026.11.22.1.
Account: the owner's, through a minted one-hour session (billing-exempt).
Base on every Ubuntu machine I made: **2026.11.22.8** (`repose status`); I
did not get 2026.11.22.10. Customer: a developer with an M4 mini deciding
whether $10 a month beats SSH and tmux.

## What would make a user scream

One P0. Ranks follow docs/REVIEW.md: P0 blocks launch, P1 is fixed before
the plan is sold, P2 after.

| # | Rank | What the customer sees |
|---|------|------------------------|
| M1 | P0 | Every line says a destroyed project's snapshot is kept for 30 days. `repose host rm` on the Mac deletes it, and its question says the snapshots "stay on its disk". Afterwards `ls --destroyed` still offers them, and the restore fails with a storage path. |
| M2 | P1 | No machine moves between repose's servers and the Mac. Hosted to Mac fails with `snapshot .../...img.zst not in the store (not_found)` (2 of 2). Mac to hosted cannot be asked for, because `restore` has no `--host`. The docs say a machine "moves only by restoring a snapshot elsewhere". |
| M3 | P1 | `repose config add redis` and `config add postgresql` each ran about 10 minutes, then the revision went `failed timeout: the host running this project did not answer in time`. The reference is 22 s. An agent was working on the machine at the time. A retry of redis took 4 s. |
| M4 | P1 | `repose run -d --agent opencode -p "..."` prints `Ready in 2.0s` and the prompt never runs (3 of 3). Run 1 typed the prompt and did not submit it. Runs 2 and 3 sat on `Starting background server...`, and the window then went away. `repose ps` said `running` throughout. |
| M5 | P1 | A `config add` stopped the running opencode agent in the middle of its task. Its window said `Restarting service… Your session will resume automatically` for more than 20 minutes and never resumed. The README edit stayed uncommitted. |
| M6 | P1 | A Mac that restarts brings nothing back until someone logs in on its screen. After a reboot with no GUI login, the host went `offline` and stayed offline (watched for 4 min). Nothing told the customer that a headless mini, or one with FileVault, needs a login or `repose host run`. |
| M7 | P1 | A refused create or restore leaves a project in `error`. `repose ls` then says "`repose start X` restarts it", and `repose start` refuses that ("no usable volume ... restore a snapshot into it again, or remove it"). Seen 4 times: restore over capacity, two tinies the host refused, and two restores from the store. |
| M8 | P1 | Capacity is two thirds of the appliance, and the api and the host disagree at the edge. 12 GB of an 18 GB appliance: 3 small, or 1 large, or 6 tiny. Large + small + tiny (14 GB) was accepted by the api and then refused by the host. The docs imply 80% (9.6 of 12 GB on a 16 GB Mac). |
| M9 | P2 | Copy that is wrong or leaves the customer guessing (list below). |

### M1 (P0). On own hardware, the promise of a kept snapshot is false

Reproduction, on a Mac with a host and one project:

1. `repose rm itsdangerous --yes` prints `Destroying itsdangerous. Its final snapshot is kept until 2026-12-22.`
2. `repose host rm e2e-mac` asks `Remove e2e-mac? Snapshots taken on it stay on its disk, and repose has no copy. [y/N]`. I read this on a host I added again as e2e-mac3 at 17:26, answered `n` under `script`, and got `Nothing removed.`. On `--yes` the removal took 10 s and deleted `~/Library/Application Support/repose`, 10 GB, with `disk.raw` and every snapshot in it.
3. `repose ls --destroyed` still lists `itsdangerous large ... 187.1 MB  2026-12-22` under RESTORABLE UNTIL.
4. `repose restore itsdangerous --as e2e-r1` prints `Could not restore e2e-r1: snapshot 01a0bf7e-.../01a203d7-.../2026-11-22T17-18-43.951482Z.img.zst not in the store (not_found). ... and the snapshot stays restorable.` and leaves `e2e-r1` in `error`.

- Expected: the destroy line on own hardware says the snapshot lives on the Mac and goes with the host. The host rm question says the snapshots are deleted. `ls --destroyed` drops what no longer exists.
- What happened: three lines say the data is kept. The fourth hides it behind a storage path. The docs page itself is right: "the command also stops the machine and removes it, disk and snapshots included". The CLI lines contradict the page, and they are what a customer reads at the moment of the decision.
- Seconds: host rm 10 s. Restore refusal 1 s.
- Instrument: the CLI's own output; `du -sh ~/Library/Application Support/repose` (10G before, missing after).
- Falsifier: the restore succeeding from any copy. It did not.
- Why P0: the rule is "anything that loses a tenant's work is P0 whatever its size". A customer who destroys a project, trusts "kept until 2026-12-22", and removes or replaces the Mac loses that work. They were told three times that it was safe.

### M2 (P1). No migration between hosted and own hardware

- Hosted to Mac: `repose run e2e-h1 --host repose --size tiny -d --no-sync` (51 s), wrote `~/marker.txt`, `repose snapshots create e2e-h1` (1 s, 230.8 KB). Then `repose snapshots restore e2e-h1 01a20425 --as e2e-h2m --yes` placed it on the Mac (the default host) and failed in 1 s: `snapshot 01a0bf7e-.../01a20424-.../2026-11-22T16-44-54.742540Z.img.zst not in the store (not_found)`. Again 30 s later with `--as e2e-h2m2`: the same. Each attempt left an errored project.
- Mac to hosted: `repose restore --help` and `repose snapshots restore --help` have no `--host`. With a host, `--as` places on the host. I found no command that asks for the other direction.
- Docs: own-hardware.md says "A machine stays on its host for life; it moves only by restoring a snapshot." billing.md says you cannot switch between Own hardware and a hosted plan; you cancel one and choose the other when it ends. Together these leave a hosted customer who wants to try the Mac with no way to take a machine along.
- Falsifier: a restore across hosts succeeding after the snapshot had time to upload. The 30 s retry failed the same way.

### M3 (P1). `config add` times out while an agent works

- Reproduction: machine `itsdangerous` (large, Ubuntu 2026.11.22.8) with an opencode agent running pytest in it. `repose config add redis` at 15:30 printed `installing redis... 30s ... 4m30s` (my SSH timeout cut the CLI there). `repose config revisions` then showed `287ab972 failed timeout: the host running this project did not answer in time, so the operation was given up; it can be run again`. In the guest, `apt-get install redis-server` started at 15:34 and dpkg finished at 15:38:37. The unit was then `masked`. A retry at 15:44 took 4 s, and `redis-cli ping` answered PONG.
- `repose config add postgresql` at 15:44: the revision was `built` until 15:54:36, then `failed timeout`. At 15:55 `apt-get ... install postgresql-16` had been running 7:28 at 57% CPU, waiting on a child. `pg_isready` answered `no response`.
- The host log at 16:04:33: `"msg":"menu not applied" ... "code":"guest_unresponsive","items":2`.
- Expected: about 22 s, the previous spark's reading for Redis through the menu on this Mac.
- Unverified: the cause. The agent's load (load average 4.5 to 6) is the only difference from the spark's run that I can name. Falsifier to run next: the same two adds on an idle new machine. I did not run it.

### M4 (P1). `run -p` with opencode does not run the prompt

- Run 1: `repose run -d --agent opencode -p "Run the test suite ..."` printed `Ready in 2.1s`. 95 s later, `repose ps opencode` showed the prompt in the input box, not sent. The guest process was `opencode --prompt=Run the test suite ...`. `tmux send-keys -t opencode Enter` started it, and it then passed all 297 tests.
- Runs 2 and 3, after a stop and start: `Ready in 2.0s` / `2.3s`. The window showed only `Starting background server...` for 2 min, and at 16:21:49 it was gone (`itsdangerous has no window opencode`). `opencode serve --service` was running. `repose ps` listed `1:opencode running` while nothing ran.
- Expected: the agent works on the prompt after `-d`, which is the product's reason to exist ("keep working after you close your laptop").
- Opencode v2.3.2. The free model `Tidepool Alpha Free` was picked by default, because no opencode login was on the Mac. I could not try Claude Code: the owner's Claude login is per host, and this host had none (agents.md says so).
- Falsifier: one run in three working. None did.

### M5 (P1). A menu change kills the agent mid-task

At 15:44 two `config add` revisions were applied. The opencode service in the guest was restarted then (`opencode serve --service`, elapsed 10:05 at 15:55). The TUI showed `Restarting service… Your session will resume automatically.` at 15:55, 16:05 and 16:06. It never resumed. The work so far (README.md, 11 lines) sat uncommitted until I stopped the machine. Unverified that the menu apply is the trigger: the timing is the only evidence. Falsifier to run next: `config add` of one small package while an agent is mid-task, then `ps -o etime` on `opencode serve`.

### M6 (P1). After a reboot, nothing comes back without a GUI login

- `repose host add` over SSH prints `launchd took no agent for this session; the appliance starts detached and the agent takes over at the next login`. troubleshooting.md explains that line.
- `sudo shutdown -r now` at 16:47:14. The Mac was up in 25 s. With no GUI login (`autoLoginUser` not set, FileVault off), the host went `offline` at 16:48:48 and stayed so (watched to 16:51:21). `repose ls` showed both machines as `running` with `your host e2e-mac has not answered for 5m`.
- Recovery that I found myself: `( repose host run > log 2>&1 </dev/null & )` over SSH. Online in 11 s. The large machine was running after 52 s, the tiny after 103 s. The disk marker was kept, and memory was gone, as documented. `nohup repose host run` fails on macOS (`can't detach from console`), and nothing starts.
- Expected: own-hardware.md says the machines start again "at your next login". It does not say that a mini used as a headless box (the "x86 box in a cupboard" picture, on a Mac) needs automatic login, which FileVault forbids, or `repose host run`.
- Partly a lab limit: an EC2 Mac has no GUI session, as the park key also shows (deliberately absent). But a real mini after a power cut sits at the login window just the same.

### M7 (P1). A refused create leaves a project whose hint is wrong

- `repose snapshots restore 4f8bd58a --as e2e-fork --yes` with a large already running: `Could not restore ...: your host e2e-mac has no room for a large machine (capacity)`, exit 8. `repose ls` then showed `e2e-fork large error` and `` `repose start e2e-fork` restarts it ``. `repose start e2e-fork` answered `E2e-fork has no usable volume because the restore into it did not finish; restore a snapshot into it again, or remove it with `repose rm e2e-fork` (conflict).`. A second restore with the same name was refused: `A project named e2e-fork already exists`.
- The same leftover came from the host refusing two tinies (`Could not start e2e-m2: the host has no free memory for a machine this size`) and from both store failures (M1, M2).
- Expected: a refused create leaves nothing, the way the api's own capacity refusal for `repose run` does (`#4 rc=8`, no project). Or the hint names what works.
- Also: `E2e-fork` is the project's name capitalised at the start of a sentence. Names are case-sensitive.

### M8 (P1). Capacity: what fits, and the two refusals that disagree

See the capacity table. The api's check and the host's check disagree at the boundary. With e2e-l1 (large) and e2e-m1 (small) running, the api accepted `repose run e2e-m2 --size tiny` (14 GB in total). The host then refused it and a second tiny, which left two errored projects (M7). With six tinies (12 GB), the api refused the seventh itself. A customer gets two different refusals for the same arithmetic, and one of them leaves debris.

### M9 (P2). Copy

- `repose host add` prints `fetching the appliance image 2026.11.22.1 (about 200 MB)`. own-hardware.md says "about 85 MB", twice.
- own-hardware.md says the appliance disk is "never under 20 GB". `repose host add --help` says `--disk GB ... (default 100, at least 40 ...)`.
- own-hardware.md says "starts in about 33 seconds". Measured starts of a stopped large: 50 s, 37 s.
- The refusal when the Mac is full says `` `repose run --host repose` makes it on repose's servers instead. `` The Own hardware plan buys no memory there (billing.md). Unverified on that plan, because the owner's account is billing-exempt, but on that plan the hint is a dead end by the docs' own words. It also does not say how much room is left or which machine to stop.
- Neither `repose host ls` nor the dashboard's Hosts page shows how much of the host's memory is in use. The CLI says `18 GB`, and the dashboard says `17.5 GB · 8 cores` for the same 18840113152 bytes. To know whether a large fits, the customer must do the arithmetic, and then learn the two-thirds rule from a refusal.
- `repose host ls --json` says `"provider": "azure"` for a Mac mini.
- Adding a host under a name used before (`e2e-mac`) returned a host whose `id` and `created_at` were from 2026-11-19 (`01a1f585-...`, `2026-11-19T20:35:40Z`), and while it was `waiting` `LAST SEEN` read `3m ago`. A new name got a new row. Unverified whether anything else of the old host came back with it.
- `An operation is in progress (conflict).` from `repose snapshots create` for 2 min 43 s, with no hint of which operation or when to retry. The earlier `create`, whose SSH session my harness lost, had in fact finished (`629c2acb`).
- `repose snapshots list` shows `4f8bd58a`, the last 8 characters of the id. `repose snapshots restore itsdangerous 4f8b` answers `itsdangerous has no snapshot 4f8b`. The help says "4 or more of its first or last characters". A customer types the start of what they see.
- `repose rm` on a project that never had a disk (e2e-r1, e2e-m2) still says `Its final snapshot is kept until 2026-12-22.`
- `repose stop e2e-k3` after an interrupted `run`: `checkout unknown (ssh failed: ssh: Could not resolve hostname e2e-k3.repose ...)`.
- install.sh on this Mac could not write the root-owned `~/.zshrc` (an AMI quirk) and advised `home-manager: home.sessionPath = [...]` to someone with no home-manager. The `export PATH=...` line after it is the one that helps.
- `repose host rm` leaves `~/Library/Logs/repose` behind. own-hardware.md does not say so.
- The sidebar's `Running 44 GB` on the dashboard adds the Mac's machine (8 GB) to hosted ones. On a hosted plan, that reads as hosted memory used.

## Capacity

24 GB M4. The appliance has 18 GB and 8 cores (three quarters, all but two). Base 2026.11.22.8. Machines made with `repose run NAME --size S -d --no-sync` one after another, 15 s apart. Instrument: the CLI's exit code and text, `repose host ls`, and on the Mac `vm_stat` plus `footprint` of `com.apple.Virtualization.VirtualMachine`.

| Class (memory) | Fit | Refused at | Create time, each (s) | Refusal |
|---|---|---|---|---|
| tiny (2 GB) | 6 | 7th | 58, 54, 56, 56, 60, 59 | api, exit 8, `no room for a tiny machine` |
| small (4 GB) | 3 | 4th | 168, 39, 33 | api, exit 8 |
| large (8 GB) | 1 | 2nd | 39 | api, exit 8 |
| large + small | fits | + tiny | 52 (the small) | host, `insufficient_capacity`, project left in error |
| xl (16 GB) | 0 | | | not tried; 16 > 12 |

- Machine budget: 12 GB of the 18 GB appliance (two thirds). The api refused a total of 14 GB in three ways and accepted it once, and then the host refused that one.
- Memory cost on the Mac: the Virtualization.framework process held RSS 17.6 GB (footprint 9.6 GB) with three small machines running. It did not shrink when machines stopped. `vm_stat` free pages stayed near 60 to 130 MiB from the first machine on. So the per-machine cost on the Mac is not measurable with these instruments. Once the appliance has touched its memory, the Mac has given up three quarters of its RAM for as long as the appliance runs, whatever runs inside. Inside a machine, `free -m` shows 3905 MB total for a small and 7923 MB for a large.
- Extrapolation to a 16 GB mini (12 GB appliance, by the docs and `--memory` default). If the two-thirds rule holds: 8 GB for machines, so **1 large, or 2 small, or 4 tiny, never an xl**. own-hardware.md's 9.6 GB gives the same counts (a tiny more would need 10). Unverified on 16 GB hardware.
- Other times: host add to `online` 59 s (first) and 44 s (second host). First machine on a new host 84 s and 102 s, the base download included. Start of a stopped large 50 s and 37 s. Stop 42 s, a 17 s snapshot included. Snapshot of a running large 23 s (213.6 MB). Fork (`snapshots restore --as`) 58 s. Destroy of three machines 17 s. Host rm 10 s. SSH `ssh e2e-t1.repose true` 0.34 s.

## Value verdicts

**Is it better than SSH into the Mac plus tmux?** Partly, today. What it adds is real and measured. Isolated Ubuntu machines with sudo: 2 to 6 of them on a 24 GB mini. Snapshots in 23 s and forks in 58 s. Machines that came back after a 60 s network drop with their processes running (a tick written every 5 s ran through 17:15:15 to 17:16:15 with no gap). SSH through the gateway in 0.34 s, from anywhere, with no port forwarded. What breaks the comparison is the core loop. The agent did not run its prompt (M4). A menu change killed the agent (M5). A database took 10 minutes and then failed (M3). With tmux on the bare Mac, none of those three happen. Against SSH, the product wins on isolation and undo, and loses on "I left it working and it worked".

**Does it detract from hosted?** A customer who moves loses three things. Machines that keep memory across a server restart. Snapshots off the box: a dead Mac disk takes every machine and every snapshot with it, and M1 shows the CLI telling them otherwise. Hosted's start, about 6 s against 37 to 50 s here. They gain unmetered memory and disk up to the Mac, and no seat to wait for. Because no machine can move either way (M2), "try the Mac, keep hosted for the big one" is not a path today. The plans cannot be held together either (billing.md).

**How easy is migration?** It does not work in either direction (M2). The only path is a fresh machine and `git push`.

**Would I pay $10 a month?** Not today. I would after M1, M3, M4 and M6 are fixed. That means agents that run their prompt, a menu that finishes, honest snapshot lines, and a restart that brings the machines back on a headless mini. At that point $10 is a fair price for the gateway, snapshots, forks, notifications and the dashboard over hardware I already own. Plain tmux costs nothing and does not lose its agent.

**Is it worth selling?** Yes, but on a narrower claim. A 16 GB mini, the entry model, gives one large or two small machines. That is a single-developer product: "isolated, undoable agent machines on your own Mac", not a fleet. Sell it with the capacity printed on the page (one large or two small on 16 GB; three small or one large plus one small on 24 GB). Do not sell it until the P0 and the agent loop are fixed. Every refusal I hit was a capacity refusal, and a customer will hit them on day one.

## What is right, measured

- The install: `curl ... install.sh | sh` took 2 s and verified the signature. `repose host add e2e-mac` took 3 s of work, and the host was `online` 59 s after it started. The second host took 44 s. No password, no port, nothing on the router.
- The first machine took 84 s with the 2.9 GB base download inside it, close to the spark's 85 s. Later smalls and larges took 33 to 39 s.
- A real repository (pallets/itsdangerous, 1712 lines of Python, 677 commits) synced in 5.9 s. The agent, once its prompt was sent, ran 297 tests green in the machine.
- A network drop of 60 s (outbound 443 and UDP 51820 blocked with pf on en4) did not interrupt a process in a machine, and the host stayed `online`, since 60 s is under the 90 s threshold.
- After a reboot, `offline` appeared between 79 and 94 s after the shutdown (15 s samples), matching the documented 90 s. `repose ls` named the host and how long it had been quiet.
- Snapshot 23 s. Stop 42 s. A fork carried the uncommitted README edit and the Redis menu service (`repose-svc-redis.service active`, PONG).
- Accident answers that tell the customer what to do: `e2e-t1 is already running.`; `e2e-t1 is tiny and running; --size sizes a machine this command creates or starts. repose resize e2e-t1 --size large changes it, which restarts it.`; `You already have a host named e2e-mac.`; `Your plan takes one host. repose host ls lists what you have.`; `No terminal to confirm destroying on; pass --yes.`
- `repose host rm` on the Mac removed the appliance, the launchd plist and the 10 GB directory in 10 s, and stopped vfkit.
- The dashboard's Hosts page at 1440 and 390, light and dark, at 1x: one row, readable, `online`, size, machine count, Remove.

## Method and what could not run

- Everything ran on the Mac over `ops/dev/macmini ssh` as a customer would run it in a terminal there. Long commands ran detached on the Mac with per-line epoch stamps, and the seconds are differences of those stamps. Screenshots came from Playwright with nixpkgs Chromium on the dev box, using a minted dashboard session (deleted after).
- I read the landing page, own-hardware.md (live at repose.run/docs/own-hardware), install.md, billing.md and the relevant parts of agents.md and troubleshooting.md. I read no code to explain a fault.
- Price as a customer reads it: Own hardware is $10 a month for one host, with seven days free. It buys no hosted memory, and it cannot be switched to or from a hosted plan without cancelling. The owner's account is billing-exempt, so no plan gate fired. Every hint that sends a customer to `--host repose` (M9) is unverified on the Own hardware plan.
- Not run: Claude Code (no Claude login on this host; logging in as the owner was out of scope). The park feature: deliberately absent; the key cannot be created without a GUI session (2026-11-22); the launcher logged `key_unavailable` every minute. The host daemon's update channel: deliberately absent until the release-tag step (2026-11-22); the appliance's `repose-hostd-update` logged `curl: (22) ... 404`. The one-host limit: deliberately absent (ENG-171, the owner is deciding); I saw only its refusal text. A session that expires mid-command: with no refresh token my minted session simply ended (`Not logged in. Run repose login.` at 16:17 and 17:17). A real customer's CLI refreshes, so this is not a finding. M3's idle-machine falsifier and M5's trigger check: not run.
- The "launcher killed mid-create" accident: killing `repose host run` (SIGTERM) 12 s into a tiny's create stopped the appliance cleanly in 4 s. Restarting it 272 s later brought all three machines back `running`. The `repose run` that was waiting printed no more lines and left no exit status in my log. Unreproduced: my detached pipeline may have been cut, so I do not rank it.
- Evidence rule that bit me: twice my harness printed nothing for a long SSH command, and I almost read a refusal into it. The two snapshot ids (629c2acb and 4f8bd58a) show the first `create` had succeeded. I count only readings that came back.
- Teardown, 17:28: every machine destroyed (itsdangerous, e2e-t1, e2e-k3, and the errored e2e-fork, e2e-m2, e2e-m3, e2e-h2m, e2e-h2m2, e2e-r1). Hosts e2e-mac and e2e-mac3 removed (`You have no hardware of your own.`). vfkit not running. Removed from the Mac: `~/.config/repose` (credentials), `~/.ssh/repose` and the `~/.ssh/config` repose wrote, `~/e2e` (the clone and logs), `~/Library/Logs/repose`, and the pf anchor. Left: `~/.local/bin/repose` v0.1.56 from install.sh, which replaced the development build at `/usr/local/bin/repose` as the launch message asked. The owner's projects were not touched. pk2 (`mac-park3`) and live1g were already in `error` before I started.
