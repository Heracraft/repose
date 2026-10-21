---
title: Quickstart
description: Install the CLI, start a machine for your project and hand a task to an agent.
section: Start here
order: 1
---
repose gives each project a fresh Linux machine with your code, tools and logins already on it. An agent there runs for hours with full permissions, on a task you handed it and walked away from. If it fucks up, restore from a [snapshot](/docs/lifecycle#snapshots).

You need Linux or macOS (on Windows, use WSL). macOS is supported but it has not been tested enough. [Feedback](https://repose.fider.io) is appreciated.

## 1. Install and log in

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
repose login
```

## 2. Choose a plan

Plans are at [repose.herakraft.co/billing](https://repose.herakraft.co/billing), first week's on us. A plan buys memory by the month, however long your machines run.

## 3. Skip permissions

Claude Code on the machine starts in `bypassPermissions` mode. If your laptop's `~/.claude/settings.json` sets [another `defaultMode`](/docs/agents#permissions), the machine uses yours.

## 4. Let it loose

```
cd ~/code/your-project
repose run -p "write tests for src/billing.ts and commit them"
```

```text
✓ Created your-project (large)  0.3s
✓ Booted your-project  5.2s
Connected to your-project (large)
Synced: 3 modified, 1 untracked (2 new commits)  1.4s
Logins copied: gh
Ready in 16s.
```

The first `run` creates the machine and copies your folder to `/home/dev/your-project`, uncommitted changes and unpushed commits included. Then the CLI starts your default agent in a tmux window, types your prompt and attaches you. Detach with `Ctrl-b` then `d` (inside a tmux on your laptop, `Ctrl-b` `Ctrl-b` `d`) and close the laptop; the agent keeps working.

Claude Code you log into once, on the machine itself; its login is never copied from your laptop. The Codex, opencode and GitHub CLI [logins](/docs/agents) your laptop has are copied, unless you [opt out](/docs/secrets#choose-what-is-copied).

If you use [herdr](https://herdr.dev), add `--multiplexer herdr` to the first run.

## 5. Paste in a screenshot

With a screenshot on your laptop's clipboard, press `Cmd+V` or `Ctrl+V` in the agent's window. Or drag a file onto the terminal. The file is [copied to the machine](/docs/run-and-attach#drop-a-file-or-paste-an-image) and its new path is pasted into the prompt, where Claude Code shows it as an image:

```text
❯ [Image #1] the button overlaps the footer on this screen
```

## 6. Come back and stop

From any computer you're logged in on (`repose ls`):

```
repose attach your-project
```

Once the agent has committed, `git fetch repose` on your laptop brings its commits over, and you merge or rebase them from there ([Getting work back](/docs/sync#getting-work-back)). Or leave `gh` credentials shared and let the agent push straight to GitHub.

Stop the machine when you're done:

```
repose stop
```

Run in your checkout, `repose stop` fetches the agent's commits first, as `git fetch repose` would. Stopping costs nothing; what the project holds counts toward [your plan's disk](/docs/billing#what-a-plan-means). The next `repose run` or `repose attach` starts it again in about 12 seconds with your files where you left them.

## Light and dark

**System**, **Light** and **Dark** at the foot of every page set the theme for this browser; **System** follows your computer's setting.
