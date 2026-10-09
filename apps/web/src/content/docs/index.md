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

## 2. Choose a plan

Choose a plan at [repose.herakraft.co/billing](https://repose.herakraft.co/billing); the first week is free. Run from a terminal on an account with no plan, `repose run` opens that page and waits, then goes on once you have one. Solo runs 8 GB at once: one `large`, or two `small`. [Pricing](/docs/billing) compares the plans.

## 3. Let it run without asking

Claude Code on the machine starts in `bypassPermissions` mode, so it doesn't stop to ask before running a command. If your laptop's `~/.claude/settings.json` sets another `defaultMode`, the machine uses yours. [Agents](/docs/agents#let-it-run-without-asking) has the details.

## 4. Hand it a task

```
cd ~/code/your-project
repose run -p "write tests for src/billing.ts and commit them"
```

```text
✓ Created your-project (large)  0.3s
✓ Built the configuration  6.1s
✓ Booted your-project  5.2s
Connected to your-project (large)
Synced: 3 modified, 1 untracked (2 new commits)
Logins copied: gh
Ready in 16s.
```

The first run creates the machine and copies your checkout to `/home/dev/your-project` on it (the checkout takes your laptop folder's name), with your uncommitted changes and unpushed commits applied. Then the CLI starts Claude Code in a tmux window there, types your prompt and attaches you. Detach with `Ctrl-b` then `d` (inside a tmux on your laptop, `Ctrl-b` `Ctrl-b` `d`) and close the laptop; the agent keeps working.

Claude Code's login is never copied from your laptop, so the first time, the window opens on its login: open the URL it prints, approve, and paste the code back. Your prompt is typed once the login is done, and your other machines are logged in too. Codex, opencode and GitHub CLI logins were copied from your laptop; [Agents](/docs/agents) covers the rest.

`repose run` with no prompt attaches to a shell on the machine instead. If you use [herdr](https://herdr.dev), add `--multiplexer herdr` to the first run and the machine runs herdr ([herdr on your machine](/docs/tutorial-herdr)).

## 5. Show it a screenshot

With a screenshot on your laptop's clipboard, press `Cmd+V` or `Ctrl+V` in the agent's window. Or drag a file onto the terminal. The file is copied to the machine and its path lands in the prompt, where Claude Code shows it as an image:

```text
❯ [Image #1] the button overlaps the footer on this screen
```

[Drop a file or paste an image](/docs/run-and-attach#drop-a-file-or-paste-an-image) has the limits.

## 6. Get a notification when it's done

Email is on by default. For your phone, pick a long random [ntfy](https://ntfy.sh) topic, subscribe to it in the ntfy app, then:

```
repose notify set --ntfy https://ntfy.sh/repose-4f9c2a7e1b
repose notify test
```

## 7. Come back and stop

From any computer you're logged in on (`repose ls` lists your projects):

```
repose attach your-project
```

When the agent has committed, `git fetch repose` in your checkout brings its commits to your laptop, and `git merge repose/main` takes them ([Getting work back](/docs/sync#getting-work-back)). Stop the machine when you're done:

```
repose stop
```

Run in your checkout, `repose stop` fetches the agent's commits first, as `git fetch repose` would. A stopped machine costs nothing; what its disk holds counts toward your plan's disk. The next `repose run` or `repose attach` starts it again in about 10 seconds with your files where you left them. Running processes, agents included, don't survive a stop.
