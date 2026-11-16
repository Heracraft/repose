# Phone access through the vendors' apps: each machine as a Remote Control host (proposal, 2026-11-10)

**Status: proposal; nothing here is decided.** This is future item 10,
"Vendor remote-control host", of
`reports/2026-11-04-product-value-council.md` (the innovation member's
round-2 card, "What changed from round 1"). The owner chose it, with an
option to turn it off. Each repose machine signs in to Claude Code Remote
Control after the user's Claude login, so the Claude app (phone, desktop,
claude.ai/code) shows the agents on the always-on machine and can start
and drive them there, with the setup that `run` carried. As a second
target, the machine can run Cursor's self-hosted worker, so Cursor's web
and iOS app can pick the machine for a run. Repose builds no phone
interface of its own. It becomes real only through `DECISIONS.md`
entries that amend DESIGN.md §13 and `docs/workstreams/13-notifications.md`
("the platform does nothing to them") and that name the off switch.

Outside facts were checked on 2026-11-10 and carry their URL in
"Sources". Facts marked *unverified* could not be confirmed from a
primary page. No measurement was taken for this proposal; every cost
figure that the freeze rule depends on is a step of the plan (section 9).

## Summary

| Question | Answer |
|---|---|
| What the user gets | The machine's Claude sessions in the Claude app's **Code** list, with a green dot while the machine is awake. From the phone: read, prompt, answer permission prompts, start a new session on the machine, get Claude's own push |
| How it runs | Two parts. (1) Every agent window repose starts registers its own session, through Claude Code's `remoteControlAtStartup` user setting, added the I-250 way. (2) One `remote-control` tmux window runs `claude remote-control --spawn worktree`, so the phone can start sessions on the machine |
| When it starts | At the first agent start or machine boot after the login share holds a login (`test -s`, I-278). Per machine. Claude Code's own one-time "Enable Remote Control?" question stays the user's to answer |
| Off switch | Claude Code's own `disableRemoteControl` or `remoteControlAtStartup: false`, carried from the laptop (I-196), always win. Repose adds `remote_control = false` in `config.toml`, global or under `[projects.NAME]`. No new command |
| Default | Proposed on for Claude, if step 1 shows an idle Remote Control machine still freezes. Off for Cursor. Owner decides |
| Freeze | A frozen machine is offline in the app, and nothing from the app can wake it. The idle cost of Remote Control must stay under 6 s CPU and 64 KiB a minute, or every Claude user's machine stops freezing |
| Security | The user's claude.ai login becomes a second way to run commands as `dev` on the machine, in `bypassPermissions`. Recommend Claude's Trusted Devices in the docs; owner decides the permission mode of sessions started from the phone |
| Cursor | `agent worker start` as a systemd user service, after `agent login` on the machine or a `CURSOR_API_KEY` secret. Needs the Cursor CLI in the base. Opt-in, second step |
| Size | S for part (1), S for part (2), M for Cursor (packaging and a new login) |

---

## 1. Claude Code Remote Control as of 2026-11-10

From the Remote Control page (code.claude.com/docs/en/remote-control,
checked 2026-11-10):

- **Three ways to start.** `claude remote-control` (alias `claude rc`) is
  server mode: one process waits for connections and starts sessions on
  demand. `claude --remote-control` (alias `--rc`) is an interactive
  session that is also visible remotely. `/remote-control` inside a
  session connects that session.
- **Sessions can start from the phone.** Server mode spawns sessions on
  demand, `--capacity` up to 32 by default. `--spawn same-dir` puts them
  all in one folder, `--spawn worktree` gives each a git worktree, and
  `--spawn session` serves one session only. `--name` sets the title in
  the session list; without it, names are `<hostname>-<two words>`, and
  `--remote-control-session-name-prefix` changes the hostname part.
  `--permission-mode` sets the starting mode of the server's sessions.
- **Auto-connect.** `remoteControlAtStartup: true` in user settings makes
  every interactive session register one remote session. A `false` in
  project or local settings turns it off for that repository; a `true`
  there is ignored. `disableRemoteControl` turns the feature off entirely.
- **Auth.** A claude.ai subscription login (Pro, Max, Ultra, Team,
  Enterprise). API keys, `ANTHROPIC_AUTH_TOKEN`, `apiKeyHelper`, and the
  long-lived `claude setup-token` / `CLAUDE_CODE_OAUTH_TOKEN` are refused
  ("Remote Control requires a full-scope login token"). On Team and
  Enterprise an Owner must turn it on first; Zero Data Retention and
  HIPAA organizations cannot. A custom `ANTHROPIC_BASE_URL`, Bedrock,
  Vertex, Foundry, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` and
  `CLAUDE_CODE_DISABLE_FEATURE_FLAGS` make it unavailable.
- **Consent and trust need a terminal.** Until the user accepts a
  one-time confirmation, `claude remote-control` asks
  `Enable Remote Control? (y/n)`. In an untrusted folder it asks
  `Trust <directory>? [y/N]`, and with no terminal on stdin or stdout it
  exits with `Workspace not trusted`. Where Claude Code stores the
  one-time consent, and whether auto-connect asks for it, is *unverified*.
- **Network.** "Outbound HTTPS requests only and never opens inbound
  ports." The process "registers with the Anthropic API and polls for
  work"; a connected device talks to it over a streaming connection.
  Presence heartbeats run; an interactive session that cannot reach the
  server for about 30 minutes disconnects. The poll interval and the
  bytes a minute an idle process moves are *unverified*.
- **Outages.** An interactive session retries for as long as an outage
  lasts. Server mode "gives up after roughly 10 minutes and the
  `claude remote-control` process exits". A stopped server's sessions
  come back with `claude remote-control` in the same folder for about
  four hours; after that, new sessions.
- **Wrappers.** A global flag that a wrapper adds before `remote-control`
  makes the server refuse to start, unless dropping it changes nothing
  (`--verbose`, `--model`).
- **Data.** While connected, the transcript is stored on Anthropic's
  servers under the Data usage policy. Execution and files stay on the
  machine.
- **Push.** The Claude app gets push "when a long-running task finishes
  or when Claude needs a decision", after the user turns on **Push when
  Claude decides** or **Push when actions required** in `/config`.
- **Trusted Devices** (beta, off by default). On Pro, Max and Ultra the
  user turns on **Require trusted devices**; each browser or phone then
  enrols, and a sign-in older than 18 hours needs Face ID, Touch ID or a
  passkey. The terminal machine gets its credential at `/login`.
- **Remote machines are an expected case.** "If you run `claude` from a
  terminal on a remote machine, start it inside `tmux` or `screen`."
- **Terms.** No clause found that forbids Remote Control on a hosted VM
  with the user's own login. Anthropic's answer to I-278's experiment C
  (whether the login share counts as "store or intermediate") is still
  open; this proposal adds nothing that reads the credential. *Unverified*
  with Anthropic.

The council's "sessions started from the phone since Aug 2026" agrees
with server mode as documented; the start date is *unverified*.

## 2. Cursor's equivalent as of 2026-11-10

From cursor.com/docs/cloud-agent/bring-your-own-machine,
cursor.com/blog/self-hosted-machines (2026-09-02) and
cursor.com/docs/cloud-agent/web-and-mobile, checked 2026-11-10:

- **Command.** `agent worker start` for **My Machines** (one user's
  laptop, devbox or VM). `agent worker --pool NAME start` is for **Team
  Pools**, which need the Enterprise plan and a service-account key.
- **Install and auth.** `curl https://cursor.com/install -fsS | bash`.
  `agent login` (browser) or a personal API key; pools use
  `CURSOR_API_KEY`. My Machines needs team admin approval where a team
  exists. Which individual plans include it is *unverified*.
- **Where the agent runs.** "Cursor runs the agent loop, inference, and
  planning." The worker runs file edits, terminal commands and local MCP
  servers, as the worker's OS user.
- **Network.** "A long-lived outbound HTTPS connection to Cursor's
  backend"; no inbound ports. Workers reconnect when they come back
  online. Hosts: `api2.cursor.sh`, `api2direct.cursor.sh`,
  `api3.cursor.sh`, `downloads.cursor.com`, an S3 bucket for artifacts.
- **Phone.** The iOS app can "pick a worker for each run: a Cloud
  machine, a Team Pool, or one of My Machines", and pushes when an agent
  finishes a turn. "Android is planned."
- **Flags seen on the page.** `--connect-workspace`, `--clone-git-repos`,
  `--sync-dashboard-secrets`, `--on-session-start`, `--pool`,
  `--verbose`; `--idle-release-timeout` (seconds, default 3600, from a
  search snippet, *unverified* for My Machines). Workers run under
  "`systemd`, `launchd`, Docker, or another process manager".
- **Terms** for a worker on a hosted VM: *unverified*.

Codex CLI also has a `remote-control` subcommand (the wrapper's list at
`nix/overlay/agents/wrap.nix:61`). It is out of scope here and
*unverified* as a phone path.

## 3. The repo today

**The wrapper passes arguments through.** `wrap.nix:31` runs
`repose-agent-setup` and `wrap.nix:39` execs the real binary with `"$@"`,
adding no flag, so `claude remote-control` through the wrapper meets
Claude Code's no-added-flag rule. The package wrapper sets only
`DISABLE_AUTOUPDATER`, `DISABLE_INSTALLATION_CHECKS` and
`USE_BUILTIN_RIPGREP` (`nix/overlay/agents/claude-code.nix:22-27`); none
of them blocks Remote Control. No file under `nix/` or `internal/` sets
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` outside a test
(`nix/guest/tests/default.nix:2855`). The base pins Claude Code 2.1.283
(`nix/overlay/agents/versions.json`); several Remote Control fixes on the
docs page need a newer release (crashed-session recovery 2.1.238 is in,
the "program file gone" fix 2.1.318 is not).

**Settings are added only where the user set none.** `setup_claude`
(`nix/overlay/agents/agent-setup.nix:154-188`) merges
`/etc/repose/claude-settings.json` into `~/.claude/settings.json`: hooks
by presence, `permissions.defaultMode` only when the user has none
(I-250, :167-172), `tui` the same way (I-425). The laptop's settings sit
on top (I-196). `remoteControlAtStartup` would follow the same rule.

**Folder trust is set by the CLI.** `claudeTrustScript`
(`internal/cli/mux_tmux.go:442`) writes
`projects[dir].hasTrustDialogAccepted = true` in `~/.claude.json` before
any agent window (I-486), so the `Trust <directory>?` question does not
apply to folders repose starts Claude in. Windows open with
`tmux new-window -t SLUG -n NAME` (`mux_tmux.go:417`).

**The login check is a size check.** `needsClaudeLogin`
(`mux_tmux.go:329-338`) runs `test -s ~/.claude/.credentials.json` and
never reads the file (R2-8, I-278). A `CLAUDE_CODE_OAUTH_TOKEN` secret
counts as logged in (:330), and that login cannot use Remote Control.

**The login share.** `nix/guest/base/claude-auth.nix` bind-mounts the
user's one shared `.credentials.json` before dev's user manager starts
(I-278, I-464). One `/login` signs in every machine of the user on that
host, and one per host.

**Hooks name the window from `TMUX_PANE`.** `repose-hook.nix:83-86`
resolves the window name from `$TMUX_PANE`; guestd records state per
window (`internal/guestd/hooks/hooks.go:67-68`). A session that a server
spawns inherits the server's `TMUX_PANE`, so every phone-started session
would report under the server's window (*unverified*, follows from
environment inheritance).

**The freeze rule.** `internal/api/resting/resting.go:43-66`: 15 quiet
one-minute samples, `cpu_ns` under 6 s (`.claude-wrapped` at its prompt
uses about 2 s), `net_rx + net_tx` under 64 KiB. Quiet also needs no SSH
session, no tmux client and every agent `idle` or `needs_input`
(`internal/api/idle/idle.go:80-82`). An open `repose-ask` question holds
the machine awake (`resting.go:230-252`, I-822). What wakes a machine is
listed in I-822: SSH at the gateway, ops, an answer, secrets, config,
timers. Nothing on the internet can reach a guest, so nothing that
Anthropic or Cursor sends can wake one. I-930 set the rule for held
connections: "Presence is not activity"; only moving bytes count.

**The public docs already promise part of this.** `docs/features/agents.md:166-169`
calls Remote Control "the 'check on it from my phone' feature this
product wants" and the reason the login is a real `/login`.
`apps/web/src/content/docs/agents.md:107` says a setup token loses
Remote Control. `DESIGN.md:499-503`, `docs/features/notifications.md:351-354`
and `docs/workstreams/13-notifications.md:36-37` say the platform does
nothing to Remote Control. I-256 named the Claude app's Remote Control as
the path for voice. `docs/LANDING.md:27-29` keeps "remote control" and
"answering from your phone" off the landing.

**The CLI's config file.** `internal/cli/config.go:30-67` has top-level
keys and `[projects.NAME]` tables for `logins` and `mcp`; `cli.md:462-500`
documents them. `repose config on|off` already means "your machine.nix on
or off for this project" (`cli.md:442-443`). Per-project switches that
the server holds (**Never freeze this machine**, **Hold base updates**)
are dashboard checkboxes with "no CLI flag for it" (`config.md:184-186`).

## 4. The proposal

### 4.1 When it starts

Per machine, once the machine has an eligible login:

1. **Agent windows.** `setup_claude` adds `"remoteControlAtStartup": true`
   to `~/.claude/settings.json` when the key is absent, the same rule as
   `defaultMode`. Every `claude` that `repose run` opens then registers
   one remote session. A laptop `false` (carried by I-196) or a project's
   `.claude/settings.json` `false` wins.
2. **The server window.** At the claude wrapper's start and at machine
   boot, a small `repose-remote-control` check opens a tmux window named
   `remote-control` when all of these hold: the switch is on (4.4), the
   login share file is not empty (`test -s`), no `CLAUDE_CODE_OAUTH_TOKEN`,
   `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` secret exists (by the
   file test in `/run/repose/secrets`, never by value), and no such window
   exists. It runs, in the main checkout:

   ```
   claude remote-control --spawn worktree --name todo-app
   ```

   inside a loop that starts it again 5 s after it exits, because server
   mode exits after about 10 minutes without network, and a freeze looks
   like that (section 5).

The first login on a host starts nothing on other machines until each
one's next boot or agent start. That is acceptable: a machine with no
agent running has nothing to show in the app.

**Why a tmux window, and not a systemd user service.** Server mode asks
its one-time `Enable Remote Control? (y/n)` on a terminal, and repose must
not answer it for the user (the same reason I-278 leaves `/login` to
Claude Code). In a window, the user sees the question at the next attach,
and the URL and QR code after it. Agents already live in tmux, which a
user unit keeps through detach. On a herdr project the window is a herdr
tab of the same name (I-501). A user service can replace the window once
the consent is known to be stored and server mode is known to run with no
terminal; both are *unverified*.

**Why `--spawn worktree`.** A session started from the phone in
`same-dir` writes in the same checkout as the agent already working
there. Worktrees keep them apart, and their branches reach the laptop
with `git fetch repose` (I-272).

### 4.2 What the user sees

- In the Claude app, **Code**: `todo-app` (the server) and one entry per
  agent window, with a computer icon and a green dot while the machine is
  awake.
- On attach, a `remote-control` window in the window list. The first
  time, it holds Claude Code's consent question.
- `repose status`, one line under the agents:

  ```
  remote control  on
  ```

  or `off`, `needs a Claude login`, `blocked: CLAUDE_CODE_OAUTH_TOKEN is a
  setup token`, `blocked by your organization`. These are fixed words
  from guestd, never Claude Code's own output (section 7).
- Nothing on `run` after a success (I-484). One line on `run` only when
  the switch is on and the machine cannot register, naming why, once per
  reason.

### 4.3 How it fits repose-ask and repose-notify

- Both stay. They are agent-neutral and script-callable, and a
  `repose-ask` answer wakes a frozen machine and holds it awake while it
  waits (I-822). A question asked inside Claude (a permission prompt,
  `AskUserQuestion`) does neither: the agent is `needs_input`, the
  machine may freeze 15 minutes later, and the answer from the phone
  waits at Anthropic until something wakes the machine.
- So the agent guide (I-243) keeps its rule: a decision the user must make
  while away goes through `repose-ask`. No change to the guide text is
  needed for part (1); part (2) adds one line saying that sessions may be
  driven from the Claude app and that `repose-ask` is still the way to
  block on the user.
- Double pushes: a finished turn may reach the phone twice, once from
  repose's `--done` and once from Claude's **Push when Claude decides**,
  if the user turns Claude's push on. Repose does not change either. The
  notifications page says which is which.

### 4.4 The off switch, and where it lives

Placement follows the owner's CLI rules (no new top-level command, no
near-duplicate names; memory `cli-surface-taste`).

**Proposed.** Two layers, no new command:

1. Claude Code's own keys always win: `disableRemoteControl: true`, or
   `remoteControlAtStartup: false`, in the laptop's
   `~/.claude/settings.json` (carried by I-196) or the machine's. With
   either set, repose opens no server window and adds no key.
2. A repose key in `~/.config/repose/config.toml`, read at `run` and
   `attach` and written to the machine like `[logins] skip`:

   ```toml
   remote_control = false        # every project

   [projects.todo-app]
   remote_control = true         # this one on
   ```

   Off removes the server window, sets `remoteControlAtStartup: false`
   only if repose added the `true`, and leaves Claude's own settings
   alone otherwise.

**Rejected placements.**

- `repose config remote on|off`: `repose config on|off` already means
  machine.nix on or off, so the same verbs would mean two things under
  one noun.
- `repose remote ...`: a new top-level noun, and "remote" in repose means
  the git remote (`repose` remote, I-272; `sync`'s remote flags).
- A dashboard checkbox only: it would need an api column, a contract
  field and a guestd path for something the laptop's Claude settings
  already carry. It is the alternative if the owner wants the switch
  reachable from a phone; see "Decisions".

The name `remote_control` is Claude Code's own term, so a user who knows
the feature finds the key by its name. The Cursor key in 6.1 is
`cursor_worker`.

### 4.5 Default on or off

**For on (proposed for Claude).** The users who chose repose for the
closed lid get the phone surface they already have on the laptop, with no
step. The login is a real `/login` (I-278) partly so that this works.
Claude Code still asks its own one-time consent on each machine, so "on"
in repose means "offered", and the user's yes is still theirs. The
owner's goal weighs reach over margin, and this is the cheapest reach
item in the council's list.

**For off.** It sends the session transcript to Anthropic's servers
(the user's own account, which already sees every prompt), it adds a
second way into the machine (section 7), and its idle cost is unmeasured.
If the idle server keeps machines from freezing, "on" costs host CPU for
every Claude user.

**Proposed rule.** On for Claude if step 1 of section 9 shows an idle
machine with Remote Control still freezes; off otherwise, until it does.
Off for Cursor: it needs a new package and a new login, and few users
have My Machines turned on.

## 5. The freeze

- **Idle cost.** An idle server polls and sends heartbeats; each
  auto-connected agent window holds its own connection. Over 15 minutes,
  each minute must stay under 6 s CPU and 64 KiB in and out together for
  the whole machine (`resting.go:59-66`). One server plus three agent
  windows is the case to measure. If it fails, the choices are fewer
  connections (server only, no auto-connect) or default off. Repose must
  not add Remote Control as a term of the rule: under I-930, a held
  connection is presence, and only bytes count.
- **Phone-driven work counts.** A session the phone drives runs hooks, so
  its turns report `working` and hold the machine awake as a laptop
  prompt does. The `TMUX_PANE` issue (section 3) makes every server
  session report under the `remote-control` window, so one session's
  `Stop` can mark the window `idle` while another works. CPU and network
  cover most of that gap; the fix is to key server sessions by
  `session_id` in `repose-hook`. That needs a change to the hook payload
  contract and its fixtures.
- **A frozen machine is offline in the app.** Nothing from Anthropic or
  Cursor reaches a guest, so the app shows the machine offline until
  something on the I-822 list wakes it. From a phone today the only lever
  is ticking **Never freeze this machine** on the dashboard. Proposed: a
  **Wake** button on the dashboard's project page, which calls the wake
  the api already has (`POST /v1/internal/wake`, behind a user route),
  sized for a phone. Built only if the owner wants a phone wake.
- **After a wake.** Outbound connections idle for 30 minutes at the NAT
  are gone (`lifecycle.md:119`). Interactive sessions retry until they
  reconnect. The server sees a long outage and may exit at once; the loop
  in 4.1 starts it again, and sessions come back if the freeze was under
  about four hours. Both are claims from the docs, *unverified* across a
  real freeze.
- **The nightly snapshot wake** (I-822) reconnects every session once a
  night. That is harmless.

## 6. Cursor

### 6.1 How it would run

- **Package.** The Cursor CLI (`agent`) is not in the base. R2-11 makes
  anything nixpkgs does not package a package repose maintains; nixpkgs
  carries a `cursor-cli` package (*unverified* for a current release).
  It is unfree; the allowlist already has `cursor` (`config.md`), so the
  CLI would join it.
- **Login.** `agent login` on the machine (a URL printed over SSH, like
  Claude's), or a `CURSOR_API_KEY` named secret. Both fit the three homes
  (a login the user makes on the machine, or a named secret). Copying the
  laptop's Cursor login is not proposed: where the laptop keeps it is
  *unverified*, and on macOS it may be the keychain.
- **Service.** `repose-cursor-worker.service`, a systemd user unit,
  `ExecStart=agent worker start`, `Restart=always`, started when
  `cursor_worker = true` and a login exists. No terminal question was
  found in Cursor's docs, so a user unit fits here.
- **Not passed:** `--connect-workspace` (teammates' live access, behind
  Cursor's "Allow VM sharing") and `--sync-dashboard-secrets` (a fourth
  place for secrets).
- **Freeze.** The worker holds one long-lived connection and reconnects
  on its own. The same 6 s and 64 KiB test applies. A run the user picks
  for a frozen machine waits until something wakes it.

### 6.2 Why second

It needs a package, a login flow and an unfree-allowlist entry; Claude's
part needs none of these. The council's five-agents verdict asks for
Cursor's CLI agent anyway; this ships the worker with it.

## 7. Security and the logging rule

- **A second key to the machine.** Today, running a command on a machine
  needs the user's repose login and an SSH certificate. With Remote
  Control on, a signed-in claude.ai session (an unlocked phone, a
  browser session someone took) can also run commands as `dev`, and
  sessions start in `bypassPermissions` (I-250). The machine holds the
  user's named secrets in `/run/repose/secrets`, the copied `gh` login,
  and the shared Claude login, which an agent on any of the user's
  machines can read (I-278). Through cross-session messaging, a session on
  one machine can message sessions on the user's other machines; how far
  that reaches is *unverified*. I-278 kept `settings.json` per machine so
  that a hook planted in one project does not run in another; a message
  between sessions is a softer version of the same path.
- **Mitigations.** (1) The agents page recommends Claude's **Require
  trusted devices**, which binds phone access to an enrolled device and a
  sign-in under 18 hours. (2) The owner decides the starting mode of
  sessions the phone starts: `bypassPermissions`, the same as a laptop
  prompt (I-250), or `--permission-mode acceptEdits`, so commands ask on
  the phone. Agent windows the user started keep their mode. (3) The
  server starts with `--no-chrome`, so a phone session does not get
  Claude in Chrome on the machine unless the user asks. (4) Off is one
  key, and claude.ai's own **Sign out everywhere** ends every session.
- **Transport.** Remote Control and the Cursor worker talk to their
  vendors directly. Repose proxies nothing, reads no transcript, and
  opens no port.
- **Logging.** guestd reports one field per machine, one of `on`, `off`,
  `no_login`, `setup_token`, `org_policy`, `failed`, taken from exit
  codes and the presence of files, never from the window's text. No log
  line, event or sample carries a session URL, a session name, a QR code,
  window contents, or the window's command line. The tmux window's text
  stays in the guest. `docs/ops/OBSERVABILITY.md` already forbids each of
  these; the test in section 9 checks it.

## 8. Docs to change

In the same commit as the code (I-242):

- `apps/web/src/content/docs/agents.md`: a section "Drive it from the
  Claude app": what registers, the consent question in the
  `remote-control` window, that a frozen machine is offline until
  something wakes it, `repose-ask` for decisions while away, Trusted
  Devices, the off switch. Keep the existing setup-token line (:107).
- `apps/web/src/content/docs/cli.md`: `remote_control` and
  `cursor_worker` in the `config.toml` example and table; the
  `repose status` line.
- `apps/web/src/content/docs/lifecycle.md`, frozen machines (:111-121):
  one sentence that the Claude app and Cursor show a frozen machine
  offline.
- `apps/web/src/content/docs/notifications.md`: which pushes come from
  repose and which from the Claude app.
- `apps/web/src/content/docs/troubleshooting.md`: the four `status`
  reasons and their fixes.
- `apps/web/src/content/docs/secrets.md`: `CURSOR_API_KEY`, with Cursor.
- `docs/features/agents.md` (:154-183 and a new section), `docs/features/notifications.md:351-354`,
  `docs/DESIGN.md` §13, `docs/workstreams/13-notifications.md:36-37`:
  repose now starts Remote Control; it still never proxies it.
- `docs/interfaces/`: the guestd status field and, for the hook fix, the
  hook payload's `session_id`, with the old shape accepted for one
  release.
- The agent guide (`nix/guest/base/agent-guide.nix`): one line, part (2)
  only.
- `docs/LANDING.md` stays as it is unless the owner lifts its rule.

## 9. Order, tests and closing evidence

1. **Measure** (no code shipped). On host-stg-01, a large machine with the
   owner's login, Claude Code at the base's version and at the newest:
   one `claude remote-control` window and three auto-connected agent
   windows, all idle, for 30 minutes. Evidence: per-minute `cpu_ns` and
   `net_rx + net_tx` from `meter_samples`, and the machine shown `frozen`
   in `repose ls`. Then 45 minutes frozen, `repose attach`, and the time
   until the app shows the sessions online, with the server window's exit
   and restart. Repeat with the Cursor worker.
2. **Part (1).** `remoteControlAtStartup` in the platform settings and the
   merge rule. Tests: `agent-setup` adds the key to a file with none,
   keeps a laptop `false`, adds nothing when `disableRemoteControl` is
   set or when `remote_control = false` arrived. Evidence: the jq merge's
   test output and a VM check that the key is in the file.
3. **Part (2).** `repose-remote-control`, the window, the restart loop,
   the status field, the config key in `internal/cli/config.go`, and the
   `cli.md` rows (`internal/cli/docs_test.go` fails without them). VM
   check `guest-remote-control` with a stub `claude` that records its
   argv: the window opens with `remote-control --spawn worktree --name
   SLUG`, does not open with a setup-token secret, an empty login file or
   the key off, and reopens after the stub exits. Live evidence: the
   session in the app from a phone, a prompt sent from it, `repose ps`
   showing `working`, and a worktree branch fetched with `git fetch
   repose`.
4. **Hook key.** `session_id` in the hook payload, fixtures under
   `internal/guestd/hooks/testdata/claude/`, and a test where two server
   sessions report `working` and `Stop` in turn and the machine stays in
   use.
5. **Wake button** (if chosen), with the dashboard at 390 wide in both
   colour schemes (CLAUDE.md, "Judge visuals at real size").
6. **Cursor.** Package, unfree allowlist, unit, secret, docs. Live: a run
   picked for the machine from cursor.com/agents completes, and the unit
   restarts after a freeze.

Before queueing each step: the `docs/CHECKLIST.md` greps, a grep of api,
hostd and guestd logs on staging for `claude.ai/code` and `cursor.com`
with zero hits, and the three audits in the owner's hand-off routine.

## 10. Risks

- **The idle cost blocks the freeze.** Then default on costs host CPU for
  every Claude user, which I-818 was built to save. Step 1 decides it.
- **Vendor churn.** The Remote Control page has version gates from
  2.1.154 to 2.1.318 and names that changed within two months. A base
  bump re-runs the step 3 live check, as I-278's trace already runs.
- **Consent unseen.** A user who never attaches never answers the
  consent question, and the server window waits. Part (1) may have the
  same gate (*unverified*). The `status` line says `waiting for your yes
  in the remote-control window` if guestd can tell; if it cannot, the
  docs say it.
- **Offline confusion.** A user who sees the machine offline in the app
  may think it stopped. The lifecycle page and the wake button answer
  that; a user who wants it always online ticks **Never freeze**.
- **Team and Enterprise.** Remote Control is off until an Owner turns it
  on. The status reason names it.
- **Two hosts.** One login per host (I-278), so a machine on a second
  host stays `no_login` until the user logs in there.
- **Base switch.** A running server keeps the old Claude Code binary
  after a base update. The window restarts it at the next activation;
  the store path stays until garbage collection, so no session fails.
- **Terms.** If Anthropic answers experiment C with a no, the login share
  goes and per-machine logins return; Remote Control still works with
  them.

## Decisions for the owner

1. **Default for Claude.** On if step 1 shows an idle machine still
   freezes (proposed), always on, or off.
2. **Which mechanism.** Auto-connect of agent windows, the server window
   for starting sessions from the phone, or both (proposed).
3. **Where the switch lives.** `remote_control` in `config.toml`, global
   and per project, with Claude Code's own keys winning (proposed); or a
   dashboard checkbox per project with an api column; or both. Approve
   the name `remote_control`.
4. **Permission mode of sessions started from the phone.**
   `bypassPermissions`, as on the laptop (I-250), or `acceptEdits`.
5. **Frozen machines and the phone.** Respect the freeze and add a
   dashboard **Wake** button (proposed), respect it with no button, or
   never freeze a machine with Remote Control on.
6. **A question asked inside Claude.** Leave it to the 15-minute rule and
   point agents to `repose-ask` (proposed), or hold the machine awake for
   a time while a Claude agent is `needs_input`.
7. **Spawn mode.** `worktree` (proposed) or `same-dir`.
8. **Cursor.** Build after Claude's part is measured (proposed) or with
   it; Cursor CLI in the base for every machine or only where
   `cursor_worker = true`; login by `agent login` and `CURSOR_API_KEY`
   only (proposed).
9. **Trusted Devices.** Recommend it on the agents page (proposed), or
   say nothing.
10. **Scope of the amendment.** A decision entry that amends DESIGN.md
    §13 and workstream 13 ("the platform does nothing to them"), and
    whether `docs/LANDING.md` keeps "remote control" off the landing.

## Sources

Checked 2026-11-10.

- Claude Code Remote Control: https://code.claude.com/docs/en/remote-control
- Settings named there (`remoteControlAtStartup`, `disableRemoteControl`, `dialogExpiry`): https://code.claude.com/docs/en/settings-reference (linked from the page above; the keys' wording is from the Remote Control page)
- Cursor Self-Hosted Machines: https://cursor.com/docs/cloud-agent/bring-your-own-machine and https://cursor.com/docs/cloud-agents/self-hosted
- Cursor blog, self-hosted machines (2026-09-02): https://cursor.com/blog/self-hosted-machines
- Cursor for iOS: https://cursor.com/docs/cloud-agent/web-and-mobile
- `--idle-release-timeout` default 3600 s: search snippet citing https://cursor.com/docs/cloud-agent/self-hosted-guides/pool (*unverified*, page not opened)
- Council report: `reports/2026-11-04-product-value-council.md`, future item 10, item 13, the innovation card
- Repo: `nix/overlay/agents/wrap.nix`, `claude-code.nix`, `agent-setup.nix`, `repose-hook.nix`, `versions.json`; `nix/guest/base/claude-auth.nix`; `internal/cli/mux_tmux.go`, `internal/cli/config.go`; `internal/api/resting/resting.go`, `internal/api/idle/idle.go`; `internal/guestd/hooks/hooks.go`; `docs/features/agents.md`, `docs/features/notifications.md`, `docs/DESIGN.md` §13, `docs/workstreams/13-notifications.md`, `docs/LANDING.md`; public docs `agents.md`, `cli.md`, `config.md`, `lifecycle.md`
- DECISIONS: R1-5, R2-8, R2-11, I-196, I-242, I-243, I-250, I-256, I-262, I-272, I-278, I-283, I-306, I-422, I-425, I-464, I-484, I-486, I-501, I-556, I-818, I-820, I-822, I-886, I-898, I-930
