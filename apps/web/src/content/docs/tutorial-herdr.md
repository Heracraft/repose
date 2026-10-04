---
title: herdr on your machine
description: Watch every agent on your repose machines from one herdr sidebar on your laptop, and get the layout and the sessions back after a stop.
section: Tutorials
order: 26
status: experimental
---

[herdr](https://herdr.dev) is a terminal multiplexer for coding agents: one sidebar shows which agent is working, which is waiting for you and which is done, across your laptop and any machine you reach over SSH. A repose machine works as one of those machines with nothing from repose beyond SSH. Its agents keep running with the laptop closed, Claude Code is already logged in there, and herdr brings the panes back after `repose stop` and `repose start`.

You need herdr on your laptop ([herdr.dev](https://herdr.dev)) and a project you've run at least once, here `todo-app`.

## Add the machine

```
herdr machine add todo-app.repose
```

herdr asks to install itself on the machine, in `~/.local/bin/herdr`. Answer yes. It copies the same version you run on the laptop, so after a `herdr update` on the laptop it asks again the next time you connect from a terminal. Your home directory survives stops, so the install does too.

## Tell herdr about the agents

herdr reads each agent's state from a small integration installed on the machine. Install one for each agent you use:

```
ssh todo-app.repose 'herdr integration install claude'
ssh todo-app.repose 'herdr integration install opencode'
ssh todo-app.repose 'herdr integration install codex'
```

They add herdr's hooks next to the ones repose put there. Both stay, so herdr shows the agent as blocked or done, and you still get repose's "needs input" and "finished" notifications. The opencode integration works with the machine's `opencode` and with [OpenCode 2](/docs/agents#opencode-2).

## Work in it

Open `herdr` on the laptop. The machine is in the sidebar under its name. Start a workspace there, `cd ~/todo-app`, and run `claude`, `opencode` or `codex` in its panes. Each runs on the machine with the project's dev environment, as in tmux. Close herdr or the laptop and the agents carry on.

## After a stop and start

`repose stop` ends every process on the machine, herdr's server included. After `repose start`, open `herdr` on the laptop. Within about 30 seconds it starts its server on the machine again, puts the workspaces and panes back, and resumes each agent that has an integration in the session it was in. Until a herdr window is open, `herdr machine status` reports the machine as stopped; that's expected. To bring it back from a script instead, run `herdr --remote todo-app.repose` once.

## What changes on a repose machine

- **Claude Code doesn't ask.** It runs in `bypassPermissions` mode on the machine ([Agents](/docs/agents#let-it-run-without-asking)), so herdr seldom shows it as blocked.
- **`repose status` doesn't list herdr's panes.** It shows agents in the project's tmux windows only. A machine whose agents all run under herdr, with no SSH session open, gets the "idle, still billing" notice after 24 hours even while they work. That notice never stops the machine.
- **`repose run "prompt"` and `repose attach` use tmux, not herdr.** A prompt sent with `repose run` starts in a tmux window, where herdr's sidebar doesn't see it.
- **Run herdr on the laptop, not inside `repose attach`.** Both herdr and the machine's tmux use `Ctrl-b`, so herdr inside tmux needs a prefix rebound (`[keys] prefix` in `~/.config/herdr/config.toml`).
- **A restore takes herdr with it.** Its layout lives in `~/.config/herdr` on the machine, so restoring a snapshot puts the workspaces back as they were then.

## When it doesn't work

**The machine stays "error" after a start.** Open a herdr window, or run `herdr --remote todo-app.repose` in a terminal. If you updated herdr on the laptop since, that's also where it asks to install the new version on the machine.

**`machine add` fails with "Bad owner or permissions on /etc/ssh/ssh_config".** herdr writes its own SSH config and runs `ssh -F` on it, which makes `ssh` check that file's owner. A laptop's file passes. If yours doesn't, for example when you run herdr from another repose machine, turn the generated config off and herdr uses your SSH config as it is, repose's connection included:

```toml
# ~/.config/herdr/config.toml
[remote]
manage_ssh_config = false
```
