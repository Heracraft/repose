---
title: Sync
description: When your checkout is copied to the machine, what travels, what stays behind, and how work comes back.
section: Using repose
order: 11
---

The first `repose run` for a machine copies your checkout to it. After that, `repose run` attaches to the machine as it is, and your laptop's later work goes over only when you run `repose sync`. Nothing syncs continuously and nothing comes back on its own: the agent commits, and you fetch its commits with `git fetch repose`.

`repose attach` and `repose run --no-sync` never touch the machine's checkout, not even on a new machine.

## Where the checkout is

The first sync puts your checkout under the machine's home directory, named after your repository's folder on the laptop, not after the project. Run `repose run kanali` in `~/Downloads/projects/factory` and the project is `kanali` while the checkout is `/home/dev/factory`:

```text
Synced: 14 modified, 30 untracked, 1 env file  2.4s
Checkout: ~/factory on the machine
```

The `Checkout:` line prints on the sync that makes the checkout and on no run after it. The [SSH host](/docs/ssh-and-editors#every-project-is-an-ssh-host) is the project's name; the path under it is your folder's. A folder name with spaces or other awkward characters becomes a safe one: `job search` is `job-search`. If anything but an empty directory already holds that name, `~/go` say, the checkout takes the project's name instead.

A machine with no checkout, one made by `repose run --no-sync` or from a folder that isn't a git repository, has you working in `/home/dev` itself. Its first `repose sync` makes the checkout.

Later runs, from any folder or any laptop, use the checkout the machine already has. A machine whose checkout an earlier version of repose made keeps it at `/home/dev/<project>`.

A second repository can live on the same machine in [a checkout of its own](/docs/run-and-attach#several-repositories-on-one-machine): run `repose run --on PROJECT` in its folder.

## What travels

- **Commits.** Your current branch, including commits you haven't pushed. Private repositories work with no setup on the machine.
- **Uncommitted changes** to tracked files.
- **Untracked files** that git isn't ignoring.
- **`repose.nix`.** At the root of the repository, it's applied as the machine's configuration when it changed ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)).
- **`.env` files.** Gitignored `.env` and `.env.*` files up to 1 MB each, anywhere in the checkout. If the machine's copy is newer, it's kept and the CLI says which. Unlike [secrets](/docs/secrets), they're files on the machine's disk, so they're in its snapshots. [Turn them off](/docs/secrets#choose-what-is-copied) and they stay on your laptop.

The checkout goes over the sync's own SSH connection, so [repose never gets a copy of your code](/docs/secrets#what-repose-stores). `repose.nix` is the exception: it goes to repose too, which builds it and keeps every version as a [revision](/docs/config#revisions-and-base-updates).

## Sending later work

`repose sync` sends your laptop's current work over the machine's checkout and returns without attaching. It creates or starts the machine first if it needs to. With nothing to send and nothing on the machine it says `Nothing new to sync: the machine already has this checkout.`

A `repose run` that finds the checkout already there leaves it alone and says what your laptop still has, in your terminal and again on the machine's status line (a [herdr notification](/docs/run-and-attach#herdr-instead-of-tmux) on herdr), because the attach covers the terminal before you've read it:

```text
Not synced: your laptop has work the machine doesn't (3 modified,
1 untracked, 2 commits). `repose sync` sends it.
```

With nothing new on your laptop, `repose run` says nothing about syncing.

Your tool logins, git settings and agent config don't wait for a sync: every `repose run` copies them ([Logins copied from your laptop](/docs/secrets#logins-copied-from-your-laptop)).

## What doesn't

- Other gitignored files: build output, caches, local databases.
- Dependency directories such as `node_modules`, `.venv`, `.next` and `.turbo`, even when they aren't ignored. The CLI names what it skipped. Install dependencies on the machine.
- Untracked files over 100 MB each, and any that doesn't fit under one sync's 500 MB of untracked files.

`sync.exclude` in [config.toml](/docs/cli#config-toml) takes more gitignore-style patterns. Both caps and that list are about untracked files: a tracked file travels whatever its size and whatever pattern matches it.

## Submodules

Submodules you have checked out travel with the checkout, nested ones included; one you never checked out stays empty on the machine. Each arrives at the commit your laptop has in it, with its uncommitted changes, untracked files and `.env` files, under the same rules and limits as the rest of the checkout. A private submodule needs no access from the machine: its commits come from your laptop.

A shallow submodule can't be sent. The machine fetches it from the submodule's own remote instead, which works for github.com when you're logged in to `gh`. If that fails the run goes on, and the CLI says the submodule is empty on the machine. Changes inside a shallow submodule don't travel; run `git -C <path> fetch --unshallow` on your laptop to send them.

Git LFS files arrive as pointer files. Run `git lfs pull` on the machine to fetch their contents.

## When the machine has changes of its own

A sync writes only the files your work touches.

With nothing new on your laptop the checkout is left as it is:

```text
Nothing new to sync. The machine has 3 commits on main your laptop
doesn't have, and uncommitted changes to 2 files.
```

Commits count from your last sync and stop counting once `git fetch repose` has brought them over.

When your laptop does have new work, the sync writes the files your new commits change, your uncommitted changes and your untracked files. The machine's changes to every other file stay where they are, and the summary counts them:

```text
Synced: 2 modified, 1 untracked (6 new commits); kept the machine's
changes to 3 files  1.6s
```

If the machine changed a file your laptop changed too, the sync would write over it, so it stops, changes nothing, names them and exits with code 6:

```text
Not synced: the machine changed 2 files that your laptop changed
too:
  src/session.ts
  notes.md
`repose sync --stash-machine` moves the machine's changes to its git
stash first.
```

`--stash-machine` moves all of the machine's uncommitted changes, untracked files included, to `git stash` there, named `repose sync --stash-machine`:

```text
Synced: 1 modified, 0 untracked; stashed the machine's changes to 3
files (git stash 1a2b3c4)  1.2s
```

`git stash apply 1a2b3c4` on the machine brings them back.

Changes that are exactly what the previous sync wrote aren't the machine's, so they never stop one. They go to a stash named `repose run: last sync`; the newest 10 of those are kept, and the sync carries on.

A file the agent creates while the sync runs, at a path your laptop is sending, is overwritten: the sync checked for overlaps before it started writing.

A merge, rebase, am, cherry-pick, revert or bisect in progress on the machine stops the sync before it changes anything, with code 6 again:

```text
Not synced: the machine's checkout is in the middle of a git rebase.
Finish or abort it there, or run `repose sync --discard-machine` to
end it and move the machine's changes to its git stash.
```

`--stash-machine` stops there too: a stash can't hold one of those in progress. `--discard-machine` ends the operation where it stands, then stashes as `--stash-machine` does, under the name `repose sync --discard-machine`. Commits the rebase had already made stay in the machine's `git reflog`.

If the agent committed on your branch and your laptop has commits of its own, the sync merges your laptop's commit into the machine's copy of that branch, so the branch has both. It merges only when git can do it without a conflict and the agent's commits leave your uncommitted files alone. The merge commit carries your laptop's git name and email. The machine sitting on another branch doesn't stop it: the checkout moves to your branch, as it does on every sync, and the other branch keeps its commits. Otherwise the sync checks your commit out detached, leaves the agent's branches where they are, and says so.

## Getting work back

`repose run`, `repose attach` and `repose sync` add a git remote named `repose` to a checkout that is the project's own. Its URL is the checkout's folder under the [SSH host every project has](/docs/ssh-and-editors#every-project-is-an-ssh-host), `todo-app.repose:~/todo-app`. Once the agent has committed, fetch its commits like any other remote's:

```
$ git fetch repose
From todo-app.repose:~/todo-app
 * [new branch]      main            -> repose/main
 * [new branch]      worktree-1      -> repose/worktree-1
$ git log --oneline main..repose/main
16df520 Show errors under each field
da9c3c3 Validate the email field
```

Nothing goes through GitHub, and the agent doesn't need to push. Only commits travel: a file the agent changed but didn't commit stays on the machine. Ask the agent to commit, or `repose attach` and commit it yourself.

The branch of a [`--worktree` agent](/docs/run-and-attach#several-agents-separate-trees) is `worktree-1` on the machine and `repose/worktree-1` on your laptop, so `git pull repose worktree-1` names it as the machine does.

The remote is fetch-only. `git push repose` fails with `'this remote is fetch-only; repose sync sends your work to the machine' does not appear to be a git repository`; a remote added before that wording changed still says `repose run`. The machine's checkout has a branch checked out and a push would move it under the agent, so work goes the other way, with `repose sync`. `git fetch --all` skips the remote, so it never reaches for a machine that's stopped.

`run` and `attach` say so once and leave it alone after that. It lives in `.git/config`. A remote named `repose` that you added yourself is left alone, and the CLI says once how to reach the machine under another name: `git remote add NAME todo-app.repose:~/todo-app`.

### Pushing from the machine

The machine's checkout has an `origin` of its own: the SSH form of your project's remote, `git@github.com:owner/repo.git`, so `git push` there reaches the same repository yours does. It pushes with the `gh` login copied from your laptop ([Logins copied from your laptop](/docs/secrets#logins-copied-from-your-laptop)); [other git hosts](/docs/secrets#other-git-hosts) have none that repose copies. A project with no remote of its own gets no `origin` on the machine.

### Single files

For a file that shouldn't go through git, use [`repose cp`](/docs/cli#repose-cp-r-src-dst). A path after `:` is on the machine, relative to the checkout:

```
repose cp :logs/app.log .
repose cp ./report.pdf todo-app:/tmp/
```

## Repositories the sync can't handle

The checkout has to be a git repository with at least one commit and its whole history. The CLI refuses before it creates or starts a machine, so a refused run costs nothing:

- No commits yet: `git add -A && git commit -m init`
- A shallow clone: `git fetch --unshallow`

On a `repose run` both refusals end with `, or pass --no-sync`, which makes the machine with no checkout.

Outside a git repository `repose run` refuses nothing: it creates the machine, copies nothing and says `Not a git repository, so nothing was synced.` The machine is [named after the folder](/docs/lifecycle#a-second-machine-for-the-same-repository). `repose sync` there exits 2 with `This folder is not a git checkout, so there is nothing to sync.`

A repository on github.com with at least 4 MiB of history is cloned by the machine itself on the first sync, over its own network, and your laptop sends only the commits GitHub doesn't have; the summary says `history cloned from github.com`. Submodules on github.com go the same way. A public repository arrives with the files of the commit you're on, and the machine fetches older versions in the background, so `git remote` on the machine lists `repose-backfill` until it's whole. If a fetch fails, the CLI sends that history itself and says so.
