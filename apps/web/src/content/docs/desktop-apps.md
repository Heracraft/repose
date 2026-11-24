---
title: Desktop apps
description: Open a machine in Claude Desktop, T3 Code, the Codex app, Antigravity, Zed, VS Code or Cursor, over your own SSH.
section: Using repose
order: 10.6
---

Every project is an SSH host, `<project>.repose` ([SSH and editors](/docs/ssh-and-editors)). An app that connects with your own `ssh` reaches it: no port to forward, no server to keep running on your laptop. The app installs what it needs on the machine the first time it connects.

## Claude Desktop

On the project's page, **Open in Claude** opens a new session in Claude Desktop's Code tab, on the machine, in its checkout. Desktop asks you to confirm the connection. A stopped machine starts first. A frozen one wakes when Desktop connects.

From a terminal:

```
repose code --editor claude todo-app
```

By hand: in the Code tab, add an SSH environment with the host `todo-app.repose`.

Desktop needs v2.110.0 or later for the link, on macOS or Linux.

- **Disk.** Desktop installs its own Claude Code under `~/.claude/remote` on the machine. Claude's docs ask for about 1 GB, which counts against your [disk](/docs/limits#disk-and-console).
- **Notifications.** Desktop's Claude Code reads the machine's `~/.claude/settings.json`, so a finished turn shows in `repose events` and [notifies you](/docs/notifications) as one in tmux does. It doesn't show in `repose status`, which lists the agents in the project's tmux windows.
- **Overnight.** A Desktop task can stop when Desktop's sign-in for it lapses, which Claude's docs say can be minutes after Desktop disconnects. Start work that must run overnight with `repose run -p` or in `repose attach`.
- **A stopped machine.** A saved environment that you pick in Desktop doesn't start the machine, because `ssh` can't. Use **Open in Claude** or `repose start`.
- **The folder.** The link opens the checkout that `repose run`, `sync` or `code` last found on the machine. A project made on the dashboard, with no run from a laptop yet, opens in the home folder.

## Other apps

Each app adds the machine by its host name. Type the name from your project.

| App | How to add the machine | Tested in the app |
| --- | --- | --- |
| T3 Code (desktop) | Settings, Environments, Add environment, SSH: `todo-app.repose`. Then add the folder `/home/dev/todo-app`. [Tutorial](/docs/tutorial-t3-code) | Not yet |
| Codex app | Settings, Connections, then pick `todo-app.repose` | Not yet. Codex lists the hosts in `~/.ssh/config`; repose's are in a file that `~/.ssh/config` includes |
| Antigravity | Connect over SSH: `todo-app.repose` | Not yet |
| Zed | `repose code --editor zed todo-app` | Yes |
| VS Code, Cursor | `repose code todo-app` | Yes |

On every machine, measured on 2026-11-24 on the Ubuntu base: the SSH server offers SFTP, a program copied into `~/.claude/remote` runs, a process started over SSH keeps running after the connection closes, port forwarding is on, and the machine reaches the internet over HTTPS.

## Windows

An app installed on Windows connects with Windows' own `ssh`, which doesn't read the `~/.ssh` in WSL where repose writes the host and the certificate. It can't reach a machine yet ([Windows](/docs/ssh-and-editors#windows)).
