# Two people, one agent (proposal, 2026-11-11)

**Status: parked for later. Nothing here is decided; nothing is to be
built now.** This records the owner's experience of 2026-11 and the
research of 2026-11-10 so that neither is lost. No DECISIONS id is
reserved and no code is written.

Repo facts carry a `file:line` from `main` at `4e0b926c`. Outside facts
were checked on 2026-11-11 and carry a URL in "Sources".

## Summary

| Question | Where this lands |
|---|---|
| What broke | Two people attached to one machine and tried to prompt one agent. Their keystrokes went into the same input box and corrupted both prompts |
| What I-1067 already fixed | The window and the size fight, not the input box (`docs/DECISIONS.md:31012`) |
| What the need really is | Shared view plus taking turns. Not simultaneous typing |
| Why not simultaneous | One agent TUI has one input box and one turn. No working design lets two people type into it at once |
| What every working design does | Serialises prompts through one host and tags each by sender (claude-share, claude-duet, Delta, Happy) |
| Proposed shape | A second person appends a message; messages are delivered in order, each prefixed `[from <name>]`, when the agent is idle or waiting |
| Shared view | `repose attach` already multiplexes clients (`apps/web/src/content/docs/run-and-attach.md:90`), or the dashboard |
| Access | A per-project grant, `repose share NAME --with <email>`. Not a team (`docs/DESIGN.md:49-52`, R5-6) |
| Demand | Real and small: 35 votes on one issue, several third-party tools, no official feature |
| Size | Not estimated. The grant is the large part, not the delivery |

## 1. What happened

At a demo in 2026-11, two people shared one account and attached to one
machine from two laptops. I-1067 (`docs/DECISIONS.md:31012`) records the
first half of the problem and fixes it: both terminals were clients of
one tmux session, so switching a window moved the other person too, and
the window took the size of whichever terminal was active last. Each
attach is now its own session grouped with the project's, with
`aggressive-resize on`, so each laptop picks its own window and a window
is sized for the terminals showing it.

The second half is not fixed and cannot be fixed in tmux. When both
clients show the same window, tmux delivers every client's keys to that
one pane. Two people typing a prompt into one agent interleave character
by character, and both prompts are destroyed. The public docs already
describe the shared-window case and say nothing about typing into it
(`apps/web/src/content/docs/run-and-attach.md:90`).

## 2. The need is turns, not simultaneity

An agent TUI has one input box and one turn. There is nothing to make
concurrent. Two people who want to work with one agent want:

1. **To see the same thing.** Both watch the turn as it runs.
2. **To take turns prompting.** One writes, then the other adds to it or
   corrects it, and the agent reads them in the order they were written.

Simultaneous typing into one box is not a goal anybody has. It is the
failure mode, not the feature.

## 3. What works elsewhere

Every multi-user design that works today serialises prompts through one
host and attributes each one. None of them splits the input box.

| Design | How two people prompt |
|---|---|
| claude-share (Paradigm-Study) | A guest's prompt goes through the room server to the host's Claude Code, "which runs it as a turn of its own". Every edit, command and network call a guest's prompt makes asks the host first |
| claude-duet (EliranG) | One host, one joiner by offer code. The session lives on the host's machine |
| Delta | `/delta share` makes a link, `/delta join` joins the thread. One thread, one agent |
| Happy (slopus/happy) | Multiplayer on the owner's own machine: both watch the same agent and either can steer it |
| anthropics/claude-code#60082 | Asks for the feature. Anthropic ships read-only sharing only |

Two facts matter for repose. The host is always one machine, and the
machine is the thing repose sells. And attribution is always present:
without `[from <name>]` the agent, and the other person, cannot tell who
asked for what.

## 4. The shape

**Delivery.** A second person with access appends a message to a named
agent window. The message is held and delivered when that agent is
`idle` or `needs input` (`apps/web/src/content/docs/agents.md:82-89`),
never into a `working` turn. Several messages are delivered in the order
they were written. Each is prefixed `[from <name>]`, so the agent and
the owner both read who asked.

Nothing new is needed inside the guest to type a message. `repose run -w
WINDOW -p TEXT` already types a prompt and Enter into a running agent's
window (`apps/web/src/content/docs/cli.md:42`,
`internal/cli/prompt_submit.go:126-127`). What is missing is the queue in
front of it, the idle check, and the sender's name.

**Shared view.** Nothing new. Several terminals attach at once, from one
computer or several; they share the windows and each picks its own
(`apps/web/src/content/docs/run-and-attach.md:90`, I-1067). The
dashboard is the second view for a person with no CLI.

**Access.** A per-project grant, not a team:

```
repose share todo-app --with ana@example.com
repose share todo-app --list
repose share todo-app --revoke ana@example.com
```

One user is one Logto identity with one card and many projects, and
there are no teams in the first release (`docs/DESIGN.md:49-52`, R5-6 at
`docs/DECISIONS.md:268`). A grant is deliberately smaller than a team: it
names one project, one person and what they may do, and it is revoked in
one command. It changes no billing: the project's memory already counts
against its owner's plan.

**What the grant does not carry.** The second person prompts and watches.
They do not read or write secrets, do not create, stop, resize or destroy
the machine, do not touch billing, and do not see other projects. The
default is the smallest grant that serves the case in section 2.

## 5. Where it sits beside other items

- **It is not the council's prompt queue.** Future item 7, "Prompt queue
  per agent: prompts start when the turn ends"
  (`reports/2026-11-04-product-value-council.md:224`, pull 4.5,
  retention 7.0, novelty 5.2, size S, 3 build-next votes), is one
  person's work backlog that drains itself so a worker does not sit idle
  overnight. This is two people taking turns. They share a mechanism —
  hold a message until the turn ends — and they are different products.
  If item 7 ships first, this sits on top of it and adds the sender's
  name and the grant.
- **It is not a chat UI.** The 2026-11-10 phone research
  (`docs/proposals/2026-11-10-remote-control-host.md`) chose the vendors'
  own apps as the way to prompt a machine from elsewhere and proposed no
  repose chat surface of its own. The landing page is told not to mention
  collaboration at all (`docs/LANDING.md:27-29`).
- **It is not remote control.** Remote Control is one person reaching
  their own machine from a phone. This is a second person reaching
  someone else's machine.

## 6. Demand

Real and small, and it is worth saying both halves.

- One Anthropic issue, opened 18 May 2026, open on 2026-11-11, 35 `+1`
  reactions and 19 comments (anthropics/claude-code#60082). Its cases are
  pair programming, mentoring, code review and handovers, between people
  who are not in the same room.
- At least four third-party tools exist for it (section 3), which says
  the need is felt and that nobody has to wait for repose to meet it.
- The owner's own case, the club meeting, is **co-located**: two people
  in one room looking at one screen. For that case shared view is most of
  the value and the grant is almost none, because one laptop is already
  signed in.

Those two cases pull in different directions. The remote pairs need the
grant and the attribution; the co-located pair needs only a second,
non-destructive input path. A build that serves only the co-located case
is much smaller and much less useful.

## 7. Open questions, not answered here

1. **Who the second person is.** A Logto account of their own, invited by
   email, or a signed link with no account. An account is auditable and
   revocable and costs a signup; a link is instant and is a bearer
   credential on someone else's machine.
2. **What they may do beyond prompting.** Section 4 proposes prompt and
   watch, with no secrets and no lifecycle. Whether they may attach a
   real terminal at all is the sharpest part: a shell on the machine is
   not a smaller permission than prompting an agent, it is a larger one.
3. **The presence hold.** I-689 (`docs/DECISIONS.md:21052`) holds an
   agent notification while the owner is present, where present is the
   project's latest sample showing a tmux client or an SSH session. A
   second viewer attached makes the project look present, so the owner's
   phone goes quiet while somebody else watches. Presence must count the
   owner's own clients, or the hold must be told which person it is
   holding for.
4. **Attribution inside the agent.** `[from <name>]` is a prefix on the
   prompt, which the agent reads as text. There is no field for it. Which
   name is used — the grantee's display name, their email, a nickname the
   owner set — is a privacy question as well as a design one.
5. **What the owner sees.** Whether a message from the second person
   raises an event and a notification for the owner, and whether the
   message text is tenant content stored like `events.summary`, or never
   stored at all. The logging rule forbids a prompt in any log field
   (CLAUDE.md, "Never log what a tenant typed").
6. **Revocation mid-turn.** What happens to a queued message and to an
   attached terminal when the owner revokes the grant while the second
   person is attached.

## Sources

Outside facts checked 2026-11-11.

- anthropics/claude-code#60082, "Feature request: real-time multi-user
  collaboration on a single Claude Code session", opened 18 May 2026,
  open, 35 `+1` of 38 reactions, 19 comments:
  https://github.com/anthropics/claude-code/issues/60082
- claude-share (Paradigm-Study), multiplayer sessions for Claude Code:
  https://github.com/Paradigm-Study/claude-share
- claude-duet (EliranG), two devs one Claude over an offer code:
  https://github.com/EliranG/claude-duet
- Delta, `/delta share` and `/delta join`:
  https://delta.dev/blog/claude-code-multiplayer
- Happy (slopus/happy), agents on a machine you own, invited viewers who
  can steer: https://github.com/slopus/happy and
  https://happy.engineering/docs/features/
- Repo: `docs/DECISIONS.md:31012` (I-1067), `:21052` (I-689), `:268`
  (R5-6); `docs/DESIGN.md:49-52`; `docs/LANDING.md:27-29`;
  `internal/cli/prompt_submit.go:126-127`; `internal/cli/run.go:1195-1219`;
  `apps/web/src/content/docs/run-and-attach.md:90`;
  `apps/web/src/content/docs/cli.md:42`, `:53`, `:404`;
  `apps/web/src/content/docs/agents.md:82-89`;
  `reports/2026-11-04-product-value-council.md:224`;
  `docs/proposals/2026-11-10-remote-control-host.md`.
