---
title: herdr on your machine
description: Run a machine's terminals in herdr, watch its agents in your laptop herdr's sidebar, and get them back after a stop.
section: Tutorials
order: 26
status: experimental
---

[herdr](https://herdr.dev) is a terminal multiplexer for coding agents: one sidebar shows which agent is working, which is waiting for you and which is done, across your laptop and the machines you reach over SSH. A repose machine can run herdr in place of tmux.

You need herdr 0.9.0 or newer on your laptop for the sidebar, and repose 0.1.31 or newer (`repose version`, [update](/docs/install#update)).

## Make herdr the default

```toml
# ~/.config/repose/config.toml
default_multiplexer = "herdr"
```

Every project `run` creates from now on gets herdr. For one project, pass the [flag](/docs/cli#repose-run-project) instead:

```
repose run --multiplexer herdr
```

An existing project switches at its next start.

## Start an agent

In the checkout, from a terminal in your laptop's herdr:

```
$ cd ~/src/todo-app
$ repose run -p "add a dark mode toggle"
✓ Created todo-app (large, herdr)  0.3s
...
todo-app is in herdr's sidebar. Ctrl-C ends its forwards.
```

`run` opened a tab in the machine's `checkout` workspace, started Claude Code there and typed the prompt. Select `checkout` under `todo-app` in the sidebar to watch the agent. The command stays in its pane until you press Ctrl-C, so the next prompt goes in another pane.

A second prompt opens another tab, `claude-2`. With [`--worktree`](/docs/run-and-attach#several-agents-separate-trees) the agent gets its own git worktree, which herdr shows under `checkout`:

```
repose run --worktree -p "write the tests for the toggle"
```

`repose ps` lists the agents and their state:

```
$ repose ps
WORKSPACE            AGENT   NAME       STATE
checkout             claude  claude*    working
todo-app-worktree-1  claude  claude-2   idle
```

## The sidebar

`run` and `attach` add each running herdr project to your laptop herdr's machine list, and `repose rm` takes it out again. Entries for your other hosts stay as they are. If you disable a repose machine in herdr, it stays disabled. A stopped machine keeps its entry; herdr shows it as stopped until `repose start`.

A machine in the sidebar keeps an SSH session open, so it never reads as [unused](/docs/lifecycle#unused-machines); [disable its entry](/docs/run-and-attach#herdr-instead-of-tmux) to stop that.

## After a stop and start

A [stop](/docs/lifecycle#stop-and-start) ends herdr with everything else on the machine, and the next start [puts its workspaces and agents back](/docs/run-and-attach#when-the-machine-stops).

## What changes on a repose machine

- **Claude Code doesn't ask.** It runs in [`bypassPermissions`](/docs/agents#permissions) mode, so herdr seldom shows it as blocked.
- **Notifications come from the machine.** `needs input` and an agent's `repose-notify --done` reach your phone and email as on tmux. The sidebar's connection counts as [being at the machine](/docs/notifications#what-youll-get).
- **A restore takes herdr with it.** Its layout lives in `~/.config/herdr`, so restoring a [snapshot](/docs/lifecycle#snapshots) puts the workspaces back as they were then.

## When it doesn't work

**`machine add` fails with "Bad owner or permissions on /etc/ssh/ssh_config".** herdr writes its own SSH config and runs `ssh -F` on it, which makes `ssh` check that file's owner. A laptop's file passes; one on another repose machine may not. Turn the generated config off and herdr uses your SSH config as it is, repose's connection included:

```toml
# ~/.config/herdr/config.toml
[remote]
manage_ssh_config = false
```
