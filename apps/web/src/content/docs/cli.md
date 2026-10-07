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
Run `repose --help` for the commands.
```

## Which project

Commands that act on a project use, in order: the `PROJECT` argument, `--project NAME`, the `REPOSE_PROJECT` environment variable, then the current checkout's git remote.

Global flags: `--project NAME`, `-v`/`--verbose` (debug output to stderr), `--version`, and `--api-url URL` (see [Other servers](#other-servers)).

## Working on a project

### `repose run [PROMPT]`

Create or start this checkout's machine and attach. A new machine gets a copy of your checkout first; one that already has it is left as it is, and `run` says when your laptop has work to send with `repose sync`. Your tool logins and settings are copied every time, and a changed `repose.nix` at the repository's root is applied in the background ([repose.nix in your repository](/docs/config#repose-nix-in-your-repository)). With a prompt, start an agent and type the prompt into it. See [Run and attach](/docs/run-and-attach) and [Sync](/docs/sync).

On your laptop, `run` changes one thing in the checkout: it adds a git remote named `repose` for the machine's checkout, so `git fetch repose` brings the agent's commits back. See [Getting work back](/docs/sync#getting-work-back).

| Flag                      | What it does                                                                                                                                                                                           |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--agent NAME`            | `claude`, `codex`, `opencode`, `gemini` or `pi`.                                                                                                                                                       |
| `--no-attach`             | Don't attach afterwards.                                                                                                                                                                               |
| `--worktree`              | Start the agent in its own git worktree. Needs a prompt.                                                                                                                                               |
| `--no-sync`               | Don't copy the checkout, even into a new machine. Your tool logins and git identity are still copied.                                                                                                  |
| `--size small\|large\|xl` | Size of a new project.                                                                                                                                                                                 |
| `--name NAME`             | The project called NAME, created if there is none: a second machine for this checkout, or a name other than the directory's for one with no remote.                                                    |
| `--on PROJECT`            | Add this folder to PROJECT's machine as another checkout, beside its own. See [Several repositories on one machine](/docs/run-and-attach#several-repositories-on-one-machine).                         |
| `--temp [DURATION]`       | A new temporary machine, destroyed with no snapshot after DURATION (`10m` to `24h`, default `24h`). See [Temporary machines](/docs/lifecycle#temporary-machines).                                      |
| `--bridge`                | Also bridge your Chrome to the machine while attached, see [`repose browser bridge`](#repose-browser-bridge-project).                                                                                  |
| `--bridge-allow HOST`     | Bridge, and let the agents use only this site in your Chrome. Repeatable; `*.example.com` is `example.com` and its subdomains.                                                                         |
| `--no-personal`           | Keep your machine.nix off this machine from now on: a new one is created without it, and one that has it switches without it in the background. See [Your machine.nix](/docs/config#your-machine-nix). |
| `--multiplexer NAME`      | `tmux` or `herdr`: what runs the machine's terminals, from its next start. It stays with the project. Without it a new project takes `default_multiplexer`, else herdr when you run it from a herdr pane on your laptop, else tmux. A `--temp` machine always runs tmux, and `--temp --multiplexer herdr` stops with an error. See [herdr instead of tmux](/docs/run-and-attach#herdr-instead-of-tmux). |

### `repose attach [PROJECT]`

Attach to the project's tmux session without syncing; on a herdr project, see [herdr instead of tmux](/docs/run-and-attach#herdr-instead-of-tmux) for the three ways it attaches. In the project's checkout, it adds the `repose` git remote too if it's missing. In a folder `run --on` added to a machine, or with `PROJECT:CHECKOUT`, it opens that checkout's windows, see [Several repositories on one machine](/docs/run-and-attach#several-repositories-on-one-machine). `--bridge` also bridges your Chrome to the machine for as long as you're attached, and `--bridge-allow HOST` does that with an allowlist, see [`repose browser bridge`](#repose-browser-bridge-project).

`run` and `attach` print one line when another of your projects is running idle, once per idle stretch. An `attach` that reuses an open connection makes no api call and skips it.

While you're attached, a file you drop on the terminal, or an image you paste with `Cmd+V` or `Ctrl+V`, is copied to the machine and its path there is pasted. See [Drop a file or paste an image](/docs/run-and-attach#drop-a-file-or-paste-an-image).

If the connection drops while you're attached, `run` and `attach` print `repose: lost the connection to todo-app. Reconnecting; Ctrl-C stops.` and attach again once the machine answers, for up to 2 minutes. Keys you type while it waits are dropped. On Windows, or with `REPOSE_INPUT_PROXY=0`, the command ends with exit code 255 instead. See [Detach and come back](/docs/run-and-attach#detach-and-come-back).

### `repose sync [PROJECT]`

Copy this checkout's current work to its machine, over the checkout already there, and don't attach. It creates or starts the machine if needed. It stops with exit code 6 when the machine has uncommitted changes your laptop would write over. See [Sync](/docs/sync). `repose run --stash-remote` and `--discard-remote` moved here, and `run` exits 2 naming this command when given one.

| Flag                      | What it does                                                       |
| ------------------------- | ------------------------------------------------------------------ |
| `--stash-remote`          | Stash the machine's uncommitted changes before syncing.            |
| `--discard-remote`        | Discard the machine's uncommitted changes before syncing.          |
| `--size small\|large\|xl` | Size of a new project.                                             |
| `--name NAME`             | The project called NAME, created if there is none.                 |
| `--temp [DURATION]`       | A new temporary machine, destroyed after DURATION (default `24h`). |
| `--multiplexer NAME`      | `tmux` or `herdr`, from the machine's next start, as on `run`.     |

### `repose ps [PROJECT]`

The project's tmux windows: number and name, the program running in each (its name, not its arguments), and when it last printed something. `*` marks the current window, the one `attach` opens on. `-q`/`--quiet` prints only the names; `--json` for JSON. On a herdr project it lists herdr's agents instead: `WORKSPACE`, `AGENT`, `NAME` and `STATE`, with `*` on the focused one; `--json` gives `workspace`, `agent`, `name`, `state` and `focused`.

```
$ repose ps
WINDOW     COMMAND  ACTIVE
0:shell    bash     3h ago
1:claude*  claude   now
2:codex    codex    12m ago
```

### `repose exec [PROJECT] [--] COMMAND [ARG...]`

Run one command in the checkout on the machine, with the environment an agent there has: your secrets and the project's dev shell. Output streams back and the exit code is the command's. The command is passed word for word, its own flags included, and no shell reads it; for a pipeline, run a shell yourself, as in the last example. If the first word names one of your projects, it is PROJECT. To run a command that has a project's name, put `--` before it. Put `-i` and `-t` before the command.

```
$ repose exec npm test
$ repose exec todo-app git log --oneline -3
$ repose exec -it psql
$ repose exec sh -c "npm run build && npm test"
```

`-i`/`--interactive` passes your input to the command; without it the command reads nothing. `-t`/`--tty` gives it a terminal. Pass both, as with `docker exec`, for anything interactive.

### `repose ssh [PROJECT]`

Open a shell on the machine in the checkout, outside tmux; `exit` ends it.

### `repose code [PROJECT]`

Open the project's checkout, `/home/dev/<folder>` ([Where the checkout is](/docs/sync#where-the-checkout-is)), in an editor on your laptop, over SSH to `<project>.repose`. It uses VS Code (`code`) if it's installed, else Cursor (`cursor`), else Zed (`zed`); on a Mac it also looks in `/Applications` and `~/Applications`. The machine must be running.

```
$ repose code todo-app
Opening todo-app.repose:/home/dev/todo-app in VS Code
```

| Flag              | What it does                                                               |
| ----------------- | -------------------------------------------------------------------------- |
| `--editor EDITOR` | `code`, `cursor` or `zed`. Default: `REPOSE_EDITOR`, else the first found. |

Other editors: see [SSH and editors](/docs/ssh-and-editors).

### `repose open PORT`

Forward one port to your laptop and open it in the browser, until `Ctrl-C`. Works for servers on `127.0.0.1`, `0.0.0.0` or `::1`. If the connection drops, `open` reconnects on the same laptop port, for up to 2 minutes.

| Flag             | What it does                                                        |
| ---------------- | ------------------------------------------------------------------- |
| `--local-port N` | Port on the laptop. Default: the same, or a free one if it's taken. |
| `--no-browser`   | Print the URL only.                                                 |

### `repose browser [PROJECT]`

Watch the agent's browser on the machine and take it over: starts the machine's desktop view if needed, forwards it to laptop port 6080 (or a free one) in the background, and opens the link in your browser. The password is in the link after `#`. The view is the size of your tab and sleeps after 30 idle minutes; opening the page again wakes it. [Browser](/docs/machine#browser) has the details.

| Flag        | What it does                                       |
| ----------- | -------------------------------------------------- |
| `--no-open` | Print the link only.                               |
| `--stop`    | Stop the view on the machine and the forward here. |

`repose open --desktop`, with `--stop` and `--no-browser`, is the old name and still works.

### `repose browser bridge [PROJECT]`

Let the agents on the machine browse in your laptop's Chrome, with your logins and extensions, until `Ctrl-C`. The machine's browser tools (`playwright` and `chrome-devtools`) reach your Chrome through the SSH connection; nothing on the machine changes, and the next call an agent makes lands in your Chrome. Each page they load is listed in the terminal, by site and path. See [Lend the agents your Chrome](/docs/your-chrome).

```
$ repose browser bridge
Chrome 144 → todo-app: the agents there browse in your Chrome now,
with your logins. Ctrl-C hands them back the machine's browser.
Chrome asks you to allow each new connection.
Pages the agents open are listed below (host and path only).
An agent on todo-app is in your Chrome.
14:03:21  admin.internal.example/signups
```

Needs the machine running (it doesn't start it) and Chrome 144 or newer with remote debugging turned on at `chrome://inspect/#remote-debugging`. When it's off, the command opens that page and waits up to 5 minutes for you to turn it on.

| Flag                  | What it does                                                                                                                                                                  |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--allow HOST`        | Let the agents use only this site: other tabs are hidden from them and other sites fail. Repeatable, or comma-separated; `*.example.com` is `example.com` and its subdomains. |
| `--cdp URL`           | Bridge a browser started with a remote debugging port instead (`http://127.0.0.1:9222`): any Chromium, no switch, no dialogs.                                                 |
| `--user-data-dir DIR` | The profile directory of a Chrome that isn't Google Chrome's default one (Chromium, Brave, Edge, a second profile).                                                           |
| `--no-browser`        | Don't open `chrome://inspect` when remote debugging is off; print what to do and wait.                                                                                        |

Only one bridge to a machine at a time. A laptop that goes to sleep keeps its bridge for up to two minutes; a new bridge takes over from it. With `--allow`, the bridge also closes if its own connection to Chrome ends.

### `repose cp [-r] SRC... DST`

Copy files with `scp`. One side is `PROJECT:PATH`, or `:PATH` for this checkout's project. Relative machine paths start at the checkout. `-r`/`--recursive` copies directories. With several sources, all on the same side, the files go into the directory `DST`, so a glob works: `repose cp ./Fwd_* todo-app:/tmp/`.

### `repose paste [PROJECT]`

Copy the image on your clipboard to `/tmp/repose-paste/` on the machine and paste its path into the tmux session's current pane, or herdr's focused pane, where Claude Code attaches it. `--window NAME` picks a tmux window, or a herdr agent by name. Nothing is sent with it; you press Enter. While you're attached, `Ctrl+V` does the same; `repose paste` is for scripts and other windows. See [Drop a file or paste an image](/docs/run-and-attach#drop-a-file-or-paste-an-image).

| Flag            | What it does                                                     |
| --------------- | ---------------------------------------------------------------- |
| `--window NAME` | Paste into this tmux window (name or number) instead.            |
| `--print`       | Copy the image and print its path on the machine; paste nothing. |

### `repose scan [DIR]`

List the tools the next `repose run` would install on the machine, and why, and the Node, Ruby and Java versions the project pins with the version the machine gets (the closest nixpkgs has when it lacks the pinned one). Installs nothing. `--json` for JSON. A `.nix` file takes precedence: with a machine.nix on your account your laptop's tools are skipped, and with a `repose.nix` at the checkout root the commands the project's scripts run are skipped; the list says which half it skipped and why (`skipped` in the JSON). Logged out, it goes by `~/.config/repose/machine.nix` and says so.

## Projects

### `repose ls`

Every project in a table, with a line under it for each running project nobody has used for a day ([Idle machines](/docs/lifecycle#idle-machines)) and for each temporary one, saying when it is destroyed. `--json` for full records, `--destroyed` for destroyed projects that can still be restored (with `--all`, every one). `-q`/`--quiet` prints only the names, one per line:

```
repose ls -q | xargs -n1 repose stop
```

### `repose status [PROJECT]`

One project in detail, including processes listening on ports, the idle line when it has had nobody on it for a day, and when a temporary one is destroyed. `--json`, `--watch` (every 5 seconds).

`disk` is the machine's filesystem, used over size, read from the machine while it runs; otherwise it is the disk's size. At 90 percent full or more, a line under it says so and names the `repose resize` that doubles the disk, up to 320 GB.

### `repose start [PROJECT]`

Start a stopped machine, or restart one in `error`. Doesn't sync.

### `repose stop [PROJECT]`

Stop the machine and snapshot its disk. `--no-snapshot` skips the snapshot.

### `repose rm [PROJECT]`

Delete the machine and disk; a final snapshot is kept 30 days. `-y`/`--yes` skips the question (required without a terminal). `--wait` waits until it's done. Run in the project's checkout, it removes the `repose` git remote; branches already fetched from it stay.

`repose projects` and `repose destroy`, the old names, still work.

On a temporary machine the question says that no snapshot is kept, and it can't be restored.

### `repose keep [PROJECT]`

Make a temporary machine a normal one: it is no longer destroyed when its time runs out. It keeps no git remote; reach it by name as before. On a project that isn't temporary it says so and does nothing. See [Temporary machines](/docs/lifecycle#temporary-machines).

### `repose restore [NAME]`

Bring back a project destroyed in the last 30 days. `--as NEW-NAME` for another name, `--snapshot ID` for an older snapshot.

### `repose fork [PROJECT]`

Snapshot the project now and start copies of it as new projects, each on its own machine. `-n`/`--count N` makes N copies (1 to 10, default 1), named `PROJECT-fork-1`, `PROJECT-fork-2` and so on; `--name NAME` names them `NAME-1`, `NAME-2`. `--size` sets their size (default: the project's). `--snapshot ID` copies one of the project's snapshots instead of taking a new one. `--prompt TEXT` starts the agent in every copy with that prompt (`--agent` picks the agent). `--json` prints the copies as JSON. The project itself keeps running. Each copy counts toward your project limit and is billed like any project. If the copies would take you past the limit, nothing is created. See [Fork a project](/docs/lifecycle#fork-a-project).

### `repose resize [PROJECT] [DISK]`

Grow the project's disk, for example `repose resize 80G`, or `repose resize todo-app 80G` for a project other than this checkout's. Disks can't shrink, and the larger disk is billed from then on. A single argument that reads as a size is the disk; anything else is the project.

`--size small|large|xl` changes the project's size, for example `repose resize --size xl` (or `repose resize todo-app --size xl`) when it keeps running out of memory. A stopped project starts at the new size next time. A running one has to be stopped for it: repose asks, then stops it (taking a snapshot), changes it and starts it again, which ends every process on it, agents included. `-y`/`--yes` skips the question (required without a terminal). It prints what the new size gives and costs; the new rate applies from the next start. See [Changing the size](/docs/machine#changing-the-size).

### `repose logs [PROJECT]`

`--kind console|build|ops` (default `console`), `--since 1h` (a duration, or a time such as `2026-09-28T10:00:00Z`), `-f`/`--follow` to follow, `--json`. Each line starts with its time.

### `repose events [PROJECT]`

Every event in the window, oldest first, one per line: time, agent, kind, summary. `--since 72h` (default `24h`), `-f`/`--follow` to keep printing new ones as they come, `--json`.

### `repose questions [PROJECT]`

The questions agents are waiting on you to answer, from all your projects (wherever you run it) or from PROJECT. Each shows the project, the agent, how long ago it asked, when it expires, the question and its options. After them it lists agents waiting at a prompt in their terminal, such as a permission prompt, which `repose reply` can't answer; `repose attach` takes you there. `--json` prints only the questions. See [Notifications](/docs/notifications#agents-can-message-you-and-ask-questions).

### `repose reply [PROJECT] [ANSWER...]`

Answer a waiting question: `repose reply todo-app yes`. The first word is the project when it names one with a waiting question; otherwise every word is the answer, which works when only one question is waiting. With several waiting, it lists them and sends nothing; name the project or pass `--question ID` (the id `repose questions` shows). With no answer, it asks for one in the terminal. When the question has options, the answer must be one of them. `--json` prints the answered question.

## Snapshots

| Command                                          | What it does                                                                                                       |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| `repose snapshots list [PROJECT]`                | Alias `ls`. `--json` for JSON, `-q`/`--quiet` for the ids only.                                                    |
| `repose snapshots create [PROJECT]`              | Take one now.                                                                                                      |
| `repose snapshots restore [PROJECT] SNAPSHOT_ID` | Replace a stopped project's disk. `--as-new NAME` restores into a new project instead; `--yes` skips the question. |

## Secrets

| Command                         | What it does                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| ------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repose secrets set NAME`       | Asks for the value. `--from-file PATH` or `--from-env` instead.                                                                                                                                                                                                                                                                                                                                                                                                |
| `repose secrets import [FILE]`  | Set every `NAME=VALUE` in a `.env` file (default `./.env`, `-` for stdin). `--dry-run` lists the names and sends nothing.                                                                                                                                                                                                                                                                                                                                      |
| `repose secrets list [PROJECT]` | Names and dates, never values. Alias `ls`.                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `repose secrets rm NAME`        | Delete it.                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `repose secrets choose`         | Choose which of your laptop's logins and files `repose run` copies: `gh`, `codex`, `opencode`, `env` (gitignored `.env` files). In a terminal, a list to toggle (space toggles, Enter saves, `q` leaves); otherwise it prints the list. `--off NAME...` leaves them on your laptop and the next run removes the copies already on the machine, `--on NAME...` copies them again, `--reset` drops the list. See [Secrets](/docs/secrets#choose-what-is-copied). |

`secrets list` on a terminal also shows what your laptop copies at each run. Piped, it prints only `NAME`, a tab and the date, one secret per line. Without `--project`, `secrets choose` sets the list for every project; with `--project NAME` it sets that project's own list, which replaces the shared one for it, and `--reset` sends the project back to the shared list.

## Configuration

| Command                           | What it does                                                                               |
| --------------------------------- | ------------------------------------------------------------------------------------------ |
| `repose config add PACKAGE...`    | Add menu entries or nixpkgs packages, build and apply.                                     |
| `repose config remove PACKAGE...` | Remove them again. Alias `rm`.                                                             |
| `repose config show [PROJECT]`    | Print the Nix file. `--revisions` lists revisions.                                         |
| `repose config edit [PROJECT]`    | Edit in `$EDITOR`, apply on save.                                                          |
| `repose config apply [PATH]`      | Apply a file. Default `./repose.nix`; with neither, apply the current configuration again. |

With `--global`, the same commands act on your machine.nix instead of the project's file: a home-manager module every machine of your account gets, kept at `~/.config/repose/machine.nix` and on your account. `repose config --global add PACKAGE...` and `remove` edit its `home.packages = with pkgs; [ ... ];` list and push it, `edit` opens the laptop's copy and pushes it on save, `show` prints the account's copy (`--revisions` lists its saves), and `apply [PATH]` pushes a file (default `~/.config/repose/machine.nix`) over the account's copy; an empty file removes it. Each push rebuilds every machine that has not opted out: a running one switches in place, a stopped one at its next start. `--global` takes no project. `repose run` pushes the file by itself when it changed since its last push, and refuses with one line when the account's copy changed since too. See [Your machine.nix](/docs/config#your-machine-nix).

## Account

| Command                             | What it does                                                                                                                             |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `repose login`                      | Log in with a code in any browser. `--no-browser` is the same; `--browser`, see [Other servers](#other-servers).                         |
| `repose logout`                     | Log out and revoke SSH certificates; connections they opened, on any device, close within 30 seconds. `--purge` removes the CLI's files. |
| `repose notify set`                 | `--email on\|off`, `--ntfy URL\|none`.                                                                                                   |
| `repose notify test`                | Send a test on every channel that's on.                                                                                                  |
| `repose version`                    | Print the version.                                                                                                                       |
| `repose completion bash\|zsh\|fish` | Print a shell completion script.                                                                                                         |
| `repose help [COMMAND]`             | Print help for a command.                                                                                                                |
| `repose mcp forward`                | Reserved, not available yet. Prints what works today.                                                                                    |

## config.toml

`~/.config/repose/config.toml` is optional.

```toml
default_class = "small"
default_agent = "codex"
default_multiplexer = "herdr"

[sync]
exclude = ["dist", "*.mp4"]

[logins]
skip = ["gh"]

[projects.todo-app.logins]
skip = ["gh", "env"]
```

| Key               | Default  | What it does                                                                                                                            |
| ----------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `default_class`   | `large`  | Size of new projects.                                                                                                                   |
| `default_agent`   | `claude` | Agent for new projects.                                                                                                                 |
| `default_multiplexer` | none | `tmux` or `herdr` for new projects; a `--temp` machine runs tmux either way. Without it, a project you create from a herdr pane gets herdr and any other gets tmux. Any other value stops every command with an error naming the key. |
| `sync.exclude`    | none     | More gitignore-style patterns the sync leaves out.                                                                                      |
| `logins.skip`     | none     | Logins `repose run` leaves on your laptop: `gh`, `codex`, `opencode`, `env`. `repose secrets choose` sets it.                           |
| `projects`        | none     | Per-project tables. `[projects.NAME.logins]` with `skip` replaces `logins.skip` for that project; `skip = []` copies everything for it. |
| `api_url`         | hosted   | See [Other servers](#other-servers).                                                                                                    |
| `logto_issuer`    | hosted   | The login server. See [Other servers](#other-servers).                                                                                  |
| `logto_client_id` | hosted   | The CLI's application id there. See [Other servers](#other-servers).                                                                    |

## Environment variables

| Variable                  | What it sets                                                                                                                                     |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `REPOSE_PROJECT`          | The project to act on, like `--project`.                                                                                                         |
| `REPOSE_NO_FORWARD=1`     | Don't forward ports automatically while attached.                                                                                                |
| `REPOSE_TIMING=1`         | Print how long each step of `run` and `attach` took.                                                                                             |
| `REPOSE_NO_SPINNER=1`     | One line per step instead of a progress line. `TERM=dumb` does the same.                                                                         |
| `REPOSE_NO_FASTPATH=1`    | Check with the server before every connection instead of reusing the last one. Slower; for when a connection keeps failing.                      |
| `REPOSE_NO_BROWSER=1`     | Never open a browser, even with `repose login --browser`.                                                                                        |
| `REPOSE_INPUT_PROXY=0`    | Don't copy dropped files or pasted images to the machine; `run` and `attach` hand the terminal straight to `ssh`.                                |
| `REPOSE_CLIPBOARD_PATH=0` | On macOS, leave the clipboard alone while you are attached, so `Cmd+V` with only an image on it pastes nothing; `Ctrl+V` still pastes the image. |
| `REPOSE_API_URL`          | Like `--api-url`.                                                                                                                                |
| `REPOSE=1`                | Set on every repose machine, so scripts can tell where they run.                                                                                 |
| `XDG_CONFIG_HOME`         | If set, the CLI's files are in `$XDG_CONFIG_HOME/repose/`.                                                                                       |
| `CLAUDE_CONFIG_DIR`       | Where your laptop's Claude Code setup is copied from, instead of `~/.claude`.                                                                    |
| `VISUAL`, `EDITOR`        | The editor for `repose config edit`. Default `vi`.                                                                                               |
| `WAYLAND_DISPLAY`         | On Linux, `repose paste` reads the Wayland clipboard with `wl-paste` when this is set.                                                           |
| `DISPLAY`                 | Otherwise it reads the X11 clipboard with `xclip`.                                                                                               |
| `REPOSE_EDITOR`           | The editor `repose code` opens: `code`, `cursor` or `zed`.                                                                                       |

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

## Exit codes

| Code | Meaning                                                                                                                                                                                     |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0    | Worked.                                                                                                                                                                                     |
| 1    | Failed; the message says why.                                                                                                                                                               |
| 2    | Wrong usage.                                                                                                                                                                                |
| 3    | Not logged in.                                                                                                                                                                              |
| 4    | No such project.                                                                                                                                                                            |
| 5    | The machine isn't running.                                                                                                                                                                  |
| 6    | The machine has uncommitted changes; the sync stopped.                                                                                                                                      |
| 7    | Account or payment problem.                                                                                                                                                                 |
| 8    | No capacity right now; try again in a few minutes. Choosing a plan while every seat is taken answers with your place on the [waitlist](/docs/limits#when-repose-is-full) and this code too. |
| 10   | The configuration build failed.                                                                                                                                                             |
| 130  | Interrupted with `Ctrl-C`.                                                                                                                                                                  |

Once `run`, `attach` or `ssh` has connected you, the exit code is `ssh`'s. Once `repose exec` has started the command, the exit code is the command's, whatever it is (a `4` from your test runner is the test runner's); the codes above come only from failures before it starts, which print a message first. `255` means `ssh` lost the connection and `run`, `attach` or `open` could not reconnect within 2 minutes. `repose cp` returns `scp`'s. `repose paste` exits 1 when there is no image on the clipboard or no tool to read it, and says which tool to install.
