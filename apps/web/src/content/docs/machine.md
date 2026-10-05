---
title: The machine
description: What's installed, installing more, your laptop's tools, ports, the browser, memory and disk.
section: Using repose
order: 12
---

Each project gets its own virtual machine running NixOS, with its own kernel, disk, memory and Docker. You log in as `dev`, which has passwordless `sudo`. Your checkout is `/home/dev/<folder>`, named after the folder on your laptop it was first synced from, and everything under `/home/dev` survives a stop and is in snapshots. See [Where the checkout is](/docs/sync#where-the-checkout-is).

| Size    | vCPU | Memory | Disk  |
| ------- | ---- | ------ | ----- |
| `small` | 2    | 4 GB   | 20 GB |
| `large` | 4    | 8 GB   | 40 GB |
| `xl`    | 8    | 16 GB  | 80 GB |

## What's installed

- **Agents:** Claude Code, Codex CLI, opencode, Gemini CLI and pi.
- **Languages:** Node.js 24 with npm and pnpm, Python 3.12 with uv, Go, and rustup (run `rustup default stable` once).
- **Build tools:** gcc, g++, make, cmake, pkg-config, so cgo, node-gyp, Python extensions and Rust crates like `openssl-sys` build. pkg-config finds OpenSSL, zlib, SQLite, libffi, libyaml, libpq, libxml2, libxslt and the MySQL client library, and `pg_config` and `mysql_config` are on `PATH`, so gems like `pg`, `mysql2`, `psych` and `nokogiri` build too.
- **Containers:** Docker with `docker compose`.
- **Browser:** Chromium and Playwright's browsers.
- **Everyday tools:** git, gh, tmux, just, curl, wget, jq, ripgrep, fd, bat, fzf, eza, zoxide, tree, htop, neovim, direnv, sqlite3, `psql`, `pg_dump` and `pg_restore` (no database server; [add one](/docs/config)), openssl, gnupg, dig, lsof, killall, file, zip, unzip and zstd.

The shell is bash with the starship prompt, and `ls` is `eza -al`. `dev` is in the `docker` group, so `docker` needs no `sudo`.

Programs downloaded for other Linux systems run as they would on Ubuntu: Prisma's engines, Playwright's own browsers, numpy and other Python wheels, esbuild, Biome, and binaries from `curl | sh` installers.

## Installing more

Installs stay on the machine's disk and on your `PATH`:

```
npm i -g tsx
go install github.com/air-verse/air@latest
cargo install ripgrep-all
uv tool install httpie
nix profile add nixpkgs#ffmpeg
```

`pip install --user`, bun, deno, gem and composer installs are on `PATH` too, as are the usual directories of yarn, dotnet, ghcup, cabal, opam, luarocks, mix, nimble, juliaup, krew and volta. `nix profile add` takes any package from nixpkgs; search names at [search.nixos.org](https://search.nixos.org/packages).

Type a command the machine doesn't have and it tells you which package has it and how to add it:

```
$ air
air: command not found
  nix profile add nixpkgs#air  install it on this machine
  repose config add air        keep it on every rebuild
                               (run this on your laptop)
Other packages with air: air-formatter
```

The last line only appears when other packages have a command by that name. Installs made on the machine are not part of the project's configuration. To have a package on every rebuild, or a database set up as a service, add it with `repose config add`; see [Installing software](/docs/config).

## Your laptop's tools come along

`repose run` looks at the tools you installed globally on your laptop (with npm, pnpm, bun, `go install`, `cargo install`, uv or pipx) and at the commands your project's scripts call (`package.json`, `Makefile`, `justfile`, `Procfile`, `.air.toml`, compose files). Only names and versions are sent. The machine installs the ones it lacks in the background:

```text
Installing 2 of your tools in the background: air, portless
```

Each comes from nixpkgs when nixpkgs has it, so its version can differ from your laptop's; otherwise your laptop's version is installed with its own package manager. If a tool fails to install, the next `run` says so; the log is `~/.repose/tools-install.log` on the machine. A Node major version pinned in `.nvmrc`, `.node-version`, `.tool-versions`, `volta.node` or `engines.node` (the first found) is installed and made the default `node`.

Ruby and Java versions work the same way. A Ruby version in `.tool-versions`, `.ruby-version` or the Gemfile's `ruby` line, and a Java version in `.tool-versions`, `.java-version` or `.sdkmanrc` (the first found of each), is installed from nixpkgs and made the default `ruby` or `java`. nixpkgs has one Ruby per minor version (3.3, 3.4 and 4.0 today) and one JDK per major (8, 11, 17, 21 and 25), not every patch release: the machine gets the same minor or major as your pin, or the closest newer one when nixpkgs doesn't have it, and `repose scan` tells you which. A Ruby 3.2.2 pin gets Ruby 3.3. JRuby and TruffleRuby pins are ignored. Gems install into `~/.local/share/gem`.

To see the list without installing anything:

```
repose scan
```

## Projects with a flake.nix

Agents start in the project's dev environment, so they and every command they run see the tools and variables it provides. This holds for every window, `--worktree` ones included, and for `repose exec`:

- With an `.envrc`, agents get what it sets. The first time an agent starts in a checkout, repose runs `direnv allow` for its `.envrc`, and again after the file changes; the agent's window says so. If you ran `direnv deny` on it, agents start without it.
- With a `flake.nix` that defines a dev shell and no `.envrc`, agents start in that dev shell. Nothing is written to the checkout: if the repository has no `flake.lock`, the one Nix makes is kept on the machine, outside the checkout.

The first load builds the dev shell. A few packages from a nixpkgs the machine hasn't fetched yet take 20 to 40 seconds; anything nixpkgs has to compile takes longer. Later loads reuse it and take under a second, until `flake.nix` or `flake.lock` changes. `repose run` shows `Loading the project's dev shell` meanwhile and sends your prompt once the agent is up. If the dev shell fails to load, the agent starts without it and its window shows the error first.

Only the dev shell for `x86_64-linux` is used. `nixosConfigurations`, `nixosModules`, `darwinConfigurations`, `homeConfigurations` and `packages` in the same flake change nothing on the machine. To install software for every shell on the machine, or to run a database, use [repose config](/docs/config).

Three things trip up a first flake:

- **Commit `flake.nix`.** Nix only sees files git tracks. An untracked `flake.nix` still reaches the machine, then fails to load with `Path 'flake.nix' in the repository ... is not tracked by Git`.
- **Commit `flake.lock`.** Without one, each new machine locks the flake's inputs to whatever is newest that day, so two machines can get different versions. With an `.envrc` that says `use flake`, the first load also writes `flake.lock` into the checkout and stages it, and your next `repose sync` stops with `The machine has uncommitted changes your laptop doesn't have`, naming `flake.lock`. Run `nix flake lock` on your laptop and commit the file. Without Nix on your laptop, have the agent commit `flake.lock` and bring it back with `git fetch repose`.
- **Define the dev shell for `x86_64-linux`.** The machine is x86-64 Linux whatever your laptop is. A flake written on a Mac with only `devShells.aarch64-darwin` fails with `does not provide attribute 'devShells.x86_64-linux.default'`. Name both systems, or use `flake-utils.lib.eachDefaultSystem`.

Your own shells get the same dev shell. In `repose ssh`, `ssh todo-app.repose`, an editor's terminal or a tmux window you open, bash loads it when you `cd` into the checkout and unloads it when you leave:

- With a `flake.nix` and no `.envrc`, you get the dev shell the agents got, from the same cache, so a later load takes under a second. On entering, the shell prints `repose: loading the dev shell from ~/todo-app/flake.nix`. If nothing has built it yet, the prompt waits for the build. Press Ctrl-C to skip it; the shell then goes without it until you leave the folder and come back, or `flake.nix` changes.
- With an `.envrc`, it loads once the file is allowed: repose allows it the first time an agent or `repose exec` starts there, or you run `direnv allow`. One you denied stays out. With `use flake` in it, direnv keeps a `.direnv` directory in the checkout, so add `.direnv/` to `.gitignore`.

This works in any checkout or worktree in your home folder. To keep agents and your shells out of a flake's dev shell, add an `.envrc` that doesn't `use flake`.

## Ports

While you're attached with `repose run` or `repose attach`, every port a program on the machine listens on appears on your laptop's `localhost` within a second or so. tmux shows each new forward:

```text
⇄ localhost:5173 → :5173
```

Ports that open together, such as the servers a test suite starts, get one message between them, like `⇄ 6 ports on localhost: 3000, 3001, 3002, 3003, 3004, 3005`, and the status bar lists every forwarded port.

Because it's `localhost`, cookies and OAuth redirects behave as they do locally. If the port is taken on your laptop, the next free one (up to 20 higher) is used and the message says which.

Ports below 1024 aren't forwarded, nor are the machine's own 5353, 5355, 5900, 6080 and 6081, nor servers that listen only on another address such as a Docker network. A container port published with `-p 8080:80` is.

To forward one port without attaching:

```
repose open 3000
```

It opens your browser and runs until `Ctrl-C`. It reaches a server listening on `127.0.0.1`, `0.0.0.0` or only on `::1` (as Vite does on some setups); if nothing listens on the port yet, it says so and forwards to `127.0.0.1`. If the port is taken on your laptop, it uses a free one and says which. `--local-port 8080` picks the laptop port, `--no-browser` only prints the URL. `REPOSE_NO_FORWARD=1` turns the automatic forwarding off.

There are no public URLs for a project's ports. To show someone a running app, deploy it or use a tunnel. `cloudflared` is in the menu: `repose config add cloudflared` on your laptop. Then, on the machine:

```
cloudflared tunnel --url http://localhost:3000
```

This quick tunnel needs no Cloudflare account and prints a random `trycloudflare.com` URL that lasts until you stop it. It is for testing only: it carries at most 200 requests at a time and no server-sent events, and it doesn't work while `~/.cloudflared/config.yaml` exists, so rename that file first. For a stable URL, set up a named tunnel on your own domain with a Cloudflare account.

## Browser

Claude Code on the machine has two browser tools registered, `playwright` and `chrome-devtools`: navigate, fill forms, take screenshots, read the console and network. Both drive the same Chromium, which starts the first time an agent uses one of them and keeps its cookies and logins between runs. Ask for them in a prompt:

```
repose run "screenshot each signup step with playwright"
```

Playwright test suites run without `npx playwright install`.

To watch the browser or use it yourself (a captcha, a passkey):

```
$ repose browser
Watching todo-app's browser at http://localhost:6080/#p=5m2k8Q1p
(the view sleeps after 30 idle minutes).
```

Your browser opens on that link and shows the agent's browser, live, at the size of your tab. The password is the part of the link after `#`, which your browser reads and never sends anywhere. The agent's browser tools use whatever you log into there. Copy and paste work both ways (your browser asks once before the page may read your clipboard; Firefox only lets text travel from the machine to you). If no agent has used the browser yet, the command starts it.

The command returns at once and leaves the forward running in the background. Run it again for the same link, `repose browser --no-open` to print the link without opening a browser, and `repose browser --stop` to close the view and the forward. If port 6080 is taken on your laptop (another project's view, say), a free port is used and the link shows it. Opening the page again wakes a sleeping view. After the machine reboots the link's password changes: the page says so, and `repose browser` prints the new link. The agent's browser keeps running while an agent uses it, and stops after 30 minutes with neither an agent nor you on it.

Text on the page is drawn at your tab's size in the machine's pixels; on a Retina display that is 1x, so it is sharp but not as sharp as a native page. `repose open --desktop` is the old name of the command and still works.

A machine on base 2026.09.27.2 or older still has the old page, which asks for the password the command printed; it gets the new one at its next base update. A CLI at v0.1.20 or older has no `repose browser`; `repose open --desktop` there prints the password to type.

An agent session started before September 25, 2026 has a headless browser of its own that the desktop can't show; restart the agent to switch it over.

Playwright test suites (`pnpm exec playwright test` and the like) still run headless unless your config asks for headed browsers. While the agent's browser is up, new shells have `DISPLAY=:99`, so a headed browser you start yourself also appears on the desktop.

### Use your own Chrome

For a job that needs your logged-in browser (an internal tool behind SSO, an account with a hardware key, an extension), lend the agents your laptop's Chrome instead: `repose run --bridge`, or `repose browser bridge` in a second terminal. While the bridge is open, the machine's two browser tools drive your Chrome, with your logins, and switch back when it closes; nothing on the machine restarts. `--allow` keeps them to the sites you name. It needs Chrome 144 or newer and a running machine. [Lend the agents your Chrome](/docs/your-chrome) has the whole of it: setup, what the agents can and can't do there, and troubleshooting.

## Network

The machine can reach the internet. Nothing on the internet can reach the machine; the only way in is SSH through repose, with your certificate. Outbound traffic is limited to 200 Mbit/s, and downloads to 1 Gbit/s. npm, pnpm, yarn v1 and Docker Hub downloads go through a cache on the server. For npm, repose adds two lines to `~/.npmrc`; delete them to go direct. An `~/.npmrc` that already names a registry or holds an npmjs token is left alone. [Limits](/docs/limits) has what's blocked.

## Memory and disk

When a machine runs out of memory, something is killed. Your agents and tmux are kept to the last, so a runaway test or dev server goes first. `sudo dmesg | grep -i killed` shows what went. Headless Chromium is stopped past 1.5, 3 or 6 GB depending on size. If it keeps happening, give the machine more memory with `repose resize --size large` (or `xl`); see [Changing the size](#changing-the-size).

Grow the disk with `repose resize 80G`, or from the project's page in the dashboard (**Resize…** under Disk, 20 to 320 GB). Disks can't shrink, and the larger disk is [billed](/docs/billing) from then on. A disk can grow only as far as the server it runs on has room for; [Limits](/docs/limits#disk-and-console) has the disk speed and size limits.

## Changing the size

A project's size is chosen when it's created (`repose run --size`, default `large`) and can be changed later with `repose resize --size small|large|xl`. Only the vCPUs and memory change; the disk keeps its size, and everything on it stays.

The size changes while the machine is stopped. On a stopped project, `repose resize --size xl` changes it and the machine boots at the new size on its next start. On a running one, repose asks first, then stops it (taking a snapshot), changes it and starts it again. The stop ends every process on the machine, agents included, so let running work finish first; `-y`/`--yes` skips the question. Asking for the size a project already has does nothing.

It prints what the new size gives and costs, for example `8 vCPU, 16 GB memory; needs the Plus plan`. Hours are billed at the size the machine ran at; see [Pricing](/docs/billing). `xl` counts toward the [limit on xl projects](/docs/limits).
