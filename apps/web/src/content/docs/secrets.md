---
title: Secrets and security
description: API keys, tool logins and git settings, where each one lives, and what can reach what.
section: Using repose
order: 14
---

## Store an API key

```
$ repose secrets set STRIPE_SECRET_KEY
Value for STRIPE_SECRET_KEY: ********************************
Set STRIPE_SECRET_KEY (pushed to running guest)
```

Each character you type or paste shows as `*`.

Or read the value from a file or from your laptop's environment:

```
repose secrets set GOOGLE_CREDENTIALS --from-file ./sa.json
repose secrets set OPENAI_API_KEY --from-env
```

To set several at once from a `.env` file, import it:

```
$ repose secrets import .env.local
Set 3 on todo-app from .env.local (pushed to the running machine):
DATABASE_URL (replaced), STRIPE_SECRET_KEY, OPENAI_API_KEY
```

Without a file name it reads `./.env`; `-` reads stdin, so a secrets manager can pipe into it. Each `NAME=VALUE` becomes a secret, replacing one of the same name, as `secrets set` does. `--dry-run` lists the names it would set and sends nothing. Only names are printed, never values.

The file is read the way docker compose and the dotenv libraries read it: `#` comments and blank lines are skipped, `export ` in front of a name is ignored, `'single quotes'` keep a value exactly as written, `"double quotes"` understand `\n`, `\t`, `\"` and `\\` and can span lines (a PEM key, say), and an unquoted value ends at ` #`. `${VAR}` is not expanded. If any name isn't a valid secret name (below), nothing is imported and the error lists the lines to fix.

On the machine, each secret is an environment variable and a file at `/run/repose/secrets/NAME`. Both are kept in memory only: never on the machine's disk, never in snapshots. A change, a removal included, reaches a running machine within seconds. Each command an agent runs after that sees it, and so does each new shell or tmux window. A program already running, such as a dev server or a shell you have open, keeps the old value until you restart it (`exec $SHELL` in a shell). The refresh comes from bash, so a command an agent runs with `sh` (`sh -c`, a `#!/bin/sh` script) keeps the values the agent started with.

A value you set yourself wins over a secret of the same name and stays when secrets change later: one in the project's `.envrc`, one you export in a shell, or one you give a single command (`STRIPE_KEY=sk_test ./run-tests.sh`). The machine tells your value from its own by comparing it with the values it delivered for that name, the last 16 of them. A value equal to one of those follows the secret, so an `.envrc` that loads the same `.env` you imported gets your later changes. A command that lacks a secret gets it back after the next change, a sandbox's command included; to keep a secret out of a command, give the variable an empty value, or leave `BASH_ENV` out of that command's environment. The machine does this with two environment variables of its own. `BASH_ENV` points every bash at `/etc/repose/bash-env.sh`, which refreshes the secrets; if you set your own `BASH_ENV`, commands under it stop picking up changes. `REPOSE_ENV_GEN` records which version of your secrets a process holds. Neither can be a secret's name, and nor can `ENV`.

So that a program still holding a removed or replaced value loses it, the machine keeps that value in memory in `/run/repose/secrets.refresh`, readable by the `dev` user, until the machine restarts (for a secret you keep changing, until 16 newer values replace it). If a key leaked, revoke it with its provider: removing the secret here takes it out of the machine's commands but leaves it valid.

```
repose secrets list
repose secrets rm STRIPE_SECRET_KEY
```

`list` shows names and dates, never values, and on a terminal it also lists the logins your laptop copies ([below](#choose-what-is-copied)). Nothing shows a value again after you set it. Secrets belong to one project; [`repose fork`](/docs/lifecycle#fork-a-project) gives each copy the project's secrets as they are at the time. Names are uppercase letters, digits and underscores, start with a letter and are up to 64 characters, other than `BASH_ENV`, `ENV` and `REPOSE_ENV_GEN`; values up to 64 KB.

The dashboard's project **Secrets** page does the same.

## Logins copied from your laptop

At each `repose run`, these are copied straight to the machine over SSH if you have them. repose never stores them.

| Tool       | File                                                                          |
| ---------- | ----------------------------------------------------------------------------- |
| GitHub CLI | `~/.config/gh/hosts.yml` (with the token, even if it's in the macOS keychain) |
| Codex CLI  | `~/.codex/auth.json`                                                          |
| opencode   | `~/.local/share/opencode/auth.json`                                           |

Your SSH keys never reach the machine, and your ssh-agent isn't forwarded. With `gh` logged in on your laptop, git on the machine sends every GitHub URL, `git@github.com:owner/repo` and `ssh://git@github.com/owner/repo` included, over HTTPS with that login. If you log in to `gh` on the machine instead, run `gh auth setup-git` there once.

Never copied: SSH private keys, Claude Code's login, Gemini's OAuth login, the Vercel CLI's login. See [Agents](/docs/agents#log-in) for the agents' logins.

The Vercel CLI's login stays on your laptop because it reaches your whole Vercel account: every team and every project. An agent on the machine could deploy, delete a project or read another project's environment variables with it. To use Vercel on the machine, either log in there:

```
vercel login
```

That login is saved on the machine's disk, so it's in its snapshots. Or create a token in Vercel's account settings, scoped to one team and with an expiry, and store it as a secret. The Vercel CLI reads `VERCEL_TOKEN` from the environment:

```
repose secrets set VERCEL_TOKEN
```

repose used to copy this login. If an earlier `repose run` copied it, the next `repose run` removes that copy and says so. A login you made on the machine is left alone, and so is a copy the Vercel CLI on the machine has rewritten since; delete `~/.local/share/com.vercel.cli/auth.json` there to remove it. Snapshots taken before then still hold the copy; to be sure, revoke that login's token in Vercel's settings and run `vercel login` on your laptop again.

### Choose what is copied

The logins in the table above are copied until you say otherwise, and so are your gitignored `.env` files ([Sync](/docs/sync)). To keep some of them on your laptop, run `repose secrets choose`:

```
$ repose secrets choose
Copied at each repose run, for every project:
space toggles, enter saves, q leaves

> [x] gh        GitHub CLI login: every repo you can reach
  [x] codex     Codex CLI login
  [ ] opencode  opencode login (not logged in on this laptop)
  [x] env       gitignored .env files (2 in this checkout)
```

Or name them, which also works in scripts:

```
repose secrets choose --off gh env
repose secrets choose --on env
```

The choice is saved in `~/.config/repose/config.toml` on your laptop, as `skip = [...]` under `[logins]`, and applies to every project. `--project NAME` gives one project its own list, saved under `[projects.NAME.logins]`, and `repose secrets choose --reset --project NAME` sends it back to the shared list. repose never sees the list.

The next `repose run` after you turn one off removes the copy an earlier run left on the machine, as long as it is still the same as your laptop's. A login you made on the machine, or a `.env` file an agent changed there, is left alone, and `run` names the file. Snapshots taken before then still hold the copy; revoke that token where you created it.

Without the `gh` login, git on the machine has no way to push to GitHub. Run `gh auth login` there, or use a token as described below.

### Other git hosts

GitLab, Bitbucket and your own server have no login that repose copies, and your SSH keys stay on your laptop. Pick one of these.

**A token, kept as a secret.** Create a token with write access to the repository on the git host, then store it:

```
repose secrets set GITLAB_TOKEN
```

On the machine, tell git to use it for that host, and to send the host's SSH URLs over HTTPS:

```
git config --global credential.https://gitlab.com.helper \
  '!f() { echo username=oauth2; echo "password=$GITLAB_TOKEN"; }; f'
git config --global url.https://gitlab.com/.insteadOf git@gitlab.com:
```

Bitbucket takes your username and an app password or access token in the same place.

**A deploy key made on the machine.** On the machine:

```
ssh-keygen -t ed25519 -N '' -C todo-app.repose \
  -f ~/.ssh/id_ed25519
cat ~/.ssh/id_ed25519.pub
```

Add the public key to that one repository as a deploy key with write access. It can push to that repository and nothing else. The private key lives on the machine's disk, so it's in its snapshots; delete the deploy key on the git host when you destroy the project.

## Git and Claude Code settings

Your global git settings are copied, minus credential helpers, signing, URL rewrites, proxies, `core.sshCommand`, `core.hooksPath`, diff and merge tools, a pager or editor the machine doesn't have, and anything that looks like a token. Settings you make on the machine win. Commits made on the machine are unsigned, since the signing key stays on your laptop.

Your Claude Code setup is copied too: `CLAUDE.md`, `settings.json` (with `env` and API key helpers removed), skills, agents, commands and the scripts your hooks run. [Agents](/docs/agents#your-claude-code-setup-comes-along) has the details.

## What an agent on the machine can reach

- **Everything on the machine**, including your checkout, `.env` files and the logins above. It has `sudo`.
- **The internet**, outbound, with the limits in [Limits](/docs/limits).
- **Your git host**, with whatever credentials the machine has.
- **Not your other projects.** Each is a separate machine, and the network stops them from reaching each other.
- **Not your laptop.** The machine can't open connections to it, and it never gets your SSH keys or your ssh-agent. While you're attached, ports on the machine appear on your laptop's `localhost`.

## What repose stores

- Your account (GitHub login, email), and each project's name, git remote, size, state, configuration and build logs.
- Named secrets, encrypted with a key for your account that is itself protected by a key in Azure Key Vault.
- Once a minute, for billing and abuse detection: whether the machine is running, CPU, memory, network and disk use, and the names of running processes. Never their arguments or environment.
- Agent events: which agent, what kind of event, and the agent's own one-line summary.

Never stored: your code, prompts, terminal contents, files on the machine, or the logins copied from your laptop. The [privacy policy](/privacy) has the full list.

Disks aren't encrypted per project yet, so an operator with root on a server can read the disks on it. Every operator login and command inside a machine is audited, and operators look inside only for an incident, an abuse report or a support request you made. Machines and snapshots are in Azure East US.
