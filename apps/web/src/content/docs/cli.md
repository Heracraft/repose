---
title: CLI reference
description: Every repose command and flag, config.toml, environment variables and the files the CLI keeps.
section: Reference
order: 40
---

`repose --help`, `repose help COMMAND` and `repose COMMAND --help` print the same in your terminal. A mistyped command gets a suggestion and exit code 2:

```
$ repose lss
unknown command "lss" for "repose"
Did you mean `repose ls`?
`repose --help` shows its usage.
```

Words other CLIs use lead to the repose command: `up`, `create`, `new` and `init` to `run`, `shell` and `connect` to `ssh`, `down` to `stop`, `port`, `forward` and `tunnel` to `open`, `env` to `secrets`, `undo` to `restore`, `clone` to `fork`. A mistyped subcommand gets the same answer with `--help` after it. So does a mistyped flag, or another tool's spelling of one: docker's detach flag on `run` names `-d`, and a force flag on `stop` names `-y`.

## Which project

Commands that act on a project take it from the `PROJECT` argument, `--project NAME`, then `REPOSE_PROJECT`; an argument and a `--project` that disagree exit 2. Otherwise it's the folder you're in: the project with this checkout's git remote, or the machine [last made there](/docs/lifecycle#a-second-machine-for-the-same-repository) in a folder with no remote. [A home folder belongs to no project](/docs/lifecycle#from-your-home-folder), so a command there has to name one.

A command whose usage names the project takes it there or as `--project`. A few take other words in that place, so the project is `--project` alone: `open`, `secrets set`, `secrets rm`, `secrets import`, `secrets choose`, `config add`, `config remove`, `config apply`, `mcp forward` and `mcp rm`; `cp` names it in `PROJECT:PATH` or `--project`. `ls`, `login`, `logout`, `notify`, `version` and `completion` act on no project and exit 2 when given `--project`.

`PROJECT:CHECKOUT` names [another checkout](/docs/run-and-attach#several-repositories-on-one-machine) `run --on` added to the machine. `run`, `attach`, `sync`, `exec`, `ssh`, `code`, `rm`, `mcp list`, `mcp forward` and `browser bridge` take it; every other command refuses it with exit 2.

Global flags: `--project NAME`, `-v`/`--verbose` and `--version`. `-v` writes a line to stderr for each request, with its status, duration, request ID and the body of a refusal, and one for each `ssh` the command runs; never a token or a header.

## Working on a project

### `repose run [PROJECT]`

Create or start this checkout's machine and [attach](/docs/run-and-attach). PROJECT is the machine of that name, created if there is none: [a second machine for this checkout](/docs/lifecycle#a-second-machine-for-the-same-repository), or one for a folder with no git remote. A new machine gets a copy of your checkout. One that already has it is left as it is, and `run` says when your laptop has work to send with `repose sync`. A changed `repose.nix` at the repository's root is applied in the background ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)). `run` and `attach` write your CLI's version to `~/.repose/cli-version` on the machine, so an agent there can tell when your CLI is older than these docs.

Starting a machine needs a [plan](/docs/billing). Without one, `run` opens the billing page, prints `Waiting for a plan at https://repose.herakraft.co/billing...` and waits up to 30 minutes for you to choose. Off a terminal it exits 7 with `Choose a plan at https://repose.herakraft.co/billing first.`

| Flag                      | What it does                                                                                                                                                                                                                                                      |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-p`, `--prompt PROMPT`   | Start an agent on PROMPT, which it takes as its first turn. On a machine where Claude Code has no login yet, the window opens on the login and PROMPT is typed in after it.                                                                                                               |
| `--agent NAME`            | `claude`, `codex`, `opencode`, `gemini` or `pi`, for `-p`. Default: the project's agent, set at creation from [`default_agent`](#config-toml). Without `-p`, NAME becomes the project's agent.                                                                      |
| `-d`, `--no-attach`       | Don't attach afterwards. With `-p`, print the window the agent is in (`Window: claude-2`, or `Tab:` on herdr). If Claude Code has no login on the machine yet, exit 1 without typing the prompt.                                                                                       |
| `-w`, `--window WINDOW`   | Type PROMPT and Enter into that agent's window instead of starting an agent: an answer to a permission prompt (`-p 1`) or a follow-up. WINDOW is a name or number as `repose ps` shows it, or on herdr an agent's name; one the session doesn't have exits 2. The attach opens on it. Not with `--worktree` or `--agent`. |
| `--worktree`              | Start the agent in its own git worktree. Needs `-p`.                                                                                                                                                                                                              |
| `--no-sync`               | Don't copy the checkout, even into a new machine. Your tool logins and git identity are still copied.                                                                                                                                                              |
| `--size small\|large\|xl` | Size of a new project, or of a stopped one this command starts. Default: [`default_size`](#config-toml). A machine of another size exits 2 and names `repose resize`, unless it's stopped; a stopping one is waited out first.                                                              |
| `--on PROJECT`            | Add this folder to PROJECT's machine as [another checkout](/docs/run-and-attach#several-repositories-on-one-machine), beside its own.                                                                                                                               |
| `--machine NAME`          | Run on [a machine you own](/docs/own-machine) added with `repose machine add`, not on a machine repose runs. The checkout stays on that machine from then on, so later runs there need no flag. A point is taken before the agent starts, which is what `repose rewind` goes back to. `--size`, `--temp`, `--worktree`, `--bridge`, `--on`, `--multiplexer` and `--no-personal` are not for such a machine and exit 2. |
| `--temp [DURATION]`       | A new [temporary machine](/docs/lifecycle#temporary-machines), destroyed with no snapshot after DURATION (`10m` to `24h`, or `1d`; default `24h`). `--temp 3h` works anywhere among the arguments.                                                                  |
| `--bridge`                | Also [bridge your Chrome](#repose-browser-bridge-project) to the machine while attached.                                                                                                                                                                           |
| `--bridge-allow HOST`     | Bridge, and let the agents use only this site in your Chrome: the `--allow` of `repose browser bridge`. Repeatable; `*.example.com` is `example.com` and its subdomains.                                                                                           |
| `--no-personal`           | Keep your [machine.nix](/docs/config#your-machine-nix) off this machine from now on: a new one is created without it, and one that has it switches without it in the background.                                                                                   |
| `--multiplexer NAME`      | `tmux` or `herdr`: what runs the machine's terminals, from its next start, and it stays with the project. Default: [`default_multiplexer`](#config-toml).    |

### `repose attach [PROJECT[:CHECKOUT]] [WINDOW]`

Attach to the project's session, on its current window, without syncing. WINDOW (or `-w`/`--window NAME`) opens on that window instead: a name or number as `repose ps` shows it, or on herdr an agent's name. If a project resolves, one word that names none of your projects is WINDOW. A window the session doesn't have exits 2.

A machine that isn't running comes up first, as [`repose start`](#repose-start-project) does. A machine being removed, or one stopped for running a miner, exits 5.

A herdr project attaches in [three ways](/docs/run-and-attach#herdr-instead-of-tmux). `--bridge` [bridges your Chrome](#repose-browser-bridge-project) to the machine for as long as you're attached; `--bridge-allow HOST` does that with an allowlist.

If the connection drops while you're attached, `run` and `attach` print `repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.` and [come back on their own](/docs/run-and-attach#detach-and-come-back). Keys you type while it waits are dropped. With `REPOSE_NO_INPUT_PROXY=1`, the command ends with exit code 255 instead.

### `repose sync [PROJECT]`

Copy this checkout's [current work](/docs/sync) over the machine's checkout, without attaching. It creates or starts the machine if needed. PROJECT works as on `run`: the machine of that name, created if there is none. It only sends; `git fetch repose` brings [the machine's commits back](/docs/sync#getting-work-back).

It writes nothing and exits 6 when the machine changed files your laptop changed too, and names them, or when the machine's checkout is in the middle of a merge, rebase, am, cherry-pick, revert or bisect. Changes the machine made to any other file stay where they are ([When the machine has changes of its own](/docs/sync#when-the-machine-has-changes-of-its-own)). In your home folder, and in a folder that isn't a git checkout, it exits 2.

| Flag                      | What it does                                                       |
| ------------------------- | ------------------------------------------------------------------ |
| `--stash-machine`         | Move the machine's uncommitted changes, untracked files included, to its git stash before syncing. The summary names the stash commit. |
| `--discard-machine`       | The same, and also end a merge, rebase, am, cherry-pick, revert or bisect in progress there. |
| `--size small\|large\|xl` | Size of a new or stopped project, as on `run`.                     |
| `--temp [DURATION]`       | A new temporary machine, destroyed after DURATION (default `24h`). |
| `--multiplexer NAME`      | `tmux` or `herdr`, from the machine's next start, as on `run`.     |
| `--json`                  | Print the project as JSON when done; the text goes to stderr.      |

`--stash-machine` and `--discard-machine` were `--stash-remote` and `--discard-remote`, which still work for one release. `repose run` given any of them exits 2 and names this command.

### `repose ps [PROJECT] [WINDOW]`

The project's windows, without attaching. Needs the machine running.

```
$ repose ps
WINDOW      COMMAND  STATE        TREE        ACTIVE
0:shell     bash     -            checkout    3h ago
1:claude*   claude   working      checkout    now
2:claude-2  claude   needs input  worktree-1  12m ago
```

WINDOW is the window's number and name, with `*` on the one `attach` opens on. COMMAND is the program in the window's active pane, its name and never its arguments. STATE is the [agent's state](/docs/agents#agent-states); `-` is a window with no agent, or one whose agent exited or never reported. TREE is the folder the window works in, `checkout`, `worktree-N`, `~/PATH`, or the path itself outside your home folder, as [`exec --workdir`](#repose-exec-project-checkout-command-arg) takes it. ACTIVE is how long since the window last printed. `-q`/`--quiet` prints the names alone, one per line.

A herdr project lists herdr's agents instead, under `WORKSPACE`, `AGENT`, `NAME` and `STATE`, with `*` on the focused one.

With WINDOW (or `-w`/`--window NAME`), it prints that window's last 20 lines instead, unwrapped; `-n`/`--tail N` for N lines, 1 to 10000. `-n` without a window prints every window's, each under a `==> 1:claude <==` line. On herdr, WINDOW is an agent's name. Where a project resolves, one word that names none of your projects is WINDOW: `repose ps claude-2` in the checkout.

`--json` gives one shape on tmux and herdr: `name`, `agent`, `command`, `agent_state`, `tree`, `focused` and `idle_seconds`, with `null` where the multiplexer can't say (`command` and `idle_seconds` on herdr; `agent` and `agent_state` for a window with no agent). `agent_state` spells `needs input` as `needs_input`, and reads `unknown` where an agent exited or never reported, which the table shows as `-`. `state` repeats `agent_state`, except on herdr, where it keeps herdr's own word (`blocked`, `done`) for this release. For this release it also keeps the old keys: `index`, `current` and `activity` on tmux, `workspace` on herdr.

### `repose exec [PROJECT[:CHECKOUT]] [--] COMMAND [ARG...]`

Run one command in the checkout on the machine, with your secrets and the project's [dev shell](/docs/machine#projects-with-a-flake-nix), as an agent there has them. Output streams back and the exit code is the command's. Needs the machine running. The command is passed word for word, its own flags included, and no shell expands it; for a pipeline, run a shell yourself. If the first word names one of your projects, it is PROJECT; that name with no command after it exits 2. To run a command that has a project's name, put `--` before it. Put `exec`'s flags before the command.

```
$ repose exec npm test
$ repose exec todo-app git log --oneline -3
$ repose exec -it psql
$ repose exec sh -c "npm run build && npm test"
```

`-i`/`--interactive` passes your input to the command; `-t`/`--tty` gives it a terminal. Without either, the command reads nothing. Pass both for anything interactive.

`--workdir DIR` runs the command in DIR instead of the checkout: `worktree-N` for a `--worktree` agent's worktree, `checkout`, `~/PATH`, `/PATH`, or a path inside the checkout, each of the first two with an optional `/SUBDIR`. A folder that isn't there exits 2 before the command runs.

```
$ repose exec --workdir worktree-1 npm test
```

### `repose ssh [PROJECT[:CHECKOUT]]`

Open a login shell in the checkout on the machine, outside the session `attach` opens; `exit` ends it. A machine that isn't running comes up first, without a sync, as with `attach`. A command after PROJECT exits 2 and names `repose exec`.

### `repose code [PROJECT[:CHECKOUT]]`

Open the project's checkout, `/home/dev/<folder>` ([Where the checkout is](/docs/sync#where-the-checkout-is)), in an editor on your laptop, over [SSH](/docs/ssh-and-editors) to `<project>.repose`. A machine that isn't running comes up first, as with `attach`. It uses VS Code (`code`) if it's installed, else Cursor (`cursor`), else Zed, whose command is `zed` or `zeditor`; on a Mac it also looks in `/Applications` and `~/Applications`. A named editor that isn't installed exits 1, as does having none of the three. Under WSL it refuses an editor whose command is the Windows app, which would connect with Windows' `ssh` ([Windows](/docs/ssh-and-editors#windows)).

```
$ repose code todo-app
Opening todo-app.repose:/home/dev/todo-app in VS Code
```

| Flag              | What it does                                                                                             |
| ----------------- | -------------------------------------------------------------------------------------------------------- |
| `--editor EDITOR` | `code`, `cursor` or `zed`. Default: `REPOSE_EDITOR`, else `editor` in config.toml, else the first found. |

### `repose open [LOCAL:]PORT`

Forward one port to your laptop and open it in the browser, until `Ctrl-C`. Needs the machine running. Works for servers on `127.0.0.1`, `0.0.0.0` or `::1`. With nothing listening on the port yet, `open` says `Nothing on todo-app is listening on port 3000 yet; the forward reaches it once it listens on 127.0.0.1 or 0.0.0.0.` and forwards to `127.0.0.1` anyway. The laptop port is PORT, or LOCAL with `repose open 8080:3000`; when it's taken, a free one, and `open` says which. The database ports 5432, 3306, 6379 and 27017 open no browser. If the connection drops, `open` reconnects on the same laptop port, for up to 2 minutes.

| Flag           | What it does        |
| -------------- | ------------------- |
| `--no-browser` | Print the URL only. |

`--local-port N`, the old form of `LOCAL:`, still works.

### `repose browser [PROJECT]`

Watch the agents' [browser](/docs/machine#browser) on the machine and take it over. It starts the machine's desktop view, forwards it in the background to laptop port 6080, or a free one when that's taken, and opens the link in your browser. The view is the size of your tab and sleeps after 30 idle minutes; opening the page again wakes it. Needs the machine running.

| Flag           | What it does         |
| -------------- | -------------------- |
| `--no-browser` | Print the link only. |

`repose open --desktop`, and `--no-open` and `--stop` on `repose browser`, are old names and still work.

### `repose browser stop [PROJECT]`

Stop the view on the machine and the forward on your laptop. The [agents' browser](/docs/machine#browser) keeps running. On a stopped machine it clears the laptop's forward alone.

### `repose browser bridge [PROJECT]`

Let the agents on the machine browse in [your laptop's Chrome](/docs/your-chrome), with your logins and extensions, until `Ctrl-C`: their [browser tools](/docs/machine#browser) drive it instead of the machine's own.

```
$ repose browser bridge
Chrome 144 → todo-app: the agents there browse in your Chrome now,
with your logins. Ctrl-C hands them back the machine's browser.
Chrome asks you to allow each new connection.
Pages the agents open are listed below (host and path only).
An agent on todo-app is in your Chrome.
14:03:21  admin.internal.example/signups
```

Needs the machine running, and Chrome 144 or newer with [its remote debugging switch](/docs/your-chrome#once-turn-chromes-switch-on) on.

| Flag                  | What it does                                                                                                                                                                  |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--allow HOST`        | Let the agents use only this site: other tabs are hidden from them and other sites fail. Repeatable, or comma-separated; `*.example.com` is `example.com` and its subdomains. |
| `--cdp URL`           | Bridge a browser started with a remote debugging port instead (`http://127.0.0.1:9222`): any Chromium, no switch, no dialogs.                                                 |
| `--user-data-dir DIR` | The profile directory of a Chrome that isn't Google Chrome's default one (Chromium, Brave, Edge, a second profile).                                                           |
| `--no-inspect`        | Don't open `chrome://inspect` when remote debugging is off; print what to do and wait. `--no-browser` is its old name.                                                        |

[Stop it](/docs/your-chrome#stop-it) has what ends a bridge, and why only one reaches a machine at a time.

### `repose mcp forward NAME...`

Let the agents on the machine use [MCP servers](/docs/agents#mcp-servers) that run on your laptop, until `Ctrl-C`. NAME is a server in your laptop's Claude Code config (this project's servers, then your user scope), Claude Desktop config (macOS), Codex config or Gemini CLI settings, or the command after `--`, which goes with one NAME. It runs on your laptop, with your apps, files and tokens. `${VAR}` in its config comes from your laptop's environment, and a `${VAR}` that environment lacks stops the forward before it starts. Only servers that start with a command can be forwarded; an HTTP server on your laptop can't be yet. Each agent session on the machine gets its own copy, and each call is printed here by agent and tool.

```
$ repose mcp forward apple-notes
apple-notes: forwarded to todo-app (12 tools). Agents already
running list it after a restart. Ctrl-C ends it.
claude called apple-notes.search_notes
```

```
$ repose mcp forward notes -- node ~/mcp/notes.js
```

Needs the machine running; a machine that stops ends the forward with exit code 5. The agents keep listing NAME after `Ctrl-C`; until the next forward, its tools answer that your laptop isn't connected. A laptop that goes to sleep brings the same answer within about 20 seconds. A dropped connection is named once and retried. A second forward of the same NAME takes over from the first. `[mcp] forward` in [config.toml](#config-toml) forwards servers whenever you're attached.

`--remove` is the old form of `repose mcp rm NAME`.

### `repose mcp rm NAME...`

Alias `remove`. Take forwarded servers off the machine's agents; they drop them at their next start. Needs the machine running. A NAME the machine has no forward for exits 1: `todo-app has no forwarded MCP server named linear.`

### `repose mcp list [PROJECT]`

Every MCP server the agents on the machine have, and the ones your laptop kept. Alias `repose mcp ls`, and `repose mcp` alone. It starts no server. Needs the machine running.

```
$ repose mcp list
NAME         FROM     AGENTS                           STATE
playwright   repose   claude codex gemini opencode pi
linear       laptop   claude codex gemini opencode pi  needs LIN_TOKEN
xcode        laptop   none                             an Apple app
notes-db     project  claude                           ~/todo-app
apple-notes  forward  claude codex gemini opencode pi
my-db        machine  claude
```

FROM is `repose` for the [browser tools](/docs/machine#browser), `laptop` for a server copied from your laptop's Claude Code or kept there (AGENTS `none`, the reason in STATE), `project` for a checkout's `.mcp.json` (Claude Code only), `forward` for [`repose mcp forward`](#repose-mcp-forward-name), and `machine` for one you added on the machine. STATE is empty when the server needs nothing. It starts with the checkout for a server that belongs to one (`~/todo-app`), reads `laptop not connected` for a forwarded server whose laptop is away, and names each agent that lacks the server with why (`codex: ~/.codex/config.toml is a link, which repose does not write`). A file repose had to leave out, such as a `laptop.json` that doesn't parse, is named on stderr after the rows. Piped, each server is one tab-separated `NAME FROM AGENTS STATE` line with no header.

| Flag     | What it does                                                                                                                                                                                           |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--json` | The machine's answer as it sent it: a `servers` array with `name`, `from`, `agents` and `state`, the `checkout`, `needs`, `missing` and `skipped` behind STATE, and `problems` for the files left out. |

### `repose cp [-r] SRC... DST`

Copy files with `scp`. One side names the machine: `PROJECT:PATH`, or `:PATH` for this checkout's project. A relative path on the machine starts in its checkout, or in `/home/dev` on a machine with no checkout. `-r`/`--recursive` copies directories. Several sources go into the directory `DST`, so a shell glob works: `repose cp ./Fwd_* todo-app:/tmp/`. Sources sit on one side and name one project; anything else exits 2. Needs the machine running.

### `repose paste [PROJECT]`

Copy the image on your clipboard, up to 20 MB, to `/tmp/repose-paste/` on the machine and paste its path into the active pane on tmux, or herdr's focused pane. The path is pasted; you press Enter. Needs the machine running. It exits 1 with no image on the clipboard, and on Linux with no clipboard tool, where the message names `wl-clipboard` on Wayland or `xclip` on X11.

| Flag            | What it does                                                                          |
| --------------- | ------------------------------------------------------------------------------------- |
| `--window NAME` | Paste into this tmux window (name or number), or this herdr agent's pane, instead.    |
| `--print`       | Copy the image and print its path on the machine; paste nothing. Not with `--window`. |

### `repose scan [DIR]`

List what the next `repose run` would install on the machine, and why. Installs nothing. DIR defaults to this checkout's root. The list comes in two halves: your laptop's global tools, and the commands the project's scripts call that neither the machine's base nor the project's own dependencies provide. The project half ends with the version pins it found: Node, Ruby and Java with the version the machine gets. A `machine.nix` or a `repose.nix` [takes over a half](/docs/machine#your-laptops-tools-come-along); a `skipped:` line says which half and why. Logged out, or when the server doesn't answer, it reads `~/.config/repose/machine.nix` and says the account wasn't checked. `--json` prints `laptop`, `project`, `node`, `ruby`, `java` and `skipped`.

## Projects

### `repose ls`

Every project in a table. `*` after a name marks the project a command run here with no `PROJECT` acts on ([Which project](#which-project)). `repose projects`, the old name, still works.

AGENTS names the agent and its [state](/docs/agents#agent-states), with how long one that needs input has waited: `claude: needs input 4h`. A `?` means the machine's last report is two minutes or older. Several agents are counted by state, needs input first, with the longest wait: `2 agents: 1 needs input (4h), 1 working`.

LEFT is a column only while a temporary machine is listed: its time left, or `up` once that has run out ([Temporary machines](/docs/lifecycle#temporary-machines)). DISK is a column only while a listed project's disk is 90 percent full or more, and reads `93% full`.

A line under the table names each running project nobody has used for a day ([Unused machines](/docs/lifecycle#unused-machines)). The last line is your plan and how much of it is in use: memory running, the disk your projects hold, egress this month. Under it, a line for each limit in the way: disk over the plan ([Projects](/docs/limits#projects)), egress past the allowance ([Egress](/docs/limits#egress)), a payment that failed ([If a payment fails](/docs/billing#if-a-payment-fails)).

`--json` prints the full records. `--destroyed` lists the destroyed projects `repose restore` can still bring back, one row per name with the earlier destroys counted; `--all` goes with `--destroyed` and lists every row, each with the `ID` that restores it. `-q`/`--quiet` prints the names alone, one per line:

```
repose stop $(repose ls -q)
```

### `repose status [PROJECT]`

One project in detail:

```
$ repose status
todo-app  running 2h14m  large
  agents     claude-2 needs input 4h, claude working
  checkout   main: 3 commits not on this laptop, 2 files not committed
             worktree-1: nothing new
  attached   nobody
  listening  node :5173 up 3d 410.0 MB
  disk       6.2 GB of 39.0 GB (40G disk), snapshot 11h08m ago
  last event 14m ago, claude finished "Added auth flow"
```

`checkout` has a line for the machine's checkout and one for each git worktree beside it: the branch, its commits your laptop doesn't have (`git fetch repose` gets them), and the files `git status` lists. Your laptop's commits are read from every checkout of the project on it, wherever you run the command; with none, the line gives the last commit's age instead. A running machine that doesn't answer in 4 seconds gets one row instead: `checkout   unknown (no ssh answer in 4 s)`. A stopped project shows the rows its last stop from this laptop read, with when it read them: `main: 3 commits not on this laptop (at the stop, 9h ago)`, or `unknown at the stop, 9h ago (no ssh answer in 15 s)` when that read failed.

`listening` gives each listening process, its port, how long it's been up and its memory, and so which ports an attach [forwards](/docs/machine#ports). A `docker` row counts the containers running. `disk` is the machine's filesystem, used over size, then the disk's size as `repose resize` names it; the filesystem keeps a few percent of the disk for itself. That figure comes from the machine, or from its [last sample](/docs/secrets#what-repose-stores) when it doesn't answer, and with neither the row is the disk's size alone. `-v` adds a `host` row with the server and the machine's address.

Above the rows, one line each for what needs you: the machine [unused for a day](/docs/lifecycle#unused-machines) or [stopped for a miner](/docs/limits#what-isnt-allowed), a [temporary one's end](/docs/lifecycle#temporary-machines). Then settings a new project wouldn't get (`setup: agent codex, machine.nix off, base updates held`), [an error](/docs/troubleshooting#machine-state) or a new system that didn't boot, repose's service not answering, which makes the rows under it old, a [disk 90 percent full](/docs/machine#memory-and-disk) with the `repose resize` that doubles it up to 320 GB, and the plan warnings `repose ls` prints.

| Flag                 | What it does                                                                                                                                            |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--json`             | The project's record, with the checkout's lines under `git`: `worktree`, `branch`, `commits_not_on_laptop`, `uncommitted_files`, `last_commit_at`. A running machine that didn't answer has `git_error` (`ssh_timeout` or `ssh_failed`) in place of `git`. A stopped one has the lines this laptop read before stopping it, or `git_error` when that read failed, and `git_at`, when it read them. |
| `-f`, `--follow`     | Redraw every 5 seconds until Ctrl-C. Piped, it prints the status again only when it changed; with `--json`, as one line. A read that fails because the server is unreachable or busy prints `Could not read the status at 21:38:02:` and the reason, and the loop goes on; any other failure ends it. `--watch`, the old name, still works. |
| `--wait STATE`       | Wait until the machine is in STATE, any [project state](/docs/lifecycle#states), then print the status. Exits 1 if it lands in `error` or is destroyed instead, or if the wait runs out. Not with `-f`. |
| `--timeout DURATION` | How long `--wait` waits before exiting 1 (default `10m`). Only with `--wait`.                                                                            |

### `repose start [PROJECT]`

Start a stopped machine, or restart one in `error`. Doesn't sync. One starting is waited for; one stopping or being restored is waited out and then started; one whose [server is restarting](/docs/lifecycle#when-a-machines-server-restarts) waits for the server. A machine already running prints `todo-app is already running.`, unless repose's service on it stopped answering, which restarts it. `--json` prints the project as JSON when done.

### `repose stop [PROJECT...]`

Stop the machine and snapshot its disk, several projects at once with one line each ([Stop and start](/docs/lifecycle#stop-and-start)).

| Flag                    | What it does                                                                                                                                                                                   |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--no-snapshot`         | Stop without a snapshot. It can't change a stop already under way, and the line says so.                                                                                                        |
| `--unused`              | Stop every machine `repose ls` shows as [unused](/docs/lifecycle#unused-machines); it takes no PROJECT. `No machine is unused.` when none is.                                                   |
| `-y`, `--yes`           | Don't ask when an agent is `working` or `needs input` (required without a terminal). The stop line is then followed by the agent it ends: `Ending claude (working).`, or `Ended claude (working).` with `--wait`. |
| `--wait`                | Wait for the snapshot and the shutdown instead of returning once the machine reads `stopping`: `Stopped todo-app in 11s with a 2.1 GB snapshot.` Exits 1 when the stop failed.                   |
| `-w`, `--window WINDOW` | Close one agent's window and leave the machine running. One PROJECT, and not with `--no-snapshot`, `--wait` or `--unused`.                                                               |
| `--json`                | The project as JSON (`stopping`, or with `--wait` as the stop left it), an array for several projects or `--unused`; the text goes to stderr.                                                   |

Before a machine goes it runs `git fetch repose` in every folder on this laptop whose `repose` remote is one of the machines being stopped, wherever you run it, and prints what came: `Fetched 3 commits on repose/main.`, or `Fetched 3 commits on repose/main in ~/code/api.` for a folder other than the one you're in. A fetch that fails is one line on stderr and the stop goes ahead. The stop line ends with what stays on the machine: `Stopping todo-app; 3 commits on main not fetched, 4 files not committed.` Commits are counted only where this laptop has a checkout of the project. The read of the machine gets 15 seconds, and the clause says when it didn't answer: `Stopping todo-app; checkout unknown (no ssh answer in 15 s).`, or `(ssh failed: Connection refused)`. A machine already stopping prints `todo-app is already stopping.`, a stopped one `todo-app is already stopped.`

`-w WINDOW` closes that agent's window, a name or number as `repose ps` shows it, or on herdr an agent's name, where only its pane closes. It asks about a busy agent as a stop does. On tmux it also removes the window's [worktree](/docs/run-and-attach#several-agents-separate-trees) when nothing there would be lost, after the same fetch: `Closed claude-2 on todo-app and removed its worktree ~/todo-app-worktree-2.` The branch stays on the machine. Otherwise the worktree stays and the line names what kept it: `Closed claude-2 on todo-app; its worktree ~/todo-app-worktree-2 stays: worktree-2 is not fetched to this laptop.`

### `repose rm [PROJECT...]`

Delete the machine and disk; a final snapshot is kept 30 days, and a [temporary machine](/docs/lifecycle#temporary-machines) keeps none. A running machine's commits are fetched first, as `stop` fetches them. Several projects get one question, which names any `working` or `needs input` agent. `-y`/`--yes` skips the question (required without a terminal), and the line then says until when the snapshot is kept. `--wait` waits for the destroy. Run in the project's checkout, it removes the `repose` git remote.

`repose rm PROJECT:CHECKOUT` removes only [a checkout `run --on` added](/docs/run-and-attach#several-repositories-on-one-machine): its folder and its worktrees on the machine, and the link from your laptop folder. Needs the machine running. Nothing is fetched first, and the question names what goes: `Remove ~/api from todo-app? It is deleted with its worktrees, uncommitted work and any commits you have not fetched or pushed. [y/N]` It exits 6 while a process works in one of those folders, and 2 for a `PROJECT:CHECKOUT` passed beside any other name.

`repose destroy`, the old name, still works.

### `repose keep [PROJECT] [DURATION]`

Make a [temporary machine](/docs/lifecycle#temporary-machines) a normal one, no longer destroyed when its time runs out. With DURATION it stays temporary and is destroyed that long from now instead: `tmp-k3f9 is temporary until Sep 29 17:02.` DURATION is `10m` to `24h`, such as `3h`; `1d` is `24h`. One argument that is a number and a unit (`s`, `m`, `h`, `d` or `w`) is DURATION, so a project called `2fa` is a project. It adds no git remote; reach it by name. On a project that isn't temporary it prints `todo-app is not temporary.` and does nothing.

### `repose restore [NAME]`

Bring back a project destroyed in the last 30 days ([Destroy and restore](/docs/lifecycle#destroy-and-restore)). `--as NEW-NAME` restores under another name, `--snapshot ID` from an older snapshot than the newest. With no NAME it takes the destroyed project whose git remote is this checkout's, and asks which one when several share that remote. It asks for a name when a live project holds this one, and exits 2 naming `--as` without a terminal. A destroy still running is waited out for up to 3 minutes, then exits 1. It ends with `Restored todo-app from its 2.1 GB snapshot of 2026-10-08 21:37 in 1m12s; it is running (large).`

### `repose fork [PROJECT]`

Copy the project's disk as it is now into [new projects](/docs/lifecycle#fork-a-project), each on its own machine. The project itself keeps running. Without `--snapshot` it has to be running or stopped: a stopping one is waited out, and any other state exits 5.

| Flag                      | What it does                                                                                                                                         |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-n`, `--count N`         | How many copies: 1 to 10, default 1. Another number exits 2.                                                                                         |
| `--name NAME`             | Name them `NAME-1`, `NAME-2` and so on, in place of `PROJECT-fork-1`. Numbers a live project of yours already has are skipped.                        |
| `--size small\|large\|xl` | Size of the copies. Default: the project's.                                                                                                          |
| `--snapshot ID`           | Copy one of the project's [snapshots](#snapshots) instead of its disk as it is now.                                                                  |
| `--prompt TEXT`           | Start the agent in every copy with that prompt. Your checkout isn't synced into them.                                                                |
| `--agent NAME`            | Which agent `--prompt` starts: `claude`, `codex`, `opencode`, `gemini` or `pi`. Default: the project's. Needs `--prompt`.                             |
| `--no-start`              | Create the copies stopped, so they take none of your plan's memory until `repose start`. Not with `--prompt`.                                        |
| `--json`                  | Print the copies as JSON.                                                                                                                            |

In a checkout whose `repose` remote is the project's machine, each copy gets a git remote of its own name; `repose rm` of a copy removes that remote again.

Each copy is a project, and counts toward [the 100 an account can have](/docs/limits#projects) and toward your plan's disk and memory. A fork that would pass any of those is refused before anything is copied and exits 7; the memory refusal names `--no-start`.

### `repose resize [PROJECT] [DISK]`

Grow the project's disk: `repose resize 80G`, or `repose resize todo-app 80G` for a project other than this checkout's. DISK needs a unit, `M`, `G` or `T`, all binary (`80G`, `80GB` and `80GiB` are one size); a bare number exits 2. Disks can't shrink: a smaller size exits 2 and names the disk's size, and a size it already has changes nothing. A grow ends with `Resized todo-app's disk to 120G.` One argument that reads as a size is DISK; anything else is PROJECT. A disk past your [plan's ceiling for one project](/docs/billing#what-a-plan-means) exits 7.

`--size small|large|xl` [changes the project's size](/docs/machine#changing-the-size), for example `repose resize --size xl` when the machine keeps running out of memory. A running project has to be stopped for the change, so it asks first: `todo-app is running. Changing it from large to xl stops it, which ends every process on it, agents included, then starts it again. Go ahead? [y/N]`. `-y`/`--yes` skips the question and is required without a terminal. A size your plan can't run beside what runs now exits 7 before the question, and nothing is stopped. The change ends with `Changed todo-app from large to xl: 4 cores, 16 GB memory; needs the Plus plan. Running again in 43s.`; a stopped project's line ends `It starts at the new size.` instead of the time.

DISK and `--size` go in one command, the disk first: `repose resize todo-app 160G --size xl`. With neither, it exits 2. `--json` prints the project as JSON when done.

### `repose logs [PROJECT]`

One of the machine's logs: `--kind console|build|ops`, default `console`. Lines come oldest first. A stopped machine, or one in `error`, still has its logs.

`console` is what the machine printed during a boot that failed: the last 200 lines of each such boot, 16 KB at most, over its last 20 operations. A machine that booted cleanly has none. `build` is the last configuration build's log. `ops` is one line per operation: its kind, how it ended (`done` or `failed`) or that it hasn't (`pending`, `running`), how long it took, and a failed one's error code and message:

```text
2026-10-08T21:38:02-04:00 stop done 4.2s
```

`-n`/`--tail N` keeps the last N lines of the first read; `-f`/`--follow` then prints each new line, polling every 2 seconds. `--json` prints one JSON object per line.

`--since` on `logs` and `events` takes a duration back from now (`90m`, `2d`, `1w`, `1d12h`), a date (`2026-10-01`, from local midnight) or an RFC 3339 time (`2026-10-01T08:00:00Z`). Anything else exits 2. `logs` has no default; `console` and `build` hold only the last boots and build.

### `repose events [PROJECT]`

Every event in the window, oldest first, one per line: time, the agent and its window, what happened in the [word its notification uses](/docs/notifications), the summary. Outside a checkout, with no PROJECT, it covers every project and adds a project column. `--since 3d` sets the window (default `24h`); its forms are under `repose logs`. `-f`/`--follow` polls for new events every 10 seconds. `--json` prints one JSON object per line, with the event's `kind` and `project`. An empty window prints `No events on todo-app in the last 24h.` on stderr.

```text
2026-10-08T20:58:30-04:00  -           snapshot  taken (stop)
2026-10-08T20:59:04-04:00  claude (1)  finished  Added tests
2026-10-08T21:12:40-04:00  claude (2)  asks      Drop old table?
```

### `repose questions [PROJECT]`

The [questions](/docs/notifications#agents-can-message-you-and-ask-questions) agents are waiting on you to answer.

```text
todo-app  claude-2 asked 12m ago, expires in 18m  (id 7f3a1c2d)
  Drop the legacy sessions table?
  options: yes|no
```

The window is the one `repose attach` takes, or the agent's name where the window has none; the id is the question's last 8 characters. With no PROJECT it covers every project on your account. Only PROJECT or `--project` narrows that, not the checkout and not `REPOSE_PROJECT`.

Agents waiting at a prompt in their own terminal, such as a permission prompt, come after the questions, by window and project. `repose reply` can't answer those; `repose run PROJECT -w WINDOW -p TEXT` types into the window instead.

`-q`/`--quiet` prints only the question ids, whole. `--json` prints the questions alone; `repose ls --json` carries the agents at a terminal prompt, in each project's `signals.agents` with `state` `needs_input`.

### `repose reply [PROJECT] [--] [ANSWER...]`

Answer a [waiting question](/docs/notifications#agents-can-message-you-and-ask-questions): `repose reply todo-app yes`. The first word is the project when it names one of your projects; otherwise every word is the answer. Put `--` before an answer that starts with a project's name: `repose reply -- todo-app is fine`. A project with no waiting question exits 1. With several waiting it lists them and exits 2; name the project, or pass `--question ID`, a whole id or its last characters as `repose questions` shows them. With no answer it asks for one in the terminal, and off a terminal exits 2. The answer to a question with options has to be one of them. A question answered or closed before your answer lands exits 1 and says how: `Not sent: answered in the terminal.` `--json` prints the answered question.

`repose reply --dismiss [PROJECT]` cancels the waiting question without an answer, chosen the same way. With no question waiting it exits 1, as `reply` does. One that closed before the dismiss landed exits 1 with `Not dismissed: answered in the terminal.`

## Snapshots

| Command                                          | What it does |
| ------------------------------------------------ | ------------ |
| `repose snapshots list [PROJECT]`                | Alias `ls`, and `repose snapshots` alone. The table shows the last 8 characters of each id; `--json` for JSON, `-q`/`--quiet` for whole ids. |
| `repose snapshots create [PROJECT]`              | Take one now; prints its id. `--json` prints the snapshot as JSON. |
| `repose snapshots restore [PROJECT] SNAPSHOT_ID` | Replace a stopped project's disk and start it, after [a question](/docs/lifecycle#snapshots) about whether the restore can be undone. `--as NAME` restores into a new project instead, which starts too and ends as [`repose restore`](#repose-restore-name) does; `-y`/`--yes` skips the question. |

SNAPSHOT_ID is a whole id, or its last or first characters (4 or more) when only one snapshot has them. `--as-new` is the old name of `--as`.

## Machines you own

A machine you own is a Linux box you can already ssh into. `repose machine add` installs repose on it over that same ssh: it checks the box, provisions undo space on btrfs, and installs repose's agent and the coding agents. The box keeps its own login, and the laptop reaches it directly ([Your own machine](/docs/own-machine)).

| Command | What it does |
| ------- | ------------ |
| `repose machine add NAME --ssh [user@]HOST[:PORT]` | Add the box as NAME and install repose on it, with a line per step. Without `user@` the login is this computer's. `--key PATH` names the key the box authorised, and `--undo-gb N` how much undo space to provision, 40 GB by default. `--agent-url URL` with `--agent-sha256 HEX` installs that build of repose's agent instead of the one from the signed release, for a box running your own build; one without the other exits 2. Run again on a machine already added, it installs over it; the same name at another address exits 1. |
| `repose machine ls` | The machines you have added, with the undo space each one holds. Alias `list`, and `repose machine` alone. A machine that does not answer shows as `unreachable`. |
| `repose machine rm NAME` | Forget it: the laptop stops reaching it, and forgets which projects are on it. Alias `remove`. What is installed on the box, and its rewind points, stay as they are. A name that is no machine of yours exits 5. |

### `repose rewind [PROJECT]`

Put the project's files back to a rewind point, a moment a btrfs snapshot on its machine holds: the newest point, or the one `--to POINT` names. It asks first, naming when the point was taken, since everything written since is lost; `-y`/`--yes` skips the question, and a no exits 1. The agents and processes writing in the project are stopped before the swap, and the line says how many. `--list` prints the points, newest first, with when each was taken, why it was taken, and what it holds. A project with no points exits 5, and so does a folder whose project is on no machine you added.

Rewind points live on that machine's disk, so [they are not a backup](/docs/own-machine#points-are-not-a-backup): the disk that dies takes them with it.

## Secrets

| Command                         | What it does |
| ------------------------------- | ------------ |
| `repose secrets set NAME`       | Asks for the value, or reads it from a pipe (`op read op://dev/stripe/key \| repose secrets set STRIPE_KEY`, one trailing newline dropped). `--from-file PATH` reads a file, `--from-env` reads `$NAME`. `NAME=VALUE` exits 2, since the value would stay in your shell history. `--global` sets it on your account, for [every project](/docs/secrets#secrets-for-every-project). |
| `repose secrets import [FILE]`  | Set every `NAME=VALUE` in a `.env` file (default `./.env`, `-` for stdin). `--mcp` sets the secrets your carried MCP servers need from the tokens in your laptop's Claude Code config instead, and asks once before replacing secrets the project already has (`-y`/`--yes` replaces them without asking). `--global` sets them on your account, and with `--mcp` reads your user-scope servers alone. `--dry-run` lists the names and sends nothing. |
| `repose secrets list [PROJECT]` | Names and dates, never values. Alias `ls`, and `repose secrets` alone. On a terminal your account's secrets follow, marked where the project's own wins, then the logins your laptop copies at each run; piped, each line is `NAME`, a tab and the date. `--json` for JSON. `--global` lists your account's. |
| `repose secrets rm NAME`        | Delete it. Alias `remove`. `--global` deletes your account's. |
| `repose secrets choose`         | Choose which of your laptop's [logins and files](/docs/secrets#choose-what-is-copied) `repose run` copies: `gh`, `codex`, `opencode`, `env` (gitignored `.env` files) and `mcp` (your Claude Code MCP servers). In a terminal, a list to toggle; otherwise it prints what is on and off. `--off NAME...` leaves them on your laptop and the next run removes the copies already on the machine, `--on NAME...` copies them again. `--project` sets one project's list; without it the list for every project changes, whatever folder you're in. `--reset` drops the list it would set: a project goes back to the shared one, and the shared one back to copying every login. |

An account secret is for every project, so `--global` beside a project exits 2.

## Configuration

| Command                             | What it does |
| ----------------------------------- | ------------ |
| `repose config add PACKAGE...`      | Add [menu entries](/docs/config#the-menu) or nixpkgs packages, build and apply. With no names, list the menu's entries by group. |
| `repose config remove PACKAGE...`   | Remove them again. Alias `rm`. |
| `repose config show [PROJECT]`      | Print the project's `repose.nix`. `repose config` alone does the same. |
| `repose config revisions [PROJECT]` | Its revisions, newest first, with the last 8 characters of each id, whether it built, and whether it carries your machine.nix. `--json` for JSON, `-q`/`--quiet` for whole ids. |
| `repose config edit [PROJECT]`      | Edit in `$VISUAL` or `$EDITOR`, apply on save. |
| `repose config apply [PATH]`        | Apply a file, by default `./repose.nix`. With no PATH and no such file, apply the newest revision that built again, on a running machine. `--revision ID` switches the running machine back to an earlier revision instead; ID is a whole id or 4 or more characters of either end. |
| `repose config on [PROJECT]`        | Give the project's machine your machine.nix again. |
| `repose config off [PROJECT]`       | Keep your machine.nix off the project's machine. [`repose run --no-personal`](#repose-run-project) does it at a run. |

With `--global`, the same commands act on your [machine.nix](/docs/config#your-machine-nix) instead of the project's file. `--global` takes no project; `on` and `off` take one, with or without it. `add` and `remove` edit the file's `home.packages = with pkgs; [ ... ];` list and push it, `edit` opens the laptop's copy and pushes it on save, `show` prints the account's copy, `revisions` lists its saves, and `apply [PATH]` pushes a file over the account's copy, by default `~/.config/repose/machine.nix`; an empty file removes it. `repose config show --revisions` is the old form of `revisions`.

The CLI's own settings are keys in [config.toml](#config-toml); `repose config set` exits 2 and names the file.

## Account

| Command | What it does |
| ------- | ------------ |
| `repose login`                                  | Log in with a code: the CLI prints a link with the code in it, and opens it in this computer's browser when it has one. `--no-browser` only prints it. `--status` prints the account you're logged in as, its server and its plan, and exits 3 when you aren't. `--browser` is for a self-hosted login server ([Other servers](#other-servers)). No login works without a browser somewhere, so CI can't log in. |
| `repose logout`                                 | Log out and revoke your SSH certificates; the connections they opened [close within 30 seconds, on any device](/docs/limits#ssh-connections). A failed revoke still logs out on this laptop, says so and exits 1; the certificates stop working within 24 hours. `--purge` also removes [the CLI's files](#files-on-your-laptop). |
| `repose notify`                                 | Where notifications go: `email: on, to you@example.com`, `ntfy: off` or its URL, `gchat: on, space AAAA...`, `off`, or `broken since 2026-01-02 (Google refused the webhook)`. `--json` for JSON. |
| `repose notify set`                             | `--email on\|off`, `--ntfy URL\|off` (`none` is the old `off`), `--gchat URL\|off` (a [Google Chat](/docs/notifications#google-chat) webhook). |
| `repose notify test`                            | Send a test on every channel that's on, and print `sent`, `off` or `failed` for each, with the reason the server gave (`failed: the server answered 403`). Six tests a minute. Exits 1 when a channel failed or none is on. |
| `repose version`                                | Print the version, as `repose 0.1.36 (herakraft)`. |
| `repose completion bash\|zsh\|fish\|powershell` | Print a shell completion script. [Install](/docs/install#shell-completion) has the line that loads it for each shell. |
| `repose help [COMMAND]`                         | Print help for a command. `repose help environment`, `repose help exit-codes` and `repose help config-file` print the tables below. A name that is neither exits 2. |

## config.toml

`~/.config/repose/config.toml` is optional.

```toml
default_size = "small"
default_agent = "codex"
editor = "cursor"
default_multiplexer = "herdr"

[sync]
exclude = ["dist", "*.mp4"]

[logins]
skip = ["gh"]

[projects.todo-app.logins]
skip = ["gh", "env"]

[mcp]
forward = ["apple-notes"]

[projects.todo-app.mcp]
forward = ["figma"]
```

| Key                   | Default  | What it does                                                                                                                                                                                                |
| --------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `default_size`        | `large`  | Size of new projects. `default_class` is its old name and still works; `default_size` wins when a file has both.                                                                                            |
| `default_agent`       | `claude` | Agent for new projects.                                                                                                                                                                                     |
| `editor`              | none     | What `repose code` opens: `code`, `cursor` or `zed`. `--editor` and `REPOSE_EDITOR` win over it.                                                                                                            |
| `default_multiplexer` | none     | `tmux` or `herdr` for new projects. Without it, a project you create from a herdr pane gets herdr and any other gets tmux. Any other value stops every command with an error naming the key.                |
| `sync.exclude`        | none     | More gitignore-style patterns the sync leaves out.                                                                                                                                                          |
| `logins.skip`         | none     | Logins `repose run` leaves on your laptop: `gh`, `codex`, `opencode`, `env`, `mcp`. `repose secrets choose` sets it.                                                                                        |
| `mcp.forward`         | none     | MCP servers [`repose mcp forward`](#repose-mcp-forward-name) runs whenever you're attached to any project, until the last attach to that project ends.                                                      |
| `projects`            | none     | Per-project tables. `[projects.NAME.logins]` with `skip` replaces `logins.skip` for that project; `skip = []` copies everything for it. `[projects.NAME.mcp]` with `forward` adds servers for that project. |
| `api_url`             | hosted   | See [Other servers](#other-servers).                                                                                                                                                                        |
| `logto_issuer`        | hosted   | The login server. See [Other servers](#other-servers).                                                                                                                                                      |
| `logto_client_id`     | hosted   | The CLI's application id there. See [Other servers](#other-servers).                                                                                                                                        |

## Environment variables

| Variable                     | What it sets                                                                                                                                                                                |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `REPOSE_PROJECT`             | The project to act on, like `--project`.                                                                                                                                                    |
| `REPOSE_NO_FORWARD=1`        | Don't forward ports automatically while attached.                                                                                                                                           |
| `REPOSE_TIMING=1`            | Print how long each step of `run` and `attach` took.                                                                                                                                        |
| `REPOSE_NO_SPINNER=1`        | One line per step instead of a progress line. `TERM=dumb` does the same. Questions are still asked.                                                                                         |
| `REPOSE_NO_FASTPATH=1`       | Check with the server before every connection instead of reusing the last one. Slower; for when a connection keeps failing.                                                                 |
| `REPOSE_NO_BROWSER=1`        | Never open a browser, even with `repose login --browser` or for a link the machine opens while you're attached.                                                                             |
| `REPOSE_NO_INPUT_PROXY=1`    | Don't copy dropped files or pasted images to the machine; `run` and `attach` hand the terminal straight to `ssh`. `REPOSE_INPUT_PROXY=0` is its old name.                                   |
| `REPOSE_NO_CLIPBOARD_PATH=1` | On macOS, leave the clipboard alone while you are attached, so `Cmd+V` with only an image on it pastes nothing; `Ctrl+V` still pastes the image. `REPOSE_CLIPBOARD_PATH=0` is its old name. |
| `REPOSE_API_URL`             | Like `--api-url`.                                                                                                                                                                           |
| `REPOSE=1`                   | Set on every repose machine, so scripts can tell where they run.                                                                                                                            |
| `XDG_CONFIG_HOME`            | If set, the CLI's files are in `$XDG_CONFIG_HOME/repose/`.                                                                                                                                  |
| `CLAUDE_CONFIG_DIR`          | Where your laptop's Claude Code setup is copied from, instead of `~/.claude`.                                                                                                               |
| `CODEX_HOME`                 | Where `repose mcp forward` reads your Codex config, instead of `~/.codex`.                                                                                                                  |
| `VISUAL`, `EDITOR`           | The editor for `repose config edit`. Default `vi`.                                                                                                                                          |
| `WAYLAND_DISPLAY`            | On Linux, `repose paste` reads the Wayland clipboard with `wl-paste` when this is set.                                                                                                      |
| `DISPLAY`                    | Otherwise it reads the X11 clipboard with `xclip`.                                                                                                                                          |
| `REPOSE_EDITOR`              | The editor `repose code` opens: `code`, `cursor` or `zed`, over `editor` in config.toml.                                                                                                    |

## Other servers

The CLI talks to the hosted repose until you point it somewhere else with `--api-url URL`, `REPOSE_API_URL` or `api_url` in config.toml, first of those wins. `--help` leaves `--api-url` out. The URL carries a version prefix, as the hosted `https://api.repose.herakraft.co/v1` does. `logto_issuer` picks the login server and `logto_client_id` the CLI's application there; it has to be a Logto, since the CLI reads OIDC discovery from `/oidc` under the issuer.

`repose login --browser` logs in through a browser on this computer instead of a code, and needs a login application that registers a loopback redirect URI. The hosted one registers none, so `--browser` there, where a browser can be opened, waits 5 minutes and exits 3 with `Login failed: timed out waiting for the browser login (5 minutes).`

## Files on your laptop

| Path                | What it holds                                                                                                                                                                                                                                       |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `~/.config/repose/` | `config.toml`, your login at mode 0600, your [`machine.nix`](/docs/config#your-machine-nix) with `machine.nix.state` beside it, `machines.toml` and `projects.toml` for the [machines you own](#machines-you-own), and caches that are safe to delete. On macOS the refresh token is in the keychain.                     |
| `~/.ssh/repose/`    | The CLI's own SSH key, its 24-hour certificate, `hosts` with one `Host` block per project, `machines` with one per machine you own, and the `config` that `~/.ssh/config` includes ([SSH and editors](/docs/ssh-and-editors#how-it-works)).                                                   |
| `~/.ssh/config`     | The `Include ~/.ssh/repose/config` line at the top, under a `# added by repose` comment. No other line changes.                                                                                                                                      |
| `.git/config`       | In each project's checkout, the `repose` remote. Nothing is committed.                                                                                                                                                                              |

`repose logout --purge` removes all of these but the `repose` remotes and `machine.nix`, which is yours; `git remote remove repose` removes one remote.

## Output

Tables print local time to the minute (`2026-10-08 21:37`); `logs` and `events` print local RFC 3339; `--json` prints UTC. Progress lines go to stderr. Without a terminal each step prints a line when it starts, that line with its time every 30 seconds while it runs, and a `✓` line with its time when it ends, unless the command prints the step's result itself. Scripts should read `--json` or `-q`: the text of tables and messages can change between releases.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | Worked. |
| 1 | Failed, or you answered no to a question; the message says why. A network failure, the login server's included, exits 1 too. |
| 2 | Wrong usage. |
| 3 | Not logged in, or the login expired. |
| 4 | No such project. |
| 5 | The machine isn't running. On a [machine you own](#machines-you-own): the project is on no machine you added, or the rewind point you named is not there. |
| 6 | The machine changed files the sync would write, or is in the middle of a merge or rebase; the sync stopped. |
| 7 | A plan or account limit refused it (memory, disk, egress, the project count), or a payment problem; the message names it. |
| 8 | No capacity, before or during a start. The message says which kind and what works for it: free memory, the host's disk pool, its build queue, its store, or a host being emptied. Stopping a machine frees memory, and nothing else. Choosing a plan while every seat is taken answers with your place on the [waitlist](/docs/limits#when-repose-is-full) and this code too. |
| 10 | The configuration build failed. |
| 130 | Interrupted with `Ctrl-C`, at a question too. |

Once `run`, `attach` or `ssh` has connected you, the exit code is `ssh`'s; `255` means the connection dropped and the command could not reconnect. `repose cp` returns `scp`'s. `repose exec` returns the command's code once the command has started, so a `4` from your test runner is the test runner's; the codes above come only from failures before it starts, and each prints a message first.
