---
title: Run a swarm
description: One conductor session on the machine splits the work, launches workers in their own worktrees, merges what comes back and runs the checks. This is how repose itself was built.
section: Tutorials
order: 24
---

A conductor is one more agent whose only job is to run the others. We built repose with one in the main checkout and four to six workers in worktrees, for weeks.

## The roles

- **The conductor** is one long-lived Claude Code session in the checkout on the machine (`~/todo-app`, on `main`). It never builds a task itself. It cuts the work into pieces that touch different files, launches a worker per piece, merges finished branches, runs the checks on the merged result, and fixes anything that blocks more than one worker.
- **A worker** is an agent in its own [git worktree](/docs/run-and-attach#several-agents-separate-trees) on its own branch. It reads the docs, builds its whole piece, commits on its branch, and stops with a report. It never merges and never pushes.
- **You** answer questions nobody else can, and run anything that spends money or leaves the machine.

## Start the conductor

From your repository folder on the laptop, with a prompt that sets the role:

```
repose run -p "You are the conductor for this repository. \
Read docs/PLAN.md. Split the open items into tasks that touch \
different files. For each task, launch a worker agent in its own \
worktree with a prompt naming the files it may touch and what to \
report. As workers finish, merge their branches onto main one at \
a time, run npm test after each merge, and fix or reassign \
anything red. Commit on main. Keep a line per task in \
docs/STATUS.md: claimed, done, blocked, with what is done and \
what is not. Don't push. Ask me only when a decision needs me."
```

The conductor merges in dependency order: a task that defines an interface before the tasks that use it.

To see each worker in its own tmux window, launch them from your laptop instead, one `repose run -d --worktree -p "..."` per task, and give the conductor the branch names to merge.

## Watch it run

```
repose ps
```

shows the conductor's window and, if you launched workers as windows, theirs. You get a [notification](/docs/notifications#what-youll-get) when the conductor stops or asks; [`repose questions` and `repose reply`](/docs/notifications#agents-can-message-you-and-ask-questions) handle the asking without an attach. The conductor's STATUS file is what you read in the morning.

From your laptop:

```
git fetch repose
git log --oneline main..repose/main   # what the conductor merged
git branch -r | grep repose/worktree- # the workers' branches
```

## The rules that came out of doing it

Put them in the conductor's prompt or in the repository's `CLAUDE.md`, where every worker reads them.

- **Workers claim before they build and record when they stop.** Keep a STATUS file with one line per task, appended by whoever is working. The next session reads it first.
- **Merge one branch at a time.** Run the checks after each merge.
- **Assign shared numbers up front.** Anything workers number in parallel (decision entries, migration files, changelog lines) collides. The conductor hands out ranges in the launch prompt.
- **Append-only files union-merge.** Mark STATUS and decision logs with `merge=union` in `.gitattributes`, so two workers appending never conflict.
- **Done needs evidence.** Each worker's report names the evidence for each item in its checklist: "the migration is registered, here's the row".
- **Workers never push, never deploy, never touch another worktree.** The conductor merges; you push.
- **Snapshot before a big round.** [`repose snapshots create`](/docs/lifecycle#snapshots) before the conductor starts merging means a bad round is one restore away, worktrees and all.

## When it gets big

- **Memory.** Six agents plus their test runs want more than a small machine. [`repose resize --size large`](/docs/machine#changing-the-size) or `xl` before a round starts.
- **Nix and other heavy builds.** Have the conductor serialize them (one lock file everyone `flock`s) or the machine spends the round swapping.
- **A second machine for a second front.** [`repose fork`](/docs/lifecycle#fork-a-project) copies the machine's disk into new projects; a conductor on a copy can take a different branch of the plan.
- **You are the rate limit.** The conductor should batch its questions, and needs a notification channel you'll see.
