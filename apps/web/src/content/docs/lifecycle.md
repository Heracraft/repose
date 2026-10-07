---
title: Projects and lifecycle
description: Check on projects, stop and start them, take and restore snapshots, destroy and bring back.
section: Using repose
order: 17
---

A project is one machine plus its disk, snapshots, secrets and configuration. The first `repose run` in a checkout creates it. After that, any checkout of the same repository, on any laptop you're logged in on, finds it by its git remote.

To act on a project from elsewhere, name it: `repose attach todo-app`, `repose stop todo-app`. For `repose run`, whose argument is the prompt, use `--project todo-app`.

## See what's running

```
$ repose ls
PROJECT   CLASS  STATE    UP     AGENTS               TODAY  MONTH
todo-app  large  running  2h14m  claude: working      2h14m  41h
api       xl     running  6h40m  2 agents: 2 working  6h40m  12h
web       small  stopped  -      -                    0h     3h
```

AGENTS shows the agent in the machine's tmux session with its state: `working`, `idle` or `needs_input` (waiting on a permission prompt). With several agents, it counts them by state. One you started by typing `claude` in the shell window counts while it runs there. Gemini counts only in a window named `gemini`, which is where `repose run` starts it. TODAY and MONTH are the hours the machine has run.

`repose ls -q` prints only the names, for scripts: `repose ls -q | xargs -n1 repose stop` stops everything.

For one project in detail, including which processes are listening on ports:

```
repose status todo-app
repose status todo-app --watch
```

The dashboard's project page shows the state, agents, SSH sessions and usage, plus events (newest 20, with Show older for the rest), snapshots and the last build. It doesn't list listening ports.

## Stop and start

```
$ repose stop todo-app
Stopped todo-app in 11s with a 2.1 GB snapshot.
Interrupted claude (working).

$ repose start todo-app
todo-app is running (large), ready in 9s.
```

The `Interrupted` line names the agents that were in the middle of a turn or waiting for an answer, as the machine's last sample showed them. Stopping ends every process and snapshots the disk (`--no-snapshot`, or unticking **Snapshot on stop** in the dashboard, skips that). Most of a stop's time is the snapshot, which grows with the data on the disk. The disk stays, with everything in `/home/dev`. A stopped project costs nothing; its disk counts toward your plan's [disk total](/docs/billing#what-a-plan-means) until `repose rm`. `repose run` in the checkout starts a stopped machine too. On a [herdr project](/docs/run-and-attach#herdr-instead-of-tmux), herdr resumes its agents at the next start.

`repose start` is also the fix for a project in the `error` state: it restarts the machine on its newest configuration. The dashboard's **Start** button is there only while a project is stopped.

## Idle machines

A machine is idle when it has been running for 24 hours with no SSH session, no tmux client and no agent working. A laptop herdr with the machine in its sidebar holds an SSH session open, so the machine is not idle while that herdr runs. An agent sitting at its prompt, finished or waiting for you, doesn't count as working. repose doesn't stop an idle machine; it tells you instead:

```
$ repose ls
PROJECT    CLASS  STATE    UP      AGENTS
todo-app   large  running  31h02m  claude: idle
todo-app: running for 26h with nobody attached
```

- `repose status` shows the same line, and the dashboard's project list shows the idle time under the state.
- `repose run` and `repose attach` in another project print one line naming it, once per idle stretch.
- You get one notification, by email and ntfy if you have them on ([Notifications](/docs/notifications)), naming the project as idle. You get another only after the machine has been used and gone idle again.

An idle machine costs nothing extra on a plan, but its memory counts against what your plan runs at once, so another machine can be refused until it stops ([Billing](/docs/billing)). When the server hasn't reported on the machine for 10 minutes, for example while it's unreachable, repose can't tell and says nothing.

## Snapshots

The disk is snapshotted every night while running, and whenever you stop. Take one yourself before something risky:

```
repose snapshots create
repose snapshots list
```

Snapshots from the last 7 days are kept, free, and the newest one is always kept. A snapshot holds the whole disk (checkout, home directory, logins made on the machine, installed tools) but not [secrets](/docs/secrets), which live only in memory, or your Claude Code login, which is kept outside the machines ([Agents](/docs/agents#log-in)).

To put a project back to a snapshot, stop it first. Stopping takes its own snapshot, so this can be undone:

```
repose stop todo-app
repose snapshots restore todo-app SNAPSHOT_ID
```

Or restore into a new project and leave the original alone:

```
repose snapshots restore SNAPSHOT_ID --as-new todo-app-old
```

The dashboard's snapshot list has **Create**, **Restore…** and **Restore as new…** too. **Restore…** works on a stopped project and asks you to type the project's name first, as **Destroy** does.

Each snapshot's SHA-256 is recorded when it's taken. A restore checks the stored snapshot against it before writing anything, and stops with `snapshot checksum mismatch` if they differ. Snapshots taken before 2026-10-03 have no recorded checksum and restore without the check.

If a restore over a project fails, the old disk is already gone, so the project shows `error` and `repose start` refuses with `the restore into it did not finish`. Run the restore again (any of the project's snapshots will do), or remove the project with `repose rm`.

## Destroy and restore

```
$ repose rm todo-app
Destroy todo-app? A final snapshot is kept for 30 days. [y/N] y
Destroying todo-app. Its final snapshot is kept for 30 days.
```

This deletes the machine and its disk and stops all charges for the project. It stops counting toward your [project cap](/docs/limits#projects) at once, while it's still `destroying`. `--yes` skips the question; `--wait` waits until it's done. `repose rm` was called `repose destroy`, and `repose ls` was `repose projects`; the old names still work. In the dashboard, **Destroy** asks you to type the project's name.

Within 30 days, bring it back, running, with its size, configuration and git remote:

```
repose ls --destroyed
repose restore todo-app
```

`--as NEW-NAME` restores under another name, and `--snapshot ID` picks an older snapshot. A restore started while the destroy is still running waits for it. After 30 days the snapshot is deleted.

The dashboard's project list has the same under **Recently destroyed**, with the date each can be restored until. It shows the 10 destroyed most recently; **Show more** lists the rest.

### Start over with a fresh machine

To throw a machine away and start again from your checkout, destroy it and run again:

```
$ repose rm -y
Destroying todo-app. Its final snapshot is kept for 30 days.
$ repose run
✓ Destroyed the old todo-app  14s
✓ Created todo-app (large)  0.4s
...
```

The name stays taken until the destroy finishes, so a `repose run` started meanwhile waits for it (`Waiting for the old todo-app to finish destroying`), then creates a new project with the same name and syncs your checkout into it. It waits the same way when the project being destroyed isn't this checkout's (one you restored without a remote, say) but holds the name `repose run` wants, instead of creating `todo-app-2`. `repose rm --wait && repose run` does the same in one line. The old machine's final snapshot is kept, and `repose ls --destroyed` still lists it. If the destroy fails, `repose run` says so and creates nothing.

## A second machine for the same repository

For an experiment that shouldn't touch your main project, create another one by name:

```
repose run --name todo-app-experiment
```

It gets your checkout, with its whole history and uncommitted work, like any first sync. Commands in the checkout still mean the original; reach the new one by name. This is also how to run several agents on one repository without them sharing a working tree.

`--name` always means the project with that name: if it exists, `run` uses it, and if not, `run` creates it. A name that belongs to another repository's project is refused, so one repository is never synced into another's machine.

The second machine has no git remote of its own, and your checkout's `repose` remote stays pointed at the original. To bring its work back, add a remote for it:

```
git remote add experiment \
  todo-app-experiment.repose:~/todo-app
git fetch experiment
```

In a directory with no git remote, such as your home directory, a plain `repose run` makes a machine named after the directory, and `repose run --name boxd` makes `boxd` there, or uses it if you have one. A later `repose run` in that directory without `--name` uses the machine last made there, and says which: `Using boxd, the machine last made in this directory.` A directory that has no machine yet takes the one you sync into with `repose sync PROJECT`.

## Temporary machines

`--temp` makes a new machine that is destroyed after 24 hours, with no snapshot:

```
$ cd ~/code/todo-app
$ repose run --temp
✓ Created tmp-k3f9 (large, temporary: destroyed Sep 29 14:02)  4s
Synced: 2 modified, 1 untracked (48 new commits)
Ready in 21s.
tmp-k3f9 is temporary: destroyed in 24h.

$ cd ~/Downloads
$ repose run --temp --name spike
✓ Created spike (large, temporary: destroyed Sep 29 14:05)  4s
Not a git repository, so nothing was synced.
```

- `--temp` always makes a new machine, named `tmp-` and four letters unless you pass `--name`. It never uses the checkout's project, and can't be combined with `--project`. Running it twice makes two machines.
- `--temp 3h` or `--temp 90m` gives it a shorter life, from 10 minutes to 24 hours. It's counted from when the machine was made.
- In a checkout it syncs as usual, uncommitted work included. In a directory that isn't a git repository it makes an empty machine. The checkout gets no `repose` git remote; fetch an agent's work with `git fetch tmp-k3f9.repose:~/todo-app BRANCH`, where `todo-app` is your checkout folder's name (the run prints it as `Checkout: ~/todo-app on the machine`).
- `run` and `attach` say how long it has left: `tmp-k3f9 is temporary: destroyed in 5h.` `repose ls` shows it in a `LEFT` column, there only while you have a temporary machine; `repose status` says `temporary: destroyed in 5h`. The dashboard shows it as temporary.
- If you're attached, or an agent is working, when the time runs out, the machine waits until nobody is attached and no agent is working, checking each minute, for up to a day. An agent sitting at its prompt doesn't count as working.
- You get a notification an hour before the end (for a machine made with more than an hour), and another when it's destroyed. See [Notifications](/docs/notifications).
- Exiting the last window of its tmux session destroys it at once: `tmp-k3f9 is temporary and its session has ended; destroying it.` Detaching (`Ctrl-b` `d`) doesn't. On Windows, or with `REPOSE_INPUT_PROXY=0`, the CLI can't see the session end, and the machine waits for its time to run out.
- A temporary machine always runs tmux, whatever `default_multiplexer` says. herdr opens a new shell when its last tab closes, so the session would never end. `--temp --multiplexer herdr` stops with an error, and so does `--multiplexer herdr` on a temporary machine until `repose keep` makes it a normal one.
- `repose rm` on it asks `Destroy tmp-k3f9? It is temporary: no snapshot is kept and it cannot be restored.` A temporary machine never appears in `repose ls --destroyed` and can't be restored.
- A temporary machine counts toward your [project cap](/docs/limits#projects) and plan while it exists, like any other.

To keep one after all:

```
$ repose keep tmp-k3f9
tmp-k3f9 is no longer temporary.
```

It's then a normal project, still reached by name, and gets snapshots like any other from then on.

## Fork a project

To have several agents try different approaches from the same starting point, each with a machine of its own, fork the project:

```
$ repose fork todo-app -n 3
Forked todo-app into 3 projects
from its snapshot of 2026-09-25 14:02 in 48s:
  todo-app-fork-1  running (large)
  todo-app-fork-2  running (large)
  todo-app-fork-3  running (large)
```

`repose fork` snapshots the project and restores the snapshot into new projects. Each copy starts with the same disk: the code and its uncommitted changes, installed dependencies, Docker images, logins made on the machine. It also gets the project's configuration and [secrets](/docs/secrets). Processes don't carry over; each copy boots fresh. The code is at the same path in every copy, `~/todo-app`. (Copying a machine whose checkout an earlier version of repose made gives `~/todo-app-fork-1`, a link to `~/todo-app`.)

`--prompt "..."` starts the agent in every copy with the same prompt. To give each copy its own prompt, attach to it and type it, or run `repose run --project todo-app-fork-2 "..."`, which leaves the copy's checkout as it is.

The original keeps running and is still the project `repose run` uses in your checkout. Reach the copies by name: `repose attach todo-app-fork-2`. To keep one copy's work, commit it there and fetch it into your checkout with a remote for that copy:

```
git remote add fork-2 todo-app-fork-2.repose:~/todo-app
git fetch fork-2
git merge fork-2/main
```

Pushing a branch from the copy (`git push origin HEAD:try-2`) works too. Destroy the copies you don't need with `repose rm todo-app-fork-1`.

Each copy is a project: it counts toward the [100 projects an account can have](/docs/limits#projects) and your plan's disk, and toward the plan's memory while it runs. If the copies would take you past 100, `repose fork` creates none of them. `--size small` makes copies that take less of that memory and disk; `--name` changes their names.

## Logs and events

```
repose logs               # boot and kernel output
repose logs --kind build  # the last configuration build
repose logs --kind ops    # create, start, stop, snapshot history
repose events             # agent and project events, last 24 hours
```

Your applications' output isn't collected; it stays on the machine.

## States

| State                  | Meaning                                                                       |
| ---------------------- | ----------------------------------------------------------------------------- |
| `creating`, `building` | A new project's disk and environment are being made.                          |
| `starting`             | Booting.                                                                      |
| `running`              | On. Compute is counted by the minute.                                         |
| `stopping`, `stopped`  | Shutting down, or off with the disk kept.                                     |
| `restoring`            | A snapshot is being written to the disk.                                      |
| `destroying`           | Being deleted, final snapshot first.                                          |
| `error`                | Something failed. `repose status` says what; `repose start` usually fixes it. |
