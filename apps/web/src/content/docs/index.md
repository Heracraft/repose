---
title: Quickstart
description: Install the CLI, start a machine for your project and hand a task to an agent.
section: Start here
order: 1
---
repose gives each project a fresh Linux machine with preconfigured with your code, tools and logins. An agent there can run for hours with full permissions to complete a hard task. If it fucks up, restore from a snapshot. Your daily driver and the private content inside it are out of reach. More importantly, you can offload long running tasks that require hours to complete and go exist. 

You need Linux or macOS (on Windows, use WSL). macOS is supported but it is has not been tested enough, [feedback](repose.fider.io) is appreciated.  
## 1. Install and log in

```
curl -fsSL https://repose.herakraft.co/install.sh | sh
repose login
```

## 2. Choose a plan

Plans are at [repose.herakraft.co/billing](https://repose.herakraft.co/billing), first week's on us. Plans are organized around memory, Solo for example, gives you 8 GB of machines running at the same time, around the clock: one `large`, or two `small`. You can keep up to 100 projects on any plan, just stop the ones that aren't doing a task right now.

## 3. Skip permissions

Claude Code on the machine starts in `bypassPermissions` mode. If your laptop's `~/.claude/settings.json` sets [another `defaultMode`](/docs/agents#let-it-run-without-asking), the machine uses yours.

## 4. Let it loose

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

The first `run` creates the machine and copies your folder to `/home/dev/your-project` onto it, with your uncommitted changes and unpushed commits applied. Then the CLI starts your default agent in a tmux window there, types your prompt and attaches you. Detach with `Ctrl-b` then `d` (inside a tmux on your laptop, `Ctrl-b` `Ctrl-b` `d`) and go chill; the agent keeps working.

For Claude Code, you'd have login to login on your first time using it. The Codex, opencode and GitHub CLI [logins](/docs/agents) your laptop has are copied, unless you [opt out](/docs/secrets#choose-what-is-copied). 

If you use [herdr](https://herdr.dev), add `--multiplexer herdr` to the first run

## 5. Paste in a screenshot

With a screenshot on your laptop's clipboard, press `Cmd+V` or `Ctrl+V` in the agent's window. Or drag a file onto the terminal. The file is [copied to the machine](/docs/run-and-attach#drop-a-file-or-paste-an-image) and it's new path is pasted into the prompt, where Claude Code shows it as an image:

```text
❯ [Image #1] the button overlaps the footer on this screen
```

%%## 6. Get a notification when it's done

Email is on by default. For your phone, pick a long random [ntfy](https://ntfy.sh) topic, subscribe to it in the ntfy app, then:

```
repose notify set --ntfy https://ntfy.sh/repose-4f9c2a7e1b
repose notify test
```
%%
## 7. Come back and stop

From any computer you're logged in on (`repose ls`):

```
repose attach your-project
```

Once the agent has committed, `git fetch repose` on your laptop fetches its commits, from there you can merge, rebase or whatever ([Getting work back](/docs/sync#getting-work-back)). Or simply leave `gh` credentials shared and let the agent push straight to GitHub. 

Stop the machine when you're done:

```
repose stop
```

Run in your checkout, `repose stop` fetches the agent's commits first, as `git fetch repose` would. A stopped machine is only billed for it's disk. The next `repose run` or `repose attach` starts it again in about 15 seconds with your files where you left them.