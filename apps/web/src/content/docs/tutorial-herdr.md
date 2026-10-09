---
title: herdr on your machine
description: Run a machine's terminals in herdr, watch its agents in your laptop herdr's sidebar, and get them back after a stop.
section: Tutorials
order: 26
status: experimental
---

[herdr](https://herdr.dev) is a terminal multiplexer for coding agents: one sidebar shows which agent is working, which is waiting for you and which is done, across your laptop and the machines you reach over SSH. A repose machine can run herdr in place of tmux. Its agents keep running with the laptop closed, and after `repose stop` and `repose start` herdr puts the tabs back and resumes the agents where they were.

You need herdr 0.9.0 or newer on your laptop ([herdr.dev](https://herdr.dev)) for the sidebar, and repose 0.1.31 or newer (`repose version`; [update](/docs/install#update) by running the install command again). Without herdr on the laptop, `repose attach` runs herdr's client on the machine, and there is no sidebar.

## Make herdr the default

```toml
# ~/.config/repose/config.toml
default_multiplexer = "herdr"
```

Every project `run` creates from now on gets herdr. You can skip this when you run repose from a herdr pane: a new project made there gets herdr anyway. For one project, pass the flag instead:

```
repose run --multiplexer herdr
```

An existing project switches at its next start. Until it stops, it keeps the tmux it runs and its agents.

## Start an agent

In the checkout, from a terminal in your laptop's herdr:

```
$ cd ~/src/todo-app
$ repose run -p "add a dark mode toggle"
✓ Created todo-app (large, herdr)  0.3s
...
Ready in 38s.
todo-app is in herdr's sidebar. Ctrl-C ends its forwards.
```

`run` opened a tab in the machine's `checkout` workspace, started Claude Code there and typed the prompt. The machine is now in herdr's sidebar as `todo-app`, with `checkout` under it; select it to watch the agent. The `repose` command stays in its pane and keeps your ports forwarded until you press Ctrl-C.

From a terminal outside herdr, the same command opens herdr's client on the machine (`herdr --remote todo-app.repose`) instead.

A second prompt opens another tab, `claude-2`. With `--worktree` the agent gets its own git worktree, which herdr shows under `checkout`:

```
repose run --worktree -p "write the tests for the toggle"
```

`repose ps` lists the agents and their state:

```
$ repose ps
WORKSPACE             AGENT   NAME      STATE
checkout              claude  claude*   working
todo-app-worktree-1   claude  claude-2  idle
```

## The sidebar

`run` and `attach` add each running herdr project to your laptop herdr's machine list, and `repose rm` takes it out again, so the list matches your account. Entries for your other hosts stay as they are. If you disable a repose machine in herdr, it stays disabled. A stopped machine keeps its entry; herdr shows it as stopped until `repose start`.

A machine in the sidebar holds an SSH connection open while your laptop's herdr runs, and that counts as someone using it: it doesn't get the idle notice. Disable its entry when you're done with it for the day; its agents keep running. The commands are under [herdr instead of tmux](/docs/run-and-attach#herdr-instead-of-tmux).

## After a stop and start

`repose stop` ends every process on the machine, herdr included, and names any agent it interrupted. `repose start` starts herdr again before anyone connects: the workspaces and tabs come back, and each agent with herdr's integration (Claude Code, Codex, opencode and pi) resumes in its conversation.

## What changes on a repose machine

- **Claude Code doesn't ask.** It runs in `bypassPermissions` mode on the machine ([Agents](/docs/agents#let-it-run-without-asking)), so herdr seldom shows it as blocked.
- **Notifications come from the machine.** `finished` and `needs input` reach your phone and email as on tmux ([Notifications](/docs/notifications)).
- **One herdr per machine.** The base's herdr runs the machine; `herdr update` on the machine installs a second one into `~/.local/bin` that repose doesn't test. Remove it to go back.
- **A restore takes herdr with it.** Its layout lives in `~/.config/herdr` on the machine, so restoring a snapshot puts the workspaces back as they were then.

## When it doesn't work

**`no server running` on `repose attach`.** Your repose CLI is older than 0.1.31. [Update](/docs/install#update) it.

**`Could not add todo-app to herdr's sidebar`.** herdr's `machine add` failed and the line ends with its message. Check that `ssh todo-app.repose true` works from a plain terminal; `run` and `attach` try again each time.

**`machine add` fails with "Bad owner or permissions on /etc/ssh/ssh_config".** herdr writes its own SSH config and runs `ssh -F` on it, which makes `ssh` check that file's owner. A laptop's file passes. If yours doesn't, for example when you run herdr from another repose machine, turn the generated config off and herdr uses your SSH config as it is, repose's connection included:

```toml
# ~/.config/herdr/config.toml
[remote]
manage_ssh_config = false
```
