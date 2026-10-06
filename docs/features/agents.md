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
| Claude Code | `~/.claude.json` `mcpServers`, merged from `/etc/repose/mcp.json` by `repose-agent-setup` | `/mcp`, per project | wins (I-246) |
| Codex | `~/.codex/config.toml` `[mcp_servers.NAME]`, appended by `repose-agent-setup` when the parsed file lacks NAME; a file that is a symlink (home-manager) is left alone | `enabled = false` in that table | wins; repose never touches a table under that name |
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

What works unchanged: HTTP and SSE MCP servers (Notion, Linear, Sentry,
GitHub, Stripe and the like), and stdio servers that are thin wrappers over
an API and only need `npx` and a token. Playwright MCP and chrome-devtools-mcp
are preinstalled (browser.md). Tokens go in as named secrets and are
referenced with `${VAR}` in the MCP config.

What does not work: servers bound to the laptop, such as Apple Notes, Xcode,
iMessage, desktop automation, Claude in Chrome, and filesystem servers
pointed at laptop paths. They are unsupported in the first release
(DECISIONS R2-14). The CLI does not sync `~/.claude.json` because its
project-scoped entries are keyed on absolute laptop paths that do not exist
in the guest; a user who wants an MCP in the guest adds it there, or in the
repo's `.mcp.json`, which syncs with the repo.

### Not built: `repose mcp forward NAME`

The command is reserved: it prints that it is not available yet and exits 0.

For when the laptop is open and a laptop-bound server is wanted anyway:

1. The CLI reads the server's definition from the laptop's Claude config.
2. It starts the stdio server locally wrapped by `mcp-proxy` as HTTP on a
   free local port.
3. It opens an SSH reverse tunnel (`-R`) from a guest port to that local
   port for the life of the CLI process.
4. It registers `NAME` in the guest's Claude user-scope config as an HTTP
   server at `http://127.0.0.1:<port>/mcp`, and removes it when the tunnel
   closes.

It only works while the laptop is up, which is the case this product exists
to escape, so it is a convenience, not a promise.

## Depends on

Workstreams 02 (overlay, wrappers, `repose-hook`), 04 (hook socket,
pane-idle heuristic, AgentState), 05 (events ingest), 07 (`--agent`,
`default_agent`, secrets), 13 (delivery).

## Deferred

`repose mcp forward`. Syncing `~/.claude.json` with path rewriting. Agents
beyond the five (DECISIONS R2-11: anything nixpkgs does not package is a
package the platform maintains).
