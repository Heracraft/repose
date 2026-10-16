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

Commands that act on a project use, in order: the `PROJECT` argument, `--project NAME`, the `REPOSE_PROJECT` environment variable, then the current checkout's git remote. In your home folder, or a folder above it, only the first three count ([From your home folder](/docs/lifecycle#from-your-home-folder)). `PROJECT:CHECKOUT` names a checkout that `run --on` added to the machine; `run`, `attach`, `sync`, `exec`, `ssh`, `code` and `rm` take it, and the other commands exit 2.

Global flags: `--project NAME`, `-v`/`--verbose` (one line on stderr for each API request, with its status, time and request ID and the body of a refusal, and one for each `ssh` the command runs; never a token), `--version`, and `--api-url URL`, which `--help` leaves out (see [Other servers](#other-servers)).

If a newer release of the CLI is out, the next command says so once on stderr, with the install command that updates it. It says it again only for a later release.

A command whose usage shows `[PROJECT]` takes the project there or as `--project`. A few take other words in that place, so the project is `--project` alone: `open`, `secrets set`, `secrets rm`, `secrets import`, `secrets choose`, `config add`, `config remove`, `config apply`, `mcp forward` and `mcp rm`; `cp` names it in `PROJECT:PATH` or `--project`. `ls`, `login`, `logout`, `notify`, `version` and `completion` act on no project and exit 2 when given `--project`.

## Working on a project

### `repose run [PROJECT]`

Create or start this checkout's machine and [attach](/docs/run-and-attach). PROJECT is the machine of that name, created if there is none: [a second machine for this checkout](/docs/lifecycle#a-second-machine-for-the-same-repository), or one for a folder with no git remote. A new machine gets a copy of your checkout. One that already has it is left as it is, and `run` says when your laptop has work to send with `repose sync`. A changed `repose.nix` at the repository's root is applied in the background ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)). `run` and `attach` write your CLI's version to `~/.repose/cli-version` on the machine, so an agent there can tell when your CLI is older than these docs.

Starting a machine needs a [plan](/docs/billing). Without one, `run` opens the billing page, prints `Waiting for a plan at https://repose.herakraft.co/billing...` and waits up to 30 minutes for you to choose. Off a terminal it exits 7 with `Choose a plan at https://repose.herakraft.co/billing first.`

| Flag                      | What it does                                                                                                                                                                                                                                                      |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-p`, `--prompt PROMPT`   | Start an agent and type PROMPT into it. On a machine where Claude Code has no login yet, the window opens on the login and PROMPT is typed after it.                                                                                                               |
| `--agent NAME`            | `claude`, `codex`, `opencode`, `gemini` or `pi`, for `-p`. Default: the project's agent, set at creation from [`default_agent`](#config-toml). Without `-p`, NAME becomes the project's agent.                                                                      |
| `-d`, `--no-attach`       | Don't attach afterwards. With `-p`, print the window the agent is in (`Window: claude-2`, or `Tab:` on herdr). If Claude Code has no login on the machine yet, exit 1 without typing the prompt.                                                                                       |
| `-w`, `--window WINDOW`   | Type PROMPT and Enter into that agent's window instead of starting an agent: an answer to a permission prompt (`-p 1`) or a follow-up. WINDOW is a name or number as `repose ps` shows it, or on herdr an agent's name; one the session doesn't have exits 2. The attach opens on it. Not with `--worktree` or `--agent`. |
| `--worktree`              | Start the agent in its own git worktree. Needs `-p`.                                                                                                                                                                                                              |
| `--no-sync`               | Don't copy the checkout, even into a new machine. Your tool logins and git identity are still copied.                                                                                                                                                              |
| `--size small\|large\|xl` | Size of a new project, or of a stopped one this command starts. Default: [`default_size`](#config-toml). A machine of another size exits 2 and names `repose resize`, unless it's stopped; a stopping one is waited out first.                                                              |
| `--on PROJECT`            | Add this folder to PROJECT's machine as [another checkout](/docs/run-and-attach#several-repositories-on-one-machine), beside its own.                                                                                                                               |
| `--temp [DURATION]`       | A new [temporary machine](/docs/lifecycle#temporary-machines), destroyed with no snapshot after DURATION (`10m` to `24h`, or `1d`; default `24h`). `--temp 3h` works anywhere among the arguments.                                                                  |
| `--bridge`                | Also [bridge your Chrome](#repose-browser-bridge-project) to the machine while attached.                                                                                                                                                                           |
| `--bridge-allow HOST`     | Bridge, and let the agents use only this site in your Chrome: the `--allow` of `repose browser bridge`. Repeatable; `*.example.com` is `example.com` and its subdomains.                                                                                           |
| `--no-personal`           | Keep your [machine.nix](/docs/config#your-machine-nix) off this machine from now on: a new one is created without it, and one that has it switches without it in the background.                                                                                   |
| `--multiplexer NAME`      | `tmux` or `herdr`: what runs the machine's terminals, from its next start, and it stays with the project. Default: [`default_multiplexer`](#config-toml).    |

### `repose attach [PROJECT[:CHECKOUT]] [WINDOW]`

Attach to the project's session, on its current window, without syncing. WINDOW (or `-w`/`--window NAME`) opens on that window instead: a name or number as `repose ps` shows it, or on herdr an agent's name. If a project resolves, one word that names none of your projects is WINDOW. A window the session doesn't have exits 2.

A machine that isn't running comes up first, as with `run`: a stopped one starts, one in `error` restarts, one starting is waited for, and one stopping or being restored is waited out and then started. So is one whose server is restarting ([When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)). A machine being removed, or one stopped for running a miner, exits 5.

A herdr project attaches in [three ways](/docs/run-and-attach#herdr-instead-of-tmux). `--bridge` [bridges your Chrome](#repose-browser-bridge-project) to the machine for as long as you're attached; `--bridge-allow HOST` does that with an allowlist.

If the connection drops while you're attached, `run` and `attach` print `repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.` and attach again once the machine answers, for up to 2 minutes. Keys you type while it waits are dropped. With `REPOSE_NO_INPUT_PROXY=1`, the command ends with exit code 255 instead.

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

With WINDOW (or `-w`/`--window NAME`), it prints that window's last 20 lines instead, as whole lines rather than as the pane wrapped them; `-n`/`--tail N` for N lines, 1 to 10000. `-n` without a window prints every window's, each under a `==> 1:claude <==` line. On herdr, WINDOW is an agent's name. Where a project resolves, one word that names none of your projects is WINDOW: `repose ps claude-2` in the checkout.

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

Needs the machine running, and Chrome 144 or newer with remote debugging on at `chrome://inspect/#remote-debugging`. With that off, it opens that page and waits up to 5 minutes for you to turn it on.

| Flag                  | What it does                                                                                                                                                                  |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--allow HOST`        | Let the agents use only this site: other tabs are hidden from them and other sites fail. Repeatable, or comma-separated; `*.example.com` is `example.com` and its subdomains. |
| `--cdp URL`           | Bridge a browser started with a remote debugging port instead (`http://127.0.0.1:9222`): any Chromium, no switch, no dialogs.                                                 |
| `--user-data-dir DIR` | The profile directory of a Chrome that isn't Google Chrome's default one (Chromium, Brave, Edge, a second profile).                                                           |
| `--no-inspect`        | Don't open `chrome://inspect` when remote debugging is off; print what to do and wait. `--no-browser` is its old name.                                                        |

Only one bridge to a machine at a time. A laptop that goes to sleep keeps its bridge for up to 2 minutes; a new bridge takes over from it. With `--allow`, the bridge also closes if its own connection to Chrome ends.

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

Copy the image on your clipboard, up to 20 MB, to `/tmp/repose-paste/` on the machine and paste its path into the active pane on tmux, or herdr's focused pane. The path is pasted, not sent; you press Enter. Needs the machine running.

| Flag            | What it does                                                                          |
| --------------- | ------------------------------------------------------------------------------------- |
| `--window NAME` | Paste into this tmux window (name or number), or this herdr agent's pane, instead.    |
| `--print`       | Copy the image and print its path on the machine; paste nothing. Not with `--window`. |

### `repose scan [DIR]`

List what the next `repose run` would install on the machine, and why. Installs nothing. DIR defaults to this checkout's root. The list comes in two halves: your laptop's global tools, and the commands the project's scripts call that neither the machine's base nor the project's own dependencies provide. The project half ends with the version pins it found: Node, Ruby and Java with the version the machine gets. A [machine.nix](/docs/config#your-machine-nix) on your account takes over the laptop half, and a `repose.nix` at the checkout root the project half; a `skipped:` line says which half and why. Logged out, or when the server doesn't answer, it reads `~/.config/repose/machine.nix` and says the account wasn't checked. `--json` prints `laptop`, `project`, `node`, `ruby`, `java` and `skipped`.

## Projects

### `repose ls`

Every project in a table. `*` after a name marks the project a command run here with no `PROJECT` acts on ([Which project](#which-project)). AGENTS is the [agent's state](/docs/agents#agent-states) with the longest wait's age (`needs input 4h`); a `?` after it means the machine's last report is over two minutes old. With several agents AGENTS counts them by state, needs input first, and gives the longest wait: `2 agents: 1 needs input (4h), 1 working`. A line under the table names each running project nobody has used for a day ([Unused machines](/docs/lifecycle#unused-machines)). A `LEFT` column appears while a temporary machine is listed, and a `DISK` column while a listed project's disk is 90 percent full or more, as `93% full`. The last line is your plan and how much of it is in use: memory running, the disk your projects hold, and egress this month. A line under it says when your projects hold more than your plan's disk ([creating, restoring, forking and growing a disk](/docs/limits#projects) are refused until they hold less), when egress is past the plan's allowance, and when a payment failed. `--json` for full records, `--destroyed` for destroyed projects that can still be restored (with `--all`, every one). `-q`/`--quiet` prints only the names, one per line:

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

`checkout` has a line for the machine's checkout and one for each git worktree beside it: the branch, its commits your laptop doesn't have (`git fetch repose` gets them), and the files `git status` lists. Your laptop's commits are read from every checkout of the project on it, wherever you run the command; with none, the line gives the last commit's age instead. `disk` is the machine's filesystem, used over size, then the disk's size as `repose resize` names it; the filesystem keeps a few percent of the disk for itself. A stopped machine, or one that hasn't reported, shows the disk's size alone. Lines above the rows say when the machine has been unused for a day, when a temporary one is destroyed, what differs from a new project's settings (`setup: agent codex`, machine.nix kept off, base updates held), when the disk is 90 percent full or more (with the `repose resize` that doubles it, up to 320 GB), and the plan lines `repose ls` prints. `-v` adds the server's name and the machine's address.

| Flag                 | What it does                                                                                                                                            |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--json`             | The project's record, with the checkout's lines under `git`: `worktree`, `branch`, `commits_not_on_laptop`, `uncommitted_files`, `last_commit_at`. A running machine that did not answer has `git_error` (`ssh_timeout` or `ssh_failed`) in place of `git`. A stopped one has the lines this laptop read before stopping it, or `git_error` when that read failed, and `git_at`, when it read them. |
| `-f`, `--follow`     | Redraw every 5 seconds until Ctrl-C. Piped, it prints the status again only when it changed; with `--json`, as one line. A failed read doesn't end it. `--watch`, the old name, still works. |
| `--wait STATE`       | Wait until the machine is in STATE (`running`, `stopped`, `error`, ...), then print the status. Exits 1 if it lands in `error` or is destroyed instead. |
| `--timeout DURATION` | How long `--wait` waits before exiting 1 (default `10m`).                                                                                               |

### `repose start [PROJECT]`

Start a stopped machine, or restart one in `error`. Doesn't sync. One starting is waited for, and one stopping or being restored is waited out and then started. So is one whose server is restarting ([When a machine's server restarts](/docs/lifecycle#when-a-machines-server-restarts)). `--json` prints the project as JSON when done.

### `repose stop [PROJECT...]`

Stop the machine and snapshot its disk. `--no-snapshot` skips the snapshot. Several projects stop at once, one line each; `--unused` stops every machine `repose ls` shows as [unused](/docs/lifecycle#unused-machines). If an agent is `working` or `needs input`, it names the agent and asks first; `-y`/`--yes` skips the question (required without a terminal), and the stop line is then followed by `Ending claude (working).` It runs `git fetch repose` first in each folder on this laptop whose `repose` remote is a stopping machine, wherever you run it, and prints what came, as `Fetched 3 commits on repose/main.` (`... in ~/code/api.` for a folder other than the one you're in). The stop line ends with what stays on the machine: `Stopping todo-app; 3 commits on main not fetched, 4 files not committed.` Commits are counted when this laptop has a checkout of the project. When the machine can't be read within 15 seconds, the line says so and why: `Stopping todo-app; checkout unknown (no ssh answer in 15 s).`, or `(ssh failed: Connection refused)`. `repose status` on the stopped machine shows the same rows, or the same `unknown`, with when the stop read them. The command returns once the machine reads `stopping`, and the snapshot and the shutdown go on without it; a stop that fails puts the project in `error` and notifies you (`stop failed`). A machine already stopping prints `todo-app is already stopping.`; `--no-snapshot` can't change a stop already under way, and the line says so. `--wait` waits for the stop, prints `Stopped todo-app in 11s with a 2.1 GB snapshot.` and `Ended claude (working).`, and exits 1 when the stop failed. `--json` prints the project as JSON (`stopping`, or with `--wait` as the stop left it), or an array of them for several projects or `--unused`.

`repose stop [PROJECT] -w WINDOW` (`--window`) closes one agent's window, a name or number as `repose ps` shows it (on herdr an agent's name), and the machine keeps running. It asks first when that agent is working or waiting for an answer, as a stop does. If the window works in a `--worktree` folder, it fetches first as above, then removes the worktree if no other window works in it, `git status` lists nothing there and your laptop has the branch's last commit: `Closed claude-2 on todo-app and removed its worktree ~/todo-app-worktree-2.` The branch stays on the machine. Otherwise the worktree stays and the line says why: `Closed claude-2 on todo-app; its worktree ~/todo-app-worktree-2 stays: worktree-2 is not fetched to this laptop.` On herdr only the agent's pane closes.

### `repose rm [PROJECT...]`

Delete the machine and disk; a final snapshot is kept 30 days. Several projects get one question. `-y`/`--yes` skips the question (required without a terminal), and the line then says until when the snapshot is kept. `--wait` waits until it's done. A running machine's commits are fetched first, as `stop` fetches them, in each of the project's checkouts on your laptop. Run in the project's checkout, it removes the `repose` git remote; branches already fetched from it stay.

`repose rm PROJECT:CHECKOUT` removes only a checkout that `run --on` added: its folder and its worktrees on the running machine, after the same question, and the link from your laptop folder. It exits 6 while a process works in one of those folders, and 2 for a name the machine doesn't have.

`repose projects` and `repose destroy`, the old names, still work.

On a temporary machine the question starts by saying that no snapshot is kept, and it can't be restored. A busy agent is named in the question, as on `stop`.

### `repose keep [PROJECT] [DURATION]`

Make a [temporary machine](/docs/lifecycle#temporary-machines) a normal one: it is no longer destroyed when its time runs out. With DURATION (`10m` to `24h`, such as `3h`; `1d` is `24h`), it stays temporary and is destroyed that long from now instead: `tmp-k3f9 is temporary until Sep 29 17:02.` One argument that is a number and a unit (`s`, `m`, `h`, `d` or `w`) is DURATION, so a project called `2fa` is a project. It adds no git remote; reach it by name. On a project that isn't temporary it says so and does nothing.

### `repose restore [NAME]`

Bring back a project destroyed in the last 30 days. `--as NEW-NAME` for another name, `--snapshot ID` for an older snapshot. It ends with the project, the snapshot, the time it took, and its state and size, as `snapshots restore --as` does.

### `repose fork [PROJECT]`

Copy the project's disk as it is now into [new projects](/docs/lifecycle#fork-a-project), each on its own machine. `-n`/`--count N` makes N copies (1 to 10, default 1), named `PROJECT-fork-1`, `PROJECT-fork-2` and so on; `--name NAME` names them `NAME-1`, `NAME-2`. `--size` sets their size (default: the project's). `--snapshot ID` copies one of the project's snapshots instead. `--prompt TEXT` starts the agent in every copy with that prompt (`--agent` picks the agent). `--no-start` creates the copies stopped, so they take none of your plan's memory until `repose start`; it can't be combined with `--prompt`. `--json` prints the copies as JSON. The project itself keeps running. In a checkout whose `repose` remote is the project's machine, `fork` adds a git remote named after each copy, so `git fetch todo-app-fork-2` brings that copy's commits back; `repose rm` of the copy removes its remote. Each copy is a project: it counts toward the [100 an account can have](/docs/limits#projects), toward your plan's disk by what it holds (at first what the project holds), and toward the plan's memory while it runs. If the copies would take you past 100, or need more memory than your plan has beside what runs now, it exits before copying anything and nothing is created; the memory refusal names `--no-start` (exit code 7).

### `repose resize [PROJECT] [DISK]`

Grow the project's disk, for example `repose resize 80G`, or `repose resize todo-app 80G` for a project other than this checkout's. DISK needs a unit, `M`, `G` or `T` (`80G`, `80GB` and `80GiB` are the same); a bare number exits 2. Disks can't shrink: a smaller size exits 2 with the disk's size, and the size it already has does nothing. A larger disk adds nothing to your plan's disk total, which counts what your projects hold, and one disk can be at most the plan's whole disk. A single argument that reads as a size is the disk; anything else is the project.

`--size small|large|xl` [changes the project's size](/docs/machine#changing-the-size), for example `repose resize --size xl` (or `repose resize todo-app --size xl`) when it keeps running out of memory. Asking for the size the project already has does nothing. A stopped project starts at the new size next time. A running one has to be stopped for it: repose asks, then stops it, changes it and starts it again, which ends every process on it, agents included. A larger size your plan can't run beside what runs now is refused before the question, with exit code 7, and nothing is stopped. `-y`/`--yes` skips the question (required without a terminal). It prints what the new size gives and which plan it needs. `--json` prints the project as JSON when done.

### `repose logs [PROJECT]`

`--kind console|build|ops` (default `console`), `--since` (below), `-n`/`--tail N` for the last N lines, `-f`/`--follow` to follow, `--json` for one JSON object per line. Each line starts with its local time.

`console` is what the machine printed during a boot that failed: the last 200 lines of each such boot, for its last 20 operations. A machine that booted cleanly has none. `build` is the last configuration build's log, `ops` one line per operation: its kind, how it ended (`done`, `failed`, or `running` while it runs), how long it took, and for a failed one its error code and message:

```
2026-10-08T21:38:02-04:00 stop done 4.2s
```

`--since` on `logs` and `events` takes a duration back from now (`90m`, `2d`, `1w`, `1d12h`), a date (`2026-10-01`, from local midnight) or an RFC 3339 time (`2026-10-01T08:00:00Z`). Anything else exits 2. `logs` has no default; `console` and `build` hold only the last boots and build.

### `repose events [PROJECT]`

Every event in the window, oldest first, one per line: local time, agent (with its tmux window), what happened in the words its [notification](/docs/notifications) uses, summary. Outside a checkout, and with no PROJECT, it covers all your projects and adds a project column. `--since 3d` (default `24h`; the forms are under `logs`), `-f`/`--follow` to keep printing new ones as they come, `--json` for one JSON object per line, with the event's `kind` and `project`. An empty window prints `No events on todo-app in the last 24h.` on stderr.

```text
2026-10-08T20:58:30-04:00  -           snapshot  taken (stop)
2026-10-08T20:59:04-04:00  claude (1)  finished  Added tests
2026-10-08T21:12:40-04:00  claude (2)  asks      Drop old table?
```

### `repose questions [PROJECT]`

The [questions](/docs/notifications#agents-can-message-you-and-ask-questions) agents are waiting on you to answer, from all your projects (wherever you run it) or from PROJECT. Each shows the project, the agent's window (the name `attach -w` takes), how long ago it asked, when it expires, the question and its options. After them it lists agents waiting at a prompt in their terminal, such as a permission prompt, which `repose reply` can't answer, by window and project: for `claude-2 on todo-app`, `repose run todo-app -w claude-2 -p 1` types `1` and Enter there, and `repose attach todo-app -w claude-2` opens it. `-q`/`--quiet` prints only the question ids. `--json` prints the questions; the agents at a terminal prompt are in `repose ls --json`, as each project's `signals.agents` with `state` `needs_input`.

### `repose reply [PROJECT] [--] [ANSWER...]`

Answer a waiting question: `repose reply todo-app yes`. The first word is the project when it names one of your projects; otherwise every word is the answer, which works when only one question is waiting. A project with no waiting question exits 1 and sends nothing. For an answer that starts with a project's name, put `--` before it: `repose reply -- todo-app is fine`. With several waiting, it lists them and sends nothing; name the project or pass `--question ID` (the id `repose questions` shows). With no answer, it asks for one in the terminal. If the question has options, the answer must be one of them. `--json` prints the answered question.

`repose reply --dismiss [PROJECT]` cancels the waiting question without an answer, chosen the same way, and the agent's `repose-ask` exits 5. With no question waiting it exits 1, as `reply` does. One that closes while the dismiss is on its way, for example because you answered it in the agent's terminal, prints `Not dismissed: answered in the terminal.` and exits 1.

## Snapshots

| Command                                          | What it does |
| ------------------------------------------------ | ------------ |
| `repose snapshots list [PROJECT]`                | Alias `ls`, and `repose snapshots` alone. The table shows the last 8 characters of each id; `--json` for JSON, `-q`/`--quiet` for whole ids. |
| `repose snapshots create [PROJECT]`              | Take one now; prints its id. `--json` prints the Snapshot object. |
| `repose snapshots restore [PROJECT] SNAPSHOT_ID` | Replace a stopped project's disk and start it, after a question that says whether a snapshot keeps the disk as it is now. It ends with `Restored todo-app; it is running (large).` `--as NAME` restores into a new project instead, which starts too; `-y`/`--yes` skips the question. |

SNAPSHOT_ID is a whole id, or its last or first characters (4 or more) when only one snapshot has them. `--as-new` is the old name of `--as`.

## Secrets

| Command                         | What it does                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repose secrets set NAME`       | Asks for the value, or reads it from a pipe (`op read op://dev/stripe/key \| repose secrets set STRIPE_KEY`, one trailing newline dropped). `--from-file PATH` or `--from-env` instead. `NAME=VALUE` exits 2, since the value would stay in your shell history. `--global` sets it on your account, for [every project](/docs/secrets#secrets-for-every-project).                                                                                                                                                                                                                                      |
| `repose secrets import [FILE]`  | Set every `NAME=VALUE` in a `.env` file (default `./.env`, `-` for stdin). `--mcp` sets the secrets your carried MCP servers need from the tokens in your laptop's Claude Code config instead, for the project, and asks once before replacing secrets the project already has (`-y`/`--yes` replaces them without asking). `--global` sets them on your account, and with `--mcp` reads your user-scope servers alone. `--dry-run` lists the names and sends nothing.                                                                                                                     |
| `repose secrets list [PROJECT]` | Names and dates, never values. Alias `ls`, and `repose secrets` alone. `--json` for JSON. `--global` lists your account's.                                                                                                                                                                                                                                                                                                                                                                                                            |
| `repose secrets rm NAME`        | Delete it. Alias `remove`. `--global` deletes your account's.                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `repose secrets choose`         | Choose which of your laptop's [logins and files](/docs/secrets#choose-what-is-copied) `repose run` copies: `gh`, `codex`, `opencode`, `env` (gitignored `.env` files), `mcp` (your Claude Code MCP servers). In a terminal, a list to toggle (space toggles, Enter saves, `q` leaves); otherwise it prints the list. `--off NAME...` leaves them on your laptop and the next run removes the copies already on the machine, `--on NAME...` copies them again, `--reset` drops the list. |

`secrets set` and `secrets rm` name the project they changed; with `--global` they take no project, and `set` names the projects whose own secret of that name wins. `secrets rm` without `--global` says when your account's secret of that name reaches the project. `secrets list` on a terminal also shows your account's secrets, marking one the project's own replaces, and what your laptop copies at each run, which lands on the machine's disk and so in its snapshots and forks. Piped, it prints only `NAME`, a tab and the date, one secret per line. Without `--project`, `secrets choose` sets the list for every project; with `--project NAME` it sets that project's own list, which replaces the shared one for it, and `--reset` sends the project back to the shared list.

## Configuration

| Command                             | What it does                                                                                                                                                                                                                                            |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repose config add PACKAGE...`      | Add menu entries or nixpkgs packages, build and apply. With no names, list the menu's entries by group.                                                                                                                                                                                                  |
| `repose config remove PACKAGE...`   | Remove them again. Alias `rm`.                                                                                                                                                                                                                          |
| `repose config show [PROJECT]`      | Print the project's `repose.nix`. `repose config` alone does the same.                                                                                                                                                                                  |
| `repose config revisions [PROJECT]` | Its revisions, newest first, with the last 8 characters of each id, whether it built, and whether it carries your machine.nix. `--json` for JSON, `-q`/`--quiet` for whole ids.                                                                         |
| `repose config edit [PROJECT]`      | Edit in `$EDITOR`, apply on save.                                                                                                                                                                                                                       |
| `repose config apply [PATH]`        | Apply a file. Default `./repose.nix`; with neither, apply the current configuration again. `--revision ID` switches the running machine back to an earlier revision that built, by the id `revisions` shows, `Building revision` printed, or its start. |
| `repose config on [PROJECT]`        | Give the project's machine your machine.nix again.                                                                                                                                                                                                      |
| `repose config off [PROJECT]`       | Keep your machine.nix off the project's machine, as `repose run --no-personal` does. A running machine switches in the background; a stopped one starts with the change.                                                                                |

With `--global`, the same commands act on your [machine.nix](/docs/config#your-machine-nix) instead of the project's file: a home-manager module every machine of your account gets, kept at `~/.config/repose/machine.nix` and on your account. `repose config --global add PACKAGE...` and `remove` edit its `home.packages = with pkgs; [ ... ];` list and push it, `edit` opens the laptop's copy and pushes it on save, `show` prints the account's copy, `revisions` lists its saves, and `apply [PATH]` pushes a file (default `~/.config/repose/machine.nix`) over the account's copy; an empty file removes it. Each push rebuilds every machine that has not opted out: a running one switches in place, a stopped one at its next start. `--global` takes no project; `on` and `off` take one, with or without `--global`. `repose config show --revisions` is the old form of `revisions`. `repose run` pushes the file by itself when it changed since its last push, and refuses with one line when the account's copy changed since too.

The CLI's own settings are keys in [config.toml](#config-toml); `repose config set` exits 2 and says so.

## Account

| Command | What it does |
| ------- | ------------ |
| `repose login`                      | Log in with a code. The CLI prints a link with the code in it and opens it in this computer's browser when it has one; `--no-browser` only prints it. `--status` prints the account you're logged in as, its server and its plan, and exits 3 when you aren't. `--browser`, see [Other servers](#other-servers). There is no login without a browser for CI. |
| `repose logout`                     | Log out and revoke your SSH certificates; connections they opened, on any device, close within 30 seconds. If the revoke fails, it logs out on this laptop, says so and exits 1; the certificates stop working within 24 hours. `--purge` removes the CLI's files. |
| `repose notify`                                 | Where notifications go: `email: on, to you@example.com`, `ntfy: off` or its URL, and `gchat: off`, `gchat: on, space AAAA...` or `gchat: broken since` a date. The Google Chat webhook URL is never shown. `--json` for JSON. |
| `repose notify set`                             | `--email on\|off`, `--ntfy URL\|off` (`none` is the old `off`), `--gchat URL\|off` (a [Google Chat](/docs/notifications#google-chat) webhook; quote it). Prints the settings.                     |
| `repose notify test`                            | Send a test on every channel that's on, and print `sent`, `off` or `failed` for each, with the reason the server gave (`failed: the server answered 403`). A broken Google Chat webhook is tried too; if it gets through, it's on again. Six tests a minute. Exits 1 when a channel failed or none is on. |
| `repose version`                                | Print the version.                                                                                                                                                                                |
| `repose completion bash\|zsh\|fish\|powershell` | Print a shell completion script. `--help` and [Install](/docs/install#shell-completion) have the line that loads it for each shell.                                                                                                                                                                  |
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
| `REPOSE_NO_SPINNER=1`        | One line per step instead of a progress line. `TERM=dumb` does the same. Questions are still asked.                                                                                                                    |
| `REPOSE_NO_FASTPATH=1`       | Check with the server before every connection instead of reusing the last one. Slower; for when a connection keeps failing.                                                                 |
| `REPOSE_NO_BROWSER=1`        | Never open a browser, even with `repose login --browser` or for a link the machine opens while you're attached.                                                                                                                                   |
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

For a test or self-hosted repose server rather than the hosted one: `--api-url URL`, `REPOSE_API_URL` or `api_url` in config.toml pick the API, and `logto_issuer` and `logto_client_id` the login server and the CLI's application there. `repose login --browser` logs in through a browser on this computer instead of a code, for a login application that allows local redirects; the hosted one doesn't.

## Files on your laptop

| Path                | What it holds                                                                                                                                                                                                                                                                                                          |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `~/.config/repose/` | Your login (mode 0600; on macOS the token is in the keychain), `config.toml`, your `machine.nix` with `machine.nix.state` (what the last push left on both sides), and caches that are safe to delete.                                                                                                                 |
| `~/.ssh/repose/`    | The CLI's own SSH key and 24-hour certificate, `hosts` with one `Host` block per project, and `config`, which has `ssh` run `repose ssh-prepare` before connecting to a `.repose` host, so the certificate is renewed and a new project's block written first ([SSH and editors](/docs/ssh-and-editors#how-it-works)). |
| `~/.ssh/config`     | One added line: `Include ~/.ssh/repose/config`.                                                                                                                                                                                                                                                                        |
| `.git/config`       | In each project's checkout, the `repose` remote. Nothing is committed.                                                                                                                                                                                                                                                 |

`repose logout --purge` removes all of these but the `repose` remotes and `machine.nix`, which is yours; `git remote remove repose` removes one remote.

## Output

Tables print local time to the minute (`2026-10-08 21:37`). `logs` and `events` print local RFC 3339 times with the offset. `--json` prints times in UTC. Progress lines go to stderr; without a terminal, each step prints a line when it starts, a line every 30 seconds while it runs, and a `✓` line with its time when it ends. Scripts should read `--json` or `-q`: the text of tables and messages can change between releases.

## Exit codes

| Code | Meaning                                                                                                                                                                                     |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0    | Worked.                                                                                                                                                                                     |
| 1    | Failed, or you answered no to a question; the message says why. A network failure, the login server's included, exits 1 too. |
| 2    | Wrong usage.                                                                                                                                                                                |
| 3    | Not logged in, or the login expired.                                                                                                                                                        |
| 4    | No such project.                                                                                                                                                                            |
| 5    | The machine isn't running.                                                                                                                                                                  |
| 6    | The machine changed files the sync would write, or is in the middle of a merge or rebase; the sync stopped.                                                                                 |
| 7    | A plan or account limit refused it (memory, disk, egress, the project count), or a payment problem; the message names it.                                                                   |
| 8    | No capacity, before or during a start: running machines hold the memory, so stopping one frees room. Choosing a plan while every seat is taken answers with your place on the [waitlist](/docs/limits#when-repose-is-full) and this code too. |
| 10   | The configuration build failed.                                                                                                                                                             |
| 130  | Interrupted with `Ctrl-C`, at a question too.                                                                                                                                                                  |

Once `run`, `attach` or `ssh` has connected you, the exit code is `ssh`'s. Once `repose exec` has started the command, the exit code is the command's, whatever it is (a `4` from your test runner is the test runner's); the codes above come only from failures before it starts, which print a message first. `255` means `ssh` lost the connection and the command could not reconnect. `repose cp` returns `scp`'s. `repose paste` exits 1 when there is no image on the clipboard or no tool to read it, and says which tool to install.
