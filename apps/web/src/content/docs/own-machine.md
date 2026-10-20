---
title: Your own machine
description: Add a Linux box you own, what the install brings and what it leaves to you, what an agent on it can reach, and what a rewind of a project brings back.
section: Using repose
order: 18
status: experimental
---

A machine you own is a Linux box you can reach with `ssh`: the desktop at home, a VPS, the box under the stairs. [`repose machine add`](/docs/cli#machines-you-own) installs repose on it over that same `ssh`, and your projects run there with no account and no server of ours in the path.

What repose adds to a box you could already `ssh` into is undo. The project's files on the machine sit in a btrfs subvolume, and [`repose rewind`](/docs/cli#repose-rewind-project) puts them back as they were at a rewind point: the code, the untracked files and the dev database together.

## What the box needs

- x86-64, systemd, and the btrfs module, which Debian, Ubuntu, Fedora and Arch all ship.
- `apt`, `dnf` or `pacman`.
- A login you can `ssh` into whose `sudo` needs no password. That login is your own account on the box; repose makes no user of its own, and nothing on this path is the `dev` user a [hosted machine](/docs/machine) has.
- Room for the undo store: 40 GB by default, `--undo-gb N` for another size.

The install stops at the first of these that is missing and names it.

### sudo without a password

The install runs as that login and reaches for `sudo -n` to mount the undo store, write its systemd units and put the agents on the box's `PATH`. A `sudo` that asks for a password stops it there: the `ssh` session it runs in has no terminal to type the password into. On the box, as a user who already has `sudo`:

```
$ sudo visudo -f /etc/sudoers.d/repose
me ALL=(ALL) NOPASSWD:ALL
```

`me` is the login you will give `repose machine add`. Use `visudo` rather than an editor: it parses the file before saving it, and a sudoers file that does not parse leaves nobody on the box able to use `sudo` at all.

## Add it

```
$ repose machine add homebox --ssh me@192.168.1.40
Installing on me@192.168.1.40 ...
✓ Checked the box (Ubuntu 24.04, x86_64, systemd, btrfs)  1.2s
✓ Provisioned 40G of undo space (btrfs at /var/lib/repose/undo)  0.4s
✓ Installed claude-code 2.1.283  31.3s
Added homebox.
```

The install prints a line per step, and each step is written to change nothing that is already in place: running `repose machine add` again on the same box reinstalls nothing and says so against every step.

The address is whatever `ssh` takes, a Tailscale address included. A `Host` entry in your `~/.ssh/config` works too, and it is resolved once, when you add the machine: the host name, port, login, key and any `ProxyJump` it stands for are what the laptop keeps from then on, and editing that entry later does not follow. A box at a new address is `repose machine add` with the same name: the line says it moved, it installs there, and the projects recorded on it stay recorded.

A box behind NAT is yours to make reachable.

Four coding agents go on the box, from the same versions and the same checksums a [hosted machine](/docs/agents) gets: Claude Code, Codex CLI, opencode and pi. Gemini CLI publishes no Linux x86-64 binary, so the install names it and leaves it out. Each agent logs in on the box, in its own way.

## Which commands work on it

| Command | On a machine you own |
| ------- | -------------------- |
| [`repose run`](/docs/cli#repose-run-project) | sends your current work, takes a rewind point, starts the agent in tmux |
| [`repose attach`](/docs/cli#repose-attach-project-checkout-window) | the project's tmux session on the box |
| [`repose sync`](/docs/cli#repose-sync-project) | sends your current work without starting an agent |
| [`repose ps`](/docs/cli#repose-ps-project-window) | the project's windows and the agents in them |
| [`repose rewind`](/docs/cli#repose-rewind-project) | the rewind points, and the swap back to one |
| [`repose machine`](/docs/cli#machines-you-own) | add a box, list the ones you have, forget one |
| [`repose ls`](/docs/cli#repose-ls) | your projects, the ones on machines you own among them |

`repose snapshots`, `resize`, `stop`, `start`, `rm`, `config`, `secrets`, `browser`, `logs` and `reply` do not work on such a machine: each acts on a machine repose creates, holds state for and bills you for. A box you own is none of those things: it is already running, it is your disk, and no server of ours holds its secrets or its logs.

## Run on it

```
$ cd ~/code/todo-app
$ repose run --machine homebox -p "finish the auth flow"
✓ Prepared todo-app on homebox  0.4s
✓ Synced 3 files and 1 commit  1.1s
✓ Took a rewind point  0.0s
```

The sync is the same one a [hosted machine](/docs/run-and-attach) gets: the commits the box has not got, the files you have changed but not committed, the untracked files, and the gitignored `.env` and `.env.*` files. Other gitignored files, build output and caches and `node_modules` among them, stay on your laptop, as they do on a hosted machine ([What doesn't](/docs/sync#what-doesnt)). The checkout stays on that machine from then on, so `repose run` in that folder needs no `--machine` again.

## What the install puts on the box

| Path | What it is |
| ---- | ---------- |
| `/opt/repose/bin` | `repose-agent`, the command your laptop runs over `ssh`, and the coding agents |
| `/var/lib/repose/undo.img` | the undo store, a sparse file that holds only what is in it |
| `/var/lib/repose/undo` | where that store is mounted, by `var-lib-repose-undo.mount` |
| `/var/lib/repose/undo/projects` | the projects, one btrfs subvolume each |
| `/etc/repose/machine.json` | the machine's name and id |
| `/etc/repose/agents.json` | which agent version is installed |
| `/etc/profile.d/repose.sh` | puts `/opt/repose/bin` on your `PATH` |

The store is a file rather than a disk of its own because it costs almost nothing to be one: btrfs in a sparse file on the filesystem the box already has is 3 percent slower on a long sequential write and no slower on small ones, measured.

`repose machine rm` forgets the machine on the laptop and leaves all of this where it is.

## What the box does not get

From the distribution the install takes what repose's own agent and the coding agents need, and nothing else: `btrfs-progs`, `tmux`, `git`, `curl`, `jq`, `rsync`, `ripgrep`, `python3`, `tar`, `openssl` and `gawk`. It also installs `gh` where the distribution has it, and names it and carries on where it does not, as Debian 12 does not; without `gh` the carried GitHub login still lets git push, and `gh` commands do not work.

It brings no language runtime, no package manager for your project and no database. `repose run` sends your checkout, takes a point and starts an agent in it; it does not read your `package.json`, `Gemfile` or `pyproject.toml`, installs nothing from them and builds nothing. Node, Python and its packages, Postgres, Redis, Docker: whatever your project needs to run, you install on the box yourself, once, as you would on any machine you own, and it stays there for every project after.

A [hosted machine](/docs/machine) reads the project and brings its toolchain; this path does not. Where it goes next is Nix as a single user on the box, with a profile per project, so a project's tools come from the project.

## What an agent on it can reach

There is no hypervisor between the agent and the box. That is the shape, not an omission: the point of this path is that the box is yours and nothing of ours sits in front of it. An agent you give full permissions to, with `--dangerously-skip-permissions` or its equivalent, can therefore do everything that login can do, which is:

- root on the box, through the passwordless `sudo` the install requires.
- the `ssh` keys and tokens in that login's home, and so whatever they reach.
- every other project on the box, repose's and not repose's.
- the rewind points themselves. They are subvolumes under `/var/lib/repose/undo`, deletable by that same login, which is what lets repose collect them without root.

So the box is the boundary, and choosing it is the decision: a box whose contents you would be willing to lose, not one that holds a production credential or somebody else's data. A rewind undoes a project's files. It does not undo the box.

## What a rewind brings back

A rewind point is a read-only snapshot of the project's whole directory on the machine: the files git tracks, the files it ignores, the dev database, `node_modules`, and whatever else was written there. A rewind puts that directory back as the point holds it, so all of it comes back at once.

**A point is taken on every `repose run` and every `repose sync`, and every 30 minutes while the project is on the box.** The state a rewind returns to is the one the agent was given, not an hour-old copy.

On a project of 37,218 files with a 200 MB database, measured on repose's own hardware: a point takes 0.13 s and 32 KiB. A rewind of that project with `src` deleted, ten packages gone from `node_modules` and 50 MB of the database overwritten takes 2.19 s, after which every file is back, the database is byte for byte what it was, and `git status` is clean.

A point costs only the blocks that change after it is taken.

Points are collected as the store fills: once an hour, the oldest go until the store is back under 85 percent, and a project's newest point is never one of them.

The project is stopped first. A process still writing in the project survives the swap and goes on writing into a directory nothing can reach any more, so `repose rewind` ends the project's agents and anything else with a file open under it, then swaps, and refuses if something will not stop.

The rewind is itself undoable. Before the swap it takes a point of the files as they are at that moment and prints that point's name; it is the project's newest point afterwards, at the top of `repose rewind --list` with `rewind` in its REASON column. A plain `repose rewind` skips those points, since going back to one restores what the last rewind escaped, so `repose rewind --to POINT` with the name the result line printed is how you undo a rewind.

## What a rewind does not bring back

- Anything outside the project's directory: packages installed with `apt`, edits to `/etc`, another project's files, Docker's images and volumes.
- Memory. The processes the rewind stopped do not come back where they were; an agent starts again from the files.

## Points are not a backup

Points live on that machine's disk, beside the project they belong to. The disk that dies takes them with it, and so does the box being reinstalled. git is the copy that leaves the box.
