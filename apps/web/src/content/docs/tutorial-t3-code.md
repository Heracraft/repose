---
title: T3 Code on your machine
description: Add the machine to T3 Code as an SSH environment and drive Claude Code and Codex threads on it, with the laptop closed.
section: Tutorials
order: 25
status: experimental
---

[T3 Code](https://github.com/pingdotgg/t3code) is an open-source app for running coding agents as threads, each with its diff and its own worktree. On a repose machine the threads run the machine's own Claude Code and Codex, keep going after you close the laptop, and survive a stop, start or restore.

## Add the machine to T3 Code

In the T3 Code desktop app, open **Settings**, then **Environments**, then **Add environment**, and choose **SSH**. Type the host `todo-app.repose`. T3 Code connects with your own `ssh`, so [the certificate renews by itself](/docs/ssh-and-editors#how-it-works). It installs its server on the machine under `~/.t3/runtime`, starts it, and forwards its port for you.

Add your checkout as a project, `/home/dev/todo-app`, and start a thread. The threads use the machine's agents, with their [copied logins](/docs/secrets#logins-copied-from-your-laptop) and the [Claude login](/docs/agents#log-in) you made on any of your machines.

A stopped machine doesn't start when T3 Code connects. Start it with **Start** on the project's page, or `repose start todo-app`.

We haven't tested this route in the app yet. The machine side is tested: [Desktop apps](/docs/ssh-and-editors#other-apps).

## From a browser

With no desktop app, run T3 Code's server as a service on the machine and open it through a forwarded port:

```
ssh todo-app.repose 'npx -y t3@latest service install'
```

The service is a systemd user unit, so a `repose stop` and `repose start` brings it back. It listens on `localhost:3773` on the machine, and only there.

While you're attached, [the port is already on your laptop](/docs/machine#ports): open http://localhost:3773. If you aren't, forward it, and keep the command running while you use the page:

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

Open that URL on the laptop before it expires. Ignore its note about the URL reaching only the machine; `repose open` put the same port on your laptop.

To reach it from your phone, use T3 Code's own T3 Connect or `t3 pair --tailscale`. repose has no public URL to offer.

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

**The desktop app can't connect.** Check that the machine runs: `repose status todo-app`. Then check that `ssh todo-app.repose true` works in a terminal; T3 Code uses the same `ssh`.

**The page doesn't load (browser route).** Check the service on the machine with `ssh todo-app.repose 'systemctl --user status t3code'`, and that `repose open 3773` is still running on your laptop.

**A Codex thread answers but says the command host is unavailable.** Older bases shipped Codex without the part it runs commands through. A newer base has it; if the project's **Config** tab has [**Hold base updates**](/docs/config#revisions-and-base-updates) ticked, untick it.
