---
title: Quickstart
description: Install the CLI, start a machine for your project and hand a task to an agent.
section: Start here
order: 1
---

repose gives each of your projects its own Linux machine in the cloud, with your code, tools and logins already on it. An agent there can run with full permissions for as long as the work takes. The worst it can do is wreck that one machine, and a snapshot puts it back. Your laptop, your SSH keys and your other projects are out of its reach.

You need macOS or Linux (on Windows, use WSL) with `git` and `ssh`, a GitHub account, and a git checkout.

## 1. Install and log in

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
repose login
```

`repose login` prints a link and a code. Open the link on any device, check the code matches and sign in with your email or with GitHub. [Install](/docs/install) has the other ways to install.

## 2. Start a machine for your checkout

```
cd ~/code/your-project
repose run
```

```text
✓ Created your-project (large)  0.3s
✓ Built the environment  6.1s
✓ Booted your-project  5.2s
Connected to your-project (large)
Synced: 3 modified, 1 untracked (2 new commits)
Credentials: gh
Ready in 14s.
```

You're now in a shell on the machine, in `/home/dev/your-project` (the checkout takes your laptop folder's name), with your uncommitted changes and unpushed commits applied. The shell runs inside tmux.

## 3. Log in to Claude Code on the machine

Claude Code's login is never copied from your laptop, so you log in once, on the first machine. Type `claude`, open the URL it prints, approve, and paste the code back. Your other machines are then logged in too.

Codex, opencode and GitHub CLI logins were copied from your laptop in step 2. [Agents](/docs/agents) covers the rest.

## 4. Let it run without asking

Claude Code on the machine starts in `bypassPermissions` mode, so it doesn't stop to ask before running a command. If your laptop's `~/.claude/settings.json` sets another `defaultMode`, the machine uses yours. [Agents](/docs/agents#let-it-run-without-asking) has the details.

## 5. Hand it a task

Detach from tmux with `Ctrl-b` then `d`. Back on your laptop:

```
repose run "write tests for src/billing.ts and commit them"
```

The CLI starts Claude Code in a new tmux window on the machine, types your prompt and attaches you. Detach and close the laptop; the agent keeps working.

## 6. Show it a screenshot

With a screenshot on your laptop's clipboard, press `Cmd+V` or `Ctrl+V` in the agent's window. Or drag a file onto the terminal. The file is copied to the machine and its path lands in the prompt, where Claude Code shows it as an image:

```text
❯ [Image #1] the button overlaps the footer on this screen
```

[Drop a file or paste an image](/docs/run-and-attach#drop-a-file-or-paste-an-image) has the limits.

## 7. Get a notification when it's done

Email is on by default. For your phone, pick a long random [ntfy](https://ntfy.sh) topic, subscribe to it in the ntfy app, then:

```
repose notify set --ntfy https://ntfy.sh/repose-4f9c2a7e1b
repose notify test
```

## 8. Come back and stop

From any computer you're logged in on (`repose ls` lists your projects):

```
repose attach your-project
```

When the agent has committed, `git fetch repose` in your checkout brings its commits to your laptop, and `git merge repose/main` takes them ([Getting work back](/docs/sync#getting-work-back)). Nothing syncs back on its own. Stop the machine when you're done:

```
repose stop
```

A stopped machine costs only its disk. The next `repose run` starts it again in about 10 seconds with your files where you left them. Running processes, agents included, don't survive a stop.
