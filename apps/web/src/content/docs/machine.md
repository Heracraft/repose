---
title: The machine
description: What's installed, installing more, your laptop's tools, ports, the browser, memory and disk.
section: Using repose
order: 12
---

Each project gets its own virtual machine running NixOS, with its own kernel, disk, memory and Docker. Its hostname is the project's name as `ssh` uses it: `todo-app` for `todo-app.repose`. A machine running since before hostnames came in keeps `repose-guest` until its next start. You log in as `dev`, which has passwordless `sudo`. [Your checkout](/docs/sync#where-the-checkout-is) is `/home/dev/<folder>`, and everything under `/home/dev` survives a stop and is in snapshots; `/tmp` starts empty at each boot.

| Size    | Cores | Memory | Disk  |
| ------- | ----- | ------ | ----- |
| `small` | 1     | 4 GB   | 20 GB |
| `large` | 2     | 8 GB   | 40 GB |
| `xl`    | 4     | 16 GB  | 80 GB |

A core is a physical core, not a hyperthread. `nproc` says the same
number, and a machine running since before 18 October 2026 keeps the
count it started with until its next stop.

## What's installed

- **Agents:** [Claude Code, Codex CLI, opencode, Gemini CLI and pi](/docs/agents).
- **Languages:** Node.js 24 with npm, pnpm and yarn (through corepack), Python 3.12 with uv, Go, and rustup, which comes with no toolchain: run `rustup default stable` once.
- **Build tools:** gcc, g++, make, cmake and pkg-config, so cgo, node-gyp, Python extensions and Rust crates like `openssl-sys` build. pkg-config finds OpenSSL, zlib, SQLite, libffi, libyaml, libpq, libxml2, libxslt and the MySQL client, with `pg_config` and `mysql_config` on `PATH`, so native gems like `pg`, `mysql2` and `nokogiri` build too.
- **Containers:** Docker with `docker compose`; neither needs `sudo`. A base update leaves Docker and its containers running, and the new Docker runs from the machine's next start.
- **Browser:** Chromium, with fonts for Chinese, Japanese and Korean text and colour emoji. The [agents drive it](#browser).
- **Everyday tools:** git with git-lfs, gh, tmux, just, curl, wget, jq, ripgrep, fd, bat, fzf, eza, zoxide, tree, htop, neovim (also as `vi` and `vim`), direnv, sqlite3, `psql`, `pg_dump` and `pg_restore` (no database server; [add one](/docs/config#the-menu)), openssl, gnupg, dig, lsof, killall, file, zip, unzip and zstd.

The shell is bash with the starship prompt. `man` has the pages of the installed tools. `ls` is GNU ls; `ll`, `la` and `lt` run eza. Ctrl-R searches history with fzf, and history keeps 100,000 lines.

Your `~/.bashrc` is read in tmux windows, SSH shells and your editor's terminal, after the machine's own settings, so what it sets wins. Write your own `~/.bash_profile`, `~/.bash_login` or a `~/.profile` that mentions `.bashrc`, and the machine stops reading `~/.bashrc` for you: your login file decides.

`host.docker.internal` isn't defined. To reach a server on the machine from a container, add `extra_hosts: ["host.docker.internal:host-gateway"]` (or `--add-host host.docker.internal:host-gateway`), and have that server listen on `0.0.0.0`; one listening only on localhost refuses the connection.

gpg asks for a passphrase in the terminal; without a terminal, pass `--batch --pinentry-mode loopback --passphrase-fd 0`.

Programs built on other Linux systems run as they would on Ubuntu: Prisma's engines, Python wheels, esbuild and binaries from `curl | sh` installers. Scripts that start with `#!/bin/bash` or `#!/usr/bin/python3`, and Makefiles with `SHELL := /bin/bash`, run unchanged.

Python packages go in a virtual environment (`uv venv`, or `python3 -m venv .venv` and then pip); there is no system-wide pip, and Python command-line tools install with `uv tool install`. The system `python3` has no Tk, so tkinter and matplotlib's TkAgg need a Python from uv (`uv python install 3.12`).

## Installing more

Installs stay on the machine's disk and on your `PATH`:

```
npm i -g tsx
go install github.com/air-verse/air@latest
cargo install ripgrep-all
uv tool install httpie
nix profile add nixpkgs#ffmpeg
```

bun, deno, gem and composer installs are on `PATH` too, as are the usual directories of yarn, dotnet, ghcup, cabal, opam, luarocks, mix, nimble, juliaup, krew and volta. `nix profile add` takes any package from nixpkgs.

Type a command the machine doesn't have and it names the package that has it, with both ways to add it:

```
$ air
air: command not found
  nix profile add nixpkgs#air  install it on this machine
  repose config add air        keep it on every rebuild
                               (run this on your laptop)
Other packages with air: air-formatter
```

The last line appears only when other packages have that command, up to three of them. Typing `repose` gets its own answer: the CLI runs on your laptop, and this machine's commands are `repose-ask`, `repose-notify` and `repose-checkout`.

These installs aren't part of the project's configuration. For a package on every rebuild, use [`repose config add`](/docs/config#the-menu).

## Your laptop's tools come along

`repose run` installs the tools you have globally on your laptop (npm, pnpm, bun, `go install`, `cargo install`, uv and pipx) and the commands your project's scripts call (`package.json`, `Makefile`, `justfile`, `Procfile`, `.air.toml` and compose files). What travels is names, managers and versions: never a path off your laptop's disk, never a value out of a config file. The machine installs what it lacks in the background:

```text
Installing 2 of your tools in the background: air, portless
```

A tool comes from nixpkgs when nixpkgs has a package with that command, so its version can differ from your laptop's; otherwise you get your laptop's version, from npm, crates.io, PyPI or its Go module path. A Node major pinned in `.nvmrc`, `.node-version`, `.tool-versions`, `volta.node` or `engines.node` (the first found) becomes the `node` of new shells. A crate carried from your laptop gets rustup a stable default on the minimal profile first, if it has none.

Ruby and Java pins work the same way, from `.tool-versions`, `.ruby-version` or the Gemfile's `ruby` line, and from `.tool-versions`, `.java-version` or `.sdkmanrc`; the first found of each wins. nixpkgs keeps one Ruby per minor version (3.3, 3.4 and 4.0 today) and one JDK per major (8, 11, 17, 21 and 25), so a pin gets its own series, the closest newer one, or the newest when the pin is past them all. A Ruby 3.2.2 pin gets Ruby 3.3; `repose scan` says which before you run. JRuby and TruffleRuby pins are ignored. A Go, Rust or Python version pinned in `go.mod`, `rust-toolchain.toml` or `.python-version` installs nothing: `go` and `uv` fetch their own, and `rustup` installs one at the first `cargo`.

Gems install into `~/.local/share/gem`. A Ruby 3.4 pin gets Bundler 2.7 there too, because the Bundler 2.6 nixpkgs ships with Ruby 3.4 prints a screen of `already initialized constant` warnings on every `bundle`. A `Gemfile.lock` that says `BUNDLED WITH` 2.6 keeps running 2.6 until you run `bundle update --bundler`.

A `.nix` file takes precedence over this guesswork. With a [machine.nix](/docs/config#your-machine-nix) on your account, `run` leaves your laptop's global tools out. With a `repose.nix` at the checkout root, it leaves out the commands the project's scripts call. The Node, Ruby and Java pins apply in every case.

To see the list without installing anything, and which half a `.nix` file took over:

```
repose scan
```

## Projects with a flake.nix

Agents start in the checkout's dev environment, and so does every command they run. That holds for every window, `--worktree` ones and an agent you type in a shell included, and for `repose exec`:

- With an `.envrc`, agents get what it sets. repose runs `direnv allow` on it the first time an agent starts in that checkout, and again after you edit the file. It doesn't ask, because the agent is about to run this checkout's code anyway; the window says `repose: allowed /home/dev/todo-app/.envrc (direnv allow) so claude starts in its environment`. One you ran `direnv deny` on stays out, and no flake is loaded in its place. An `.envrc` of your own that says `use flake` keeps a `.direnv` directory in the checkout, so add `.direnv/` to `.gitignore`.
- With a `flake.nix` whose text names a dev shell and no `.envrc`, agents get `devShells.x86_64-linux.default`, from the nearest such flake at or above the folder and below your home folder. Nothing is written to your checkout: when the repository has no `flake.lock`, the one Nix writes is kept outside it.

The first load builds the dev shell, and the window says `repose: loading the dev shell from /home/dev/todo-app/flake.nix (the first load can take minutes)`. Anything nixpkgs has to compile takes longer still. `repose run -p` doesn't wait for it: your prompt is on the agent's command line, so the agent takes it when the load finishes. On a herdr project `run` waits for the agent, up to 30 minutes, and shows `Loading the project's dev shell`. Later loads come from the cache in under a second, until `flake.nix` or `flake.lock` changes.

A load that fails prints Nix's error and then `repose: the dev shell from /home/dev/todo-app/flake.nix did not load (the error is above); starting claude without it`. A flake that stopped evaluating ends `starting claude with the last one that did, if any`: nix-direnv keeps the last dev shell it built and doesn't say whether there was one, so the line can't either. `repose exec` prints none of this unless the load takes more than 2 seconds or fails.

A flake input that names `nixpkgs` without a URL (`outputs = { self, nixpkgs }`, or `inputs.nixpkgs.url = "nixpkgs"`) locks offline to the machine's own nixpkgs revision, as a `github:` URL that works on your laptop too.

A flake's own binary caches (`nixConfig.extra-substituters`) apply with `nix develop --accept-flake-config`, or with `--option extra-substituters URL --option extra-trusted-public-keys KEY`; an agent has no terminal for Nix's prompt, so one of those is the only way it gets them. `dev` is a trusted nix user, so `nix run nixpkgs#cachix -- use NAME` works too.

Only the dev shell for `x86_64-linux` is used. `nixosConfigurations`, `nixosModules`, `darwinConfigurations`, `homeConfigurations` and `packages` in the same flake change nothing on the machine; what configures it is [repose config](/docs/config).

Three things to get right in a first flake:

- **Commit `flake.nix`.** Nix only sees files git tracks. An untracked one still reaches the machine, then fails to load with `Path 'flake.nix' in the repository ... is not tracked by Git`.
- **Commit `flake.lock`.** Without one, each machine locks the flake's inputs on its own first load, so two machines can get different versions. Run `nix flake lock` on your laptop and commit the file; with no Nix there, have the agent commit it and bring it back with `git fetch repose`. An `.envrc` of your own that says `use flake` writes the lock into the checkout instead, and once your laptop and the machine have each written one, [the sync stops on that file](/docs/sync#when-the-machine-has-changes-of-its-own) until it's committed.
- **Define the dev shell for `x86_64-linux`.** The machine is x86-64 Linux whatever your laptop is. A flake written on a Mac with only `devShells.aarch64-darwin` fails with `does not provide attribute 'devShells.x86_64-linux.default'`. Name both systems, or use `flake-utils.lib.eachDefaultSystem`.

Your own shells get the same dev shell. In `repose ssh`, `ssh todo-app.repose`, an editor's terminal or a tmux window you open, bash loads it when you `cd` into the checkout and unloads it when you leave:

- With a `flake.nix` and no `.envrc`, a later load comes from the agents' cache in under a second. A cold one holds the prompt until it is built, and your shell's loading line ends `Ctrl-C skips it until you leave the folder`. Press that and the shell goes without, until you leave the folder and come back or `flake.nix` changes.
- With an `.envrc`, it loads once the file is allowed; `repose exec` allows it the same way an agent does.

To keep agents and your shells out of a flake's dev shell, add an `.envrc` that doesn't `use flake`.

## Scheduled jobs

The machine has no cron. A systemd user timer runs a job on a schedule with nobody attached, and it survives a stop and start because the unit files live in your home directory. For a job named `backup`, write two files on the machine:

```ini
# ~/.config/systemd/user/backup.service
[Service]
Type=oneshot
ExecStart=/bin/bash -lc 'cd ~/myapp && ./scripts/backup.sh'
```

```ini
# ~/.config/systemd/user/backup.timer
[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

A file on disk schedules nothing until systemd reads it:

```
systemctl --user daemon-reload
systemctl --user enable --now backup.timer
```

`bash -lc` makes it a login shell, so the job gets the same `PATH`, secrets and variables you get in a terminal. `Persistent=true` catches up a run the machine was stopped for, at its next start. Nothing on the machine sends mail, so the job's output is in the journal: `journalctl --user -u backup`. `systemctl --user list-timers` shows when each job runs next. A job that comes due while the machine is [frozen](/docs/lifecycle#frozen-machines) wakes it and runs.

## Ports

As long as you're attached with `repose run` or `repose attach`, every port a program on the machine listens on appears on your laptop's `localhost` within a second or so. tmux shows each new forward:

```text
⇄ localhost:5173 → :5173
```

Ports that open together, such as the servers a test suite starts, get one message between them, like `⇄ 6 ports on localhost: 3000, 3001, 3002, 3003, 3004, 3005`, and tmux's status bar lists every forwarded port. A herdr machine has no such list and shows the messages as [herdr notifications](/docs/run-and-attach#herdr-instead-of-tmux).

Because it's `localhost`, cookies and OAuth redirects behave as they do locally. If the port is taken on your laptop, the next free one within 20 of it is used and the message says which.

A forward needs a listener on `127.0.0.1` or `0.0.0.0`, or on `::1` alone (as Vite does on some setups), on port 1024 or above. A server on another address, a Docker network say, doesn't get one; a container port published with `-p 8080:80` does. The machine keeps 5353, 5355, 5900, 6080, 6081 and 9224 to 9226 for itself. `REPOSE_NO_FORWARD=1` turns the automatic forwarding off.

To forward one port without attaching, [`repose open`](/docs/cli#repose-open-local-port) runs one forward in your terminal until `Ctrl-C` and points your browser at it:

```
repose open 3000
```

There are no public URLs for a project's ports. To show someone a running app, deploy it or use a tunnel. `cloudflared` is in [the menu](/docs/config#the-menu): `repose config add cloudflared` on your laptop. Then, on the machine:

```
cloudflared tunnel --url http://localhost:3000
```

This quick tunnel needs no Cloudflare account and prints a random `trycloudflare.com` URL that lasts until you stop it. It's for testing: at most 200 requests at a time, and no server-sent events. A stable URL takes a named tunnel on your own domain. The tunnel is a connection the machine holds open to the internet, so a [freeze](/docs/lifecycle#frozen-machines) can end it; [keep the machine awake](/docs/config#keeping-a-machine-awake) while the URL has to work.

## Browser

Every agent on the machine has two browser tools registered, `playwright` and `chrome-devtools`: navigate, fill forms, take screenshots, read the console and network. Both drive the same Chromium, which starts the first time an agent uses one of them and keeps its cookies and logins between runs.

Playwright 1.63's Chromium is installed. For another Playwright release, or for Firefox, run `npx playwright install chromium` or `npx playwright install firefox` once, without `--with-deps`. WebKit doesn't run on the machine.

To watch the browser or use it yourself (a captcha, a passkey):

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p
(the view sleeps after 30 idle minutes).
```

Your browser reads the password after the link's `#` and never sends it anywhere. The agent's browser tools use whatever you log into there. Text you copy on the machine lands on your clipboard, and `Ctrl+V` (`Cmd+V` on a Mac) in the page, or its paste button, pastes your clipboard on the machine. If no agent has used the browser yet, the command starts it.

`repose browser stop` closes the view and the forward. After the machine reboots the link's password changes: the page says so, and `repose browser` prints the new link. The agent's browser keeps running while an agent uses it, and stops after 30 minutes with neither an agent nor you on it. A base update doesn't restart it; the new Chromium runs from its next start.

Text on the page is drawn at your tab's size in the machine's pixels; on a Retina display that is 1x, so it is sharp but not as sharp as a native page.

Commands that open a page in a browser, such as `gh browse` or `gh pr create --web`, print the URL instead: `Open in your browser: https://...`. Setting `BROWSER` yourself overrides that.

Playwright test suites (`pnpm exec playwright test` and the like) still run headless unless your config asks for headed browsers. While the agent's browser is up, new shells have `DISPLAY=:99`, so a headed browser you start yourself also appears in the view.

### Use your own Chrome

For a job that needs your logged-in browser, [lend the agents your laptop's Chrome](/docs/your-chrome) instead.

## Network

The machine can reach the internet over TCP and UDP, within [the network limits](/docs/limits#network). Nothing on the internet can reach the machine; the only way in is SSH through repose, with your certificate.

npm, pnpm, yarn v1 and Docker Hub downloads go through a cache on the server. For npm, repose adds two lines to `~/.npmrc`; delete them to go direct and they stay deleted. An `~/.npmrc` that already names a registry or holds an npmjs token is left alone.

## Memory and disk

On a machine out of memory, the kernel kills a process rather than let the machine stall. Your agents, the tmux or herdr server and your SSH connection are kept to the last, so the largest of the rest goes first, usually a runaway test or dev server. `sudo dmesg | grep -i killed` shows what went. The kill takes that process and nothing else: the pane it died in stays open, and the agent that started it keeps working. If it keeps happening, give the machine more memory with [`repose resize --size large`](#changing-the-size) (or `xl`).

The agents' browser, and any `chromium` you start (Puppeteer's too), is held to 1.5 GB on `small`, 3 GB on `large` and 6 GB on `xl`. A tab past that crashes and the browser stays up. A browser a Playwright test launches has no ceiling of its own.

A deleted file's blocks go back to the server at the daily `fstrim`, and at once when the machine stops. Until then the file still counts toward your [plan's disk](/docs/billing#what-a-plan-means), though `df /` on the machine has already forgotten it. `sudo fstrim /` closes the gap now.

Once a week the machine deletes the nix store paths it downloaded or built itself that nothing uses any more, and generations of your nix profile older than 14 days; `sudo systemctl start repose-store-gc` does it now. `nix-collect-garbage` and `nix store gc` are safe to run too: they keep the store paths repose shares with the machine, its system included. If an earlier garbage collection hid any of them, the machine's next start puts them back.

`repose status`, `repose ls` and the project's page in the dashboard say when the machine's filesystem is 90 percent full or more, the point past which a build's next write can fail. Grow the disk with `repose resize 80G`, or with **Resize…** under Disk in the dashboard, which offers 20, 40, 80, 160 and 320 GB. Disks can't shrink, and a bigger one raises the ceiling.

## Seeing what the machine is doing

The dashboard's **Usage** card draws four charts over the last hour, day or week:

- **CPU**: the share of the machine's cores in use.
- **Memory**: memory in use without the page cache, of the size's memory.
- **Waiting for a core**: the time something in the machine was ready to run and had to wait. High while CPU sits at 100% means more work than cores: a build running more jobs than the size has, or several test runs at once. Run fewer jobs at a time, or [give the machine more cores](#changing-the-size).
- **Waiting for the server**: the time the machine's cores waited for a CPU on the server. A slow minute with your own CPU low came from the server.

The hour is drawn a minute to a point, the day five minutes and the week an hour, so a one-minute spike flattens in the longer windows. A stopped machine isn't sampled, and leaves a gap. A [frozen](/docs/lifecycle#frozen-machines) machine is sampled: its CPU reads about zero and its memory stays where it was.

Under the charts, folded, are the eight busiest processes of the window: name, CPU time, peak memory. repose records process names and numbers, never their arguments or anything you type ([what repose stores](/docs/secrets#what-repose-stores)). For a live view, run `htop` on the machine.

Your SSH sessions and the tmux or herdr server get the CPU before the programs in your panes when every core is busy, so what you type keeps showing up at once. Nothing is capped: on an idle machine a build gets everything.

## Changing the size

`repose resize --size small|large|xl` changes a project's size. Only the cores and memory change; the disk keeps its size, and everything on it stays.

The machine has to be stopped for the change. On a stopped project, `repose resize --size xl` changes it and the machine boots at the new size on its next start. On a running one, repose asks first, then [stops it](/docs/lifecycle#stop-and-start), changes it and starts it again, so let running work finish first. No snapshot is taken, since the change doesn't touch the disk; `repose snapshots create` first for one.

While the machine runs, its size counts toward [the memory your plan runs at once](/docs/limits#your-plan).
