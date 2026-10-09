---
title: Git with repose
description: Where your code goes when you run repose, how the agent's commits come back, and what to do when both sides changed.
section: Tutorials
order: 20
---

You have a checkout on your laptop. The agent works in a copy of it on the machine. Git is the only thing that moves work between the two, and it moves it in two different ways: your side goes up with `repose run`, the agent's side comes back with `git fetch`.

## Start with something small

Any repository with a commit will do:

```
mkdir hello && cd hello && git init -q
echo '# hello' > README.md && git add -A && git commit -qm init
repose run
```

You land in a tmux session in `~/hello` on the machine. Detach with `Ctrl-b` `d`.

## What went up

The first `repose run` copied the state of your checkout:

- your current branch, including commits you haven't pushed;
- uncommitted changes to tracked files;
- untracked files that aren't gitignored;
- gitignored `.env` files.

It doesn't copy build output, `node_modules` or anything else gitignored, and it never watches your files afterwards. Later runs attach to the machine as it is. Change something on your laptop and it stays there until you run `repose sync`. [Sync](/docs/sync) has the full list and the size limits.

On your laptop:

```
echo 'hello from the laptop' > note.txt
repose run
```

```text
Not synced: your laptop has work the machine doesn't (1 untracked).
`repose sync` sends it.
```

Detach, then send it:

```
repose sync
```

```text
Synced: 0 modified, 1 untracked
```

Neither command restarts or rebuilds the machine.

## What comes back

The agent commits, and you fetch. The first `repose run` added a git remote called `repose` to your checkout, pointing at the machine's copy over the same SSH connection everything else uses:

```
$ git remote -v
repose  hello.repose:~/hello (fetch)
repose  'this remote is fetch-only; repose sync sends your work to
the machine' (push)
```

Give the agent something to commit:

```
repose run -p "add an MIT LICENSE file and commit it"
```

Fetch once it's done (you get a notification):

```
$ git fetch repose
From hello.repose:~/hello
 * [new branch]      main       -> repose/main
$ git log --oneline main..repose/main
3f1c2a0 Add the MIT license
```

`repose/main` is the machine's `main`. Use it like any remote branch:

```
git diff main repose/main      # what changed
git merge repose/main          # take it
git cherry-pick 3f1c2a0        # or take one commit
```

Only commits travel this way. Files the agent changed but didn't commit stay on the machine; ask the agent to commit, or `repose attach` and commit yourself. For a single file that shouldn't go through git, `repose cp :path/on/machine .` copies it.

## When both sides changed

You edited `README.md` on your laptop while the agent was editing it on the machine. `repose run` attaches without touching either copy. `repose sync` would write over the agent's edit, so it stops:

```text
Not synced: the machine changed 1 file that your laptop changed too:
  README.md
`repose sync --stash-remote` moves the machine's changes to its git
stash first.
```

The exit code is 6, so a script notices. Files the agent changed that your laptop didn't touch never stop a sync; they stay as the agent left them. Ask agents to commit: had the agent committed instead of leaving the file dirty, the sync would merge your laptop's commit into the agent's branch, or, when the two conflict, check your commit out detached and leave the agent's branch where it is. `git fetch repose` brings that branch to you to merge like any other.

## Agents on their own branches

`--worktree` gives an agent its own git worktree and branch, next to the checkout on the machine:

```
$ repose run --worktree -p "try another parser approach"
Worktree: ~/hello-worktree-1 on branch worktree-1
```

Two agents in two worktrees never edit each other's files. On your laptop the branch arrives as `repose/worktree-1`:

```
git fetch repose
git log --oneline main..repose/worktree-1
git merge repose/worktree-1
```

## Pushing from the machine

The machine's checkout has the same `origin` as yours. If you're logged in to the GitHub CLI on your laptop, that login is copied, and `git push` on the machine works over HTTPS, so an agent can open a pull request with `gh pr create`. Your SSH keys never go to the machine; [Secrets and security](/docs/secrets#other-git-hosts) covers other hosts.

## Git and snapshots

Git protects tracked source. A [snapshot](/docs/lifecycle#snapshots) protects the whole machine: the database, installed tools, uncommitted work, logins. Take one before letting an agent loose on something destructive, and you can put everything back in a few minutes, git included.

## Clean up

```
repose rm hello
```

That removes the machine and the `repose` remote from your checkout. Branches you fetched stay until `git branch -rd repose/main`.
