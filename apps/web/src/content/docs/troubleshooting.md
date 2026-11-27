---
title: Troubleshooting
description: The errors you're most likely to see and what fixes each one.
section: Reference
order: 41
---

For more detail on any command, add `-v`; its request IDs are what support looks up. [`REPOSE_TIMING=1`](/docs/cli#environment-variables) and [`repose logs --kind ops`](/docs/cli#repose-logs-project) are the other two places to look.

## Logging in and connecting

**``Not logged in. Run `repose login`.``** Your login expired or you logged out.

**The dashboard says `The dashboard needs JavaScript.`** Your browser is blocking scripts for repose.run. The dashboard is drawn in the browser, so it needs them. The CLI needs none.

**`Could not reach the login server (...)`** or **`Could not reach api.repose.run`.** Your laptop is offline, or the server is down; run the command again once you're connected.

**`Repose had a problem on its side. Try again in a minute.`** Repose's api is restarting, usually for a release, or failed; the line ends with an id to give support when it keeps happening. A read is sent once more by itself; a command that changes something is not, so run it again.

**`ssh on this laptop failed before it reached todo-app`.** Your laptop's ssh refused before it got to the gateway: a line in `~/.ssh/config`, a `ControlPath` too long for a socket, or `~/.ssh/repose/known_hosts`. The line quotes ssh and names the fix when it knows one.

**`Host key verification failed`, or ssh says `Certificate invalid: name is not a listed principal`.** The gateway's host key or the name repose reaches it by changed. Delete `~/.ssh/repose/known_hosts`; the next repose command writes it again.

**`No repose project for github.com/you/app`.** This checkout has no project yet, or its remote changed. `repose run` creates one; `repose attach NAME` reaches an existing one.

**`ssh todo-app.repose` says `Could not resolve hostname`.** With ``Not logged in. Run `repose login`.`` on a line before it, log in and connect again. With no other message, ssh isn't reading [the file repose writes](/docs/ssh-and-editors#when-it-doesnt-connect).

**``repose: No repose project is called todo-app. `repose ls` lists yours.``** The host name is misspelt, or the project was destroyed.

**`ssh todo-app.repose` says `Permission denied`.** The certificate couldn't be renewed, most often because you're logged out: run `repose login`, then connect again. `repose attach todo-app` renews it too.

**`too many open connections for your account; close some and try again`.** You have 32 [SSH connections](/docs/limits#ssh-connections) open through the gateway, across all your projects. A script that opens connections without closing them is the usual cause.

**`kex_exchange_identification: Connection closed by remote host`, or `read: Connection reset by peer`.** Your address opened more connections at once than the gateway lets in, or failed to log in many times in a row. `ssh -v` shows the gateway's reason on a line starting `repose gateway:`. Wait a minute, or 10 minutes if it was the logins.

**Your connection closed on its own after hours.** Its certificate expired, or a `repose logout` closed it ([SSH connections](/docs/limits#ssh-connections)). Connect again and your `ssh` renews the certificate. The session on the machine is as you left it.

**`repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.`** The connection dropped: Wi-Fi, a laptop that slept, or [the machine's server restarting](/docs/lifecycle#when-a-machines-server-restarts). `run` and `attach` attach again as soon as the machine answers.

**`attach` pauses for a second or two before it connects.** After the laptop slept or changed networks, `attach` spends up to 2 seconds finding that the old connection is dead, then connects as usual.

**``repose: could not reach todo-app for 2 minutes. `repose attach todo-app` attaches again once it answers.``** The connection didn't come back within 2 minutes. Check your network, then run `repose attach todo-app`; `repose status todo-app` says whether the machine is running.

**Your editor can't connect to `todo-app.repose`.** Run `ssh todo-app.repose true` in a terminal. It fails the same way the editor did, with the reason. A stopped machine says ``todo-app is stopped; run `repose start todo-app` ``.

**`repose gateway: too many authentication attempts from your address; try again later`.** Your address failed to log in 20 times in 10 minutes without a repose certificate, usually an ssh run with another key or an old `~/.ssh/config` entry. Every connection from that address is refused for 10 minutes. Connections refused because your machine is stopped or gone, or because repose couldn't reach its own control plane, don't count.

**`repose gateway: certificate not valid for this project`.** The certificate on your laptop is for another project or another account handle: a project removed and made again has a new id, and so does a renamed handle. Any `repose` command gets a new certificate and connects; plain `ssh` and your editor don't, so run `repose ssh todo-app` once first.

**`todo-app is on a host your account does not own`.** The machine is placed on hardware another account added, which repose never does on its own. Nothing on your side fixes it; write to support with the project's name.

**`repose gateway: gateway cannot reach control plane; try again shortly`.** repose can't reach the service that checks your certificate, so it can't let anyone in. Your machine keeps running and connections already open keep working. Connect again in a few minutes.

**`repose gateway: gateway busy`.** The gateway is at its limit for connections being set up, which has nothing to do with your account or your certificate. Connect again.

**`todo-app is running but did not answer ssh in 15 s.`** `repose stop` and then `repose start` restart it. A command that has just started or created the machine waits 60 s instead. If the line under it says the machine is frozen, read the frozen entry below first: a stop there ends processes that are still running.

## Machine state

**``todo-app is stopped. `repose start todo-app` starts it.``** `exec`, `ps`, `cp`, `open`, `mcp forward` and `browser bridge` don't start a stopped machine; [the ones that do](/docs/lifecycle#stop-and-start) start it for you.

**``todo-app is stopping. `repose start todo-app` waits for the stop and starts it again.``** The commands that don't start a stopped machine don't wait out a stop either.

**A machine that was running is stopped, and you didn't stop it.** Its server restarted: `repose events todo-app` shows `server restarted`. [When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts) says what repose starts again by itself and what it doesn't. `repose start todo-app` starts it.

**``todo-app's server is not answering; machines that were running on it start again by themselves when it is back, and `repose start todo-app` works then``.** The machine's server is down or unreachable. The machine's disk is on that server, so nothing can start it elsewhere without losing the work since its last snapshot.

**Machines on your own Mac started cold after a restart.** The Mac was restarted some other way than through repose: the Apple menu, a macOS update, a power cut or a crash. macOS ends every virtual machine the moment a restart begins, so their memory is lost and they start fresh with their disks as they were. `repose host restart` on the Mac, or **Restart…** on Settings → Hosts, parks them first ([Restart your Mac through repose](/docs/own-hardware#restart-your-mac-through-repose-and-your-machines-keep-their-memory)).

**`todo-app: parked; it waits for you to log in to mac-mini, and its memory is kept`.** `repose ls` shows `running parked`. The Mac restarted through repose and nobody has logged in since: macOS reads a parked machine's memory only in your own session. Log in to the Mac and it runs again where it was. `repose start` starts nothing meanwhile, because starting it would throw that memory away. After six hours the memory file is deleted and the machine stops; `repose start` then starts it fresh.

**`todo-app: parked; it waits for you to unlock mac-mini, and its memory is kept`** (or **`resting; it wakes once you unlock mac-mini`**). The Mac is logged in but its screen is locked, and macOS reads a machine's saved memory only while it is unlocked. Unlock the Mac and the machine runs again where it was. A Mac that restarted while its screen was locked logs in locked even with automatic login on.

**`This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again.`** From `repose host restart` or the dashboard's **Restart…**: macOS saves a machine's memory only while the Mac is unlocked. Unlock it and restart again, or restart anyway (`--force`, **Restart anyway**) and the machines start fresh.

**``todo-app: not answering for 4m. `repose start todo-app` restarts it; what was in its memory is lost.``** `repose ls` shows `running not answering`; on the dashboard the row, the project's header and its dot in the sidebar say `running, not answering`, the header's **Restart** takes the place of **Stop**, and **Settings → Hosts** counts it under the host. repose's service on the machine has not answered for about three minutes, three samples in a row a minute apart, so one slow minute never shows. The machine is hung: its memory is full, or a program in it has taken every core. SSH to it fails too. `repose start todo-app` restarts it, and nothing does it for you, because what was in its memory is lost; its disk is kept. `repose events todo-app` has a `not answering` line for it, and no notification is sent. On [your own Mac](/docs/own-hardware#what-fits-on-a-mac), stop a machine you are not using first.

**`todo-app is in an error state: ...`** The rest of the line is what failed, for example `repose's service on the machine stopped answering`. `repose start todo-app` restarts it. When a boot failed, `repose logs todo-app --kind console` shows what the machine printed.

**`todo-app is in an error state: the machine started but did not finish loading its system in time`.** The machine booted and answered, and then the step that gives it the list of its own software ran out of time. `repose start todo-app` tries again, and `repose logs todo-app --kind console` shows what the machine printed.

**`todo-app is in an error state: the machine started but did not finish setting up the project in time`.** The machine booted and answered, and then the step that prepares your home directory and the project's folder ran out of time. `repose start todo-app` tries again, and `repose logs todo-app --kind console` shows what the machine printed.

**`todo-app is in an error state: the machine did not answer while it was starting`.** The machine was started and never answered repose. `repose logs todo-app --kind console` shows what it printed while booting, which is usually where the reason is, and `repose start todo-app` tries again.

**`todo-app: the host running this project did not answer in time, so the operation was given up; it can be run again`.** The server took the command and never said what happened. Nothing was lost: run the command again. On your own Mac, `repose host ls` says whether the Mac is answering.

**`todo-app: its new system did not boot, so it runs its previous one: ...`** A start, or a reboot for a new base, gave the machine a system that didn't boot. repose booted the one it ran before, so your work is there and the machine runs. The rest of the line is the reason, and `repose logs todo-app --kind console` shows what the failed boot printed. Nothing tries that system again: the next base update, or a change to the configuration, builds a new one.

**`error while loading shared libraries: libpq.so.5: cannot open shared object file`** from a program you built on the machine. The program names a library by the version it had when you compiled it, and a [base update](/docs/machine#base-updates) brought a newer one. Build the program again on the machine.

**`environment is frozen and did not wake; try again shortly`**, on a machine `repose ls` marks `frozen`. The wake failed, not the machine: it is still `running`, with its processes, open files and memory as they were. `repose attach todo-app` asks for the wake again, and so does any other command that reaches the machine. `repose stop todo-app` then `repose start todo-app` is the last resort: the stop ends every process on it ([Frozen machines](/docs/lifecycle#frozen-machines)).

**``todo-app is running but repose has no address for it; run `repose stop todo-app` then `repose start todo-app` ``.** The machine is running, but repose doesn't hold the address connections go to, so nothing can reach it. The stop and the start give it one; waiting doesn't help.

**``Could not start todo-app: the server's machine disks are unavailable right now; this machine's disk is intact, and `repose start` works once they are back``.** The machine's server restarted and its disk storage for machines didn't come back. `todo-app` stays stopped with its disk as it was.

**`todo-app is being destroyed`.** `attach` and the other commands can't reach a machine that's going away. `repose run` in the checkout [waits for the destroy](/docs/lifecycle#start-over-with-a-fresh-machine) and creates a fresh `todo-app`; `repose restore todo-app` brings the old one back once the destroy is done.

**`The claude window closed before the attach: the agent in it exited. Attached to the session instead; start the agent again there.`** Running the agent by hand shows why it exited.

**`Repose has no room for this machine right now.`**, then `Stop one of your running machines to make room.`, or **`Could not create todo-app: no host with capacity`.** The servers are full. Memory is never oversubscribed, so waiting frees nothing. Right after `repose rm` or `repose stop`, a new machine waits up to three minutes for that memory. A create that failed this way leaves the project in the error state with no machine: `repose ls` lists it, and `repose start todo-app` or `repose run` in the checkout creates its machine once there's room. A create that the host itself refuses for room (`the host has no free memory for a machine this size`, or on your own host `your host mac-mini runs as many machines at once as its cores allow`) leaves no project: run it again once there is room.

**`No host can run the kind of machine your account makes (Ubuntu or NixOS), so stopping machines frees nothing for it.`** No host, yours included, runs the kind of machine your account makes, Ubuntu or NixOS, so room is not what is missing. When your plan includes repose's servers, `repose run --host repose` tries them. Otherwise [ask on the feedback board](#something-else): a host runs the kinds it was set up for.

**`repose is full right now`.** [Every seat](/docs/limits#when-repose-is-full) is taken, and choosing a plan put you on the waitlist.

## Your own hardware

**`This Mac has 6 GB free; repose needs 10 GB for itself and a machine's base image`.** `repose host add` checks free space before it adds a host to your account or downloads anything, so nothing was created or written. Free up space and run `repose host add` again ([What a Mac needs](/docs/own-hardware#what-a-mac-needs)).

**`macOS ... is older than 14; repose needs macOS 14 or later`**, or **`This Mac has ... GB of memory; repose needs 8 GB or more`**. Checked before a host is added to your account, so nothing was created.

**`Repose for this Mac did not install: ...`** (or **`no data for 1m0s after ...`**, or **`sudo did not install: ...`**). `repose host add` retries a download twice, continuing from what is already on disk, and gives up on a stalled connection after a minute of no bytes. By then the host is on your account, so a plain `repose host add` with the same name is refused with `You already have a host named ...`. Run the command the failure printed, `repose host add --join-file ~/.config/repose/join-<name>`, once the cause is fixed; it continues from what is already here. To start over instead, `repose host rm <name>` removes the host from your account and whatever the install left on the Mac; `repose host rm --local` clears the Mac without touching your account.

**`Waiting for your Mac (mac-mini) to finish installing.`**, **`Your Mac mac-mini never finished installing.`** (then `Remove it and add it again: repose host rm mac-mini, then repose host add`) (or `repose host ls` stays `waiting`). A new project waits for the host to join and starts there when it does; once the host's join token has expired, new machines are refused with exit code 8 instead. On a Mac, `repose host add` waits for the join and says when it has not happened after 3 minutes; read `/Library/Logs/repose/hostd.log`. The Mac needs outbound UDP 51820 and TCP 443, and its join token expires after 24 hours. To start over, run `repose host rm mac-mini` (on the Mac this removes repose from it too), then `repose host add`. On a hosted plan, `repose run --host repose` creates a project on repose's servers meanwhile ([Where a project runs](/docs/api#where-a-project-runs)).

**`Your Mac mac-mini is full.`**, then `Stop api there, or run it on repose's servers: repose run --host repose` (or **`is out of disk space: 40 GB free, 61 GB needed`**). Your host is full, and nothing was created or started. For memory, stop a machine on that host with `repose stop`; the line names the machines running there, and offers repose's servers when your plan runs machines there. For disk, a stop frees nothing: `repose rm` frees a machine's disk, and its final snapshot, compressed, stays on the host's disk for 30 days. With several hosts, the sentence names each one and what it has.

**`Your host box is running as many machines as it can.`**, then `Stop api or web there`, followed for a new machine by `, or run it on repose's servers: repose run --host repose` when your plan runs machines there. Nothing was created or started. A Mac never refuses a machine for its cores: it starts it with fewer ([What fits](/docs/own-hardware#what-fits-on-a-mac)). A machine listed there may be one that is [not answering](#machine-state); `repose ls` says so.

**`stopped: your host mac-mini ran out of memory, so repose stopped api to keep the others running`**. Your machines on that host together wanted more memory than it has, so repose stopped the one whose memory grew most, or the one started most recently when none grew, before the host ran out. Its disk is kept; `repose start api` starts it again. Stop a machine you are not using first, or it can happen again ([What fits](/docs/own-hardware#what-fits-on-a-mac)).

**`This Mac still has the appliance an earlier repose installed`**. Repose no longer runs a Linux virtual machine on a Mac, and the one an earlier version installed does not start. `repose host rm --local` removes it, with the machines and snapshots on its disk, which do not move across; then `repose host add` installs repose again.

**`your host mac-mini has not answered for 14m`** (or `repose host ls` says `offline`). The Mac is asleep, shut down, off the network, or hostd stopped (its log is `/Library/Logs/repose/hostd.log`). If it is only offline, bring the Mac back on the network and wait for `repose host ls` to show `online`. If the host is gone for good or you want to replace it, retire the offline host with `repose host rm mac-mini` (it asks once and removes the host even with machines still on it, because their disks were on that host and cannot be recovered), then run `repose rm` on each stranded machine to clear its row without the host. Once the host is retired, your host slot is free for `repose host add` ([Taking it off](/docs/own-hardware#taking-it-off)).

**`mac-mini still has todo-app on it.`** `repose host rm` refuses to remove a host that is still answering while machines remain on it. Run `repose rm` on each listed machine first, then `repose host rm mac-mini`. If you already ran `repose host rm --local` on the Mac first, the host stays on your account until its heartbeat times out (about 90 seconds): wait until `repose host ls` shows `offline`, then run `repose host rm mac-mini` and `repose rm` each stranded machine ([Taking it off](/docs/own-hardware#taking-it-off)).

**`10 hosts have not joined yet`**. Ten hosts at a time can be waiting to join, and nothing was added. A host stops waiting when its install finishes, when you remove it, or when its token runs out 24 hours after `repose host add`. `repose host ls` lists them as `waiting`.

**`Hardware of your own needs a plan`**. Your account has no plan. Every plan includes hosts of your own: [Own hardware](/docs/billing#own-hardware) is $10 a month for any number of them, and Solo, Plus and Pro include them at no extra price.

## Sync

**`Not synced: the machine changed 2 files that your laptop changed too`.** Something on the machine, usually an agent, changed the files the message lists, and your laptop's work changes them too. Commit or move them on the machine, or run `repose sync --stash-machine` to move the machine's uncommitted changes to `git stash` there. Changes at [any other path](/docs/sync#when-the-machine-has-changes-of-its-own) never stop a sync.

**`Not synced: the machine's checkout is in the middle of a git rebase`.** An agent left a merge, rebase, `am`, cherry-pick, revert or bisect unfinished, and the message names which. Finish or abort it on the machine. `repose sync --discard-machine` ends it from here instead and moves the machine's uncommitted changes to `git stash` there.

**`The machine's main has commits that could not be merged with yours`.** An agent committed on the branch, and git couldn't merge its commits with your laptop's cleanly, or they change a file you have uncommitted. The machine is on your laptop's commit, detached, and the branch keeps the agent's commits. Run `git fetch repose`, merge `repose/main` on your laptop, then `repose sync` again.

**`git fetch repose` fails.** With ``todo-app is stopped; run `repose start todo-app` ``, start it and fetch again. With `Permission denied`, see the `ssh todo-app.repose` entry above; what works for `ssh` works for the fetch. With `does not appear to be a git repository`, the machine has no checkout yet, and `repose run` makes one. If `git remote` doesn't list `repose` at all, run `repose run` or `repose attach` in the checkout; a remote of that name [you added yourself](/docs/sync#getting-work-back) is left alone.

**`git push repose` fails with `this remote is fetch-only`.** Pushing would move the branch the agent has checked out, so the remote only fetches. `repose sync` sends your work the other way.

**`Not synced: your laptop has work the machine doesn't`.** `repose run` copies your checkout only into a new machine. `repose sync` sends the rest.

**`Not sent: web/node_modules (dependencies and caches)`.** [Dependency directories and build caches](/docs/sync#what-doesnt) never travel, ignored or not. Run your install command on the machine.

**`Skipped video.mp4: over 100 MB`** or **`Skipped 12 untracked files past the 500 MB limit for one sync`.** [Both caps are on untracked files](/docs/sync#what-doesnt), so commit what matters, or add large directories to `.gitignore` or `sync.exclude`.

## On the machine

**A command isn't found.** Ubuntu names the package that has it, and `sudo apt-get install` adds it ([Installing more](/docs/machine#installing-more)). `air is still being installed; try again in a moment` means it's one of your laptop's tools, still arriving in the background.

**A tool from the project's `.envrc` is missing.** The [dev environment](/docs/machine#the-projects-dev-environment) didn't load. An `.envrc` loads only once it's allowed: repose allows it for agents and `repose exec`, and in your own shell you run `direnv allow` once. A `use flake` line in it fails on the machine; set the tools up with mise instead.

**A package is missing after a base update.** The machine installs again what you installed with `apt-get`, in the background after its first start on the new base, so give it a minute. What lived outside `/home/dev`, Docker's data and the services' data, such as files under `/usr/local` or `/opt`, starts clean ([Base updates](/docs/machine#base-updates)): install it again, or keep it in `/home/dev`. So does a database's data under `/var/lib` ([Databases](/docs/config#databases)). A package Ubuntu no longer has can't come back; install what replaced it.

**Your plan counts more disk than `df` on the machine shows.** A deleted file counts until the daily `fstrim` gives its blocks back. `sudo fstrim /home/dev` on the machine does it now.

**The browser view asks for a password.** The machine runs base 2026.09.27.2 or older, whose viewer page ignores the password in the link and asks for it instead. Run `repose ssh`, then `repose-guest-profile desktop start` on the machine: it prints the password to type. The new page comes with the next base update. A CLI at v0.1.20 or older has no `repose browser` at all: [update](/docs/install#update) it.

**A tool from your laptop didn't arrive.** The next `repose run` says so once, naming the tool. `~/.repose/tools-install.log` on the machine keeps every failure. A tool that failed isn't tried again until its entry on your laptop changes, its version included. `repose scan` says whether the CLI saw it at all.

**A program you installed isn't on `PATH`.** Every package manager's user bin dir is [on `PATH` in every shell](/docs/machine#installing-more), so a program that doesn't resolve was installed outside them: a prefix of your own, a `make install`, or a tool that manages `PATH` from its own shell setup. Add it to `PATH` in `~/.bashrc`; shells already open keep the `PATH` they started with.

**Processes get killed, or the machine is slow under load.** It ran out of memory. `sudo dmesg | grep -i killed` names what the kernel stopped, and `repose status` lists what's still listening. Stop what you don't need, or [give the machine more memory](/docs/machine#changing-the-size).

**A secret isn't in a program's environment.** Programs read their environment when they start. Restart the program or the agent, or open a new tmux window.

## Agents

**`Claude Code is not logged in on todo-app.`** Claude Code's login is never copied from your laptop ([Log in](/docs/agents#log-in)). With `--no-attach` nothing opened and nothing was typed: attach, log in there, then run the prompt again.

**An [image you dropped or pasted](/docs/run-and-attach#drop-a-file-or-paste-an-image) didn't attach.**

- Cmd+V did nothing on a Mac: you started with `REPOSE_NO_CLIPBOARD_PATH=1`, or a CLI older than v0.1.22. Ctrl+V reads the clipboard itself.
- Your laptop's path appeared: the drop was past a limit, or the copy failed, and the status line said which. `REPOSE_NO_INPUT_PROXY=1` pastes the laptop's path too.
- Ctrl+V did nothing on Linux: the status line names the tool to install, `wl-clipboard` or `xclip`. Over SSH there is no desktop clipboard to read, so run `repose` on the computer you copied the image on.
- Ctrl+V did nothing under WSL: it reads the Linux clipboard, which may not hold an image you copied in Windows.

**`Ctrl+V` pasted text into vim instead of starting a block select.** `Ctrl+V` pastes your clipboard while you are attached. `Ctrl+Q` is the block select, and the shell's quoted insert.

**The browser view shows nothing the agent is doing.** An agent session started before September 25, 2026 has a headless browser of its own that the view can't show; restart the agent to switch it over.

**`this CLI has no complete local package` when you start `codex`.** Codex on bases up to 2026.10.04.1 is missing files its background server needs; base 2026.10.05 ships them. `codex --no-daemon` starts it until the machine gets a newer [base](/docs/config#revisions-and-base-updates).

**`no server running` on `repose attach` to a herdr project.** Your repose CLI is older than 0.1.31 and attaches to tmux. [Update](/docs/install#update) it.

**herdr on the machine is newer or older than expected.** The machine runs the herdr its base ships, and that release is what repose tests against. `herdr update` on the machine installs another into `~/.local/bin`, which comes first on `PATH`. `rm ~/.local/bin/herdr`, then a `repose stop` and `repose start`, goes back to the base's.

**`herdr is not running on todo-app`.** herdr's server is down, and `run`, `attach`, `ps`, `paste` and `stop -w` refuse rather than fall back to tmux. `repose stop todo-app` and `repose start todo-app` bring it back with its workspaces and the agents it had recorded.

**`Could not add todo-app to herdr's sidebar`.** herdr's own `machine add` failed, and the rest of the line is its message. Check that `ssh todo-app.repose true` works from a plain terminal; `run` and `attach` try again each time.

**An agent seems stuck.** Attach and look; it's usually waiting on a [permission prompt](/docs/agents#permissions).

## Ports

**Your dev server isn't on your laptop's `localhost`.** Forwarding runs only while you're attached, and `repose status todo-app` lists the ports it will forward and no others. A server up on the machine and missing from that list doesn't meet [the conditions for a forward](/docs/machine#ports). [`repose open 3000`](/docs/cli#repose-open-local-port) forwards one port without attaching.

**`localhost:5173` answers, but it's your laptop's server.** That port was taken when the forward was made, so repose [used the next free one](/docs/machine#ports) and said which: `⇄ localhost:5174 → :5173 (5173 is taken on your laptop)`. Use the port that line names, or stop your laptop's server and attach again. With no free port in that range there's no forward and no message at all; it's tried again every 30 seconds, and `repose open 8080:5173` puts the port on a laptop port you name.

## Notifications

**Nothing arrives.** Run `repose notify test`. A channel that says `failed` is one repose couldn't send to, with the reason ntfy or Google Chat gave; an email failure says only `failed`, since that one's ours to fix. `Every channel is off; nothing was sent.` means none is set up. If every channel that's on says `sent`, `repose events` shows whether the event was raised; a project past [30 notifications an hour](/docs/notifications#limits) is paused until the time that notice names.

## Updating the CLI

**`repose 0.1.30 is too old for some of repose's answers.`** The server stopped answering a CLI this old the way these docs describe, so a command can behave differently from its page. It prints at the end of every `repose run`, `repose attach` and `repose sync` until you [update](/docs/install#update). The weaker line, `repose 0.1.43 is older than 0.1.44, the latest release.`, prints once for each new release.

**`/nix/store/...-repose-0.1.43/bin/repose belongs to Nix.`** A package manager put that binary there, and `repose version update` won't overwrite it. Update it with that manager, or install repose with [the script](/docs/install#install) into `~/.local/bin`.

**`Can't write /usr/local/bin.`** The binary is in a directory your user can't write, which is where `install.sh --system` puts it. Run `sudo repose version update`.

## Something else

Post a bug or an idea on the [feedback board](https://repose.fider.io). Sign in with your repose account, the same email or GitHub login you use for `repose login`. Posts are public, so leave out secrets, tokens and private code.
