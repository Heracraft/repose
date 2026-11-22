---
title: Installing software
description: Add packages and services to a project with repose config, from the CLI or the dashboard, so they're there after every restart and base update.
section: Using repose
order: 13
---

There are two ways to install software on a project's machine:

- **[On the machine](/docs/machine#installing-more)**, with `sudo apt-get install`, `mise use -g`, `npm i -g` and the like. It takes seconds and stays on the machine's disk.
- **In the project's configuration**, with `repose config`. It's installed again after every [base update](#revisions-and-base-updates) and on a restore onto another server, and services such as PostgreSQL are set up and started.

## Add a package

```
$ repose config add postgresql bun
postgresql: updating package lists
postgresql: installing 6 packages (50.0 MB)
postgresql: unpacking
postgresql: setting up
postgresql: starting repose-svc-postgresql.service
✓ postgresql 16 ready on 127.0.0.1:5432  14s
bun: installing bun@1 with mise
✓ bun ready  2.4s
Added postgresql and bun to todo-app.
Applied revision 4f1c2a9e in 17s.
```

The lines come from the machine as each step starts; each ✓ line is an item in place. A name that isn't a [menu entry](#the-menu) is a package from Ubuntu 24.04's archive, installed with `apt-get`: `repose config add ffmpeg libpq-dev`. Package names are lower case letters, digits, `+`, `-` and dots, at most 128 characters, as `apt` has them.

The machine installs into what's running, so your agents keep running. A stopped machine installs at its next start.

An item that doesn't install fails the command with the machine's own line and exit code 10:

```
$ repose config add bun qq
bun: installing bun@1 with mise
✓ bun ready  2.0s
qq: updating package lists
qq failed: E: Unable to locate package qq (apt-get exited 100)
error: qq failed: E: Unable to locate package qq (apt-get exited 100)
todo-app's menu is unchanged.
```

The menu keeps what it had, so no name from that command reaches it, `bun` included; run it again without the wrong name.

`repose config remove redis` takes an entry or a package out. A service's data stays on the machine, in `/var/lib/repose/services/redis`, and is there again if you add it back.

Press Ctrl-C to stop watching: `Interrupted. The install keeps going on the machine.` It's applied when it finishes, and `repose config revisions` says when.

## The menu

The project's **Config** tab is this menu, under **Packages**: tick an entry, choose a version where there's a choice, **Apply**. It groups the databases under **Services**. Type a package's name in the search and **Add** puts it under **Extra packages**, as `repose config add NAME` does; each there has **Remove**. When the install ends, the bar with **Apply** says `Applied`, or `Not installed:` and the machine's line for the item that failed, and the menu keeps what it had. If the menu changed since the page loaded, in another tab or from the CLI, **Apply** changes nothing, the bar says so, and the page shows the menu as it is now.

| Group     | Entries                                                                                                                     |
| --------- | --------------------------------------------------------------------------------------------------------------------------- |
| runtimes  | `bun`, `deno`, `nodejs` (20 or 22), `python` (3.11 or 3.13), `go-tools`, `zig`, `elixir`, `ruby`, `jdk` (17 or 21), `dotnet` |
| databases | `postgresql` (15, 16 or 17), `redis`, `mysql` (MariaDB), `memcached`, `rabbitmq`, `meilisearch`, `nats`                      |
| tools     | `awscli`, `opentofu`, `kubernetes` (kubectl, Helm, k9s), `shell-extras` (lazygit, delta, tldr, hyperfine)                    |
| deploy    | `wrangler`, `supabase`, `flyctl`, `vercel`, `portless`, `cloudflared`                                                        |

Those are the names `repose config add` takes. `repose config add` can't pick a version and takes the default: `nodejs` 22, `python` 3.13, `jdk` 21, `postgresql` 16.

Runtimes and command-line tools install with [mise](https://mise.jdx.dev) into `/home/dev`; `elixir`, `ruby` and `dotnet` (the .NET 8 SDK) come from Ubuntu's archive. `awscli` is AWS CLI 2.42.1 and `meilisearch` Meilisearch 1.57.0, each checked against its published checksum. `cloudflared` is already on every machine.

Databases listen on `127.0.0.1` only: PostgreSQL on 5432, Redis 6379, MariaDB 3306, Memcached 11211, RabbitMQ 5672 with its management UI on 15672, Meilisearch 7700, NATS 4222 with JetStream on. Each runs as its own systemd service, `repose-svc-postgresql.service` and so on, and keeps its data in `/var/lib/repose/services/<name>`. PostgreSQL comes from the PostgreSQL project's own apt repository, keeps each major version's data apart (`/var/lib/repose/services/postgresql/16`), and has a `dev` role and a `dev` database with no password, so `psql` and `postgres://localhost/dev` work. MariaDB has a `dev` database too. Meilisearch runs in development mode, with no master key.

## Configuration files

A machine takes its configuration from the menu and nothing else. `repose config apply FILE`, `repose config edit`, `repose config on` and `repose config off` are refused for it, with exit code 1:

> This machine runs the Ubuntu base; custom Nix files are not used, and `repose config add <item>` installs packages and services.

The account-wide file `repose config --global` writes never reaches it either. When every machine of your account runs the Ubuntu base, `repose config --global add`, `edit` and `apply` refuse with exit code 1 and change nothing; with older machines beside them, the save goes ahead and names the Ubuntu machines it doesn't reach.

A configuration file committed at the root of the checkout isn't used either: `repose run` and `repose sync` leave it alone.

Dotfiles, aliases and environment variables go in `/home/dev`, which every base update keeps: `~/.bashrc`, `~/.config/...`.

## Revisions and base updates

Every change to a project's configuration is saved as a revision. `repose config revisions` lists them with the error of any that failed. To get back from a change that broke something, `repose config apply --revision 9c41d2e7` applies an earlier revision to the running machine; the project's **Config** tab re-applies one from its **Revisions** list.

The platform updates the base (Ubuntu, agents, tools, kernel) most weeks, sooner for a security fix. A machine gets a new base when it restarts, never while it runs. Until then `repose status`, `repose ls`, the project's **Overview** and its **Config** tab say so; the Config tab puts it under the base it runs:

```text
Base 2026.11.30 is ready. It is used at the next restart.
```

**Restart** there stops the machine, with no snapshot, and starts it again; `repose stop --no-snapshot todo-app && repose start todo-app` does the same. What a new base keeps is in [The machine](/docs/machine#base-updates). To hold a project on its current base, tick **Hold base updates** on its **Config** tab; there's no CLI flag for it.

Holding has a price: a held project doesn't get what a new base adds. [herdr](/docs/run-and-attach#herdr-instead-of-tmux) arrived in base 2026.10.07, so a project on an older base can't switch to it. `repose run --multiplexer herdr` stops with `todo-app runs base 2026.09.15; herdr needs 2026.10.07 or newer.` Untick the hold and restart it first.

## Keeping a machine from freezing

A machine nobody is using is [frozen](/docs/lifecycle#frozen-machines) until something reaches it. To keep one out of that, tick **Never freeze this machine** on its Config page; there's no CLI flag for it. It takes effect at once: a machine that is frozen when you tick it wakes.
