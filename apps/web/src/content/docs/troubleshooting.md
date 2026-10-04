---
title: Troubleshooting
description: The errors you're most likely to see and what fixes each one.
section: Reference
order: 41
---

For more detail on any command, add `-v`. `REPOSE_TIMING=1` shows where the time in `run` and `attach` went, and `repose logs --kind ops` lists every operation on the project with its result.

## Logging in and connecting

**``Not logged in. Run `repose login`.``** Your login expired or you logged out. Run `repose login`.

**`No repose project for github.com/you/app`** This checkout has no project yet, or its remote changed. `repose run` creates one; `repose attach NAME` reaches an existing one.

**`ssh todo-app.repose` says `Could not resolve hostname`.** If a line before it says ``Not logged in. Run `repose login`.``, log in and connect again. With no other message, ssh isn't reading the file repose writes; see [When it doesn't connect](/docs/ssh-and-editors#when-it-doesnt-connect).

**`ssh todo-app.repose` says `Permission denied`.** The certificate couldn't be renewed, most often because you're logged out: run `repose login`, then connect again. `repose attach todo-app` also renews it.

**`too many open connections for your account; close some and try again`.** You have 32 SSH connections open through the gateway, across all your projects. A script that opens connections without closing them is the usual cause. See [SSH connections](/docs/limits#ssh-connections).

**`kex_exchange_identification: Connection closed by remote host`.** Your address opened more connections at once than the gateway lets in, or failed to log in many times in a row; `ssh -v` shows the gateway's reason on a line starting `repose gateway:`. Wait a minute and connect again. After many failed logins, the wait is 10 minutes.

**Your connection closed on its own after hours.** A connection ends when its certificate expires, at most 24 hours after it was issued, or when you run `repose logout` on another device. Connect again; your `ssh` renews the certificate. The tmux session on the machine is still there. `run` and `attach` do this for you.

**`repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.`** The connection dropped: Wi-Fi, a laptop that slept, or a restart on repose's side. The machine is still running. `run` and `attach` attach again as soon as it answers.

**``repose: could not reach todo-app for 2 minutes. `repose attach todo-app` attaches again once it answers.``** The connection didn't come back within 2 minutes. Check your network, then run `repose attach todo-app`. `repose status todo-app` shows whether the machine is running.

**Your editor can't connect to `todo-app.repose`.** Run `ssh todo-app.repose true` in a terminal. It shows the same error the editor got, with the reason. A stopped machine says ``todo-app is stopped; run `repose start todo-app` ``; connecting never starts one.

**`Guest is running but SSH did not answer in 60s.`** `repose logs --kind console` shows the boot log. `repose start` restarts a stuck machine.

## Machine state

**``todo-app is stopped. Start it with `repose start todo-app` ...``** `attach`, `ssh`, `exec`, `code`, `open` and `cp` don't start a stopped machine, and neither does a plain `ssh todo-app.repose`. Run `repose start todo-app`, or `repose run` in the checkout.

**`todo-app is in an error state`** or **`guestd stopped answering`.** `repose start todo-app` restarts it. If that fails, `repose logs todo-app --kind console` shows what happened during boot.

**`todo-app is being destroyed`.** `attach` and the other commands can't reach a machine that's going away. `repose run` in the checkout waits for the destroy, then creates a fresh `todo-app`; see [Start over with a fresh machine](/docs/lifecycle#start-over-with-a-fresh-machine).

**`The claude window closed before the attach`.** The agent `repose run "..."` started exited before you were attached, so you're in the machine's session instead. Start the agent again there, for example by typing `claude`. If it exits right away again, running it by hand shows why.

**`No capacity right now`** or **`Could not create todo-app: no host with capacity`.** The servers are full. A start changes nothing. A new project is left in the error state with no machine; `repose ls` lists it, and `repose start todo-app` or `repose run` in the checkout creates its machine once there's room. Try again in a few minutes. Right after `repose rm` or `repose stop` of another machine, a new one waits up to three minutes for that machine's memory instead of failing.

**`repose is full right now`.** Every seat is taken. Join the waitlist from the dashboard's [Billing page](https://repose.herakraft.co/billing); you're emailed when a seat frees, with 72 hours to choose a plan. See [When repose is full](/docs/limits#when-repose-is-full).

## Sync

**`The machine has uncommitted changes your laptop doesn't have`.** Something on the machine, usually an agent, changed files since your last sync, and your laptop has new work that would write over them. `repose attach` to look, or run `repose sync --stash-remote` to keep them in `git stash` or `repose sync --discard-remote` to drop them. `repose run` never syncs over a machine that already has your checkout, so it attaches either way. See [Sync](/docs/sync#when-the-machine-has-changes-of-its-own).

**`git fetch repose` fails.** With ``todo-app is stopped; run `repose start todo-app` ``, the machine is stopped: start it and fetch again. With `Permission denied`, see the `ssh todo-app.repose` entry above; anything that works for `ssh` works for the fetch. With `does not appear to be a git repository`, the machine has no checkout yet: `repose run` makes one. If `git remote` doesn't list `repose` at all, run `repose run` or `repose attach` in the checkout, or see [Getting work back](/docs/sync#getting-work-back) for a remote of that name you already had.

**`git push repose` fails with `this remote is fetch-only`.** The remote only brings work back. `repose sync` sends your work to the machine.

**`Not synced: your laptop has work the machine doesn't`.** `repose run` copies your checkout only into a new machine. Run `repose sync` to send the rest.

**`Not sent: web/node_modules`.** Dependency directories never travel. Run your install command on the machine.

**A big file wasn't sent.** Files over 100 MB and untracked files past 500 MB per sync are skipped. Commit what matters, or add it to `.gitignore`.

## On the machine

**A command isn't found.** The machine prints the nixpkgs package that has it and the two ways to add it. If it says the tool `is still being installed`, it's one of your laptop's tools arriving in the background; try again shortly.

**A tool from your laptop didn't arrive.** The next `repose run` names it. The log is `~/.repose/tools-install.log` on the machine. `repose scan` shows what the CLI looked for.

**A program you installed isn't on `PATH`.** Installs with npm, pnpm, `go install`, `cargo install`, uv, pip `--user`, bun, deno, gem and composer are on `PATH` in new shells. Open a new tmux window. Tools that manage `PATH` from their own shell setup (nvm, pyenv, rbenv) need that setup in `~/.bashrc`.

**Processes get killed, or the machine is slow under load.** It ran out of memory: `sudo dmesg | grep -i killed` names what the kernel stopped. Stop what you don't need (`repose status` lists dev servers still listening), or give the machine more memory with `repose resize --size large` or `--size xl`, which restarts it. See [Changing the size](/docs/machine#changing-the-size).

**A secret isn't in a program's environment.** Programs read their environment when they start. Open a new tmux window, or restart the program or agent.

**`config error` or exit code 10.** The build failed and nothing changed. The message names the problem; `repose logs --kind build` has the full log. Fix it with `repose config edit` or `repose config remove`.

## Agents

**`Claude Code is not logged in on this guest yet.`** Finish the login in the window the CLI opened, then run your prompt again. See [Agents](/docs/agents#log-in).

**An image you dropped or pasted didn't attach.** See [Drop a file or paste an image](/docs/run-and-attach#drop-a-file-or-paste-an-image).

- Cmd+V did nothing on a Mac: you started with `REPOSE_CLIPBOARD_PATH=0`, or a CLI older than v0.1.22, and a terminal sends nothing for Cmd+V when the clipboard holds only an image. Press Ctrl+V, which reads the clipboard itself.
- Your laptop's path appeared, not the machine's: the file was over 20 MB, you dropped more than 20 files, or the copy failed, and the tmux status line said which. `REPOSE_INPUT_PROXY=0` and Windows paste the laptop's path too.
- Ctrl+V did nothing on Linux: the status line names the tool to install, `wl-clipboard` or `xclip`. When `repose` itself runs on a computer you reached over SSH, it has no clipboard to read.
- The machine's path appeared as text: Claude Code attaches images only; for another file it gets the path, which it can open.

**`this CLI has no complete local package` when you start `codex`.** Codex on bases up to 2026.10.04.1 is missing the files its background server needs. The next platform update fixes it in place; you don't need to do anything. Until then, start it with `codex --no-daemon`.

**An agent seems stuck.** Attach and look; it's usually waiting on a permission prompt. See [Let it run without asking](/docs/agents#let-it-run-without-asking).

## Ports

**My dev server isn't on localhost.** Check that:

- you're attached (`repose run` or `repose attach`);
- the server listens on `127.0.0.1`, `localhost` or `0.0.0.0`, on port 1024 or higher;
- `REPOSE_NO_FORWARD` isn't set;
- it didn't move to another port because yours was taken (tmux shows the new one).

`repose open PORT` forwards one port by hand.

## Notifications

**Nothing arrives.** Run `repose notify test`. An `error` means that channel's settings are wrong. If both are `ok`, `repose events` shows whether the event happened; a project sends at most 30 notifications an hour.

## Something else

Post a bug or an idea on the [feedback board](https://repose.fider.io), or vote on one that's already there. Sign in with **repose account**, the same email or GitHub login you use for `repose login`. What you post is public, so leave out secrets, tokens and private code.
