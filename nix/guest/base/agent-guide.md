<!-- The machine guide every agent on a repose machine reads (DECISIONS I-243).
agent-guide.nix renders it: HTML comments are dropped, and a line marked
"needs: CMD" is dropped while the guest has no CMD. Each line names the /docs
section it summarises. internal/cli/agent_guide_test.go fails when a section
of machine.md, agents.md or limits.md is referenced by no line here, when a
reference has no such page or heading, and when a command named here is not
on the machine. Keep it short and factual; the user reads it too. -->
# This machine

This is a repose machine: a NixOS virtual machine for one project, where agents keep working after the user's laptop closes. <!-- /docs/machine -->
The user works from their laptop. You cannot reach the laptop or its files from here; what they should see has to be on this machine, in git, or sent with the commands under "Reaching the user". <!-- /docs/secrets#what-an-agent-on-the-machine-can-reach -->
The user can also work in this checkout from their laptop without attaching: in their editor over SSH (`repose code`), or one command at a time (`repose exec`), so files here can change while you work. <!-- /docs/ssh-and-editors -->
Before you tell the user to run a `repose` command, read its page: https://repose.herakraft.co/llms.txt lists every docs page as plain markdown. The docs describe the latest release. `~/.repose/cli-version` holds the version on the user's laptop as of their last `repose run` or `repose attach`; when it is older, a command may not work as the docs show, so tell them to update by running the install command again. <!-- /docs/cli -->
You are `dev`, with passwordless `sudo`. The checkout is under `/home/dev`, and everything in `/home/dev` survives a stop; `/tmp` starts empty at each boot. <!-- /docs/machine -->
The checkout is named after the folder on the user's laptop it came from, not after the project, and `repose-checkout` prints its path; before the first sync there is none and work happens in `/home/dev`. <!-- /docs/sync#where-the-checkout-is --> <!-- needs: repose-checkout -->
The user may have added other repositories to this machine as folders beside it, listed in `~/.repose/checkouts`; each is a separate project of theirs. Work in the folder you were started in, and leave the others alone unless the user asks. <!-- /docs/run-and-attach#several-repositories-on-one-machine -->

## Servers and ports

- While the user is attached, every port a program here listens on (1024 and up, on `localhost` or `0.0.0.0`) appears on their laptop at the same port within a second or so. Tell them "open http://localhost:PORT". <!-- /docs/machine#ports -->
- There are no public URLs. Don't look for one or start a tunnel unless the user asks. <!-- /docs/machine#ports -->
- Not forwarded: ports below 1024, 5353, 5355, 5900, 6080, 6081 and 9224 to 9226 (the machine's browser), and servers that listen only on another address. A Docker port published with `-p` is forwarded. <!-- /docs/machine#ports -->
- If the user isn't attached, they can forward one port with `repose open PORT` on their laptop. <!-- /docs/machine#ports -->

## Installing tools

- Already installed: Node.js 24 with npm, pnpm and yarn (through corepack), Python 3.12 with uv, Go, rustup (run `rustup default stable` once, and `rustup component add rust-analyzer` if your editor uses it), gcc, make, cmake, Docker, Chromium, git, gh, jq, ripgrep, sqlite3, psql and the usual command-line tools. <!-- /docs/machine#whats-installed -->
- Programs built for other Linux systems (prebuilt binaries, Prisma engines, Python wheels, `curl | sh` installers) run as they would on Ubuntu. Scripts that start with #!/bin/bash or #!/usr/bin/python3, and Makefiles with SHELL := /bin/bash, run unchanged. <!-- /docs/machine#whats-installed -->
- Python packages go in a venv (`uv venv`, or `python3 -m venv .venv` and then pip); Python CLIs install with `uv tool install`. The system `python3` has no Tk; for tkinter, use a Python from uv (`uv python install 3.12`). <!-- /docs/machine#whats-installed -->
- Install a missing tool now with `nix profile add nixpkgs#NAME`. Typing a missing command prints the package that has it. `npm i -g`, `go install` and `uv tool install` work too. All of these stay on this machine's disk. <!-- /docs/machine#installing-more -->
- Installs made here are not part of the project's configuration. To keep a package on every rebuild, tell the user to run `repose config add NAME` on their laptop. <!-- /docs/config#add-a-package -->
- A tool the user wants on every machine of their account, with their shell aliases and dotfiles, belongs in their machine.nix: `repose config --global add NAME` on their laptop. <!-- /docs/config#your-machine-nix -->
- Tools the user has on their laptop are installed in the background after each `repose run`. If one is missing right after a start, check `~/.repose/tools-install.log` before installing it yourself. <!-- /docs/machine#your-laptops-tools-come-along -->
- There is no cron. For a job on a schedule, write a service and a timer in ~/.config/systemd/user: NAME.service with Type=oneshot and ExecStart=/bin/bash -lc 'CMD' under [Service], NAME.timer with OnCalendar=daily and Persistent=true under [Timer] and WantedBy=timers.target under [Install]. Then run `systemctl --user daemon-reload && systemctl --user enable --now NAME.timer`. It runs with nobody attached and survives a stop. <!-- /docs/machine#scheduled-jobs -->
- npm, pnpm, yarn v1 and Docker Hub downloads go through a cache on the server. Leave the two lines repose added to `~/.npmrc` in place. <!-- /docs/machine#network -->
- You started inside the project's dev environment: its `.envrc`, or its flake's dev shell when it has a `flake.nix` and no `.envrc`. If a tool the flake provides is missing, the load failed or the `.envrc` is denied; the top of your tmux window or herdr pane says which. <!-- /docs/machine#projects-with-a-flake-nix -->
- A project flake's binary caches apply with `nix develop --accept-flake-config`; Nix's prompt for them needs a TTY you don't have. <!-- /docs/machine#projects-with-a-flake-nix -->

## Docker and databases

- `docker` and `docker compose` work without `sudo`. <!-- /docs/machine#whats-installed -->
- No database server is installed. Run one in Docker (`docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=dev postgres:17`), or tell the user to run `repose config add postgresql` on their laptop (also `redis`, `mysql` and others), which starts it on `localhost`. <!-- /docs/config#the-menu -->
- `host.docker.internal` isn't defined. To reach a server on the machine from a container, add `extra_hosts: ["host.docker.internal:host-gateway"]` (or `--add-host host.docker.internal:host-gateway`), and have that server listen on `0.0.0.0`; one listening only on localhost refuses the connection. <!-- /docs/machine#whats-installed -->

## Secrets

- Each command you run sees the user's current secrets as environment variables, and `/run/repose/secrets/NAME` holds the same value. Check that one is set with `[ -n "${NAME:-}" ]`; never read or print the file. <!-- /docs/secrets#store-an-api-key -->
- Never print, log or commit a secret's value, and never write one into the repository. Refer to it by name, as `$NAME`. <!-- /docs/secrets#store-an-api-key -->
- If you need a secret that isn't set, ask the user to run `repose secrets set NAME` on their laptop, or `repose secrets import` to set every line of a `.env` file there. A new value reaches your next command within seconds, unless you or the project's `.envrc` set that variable yourself, which then wins; a long-running process such as a dev server gets it only when you restart it, and a command you run with `sh` instead of `bash` keeps the values you started with. <!-- /docs/secrets#store-an-api-key -->
- The user's Vercel login is not copied here. If the Vercel CLI says it isn't logged in, ask the user to log in to Vercel on this machine, or to store a team-scoped token with `repose secrets set VERCEL_TOKEN` on their laptop. <!-- /docs/secrets#logins-copied-from-your-laptop -->

## Browser

- Drive a browser with the `playwright` or `chrome-devtools` MCP tools, which every agent here has (Codex not while `~/.codex/config.toml` is a symlink), or with Playwright from code. Playwright @playwrightVersion@'s Chromium is installed; for another Playwright release, or for Firefox, run `npx playwright install chromium` or `npx playwright install firefox` once, without `--with-deps`. WebKit does not run here. <!-- /docs/machine#browser -->
- The `playwright` and `chrome-devtools` tools share one browser, which the user sees live when they run `repose browser` on their laptop. For a step only a person can do (a captcha, a passkey, a login), ask them to run `repose browser` and do it in that browser; its logins are kept. <!-- /docs/machine#browser -->
- While the user runs `repose browser bridge` on their laptop, those same two tools drive the user's own Chrome there instead, with their logins and extensions; the switch happens on your next call, nothing restarts. `repose-guest-profile browser bridge status` prints on while it does, off otherwise. A site the user is logged in to on their laptop needs that; ask them for the bridge rather than for their password. <!-- /docs/machine#use-your-own-chrome --> <!-- needs: repose-guest-profile -->
- The user may open the bridge with an allowlist. Then a site off the list fails: navigating there returns an error saying it is not on the bridge's allowlist, and pages there fail to load with net::ERR_BLOCKED_BY_CLIENT. Ask the user to add the site; don't try to reach it another way. Tabs of theirs on other sites are hidden from you. <!-- /docs/your-chrome#keep-the-agents-to-some-sites -->
- Through the bridge, network events carry no cookies, no Authorization or API-key headers, no request bodies and no WebSocket or server-sent event payloads, and reading a response body is refused. Read what you need from the page itself. <!-- /docs/your-chrome#what-the-agents-can-and-cant-do-in-your-chrome -->
- `gh browse`, `gh pr create --web` and other commands that open a browser print the URL instead; pass it on to the user. <!-- /docs/machine#browser -->

## Memory and disk

- When memory runs out, test runs and dev servers are killed before agents, tmux, herdr and the user's SSH connection. `sudo dmesg | grep -i killed` shows what went. <!-- /docs/machine#memory-and-disk -->
- `df -h /home/dev` shows free disk. Past 90 percent, one build or download can fill it, and then writes fail with "No space left on device"; tell the user then, since only they can grow it, with `repose resize 80G` (any size) on their laptop. <!-- /docs/machine#memory-and-disk -->
- `sudo systemctl start repose-store-gc` deletes the nix store paths this machine downloaded or built that nothing uses, and nix profile generations older than 14 days; it also runs weekly. <!-- /docs/machine#memory-and-disk -->
- If processes keep getting killed for memory, tell the user: `repose resize --size large` (or `--size xl`) on their laptop gives the machine more memory. It restarts the machine, which ends every process here, you included. <!-- /docs/machine#changing-the-size -->
- The user sees this machine's CPU, memory and busiest processes on the dashboard. Several builds or test runs at once can keep every vCPU busy and slow each other down; run fewer at a time. <!-- /docs/machine#seeing-what-the-machine-is-doing -->

## Reaching the user

- The user is notified when you finish or wait for input. <!-- /docs/notifications#what-youll-get -->
- To tell the user something while they're away, run `repose-notify "MESSAGE"`. It reaches their phone or email. <!-- /docs/notifications#agents-can-message-you-and-ask-questions --> <!-- needs: repose-notify -->
- To hand the user a file, give them the command for their laptop: `repose cp NAME:/home/dev/PATH .`, where NAME is this project's name, which `jq -r .slug ~/.repose/project.json` prints. Their scp, rsync and editor reach this machine as the SSH host NAME.repose. <!-- /docs/ssh-and-editors -->
- When you're blocked on a decision only the user can make, run `repose-ask --options yes,no "QUESTION"` (or without `--options` for a free answer). It waits up to 30 minutes (`--timeout`) and prints their answer; exit 3 means no answer came, 4 that they have no notifications set up. Don't ask what you can decide yourself. <!-- /docs/agents#let-it-ask-you --> <!-- needs: repose-ask -->

## Git

- Commit your work so the user can get it. From their laptop they fetch the commits in this checkout, worktree branches included, straight from this machine with `git fetch repose`, so they don't need you to push. Uncommitted changes don't reach them that way. Push when they ask for it. <!-- /docs/sync#getting-work-back -->
- The user's `repose sync` writes only the files their laptop's work changes, and stops before writing over one you changed or while you are in the middle of a merge, rebase, cherry-pick, revert or bisect. It moves the checkout to their branch, from any branch you are on. When that branch has commits of yours their laptop lacks, it merges their commit into it; when that merge can't be made cleanly, it checks their commit out detached (`git status` says HEAD detached) and your branches keep your commits. Then switch back to your branch and merge that commit, or ask the user, before you commit. <!-- /docs/sync#when-the-machine-has-changes-of-its-own -->
- Push over HTTPS. When the user's `gh` login was copied, `git push` to github.com works, and `git@github.com:` remotes are rewritten to HTTPS. There is no SSH key on this machine. <!-- /docs/secrets#logins-copied-from-your-laptop -->
- Commits made here are unsigned; the signing key stays on the laptop. <!-- /docs/secrets#git-and-claude-code-settings -->
- With no setting from the user, git here names a new repository's first branch main, merges on `git pull`, and sets the upstream on a branch's first `git push`. <!-- /docs/secrets#git-and-claude-code-settings -->
- git-lfs is installed. A synced checkout holds LFS files as pointer files until `git lfs pull`. <!-- /docs/sync#submodules -->
- You have no terminal for gpg's passphrase prompt: pass `--batch --pinentry-mode loopback --passphrase-fd 0`. <!-- /docs/machine#whats-installed -->
- If you were started in a folder next to the checkout (`~/CHECKOUT-worktree-1` and the like, where CHECKOUT is the checkout's folder), it is a git worktree on its own branch (worktree-1 and the like), so other agents' files are not yours. Commit your work on that branch; don't copy it into the checkout. <!-- /docs/run-and-attach#several-agents-separate-trees -->

## Agents

- Claude Code (`claude`), Codex (`codex`), opencode (`opencode`), Gemini CLI (`gemini`) and pi (`pi`) are installed. <!-- /docs/agents -->
- If an agent says it isn't logged in, ask the user to log it in on this machine; logins are never copied here for Claude Code. <!-- /docs/agents#log-in -->
- `~/.claude/CLAUDE.md`, settings and skills are copied from the user's laptop at each `repose run`, so lasting changes to them belong on the laptop. <!-- /docs/agents#your-claude-code-setup-comes-along -->
- Claude Code, Codex and Gemini CLI can search the web. opencode can only when $OPENCODE_ENABLE_EXA is 1, which the user turns on with `repose secrets set OPENCODE_ENABLE_EXA` on their laptop; pi has no search tool, so fetch pages with `curl`. <!-- /docs/agents#web-search -->
- MCP servers from the user's laptop arrive with `${NAME}` in place of tokens; one that fails to start usually lacks that secret, and `repose-mcp status --json` lists the secrets each server needs. Ask the user to set it on their laptop with `repose secrets import --mcp`, or `repose secrets set NAME` when the laptop has no value, and then to restart this agent session. Servers that need the laptop (Apple Notes, Xcode) stay there; when one of their tools answers that the laptop isn't connected, ask the user to run `repose mcp forward NAME` on their laptop. <!-- /docs/agents#mcp-servers --> <!-- needs: repose-mcp -->
- An MCP server you add with an agent's own command reaches only that agent. Put a token in by its secret's name, never its value: `${NAME}` for Claude Code, Gemini CLI and pi, `{env:NAME}` for opencode, `env_vars = ["NAME"]` or `bearer_token_env_var = "NAME"` for Codex. A server repose put there comes back at the next start if you delete it; turn it off with the agent's own switch instead. <!-- /docs/agents#mcp-servers -->
- Files and images the user drops or pastes into the terminal are copied to `/tmp/repose-paste/`, and the file's path there is pasted into your prompt; a file from the checkout arrives as its path in the checkout instead. Claude Code attaches images; other agents can open the file. Copies are deleted after a day. Hidden files, private keys and system files are never copied: you get the laptop's path as text, and you can't open it. Don't ask the user to paste such a file. <!-- /docs/run-and-attach#drop-a-file-or-paste-an-image -->

## Limits

- The user's plan buys memory that may run at once (Solo 8 GB, Plus 16 GB, Pro 32 GB), disk that may be allocated and egress for the month. A start refused with exit code 7 and a message naming the machine using the memory is the user's call: they stop one or upgrade. Don't work around it. <!-- /docs/limits#your-plan -->
- Data this machine sends to the internet counts against the user's monthly egress allowance (250 GB on Solo, 100 GB during its $20 introductory months, 500 GB on Plus, 1 TB on Pro); every GB past it costs them $0.05, and at four times the allowance their machines stop until the month turns. Incoming data is free, disk is a hard limit: don't download, serve or upload large files needlessly. <!-- /docs/limits#egress -->
- Nothing on the internet can connect to this machine. Outbound TCP and UDP are allowed, up to 200 Mbit/s; ping to the internet gets no reply, so check connectivity with `curl -sI https://example.com`. <!-- /docs/limits#network --> <!-- /docs/machine#network -->
- Outbound port 25 is blocked. Send mail through a provider's API or its submission port (587 or 465). <!-- /docs/limits#network -->
- New outbound connections are limited to 200 a second, in bursts of up to 2000, with at most 16,384 open at once. <!-- /docs/limits#network -->
- Disk reads and writes are rate-limited by size (small 2,000 operations a second and 80 MB/s, large 3,000 and 120 MB/s, xl 4,000 and 150 MB/s), so a slow build or test may be disk-bound. A disk resize can fail with "the host has no room for this project right now" even within the plan. <!-- /docs/limits#disk-and-console -->
- Don't mine cryptocurrency, send bulk mail, or scan or flood other systems. A known miner gets the machine stopped. <!-- /docs/limits#what-isnt-allowed -->
