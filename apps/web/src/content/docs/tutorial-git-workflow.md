---
title: A git workflow for several agents
description: One branch per task, review on your laptop, merge what's good, throw the rest away.
section: Tutorials
order: 23
---

This page assumes you've read [Git with repose](/docs/tutorial-git).

**Each task is a branch.** Every agent gets its own worktree and branch on the machine, you never edit the machine's `main`, and your laptop is where branches get reviewed and merged.

## 1. Sync once, in the morning

```
repose sync
```

Your `main` goes up. From here on the machine's `main` is a base for branches.

## 2. One agent per task, each in a worktree

```
repose run -d --worktree -p "rate-limit the public API"
repose run -d --worktree -p "parse dates with date-fns"
repose run -d --worktree --agent codex -p "audit_log migration"
```

`-d` keeps your shell. Each command prints where the agent works and its window:

```text
Worktree: ~/todo-app-worktree-1 on branch worktree-1
Window: claude
Worktree: ~/todo-app-worktree-2 on branch worktree-2
Window: claude-2
Worktree: ~/todo-app-worktree-3 on branch worktree-3
Window: codex
```

Worktrees are numbered from 1, skipping any number whose folder or branch is still there. `repose ps` shows who's still busy, and in which worktree:

```
$ repose ps
WINDOW      COMMAND  STATE        TREE        ACTIVE
0:shell     bash     -            checkout    2h ago
1:claude    claude   working      worktree-1  now
2:claude-2  claude   needs input  worktree-2  4m ago
3:codex     codex    working      worktree-3  now
```

`repose ps todo-app claude-2` prints that agent's last lines without attaching.

You get a [notification](/docs/notifications) as each one finishes or asks a question. `repose questions` lists what's waiting on you; `repose reply` answers without attaching.

## 3. Fetch and review on your laptop

```
$ git fetch repose
 * [new branch]  worktree-1  -> repose/worktree-1
 * [new branch]  worktree-2  -> repose/worktree-2
 * [new branch]  worktree-3  -> repose/worktree-3
```

Review each branch:

```
git log --oneline main..repose/worktree-1
git diff main...repose/worktree-1
git diff main...repose/worktree-1 -- test/
```

Run the tests on the machine without attaching, with the dev shell and secrets the agent had:

```
repose exec --workdir worktree-1 npm test
```

Or open the worktree in your editor over SSH with `repose code` and read it there.

## 4. Merge what's good

```
git merge repose/worktree-1
git merge repose/worktree-3
```

A branch that isn't good enough gets a second round: `repose attach -w claude`, the agent of that worktree, and tell it what to change. It commits on the same branch; you fetch again. Or ask for a pull request instead and review on GitHub: the machine has your `gh` login, so "push the branch and open a PR" works in a prompt.

## 5. Send the merged result back up

Send your laptop's `main` to the machine:

```
repose sync
```

The worktrees keep their branches, based on the old `main`. The third agent, still working, isn't touched: `repose sync` never touches a worktree.

## 6. Tidy

On the machine, or through `repose exec`:

```
repose exec -- git worktree remove ~/todo-app-worktree-1
repose exec -- git branch -D worktree-1
```

On your laptop, `git branch -rd repose/worktree-1` drops the fetched copy. Worktrees you leave in place cost only disk.

## The rules that make this work

- **Agents commit.** Put it in the prompt: "commit as you go" or "commit when the tests pass". An uncommitted change on the machine is the one thing that can get in the way of a sync.
- **Nobody works on the machine's `main`.** Then `repose sync` is always safe, and a branch is always a clean diff against something you know.
- **Review on the laptop.** There `git diff main...branch` reads a copy the agent can't touch.
- **Risky experiments get a fork.** `repose fork` copies the whole machine, worktrees, database and all, so an agent can try something destructive on the copy. [Projects and lifecycle](/docs/lifecycle#fork-a-project).
