---
title: Agents
description: The five agents on every machine, how each logs in, and how to let them run without asking.
section: Using repose
order: 15
---

Every machine has five coding agents installed, unmodified:

| Agent       | `--agent`  | Login                                      |
| ----------- | ---------- | ------------------------------------------ |
| Claude Code | `claude`   | Log in once, or a setup token              |
| Codex CLI   | `codex`    | Copied from your laptop                    |
| opencode    | `opencode` | Copied from your laptop                    |
| Gemini CLI  | `gemini`   | `GEMINI_API_KEY` secret, or on the machine |
| pi          | `pi`       | A provider's API key as a secret           |

`repose run -p "prompt"` uses Claude Code. To use another agent for one prompt:

```
repose run --agent codex -p "port the build scripts to bun"
```

All five get new versions with platform updates.

You can set the default agent with `default_agent = "codex"` in `~/.config/repose/config.toml`.  `repose run --agent codex`, without `-p`, changes it for this project.

## Permissions

Claude Code defaults to `bypassPermissions` mode on every machine. Your deny rules still apply.

To start in another mode, set `defaultMode` in `~/.claude/settings.json`, on your laptop (copied to every machine at `run`) or on one machine. 

```json
{
	"permissions": {
		"defaultMode": "default"
	}
}
```

Codex CLI also starts without asking: no approval prompts, and full access to the machine. To start it otherwise, set both `approval_policy` and `sandbox_mode` in `~/.codex/config.toml` on the machine; %%setting only one keeps the machine's value for the other. -- owner: An example of confusing text, does this mean updating that setting in one machine propagates it across all other machines?? %%.

opencode, Gemini CLI and pi keep their own permission prompts unless you configure them. %%An agent waiting on a permission prompt sends a "needs input" notification (Claude Code and opencode) or waits until you attach..%%

%%Commenting out notifcation system shi for now, its too shitty of a feature fpr now, people just gotta use claude /rc%%

## Web search

| Agent       | Web search                                           |
| ----------- | ---------------------------------------------------- |
| Claude Code | Built in.                                            |
| Codex CLI   | Built in, live (below).                              |
| Gemini CLI  | Built in (Google Search).                            |
| opencode    | Off unless you turn it on, below.                    |
| pi          | None built in. It can still fetch pages with `curl`. |

**Codex** searches live, since it runs in `danger-full-access` on the machine. Set `web_search = "cached"` in `~/.codex/config.toml` to search a cache of pages instead, or `"disabled"` to turn it off.

**opencode** offers its `websearch` tool only with its own Zen provider, or when `OPENCODE_ENABLE_EXA` is `1`. %%Set it once as a secret and every opencode on the machine can search, through Exa's public endpoint, with no key:%%

```
repose secrets set OPENCODE_ENABLE_EXA
```

Type `1` as the value. %%Each search sends the query to Exa. Store `EXA_API_KEY` too if you have an Exa account. An opencode that's already running needs a restart to pick it up.%%

%%**OpenCode 2** asks which search provider to use the first time it searches, and a prompt sent with `opencode2 run` can't answer, so the search fails. Name one in `~/.config/opencode/opencode.json` on the machine. TinyFish works with no key:

```json
{
	"websearch": { "provider": "tinyfish" }
}
```

`exa`, `firecrawl`, `parallel` and `tavily` work too, each with its key stored as a secret (`EXA_API_KEY`, `FIRECRAWL_API_KEY`, `PARALLEL_API_KEY`, `TAVILY_API_KEY`).%%

%%
Feels like bloat to me, for now lets save it in some archive but not put it, not sure if users want this info
%%

## OpenCode 2

The machine's `opencode` is version 1, the one opencode's installer still gives by default. OpenCode 2 comes from a separate channel, and you can install it on the machine next to version 1:

```
curl -fsSL https://opencode.ai/v2/install -o /tmp/oc2-install
bash /tmp/oc2-install --no-modify-path
ln -s ~/.opencode/bin/opencode ~/.local/bin/opencode2
```

You run it as `opencode2`. `--no-modify-path` keeps `opencode` pointing at version 1, which `repose run --agent opencode -p "..."` starts. Without it, the installer puts OpenCode 2 first on your `PATH` in `.bashrc`, so a plain `opencode` in a new shell is OpenCode 2, and it starts outside the project's dev environment.

- **Notifications work.** OpenCode 2 loads the same plugin as version 1, `~/.config/opencode/plugins/repose.js`, and sends "finished", "needs input" and "error". If you edit the plugin, run `opencode2 service restart`.
- **Logins.** The first time it runs, OpenCode 2 imports the opencode logins `repose run` copied. After that, run `opencode2 auth login` on the machine, or store the provider's API key as a secret.
- **Its web app.** OpenCode 2 keeps sessions in a background service on the machine's `localhost:49374`. Run `opencode2 pair` on the machine for a one-time link, then open it on your laptop within 5 minutes. If you're attached, the port is already on your laptop; if not, run `repose open 49374`.
- **Updates.** `opencode2 upgrade` updates it, platform updates leave it as it is, and a restore takes it back with the rest of your home directory.

%%
Lets drop support for opencode 1 completely. Going forward we only do opencode 2, so no need for this hoopity poopity thing of opencode 1 vs 2 and here is the diff. COnfusing for absolutely no reason. 
1%%

%%## Let it ask you

Any agent can message you or ask you a question with two commands on the machine: `repose-notify "text"` sends a notification, and `repose-ask --options yes,no "question"` waits for [your answer](/docs/notifications#agents-can-message-you-and-ask-questions) and prints it. The machine's guide for agents says when to use each; a rule in `CLAUDE.md` or `AGENTS.md` overrides it.
%%

%%Again, notifications are shitty rn, the integrations are not deep enough and they cry wolf too many times, for now comment ing anytrhing about notifications out.%%

%%Plus Remote control is really good, why fight anthropic by rolling our own way to communicate with the agent, mfs can also just use t3 code, opencode 2 and delight%%
## Log in

Logins are kept on the machine's disk. They survive stops and are in snapshots.

**Claude Code** gets some special treatment, Anthropic doesn't like you touching (CRUD) Its login credentials so we dont. You do the whole --open your laptop's browser and paste the code -- dance on your first machine  and all other machines are then logged in too, including ones you create later. The login is kept on the host, in a 16 MB space of its own that holds only that file, so it isn't in snapshots and outlasts destroying a project; it's deleted 30 days after your last machine is gone. 

Instead of logging in, you can store a long-lived token from your laptop as a secret. Remote Control, connectors and Claude in Chrome don't work with it.

```
claude setup-token
repose secrets set CLAUDE_CODE_OAUTH_TOKEN
```

Or via API key: `repose secrets set ANTHROPIC_API_KEY`.
**Gemini CLI.** Store `GEMINI_API_KEY` as a secret, or run `gemini` on the machine and log in there.

**pi.** Store your model provider's key, such as `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.

## Your Agent setup is cloned too
%%Lets generalize this sectiuon, okay to drop some detail as long as it is not claude centric%%

`run` and `attach` copy from your laptop's `~/.claude/`: `CLAUDE.md`, `settings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`, `keybindings.json`, and scripts your hooks or status line run. Plugins you enabled are installed on the machine in the background.

Your `settings.json` is merged into the machine's with your settings taking precedence and permission lists are combined. Hooks are combined per event. Keys that tend to hold secrets (`env`, `apiKeyHelper` and the cloud auth helpers) are removed first. Hooks that call commands the machine doesn't have, such as macOS's `afplay`, are left out with a note.

Never copied: your login, conversation history, anything in `~/.claude.json` besides your MCP servers, and anything named like a key or credential.

## MCP servers

Every agent on the machine has the [browser tools](/docs/machine#browser) `playwright` and `chrome-devtools`. In place of Claude in Chrome, `repose browser bridge` lends those tools [access to your laptop's Chrome](/docs/your-chrome).

`run` and `attach` copy the MCP servers you added with `claude mcp add` %%only those added to claude?%% on your laptop to every running agent on the machine. Tokens are left out. `repose secrets import --mcp` sets them from your laptop's values, or set each with `repose secrets set NAME`. %%Until a secret is set, a server that starts with a command stops at its start with a line naming the secret, and an HTTP server is left out of every agent; [`repose mcp list`](/docs/cli#repose-mcp-list-project) shows `needs NAME` for both. An HTTP server arrives the next time an agent starts after you set its secret. Secrets belong to one project, so a server you use in three projects needs its secret set in each. A server that signs in with OAuth needs a sign-in with `/mcp` in Claude Code on the machine while you're attached, since the sign-in page returns to a port on the machine that the attach carries to your laptop. The sign-in is kept with your Claude Code login, so agents on all your machines can use it. Servers that need your laptop stay there, and `run` names them once: Apple apps, a program or files on your laptop, a server on `localhost` or your own network

-- owner: lots of fluff and unnnecesary level of  detail, perhaps cut down or remove, or archive for now?%%. `repose secrets choose --off mcp` stops the copy.

While your laptop is open, [`repose mcp forward NAME`](/docs/cli#repose-mcp-forward-name) lends the agents on the machine the specified MCP server.%% It runs servers that start with a command; an HTTP server on your laptop can't be forwarded yet%%. To forward a server whenever you're attached, add it to `[mcp] forward` in [config.toml](/docs/cli#config-toml).

%%In the section  above, the word server is unclear what it revers to, a traditional HTTP express like server or MCP server. Also what do you mean http server can t be forwarded yet,wold users wwant to forward APIs? Anyways, I commented some styuff out, use judgement%%

%%HTTP servers (Linear, Sentry, Notion, GitHub and the like) and stdio servers that only need `npx` and a token work on the machine. Add one there with the agent's own command, such as `claude mcp add`, or commit it in the repository's `.mcp.json`. A server added that way reaches only that agent, and only Claude Code reads `.mcp.json`. Put a token in by its secret's name, never its value: `${NAME}` in Claude Code, Gemini CLI, pi and `.mcp.json`, `{env:NAME}` in opencode, and in Codex `env_vars = ["NAME"]` for a server that starts with a command or `bearer_token_env_var = "NAME"` for an HTTP one. Then set the secret with `repose secrets set NAME`.%%

%%I commented the section above because it is commiting the sin of becoming the docs for each agent. Users of claude and MCP know how t do MCP, there are specific sources for how to MCP with claude, with t3 code and witrh openvcode, no need to duplicate docs, unless it is repose specific do not document, only say., for example "instead of forwardibng,, configure manually each agent's mcp list and that way they will be confined to eafch machine" or somethin g like that%%

%%Each time an agent starts, repose writes the machine's servers into that agent's own config: `~/.claude.json`, `~/.codex/config.toml`, `~/.config/opencode/config.json` and a Gemini CLI extension named `repose-mcp`. opencode, Gemini CLI and pi get the browser tools from their system configs instead. An entry you edit is yours, and repose leaves it as you left it. A server you add under a name repose uses replaces repose's. opencode merges your `playwright` or `chrome-devtools` with the machine's field by field, so give yours another name there. An entry you delete comes back at the next start, so turn a server off with the agent's own switch:%%

%%
^^^ seems like regerence material for me. too verbose, nobody cares like that
%%

| Agent       | Turn `playwright` off                                                                                       |
| ----------- | ----------------------------------------------------------------------------------------------------------- |
| Claude Code | `/mcp`, then disable it (per project)                                                                       |
| Codex CLI   | `enabled = false` under `[mcp_servers.playwright]` in `~/.codex/config.toml`                                |
| opencode    | `{"mcp": {"playwright": {"enabled": false}}}` in `~/.config/opencode/opencode.json`                         |
| Gemini CLI  | `{"mcp": {"excluded": ["playwright"]}}` in `~/.gemini/settings.json`                                        |
| pi          | `{"mcpServers": {"playwright": {"command": "playwright-mcp", "enabled": false}}}` in `~/.pi/agent/mcp.json` |

If `~/.codex/config.toml` is a symlink, as home-manager makes it, repose adds only `notify = ["repose-hook"]` to it, through the link, and writes no MCP servers there. Codex then gets none of the machine's servers, the browser tools included, until you add their tables yourself. `~/.repose/mcp/agents/codex.json` on the machine lists what repose would have written, in JSON; Codex wants each as a `[mcp_servers.NAME]` table in TOML.

Gemini CLI on the machine doesn't ask whether you trust a folder. Turning `security.folderTrust` on in `~/.gemini/settings.json` turns off MCP servers in every folder you haven't trusted.

%%[`repose mcp list`](/docs/cli#repose-mcp-list-project) shows each server on the machine, where it came from, which agents have it, and what it lacks.
%%
## What agents are told about the machine

repose gives every agent on the machine a short guide to it: that it's a separate machine and can't reach your laptop, that the ports its servers listen on reach your laptop's `localhost`, how to install a missing tool and how to persist it(`repose config add`), Docker and databases, where your secrets are and never to read them, the browser tools, that its commits reach you with `git fetch repose`, where files you drop arrive, how to reach you, the `repose cp` command that copies a file from the machine to your laptop, and the [limits](/docs/limits). It also points the agent at [/llms.txt](https://repose.herakraft.co/llms.txt), these docs as plain markdown, so a `repose` command it suggests to you is checked against them. `run` and `attach` write your CLI's version to `~/.repose/cli-version` on the machine, so an agent can tell when your CLI is older than the docs. To read the guide, on the machine:

%%
^^^
Extremely verbose, doesnt need to be that loing, we can have a reference section or have this in the repo docs instead
%%

```
cat /etc/claude-code/CLAUDE.md
```

%%Claude Code reads it as system-wide memory, Codex from `/etc/codex/config.toml`, opencode from `/etc/opencode/opencode.json`, and Gemini CLI and pi as an extension called `repose-machine-guide`. Your own `CLAUDE.md`, `AGENTS.md` and `GEMINI.md` are never changed, and what they say comes on top of the guide. The guide changes only with a platform update.
%%

%%
^^^
Implementation details not relevant to the end user
%%