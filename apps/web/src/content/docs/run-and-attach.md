---
title: Run and attach
description: Start agents, move around the tmux session or herdr, get back to it later, and connect with SSH or an editor.
section: Using repose
order: 10
---

## Start an agent with a prompt

```
repose run "move the date handling to Temporal, fix the tests"
```

Quotes are optional; everything after the flags is the prompt. A one-word prompt that is the name of one of your projects is refused as a likely slip (exit code 2); to send it anyway, name the agent: `repose run --agent claude todo-app`. `run` creates or starts the machine, copies your checkout into it if the machine is new ([Sync](/docs/sync)), opens a new tmux window, starts the agent there, types your prompt and attaches you to it.

Without a prompt, `repose run` drops you in the last active window.

Pick a different agent for one prompt with `--agent`:

```
repose run --agent codex "port the build scripts to bun"
```

The agent is the normal interactive program, the same as running `claude` yourself.

Claude Code doesn't ask whether you trust the folder: `run` marks the folder it starts Claude Code in as trusted in `~/.claude.json` on the machine (the checkout, or the worktree with `--worktree`). If Claude Code asks anyway, `run` doesn't type your prompt into the question. It says so and attaches you to answer it; with `--no-attach` it exits with code 1. Running `claude` yourself in another folder on the machine still asks.

If that agent already has a window, the new one is named `claude-2`, then `claude-3`, and so on, and the CLI warns that the agents share one working tree.

Only the first run on a new machine copies your checkout; later ones attach to the machine as it is, and say so when your laptop has work to send with `repose sync`.

If the agent exits before you're attached, its window closes with it. `run` then attaches you to the session and says `The claude window closed before the attach`; start the agent again there.

## Several agents, separate trees

`--worktree` starts the agent in its own git worktree, so it doesn't edit the files another agent is working on:

```
$ repose run --worktree "try the other approach"
Worktree: ~/todo-app-worktree-1 on branch worktree-1
Copied 1 .env file from ~/todo-app
```

The worktree is a folder next to your checkout on the machine, numbered from 1: `~/todo-app-worktree-1`, then `~/todo-app-worktree-2`. Its branch has the same name, `worktree-1`, and starts at the checkout's last commit. Uncommitted changes in the checkout aren't in it. The agent's window is named like any other, `claude` or `claude-2`.

In the worktree the agent has the whole repository at that commit, plus the checkout's gitignored `.env` and `.env.*` files as they are on the machine. `repose run` never syncs a worktree, and what the agent does there doesn't count as changes on the machine. Commit on the branch and merge or push it like any other. On your laptop, `git fetch repose` brings it as `repose/worktree-1` ([Getting work back](/docs/sync#getting-work-back)).

Each `--worktree` run makes a new one with the lowest free number. They stay until you remove them, from the checkout on the machine:

```
git worktree remove ~/todo-app-worktree-1
git branch -D worktree-1
```

The machine's checkout needs at least one commit; otherwise `--worktree` is refused with exit code 2. Dependencies aren't shared, so the agent installs them again in the worktree. Other gitignored files, such as build output and local databases, aren't copied.

Worktrees made before CLI v0.1.22 keep their old names, `~/todo-app-claude-2` on branch `repose/claude-2`, which your laptop fetches as `repose/repose/claude-2`.

## Several repositories on one machine

One machine can hold more than one repository. In a second folder, `--on` adds it to a machine you already have, beside that machine's own checkout:

```
~/code/api $ repose run --on todo-app
Connected to todo-app (small)
Added api to todo-app as ~/api.
Synced: 3 untracked
```

The folder gets its own checkout, `~/api`, named after the folder like the first one (`~/api-2` if the machine already has an `api`). It doesn't count as another project, so it uses none of your plan's running memory beyond what the agents in it use. Your laptop remembers the folder: from then on a plain `repose run`, `attach`, `sync`, `exec`, `ssh`, `cp :PATH` or `code` in `~/code/api` works in `~/api`, and `git fetch repose` there brings back that checkout's commits.

Agent windows in the added checkout are named after it, `api/claude`, then `api/claude-2`, so `repose ps` shows which tree each agent works in. `repose attach` in the folder opens the last of its windows you used, or a new shell window `api` in `~/api`. From anywhere else, `repose attach todo-app:api` does the same. `--worktree` works too: its worktrees are `~/api-worktree-1` and so on.

The checkouts share one machine, so they share these:

- Secrets, tool logins and settings. Every checkout sees the machine's secrets.
- Your git identity. If your laptop picks a different email per folder (an `includeIf` for work repositories), the machine has the one from the folder you ran in last.
- The machine's configuration. A `repose.nix` is applied to the whole machine; a `flake.nix` or `.envrc` dev shell loads per folder, as usual.
- Ports. Two dev servers on port 3000 collide; give one another port.
- Disk, snapshots and undo. `repose undo` and a restore roll back every checkout, and `repose destroy` deletes them all.

The added checkout's `.env` files travel like the first one's, but the machine keeps one record of the last set it was sent, so running in the two folders by turns sends each set again. `--on` can't be combined with `--temp`, `--name`, `--project` or `--size`, and a folder that is already the machine's own checkout is refused, with exit code 2 for both. To remove an added checkout, delete its folder on the machine and its line in `~/.repose/checkouts`; on your laptop, the folder's entry under `checkouts` in `~/.config/repose/projects.json`.

## Detach and come back

Press `Ctrl-b`, let go, then `d`. Everything on the machine keeps running. Closing the terminal does the same.

If Wi-Fi drops or the laptop sleeps while you're attached, `run` and `attach` say `lost the connection` and attach again by themselves once the machine answers, for up to 2 minutes, with the screen as you left it. `Ctrl-C` stops waiting.

After the laptop sleeps or changes networks, a new `attach` spends up to 2 seconds checking whether the connection from your last command still answers, and opens a fresh one if it doesn't. repose keeps your login fresh while you're attached, so coming back after hours doesn't wait on a login refresh.

To get back, from the checkout or from anywhere:

```
repose attach
repose attach todo-app
```

`attach` doesn't sync your checkout, so it's safe to use from a second computer whose copy is older. It doesn't start a stopped machine either; it tells you to run `repose start`.

Several terminals can be attached at once, from one computer or several. They see the same windows.

## tmux keys

Each machine has one tmux session. Its first window, `shell`, opens in your checkout; agents get windows next to it. Press `Ctrl-b`, then:

| Key     | Action                                          |
| ------- | ----------------------------------------------- |
| `d`     | Detach.                                         |
| `w`     | Pick a window from a list.                      |
| `n` `p` | Next or previous window.                        |
| `c`     | New window with a shell.                        |
| `[`     | Scroll back. Arrow keys or Page Up; `q` leaves. |

Exiting the last window ends the session, and a new one with a `shell` window starts 5 seconds later. A temporary machine is destroyed instead when you leave its last window from `repose attach` or `repose run`; left any other way, it waits for its expiry.

tmux leaves the mouse to your terminal, so selecting text and copying work as they do outside tmux. If you want tmux's mouse mode instead (click a window name to switch, scroll with the wheel), run `echo 'set -g mouse on' >> ~/.tmux.conf` on the machine, then `tmux source-file ~/.tmux.conf`. The file stays in your home directory across stops.

Shift+Enter starts a new line in Claude Code instead of sending the prompt, when your terminal reports modified keys to tmux (xterm's modifyOtherKeys; Ghostty, WezTerm, iTerm2 and xterm do, Apple's Terminal doesn't). If Shift+Enter still sends the prompt, type `\` and then Enter, or press Ctrl+J. Links an agent prints are clickable in terminals that support links (OSC 8), and a program in the current window can send escape sequences through tmux to your terminal.

Terminals with 24-bit colour (Ghostty, kitty, WezTerm, iTerm2, Alacritty, foot, or any that sets `COLORTERM=truecolor`) get it; Apple's Terminal before macOS 26 gets 256 colours. tmux sets your terminal's title to the machine's host name and the session.

## herdr instead of tmux

A machine can run its terminals in [herdr](https://herdr.dev), a multiplexer made for coding agents, in place of tmux. Pick it when you create the project, or switch one you have:

```
repose run --multiplexer herdr
```

The choice stays with the project. To make herdr the default for every project `run` creates, put `default_multiplexer = "herdr"` in [`config.toml`](/docs/cli#config-toml). A project you create from a terminal inside herdr on your laptop gets herdr unless the flag or that key says otherwise. A [temporary machine](/docs/lifecycle#temporary-machines) always runs tmux.

A switch takes effect at the next start. A running machine keeps its current multiplexer and its agents until it stops:

```
$ repose run --multiplexer herdr
todo-app uses herdr from its next start; tmux runs until then.
```

On a herdr project the commands on this page work through herdr:

- `repose run "prompt"` opens a tab in the checkout's workspace, starts the agent there and types the prompt. With `--worktree` the worktree shows under the repository in herdr's sidebar.
- `repose attach` from a terminal inside herdr on your laptop opens nothing new: the machine is in herdr's sidebar, and the command prints `todo-app is in herdr's sidebar.` and keeps port forwards and the browser bridge going until you press Ctrl-C. Elsewhere, with herdr 0.9.0 or newer installed, it opens `herdr --remote todo-app.repose`. Without herdr on the laptop, it runs herdr's client on the machine over SSH.
- `repose ps` lists herdr's agents with their workspace and state. `repose paste` sends the image's path to the focused pane, or to an agent's pane with `--window NAME`.
- `repose status` shows `herdr` after the size.
- From herdr on your laptop (the sidebar or `herdr --remote`), a dropped file or `Ctrl+V` goes to herdr, and nothing is copied to the machine. Use `repose paste` for an image on the clipboard and [`repose cp`](/docs/sync#single-files) for a file. herdr's client over SSH, without herdr on the laptop, copies drops and pastes as tmux does.
- If herdr has stopped on the machine, `run` and `attach` say `herdr is not running on todo-app` and exit 1. `repose stop` and `repose start` bring it back.

repose's messages inside the session (the time zone, new port forwards, copied files) are herdr notifications, which the machine's `~/.config/herdr/config.toml` turns on. A `config.toml` that was there before the machine first ran herdr, for example because you ran herdr there yourself, keeps its own settings. Add this to it and run `herdr server reload-config` on the machine:

```toml
[ui.toast]
delivery = "herdr"
```

Without it, a copied file is not named anywhere on herdr.

`run` and `attach` keep herdr's sidebar on your laptop in step: a running herdr project is added there, and `repose rm` removes it. Entries you made for other hosts are left alone, and so is an entry you disabled. Each machine in the sidebar keeps an SSH connection open, which counts as someone using it for the [idle notice](/docs/notifications) and for [temporary machines](/docs/lifecycle#temporary-machines). Disable an entry in herdr to stop that.

A machine on herdr needs repose 0.1.31 or newer (`repose version`). An older CLI answers `no server running` on `attach`; [update](/docs/install#update) it. The [herdr tutorial](/docs/tutorial-herdr) walks through a first project.

## See what's running, run one command

`repose ps` lists the tmux windows without attaching: what runs in each and when it last printed something. `*` is the window `attach` opens on.

```
$ repose ps
WINDOW     COMMAND  ACTIVE
0:shell    bash     3h ago
1:claude*  claude   now
2:codex    codex    12m ago
```

An agent that's working usually shows `now`; one that has been waiting for you shows roughly how long. COMMAND is the program's name only, never its arguments.

`repose exec` runs one command in the checkout on the machine and gives you its output and exit code, the way `docker exec` does. The command gets what an agent there gets: your [secrets](/docs/secrets) as environment variables and the project's dev shell (its `.envrc`, or its `flake.nix` dev shell). Loading it prints nothing unless it takes more than 2 seconds or fails.

```
$ repose exec npm test
$ repose exec todo-app git status --short
$ repose exec -it psql
```

A first word that names one of your projects picks that project; otherwise it is this checkout's. `repose exec -- COMMAND` runs a command that happens to share a project's name. Without `-i` it reads no input, and without `-t` it has no terminal; `-it` is for something interactive, like a REPL.

`repose ssh` opens a plain shell in the checkout instead of the tmux session, and `exit` closes it. Start long jobs in tmux (`repose attach`), where they outlive the connection.

Like `attach`, these start no machine: a stopped one gets you exit code 5 and the command to start it.

## Drop a file or paste an image

While you're attached, drag a file onto the terminal, or press `Cmd+V` or `Ctrl+V` with a screenshot on your laptop's clipboard. The file is copied to `/tmp/repose-paste/` on the machine and its path there is pasted where your cursor is:

```text
❯ [Image #1] the button overlaps the footer on this screen
```

Claude Code shows an image as `[Image #1]`. Other agents, and the shell, get the path as text.

- A file from your checkout isn't copied. You get its path in the machine's checkout, such as `/home/dev/todo-app/docs/mockup.png`. If the machine's copy isn't there yet or differs in size, the file is copied like any other.
- Drop several files at once to paste several paths.
- Up to 20 files and 20 MB per file. A bigger drop pastes your laptop's path unchanged, and the tmux status line says why; use [`repose cp`](/docs/sync#single-files) for large files.
- Only you and the machine's `dev` user can read the copies. Copies older than a day, and all but the newest 50, are deleted at the next copy.
- A paste that is nothing but paths of files on your laptop counts as a drop, so pasting a copied path works too. Paths under system folders such as `/etc`, `/usr` and `/nix` are pasted as they are, and so are hidden files, anything in a hidden folder such as `~/.ssh`, and private keys: those are never copied. A private key is a file named `id_rsa`, `id_dsa`, `id_ecdsa` or `id_ed25519` (`.pub` files still copy), a file ending in `.pem`, `.p12`, `.pfx`, `.p8`, `.ppk`, `.jks`, `.keystore`, `.kdbx`, `.keychain` or `.keychain-db`, or a file of any name that starts with a PEM, OpenSSH or PGP private key, or with an OpenPGP secret key (`.gpg`, `.pgp`). A `.key` file is refused only when it holds such a key, so Keynote decks still copy; public keys, signatures and encrypted `.gpg` files copy too. A link counts as the file it points to, so a link to a file in `~/.ssh` isn't copied either.
- Every copy is named on the tmux status line, for example `copied report.pdf to the machine`. If an agent asks you to paste a path, that line tells you what left your laptop. On herdr the line is a [herdr notification](#herdr-instead-of-tmux).

Any terminal that types a dropped file's path works: plain, quoted, with backslashes before spaces, or as a `file://` address.

On macOS, Cmd+V and Ctrl+V both paste the image. A terminal sends nothing for Cmd+V when the clipboard holds only an image, so while you're attached repose adds a text version to such a clipboard: the path of a PNG copy in `~/Library/Caches/repose/clipboard/`. Cmd+V pastes that path, and it's copied to the machine like a dropped file. The image stays on the clipboard as well, so apps that take images still get it; a plain text field gets the path. When you detach, the text is taken off again unless you've copied something since. The last 20 copies are kept for a day. `REPOSE_CLIPBOARD_PATH=0` leaves your clipboard alone, and then only Ctrl+V pastes an image.

Ctrl+V reads the clipboard with `pngpaste` if you have it, otherwise `osascript`. On Linux it uses `wl-paste` (from wl-clipboard) under Wayland and `xclip` under X11. With no image on the clipboard, Ctrl+V is an ordinary Ctrl+V. With one, Ctrl+V pastes the image in every window.

`REPOSE_INPUT_PROXY=0` turns this off: `run` and `attach` then hand your terminal straight to `ssh`, and a drop pastes your laptop's path. On Windows it's always off; copy the file with `repose cp FILE :/tmp/` and type its path.

### From a script or another window

`repose paste` copies the image on the clipboard and pastes its path into the tmux window you were last in, without a key press:

```
repose paste
```

- `repose paste todo-app` from anywhere; `--window claude-2` for another window; `--print` to only print the path.
- It reads the clipboard with the same tools as Ctrl+V. Under WSL it reads the Linux clipboard, which may not have images copied in Windows.

## Useful flags

```
repose run --no-attach "..."  # start it, keep your shell
repose sync                   # send your laptop's work, no attach
repose run --no-sync          # a new machine without your checkout
repose run --size xl          # size of a new project
repose run --name scratch     # a project by name, made if missing
repose run --temp             # a new machine, gone after 24 hours
repose run --multiplexer herdr # herdr, from the next start
repose run --project todo-app # a project other than this checkout's
```

`--name` is also how you get [a second machine for the same repository](/docs/lifecycle#a-second-machine-for-the-same-repository), and `--temp` is described under [Temporary machines](/docs/lifecycle#temporary-machines).

The full list is in the [CLI reference](/docs/cli#repose-run-prompt).

## SSH and editors

Every project is also an SSH host called `<project>.repose`, so `ssh`, `scp`, `rsync`, git and editors reach it with no `repose` command first. `repose code` opens the checkout in VS Code, Cursor or Zed:

```
ssh todo-app.repose
repose code todo-app
```

Plain `ssh` doesn't attach to tmux; run `tmux attach` for that. [SSH and editors](/docs/ssh-and-editors) has the rest: scp and rsync, git over SSH, each editor by hand, and what to do when a connection fails.

mosh doesn't work: it needs a UDP connection straight to the machine, and the only way in is SSH through repose.

## Time zone

`run` and `attach` set the machine's time zone to your laptop's. New shells pick it up. Until the first `run`, a machine uses the time zone on the dashboard's **Settings** page.

## When the machine stops

A stop ends every process, agents included. After the next start, the tmux session has a fresh `shell` window and the agents' windows are gone. To continue a Claude Code conversation, run `claude --resume` in the checkout and pick it.

On a herdr project, herdr puts its workspaces and tabs back at the start and resumes each agent that has herdr's integration (Claude Code, Codex, opencode and pi) in the conversation it was in.

## Timing

`REPOSE_TIMING=1 repose run` prints how long each step took. A `run` into a running machine that already has your checkout usually takes about a second; starting a stopped one, about 10.
