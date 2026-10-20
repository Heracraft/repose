---
title: Your own machine
description: Add a Linux box you own, what the install puts on it, and what a rewind of a project brings back.
section: Using repose
order: 18
status: experimental
---

A machine you own is a Linux box you can reach with `ssh`: the desktop at home, a VPS, the box under the stairs. [`repose machine add`](/docs/cli#machines-you-own) installs repose on it over that same `ssh`, and your projects run there with no account and no server of ours in the path.

What repose adds to a box you could already `ssh` into is undo. The project's files on the machine sit in a btrfs subvolume, and [`repose rewind`](/docs/cli#repose-rewind-project) puts them back as they were at a rewind point: the code, the files git ignores and the dev database together.

## What the box needs

- x86-64, systemd, and the btrfs module, which Debian, Ubuntu, Fedora and Arch all ship.
- `apt`, `dnf` or `pacman`.
- A login you can `ssh` into whose `sudo` needs no password. That login is your own account on the box; repose makes no user of its own, and nothing on this path is the `dev` user a [hosted machine](/docs/machine) has.
- Room for the undo store: 40 GB by default, `--undo-gb N` for another size.

The install stops at the first of these that is missing and names it.

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

From the distribution it installs what it needs and leaves the rest of the box alone: `btrfs-progs`, `tmux`, `git`, `curl`, `jq`, `rsync`, `ripgrep`, `python3`, `tar` and `openssl`.

The address is whatever `ssh` takes, a Tailscale address or a `~/.ssh/config` host included. A box behind NAT is yours to make reachable.

Four coding agents go on the box, from the same versions and the same checksums a [hosted machine](/docs/agents) gets: Claude Code, Codex CLI, opencode and pi. Gemini CLI publishes no Linux x86-64 binary, so the install names it and leaves it out. Each agent logs in on the box, in its own way.

## Run on it

```
$ cd ~/code/todo-app
$ repose run --machine homebox -p "finish the auth flow"
✓ Prepared todo-app on homebox  0.4s
✓ Synced 3 files and 1 commit  1.1s
✓ Took a rewind point  0.0s
```

The sync is the same one a [hosted machine](/docs/run-and-attach) gets: the
commits the box has not got, the files you have changed but not committed, and
the files git ignores. The checkout stays on that machine from then on, so
`repose run` in that folder needs no `--machine` again.

**A point is taken on every run, before the agent starts.** That is what
`repose rewind` goes back to, so the state a rewind returns to is the one the
agent was given, not an hour-old copy.

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

## What a rewind brings back

A rewind point is a read-only snapshot of the project's whole directory on the machine: the files git tracks, the files it ignores, the dev database, `node_modules`, and whatever else was written there. A rewind puts that directory back as the point holds it, so all of it comes back at once.

On a project of 37,218 files with a 200 MB database, measured on repose's own hardware: a point takes 0.13 s and 32 KiB. A rewind of that project with `src` deleted, ten packages gone from `node_modules` and 50 MB of the database overwritten takes 2.19 s, after which every file is back, the database is byte for byte what it was, and `git status` is clean.

A point costs only the blocks that change after it is taken. Rewrite 100 MB of a 200 MB database and the point holds 100 MB; leave the project alone and it holds 32 KiB. `repose rewind --list` prints what each one holds.

The project is stopped first. A process still writing in the project survives the swap and goes on writing into a directory nothing can reach any more, so `repose rewind` ends the project's agents and anything else with a file open under it, then swaps, and refuses if something will not stop. The rewind is itself undoable: a point of the files as they are now is taken before the swap.

## What a rewind does not bring back

- Anything outside the project's directory: packages installed with `apt`, edits to `/etc`, another project's files, Docker's images and volumes.
- Memory. The processes the rewind stopped do not come back where they were; an agent starts again from the files.

## Points are not a backup

Points live on that machine's disk, beside the project they belong to. The disk that dies takes them with it, and so does the box being reinstalled. git is the copy that leaves the box.
