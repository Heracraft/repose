---
title: Sync
description: When your checkout is copied to the machine, what travels, what stays behind, and how work comes back.
section: Using repose
order: 11
---

The first `repose run` for a machine copies your checkout to it. After that, `repose run` attaches to the machine as it is, and your laptop's later work goes over only when you run `repose sync`. Nothing syncs continuously and nothing comes back on its own: the agent commits, and you fetch its commits with `git fetch repose`.

An agent's uncommitted work on the machine can't block a `repose run`, and you choose when your laptop's work lands on top of it.

`repose attach` and `repose run --no-sync` never touch the machine's checkout, not even on a new machine.

## Where the checkout is

The first sync puts your checkout in the machine's home directory under your laptop folder's name, whatever the project is called. Run `repose run --name kanali` in `~/Downloads/projects/factory` and the project is `kanali` but the checkout is `/home/dev/factory`, and the run says so:

```text
Synced: 14 modified, 30 untracked, 1 env file
Checkout: ~/factory on the machine
```

The host is the project's name and the path is your folder's: `scp -r ./infra kanali.repose:factory/`, `ssh kanali.repose 'cd factory && ls'`. A folder name with spaces or other characters becomes a safe name (`job search` is `job-search`). If the machine already has a non-empty directory of that name, such as `~/go`, the checkout goes under the project's name instead.

A machine with no checkout, one made with `repose run --no-sync` or from a directory that isn't a git repository, has you work in `/home/dev` itself. Its first `repose sync` makes the checkout.

Later runs, from any folder or laptop, use the checkout the machine already has. A machine whose checkout an earlier version of repose made keeps it at `/home/dev/<project>`.

## What travels

- **Commits.** Your current branch, including commits you haven't pushed. They go straight from your laptop, so private repositories work with no setup on the machine.
- **Uncommitted changes** to tracked files.
- **Untracked files** that git isn't ignoring.
- **`repose.nix`.** At the root of the repository, it's applied as the machine's configuration when it changed ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)).
- **`.env` files.** Gitignored `.env` and `.env.*` files up to 1 MB each. If the machine's copy is newer, it's kept. Unlike [secrets](/docs/secrets), they are files on the machine's disk, so they are in snapshots. `repose secrets choose --off env` keeps them on your laptop ([Secrets](/docs/secrets#choose-what-is-copied)).

Everything goes over your SSH connection. None of it is stored by repose.

```text
Synced: 4 modified, 2 untracked, 2 env files (3 new commits)
```

## Sending later work

When the machine already has your checkout and your laptop has work it doesn't, `repose run` attaches without copying it and tells you:

```text
Not synced: your laptop has work the machine doesn't (3 modified,
1 untracked, 2 commits). `repose sync` sends it.
```

With nothing new on your laptop, `repose run` says nothing about syncing.

`repose sync` (or `repose sync PROJECT`) copies your laptop's current work over the machine's checkout and returns without attaching. It creates or starts the machine if needed. With nothing new it says `Nothing new to sync: the machine already has this checkout.`

Your tool logins, git identity and Claude Code settings are copied at every `repose run` whether or not the checkout is, so a rotated token reaches the machine on your next run.

## What doesn't

- Other gitignored files: build output, caches, local databases.
- Dependency directories such as `node_modules`, `.venv`, `.next` and `.turbo`, even when they aren't ignored. The CLI names what it skipped. Install dependencies on the machine; they need Linux builds anyway.
- Files over 100 MB, and untracked files past 500 MB in one sync.

To leave out more, add gitignore-style patterns to `~/.config/repose/config.toml`:

```toml
[sync]
exclude = ["dist", "*.mp4", "fixtures/large"]
```

## Submodules

Submodules you have checked out travel the same way, nested ones included. Each arrives at the commit your laptop has checked out in it, with its uncommitted changes, untracked files and `.env` files, under the same rules and limits as the rest of the checkout. Submodule commits you haven't pushed travel too, and a private submodule needs no access from the machine. A submodule you never checked out on your laptop stays empty on the machine.

A submodule that is a shallow clone on your laptop can't be sent. The machine fetches it from its own remote instead, which works for github.com when you're logged in to `gh`. If that fails, the run goes on and the CLI says the submodule is empty on the machine. Changes inside a shallow submodule aren't sent; run `git -C <path> fetch --unshallow` on your laptop to send them.

Git LFS files arrive as their small pointer files, not their contents. Run `repose config add git-lfs` once, then `git lfs pull` on the machine to fetch them.

## When the machine has changes of its own

`repose sync` only copies your laptop's work onto the machine. It never restarts or rebuilds the machine.

If the machine changed since your last sync (usually an agent's edits or commits) and your laptop has nothing new since then, there is nothing to copy, and the checkout is left as it is:

```text
Nothing new to sync. The machine has changes your laptop doesn't
have (27 files); `repose sync --stash-remote` puts them in git
stash and lays your laptop's work over them.
```

If your laptop does have new work, copying it would write over the machine's changes, so the sync stops, changes nothing and exits with code 6:

```text
`repose sync` copies your laptop's work onto the machine.
It doesn't restart or rebuild anything.
The machine has uncommitted changes your laptop doesn't have
(27 files), probably an agent's:
  src/auth.ts
  src/routes/login.ts
  src/routes/logout.ts
  src/session.ts
  src/session.test.ts
  package.json
  pnpm-lock.yaml
  notes.md
  and 19 more
Your laptop has new work as well, so syncing now would write over
them. Nothing was changed. Pick one:
  repose attach                  look at the machine first
  repose sync --stash-remote     put the machine's changes in git
                                 stash, then sync
  repose sync --discard-remote   throw the machine's changes away,
                                 then sync
```

`--stash-remote` keeps the machine's changes in `git stash` there, named `repose run`. `--discard-remote` throws them away.

Changes that are exactly what the previous sync wrote don't count as the machine's: they are stashed on the machine as `repose run: last sync` (the newest 10 are kept) and the sync goes on.

If the agent committed on the branch and your laptop has new commits of its own, the sync checks out your laptop's commit detached and leaves the agent's branch where it is. Nothing is lost. `git fetch repose` brings the agent's branch to your laptop, where you merge or rebase it as you would any other.

## Getting work back

`repose run` adds a git remote named `repose` to your checkout. It points at the checkout on the machine, over the same SSH connection as `ssh todo-app.repose`. Once the agent has committed, fetch its commits like any other remote's:

```
$ git fetch repose
From todo-app.repose:~/todo-app
 * [new branch]      main            -> repose/main
 * [new branch]      worktree-1      -> repose/worktree-1
$ git log --oneline main..repose/main
16df520 Show errors under each field
da9c3c3 Validate the email field
```

Then use them as you would any branch:

```
git diff main repose/main          # what the agent changed
git merge repose/main              # take all of it
git cherry-pick da9c3c3            # take one commit
git pull repose main               # fetch and merge in one step
```

Nothing goes through GitHub, and the agent doesn't need to push. Only commits travel: files the agent changed but didn't commit stay on the machine. Ask the agent to commit, or `repose attach` and commit yourself.

A branch on the machine appears under `repose/` followed by its name there. The branch of a [`--worktree` agent](/docs/run-and-attach#several-agents-separate-trees), `worktree-1` on the machine, is `repose/worktree-1` on your laptop, and `git pull repose worktree-1` names it as the machine does.

The remote is for fetching. `git push repose` fails with `'this remote is fetch-only; repose sync sends your work to the machine' does not appear to be a git repository` (remotes added before this change name `repose run`): the machine's checkout has a branch checked out, and pushing would move it under the agent. To send your work, run `repose sync`. `git fetch --all` skips the remote, so it doesn't try a machine that's stopped.

Details:

- `repose run` and `repose attach` add the remote when the checkout is the project's own, and say so the first time. They leave it alone after that. It lives in `.git/config`, which isn't committed, so nothing changes in your repository.
- If your checkout already has a remote named `repose` that points somewhere else, it's left alone, and the CLI says once how to add the machine under another name: `git remote add NAME todo-app.repose:~/todo-app`.
- `repose rm` in the checkout removes the remote. Branches you already fetched stay as `repose/...` until you delete them with `git branch -rd`.
- Copies made with [`repose fork`](/docs/lifecycle#fork-a-project) don't get a remote of their own. Add one by hand: `git remote add fork-2 todo-app-fork-2.repose:~/todo-app-fork-2`.
- The machine has to be running. On a stopped one, `git fetch repose` fails with ``todo-app is stopped; run `repose start todo-app` ``.

### Pushing from the machine

The machine's checkout has the same `origin` as yours, so `git push` there works as it does locally. If you're logged in to the GitHub CLI (`gh`) on your laptop and the remote is on github.com, that login is copied and git on the machine pushes over HTTPS with it. For other git hosts, see [Secrets](/docs/secrets#other-git-hosts). This is the way when the work should land on GitHub anyway, for a pull request.

### Single files

For a file that shouldn't go through git, use `repose cp`. A path after `:` is on the machine, relative to the checkout:

```
repose cp :logs/app.log .
repose cp todo-app:/tmp/trace.json .
repose cp ./report-*.pdf todo-app:/tmp/
repose cp -r ./fixtures :test/fixtures
```

## Repositories the sync can't handle

The checkout must be a git repository with at least one commit and full history. The CLI tells you what to run:

- No repository or no commits: `git init && git add -A && git commit -m init`
- A shallow clone: `git fetch --unshallow`

It says so before it creates or starts a machine, so a refused run costs nothing. `repose run` in a directory that isn't a repository makes a machine without syncing and says `Not a git repository, so nothing was synced.` `repose sync` there refuses.

A directory without a remote gets a machine named after the directory (`job search` becomes `job-search`); `--name` picks another name.

For a repository on github.com over about 20 MB, the first sync has the machine clone the history from GitHub and sends only what GitHub doesn't have. If that clone fails, the CLI sends everything itself.
