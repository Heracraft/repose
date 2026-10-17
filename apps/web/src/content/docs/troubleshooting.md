---
title: Troubleshooting
description: The errors you're most likely to see and what fixes each one.
section: Reference
order: 41
---

For more detail on any command, add `-v`; its request IDs are what support looks up. [`REPOSE_TIMING=1`](/docs/cli#environment-variables) and [`repose logs --kind ops`](/docs/cli#repose-logs-project) are the other two places to look.

## Logging in and connecting

**``Not logged in. Run `repose login`.``** Your login expired or you logged out.

**`Could not reach the login server (...)`** or **`Could not reach api.repose.herakraft.co`.** Your laptop is offline, or the server is down; run the command again once you're connected.

**`The repose api answered 502`** (or 503, or 504). The API is restarting, usually for a release. A read is sent once more by itself; a command that changes something is not, so run it again.

**`ssh on this laptop failed before it reached todo-app`.** Your laptop's ssh refused before it got to the gateway: a line in `~/.ssh/config`, a `ControlPath` too long for a socket, or `~/.ssh/repose/known_hosts`. The line quotes ssh and names the fix when it knows one.

**`No repose project for github.com/you/app`.** This checkout has no project yet, or its remote changed. `repose run` creates one; `repose attach NAME` reaches an existing one.

**`ssh todo-app.repose` says `Could not resolve hostname`.** With ``Not logged in. Run `repose login`.`` on a line before it, log in and connect again. With no other message, ssh isn't reading [the file repose writes](/docs/ssh-and-editors#when-it-doesnt-connect).

**``repose: No repose project is called todo-app. `repose ls` lists yours.``** The host name is misspelt, or the project was destroyed.

**`ssh todo-app.repose` says `Permission denied`.** The certificate couldn't be renewed, most often because you're logged out: run `repose login`, then connect again. `repose attach todo-app` renews it too.

**`too many open connections for your account; close some and try again`.** You have 32 [SSH connections](/docs/limits#ssh-connections) open through the gateway, across all your projects. A script that opens connections without closing them is the usual cause.

**`kex_exchange_identification: Connection closed by remote host`.** Your address opened more connections at once than the gateway lets in, or failed to log in many times in a row. `ssh -v` shows the gateway's reason on a line starting `repose gateway:`. Wait a minute, or 10 minutes if it was the logins.

**Your connection closed on its own after hours.** Its certificate expired, or a `repose logout` closed it ([SSH connections](/docs/limits#ssh-connections)). Connect again and your `ssh` renews the certificate. The session on the machine is as you left it.

**`repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.`** The connection dropped: Wi-Fi, a laptop that slept, or [the machine's server restarting](/docs/lifecycle#when-a-machines-server-restarts). `run` and `attach` attach again as soon as the machine answers.

**`attach` pauses for a second or two before it connects.** After the laptop slept or changed networks, `attach` spends up to 2 seconds finding that the old connection is dead, then connects as usual.

**``repose: could not reach todo-app for 2 minutes. `repose attach todo-app` attaches again once it answers.``** The connection didn't come back within 2 minutes. Check your network, then run `repose attach todo-app`; `repose status todo-app` says whether the machine is running.

**Your editor can't connect to `todo-app.repose`.** Run `ssh todo-app.repose true` in a terminal. It fails the same way the editor did, with the reason. A stopped machine says ``todo-app is stopped; run `repose start todo-app` ``.

**`repose gateway: too many authentication attempts from your address; try again later`.** Your address failed to log in 20 times in 10 minutes without a repose certificate, usually an ssh run with another key or an old `~/.ssh/config` entry. Every connection from that address is refused for 10 minutes. Connections refused because your machine is stopped or gone don't count.

**`repose gateway: certificate not valid for this project`.** The certificate on your laptop is for another project or another account handle: a project removed and made again has a new id, and so does a renamed handle. Any `repose` command gets a new certificate and connects; plain `ssh` and your editor don't, so run `repose ssh todo-app` once first.

**`repose gateway: gateway busy`.** The gateway is at its limit for connections being set up, which has nothing to do with your account or your certificate. Connect again.

**`todo-app is running but did not answer ssh in 15 s.`** `repose stop` and then `repose start` restart it. A command that has just started or created the machine waits 60 s instead.

## Machine state

**``todo-app is stopped. `repose start todo-app` starts it.``** `exec`, `ps`, `cp`, `open`, `mcp forward` and `browser bridge` don't start a stopped machine; [the ones that do](/docs/lifecycle#stop-and-start) start it for you.

**``todo-app is stopping. `repose start todo-app` waits for the stop and starts it again.``** The commands that don't start a stopped machine don't wait out a stop either.

**A machine that was running is stopped, and you didn't stop it.** Its server restarted: `repose events todo-app` shows `server restarted`. [When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts) says what repose starts again by itself and what it doesn't. `repose start todo-app` starts it.

**``todo-app's server is not answering; machines that were running on it start again by themselves when it is back, and `repose start todo-app` works then``.** The machine's server is down or unreachable. The machine's disk is on that server, so nothing can start it elsewhere without losing the work since its last snapshot.

**`todo-app is in an error state: ...`** The rest of the line is what failed, for example `repose's service on the machine stopped answering`. `repose start todo-app` restarts it. When a boot failed, `repose logs todo-app --kind console` shows what the machine printed.

**`todo-app: its new system did not boot, so it runs its previous one: ...`** A start, or a reboot for a new base, gave the machine a system that didn't boot. repose booted the one it ran before, so your work is there and the machine runs. The rest of the line is the reason, and `repose logs todo-app --kind console` shows what the failed boot printed. Nothing tries that system again: the next base update, or a change to the configuration, builds a new one.

**`todo-app: a nix garbage collection inside the machine hid parts of the new system, so it keeps its current one`.** A garbage collection run on the machine on an older base deleted store paths repose shares with it and that the new system needs. `repose stop todo-app`, then `repose start todo-app`: the start puts the paths back and switches to the new system.

**`todo-app: the new system is not in the machine's store, so it was not switched to and keeps its current system`.** The machine's store doesn't show paths the new system needs: the garbage collection above, or a server that no longer has them. The configuration stays queued, and a `repose stop todo-app` then `repose start todo-app` tries the switch again.

**`error while loading shared libraries: libpq.so.5: cannot open shared object file`** from a program you built on the machine. The program names a library by the exact path it had when you compiled it, and a later platform update moved that library. The machine keeps the libraries of the base it runs, so building the program again is the usual fix. To keep the old build, `repose-store-repair ./your-program` on the machine downloads the libraries it names, well under a second each, as long as the machine can reach the internet. If it says the path is empty, `repose stop` and `repose start` the machine, then run it again.

**``todo-app is running but repose has no address for it; run `repose stop todo-app` then `repose start todo-app` ``.** The machine is running, but repose doesn't hold the address connections go to, so nothing can reach it. The stop and the start give it one; waiting doesn't help.

**``Could not start todo-app: the server's machine disks are unavailable right now; this machine's disk is intact, and `repose start` works once they are back``.** The machine's server restarted and its disk storage for machines didn't come back. `todo-app` stays stopped with its disk as it was.

**`todo-app is being destroyed`.** `attach` and the other commands can't reach a machine that's going away. `repose run` in the checkout [waits for the destroy](/docs/lifecycle#start-over-with-a-fresh-machine) and creates a fresh `todo-app`; `repose restore todo-app` brings the old one back once the destroy is done.

**`The claude window closed before the attach: the agent in it exited. Attached to the session instead; start the agent again there.`** Running the agent by hand shows why it exited.

**`repose has no room for this machine. A machine holds its memory while it runs, so stopping one frees room for it.`** or **`Could not create todo-app: no host with capacity`.** The servers are full. Memory is never oversubscribed, so waiting frees nothing. Right after `repose rm` or `repose stop`, a new machine waits up to three minutes for that memory. A create that failed this way leaves the project in the error state with no machine: `repose ls` lists it, and `repose start todo-app` or `repose run` in the checkout creates its machine once there's room.

**`repose is full right now`.** [Every seat](/docs/limits#when-repose-is-full) is taken, and choosing a plan put you on the waitlist.

## Sync

**`Not synced: the machine changed 2 files that your laptop changed too`.** Something on the machine, usually an agent, changed the files the message lists, and your laptop's work changes them too. Commit or move them on the machine, or run `repose sync --stash-machine` to move the machine's uncommitted changes to `git stash` there. Changes at [any other path](/docs/sync#when-the-machine-has-changes-of-its-own) never stop a sync.

**The same message names only `flake.lock`.** The repository has an `.envrc` with `use flake` and no committed `flake.lock`, so your laptop and the machine each wrote one of their own on the first dev shell load. Commit one; [Projects with a flake.nix](/docs/machine#projects-with-a-flake-nix) says how with no Nix on your laptop. Until then `repose sync --stash-machine` is safe, because the next load writes the file again.

**`Not synced: the machine's checkout is in the middle of a git rebase`.** An agent left a merge, rebase, `am`, cherry-pick, revert or bisect unfinished, and the message names which. Finish or abort it on the machine. `repose sync --discard-machine` ends it from here instead and moves the machine's uncommitted changes to `git stash` there.

**`The machine's main has commits that could not be merged with yours`.** An agent committed on the branch, and git couldn't merge its commits with your laptop's cleanly, or they change a file you have uncommitted. The machine is on your laptop's commit, detached, and the branch keeps the agent's commits. Run `git fetch repose`, merge `repose/main` on your laptop, then `repose sync` again.

**`git fetch repose` fails.** With ``todo-app is stopped; run `repose start todo-app` ``, start it and fetch again. With `Permission denied`, see the `ssh todo-app.repose` entry above; what works for `ssh` works for the fetch. With `does not appear to be a git repository`, the machine has no checkout yet, and `repose run` makes one. If `git remote` doesn't list `repose` at all, run `repose run` or `repose attach` in the checkout; a remote of that name [you added yourself](/docs/sync#getting-work-back) is left alone.

**`git push repose` fails with `this remote is fetch-only`.** Pushing would move the branch the agent has checked out, so the remote only fetches. `repose sync` sends your work the other way.

**`Not synced: your laptop has work the machine doesn't`.** `repose run` copies your checkout only into a new machine. `repose sync` sends the rest.

**`Not sent: web/node_modules (dependencies and caches)`.** [Dependency directories and build caches](/docs/sync#what-doesnt) never travel, ignored or not. Run your install command on the machine.

**`Skipped video.mp4: over 100 MB`** or **`Skipped 12 untracked files past the 500 MB limit for one sync`.** [Both caps are on untracked files](/docs/sync#what-doesnt), so commit what matters, or add large directories to `.gitignore` or `sync.exclude`.

## On the machine

**A command isn't found.** The machine names the nixpkgs package that has it and [the two ways to add it](/docs/machine#installing-more). `air is still being installed; try again in a moment` means it's one of your laptop's tools, still arriving in the background.

**A tool from the project's `flake.nix` is missing.** The [dev shell](/docs/machine#projects-with-a-flake-nix) didn't load. In an agent's window or `repose exec`, Nix's error is above the agent's first screen or the command's output; an untracked `flake.nix` and a dev shell defined only for macOS are the usual causes. In your own shell it loads when you `cd` into the checkout, and a Ctrl-C while it loads keeps it out until you leave the folder and come back. An `.envrc` of your own loads only once you've run `direnv allow` on it.

**The browser view asks for a password.** The machine runs base 2026.09.27.2 or older, whose viewer page ignores the password in the link and asks for it instead. Run `repose ssh`, then `repose-guest-profile desktop start` on the machine: it prints the password to type. The new page comes with the next base update. A CLI at v0.1.20 or older has no `repose browser` at all: [update](/docs/install#update) it.

**A tool from your laptop didn't arrive.** The next `repose run` says so once: `Could not install tinygo: no nixpkgs package has bin/tinygo`. `~/.repose/tools-install.log` on the machine keeps every failure. A tool that failed isn't tried again until its entry on your laptop changes, its version included. `repose scan` says whether the CLI saw it at all.

**A program you installed isn't on `PATH`.** Every package manager's user bin dir is [on `PATH` in every shell](/docs/machine#installing-more), so a program that doesn't resolve was installed outside them: a prefix of your own, a `make install`, or a tool that manages `PATH` from its own shell setup. Add it to `PATH` in `~/.bashrc`, or to [`home.sessionPath`](/docs/config#write-it-in-nix) for every process on the machine; shells already open keep the `PATH` they started with.

**Processes get killed, or the machine is slow under load.** It ran out of memory. `sudo dmesg | grep -i killed` names what the kernel stopped, and `repose status` lists what's still listening. Stop what you don't need, or [give the machine more memory](/docs/machine#changing-the-size).

**A secret isn't in a program's environment.** Programs read their environment when they start. Restart the program or the agent, or open a new tmux window.

**`config error` or exit code 10.** The configuration didn't build, so nothing on the machine changed. The message names the problem; `repose logs --kind build` has the full log. Fix it with `repose config edit` or `repose config remove`.

## Agents

**`Claude Code is not logged in on todo-app.`** Claude Code's login is never copied from your laptop ([Log in](/docs/agents#log-in)). With `--no-attach` nothing opened and nothing was typed: attach, log in there, then run the prompt again.

**An [image you dropped or pasted](/docs/run-and-attach#drop-a-file-or-paste-an-image) didn't attach.**

- Cmd+V did nothing on a Mac: you started with `REPOSE_NO_CLIPBOARD_PATH=1`, or a CLI older than v0.1.22. Ctrl+V reads the clipboard itself.
- Your laptop's path appeared: the drop was past a limit, or the copy failed, and the status line said which. `REPOSE_NO_INPUT_PROXY=1` pastes the laptop's path too.
- Ctrl+V did nothing on Linux: the status line names the tool to install, `wl-clipboard` or `xclip`. Over SSH there is no desktop clipboard to read, so run `repose` on the computer you copied the image on.
- Ctrl+V did nothing under WSL: it reads the Linux clipboard, which may not hold an image you copied in Windows.

**The browser view shows nothing the agent is doing.** An agent session started before September 25, 2026 has a headless browser of its own that the view can't show; restart the agent to switch it over.

**`this CLI has no complete local package` when you start `codex`.** Codex on bases up to 2026.10.04.1 is missing files its background server needs; base 2026.10.05 ships them. `codex --no-daemon` starts it until the machine gets a newer [base](/docs/config#revisions-and-base-updates).

**`bundled bubblewrap digest mismatch` when Codex runs a command.** Base 2026.10.05 ships Codex with a sandbox helper it refuses, and 2026.10.05.1 replaced it. `nix profile add nixpkgs#bubblewrap` on the machine gives Codex a bwrap it takes instead.

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

## Something else

Post a bug or an idea on the [feedback board](https://repose.fider.io). Sign in with your repose account, the same email or GitHub login you use for `repose login`. Posts are public, so leave out secrets, tokens and private code.
