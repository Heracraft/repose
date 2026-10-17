---
title: T3 Code on your machine
description: Run T3 Code's server on the machine and drive Claude Code and Codex threads from a browser or your phone, with the laptop closed.
section: Tutorials
order: 25
status: experimental
---

[T3 Code](https://github.com/pingdotgg/t3code) is an open-source app for running coding agents as threads, each with its diff and its own worktree. On a repose machine the threads run the machine's own Claude Code and Codex, keep going after you close the laptop, and survive a stop, start or restore.

## Install the server

```
ssh todo-app.repose 'npx -y t3@latest service install'
```

The service is a systemd user unit, so a `repose stop` and `repose start` brings it back. It listens on `localhost:3773` on the machine, and only there.

## Open it on your laptop

While you're attached, [the port is already on your laptop](/docs/machine#ports): open http://localhost:3773. If you aren't, forward it:

```
repose open 3773 --project todo-app
```

T3 Code asks to be paired the first time a browser connects. Get a pairing link from the machine:

```
ssh todo-app.repose 'npx -y t3@latest pair'
```

```text
Pairing with todo-app (http://127.0.0.1:3773).
Pairing URL: http://localhost:3773/pair#token=...
Expires: ...
```

Open that URL on the laptop before it expires. Ignore its note about the URL reaching only the machine; `repose open` put the same port on your laptop. The threads use the machine's agents, with their [copied logins](/docs/secrets#logins-copied-from-your-laptop) and the [Claude login](/docs/agents#log-in) you made on any of your machines. Add your checkout as a project (`/home/dev/todo-app`) and start a thread.

To reach it from your phone, use T3 Code's own T3 Connect or `t3 pair --tailscale`. repose has no public URL to offer.

The T3 Code desktop app can add the machine as an SSH computer, `todo-app.repose`; it uses your own `ssh`, so [the certificate renews by itself](/docs/ssh-and-editors#how-it-works). We've tested the browser route above and not the desktop app's.

## What changes on a repose machine

- **Claude Code doesn't ask.** T3 Code starts the machine's `claude`, which runs in [`bypassPermissions` mode](/docs/agents#permissions), and T3 Code's approval settings don't change that. For Codex, T3 Code's settings apply.
- **You still get notified.** A question from a thread, or its [`repose-notify --done`](/docs/notifications#agents-can-message-you-and-ask-questions), notifies you as one in tmux does.
- **`repose status` doesn't list T3 Code's threads.** It shows agents in the project's tmux windows only. A machine whose agents all run under T3 Code, with no SSH session open, gets the ["unused for 24h"](/docs/lifecycle#unused-machines) notice even while threads are working.
- **Ignore T3 Code's "Updates available" notice for Claude and Codex.** The agents come with the machine and update with [its base](/docs/config#revisions-and-base-updates); T3 Code leaves them alone.
- **A restore takes T3 Code with it.** Its threads and pairing live in `~/.t3` on the machine, so restoring a [snapshot](/docs/lifecycle#snapshots) puts them back as they were then.

## Disk

Each T3 Code update keeps the old server under `~/.t3/runtime/versions`. Check with:

```
ssh todo-app.repose 'du -sh ~/.t3/runtime/versions'
```

`npx -y t3@latest uninstall` removes the service, the launcher and every downloaded version, and keeps your projects and threads.

## When it doesn't work

**The page doesn't load.** Check the service on the machine with `ssh todo-app.repose 'systemctl --user status t3code'`, and that `repose open 3773` is still running on your laptop.

**A Codex thread answers but says the command host is unavailable.** Older bases shipped Codex without the part it runs commands through. A newer base has it; if the project's Config page has [**Hold base updates**](/docs/config#revisions-and-base-updates) ticked, untick it.
