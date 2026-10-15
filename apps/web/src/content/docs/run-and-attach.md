---
title: Run and attach
description: Start agents, move around the tmux session or herdr, get back to it later, and connect with SSH or an editor.
section: Using repose
order: 10
---

## Start an agent with a prompt

```
repose run -p "move dates to Temporal, fix the tests"
```

`run` starts the machine if it's stopped, opens a tmux window, starts the agent there, types your prompt in and attaches you. A machine it creates gets [your checkout](/docs/sync) first.

Without a prompt, `run` drops you in the window you were last in.

`-d` (`--no-attach`) starts the agent and keeps your shell. It prints the window the agent is in:

```
$ repose run -d -p "add rate limiting to the public API"
Window: claude-2
```

`--agent` picks [another agent](/docs/agents) for this prompt.

`run` types the prompt in as you would, so a question waiting on the agent's first screen would take it as the answer. Claude Code and Codex don't ask whether you trust the folder they start in: `run` marks it trusted before the agent starts, and hands Claude Code the `.mcp.json` answers you gave on your laptop for this repository. A question that shows up anyway is never typed into: `run` names it, leaves your prompt untyped and attaches you to answer it, and with `-d` exits 1 instead of attaching. An agent you start yourself in another folder asks you as usual.

If the agent already has a window, the new one is named `claude-2`, then `claude-3`. The CLI warns you then that the two agents share one working tree, and warns the same way when that agent is already running in a window of another name, such as `claude` typed in the `shell` window.

## Several agents, separate trees

Two agents in one working tree edit the same files. `--worktree` gives an agent its own tree beside the checkout. No more memory comes off your plan; the agents share the one machine's. An agent that needs a machine of its own gets one from [`repose fork`](/docs/lifecycle#fork-a-project), which copies this machine's disk as it stands.

```
$ repose run --worktree -p "try the other approach"
Worktree: ~/todo-app-worktree-1 on branch worktree-1
Copied 1 .env file from ~/todo-app
```

Worktrees are folders beside the checkout, `~/todo-app-worktree-1` and up, each on its own `worktree-N` branch. The branch starts at the checkout's last commit, so uncommitted changes there aren't in it, and `run` says so when the checkout is dirty. The window keeps the agent's name, `claude` or `claude-2`. Each `--worktree` run takes the lowest number whose folder and branch are both free, so a new prompt never lands on an old attempt's files.

In the worktree the agent has the whole repository at that commit, plus the checkout's gitignored `.env` and `.env.*` files. Dependencies aren't shared, so the agent installs them again and they count toward your plan's disk; build output and local databases aren't copied either. A worktree sits outside the synced tree, so `repose sync` leaves it alone and what the agent does there doesn't count as changes on the machine. Its commits come back as `repose/worktree-1` ([Getting work back](/docs/sync#getting-work-back)).

On tmux, `repose stop -w claude-2` closes the window, fetches the branch and removes the worktree. One with uncommitted files, another window in it, or a branch the fetch didn't bring stays, and the line says why ([`repose stop`](/docs/cli#repose-stop-project)).

The machine's checkout needs at least one commit; `--worktree` on one with none is refused with exit code 2.

## Several repositories on one machine

In a second folder, `--on` adds it to a machine you already have, beside that machine's own checkout:

```
~/code/api $ repose run --on todo-app
Connected to todo-app (small)
Added api to todo-app as ~/api.
Synced: 0 modified, 3 untracked  0.6s
```

It gets a checkout of its own, `~/api`, named after the folder ([Where the checkout is](/docs/sync#where-the-checkout-is)), or `~/api-2` if that name is taken. It isn't another project, so it adds nothing to your plan's memory or your [project count](/docs/limits#projects). Your laptop remembers the folder. From then on a plain `repose run`, `attach`, `sync`, `exec`, `ssh` or `code` there works in `~/api`, and `git fetch repose` brings that checkout's commits back.

Agent windows in the added checkout carry its name, `api/claude`, then `api/claude-2`. `repose attach` in the folder opens the one of its windows that printed last, or a new shell window `api` in `~/api`. From anywhere else it's `repose attach todo-app:api`. `--worktree` works there too, in `~/api-worktree-1` and on.

The checkouts share one machine, so they share these:

- Secrets, tool logins and settings. Every checkout sees the machine's secrets.
- Your git identity. If your laptop picks a different email per folder (an `includeIf` for work repositories), the machine has the one from the folder you ran in last.
- The machine's configuration. Only its own checkout's `repose.nix` configures the machine; one in an added checkout is never applied ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)). A `flake.nix` or `.envrc` dev shell still loads per folder.
- Ports. Two dev servers on port 3000 collide; give one another port.
- Disk and snapshots. The checkouts fill one disk, `repose snapshots restore` rolls every one of them back, and `repose rm` destroys them all.
- One record of the `.env` files last sent. Running in two folders by turns sends each set again.

`--on` takes none of PROJECT, `--temp`, `--project` or `--size`, and it refuses a folder that is the machine's own checkout or one that joined another machine; those exit 2. A checkout name the machine doesn't have is refused everywhere with exit 2, so a typo never makes an empty one. [`repose rm todo-app:api`](/docs/cli#repose-rm-project) removes the checkout and its worktrees; the machine and its own checkout stay.

## Detach and come back

Press `Ctrl-b`, let go, then `d`. Everything on the machine keeps running. Closing the terminal does the same.

If Wi-Fi drops or the laptop sleeps while you're attached, `run` and `attach` say so and attach again by themselves once the machine answers, for up to 2 minutes, with the screen as you left it. `Ctrl-C` stops waiting.

`repose attach` puts you back, in the checkout or by name from anywhere:

```
repose attach
repose attach todo-app
```

It syncs nothing, so attaching from a second computer whose copy is older is safe. A stopped machine starts first, as with `run`.

Several terminals can be attached at once, from one computer or several. They see the same windows.

## tmux keys

Each machine has one tmux session. Its first window, `shell`, opens in your checkout, and agents get windows beside it. Press `Ctrl-b`, then:

| Key     | Action                                                      |
| ------- | ----------------------------------------------------------- |
| `w`     | Pick a window from a list.                                  |
| `n` `p` | Next or previous window.                                    |
| `c`     | New window with a shell, in your checkout.                  |
| `[`     | Scroll back, 50,000 lines deep. Arrow keys or Page Up; `q` leaves. |

A window whose agent waits for your input has `?` after its name in the status line, as in `2:claude-2?`. tmux puts the project's name in your terminal's tab title.

Inside a tmux on your laptop, `Ctrl-b` goes to that tmux. Press it twice to reach the machine's: `Ctrl-b` `Ctrl-b` `d` detaches from the machine.

Exiting the last window ends the session, and a new one with a `shell` window starts 5 seconds later; on a [temporary](/docs/lifecycle#temporary-machines) machine it destroys the machine instead.

The mouse stays with your laptop's terminal, so selecting and copying text work as they do outside tmux. For tmux's own mouse (click a window name to switch, scroll with the wheel), run `echo 'set -g mouse on' >> ~/.tmux.conf` on the machine, then `tmux source-file ~/.tmux.conf`.

Shift+Enter is a newline in Claude Code rather than a submit, if your terminal answers tmux's request for modified keys (modifyOtherKeys). Ghostty, WezTerm, iTerm2 and xterm answer; Apple's Terminal doesn't, and there `\` then Enter, or Ctrl+J, is the newline. Links an agent prints stay clickable in a terminal that draws them (OSC 8).

tmux passes 24-bit colour to a terminal that has it (Ghostty, kitty, WezTerm, iTerm2, Alacritty, foot, or any that sets `COLORTERM=truecolor`); Apple's Terminal before macOS 26 gets 256 colours.

## herdr instead of tmux

A machine can run its terminals in [herdr](https://herdr.dev), a multiplexer for coding agents, instead of tmux. `repose run --multiplexer herdr` picks it for one project, and the choice stays with the project. [herdr on your machine](/docs/tutorial-herdr) has the config key that makes it the default.

Until it stops, the machine keeps the multiplexer it's running, and its agents with it:

```
$ repose run --multiplexer herdr
todo-app uses herdr from its next start; tmux runs until then.
```

`repose status` names `herdr` after the size. The dashboard names it on a stopped machine, since a running one may still be on tmux.

On a herdr project the commands on this page work through herdr:

- The sidebar carries the machine under the project's name, and the workspace in its checkout is `checkout`, where `repose run -p` opens the agent's tab. A checkout added with `--on` gets a workspace of its own name, a machine with no checkout gets `home`, and a `--worktree` agent gets one herdr groups under `checkout`.
- `repose attach` from a terminal inside herdr on your laptop opens no client: the machine is in the sidebar already. It prints `todo-app is in herdr's sidebar. Ctrl-C ends its forwards.` and stays, holding your port forwards and the browser bridge up. With no forwards, bridge or MCP forward to hold up, it prints `todo-app is in herdr's sidebar.` and returns. From any other terminal, with herdr 0.9.0 or newer on your PATH, it opens your laptop's herdr with `herdr --remote todo-app.repose --session default`; with no herdr on the laptop, herdr's client on the machine over SSH.
- In place of tmux windows, `repose ps` lists herdr's agents and the workspace each works in. `repose paste` pastes into the focused pane, or into an agent's pane with `--window NAME`.
- In herdr on your laptop, a dropped file or `Ctrl+V` goes to herdr and nothing is copied to the machine. Send an image with `repose paste`, a file with [`repose cp`](/docs/sync#single-files). herdr's client over SSH, without herdr on the laptop, copies drops and pastes as tmux does.

repose's messages inside the session (the time zone, new port forwards, copied files) are herdr notifications, which the machine's `~/.config/herdr/config.toml` turns on. A `config.toml` you put there yourself keeps its own settings, and then those messages don't appear. Add the key and run `herdr server reload-config` on the machine:

```toml
[ui.toast]
delivery = "herdr"
```

To take a machine out of the sidebar for a while, so herdr's connection doesn't hold it [in use](/docs/lifecycle#unused-machines), disable its entry. Its agents keep running.

```
id=$(herdr machine list --json |
  jq -r '.[] | select(.label=="todo-app") | .id')
herdr machine disable "$id"
```

`herdr machine enable` with the same id brings it back. `run` and `attach` leave a disabled entry alone, while `herdr machine remove` doesn't last: the next `run` or `attach` adds the machine back while it runs herdr.

## See what's running, run one command

`repose ps` lists the machine's tmux windows without attaching: what runs in each, the agent's state ([Agent states](/docs/agents#agent-states)), the folder it works in and when it last printed something. `*` is the window `attach` opens on.

```
$ repose ps
WINDOW      COMMAND  STATE        TREE        ACTIVE
0:shell     bash     -            checkout    3h ago
1:claude*   claude   working      checkout    now
2:claude-2  claude   needs input  worktree-1  12m ago
```

To read what an agent printed without attaching, name its window: `repose ps todo-app claude-2` prints its last 20 lines, `-n 50` more. `repose ps -n 5` prints the last 5 lines of every window. `repose attach -w claude-2` opens that window. `repose run -w claude-2 -p 1` types `1` and Enter into it, which answers a permission prompt; any other text is a follow-up for the agent.

`repose exec` runs one command in the checkout on the machine and gives you its output and exit code, the way `docker exec` does. The command gets what an agent there gets: your [secrets](/docs/secrets) as environment variables and the project's [dev shell](/docs/machine#projects-with-a-flake-nix).

```
$ repose exec npm test
$ repose exec todo-app git status --short
$ repose exec -it psql
```

`--workdir worktree-1` runs the command in that worktree instead, and takes any TREE that `ps` shows. The rest of exec's flags are in the [CLI reference](/docs/cli#repose-exec-project-checkout-command-arg).

Unlike `attach`, `ps` and `exec` start no machine: a stopped one gets you exit code 5 and the command to start it.

## Drop a file or paste an image

While you're attached, drag a file onto the terminal, or press `Cmd+V` or `Ctrl+V` with an image on your laptop's clipboard. The file is copied to `/tmp/repose-paste/` on the machine and its path there is pasted where your cursor is. Claude Code attaches an image; any other file reaches it as a path it can open, and the shell gets the path as text.

- A file from your checkout isn't copied. You get its path in the machine's checkout, such as `/home/dev/todo-app/docs/mockup.png`; if the machine's copy isn't there yet or differs in size, the file is copied like any other.
- Drop several files at once to paste several paths. Up to 20 files, 20 MB each. Over either, nothing is copied: you get your laptop's path and the status line says why. [`repose cp`](/docs/sync#single-files) takes the large ones.
- The copies are `dev`'s own, in a 0700 directory with 0600 files. Each new copy deletes the ones over a day old and all but the newest 50.
- The copies are named on the tmux status line, `repose: copied report.pdf to the machine`. When an agent asks you to paste a path, that line is how you see what left your laptop. On herdr it's a [herdr notification](#herdr-instead-of-tmux).

A paste that is nothing but paths of files on your laptop counts as a drop, so pasting a path you copied works too. Some of those paths are never copied and arrive as the text you pasted: hidden files, anything in a hidden folder such as `~/.ssh`, and private keys. A link counts as the file it points to, so a link to a key in `~/.ssh` isn't copied either. Paths under system folders such as `/etc`, `/usr` and `/nix` are pasted as they are, since they name the machine's own files.

A private key is a file whose name starts with `id_rsa`, `id_dsa`, `id_ecdsa` or `id_ed25519` (`.pub` files still copy), a file ending in `.pem`, `.p12`, `.pfx`, `.p8`, `.ppk`, `.jks`, `.keystore`, `.kdbx`, `.keychain` or `.keychain-db`, or a file of any name whose first 4 KB hold a PEM, OpenSSH or PGP private key or an OpenPGP secret key (`.gpg`, `.pgp`). A `.key` file is refused only when it holds such a key, so Keynote decks still copy; public keys, signatures and encrypted `.gpg` files copy too.

On macOS a terminal sends nothing at all for `Cmd+V` when the clipboard holds only an image, so no key handler can see it. While you're attached, repose adds a text version to such a clipboard: the path of a PNG copy in `~/Library/Caches/repose/clipboard/`. `Cmd+V` pastes that path, and the file is copied like a dropped one. The image stays on the clipboard as well, so apps that take images still get it; a plain text field gets the path. When you detach, the text comes off again unless you've copied something since. The next attach deletes the copies over a day old and all but the newest 20. `REPOSE_NO_CLIPBOARD_PATH=1` leaves your clipboard alone, and then only `Ctrl+V` pastes an image.

`Ctrl+V` reads the clipboard itself. With no image on it, `Ctrl+V` is an ordinary `Ctrl+V`, so vim and friends are unaffected. On Linux it needs `wl-clipboard` under Wayland or `xclip` under X11, and the status line names the missing one once.

`REPOSE_NO_INPUT_PROXY=1` turns all of this off: `run` and `attach` hand your terminal straight to `ssh`, and a drop pastes your laptop's path.

[`repose paste`](/docs/cli#repose-paste-project) does the same from a script, or into a window you aren't in, with no key press.

## Links the machine opens

While you're attached, an `https` link a program on the machine hands to `$BROWSER` opens in your laptop's browser. Claude Code's `/login` goes that way, so only the code comes back through the terminal. A link waits two minutes for a laptop to take it and an older one is dropped, so no attach opens yesterday's login page. With two laptops attached, one of them opens each link.

With nobody attached, the link is only [printed on the machine](/docs/machine#browser). Same with `REPOSE_NO_BROWSER=1` on your laptop, or on Linux with neither `DISPLAY` nor `WAYLAND_DISPLAY` set.

## SSH and editors

Every project is also an SSH host called `<project>.repose`, so `ssh`, `scp`, `rsync`, git and editors reach it with no `repose` command first. `repose code` opens the checkout in VS Code, Cursor or Zed. [SSH and editors](/docs/ssh-and-editors) has each editor by hand, JetBrains Gateway, and what to do when a connection fails.

mosh doesn't work: it needs a UDP connection straight to the machine, and the only way in is SSH through repose.

## Time zone

`run` and `attach` set the machine's time zone to your laptop's, each time. New shells get it; one already open keeps the zone it started in. A project with no zone of its own uses the time zone on the dashboard's **Settings** page, or UTC when the account has none.

## When the machine stops

Your agents come back from a [stop](/docs/lifecycle#stop-and-start), and nothing else on the machine does. At the next start each agent's window opens again under its name and in its folder, with the agent continuing the conversation it was in: Claude Code and Codex by the conversation's id, opencode, pi and Gemini CLI with their newest one in that folder. Those three have no id to go back to, so where one of them had two windows in one folder only the first reopens; both would resume the same conversation. The session starts with its `shell` window beside the agents.

An agent that can't resume (a conversation that's gone, or one cut mid-line) exits non-zero within 15 seconds, and its window keeps a login shell under `claude could not continue its conversation (exit N).`

On a herdr project herdr puts its workspaces and tabs back and resumes each agent whose integration recorded a conversation: Claude Code, Codex, opencode and pi. Gemini CLI has no herdr integration, and a tab that was running anything else comes back as a shell in its folder.

### When the machine ends without a stop

A machine whose server crashes, or that can't be brought back when its server restarts, ends without a stop ([When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)). The machine records its agent windows every minute, and the next start reopens the windows of that record the same way. What's lost:

- the turn in progress: the agent picks its conversation up as its own files last recorded it, and doesn't redo the work the crash cut off;
- a window opened in the minute before the crash;
- every other process, and what the windows showed;
- the last few seconds of writes to the disk.

On a herdr project herdr's last save is seconds old rather than a minute, and the start puts back what it holds.
