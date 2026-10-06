# Agents

Five coding agents ship in every guest: Claude Code, opencode, Codex CLI,
Gemini CLI, and pi. The platform wraps each so it reports to the notification
system and lands in the right tmux window; otherwise they are the upstream
binaries, unmodified.

## What the user sees

```
$ repose run "write tests for the payment module"
...
Starting claude...          (then attaches to the tmux window todo-app:claude)
```

First use of Claude in a guest with no credentials:

```
$ repose run "write tests for the payment module"
Claude Code is not logged in on this guest yet. Finish the login in the window that opens, then re-run with your prompt.
```

Setting the headless fallback:

```
$ claude setup-token                       # on the laptop
$ repose secrets set CLAUDE_CODE_OAUTH_TOKEN
Value for CLAUDE_CODE_OAUTH_TOKEN: ****************************************
Set CLAUDE_CODE_OAUTH_TOKEN (pushed to running guest)
```

Picking a default for projects created from now on, in
`~/.config/repose/config.toml` on the laptop:

```toml
default_agent = "codex"
```

A project keeps the default it was created with; `repose run --agent NAME`
picks another for one prompt.

## The five agents

| Agent | Binary | Window | How it reports | Login in guest |
|---|---|---|---|---|
| Claude Code | `claude` | `claude` | `Notification` and `Stop` hooks in `~/.claude/settings.json` run `repose-hook`: finished, needs input | `claude` prints a paste code over SSH; or `CLAUDE_CODE_OAUTH_TOKEN` named secret |
| opencode | `opencode` | `opencode` | plugin `~/.config/opencode/plugins/repose.js` (version 1 and OpenCode 2, I-481) sends finished (`session.idle`; V2 `session.execution.succeeded`), needs input (`permission.asked`), error (`session.error`; V2 `session.execution.failed`) | `~/.local/share/opencode/auth.json` synced from the laptop |
| Codex CLI | `codex` | `codex` | `notify = ["repose-hook"]` in `~/.codex/config.toml`: finished only | `~/.codex/auth.json` synced from the laptop |
| Gemini CLI | `gemini` | `gemini` | no hook; tmux pane-idle heuristic | `GEMINI_API_KEY` named secret, or log in on the machine |
| pi | `pi` | `pi` | no hook; tmux pane-idle heuristic | provider API key as a named secret |

`repose-agent-setup` (`nix/overlay/agents/agent-setup.nix`) writes these
entries each time the agent starts. It adds its entry only when none running
`repose-hook` is there and never removes the user's. Claude Code also starts
in `bypassPermissions` mode (below, DECISIONS I-250). The
payload each agent actually sends is recorded as a fixture under
`internal/guestd/hooks/testdata/<agent>/`, one file per shape, so a change in
an agent's payload shows up as a failing test rather than as a notification
that stops arriving. The two agents with no hook have a README there instead,
naming the heuristic test that covers them. Where an
agent has no completion hook, guestd watches the pane: an agent window whose
pane has produced no output for 90 seconds and whose foreground process is
the agent itself is reported `idle`, which becomes a `completed` event once,
until the pane produces output again. That heuristic is named as such in the
notification (`claude finished` versus `gemini went idle`), so the user
knows what they are being told.

## Wrappers

Each binary is wrapped by `nix/overlay/agents/wrap.nix` to:

1. Run `repose-agent-setup`, which writes its hook configuration so completion and needs-input
   events go to `repose-hook`, which POSTs to `/run/repose/hooks.sock`.
   The patch is idempotent and preserves the user's other hooks.
2. Export `TERM=tmux-256color` and `COLORTERM=truecolor` so the TUIs render.
3. Exec the real binary with all arguments.

`repose-hook` always exits 0. A hook that fails must never block an agent,
because a blocked agent is a silently wasted night.

## Claude Code starts without permission prompts (DECISIONS I-250)

The guest's baseline `~/.claude/settings.json` (from
`/etc/repose/claude-settings.json`) carries
`permissions.defaultMode: "bypassPermissions"` and
`skipDangerousModePermissionPrompt: true`, so Claude Code in a guest runs
commands and edits without asking and shows no warning dialog, including
in `repose run`. The guest is the blast radius and a
snapshot restores it; `dev` is not root, which bypass mode requires.

- The Claude wrapper's setup adds both keys to an existing file only when
  it has no `permissions.defaultMode`; a value the user set, in the guest
  or carried from the laptop by the I-196 merge (laptop on top), is never
  changed. Opting out is setting another `defaultMode`; deleting the key
  brings the default back at the next start.
- These are user settings, never managed settings
  (`/etc/claude-code/managed-settings.json`), which would outrank the user.
- The same file carries `tui: "fullscreen"`, added where the user's file
  has no `tui` (I-425), so Claude Code draws in the alternate screen of
  its tmux pane from a machine's first start. Without it, the renderer
  follows a server-side flag a new machine has not cached yet. A user's
  `"default"`, from the guest or the laptop, is kept.
- Deny rules, explicit ask rules and removals of critical paths still
  prompt or block in this mode (Claude Code's own rules).
- Codex, opencode, Gemini CLI and pi keep their own defaults; the user docs
  show Codex's two keys.

## The machine guide (DECISIONS I-243)

Every agent is told what the machine offers: that the laptop is out of
reach, that its servers' ports reach the laptop's `localhost` while the user
is attached, how to install a tool now and how the user keeps it, Docker and
databases, secrets (and never to print them), the browser, how to reach the
user, git over HTTPS, and the limits. The one source is
`nix/guest/base/agent-guide.md`; `nix/guest/base/agent-guide.nix` renders it
(comments dropped, a `needs: CMD` line kept only when the guest has `CMD`)
and installs it where each agent reads global instructions without touching
a file the user owns:

| Agent | Where | Mechanism |
|---|---|---|
| Claude Code | `/etc/claude-code/CLAUDE.md` | managed memory, loaded before the user's `~/.claude/CLAUDE.md` |
| Codex | `/etc/codex/config.toml` `developer_instructions` | system config layer; a user-level `developer_instructions` replaces it |
| opencode | `/etc/opencode/opencode.json` `instructions` | managed config dir; `instructions` arrays are unioned with the user's |
| Gemini CLI | `~/.gemini/extensions/repose-machine-guide` → `/etc/repose/gemini-extension` | extension context file, linked by the wrapper; no system-level context exists and `@` imports outside the workspace are refused |
| pi | `~/.pi/agent/extensions/repose-machine-guide.js` → `/etc/repose/pi-extension.js` | extension adding a system prompt section, linked by the wrapper |

The user's `CLAUDE.md`, `AGENTS.md` and `GEMINI.md` are never edited, and
the carried `~/.claude/CLAUDE.md` (I-196) stays the user's. A new guest
capability updates the guide in the same commit; `internal/cli/agent_guide_test.go`
fails when a section of the machine, agents or limits page has no guide
line, and the `guest-agent-guide` VM test checks every agent sends it.

## Versions

Agents come from the platform overlay (`nix/overlay/agents/`), which
repackages upstream binary releases and is bumped by a scheduled job that
opens a pull request with the version diff. Users see the version in `repose
status --verbose` and in the base changelog. nixpkgs is the fallback only;
it lags Claude Code by weeks and Gemini CLI by months (DECISIONS R3-19).

## Claude login and the policy behind it

Anthropic's terms for hosted use of Claude Code require the binary to be
unmodified and every user to authenticate with their own credentials; third
party reuse of subscription OAuth is forbidden. So:

- The platform never reads, proxies, or copies Claude credentials.
  `~/.claude/.credentials.json` is not in the synced-files list and a test
  asserts it never appears in a tar stream. Copied refresh tokens also do
  not refresh, so copying would break within hours anyway.
- The default path is logging in inside a guest, once per user. The first
  `run` with agent `claude` and no credentials (an empty or missing
  `~/.claude/.credentials.json`) starts `claude` in the agent window; it
  prints a URL and expects a code pasted back. That keeps every feature,
  including Remote Control, which is the "check on it from my phone"
  feature this product wants.
- The file Claude Code writes is the user's login share: one file per user
  on the host, bind-mounted into every guest of that user, so one `/login`
  signs in every project, and a refresh in one guest is seen by the others
  (DECISIONS I-278; how Claude Code writes it, and why a bind mount of the
  one file rather than a symlink or the whole `~/.claude`, is in the
  decision). repose creates and mounts the file and never opens it.
- The fallback is `claude setup-token` on the laptop, stored as the named
  secret `CLAUDE_CODE_OAUTH_TOKEN`. It works headless and survives guest
  restarts, but loses Remote Control, connectors and Claude in Chrome. The
  public agents page says so; the CLI does not.
- The wrapper does not modify the binary. Hooks are configuration.

## Your Claude Code config comes with you (DECISIONS I-196)

`run` and `attach` carry the laptop's own Claude configuration into the
guest, and never its login or its history:

- Carried: `~/.claude/CLAUDE.md`, `settings.json` (merged, below),
  `skills/`, `agents/`, `commands/`, `output-styles/`, `keybindings.json`,
  and the scripts under `~/.claude/` that `settings.json` runs (hooks,
  `statusLine`). Files are copied onto what the guest has, so a skill made
  in the guest stays; a directory over 32 MB is skipped with one line.
- Never carried: `.credentials.json` (anywhere under `~/.claude`),
  `projects/` (transcripts), `history.jsonl`, `todos/`,
  `shell-snapshots/`, `file-history/`, `paste-cache/`, `sessions/`,
  `plugins/`, `statsig/`, and `~/.claude.json`. The list above is an
  allowlist, so nothing else is read.
- `settings.json` is merged in the guest with `jq`, never overwritten:
  the guest's file is the base and the laptop's goes on top (the laptop
  wins on any key it sets, `model` included); `permissions.allow`, `deny`
  and `ask` are unioned, so "Yes, and don't ask again" in the guest
  survives the next run; the platform's `repose-hook` entries are removed
  from both sides and added back once, last; the laptop's home directory
  is rewritten to `/home/dev`; and a hook or `statusLine` whose command
  the guest cannot run (`afplay`, a Homebrew path) is dropped and named
  once. The result is checked with `jq empty` before it replaces the old
  file, which is kept as `settings.json.repose-prev`. A laptop file that
  is not JSON is not sent (one warning); a guest file that is not JSON is
  left alone (one warning).
- Plugins: `settings.json` names them but Claude Code does not reinstall
  them on a new machine, so the guest installs the marketplace plugins
  in `enabledPlugins` that it lacks, in the background, with `claude
  plugin marketplace add` and `claude plugin install`, and says in tmux
  what it installed or could not. A failed install is tried again on the
  next run. A plugin whose marketplace is a directory on the laptop is
  named once and skipped.
- Only what changed on the laptop is sent (one marker per item in the
  guest, DECISIONS I-206).

## Behaviour that must hold

- Every agent starts in its own tmux window named after it, in the
  checkout (guest-conventions "The checkout"), with the prompt typed once the TUI is ready
  (run-and-attach.md).
- Every agent's completion and needs-input reach the API as events within
  10 seconds of the hook firing, or within 90 seconds for heuristic agents.
- A missing login produces the agent's own login prompt in the window plus
  the CLI message above; it never produces a crash loop.
- `repose status` shows per agent window: `working`, `idle`,
  `needs_input`, or `unknown`, from guestd's `AgentState` notifications.
  An agent started by hand in another window (the `shell` window) is one
  of them while it is that window's foreground program (I-421).
- Removing an agent from the overlay is a base bump with a changelog line;
  a project holding base updates keeps the old one.

## MCP

### Platform servers

Every agent has `playwright` and `chrome-devtools` (browser.md), written
where the agent has a layer beneath the user's own file, with `command`
and `args` only, so a user's field-level override stays clean (DECISIONS
I-553, I-554):

| Agent | Where repose writes them | User turns one off | User's server of the same name |
|---|---|---|---|
| Claude Code | `~/.claude.json` `mcpServers`, from `/etc/repose/mcp.json`, by `repose-mcp sync claude` (I-555) | `/mcp`, per project | wins (I-246) |
| Codex | `~/.codex/config.toml` `[mcp_servers.NAME]`, by `repose-mcp sync codex` (I-555); on a base without `repose-mcp`, appended by `repose-agent-setup` when the parsed file lacks NAME; a file that is a symlink (home-manager) is left alone | `enabled = false` in that table | wins; repose never touches a table it did not write |
| opencode | `/etc/opencode/opencode.json` `mcp.NAME`, `{type: "local", command: [...]}` | `{"mcp":{"playwright":{"enabled":false}}}` in `~/.config/opencode/opencode.json` | merged field by field; the user picks another name |
| Gemini CLI | `/etc/gemini-cli/system-defaults.json` `mcpServers`, a copied root 0644 file | `{"mcp":{"excluded":["playwright"]}}` in `~/.gemini/settings.json` | replaces ours whole |
| pi | `pi.registerMcpServer` in `/etc/repose/pi-extension.js` (pi 0.99 and later) | `{"mcpServers":{"playwright":{"command":"playwright-mcp","enabled":false}}}` in `~/.pi/agent/mcp.json` | wins |

Rejected layers: Claude Code's `managed-mcp.json` takes exclusive control
(`claude mcp add` fails and user servers disappear from `claude mcp
list`). Codex's `/etc/codex/config.toml` would put the servers beneath the
user's file, but a user table of the same name holding `url` then stops
every `codex` command (`url is not supported for stdio`), and `codex mcp
add` copies every system-layer server into the user file anyway. Gemini's
folder trust is off in the system defaults, since in a folder it was not
told to trust it disables every MCP server, headless runs included. `pi mcp
list` does not load extensions and does not show the two.

What works on the machine: HTTP and SSE MCP servers (Notion, Linear,
Sentry, GitHub, Stripe and the like), and stdio servers that are thin
wrappers over an API and only need `npx` and a token. Tokens go in as named
secrets. A user who wants an MCP server only in the guest adds it there with
the agent's own command, or in the repo's `.mcp.json`, which syncs with the
repo. Servers bound to the laptop (Apple Notes, Xcode, iMessage, desktop
automation, filesystem servers pointed at laptop paths) stay there; the
carry names them once, and `repose mcp forward` (below) runs one on the
laptop for the machine's agents while the laptop is open.

### One list per machine, rendered per agent (DECISIONS I-555)

The guest keeps one MCP list in `~/.repose/mcp/`: the platform servers
from `/etc/repose/mcp.json`, the laptop's Claude Code servers the carry
writes to `laptop.json`, and each forwarded server in `forward/NAME.json`.
`repose-mcp sync AGENT`, run by `repose-agent-setup` at every agent start,
writes that list into the agent's own config: `~/.claude.json` (user scope,
and `projects[<path>]` for a checkout's servers), `~/.codex/config.toml`,
the Gemini CLI extension `repose-mcp`, `~/.config/opencode/config.json`
(which opencode loads beneath the user's `opencode.json`), and
`~/.repose/mcp/agents/pi.json` for pi's extension. The carry and the
forward never touch an agent file. repose writes `~/.claude.json` from
three places, sync, agent-setup's onboarding keys and the CLI's folder
trust write (I-486), and each holds Claude Code's own lock.

An entry is repose's only while it holds what repose wrote; a user who
edits one owns it from then on, and a user's server of the same name wins
(I-246's rule, now for every agent). opencode merges same-name servers
field by field, so a name in the user's `opencode.json` or
`opencode.jsonc` keeps repose's entry out of `config.json` entirely. Deleting a server repose renders
brings it back at the next start; each agent's own off switch (`enabled =
false` in Codex and opencode, `mcp.excluded` in Gemini CLI, `/mcp` in
Claude Code) turns it off.

Secrets: Codex hands stdio servers a fixed environment allowlist and
expands nothing, so its carried stdio servers run as `repose-mcp run
NAME`, which fills each `${NAME}` from `/run/repose/secrets` and execs the
real command; a secret set later reaches the next server start without
restarting Codex. A secret it lacks stops the start with one line naming
it and `repose secrets set NAME`, since the server would otherwise send
the literal `${NAME}` to its service as a token. Claude Code, Gemini CLI, opencode (`{env:NAME}`) and pi
expand references themselves and get the server's own shape, except a
server with a reference in its command or arguments, or a
`${NAME:-default}`, which goes through the launcher too. Codex takes
`Bearer ${NAME}` and whole-value `${NAME}` headers as its
`bearer_token_env_var` and `env_http_headers`; an SSE server, or a header
it cannot fill, is skipped for Codex alone.

`repose-mcp status --json` reports, per server, where it came from, which
agents have it and what it lacks; `repose mcp list` shows it (I-558). The
layout and the commands are in `docs/interfaces/guest-conventions.md` "MCP
registry". `repose-mcp NAME` and `repose-mcp hold` are the forward's two
ends (below); in a base before the forward they exit 1 with "forwarding is
not built in this base", which the CLI reads as an old base.

### Carry of the laptop's Claude Code servers (DECISIONS I-556)

`run` and `attach` carry the `mcpServers` of the laptop's `~/.claude.json`
(or `$CLAUDE_CONFIG_DIR/.claude.json`): user scope, and local scope for
the repository (`projects[<main worktree root>]`, which a linked worktree
and a subdirectory share). Nothing else in that file is read into the
payload. `internal/cli/carry_mcp.go` classifies each server, in order:

- dropped without a word: the platform's own names and packages
  (`playwright`, `chrome-devtools`, `@playwright/mcp`, `playwright-mcp`,
  `chrome-devtools-mcp`), since the machine's are wired to the shared
  browser (I-246);
- left on the laptop with a short reason, named once: a name outside
  `^[A-Za-z0-9_-]{1,64}$`, a `type` other than stdio/http/sse/ws, an Apple
  app (`applescript`, `apple-`, `imessage`, `xcode`, `iterm`, `macos`,
  `osascript`, `shortcuts` in the command or arguments), a URL on
  `localhost`, `*.local`, `*.ts.net`, a loopback, private, link-local or
  `100.64/10` address, `headersHelper` or `oauth.clientSecretHelper`, an
  absolute command or argument under the laptop's home or a macOS or
  Homebrew location (`docker -v /Users/...` and `${HOME}/x` included), an
  env value that is such a path;
- carried, templated: a launcher's absolute path (`~/.nvm/.../npx`) becomes
  its base name; a path under the repository becomes
  `@@REPOSE_CHECKOUT@@`; every literal credential becomes `${NAME}`: URL
  user info, credential-named query parameters, and a URL path segment or
  query value shaped like a token; `Authorization: Bearer|Basic|Token
  <literal>`; every header value but `Accept*`, `Content-Type`,
  `User-Agent`, `MCP-Protocol-Version` and `Cache-Control`, in `headers`
  and in a `--header "Name: value"` argument; the value of a flag whose
  name holds token, secret, password or apikey or ends in key, auth or
  pass (`--auth-type` does not); an env value under a credential-named
  key; anywhere, a value `secretIn` matches, a provider key prefix
  (`sk-`, `AIza`, `ntn_`, ...), or 20 token characters with a letter and
  a digit (env values: any 20-character run); `oauth.clientSecret`. An env key keeps its own
  name when it is a valid secret name and not one an agent or the shell
  reads for itself (`ANTHROPIC_*`, `CLAUDE_CODE_*`, `OPENAI_API_KEY`,
  `CODEX_*`, `GEMINI_API_KEY`, `GOOGLE_API_KEY`,
  `GOOGLE_APPLICATION_CREDENTIALS`, `GITHUB_TOKEN`, `GH_TOKEN`,
  `VERCEL_TOKEN`, `OPENCODE_*`, `REPOSE*`, `BASH_ENV`, `ENV`); everything
  else is `<SERVER>_<SUFFIX>`, upper case, `MCP_` in front of a digit. Two
  servers with different values under one name: the second takes its
  server prefix. The comparison is in memory.

The hash covers the templated list only, so a rotated laptop token sends
nothing. The guest script writes `~/.repose/mcp/laptop.json`
(guest-conventions.md) and no agent file; `repose-mcp sync` renders it at
each agent start, so a running agent sees a change after a restart, as on
the laptop. The run prints, once per change: the servers left on the
laptop, the secrets the machine lacks (with `repose secrets import --mcp`
when the laptop had the value), the commands it lacks, and, on a base
without `repose-mcp`, that the servers arrive with the next base. A
same-name server the user added on the machine is theirs; the renderer
keeps it.

`logins.skip = ["mcp"]` (`repose secrets choose --off mcp`) sends empty
scopes, on `run` and on `attach`, and the next render removes only what
repose rendered. `repose secrets import --mcp` reads the laptop config
again, resolves each name the carry templated to its laptop value in
memory, and sets them through the secrets PUT for the folder's project.
The carry chose the names, so when the project already
has some it asks once before replacing them (`--yes` skips the question,
a no sets only the others); it refuses a FILE argument.

The agent window's trust write (I-486) also copies the laptop's
`enabledMcpjsonServers` and `disabledMcpjsonServers` for the repository,
adding each server the guest's entry answers in neither list (Claude Code
writes both lists empty into every project it opens, so an empty list is
no answer), and Claude Code's "New MCP server found
in this project" dialog stops `run` from typing its prompt (outside
`bypassPermissions` that dialog would otherwise take the Enter).

### Forward: `repose mcp forward NAME...` (DECISIONS I-557)

For a server the carry leaves on the laptop (an Apple app, a laptop file,
a program only the laptop has), `repose mcp forward NAME...` runs it on the
laptop for the agents on the machine, until Ctrl-C. `[mcp] forward` and
`[projects.NAME.mcp] forward` in config.toml do the same beside every
attach, through the session helper, the way `--bridge` does; that path
prints nothing on success and names a failure through tmux. The command
takes names only; the project comes from the folder or `--project`, since
names and a PROJECT cannot share positions without a guess (an exception
to I-155).

Definitions are the laptop's, in order: Claude Code's local scope for the
repository, its user scope, Claude Desktop (macOS), `$CODEX_HOME` or
`~/.codex/config.toml`, `~/.gemini/settings.json`, or `-- COMMAND ARGS`
for one NAME. `${VAR}` expands from the laptop's environment, so tokens
stay there. The server runs in the laptop's repository, or home, or the
entry's `cwd`. An HTTP entry is refused (forwarding loopback HTTP servers
is deferred); an unknown NAME lists every name each config has.

Transport, per the design's critic correction 1: the laptop runs `ssh
MACHINE repose-mcp hold NAME...` on a connection of its own
(`ControlPath=none`) with no `-R`, since the gateway relays only
`forwarded-tcpip` channels back to a client (I-296). The session's stdin
and stdout carry frames (`internal/mcpshim/frame.go`): hello, open NAME,
data, close, ready, gone. Hold listens on `/run/repose/mcp/NAME.sock`
(tmpfiles `0700 dev`, socket `0600`) and carries each connection as a
stream; the laptop starts one server process per stream, so each agent
session gets its own, as stdio servers run locally. At most 8 per NAME:
hold turns the ninth away with a busy line the shim reports, and the
laptop refuses past that too. A stream's end closes the server's stdin and
sends TERM to its process group, KILL after 2 s. The laptop's end answers
the shim's `$/repose/ping` itself, and starts only the command its own
config names: the machine sends a name, never a command.

Registration: before it listens, hold acts as an MCP client through the
laptop (initialize, then `tools/list` page by page), writes
`~/.repose/mcp/forward/NAME.json`, and on a first registration runs
`repose-mcp sync` for every agent sync has rendered for, so each lists
NAME at its next start. The ready frame carries the tool count and whether
the registration is new or the tools changed, for the CLI's one line. A
new hold of the same NAME unlinks and rebinds the socket; the old one sees
the socket's inode change within a second, ends its streams and sends
gone. `repose-mcp hold --remove NAME...` deletes the registration and
syncs; the CLI's `--remove` runs it.

The shim, `repose-mcp NAME`, is what every agent's config starts. It
answers `initialize` (protocol version the older of the agent's and the
cached one, `capabilities.tools.listChanged` set, since opencode listens
for list_changed only then) and `tools/list` from the registration, so an
agent's start never waits on the laptop. After `notifications/initialized`
it connects, replays the agent's initialize to the laptop's server, fetches
the tools and passes lines both ways; tools that differ from what the
agent has send `notifications/tools/list_changed` (Codex ignores it, which
the CLI's line says). A server choosing another protocol revision than the
agent was told is a stderr line, and the session goes on. The shim pings
every 10 s; two misses, or the socket closing, mark the laptop away:
pending requests are answered (a tools/call with `isError`), every later
tools/call answers `NAME runs on the user's laptop, which isn't connected.
Ask them to run repose mcp forward NAME there.`, list methods answer from
the cache, and the shim retries every 5 s. A sleeping laptop shows within
about 20 s, where sshd alone takes 2 minutes.

The laptop prints one line per name when it is registered, one per call
(`claude called apple-notes.search_notes`, agent from the initialize's
`clientInfo`, tool name only), and a line when the connection drops and
it reconnects with a backoff up to 30 s. A base without the forward is
named once and not retried. Windows has no session helper, so `[mcp]
forward` does nothing there; the foreground command needs only ssh's
stdio.

### List: `repose mcp list [PROJECT]` (DECISIONS I-558)

`repose mcp list` (alias `ls`) runs `repose-mcp status --json` over the
project's SSH connection and prints one row per server: NAME, FROM,
AGENTS (`none` when no agent has it) and STATE. FROM is `repose` (the
platform), `laptop` (carried, or left on the laptop with the carry's
reason), `project` (a checkout's `.mcp.json`), `forward` or `machine`
(added on the machine in an agent's own config). STATE is the status
document's `state`, after the checkout path (`~/app`) for a row that
belongs to one checkout. On a terminal it is a table with a header; piped,
one tab-separated line per server and no header, as `secrets list`.
`--json` prints the machine's document as it came. It reads files and
starts no server, so it answers in the time of one SSH command. A base
without `repose-mcp` (exit 127) gets one line: the base predates the list
and it works after the machine's next update. Needs the machine running.

## Depends on

Workstreams 02 (overlay, wrappers, `repose-hook`), 04 (hook socket,
pane-idle heuristic, AgentState), 05 (events ingest), 07 (`--agent`,
`default_agent`, secrets), 13 (delivery).

## Deferred

Carrying the laptop's Codex, Gemini CLI and opencode MCP configs (only
Claude Code's travels, I-556). Forwarding loopback HTTP servers on the
laptop (Figma Dev Mode at `127.0.0.1:3845/mcp`): the laptop end would be an
HTTP client of that URL, and the guest side stays as it is. Agents
beyond the five (DECISIONS R2-11: anything nixpkgs does not package is a
package the platform maintains).
