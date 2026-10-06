---
title: Agents
description: The five agents on every machine, how each logs in, and how to let them run without asking.
section: Using repose
order: 15
---

Every machine has five coding agents installed, unmodified:

| Agent       | `--agent`  | Login                                      | Notifies you          |
| ----------- | ---------- | ------------------------------------------ | --------------------- |
| Claude Code | `claude`   | Log in once, or a setup token              | Finished, needs input |
| Codex CLI   | `codex`    | Copied from your laptop                    | Finished              |
| opencode    | `opencode` | Copied from your laptop                    | Finished, needs input |
| Gemini CLI  | `gemini`   | `GEMINI_API_KEY` secret, or on the machine | When it goes idle     |
| pi          | `pi`       | A provider's API key as a secret           | When it goes idle     |

`repose run "prompt"` uses Claude Code. To use another agent for one prompt:

```
repose run --agent codex "port the build scripts to bun"
```

To change the default for projects you create from now on, set `default_agent = "codex"` in `~/.config/repose/config.toml`. You can also start any agent by hand in a tmux window. However it starts, an agent runs in the project's dev environment: its `.envrc`, or its flake's dev shell ([Projects with a flake.nix](/docs/machine#projects-with-a-flake-nix)).

## Let it run without asking

Claude Code starts in `bypassPermissions` mode on every machine: it runs commands and edits files without asking. Your deny rules still apply, and removing a critical path such as a home directory still asks.

To start in another mode, set `defaultMode` in `~/.claude/settings.json`, on your laptop (copied to every machine at `run`) or on one machine. Your value is kept.

```json
{
	"permissions": {
		"defaultMode": "default"
	}
}
```

`default` asks before edits and commands, `acceptEdits` asks before most commands but not edits, `plan` plans first. `Shift-Tab` switches modes inside a session. Machines in bypass mode don't show Claude Code's one-time offer to switch to auto mode; to use auto mode, press `Shift-Tab` or set `defaultMode` to `auto`.

Claude Code also starts with its fullscreen renderer, which fills the tmux window and scrolls inside itself. To draw inline instead, set `"tui": "default"` in `~/.claude/settings.json` on your laptop or the machine, or run `/tui default` in Claude Code. Your value is kept.

The other agents ask as they normally do unless you configure them. An agent waiting on a permission prompt sends a "needs input" notification (Claude Code and opencode) or waits until you attach.

**Codex CLI:** in `~/.codex/config.toml` on the machine:

```toml
approval_policy = "never"
sandbox_mode = "danger-full-access"
```

## Web search

| Agent       | Web search                                           |
| ----------- | ---------------------------------------------------- |
| Claude Code | Built in.                                            |
| Codex CLI   | Built in, from a cache unless set otherwise (below). |
| Gemini CLI  | Built in (Google Search).                            |
| opencode    | Off unless you turn it on, below.                    |
| pi          | None built in. It can still fetch pages with `curl`. |

**Codex** searches a cache of pages by default, and live in `danger-full-access`. Set `web_search = "live"` in `~/.codex/config.toml` for live results, or `"disabled"` to turn it off.

**opencode** offers its `websearch` tool only with its own Zen provider, or when `OPENCODE_ENABLE_EXA` is `1`. Set it once as a secret and every opencode on the machine can search, through Exa's public endpoint, with no key:

```
repose secrets set OPENCODE_ENABLE_EXA
```

Type `1` as the value. Each search sends the query to Exa. Store `EXA_API_KEY` too if you have an Exa account. An opencode that's already running needs a restart to pick it up.

**OpenCode 2** asks which search provider to use the first time it searches, and a prompt sent with `opencode2 run` can't answer, so the search fails. Name one in `~/.config/opencode/opencode.json` on the machine. TinyFish works with no key:

```json
{
	"websearch": { "provider": "tinyfish" }
}
```

`exa`, `firecrawl`, `parallel` and `tavily` work too, each with its key stored as a secret (`EXA_API_KEY`, `FIRECRAWL_API_KEY`, `PARALLEL_API_KEY`, `TAVILY_API_KEY`).

## OpenCode 2

The machine's `opencode` is version 1, the one opencode's installer still gives by default. OpenCode 2 comes from a separate channel, and you can install it on the machine next to version 1:

```
curl -fsSL https://opencode.ai/v2/install -o /tmp/oc2-install
bash /tmp/oc2-install --no-modify-path
ln -s ~/.opencode/bin/opencode ~/.local/bin/opencode2
```

You run it as `opencode2`. `--no-modify-path` keeps `opencode` pointing at version 1, which `repose run --agent opencode` starts. Without it, the installer puts OpenCode 2 first on your `PATH` in `.bashrc`, so a plain `opencode` in a new shell is OpenCode 2, and it starts outside the project's dev environment.

- **Notifications work.** OpenCode 2 loads the same plugin as version 1, `~/.config/opencode/plugins/repose.js`, and sends "finished", "needs input" and "error". If you edit the plugin, run `opencode2 service restart`.
- **Logins.** The first time it runs, OpenCode 2 imports the opencode logins `repose run` copied. After that, run `opencode2 auth login` on the machine, or store the provider's API key as a secret.
- **Its web app.** OpenCode 2 keeps sessions in a background service on the machine's `localhost:49374`. Run `opencode2 pair` on the machine for a one-time link, then open it on your laptop within 5 minutes. If you're attached, the port is already on your laptop; if not, run `repose open 49374`.
- **Updates.** `opencode2 upgrade` updates it, platform updates leave it as it is, and a restore takes it back with the rest of your home directory.

## Let it ask you

Any agent can message you or ask you a question with two commands on the machine: `repose-notify "text"` sends a notification, and `repose-ask --options yes,no "question"` waits for your answer and prints it. Add a line to the agent's instructions (`CLAUDE.md`, `AGENTS.md`) telling it to use them. [Notifications](/docs/notifications#agents-can-message-you-and-ask-questions) has where you answer and the options.

## Log in

Logins are kept on the machine's disk, except Claude Code's (below). They survive stops and are in snapshots.

**Claude Code.** Its login is never copied from your laptop, so log in once on any of your machines: type `claude`, open the URL on your laptop, approve, paste the code back. Your other machines are then logged in too, including ones you create later. The login is kept on the host, in a 16 MB space of its own that holds only that file, so it isn't in snapshots and outlasts destroying a project; it's deleted 30 days after your last machine is gone. If you send a prompt before logging in, the CLI opens the Claude window for the login and asks you to run the prompt again after. A subscription login keeps Remote Control, so you can follow the session in the Claude app.

Instead of logging in, you can store a long-lived token from your laptop as a secret. Remote Control, connectors and Claude in Chrome don't work with it.

```
claude setup-token
repose secrets set CLAUDE_CODE_OAUTH_TOKEN
```

An API key works the same way: `repose secrets set ANTHROPIC_API_KEY`.

**Codex and opencode.** Your laptop's login is copied at each `run`. A login you made on the machine is never overwritten by an older one. For opencode that's `~/.local/share/opencode/auth.json`, which OpenCode 2 reads once and then stops updating, so a login you make in OpenCode 2 on your laptop doesn't reach the machine ([OpenCode 2](#opencode-2)).

**Gemini CLI.** Store `GEMINI_API_KEY` as a secret, or run `gemini` on the machine and log in there.

**pi.** Store your model provider's key, such as `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.

## Your Claude Code setup comes along

`run` and `attach` copy from your laptop's `~/.claude/`: `CLAUDE.md`, `settings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`, `keybindings.json`, and scripts your hooks or status line run. Plugins you enabled are installed on the machine in the background.

Your `settings.json` is merged into the machine's: your keys win, and permission lists are combined, so answers you gave on the machine are kept. Keys that tend to hold secrets (`env`, `apiKeyHelper` and the cloud auth helpers) are removed first. Hooks that call commands the machine doesn't have, such as macOS's `afplay`, are left out with a note.

Never copied: your login, conversation history, anything in `~/.claude.json` besides your MCP servers, and anything named like a key or credential.

## MCP servers

Every agent on the machine has the browser tools `playwright` and `chrome-devtools` ([Browser](/docs/machine#browser)). In place of Claude in Chrome, `repose browser bridge` lends those tools your laptop's Chrome, logins included; see [Lend the agents your Chrome](/docs/your-chrome).

`run` and `attach` copy the MCP servers you added with `claude mcp add` on your laptop, at user scope and for this project, to every agent on the machine. Tokens stay on your laptop: each becomes `${NAME}`, and `run` names the secrets the machine lacks. `repose secrets import --mcp` sets them from your laptop's values, or set each with `repose secrets set NAME`. Secrets belong to one project, so a server you use in three projects needs its secret set in each. A server that signs in with OAuth needs a sign-in with `/mcp` in Claude Code on the machine. Servers that need your laptop stay there, and `run` names them once: Apple apps, a program or files on your laptop, a server on `localhost` or your own network. `repose secrets choose --off mcp` stops the copy.

While your laptop is open, [`repose mcp forward NAME`](/docs/cli#repose-mcp-forward-name) lends the agents on the machine one of the servers that stay there. It runs on your laptop, as your Claude Code, Claude Desktop, Codex or Gemini CLI config defines it, until `Ctrl-C`, and each agent session on the machine gets its own copy. Your terminal shows each call by agent and tool. To forward a server whenever you're attached, add it to `[mcp] forward` in [config.toml](/docs/cli#config-toml). The agents keep listing a server you forwarded once; while nothing forwards it, its tools answer that your laptop isn't connected. Agents already running when you first forward a server see it after a restart. `repose mcp forward --remove NAME` takes it off the machine.

HTTP servers (Linear, Sentry, Notion, GitHub and the like) and stdio servers that only need `npx` and a token work on the machine. Add one there with the agent's own command, such as `claude mcp add`, or commit it in the repository's `.mcp.json`.

Each time an agent starts, repose writes the machine's servers into that agent's own config: `~/.claude.json`, `~/.codex/config.toml`, `~/.config/opencode/config.json` and a Gemini CLI extension named `repose-mcp`. opencode, Gemini CLI and pi get the browser tools from their system configs instead. An entry you edit is yours, and repose leaves it as you left it. A server you add under a name repose uses replaces repose's, except in opencode, which merges the two field by field; give yours another name there. An entry you delete comes back at the next start, so turn a server off with the agent's own switch:

| Agent       | Turn `playwright` off                                                                                       |
| ----------- | ----------------------------------------------------------------------------------------------------------- |
| Claude Code | `/mcp`, then disable it (per project)                                                                       |
| Codex CLI   | `enabled = false` under `[mcp_servers.playwright]` in `~/.codex/config.toml`                                |
| opencode    | `{"mcp": {"playwright": {"enabled": false}}}` in `~/.config/opencode/opencode.json`                         |
| Gemini CLI  | `{"mcp": {"excluded": ["playwright"]}}` in `~/.gemini/settings.json`                                        |
| pi          | `{"mcpServers": {"playwright": {"command": "playwright-mcp", "enabled": false}}}` in `~/.pi/agent/mcp.json` |

When `~/.codex/config.toml` is a symlink, as home-manager makes it, repose leaves it alone, and Codex gets none of the machine's servers until you add their tables there yourself. `~/.repose/mcp/agents/codex.json` on the machine lists what repose would have written.

[`repose mcp list`](/docs/cli#repose-mcp-list-project) shows each server on the machine, where it came from, which agents have it, and what it lacks.

## What agents are told about the machine

Every agent on the machine is given a short guide to it: that it's a separate machine and can't reach your laptop, that the ports its servers listen on reach your laptop's `localhost`, how to install a missing tool and how you keep it (`repose config add`), Docker and databases, where your secrets are and never to print them, the browser tools, that its commits reach you with `git fetch repose`, where files you drop arrive, how to reach you, and the [limits](/docs/limits). It also points the agent at [/llms.txt](https://repose.herakraft.co/llms.txt), these docs as plain markdown, so a `repose` command it suggests to you is checked against them. `run` and `attach` write your CLI's version to `~/.repose/cli-version` on the machine, so an agent can tell when your CLI is older than the docs. To read the guide, on the machine:

```
cat /etc/claude-code/CLAUDE.md
```

Claude Code reads it as system-wide memory, Codex from `/etc/codex/config.toml`, opencode from `/etc/opencode/opencode.json`, and Gemini CLI and pi as an extension called `repose-machine-guide`. Your own `CLAUDE.md`, `AGENTS.md` and `GEMINI.md` are never changed, and what they say comes on top of the guide. The guide changes only with a platform update.
