---
title: Agents
description: The five agents on every machine, how each logs in, and how to let them run without asking.
section: Using repose
order: 15
---

Every machine has five coding agents installed, unmodified:

| Agent       | `--agent`  | Login                                      | States                     |
| ----------- | ---------- | ------------------------------------------ | -------------------------- |
| Claude Code | `claude`   | Log in once, or a setup token              | working, needs input, idle |
| Codex CLI   | `codex`    | Copied from your laptop                    | working, idle              |
| opencode    | `opencode` | Copied from your laptop                    | working, needs input, idle |
| Gemini CLI  | `gemini`   | `GEMINI_API_KEY` secret, or on the machine | working, idle              |
| pi          | `pi`       | A provider's API key as a secret           | working, idle              |

`repose run -p "prompt"` uses Claude Code. To use another agent for one prompt:

```
repose run --agent codex -p "port the build scripts to bun"
```

All five get new versions with platform updates.

To change the default for projects you create from now on, set `default_agent = "codex"` in `~/.config/repose/config.toml`. `repose run --agent codex`, without `-p`, changes it for this project. An agent gets the checkout's [dev environment](/docs/machine#projects-with-a-flake-nix) however it was started.

## Permissions

Claude Code starts in `bypassPermissions` mode on every machine: it runs commands and edits files without asking. Your deny rules still apply, and removing a critical path such as a home directory still asks.

To start in another mode, set `defaultMode` in `~/.claude/settings.json`, on your laptop (copied to every machine at `run`) or on one machine. Your value is kept.

```json
{
	"permissions": {
		"defaultMode": "default"
	}
}
```

`Shift-Tab` switches modes inside a session. A machine in bypass mode doesn't show Claude Code's one-time offer to make auto mode your default; `Shift-Tab` or `defaultMode` set to `auto` still gets you there.

Claude Code also starts with its fullscreen renderer, which fills the tmux window and scrolls inside itself. Set `"tui": "default"` in `~/.claude/settings.json`, on your laptop or the machine, to draw it inline, or run `/tui default` in Claude Code.

Codex CLI also starts without asking: `approval_policy = "never"` and `sandbox_mode = "danger-full-access"`, from the machine's system config. To start it otherwise, set those keys in `~/.codex/config.toml` on the machine. That file wins over the system config one key at a time, so setting only `approval_policy` leaves the sandbox as the machine set it. The file belongs to that one machine; your other machines keep what they had, and a fork gets a copy.

opencode, Gemini CLI and pi keep their own permission prompts unless you configure them. An agent waiting on a permission prompt sends a "needs input" notification (Claude Code and opencode) or waits until you attach.

## Web search

| Agent       | Web search                                           |
| ----------- | ---------------------------------------------------- |
| Claude Code | Built in.                                            |
| Codex CLI   | Built in, live (below).                              |
| Gemini CLI  | Built in (Google Search).                            |
| opencode    | Off unless you turn it on, below.                    |
| pi          | None built in. It can still fetch pages with `curl`. |

**Codex** searches live, since it runs in `danger-full-access` on the machine. Set `web_search = "cached"` in `~/.codex/config.toml` to search a cache of pages instead, or `"disabled"` to turn it off.

**opencode** offers its `websearch` tool only with its own Zen provider, or when `OPENCODE_ENABLE_EXA` is `1`. Set it once as a secret and every opencode on the machine can search, through Exa's public endpoint, with no key of yours:

```
repose secrets set OPENCODE_ENABLE_EXA
```

Type `1` as the value. Each search sends your query to Exa. An opencode that's already running needs a restart to pick it up.

## OpenCode 2

The machine's `opencode` is version 1, the one opencode's installer still gives by default. OpenCode 2 comes from a separate channel, and you can install it on the machine next to version 1:

```
curl -fsSL https://opencode.ai/v2/install -o /tmp/oc2-install
bash /tmp/oc2-install --no-modify-path
ln -s ~/.opencode/bin/opencode ~/.local/bin/opencode2
```

You run it as `opencode2`. `--no-modify-path` keeps `opencode` pointing at version 1, which `repose run --agent opencode -p "..."` starts; without it a plain `opencode` in a new shell is OpenCode 2, started outside the project's dev environment. `opencode2 upgrade` updates it, a platform update leaves it as it is, and a restore brings it back with the rest of your home directory. Its notifications come from the same plugin as version 1, `~/.config/opencode/plugins/repose.js`; run `opencode2 service restart` if you edit it.

## Agent states

`repose ls`, `repose ps` and the dashboard show each agent's state as its hooks report it, within about 10 seconds:

- `working`: from your prompt to the end of the turn, including a pause while a background task it started runs. After 2 minutes with no output in its window it shows `idle`, which is also how a turn you interrupted ends.
- `needs input`: waiting for your permission (Claude Code and opencode).
- `idle`: at its prompt.
- `running`: the agent's process is alive and no hook says more. Gemini CLI, pi and opencode show it until their first prompt. OpenCode 2 shows it throughout, since it doesn't report prompts, and so do Gemini CLI and pi if you replaced the machine's extension for them.

Gemini CLI and pi report through the machine's extension for them, `repose-machine-guide`.

A hookless agent's turn ends in `repose events` as `finished` with the summary `gemini went idle`, once its window has printed nothing for about 90 seconds. On a [herdr project](/docs/run-and-attach#herdr-instead-of-tmux) herdr reports the turn end and the state instead, and a permission prompt it reports sets `needs input` with no notification.

A window with another name counts as an agent's while the agent is its foreground program, `claude` typed in the `shell` window for example; Gemini CLI counts only in a window named `gemini`, because it runs as `node`.

## Let it ask you

Any agent can message you or ask you a question from the machine with `repose-notify`, `repose-notify --done` and `repose-ask` ([Agents can message you and ask questions](/docs/notifications#agents-can-message-you-and-ask-questions)). The machine's guide for agents says when to use each, and a rule in `CLAUDE.md` or `AGENTS.md` overrides it.

## Log in

Logins are kept on the machine's disk, except Claude Code's. They survive stops and are in snapshots.

**Claude Code.** Its login is never copied from your laptop, so log in once on any of your machines: type `claude`, approve in the page that opens in your laptop's browser, and paste the code back. Your other machines on the same host are then logged in too, including ones you create later; machines on another host need their own login. The file Claude Code writes lives on the host, shared into each of your machines, and repose never opens it. It isn't in snapshots, it outlasts destroying a project, and it's deleted 30 days after your last machine is gone.

Instead of logging in, you can store a long-lived token from your laptop as a secret. Remote Control, connectors and Claude in Chrome then don't work.

```
claude setup-token
repose secrets set CLAUDE_CODE_OAUTH_TOKEN
```

An API key works the same way: `repose secrets set ANTHROPIC_API_KEY`.

**Codex and opencode.** Your laptop's login is [copied at each `run`](/docs/secrets#logins-copied-from-your-laptop).

**Gemini CLI.** Store `GEMINI_API_KEY` as a secret, or run `gemini` on the machine and log in there.

**pi.** Store your model provider's key, such as `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.

## Your agent setup comes along

`run` and `attach` copy from your laptop's `~/.claude/`: `CLAUDE.md`, `settings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`, `keybindings.json`, and scripts your hooks or status line run. Plugins you enabled are installed on the machine in the background. The other four get your MCP servers, and Codex and opencode your login; nothing from their own config directories.

Your `settings.json` is merged into the machine's: your keys win, and permission lists are combined, so answers you gave on the machine are kept. Keys that hold a credential or run one (`env`, `apiKeyHelper` and the cloud auth helpers) are removed first. Hooks are combined per event, so a hook set up on the machine, such as herdr's `SessionStart` hook, stays next to yours, and a hook you delete on your laptop stays on the machine; one that calls a command the machine doesn't have, such as macOS's `afplay`, is left out with a note.

Never copied: your login, conversation history, anything in `~/.claude.json` besides your MCP servers, and anything named like a key or credential.

## MCP servers

Every agent on the machine has the [browser tools](/docs/machine#browser) `playwright` and `chrome-devtools`. In place of Claude in Chrome, `repose browser bridge` lends those tools [your laptop's Chrome](/docs/your-chrome), logins included.

`run` and `attach` copy the MCP servers in your laptop's Claude Code config, `~/.claude.json`: the ones at user scope and the ones you added for this checkout. They reach all five agents on the machine. A server you added to another agent on your laptop stays there. `repose secrets choose --off mcp` stops the copy.

Tokens stay on your laptop. Each becomes `${NAME}`, and `run` names the secrets the machine lacks. `repose secrets import --mcp` sets them from your laptop's values, or `repose secrets set NAME` sets one. A secret set with `--global` reaches [every project](/docs/secrets#secrets-for-every-project), so `repose secrets import --global --mcp` sets the tokens of your user-scope servers once for all of them. [`repose mcp list`](/docs/cli#repose-mcp-list-project) shows `needs NAME` for a server still waiting on one. A server that starts with a command reads its secrets each time an agent starts it; one that reaches a URL is left out of every agent until its secrets are set. An agent that's already running sees a new server, or a secret you just set, after you restart it.

A server that signs in with OAuth needs a sign-in from `/mcp` in Claude Code on the machine while you're attached, since the sign-in page returns to a port on the machine that the attach carries to your laptop. The sign-in is kept with your Claude Code login, so agents on your machines on the same host can use it.

Servers that need your laptop stay there, and `run` names them once: Apple apps, a program or files on your laptop, a server on `localhost` or your own network. While your laptop is open, [`repose mcp forward NAME`](/docs/cli#repose-mcp-forward-name) lends the agents on the machine one of them, running on your laptop until `Ctrl-C`. To forward a server whenever you're attached, add it to `[mcp] forward` in [config.toml](/docs/cli#config-toml).

Add a server on the machine with the agent's own command and it reaches only that agent. An entry repose wrote comes back at the next agent start if you delete it, so turn it off with whatever switch that agent has for disabling a server. If `~/.codex/config.toml` is a symlink, as home-manager makes it, repose writes no servers there, so Codex gets none of the machine's, the browser tools included; `~/.repose/mcp/agents/codex.json` on the machine lists what it would have written.

Gemini CLI on the machine doesn't ask whether you trust a folder. Turning `security.folderTrust` on in `~/.gemini/settings.json` turns off MCP servers in every folder you haven't trusted.

## What agents are told about the machine

repose gives every agent on the machine a short guide to it: that the machine is its own and can't reach your laptop, how to install a missing tool and keep it (`repose config add`), where your secrets are and never to print them, and how to reach you. It also points the agent at [/llms.txt](https://repose.herakraft.co/llms.txt), these docs as plain markdown, so a `repose` command it suggests to you is checked against them. Your own `CLAUDE.md`, `AGENTS.md` and `GEMINI.md` are never changed, and what they say comes on top of the guide, which changes only with a platform update. To read the guide, on the machine:

```
cat /etc/claude-code/CLAUDE.md
```
