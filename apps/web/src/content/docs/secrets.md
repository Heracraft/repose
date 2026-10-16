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
Set STRIPE_SECRET_KEY on todo-app; the running machine has it now.
```

Pipe the value in instead, or read it from a file or your laptop's environment:

```
op read op://dev/stripe/secret-key \
  | repose secrets set STRIPE_SECRET_KEY
repose secrets set GOOGLE_CREDENTIALS --from-file ./sa.json
repose secrets set OPENAI_API_KEY --from-env
```

`repose secrets set NAME=VALUE` is [refused](/docs/cli#secrets): the value would stay in your shell history.

[`repose secrets import`](/docs/cli#secrets) sets one for every `NAME=VALUE` in a `.env` file:

```
$ repose secrets import .env.local
Set 3 on todo-app from .env.local (pushed to the running machine):
DATABASE_URL (replaced), STRIPE_SECRET_KEY, OPENAI_API_KEY
```

Only names are printed, never values. The file is read the way docker compose and the dotenv libraries read it. `#` comments and blank lines are skipped, and `export ` in front of a name is ignored. `'single quotes'` keep a value exactly as written; `"double quotes"` understand `\n`, `\r`, `\t`, `\"`, `\\`, `\$` and `` \` ``. A quoted value can span lines (a PEM key, say), and an unquoted value ends where a space or tab is followed by `#`. `${VAR}` is not expanded. A name that isn't a valid secret name (below) stops the whole import before anything is sent, and the error lists the lines to fix.

In a server you add on the machine yourself, write the token by its secret's name, never its value ([MCP servers](/docs/agents#mcp-servers)).

On the machine, each secret is an environment variable and a file at `/run/repose/secrets/NAME`. Both are kept in memory only: never on the machine's disk, never in snapshots. The one exception is [a restart of the server it runs on](/docs/lifecycle#when-a-machines-server-restarts), which keeps the machine's memory, secrets included, as an encrypted image there for at most 6 hours.

A change, a removal included, reaches a running machine within seconds, and a stopped one at its next start. Every command an agent runs after that sees it, and so does every new shell, tmux window and herdr tab. A program already running keeps the old value until you restart it: a dev server, or a shell you have open (`exec $SHELL`). The refresh comes from bash, so a command an agent runs with `sh` (`sh -c`, a `#!/bin/sh` script) keeps the values the agent started with.

A value you set yourself wins over a secret of the same name and stays when secrets change later: one in the project's `.envrc`, one you export in a shell, or one you give a single command (`STRIPE_KEY=sk_test ./run-tests.sh`). The machine tells your value from its own by comparing it with the values it delivered for that name, the last 16 of them. A value equal to one of those follows the secret, so an `.envrc` that loads the same `.env` you imported gets your later changes. To keep a secret out of one command, give that variable an empty value.

`BASH_ENV` points every bash at `/etc/repose/bash-env.sh`, which refreshes the secrets; set your own and commands under it stop picking up changes. `REPOSE_ENV_GEN` records which version of your secrets a process holds. Neither can be a secret's name, and nor can `ENV`.

So that a program still holding a removed or replaced value loses it, the machine keeps that value in memory, readable by `dev`, until the machine stops or reboots (for a secret you keep changing, until 16 newer values replace it). If a key leaked, revoke it with its provider: removing the secret here takes it out of the machine's commands but leaves it valid.

```
repose secrets list
repose secrets rm STRIPE_SECRET_KEY
```

Nothing shows a value again after you set it. Names are uppercase letters, digits and underscores, start with a letter and are up to 64 characters, other than `BASH_ENV`, `ENV` and `REPOSE_ENV_GEN`; values up to 64 KB.

The dashboard's project **Secrets** page does the same: type the value, or take it from a file with **Load from file**.

## Secrets for every project

```
$ repose secrets set --global COOLIFY_BASE_URL
Value for COOLIFY_BASE_URL: ****************************
Set COOLIFY_BASE_URL for every project; 1 running machine has it now.
```

With `--global`, the secret is your account's. Every project gets it, including the ones you create later. A project's own secret of the same name wins, and `set --global` names the projects that keep theirs. Remove a project's own and it gets your account's.

```
repose secrets list --global
repose secrets rm --global COOLIFY_BASE_URL
repose secrets import --global ~/.config/shared.env
```

The dashboard keeps these on **Settings**, and each project's **Secrets** page lists them, striking out any the project has itself.

## Logins copied from your laptop

At each `repose run`, these are copied straight to the machine over SSH if you have them. repose never stores them. `Logins copied: gh, codex` prints when a project's copied logins change.

| Tool       | File                                                                             |
| ---------- | -------------------------------------------------------------------------------- |
| GitHub CLI | `~/.config/gh/hosts.yml` (with the token, even when gh keeps it in your keyring) |
| Codex CLI  | `~/.codex/auth.json`                                                             |
| opencode   | `~/.local/share/opencode/auth.json`                                              |

With the `gh` login copied, git on the machine sends every GitHub URL over HTTPS with that login, `git@github.com:owner/repo` and `ssh://git@github.com/owner/repo` included. A `gh auth login` made on the machine covers HTTPS URLs only; the SSH rewrite comes with a copied login. A copy on the machine newer than your laptop's is kept, so the login you made there survives the next run.

Never copied: SSH private keys, [Claude Code's login, Gemini's OAuth login](/docs/agents#log-in), the Vercel CLI's login.

The Vercel CLI's login stays on your laptop because it reaches your whole Vercel account: every team and every project. An agent on the machine could deploy, delete a project or read another project's environment variables with it. To use Vercel on the machine, log in there:

```
vercel login
```

That login is on the machine's disk and in its [snapshots](/docs/lifecycle#snapshots). Or create a token in Vercel's account settings, scoped to one team and with an expiry, and store it under the name the Vercel CLI reads:

```
repose secrets set VERCEL_TOKEN
```

repose used to copy this login. The next `repose run` removes a copy an earlier run left, and says so. It leaves alone a login you made on the machine, or a copy the Vercel CLI has rewritten since; `rm ~/.local/share/com.vercel.cli/auth.json` on the machine removes it. Snapshots taken before then still hold the copy, so revoke that login's token in Vercel's settings and run `vercel login` on your laptop again.

### Choose what is copied

The logins above are copied until you say otherwise, and so are your gitignored `.env` files ([Sync](/docs/sync)) and your Claude Code MCP servers ([Agents](/docs/agents#mcp-servers)). Keep any of them on your laptop with `repose secrets choose`:

```
$ repose secrets choose
Copied at each repose run, for every project:
space toggles, enter saves, q leaves

> [x] gh        GitHub CLI login: every repo you can reach
  [x] codex     Codex CLI login
  [ ] opencode  opencode login (not logged in on this laptop)
  [x] env       gitignored .env files (2 in this checkout)
  [x] mcp       Claude Code MCP servers, without their tokens
```

Or name them, which also works in scripts:

```
repose secrets choose --off gh env
repose secrets choose --on env
```

The list is saved in `~/.config/repose/config.toml` on your laptop and covers every project; `--project NAME` gives one project its own ([config.toml](/docs/cli#config-toml)). repose never sees it.

The next `repose run` after you turn one off removes the copy an earlier run left on the machine, as long as it is still the same as your laptop's. A `.env` file an agent changed there is left alone, and `run` names it. With `mcp` off, the next `run` or `attach` takes the servers it copied off the machine's agents; a server you added on the machine stays.

Without the `gh` login, git on the machine has no way to push to GitHub. Run `gh auth login` there, or use a [token](#other-git-hosts).

### Other git hosts

GitLab, Bitbucket and your own server have no login that repose copies.

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

**A deploy key made on the machine.**

```
ssh-keygen -t ed25519 -N '' -C todo-app.repose \
  -f ~/.ssh/id_ed25519
cat ~/.ssh/id_ed25519.pub
```

Add the public key to that one repository as a deploy key with write access. It can push to that repository and nothing else. The private key is on the machine's disk and in its snapshots; delete the deploy key on the git host when you destroy the project.

## Git and Claude Code settings

Your global git settings are copied, and your global gitignore to `~/.config/git/ignore`, which git reads by default. A setting you make on the machine wins over the copy. Left on your laptop: credential helpers, signing keys, URL rewrites, proxies, `core.sshCommand`, `core.hooksPath`, diff and merge tools, anything that looks like a token, and any setting naming a program or path the machine doesn't have. The name and email on the machine's commits are the ones git resolves in your checkout, so a per-directory `includeIf`, or an identity the repository sets itself, is what arrives.

Commits made on the machine are unsigned, since the signing key stays on your laptop. Where neither sets a value, git on the machine starts a new repository on `main`, merges on `git pull`, and sets the upstream on a branch's first `git push`.

Your Claude Code setup is copied too, with the keys that hold credentials taken out of `settings.json` first ([what travels](/docs/agents#your-agent-setup-comes-along)).

## What an agent on the machine can reach

- **Everything on the machine.** Your checkout, your `.env` files, your secrets and the logins in the table above. It runs as `dev`, with [`sudo`](/docs/machine).
- **The internet**, outbound, within the [network limits](/docs/limits#network).
- **Your git host**, with whatever credentials the machine has.
- **Not your other projects.** Each is a separate machine, and the network stops them from reaching each other. Your [Claude Code login](/docs/agents#log-in) and your [account's secrets](#secrets-for-every-project) are the exceptions; a hook or a setting an agent writes stays on its machine.
- **Not your laptop.** Nothing on the machine can open a connection to it. A tunnel you open is the exception: a [Chrome bridge](/docs/your-chrome#what-the-agents-can-and-cant-do-in-your-chrome) hands anything on the machine your logged-in Chrome, and [`repose mcp forward`](/docs/agents#mcp-servers) runs a server on your laptop for the agents to call.

## What repose stores

- Your account (GitHub login, email), and each project's name, git remote, size, state, configuration and build logs.
- Named secrets, encrypted with a key for your account that is itself protected by a key in Azure Key Vault.
- Once a minute, for billing and abuse detection: whether the machine is running, CPU, memory, network and disk use, and the names of running processes. Never a process's arguments or environment. The same sample carries which agents are running, in which window, and each one's [state](/docs/agents#agent-states).
- Agent events: which agent, what kind of event, and the summary the agent wrote. The messages and questions [agents send you](/docs/notifications#agents-can-message-you-and-ask-questions) are stored with your answers.

Never stored ([full list](/privacy)): your code, prompts, terminal contents, or files on the machine.

Disks aren't encrypted per project yet, so an operator with root on a server can read the disks on it. Every operator login and command inside a machine is audited, and operators look inside only for an incident, an abuse report or a support request you made. Machines and snapshots are in Azure East US.
