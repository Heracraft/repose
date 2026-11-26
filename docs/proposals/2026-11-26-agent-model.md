# Proposal: an agent is a session, and a terminal is optional

**Status:** proposal for the owner's judgment. Docs only; no product code.
research agent-model, 2026-11-26. Prompted by the owner ("Perhaps we
should challenge our own tmux-centred design") and by card ENG-255.
Nothing here was run against a live system. Code references are to
`main` at `5afac9a3`. Prior art is cited by URL and was read on
2026-11-26.

## Headline

- **Today an agent is a tmux window (or a herdr pane) that repose can
  see.** `GuestSignals.agents` is built from the watcher's windows alone
  (`internal/guestd/sample/watcher.go` `Signals()`), and `AgentProc` is
  `{agent, tmux_window, state}` (`proto/repose/hostd/v1/hostd.proto:582`).
  Every surface reads that list: the dashboard's Agents card, `repose
  ps`/`ls`/`status`, `say`, the stop question, the rest rule, the OOM
  rules and the restart reopen.
- **That model is already wrong in four places, not one.** ENG-255 (a
  Claude Desktop session is invisible) is the visible one. The same root
  cause also lets the rest rule freeze a machine under a working
  windowless agent, leaves Desktop's Claude without OOM protection, never
  closes its questions on a prompt, and reports every Remote Control
  session under one window (section 2).
- **Recommendation: an agent is one running agent process, named by
  `pid:starttime`, carrying the agent's own conversation id when its
  hooks report one, and a terminal (tmux window, herdr pane) only when it
  has one.** Hooks are the primary discovery, because they already reach
  guestd from every Claude Code that reads `~/.claude/settings.json`
  (ENG-255's evidence). The tmux and herdr scans stay, as a second source
  that attaches a terminal to an agent and finds hookless ones.
- **This is the pattern the agents' own vendors use.** Claude Code's
  background sessions are a supervisor plus session ids with `claude
  attach` as an optional view; Codex's TUI is one client of an
  app-server keyed by thread id; ACP keys on `sessionId`; Coder deprecated
  its terminal-scraping AgentAPI for a control-plane chat model (section 4).
- **tmux stays, as the terminal repose gives the agents it starts.**
  `repose run`, `attach`, trigger runs, typed `say` for agents with no
  inbox, presence by `client_activity` and `ps -n` keep using it. What
  changes is that tmux stops being the identity.
- **Cost: about 12.5 days in nine items. Stage 1 is 6.5 days and closes
  ENG-255; a 2-day thin cut fixes the list and the rest rule alone**
  (section 9). The old shape is accepted for one release.

## 1. What the model is today

| Surface | What it keys on | Where |
|---|---|---|
| Agent list (dashboard, `ps`, `ls`, `status`) | tmux window name or herdr key | `watcher.go` `Signals()`; `api/http/projects.go:201`; `AgentsCard.svelte` |
| Agent state | hook record under the window name | `watcher.go` `RecordHook`, `keyFor` |
| Hook ingest | `TMUX_PANE` of the caller, or `herdr:<pane>` | `hooks/hooks.go` `windowOf`, `windowOfCaller` |
| Windowless hook | relayed as an event, no state | `guestd.go` `onHook` (prompt with `window == ""` returns at once) |
| Agent process (questions, OOM) | nearest ancestor whose comm or exe basename is the agent's binary | `hooks.go` `agentProc`; `agentproc.Binaries` |
| `say` | window (`says.tmux_window`), typed with `internal/agentprompt` | I-1144, I-1356; `api/say/say.go` |
| Questions | `AgentProc` (`pid:starttime`) or window | `hooks.go` `askedFrom`; I-690 |
| Stop question, rest rule | `meter_samples.agents[].state` | I-694; `api/idle/idle.go` `UsedSQL`; `api/resting/verdict.go` |
| Restart reopen | `~/.repose/agent-windows`, conversation id in pane option `@repose-session` | I-636, I-640; `cmd/repose-hook/session.go` |
| Trigger run | run id, one tmux window `t-<trigger>` | I-1256, I-1356 |

The decisions behind it: R3-14 + R4-10 put an agent's TUI in a tmux window
and rejected headless print mode because there would be "nothing to attach
to". I-606 made agents "addressed by window". I-506 said a hook whose
window resolves to no pane "changes no agent's state". I-1144 chose "the
agent's own terminal ... not a headless mode and not an agent's own API"
for `say`. I-1256 chose a window for trigger runs because `repose-ask`,
`say` and `attach` keep working in it. I-1608 made the agent's name come
from the hook payload, and recorded, correctly, that "the dashboard's
agents list still show[s] tmux windows only".

Each of those was right for the agents repose started. None of them
covered an agent that something else starts on the machine.

## 2. What breaks, from the code

1. **ENG-255: a Desktop session never shows as an agent.** Confirmed on
   shule-id by the card: the hooks reach guestd (journal: `agent prompt
   received` then `agent hook received`, agent=claude), `window` is `""`,
   `onHook` records nothing, and `Signals()` lists only windows.
2. **The rest rule cannot see a windowless agent at work.** The rule's
   agent term is `exists (... agents ... where state not in ('idle',
   'needs_input'))` (`idle.UsedSQL`, `resting.verdict.go:89`), and
   `agents` is the window list. Desktop's server is daemonised (parent
   pid 1, per ENG-255), so its SSH connection is not a reliable presence
   term either. What still holds the machine awake during a windowless
   turn is the CPU term (15 s a minute, I-1230) and the network term.
   *Inferred from code, not observed:* a turn that waits on the model with
   little CPU can rest the machine mid-turn. Next check: `rest_verdicts`
   rows for shule-id during a Desktop turn.
3. **Desktop's Claude is not OOM-protected.** The OOM priority (I-200)
   reads `agentPanes`, the windows' pane pids, plus herdr's tree.
   `agentproc.Binaries["claude"]` is `{"claude"}`, and Desktop's binary's
   comm is `2.1.330-3cc8a86` (ENG-255). So no rule finds it.
4. **A prompt in Desktop closes none of its questions.** I-690 closes the
   questions asked from the prompting agent's process. `agentProc` walks
   up by binary name and finds nothing for Desktop's CLI, and `window` is
   `""`, so `askedFrom("", "")` matches no question.
5. **Remote Control sessions are one agent.** `features/agents.md`:
   "every session the server spawns inherits the server's `TMUX_PANE`, so
   they all report under that one window." Two phone sessions, one working
   and one idle, show as one state that flips between them.
6. **The restart reopen depends on a pane option.** I-636 reads the
   conversation id from `@repose-session`, which `repose-hook` sets only in
   a tmux pane. That is fine for what it reopens, and it means the
   conversation id lives in tmux, not in repose's own state.

The board has no card for items 2 to 5 (`kaneo ls`, 2026-11-26). A note
on ENG-255 points here; whoever builds stage 1 files them with their
evidence.

## 3. The ways agents run on a machine, and what each gives repose

| How the agent runs | Process | Terminal repose can see | Hooks reach guestd | Conversation id in hooks |
|---|---|---|---|---|
| `repose run -p`, `attach` and a hand-started agent in a tmux window | the agent's binary | tmux window | yes | Claude `session_id`, Codex `thread-id` |
| herdr project | the agent's binary | herdr pane | yes (I-506) | same |
| Trigger run | the agent's binary | tmux window `t-*` | yes | same |
| Claude Desktop "Open in Claude" (I-1606) | `~/.claude/remote/ccd-cli/<version>`, parent Desktop's server, then pid 1 | none | yes (ENG-255, I-1608) | `session_id` |
| Remote Control server (`claude remote-control`) | children of the server | server's window only | yes | one per session |
| VS Code or JetBrains over SSH | the extension's own Claude Code or Codex | none (the IDE's terminal is the IDE's) | yes for Claude Code if it reads the user's settings (unmeasured) | `session_id` |
| `claude -p`, Agent SDK, `claude --bg` | `claude` | none | yes (hooks run in `-p` too) | `session_id` |
| opencode, codex, gemini, pi started outside tmux | the agent's binary | none | the agent's own hook config | codex only |

The pattern in the table: every way of running an agent gives repose a
process and, for every hooked agent, a hook with an id. Only some give it a
terminal. A model keyed on the terminal sees a shrinking share of what
runs.

## 4. Prior art

**Claude Code itself.** Every hook receives `session_id`,
`transcript_path`, `cwd` and `hook_event_name`, and `SessionStart` fires
with `startup`, `resume`, `clear`, `compact` or `fork`, `SessionEnd` with
`clear`, `resume`, `logout`, `prompt_input_exit` or `other`
([hooks](https://code.claude.com/docs/en/hooks)). Background sessions
are run by "a separate supervisor process", listed by `claude agents
--json` with `id`, `pid`, `sessionId`, `state` (`working`, `blocked`,
`done`, `failed`, `stopped`) and `status`, and a terminal is attached
only on demand with `claude attach <id>`; "Detaching never stops a
background session"
([agent view](https://code.claude.com/docs/en/agent-view.md)). A session
is "a saved conversation tied to a project directory", resumable by id,
and two processes resuming one id "both write to one transcript" and lose
messages ([sessions](https://code.claude.com/docs/en/sessions.md)).
Remote Control keeps the session in the local process and makes the phone
and browser clients of it
([Remote Control](https://code.claude.com/docs/en/remote-control.md)).

**Claude Code's per-session inbox.** Since v2.1.224 each session binds a
Unix socket and exports its path to hooks as
`CLAUDE_CODE_MESSAGING_SOCKET`, with a token in
`CLAUDE_CODE_MESSAGING_TOKEN`. A `claude -p` session binds one too. "The
receiving Claude reads the message between tool calls during an active
turn. When the receiving session is idle, Claude Code starts a new turn
with the message." Inbound control is `crossSessionInbound` (`accept`,
`hold`, `refuse`); with no value, a session in `bypassPermissions` holds a
message from a sender that does not say it bypasses too, and Desktop and
VS Code cannot show the approval dialog, so a held message is dropped
after `dialogExpiry` (5 minutes). A message is marked as from another
session: "It can't approve anything" and permission prompts still fire
([cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging.md)).
The doc names this socket for "a script or hook to post into a session",
but gives only the auth line of the wire format.

**Claude Code channels** push events into a running session through an
MCP server that declares `claude/channel` and sends
`notifications/claude/channel`; events queue and arrive on the next turn.
It is a research preview, needs `--channels` at launch (or
`--dangerously-load-development-channels` for a custom server), and the
flag is ignored under `-p` and the Agent SDK
([channels reference](https://code.claude.com/docs/en/channels-reference)).
repose cannot add a flag to Desktop's launch, so channels do not reach the
sessions this proposal is about.

**Codex.** The app-server is "the interface Codex uses to power rich
clients": JSON-RPC over stdio or a Unix socket, with threads (`thr_...`),
turns and items, `thread/start`, `thread/resume`, `turn/start`,
`turn/steer`, `turn/interrupt` and `turn/started`/`turn/completed`
notifications. The VS Code extension is its client, and the TUI can
connect to a remote app-server with `codex --remote`
([app-server](https://learn.chatgpt.com/docs/app-server)). The terminal is
one client of a thread, not the thread.

**Agent Client Protocol (Zed).** "The session ID returned by `session/new`
is a unique identifier for the conversation context"; `session/load`
replays it, `session/prompt` and `session/cancel` act on it, and
`session/update` streams it
([ACP session setup](https://agentclientprotocol.com/protocol/session-setup)).
No terminal in the model.

**Coder.** AgentAPI ran an in-memory terminal emulator, typed API calls as
keystrokes and parsed the screen into messages; its limitation was that
cleaning messages "depends on recognizing TUI elements", and it is now
"deprecated and no longer maintained"
([coder/agentapi](https://github.com/coder/agentapi)). Coder Tasks reported
status through an MCP server the agent runs (`coder exp mcp server`,
`CODER_MCP_APP_STATUS_SLUG`) and is replaced by Coder Agents and a Chats
API whose state is in Coder's database, not in the workspace
([custom agents](https://coder.com/docs/ai-coder/custom-agents),
[Tasks to Chats migration](https://coder.com/docs/ai-coder/agents/tasks-to-chats-migration)).

**Cursor background agents** are an id with a status (`CREATING`,
`RUNNING`, `FINISHED`, `ERROR`, `EXPIRED`), a conversation and
`POST /v0/agents/{id}/followup`
([agent status](https://docs.cursor.com/background-agent/api/agent-status),
[add followup](https://docs.cursor.com/background-agent/api/add-followup)).

**tmux-based managers.** claude-squad gives each agent "isolated terminal
sessions" in tmux plus a git worktree
([claude-squad](https://github.com/smtg-ai/claude-squad)): the model repose
has today, and it fits a tool whose only job is to start agents. herdr is
the same with its own agent keys (I-509).

**Process supervisors.** systemd names a unit, not a terminal, and finds
its processes by cgroup; a terminal is an optional property of a unit
([systemd-run](https://www.freedesktop.org/software/systemd/man/latest/systemd-run.html)).
hostd already reconciles guests this way (`systemctl list-units 'guest@*'`).

What the prior art agrees on: the identity is the session (an id the agent
issues) run by a process; status comes from the agent's own events, not the
screen; a terminal is a view that comes and goes. The products that keyed
on the terminal (AgentAPI, claude-squad) are the ones that only ever start
agents themselves, and the one with the most customers moved off it.

## 5. The model proposed

**An agent is one running agent process.**

- **Key:** `pid:starttime` of the agent's process, the key `agentProc`
  already makes for questions (I-690), so a reused pid never matches.
  Written `agent_id` on the wire.
- **Attributes:** `agent` (claude, codex, opencode, gemini, pi; I-1608's
  rule names it), `session_id` (the agent's own conversation id, when a
  hook carries one; it changes on `/clear` and resume and the key does
  not), `terminal` (`tmux:<window>`, `herdr:<key>` or empty), `origin`
  (`terminal`, `trigger:<run_id>`, or `none`), `state` (I-694's words:
  `working`, `idle`, `needs_input`, `running`, `unknown`), `since`, and,
  for Claude Code, the inbox socket path from its own hook environment.
- **Never stored:** the working directory, a session name, the
  transcript path, or anything else the docs' logging rule forbids. A
  session name is text the user typed (`/rename`), so the list shows
  `claude` and a short id, as `repose ps` shows a window name today.

Why the process and not the conversation id: one Claude process is one
agent the user sees, and its `session_id` changes under it on `/clear`
(`SessionStart` with `clear`). A key that changed on `/clear` would make
one agent leave the list and another arrive. A hookless agent has no id at
all. The conversation id is still what the restart reopen and `claude
--resume` need, so it is kept as an attribute.

### 5.1 Discovery

1. **Hooks, first.** Every hook already reaches guestd with the caller's
   pid (`peerKey`). Add `SessionStart` and `SessionEnd` to the platform's
   `claude-settings.json` (the merge in `internal/cli/carry_claude.go`
   unions the platform file into the user's). A hook upserts the record:
   `SessionStart` creates it, every hook moves its state as today,
   `SessionEnd` removes it. `repose-hook` adds `session_id` from the
   payload (it already parses it, `cmd/repose-hook/session.go`) and the
   inbox socket path from its own environment, which it already reads for
   `TMUX_PANE` and `HERDR_PANE_ID` (I-506: "they read their own
   environment, so guestd reads no new variable").
2. **Finding the process.** `agentProc` walks up by binary name. Keep
   that, and when it finds nothing, take the hook's nearest ancestor that
   is not a shell. That finds Desktop's `ccd-cli` without a list of
   Desktop's version-named binaries. *Measure first:* whether Claude Code
   runs a hook through `sh -c`, and whether that shell execs.
3. **tmux and herdr, second.** The 5-second refresh keeps listing panes as
   today. A pane whose tree holds a recorded agent's pid gives that record
   its `terminal`. A pane with an agent's binary and no record is a record
   of its own (hookless agents, and hooked ones before their first hook),
   as today.
4. **Liveness and reconcile.** Each refresh checks every record's
   `/proc/<pid>/stat` start time; a record whose process is gone is
   removed, so a crash with no `SessionEnd` cleans up within 5 s. Records
   are kept in the run directory, as `agent-hooks.json` is, and checked
   against `/proc` when guestd starts, so a guestd restart loses no agent.
   This is the reconcile rule CLAUDE.md asks of every guest operation.
5. **The agents' own APIs, later and optional.** `claude agents --json`
   lists Claude's background sessions with `pid` and `sessionId`; Codex's
   app-server and opencode's server have their own lists. None is needed
   for discovery, since hooks cover the same sessions. Each is worth
   reading only for what hooks cannot give (Claude's `blocked` state for a
   background session nobody is attached to). Not in the work items.

### 5.2 What each surface does with an agent that has no terminal

| Surface | Agent with a terminal (unchanged) | Agent with no terminal |
|---|---|---|
| Dashboard Agents card, `ps`, `ls`, `status` | as today | listed: agent, state, and `no terminal` where the window name is. `ps -n` (last lines) prints nothing for it, because there is no screen and repose reads no transcript. |
| `repose stop` question (I-614, I-694) | as today | counted the same way: a `working` or `needs_input` agent is asked about. |
| Rest rule | as today | counted the same way, because `agents` now holds it. This closes section 2 item 2 with no SQL change. |
| OOM and nice rules (I-200) | as today | its pid is protected like a pane's agent. |
| Notifications | as today | already delivered (I-1608); the event gains `agent_id`. |
| Presence hold (I-1272) | typed in tmux within 5 minutes | a `prompt` hook from a windowless agent is also "having typed", except a prompt a `say` or a trigger delivered. *Unverified:* whether a Desktop user wants the phone silent while they type in Desktop; this is the reading of I-1272's own sentence. |
| Questions (I-690) | as today | keyed by `agent_id`; a prompt closes them, which fixes section 2 item 4. |
| `say` (I-1144) | typed into the window, as today | for Claude Code: posted to the session's inbox socket (section 4). For any other agent: refused with a named reason, `no_terminal`, and the sentence "AGENT runs outside a terminal repose can type into." |
| `attach` | as today | not opened. Running `claude --resume ID` in a new window would make a second process write the same transcript, which the Claude docs say loses messages. `attach` names where it runs ("claude runs outside tmux on NAME; open it where it was started."). For a Claude background session (`claude --bg`), `attach` runs `claude attach <id>` in a new window: the agent's own attach. |
| Restart reopen (I-636) | as today, with the conversation id read from the record rather than from `@repose-session` | not reopened. The app that started it owns it: Desktop reconnects and resumes its own sessions. |
| Trigger runs (I-1256) | unchanged: a window, keyed by run id | n/a |
| Remote Control | the server's window stays one record (the server process) | each spawned session is its own record with its own pid and state, which fixes section 2 item 5. |

**On `say` through the inbox.** It amends I-1144's "not an agent's own
API" for agents with no terminal only. It is the documented door, needs no
flag at launch, and works for Desktop, `-p` and the SDK. Its delivery
semantics are better than typing: Claude reads it between tool calls or
starts a turn if idle, so the "wait for idle" of I-1144 is the agent's,
not repose's. Three facts need a measurement before it is built:

1. The wire format past the auth line is not documented.
2. Under `bypassPermissions` (the guest's default, I-250) and with no
   `crossSessionInbound` value, a message from guestd is held, and Desktop
   drops a held message after 5 minutes. `repose-agent-setup claude`
   would add `crossSessionInbound: "accept"` where the user's file has no
   such key, the same rule as `defaultMode` (I-250). That lets the user's
   other Claude sessions on the machine message this one, which is a
   widening of what they can already do by typing into a tmux window.
3. Claude is told the message came "from another session, not from you".
   Whether Claude acts on it as it acts on the owner's typed words is
   unknown. If it does not, `say` to Desktop is refused rather than
   delivered as a weaker instruction.

## 6. What tmux remains good for

- **The terminal repose gives the agents it starts.** `repose run -p`,
  `attach`, the dropped-connection reattach (I-469), several clients, copy
  mode, and an agent that outlives the laptop: none of that changes.
- **Typed input for agents with no other door.** opencode, gemini and pi
  have no inbox; `say` and `run -w -p` keep typing into their window.
  Typing has its own cost: ENG-232 (a `say` typed at Claude's trust
  prompt quits Claude) is a failure a socket message cannot have.
- **The presence signal.** `client_activity` (I-1272) is one number and
  reads no pane.
- **Trigger runs.** I-1256's reasons stand: `repose-ask`, `say` and
  `attach` keep working in a window, and a run is already keyed by its id,
  which is the shape this proposal gives every agent.
- **Showing the screen on request.** `ps -n` reads the screen only when
  the user asks for it.

What it stops being: the identity of an agent, the place a conversation id
is kept, and the only list of agents.

## 7. Migration

The rule is CLAUDE.md's: the contract change ships with the code, and the
old shape stays accepted for one release.

- **`AgentProc`** (grpc-hostd.md) gains `agent_id = 4`, `session_id = 5`,
  `terminal = 6`, `origin = 7`. `tmux_window` stays filled for a tmux
  agent. An api that reads an `AgentProc` with no `agent_id` (an older
  guestd) derives `tmux:<window>`, so one release of mixed guests lists
  the same agents as today. `ps --json` gains the same keys beside the
  old ones (as I-606 did for herdr).
- **`AgentEvent`** gains `agent_id` beside `tmux_window` (I-121's field).
  An older hostd drops it, and the api keeps working from `tmux_window`.
- **`says`** gains `agent_id`. `repose say --agent` accepts a window name
  (old) or an agent id (new) for one release, then the id and the names
  `repose ps` prints. The `SayToAgent` vsock command takes either.
- **Questions** gain `agent_id` beside `window` and `agent_proc`.
- **Restart reopen.** `repose-tmux-save` writes `~/.repose/agent-windows`
  in its current format for one release, filled from the records; the
  reopen script reads it unchanged.
- **Hooks.** The platform settings gain `SessionStart` and `SessionEnd`.
  A machine on an older base keeps working without them: its first hook
  of any kind creates the record.
- **Interfaces touched:** `docs/interfaces/grpc-hostd.md`,
  `vsock-guestd.md`, `api.md`, `guest-conventions.md`. Each change ships
  with its code, with a DECISIONS entry that amends I-506, I-606, I-636,
  I-1144 and I-1608 by number.

## 8. How ENG-255 is answered

Desktop's CLI starts, and its `SessionStart` hook reaches guestd from pid
P. `agentProc` finds no `claude` above it, so the nearest non-shell
ancestor, `ccd-cli/2.1.330-...`, is the process: record
`agent_id=P:start`, `agent=claude` (from the payload, I-1608),
`session_id` from the payload, `terminal` empty. Its `UserPromptSubmit`
makes it `working`; its `Stop` makes it `idle`. `Signals()` lists it, so
the dashboard's Agents card shows `claude  working  no terminal`, and
Conversation.svelte stops saying "No agent is running". While it works,
the rest rule counts it and the stop question asks about it. Its process
is OOM-protected. Its questions close on its next prompt. `say` reaches it
through its inbox once the measurement in 5.2 passes, and until then the
dashboard's box says why it cannot. When Desktop ends the session,
`SessionEnd` removes it, and if Desktop is killed, the next refresh does.

## 9. Work items

| # | Item | Days | Stage |
|---|---|---|---|
| 1 | guestd agent registry: record type keyed by `pid:starttime`, upsert from hooks, `/proc` liveness each refresh, kept in the run dir and reconciled at start, `Signals()` from it; tests for a windowless agent, a crash, a guestd restart, `/clear` | 2 | 1 |
| 2 | Hooks: `SessionStart`/`SessionEnd` in the platform Claude settings; `repose-hook` sends `session_id` and the inbox path; `agentProc` falls back to the nearest non-shell ancestor (after measuring how Claude Code spawns a hook) | 1 | 1 |
| 3 | Contracts: `AgentProc`, `AgentEvent`, questions gain `agent_id` and the rest; hostd forwards; api ingest derives the old key; interface docs | 1.5 | 1 |
| 4 | Surfaces that only read: dashboard Agents card and Conversation, `ps`/`ls`/`status` (`no terminal`), `attach`'s refusal sentence, public docs in `apps/web/src/content/docs/` | 1.5 | 1 |
| 5 | OOM and nice rules, and the questions' close, from the registry | 0.5 | 2 |
| 6 | Presence: a windowless `prompt` hook counts as having typed, less `say` and trigger prompts | 1 | 2 |
| 7 | `say` to a windowless Claude: spike on a non-production machine (wire format, held or accepted, how Claude acts on it) 0.5, then build or refuse with `no_terminal` 1.5 | 2 | 2 |
| 8 | Restart reopen and Remote Control from the registry: conversation id from the record, one record per spawned session | 1 | 2 |
| 9 | DECISIONS entries, `features/agents.md`, `run-and-attach.md`, `notifications.md`, the agent guide | 1 | both |
| | **Total** | **12.5** | |

Stage 1 (items 1 to 4, and item 9's share) is 6.5 days and closes ENG-255
and section 2 item 2. A thinner first cut, the registry fed only by
windowless hooks and shown in `Signals()` with the old contract (an
`AgentProc` whose `tmux_window` is empty), is about 2 days; it fixes the
list and the rest rule and leaves `say` and `attach` refusing as today.
That is the cut to take if the owner wants ENG-255 closed this week.

## 10. Counter-evidence

- **The tmux model works for the main path, and nobody has counted the
  others.** `repose run` is how most agents start, and I-1606 is two days
  old. A count of windowless hook events in production (guestd's `agent
  hook received` lines with no window) would say how much of the fleet
  this is. It was not read here (no live systems).
- **Claude's own supervisor may make repose's wrapping redundant for
  Claude.** `claude --bg`, `claude agents` and `claude attach` are
  Anthropic shipping the same layer. repose's case is the other four
  agents and a machine-side view that works whatever harness runs (the
  owner's harness-agnostic rule), which this model keeps. It also means
  repose should read Claude's list rather than rebuild it, if it ever
  needs more than hooks give.
- **`say` through the inbox is not `say` through the terminal.** The
  message is labelled as from another session and may need
  `crossSessionInbound: accept`. If the spike shows Claude treats it as
  less than the owner's words, windowless `say` is refused, and the
  model still stands for everything else.
- **Hooks are only as good as the agent's config.** An IDE extension that
  runs Claude Code with its own settings, or a hookless agent started
  outside tmux, is invisible to step 1 of discovery. The process scan
  finds the hookless ones that run a known binary; an agent with neither
  hooks nor a known binary stays invisible, as it is today.
- **More state in guestd.** A registry is one more thing to reconcile.
  It is smaller than the hook map it replaces (`agent-hooks.json` keyed
  by window), and it reconciles from `/proc`, which cannot disagree with
  itself the way tmux names can (I-1356's `t-` prefix problem).
- **Coder's lesson cuts both ways.** It moved off the terminal, and it
  also moved the agent loop into its control plane. R3-14 rejected
  owning the loop, and this proposal does not reopen that: the agent stays
  the upstream binary, on the machine, in whatever the user started it in.

## 11. Not decided here

- Whether `say` to a windowless agent ships at all (item 7's spike).
- Whether `crossSessionInbound: accept` is a default repose may set.
- How the IDE extensions behave over SSH (whether they read
  `~/.claude/settings.json`), which needs an IDE on a test machine.
- Codex outside tmux: whether its hooks fire a session start; until
  measured, a codex record starts at its first hook.
