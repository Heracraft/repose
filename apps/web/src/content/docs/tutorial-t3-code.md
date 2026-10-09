---
title: T3 Code on your machine
description: Run T3 Code's server on the machine and drive Claude Code and Codex threads from a browser or your phone, with the laptop closed.
section: Tutorials
order: 25
status: experimental
---

[T3 Code](https://github.com/pingdotgg/t3code) is an open-source app for running coding agents as threads, each with its diff and its own worktree. On a repose machine Claude Code is already logged in, the threads keep going after you close the laptop, and a stop, start or restore keeps them. You need nothing from repose beyond SSH.

## Install the server

Install T3 Code as a background service on the machine:

```
ssh todo-app.repose 'npx -y t3@latest service install'
```

The service is a systemd user unit, so it comes back by itself after `repose stop` and `repose start`. It listens on `localhost:3773` on the machine, and only there.

## Open it on your laptop

While you're attached, the port is already on your laptop: open http://localhost:3773. When you aren't, forward it:

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

Open that URL on the laptop within 5 minutes. The note it prints about the URL being reachable only from the machine doesn't apply here, because `repose open` puts the same port on your laptop. T3 Code uses the machine's Claude Code and Codex, with the logins `repose run` copied and the Claude login you made on any of your machines. Add your checkout as a project (`/home/dev/todo-app`) and start a thread.

To reach it from your phone, use T3 Code's own T3 Connect or `t3 pair --tailscale`. repose has no public URL to offer.

The T3 Code desktop app can also add the machine as an SSH computer, `todo-app.repose`. It uses your `ssh`, so the certificate renews by itself. We have tested the browser route above, and not yet the desktop app's.

## What changes on a repose machine

- **Claude Code doesn't ask.** T3 Code starts the machine's `claude`, which runs in `bypassPermissions` mode ([Agents](/docs/agents)). T3 Code's approval settings don't make it ask. For Codex, T3 Code's settings apply.
- **You still get notified.** A Claude Code or Codex turn in T3 Code sends the same "finished" notification as one in tmux.
- **`repose status` doesn't list T3 Code's threads.** It shows agents in the project's tmux windows only. A machine whose agents all run under T3 Code, with no SSH session open, gets the "unused for 24h" notice even while threads are working. That notice never stops the machine.
- **Ignore T3 Code's "Updates available" notice for Claude and Codex.** The agents come with the machine and update with the platform ([Installing software](/docs/config)); T3 Code leaves them alone.
- **A restore takes T3 Code with it.** Its threads and pairing live in `~/.t3` on the machine, so restoring a snapshot puts them back as they were then.

## Disk

Each T3 Code update keeps the old server under `~/.t3/runtime/versions`. Check with:

```
ssh todo-app.repose 'du -sh ~/.t3/runtime/versions'
```

`npx -y t3@latest uninstall` removes the service, the launcher and every downloaded version, and keeps your projects and threads.

## When it doesn't work

**The page doesn't load.** Check the service on the machine with `ssh todo-app.repose 'systemctl --user status t3code'`, and that `repose open 3773` is still running on your laptop.

**A Codex thread answers but says the command host is unavailable.** Bases up to 2026.10.03.1 shipped Codex without the part it runs commands through. The next platform update fixes it; if the project's Config page has **Hold base updates** ticked, untick it.
