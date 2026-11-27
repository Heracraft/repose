---
title: Configuration
description: Installing software on a project's machine, databases, base updates and the Config tab's switches.
section: Using repose
order: 13
---

Software goes on the machine itself, with the tools any Ubuntu machine has: `sudo apt-get install -y ffmpeg`, `mise use -g node@22`, `npm i -g tsx`, or ask the agent to install what it needs ([Installing more](/docs/machine#installing-more)). What `apt-get` installs is recorded on the machine's disk and installed again after every [base update](#revisions-and-base-updates).

## Databases

A database server installed with `apt-get` keeps its data under `/var/lib` (`/var/lib/postgresql`, `/var/lib/redis`), and a base update starts that clean: the server comes back, its data doesn't. Run the database in Docker, whose volumes a base update keeps:

```
docker run -d --name postgres --restart unless-stopped \
  -p 127.0.0.1:5432:5432 -e POSTGRES_PASSWORD=dev \
  -v pg:/var/lib/postgresql/data postgres:17
```

## Earlier menu entries

A project with entries in the menu that `repose config add` and the Config tab used to fill keeps them: the machine installs them again after every base update, the project's **Config** tab lists them, and `repose config show` prints them. A service from it, such as `repose-svc-postgresql.service`, keeps its data in `/var/lib/repose/services/<name>`, which a base update keeps.

## Configuration files

An Ubuntu machine takes no configuration file. `repose config apply FILE`, `repose config edit`, `repose config on` and `repose config off` are refused for it, with exit code 1:

> This machine runs the Ubuntu base; custom Nix files are not used. Install packages on the machine with apt-get.

The account-wide file `repose config --global` writes never reaches it either. When every machine of your account runs the Ubuntu base, `repose config --global edit` and `apply` refuse with exit code 1 and change nothing; with older machines beside them, the save goes ahead and names the Ubuntu machines it doesn't reach.

A configuration file committed at the root of the checkout isn't used either: `repose run` and `repose sync` leave it alone.

Dotfiles, aliases and environment variables go in `/home/dev`, which every base update keeps: `~/.bashrc`, `~/.config/...`.

## Revisions and base updates

On an [older machine](/docs/machine#older-machines), every change to its configuration is saved as a revision. `repose config revisions` lists them with the error of any that failed. To get back from a change that broke something, `repose config apply --revision 9c41d2e7` applies an earlier revision to the running machine; the project's **Config** tab re-applies one from its **Revisions** list.

The platform updates the base (Ubuntu, agents, tools, kernel) most weeks, sooner for a security fix. A machine gets a new base when it restarts, never while it runs. Until then `repose status`, `repose ls`, the project's **Overview** and its **Config** tab say so; the Config tab puts it under the base it runs:

```text
Base 2026.11.30 is ready. It is used at the next restart.
```

**Restart** there stops the machine, with no snapshot, and starts it again; `repose stop --no-snapshot todo-app && repose start todo-app` does the same. What a new base keeps is in [The machine](/docs/machine#base-updates). To hold a project on its current base, tick **Hold base updates** on its **Config** tab; there's no CLI flag for it.

Holding has a price: a held project doesn't get what a new base adds. [herdr](/docs/run-and-attach#herdr-instead-of-tmux) arrived in base 2026.10.07, so a project on an older base can't switch to it. `repose run --multiplexer herdr` stops with `todo-app runs base 2026.09.15; herdr needs 2026.10.07 or newer.` Untick the hold and restart it first.

## Keeping a machine from freezing

A machine nobody is using is [frozen](/docs/lifecycle#frozen-machines) until something reaches it. To keep one out of that, tick **Never freeze this machine** on its Config page; there's no CLI flag for it. It takes effect at once: a machine that is frozen when you tick it wakes.
