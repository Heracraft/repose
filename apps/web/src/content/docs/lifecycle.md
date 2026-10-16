---
title: Projects and lifecycle
description: Check on projects, stop and start them, take and restore snapshots, destroy and bring back.
section: Using repose
order: 17
---

A project is one machine plus its disk, snapshots, secrets and configuration. The first `repose run` in a checkout creates it. After that, any checkout of the same repository, on any laptop you're logged in on, finds it by its git remote.

To act on a project from elsewhere, name it: `repose attach todo-app`, `repose stop todo-app`, `repose run todo-app`.

## See what's running

```
$ repose ls
PROJECT     SIZE   STATE    UP     AGENTS
todo-app *  large  running  2h14m  claude: working
api         large  running  6h40m  codex: needs input 4h
web         small  stopped  -      -
Plus: 16 of 16 GB running, 41.3 of 250 GB disk, 212 of 500 GB egress
this month
```

`*` marks the project a command run in this folder acts on. AGENTS gives each agent its state and how long it has been waiting ([Agent states](/docs/agents#agent-states)). The last line is your plan ([Pricing](/docs/billing)). [`repose ls`](/docs/cli#repose-ls) has the rest: every column, and the lines that show under the table when something needs you.

[`repose status`](/docs/cli#repose-status-project) shows one project in detail: each agent by its window, the commits sitting on the machine that your laptop hasn't fetched, and what's listening on which port.

```
repose status todo-app
repose status todo-app -f
```

The dashboard's **Projects** page lists the same projects, with **Stop** on a running one's row and **Start** on a stopped one's, and **Recent activity** under the list. A project's own page has its agents, its snapshots and its events, which filter to agents, errors or the machine. It lists no listening ports.

## Stop and start

```
$ repose stop todo-app
todo-app has claude (working). Stopping ends it. Stop todo-app?
[y/N] y
Fetched 2 commits on repose/main.
Stopping todo-app; 1 file not committed.
```

A stop ends every process on the machine. It asks first when the machine's last sample showed an agent working or waiting for an answer, and it fetches that agent's commits before the machine goes. That fetch runs `git fetch repose` in each of the project's checkouts on this laptop, whatever folder you run it in. The line ends with what stays behind: commits your checkouts haven't fetched, files nobody committed, or that the machine stopped answering before any of it could be read. [`repose stop`](/docs/cli#repose-stop-project) has the flags and the exact lines.

The command comes back once the stop is under way, not when it's done. The snapshot and the shutdown run on the server, so you can close the laptop. Most of a stop is its snapshot, which grows with what the disk holds; `--no-snapshot`, or unticking **Snapshot on stop** in the dashboard, stops without one. A stop that fails after the command returned leaves the project in `error` with the reason and [notifies you](/docs/notifications), and `repose stop` tries again.

Your plan's memory is given back the moment the project reads `stopping`. The disk is kept, and a stopped project keeps counting toward [your plan's disk](/docs/billing#what-a-plan-means) until `repose rm`.

```text
$ repose start todo-app
todo-app is running (large), ready in 9s.
```

Each agent's window opens again with the conversation it was in ([When the machine stops](/docs/run-and-attach#when-the-machine-stops)). `attach`, `run`, `ssh` and `code` start a stopped machine too; during a stop they wait for it to finish, say so if it failed, and start the machine again. `rm`, `fork`, `resize`, `snapshots create`, a restore and a config change wait the stop out and then do their work.

`repose start` is also the way out of the `error` state: it restarts the machine on its newest configuration. A project in `error` has **Restart** on its own page.

## When a machine's server restarts

A machine whose server restarts comes back as it was, usually in a couple of minutes: programs, tmux windows and agents carry on where they were. Servers restart for maintenance and after faults. An attached terminal reconnects itself; connections that were open drop, SSH sessions and forwarded ports included. The machine's clock is set when it comes back. While the server restarts, the machine's memory, [secrets](/docs/secrets) included, is kept as an encrypted image on that server and removed once the machine is back.

A machine that can't come back that way stops instead. Its disk is kept; processes and memory are lost, the agent's turn with them ([When the machine ends without a stop](/docs/run-and-attach#when-the-machine-ends-without-a-stop)). That's what happens if the server loses power or crashes, if there isn't time or room to keep the memory, or if the machine is changed while the server is down.

When the server is back, repose starts each machine that stopped and was running or starting when it went down, three at a time per server, so yours is usually running again within a few minutes. It boots the system the machine was running; a configuration built and waiting for the next start is applied at the one after.

A machine isn't started again when:

- you stopped, removed, restored or resized it while the server was down, or started it yourself since;
- more than 6 hours have passed since it stopped;
- it's [temporary](#temporary-machines) and its time ran out;
- a `repose start` of yours would be refused too: your account is suspended, your plan's memory is taken by the machines already running, or the machine is on hold ([Limits](/docs/limits#what-isnt-allowed)). A failed payment doesn't keep it stopped ([Billing](/docs/billing#if-a-payment-fails));
- its start fails, after one more try when the machine doesn't answer in time.

If any machine stopped, you get one [notification](/docs/notifications) per server restart, with a line for each:

```text
The server running your machines restarted; it was back at 05:46 UTC.
api: running again.
todo-app: running again; its last agent turn was interrupted.
```

A machine that wasn't started gets `not started:`, the reason, and `repose start NAME`. Machines you stopped or removed meanwhile aren't listed. One still waiting 10 minutes after the server came back is listed as `waiting to start`, and there's no second notification when it starts. `repose events` shows `server restarted` on each machine that stopped.

About 90 seconds after the server stops answering, repose marks it as down. From then on `repose run`, `attach`, `start`, `ssh` and `code` wait for it with `Waiting for api's server to come back`, then go on: a machine that was running is there as it was, and a stopped one starts. After 10 minutes they exit 5 with ``api's server has not answered for 10m. `repose status api` shows when it is back.`` In those first 90 seconds a connection to a machine that was running fails with `did not answer ssh`, and `start` says it is running; run the command again.

## Unused machines

A running machine is unused once 24 hours pass with no SSH session, no tmux client and no agent `working` or `running`. The 24 hours run from the last of those, or from the boot if there's been none. An agent finished (`idle`) or waiting on you (`needs input`) doesn't count. The machine people forget is the one whose agent has sat at its prompt since `repose run` started it. An agent `running` counts, since repose can't tell whether it's mid-task. A laptop [herdr](/docs/run-and-attach#herdr-instead-of-tmux) counts too, since it keeps an SSH session open to every machine in its sidebar.

repose never stops an unused machine; it tells you instead, on every `repose ls`:

```
$ repose ls
PROJECT   SIZE   STATE    UP      AGENTS
todo-app  large  running  31h02m  claude: needs input 3h
todo-app: unused for 26h; claude needs input 3h
```

- `repose status` prints the line too, and the dashboard's project list puts `unused` and the time under the state.
- `repose run` and `repose attach` on another project print `Running and unused: todo-app (26h).`, once per unused stretch.
- One [notification](/docs/notifications#what-youll-get) per unused stretch. Use the machine and the next stretch gets its own.

An unused machine's memory still counts against what your plan runs at once, so a start elsewhere can be refused until you stop it ([Memory](/docs/billing#what-a-plan-means)). When the machine hasn't reported for 10 minutes, or its server isn't answering, repose can't tell and says nothing.

## Snapshots

The disk is snapshotted every night while running, and whenever you stop. Take one yourself before something risky:

```
repose snapshots create
repose snapshots list
```

A snapshot holds the whole disk: the checkout, the home directory, logins made on the machine, installed tools. Not [secrets](/docs/secrets), and not your [Claude Code login](/docs/agents#log-in). Snapshots from the last 7 days are kept, and the newest one always.

To put a project back to a snapshot, stop it first:

```
repose stop todo-app
repose snapshots restore todo-app SNAPSHOT_ID
```

The question names the snapshot it would write and whether anything keeps the disk as it is now. The stop's own snapshot usually does, so the restore is itself undoable. After `repose stop --no-snapshot` or `repose resize --size`, whose stop takes none, it says instead that what changed since the newest snapshot is lost for good; a stopped project can still be snapshotted, which is the way back from that. The restore starts the machine on the restored disk and ends with `Restored todo-app; it is running (large).`

Or restore into a new project and leave the original alone:

```
repose snapshots restore SNAPSHOT_ID --as todo-app-old
```

The new project boots with its `shell` window alone; agent windows reopen only in the project they were recorded in.

The dashboard's snapshot list has **Create snapshot**, **Restore…** and **Restore as new…**, and shows the newest 8 with **Show all** for the rest. **Restore…** wants the project stopped and asks you to type its name, as **Destroy** does.

A restore checks what it reads against the SHA-256 recorded when the snapshot was taken. If they differ it stops with `snapshot checksum mismatch` and deletes the disk it was writing, so nothing boots on it. A snapshot taken before 2026-10-03 has no digest and restores unchecked.

A restore over a project destroys the old disk before writing the new one, so one that fails leaves the project in `error` with nothing to boot, and `repose start` refuses with `todo-app has no usable volume because the restore into it did not finish`. `repose stop todo-app` and restore over it again, or restore into a new project with `--as NAME`; `repose rm` removes it. A fork copy whose disk copy failed has no snapshot of its own, so `start` says to remove it and fork the source again.

## Destroy and restore

```
$ repose rm todo-app
Destroy todo-app? A final snapshot is kept for 30 days. [y/N] y
Destroying todo-app.
```

`repose rm` deletes the machine and its disk. The command returns once the destroy is under way; the project reads `destroying` until it's gone.

Within 30 days, bring it back, running, with its size, its configuration and its git remote, so your checkout finds it again:

```
repose ls --destroyed
repose restore todo-app
```

`--as NEW-NAME` restores under another name, and `--snapshot ID` picks an older snapshot. A restore started while the destroy is still running waits for its final snapshot. If a live project holds the name, the restore asks you for another. A live project holding the git remote keeps it, and the restored one is reached by name.

The dashboard's project list has the same under **Recently destroyed**, with the date each can be restored until, and **Show more** for the rest.

### Start over with a fresh machine

Destroy the machine, then run again in your checkout:

```
$ repose rm -y
Destroying todo-app. Its final snapshot is kept until 2026-11-07.
$ repose run
✓ Destroyed the old todo-app  14s
✓ Created todo-app (large)  0.4s
...
```

The name stays taken until the destroy finishes, so a `repose run` started meanwhile waits for it (`Waiting for the old todo-app to finish destroying`) and then creates a new project with the same name. It waits the same way for a project being destroyed that holds the name it wants but isn't this checkout's, instead of creating `todo-app-2`. `repose rm --wait && repose run` does the same in one line. The old machine's final snapshot is kept, and `repose ls --destroyed` still lists it, as `todo-app (name in use)`. If the destroy fails, the project lands in `error` and `repose run` creates nothing; `repose rm` tries again.

## A second machine for the same repository

One repository can have several machines, one per name:

```
repose run todo-app-experiment
```

It gets your checkout, with its whole history and uncommitted work, like any first sync. Commands in the checkout still mean the original.

`repose run NAME` always means the project with that name: if it exists, `run` uses it, and if not, `run` creates it. A name that belongs to another repository's project is refused, so one repository is never synced into another's machine.

Your checkout's `repose` remote stays pointed at the original, and the run adds a remote named after the second machine, so its work comes back with:

```
git fetch todo-app-experiment
```

`repose rm todo-app-experiment` removes that remote; branches you fetched from it stay.

In a folder with no git remote, such as `~/Downloads/job search`, a plain `repose run` makes a machine named after the folder, and `repose run boxd` makes `boxd` there, or uses it if you have one. A later plain `repose run` in that folder uses the machine last made there, and says which: `Using boxd, the machine last made in this folder.` A folder that has no machine yet takes the one you sync into with `repose sync PROJECT`. `repose rm` forgets every folder that used the machine. `Ctrl-C` between a machine being created and the attach leaves the machine on your account without tying the folder to it, and names it: ``Interrupted. job-search was created and stays on your account; `repose rm job-search` removes it.``

### From your home folder

Your home folder, and every folder above it, belongs to no machine. There, `repose run NAME` creates NAME or uses it, starting it if it is stopped, and syncs nothing; `repose attach NAME` starts it too:

```
$ cd ~
$ repose run scratch -p "try the new API"
$ repose run notes
$ repose attach notes
$ repose run --temp
```

A plain `repose run` there stops with ``Your home folder is not a project. cd into one, or run `repose run NAME` or `repose run --temp`.`` Other commands need the machine's name there. `repose sync` and `run --on` stop with an error. A home folder that is a git repository, such as a dotfiles repository, is never synced and gets no `repose` git remote.

## Temporary machines

`--temp` makes a new machine that is destroyed after 24 hours, with no snapshot:

```
$ cd ~/code/todo-app
$ repose run --temp
✓ Created tmp-k3f9 (large, temporary until Sep 29 14:02)  4s
Synced: 2 modified, 1 untracked (48 new commits)  3.2s
Ready in 21s.
tmp-k3f9 is temporary: destroyed in 24h.

$ cd ~/Downloads
$ repose run --temp spike
✓ Created spike (large, temporary until Sep 29 14:05)  4s
Not a git repository, so nothing was synced.
```

- `--temp` always makes a new machine, named `tmp-` and four characters unless you name it (`repose run --temp spike`). It never uses the checkout's project, and can't be combined with `--project`. Running it twice makes two machines.
- `--temp 3h` or `--temp 90m` gives it a shorter life, from 10 minutes to 24 hours (`1d` is 24 hours). It's counted from when the machine was made. A word after `--temp` that is a number and a unit is its duration wherever it stands, so `repose run spike --temp 3h` works.
- In a checkout it syncs as usual, uncommitted work included. In a directory that isn't a git repository it makes an empty machine. The checkout gets no `repose` git remote; fetch an agent's work with `git fetch tmp-k3f9.repose:~/todo-app BRANCH`, where `todo-app` is your checkout folder's name (the run prints it as `Checkout: ~/todo-app on the machine`).
- `run` and `attach` say how long it has left: `tmp-k3f9 is temporary: destroyed in 5h.` `repose ls` shows it in a `LEFT` column, there only while you have a temporary machine; `repose status` says `temporary: destroyed in 5h`. The dashboard shows it as temporary.
- If you're attached, or an agent is working, when the time runs out, the machine waits until nobody is attached and no agent is working, checking each minute, for up to a day. An agent sitting at its prompt doesn't count as working. On herdr only a working agent holds it.
- You get a [notification](/docs/notifications) an hour before the end (for a machine made with more than an hour), and another when it's destroyed.
- On tmux, exiting the last window of its session destroys it at once: `tmp-k3f9 is temporary and its session has ended; destroying it.` Detaching (`Ctrl-b` `d`) doesn't. Before the destroy the CLI checks the machine's checkout, and keeps the machine until its time runs out when it holds work your laptop doesn't: changed or new files beyond what the sync wrote, in the checkout or one of its worktrees, or commits on any branch that are neither what the sync sent nor pushed. It says `tmp-k3f9 has 2 commits and 1 changed file your laptop does not; it stays until 14:02.` If the check can't run, the machine stays too. With `REPOSE_NO_INPUT_PROXY=1`, the CLI can't see the session end, and the machine waits for its time to run out.
- On herdr, closing its tabs doesn't destroy it. It goes when its time runs out.
- `repose rm` on it asks `tmp-k3f9 is temporary: destroying it keeps no snapshot and it cannot be restored. Destroy tmp-k3f9?` A temporary machine never appears in `repose ls --destroyed` and can't be restored.

To keep one after all:

```
$ repose keep tmp-k3f9
tmp-k3f9 is no longer temporary.
```

It's then a normal project, and its snapshots stop expiring with it. To give it more time and keep it temporary, add a duration, from `10m` to `24h`, counted from now:

```
$ repose keep tmp-k3f9 3h
tmp-k3f9 is temporary until Sep 29 17:02.
```

## Fork a project

`repose fork` copies a project's disk as it is at that moment into new ones, one per agent trying a different approach:

```
$ repose fork todo-app -n 3
✓ Copied todo-app's disk into todo-app-fork-1  0.4s
✓ Started todo-app-fork-1  14s
✓ Started todo-app-fork-2  15s
✓ Started todo-app-fork-3  15s
Forked todo-app into 3 projects in 16s:
  todo-app-fork-1  running (large)
  todo-app-fork-2  running (large)
  todo-app-fork-3  running (large)
Added the git remotes todo-app-fork-1, todo-app-fork-2 and
todo-app-fork-3.
```

The copies are made on the project's own server and take seconds whatever the disk holds. When that server has no room for them, `fork` snapshots the project first (`✓ Snapshot of todo-app taken`) and restores the copies from the snapshot on whichever servers have room. That snapshot is listed with reason `fork` and deleted a day later. Each copy starts with the same disk: the code and its uncommitted changes, installed dependencies, Docker images, logins made on the machine. It also gets the project's configuration and [secrets](/docs/secrets). Processes don't carry over, and no agent window reopens ([Snapshots](#snapshots)). The code is at the same path in every copy, `~/todo-app`. (Copying a machine whose checkout an earlier version of repose made gives `~/todo-app-fork-1`, a link to `~/todo-app`.)

`--prompt "..."` starts the agent in every copy with the same prompt. To give each copy its own prompt, attach to it and type it, or run `repose run todo-app-fork-2 -p "..."`, which leaves the copy's checkout as it is.

The original keeps running and is still the project `repose run` uses in your checkout. Reach the copies by name: `repose attach todo-app-fork-2`. Run in your checkout, `fork` adds a git remote for each copy, named after it, as `run` adds `repose` for the project. To keep one copy's work, commit it there and fetch it:

```
git fetch todo-app-fork-2
git merge todo-app-fork-2/main
```

Each copy is a project: it counts toward the [100 projects an account can have](/docs/limits#projects), toward your plan's disk by what it holds (at first what the project holds), and toward the plan's memory while it runs. If the copies would take you past 100, or what they hold past your plan's disk, `repose fork` creates none of them. If running them all beside what runs now needs more memory than your plan has, it says so before copying anything and exits with code 7. `--no-start` creates the copies stopped, so they take no memory until you `repose start` one.

## Logs and events

```
repose logs               # what a boot that failed printed
repose logs --kind build  # the last configuration build
repose logs --kind ops    # every operation, and how it ended
repose events             # agent and project events, last 24 hours
```

Your applications' output isn't collected; it stays on the machine.

## States

| State                     | Meaning                                                                                     |
| ------------------------- | ------------------------------------------------------------------------------------------- |
| `creating`, `building`    | A new project's configuration and disk are being made.                                      |
| `starting`                | Booting.                                                                                    |
| `running`                 | On.                                                                                         |
| `stopping`, `stopped`     | Shutting down, or off with the disk kept.                                                   |
| `restoring`               | A snapshot is being written to the disk, or a [fork](#fork-a-project)'s copy is being made. |
| `destroying`, `destroyed` | Being deleted, or gone ([Destroy and restore](#destroy-and-restore)).                       |
| `error`                   | Something failed. `repose status` says what ([Stop and start](#stop-and-start)).            |
