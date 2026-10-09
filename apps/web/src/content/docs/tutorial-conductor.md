---
title: Run a swarm
description: One conductor session on the machine splits the work, launches workers in their own worktrees, merges what comes back and runs the checks. This is how repose itself was built.
section: Tutorials
order: 24
---

A conductor is one more agent whose only job is to run the others. repose was built this way: a conductor session in the main checkout and four to six worker sessions in worktrees, for weeks.

## The roles

- **The conductor** is one long-lived Claude Code session in the checkout on the machine (`~/todo-app`, on `main`). It never builds a task itself. It cuts the work into pieces that touch different files, launches a worker per piece, merges finished branches, runs the checks on the merged result, and fixes anything that blocks more than one worker.
- **A worker** is an agent in its own git worktree on its own branch. It reads the docs, builds its whole piece, commits on its branch, and stops with a report. It never merges and never pushes.
- **You** answer questions nobody else can, and run anything that spends money or leaves the machine.

## Start the conductor

Attach and start Claude Code in the checkout, with a prompt that sets the role:

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

Claude Code's own Agent tool launches workers in isolated worktrees (under `.claude/worktrees/`, each on a `worktree-` branch) and tells the conductor when each finishes. The conductor waits on those notices rather than polling, and merges in dependency order: a task that defines an interface before the tasks that use it.

If you'd rather see each worker in its own tmux window, launch them from your laptop instead, one `repose run --worktree -p "..."` per task, and give the conductor the branch names to merge.

## Watch it run

```
repose ps
```

shows the conductor's window and, if you launched workers as windows, theirs. You get a notification when the conductor stops or asks; `repose questions` and `repose reply` handle the asking without an attach. The conductor's STATUS file is the log you read in the morning: one line per task, newest at the bottom.

From your laptop:

```
git fetch repose
git log --oneline main..repose/main   # what the conductor merged
git branch -r | grep repose/worktree- # the workers' branches
```

## The rules that came out of doing it

Put them in the conductor's prompt or in the repository's `CLAUDE.md`, where every worker reads them.

- **Workers claim before they build and record when they stop.** Keep a STATUS file with one line per task, appended by whoever is working. The next session reads it first.
- **Merge one branch at a time, and check after each merge.**
- **Assign shared numbers up front.** Anything workers number in parallel (decision entries, migration files, changelog lines) collides. The conductor hands out ranges in the launch prompt.
- **Append-only files union-merge.** Mark STATUS and decision logs with `merge=union` in `.gitattributes`, so two workers appending never conflict.
- **Done needs evidence.** Each worker's report names the evidence for each item in its checklist: "the migration is registered, here's the row".
- **Workers never push, never deploy, never touch another worktree.** The conductor merges; you push.
- **Snapshot before a big round.** [`repose snapshots create`](/docs/lifecycle#snapshots) before the conductor starts merging means a bad round is a one-minute restore, worktrees and all.

## When it gets big

- **Memory.** Six agents plus their test runs want more than a small machine. `repose resize --size large` or `xl` before a round, not during.
- **Nix and other heavy builds.** Have the conductor serialize them (one lock file everyone `flock`s) or the machine spends the round swapping.
- **A second machine for a second front.** [`repose fork`](/docs/lifecycle#fork-a-project) copies the whole machine; a conductor on the copy can take a different branch of the plan.
- **You are the rate limit.** The conductor should batch its questions. "Ask me only when a decision needs me" in its prompt, and a notification channel you'll see.
