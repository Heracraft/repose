# repose as a framework for always-on agents: the approaches (proposal, 2026-11-12)

**Status: thinking, saved. Nothing here is decided and nothing is to be built
from this file.** The owner is repositioning repose from "your dev environment,
replicated in the cloud, that an agent can wreck safely" (I-477, `LANDING.md`
"What we sell") to "a framework for running always-on agents that works with any
harness and any model" (`~/pitch/canon/CANON.md` on kanali, deck v3, 2026-11-11).
This file records what the pitch material and the first non-coding agent say
about the primitives, three approaches, and one sizing idea. Sources: the mining
of kanali's decks and 27 session transcripts and of the `job` machine's
auto-apply agent, both 2026-11-11, by the conductor's sub-agents.

## Summary

| Question | Answer |
|---|---|
| What repose already gives an always-on agent | A persistent machine with the user's logins and checkout, secrets by name on a tmpfs, a browser, `repose-ask`/`repose-notify`, and since r20261111-4 a public webhook route into a port (`hooks.repose.run`) |
| What the first real agent had to hand-roll | Webhook receiver on a port, signature check, dedupe, a cheap gate, a queue, one headless session per event in its own tmux window, a 10 s supervisor, timeouts, a ledger, retries (none) |
| The missing layer | **Trigger → session**: bind an event (a webhook route, a schedule) to a prompt and a working directory, with admission, lifecycle, outcome and state owned by the machine |
| Approach A | The trigger layer on persistent machines (the job skill, productised) |
| Approach B | Per-event machines: a tiny machine booted from a save point for each event, destroyed after (what Vercel Sandbox and Fly Sprites sell) |
| Approach C | A workflow graph with an agent node (n8n's shape) |
| Sizing | A `tiny` class below `small`: 1 vCPU, 2 GB, for agents that drive a browser, call APIs and compile documents; needs OOM rules that spare the agent and the receiver |
| Decide first | A before B: the job agent proves A is one layer away; B needs fork-per-event, which the council killed as a gimmick for development and is the right shape only when events are frequent and stateless |

## 1. What the two machines say

**The pitch** moved in three versions in three weeks: v7 (2026-10-25) "Close the
laptop. The agent keeps working."; v2/v3 (2026-11-11) "a framework for running
always-on agents", the eras staircase (chat → harness → always-on), the first
responder as the example. The owner's private vocabulary had the word since
2026-10-05: "I don't want repose to be one of those things that give you a web UI
for codex and claude. It should give you primitives to run shit persistently."
The landing (`LANDING.md` "What we sell", unchanged since 2026-11-02) still says
the opposite: never mention always-on, phone, collaboration. Unreconciled.

**The job agent** (`job` guest, built 2026-11-11 in one evening): the owner's
recruiting product POSTs `role.created` to a port on the machine; a 150-line
Node receiver verifies the HMAC, de-duplicates, applies a cheap gate (no
sponsorship, clearance, graduation window), queues the event, and a 10-second
loop starts one headless Claude Code session per event in its own tmux window;
the session screens the posting, tailors a resume, submits in the machine's
Chromium, appends a ledger row, and reports with `repose-notify`, asking with
`repose-ask --options` at captchas and logins. One real run so far (an Intel
role screened and skipped, 40 s). The owner then asked for the generic version
and it exists as a skill (`~/.claude/skills/always-on-agent/`): config-driven,
five signature schemes, examples for incidents, CI failures and ticket triage.
By the evening the machine's `repose status` showed `webhooks recruiting 200
body_dropped`, so the delivery now comes in through `hooks.repose.run`.

Of all that, repose gave four things the owner never had to rebuild: the
persistent machine, secrets by name, the browser, and ask/notify. "The premise is
already there: receiving a webhook, sending a message, a browser inside." The
rest was written by hand, and `repose webhooks` stops at the port.

## 2. The primitives, in the order the owner hit them

1. **A reachable, verified event endpoint** whose existence the guest can see
   (`repose webhooks` delivers; the guest learns of the route only from the user).
2. **Event → session**: "every X, run this prompt": a trigger bound to a prompt
   file and a working directory; the payload handed over as a file, untrusted.
3. **Isolation by default**: one session per event, its own window, the
   supervising agent's context untouched ("you do not want to pollute your
   context", owner, 2026-11-11).
4. **Admission before spend**: dedupe on the provider's event id, a declarative
   gate, a concurrency limit. Every accepted event costs a whole agent run.
5. **A lifecycle owner**: start, timeout, kill the window not the multiplexer,
   mark done, take the next, survive a restart.
6. **Secrets by name** (right already; make it the only path, so no Chat
   webhook URL lands in a SKILL.md again).
7. **A blocking human step**: `repose-ask` with options and a timeout; the one
   primitive never rebuilt, and what makes an unattended run safe.
8. **Outcome with a link**, then **state you can query** ("check if any have
   been sent so far?" should be a command, not a Markdown ledger).
9. **Retries and a dead letter**: today a failed session is a file and silence.
10. **A schedule trigger** beside the event trigger, same machinery (parked:
    `2026-11-10-scheduled-prompts.md`; the machine has no cron).

## 3. Three approaches

**A. The trigger layer on persistent machines.** `repose trigger add
role-created --route recruiting --prompt apply.md` (or `--every 'weekdays
07:00'`). The machine owns 2–5, 8–10 above; the user's prompt and skill own the
work. The machine stays the user's: their logins, checkout, browser profile, the
ledger in their repo. This is the job skill, productised; it is one layer away.
Cost: a machine per agent, always allocated (frozen when idle, so cheap, but a
seat). Fit: agents that need the user's state (logged-in browser, a checkout,
accumulated memory files), which every example the owner names does.

**B. Per-event machines.** A template machine holds the setup; each event boots
a copy from its save point, runs one session, reports, and is destroyed. What
Vercel Sandbox, Fly Sprites and Docker Cloud Sandboxes sell, and what `fork` was
(removed 2026-11-11, I-1082: three 8 GB copies filled a Solo plan after the
demo). Cost per event is small and nothing holds a seat between events; state
(the ledger, the browser profile) must live outside the machine. Fit: frequent,
stateless events; wrong for an agent that accumulates a working memory.
Open: boot time (26–30 s on staging today) against the event rate.

**C. A workflow graph with an agent node.** Triggers, branches, HTTP nodes, an
"agent" node (n8n, Dify, Make). The user draws the graph; the agent is a step.
Not repose's shape: the thing the owner sells is that the agent *is* the
workflow, with a whole computer, and the user writes a prompt and a skill, not
a graph. Borrow from C only the admission vocabulary (dedupe, gate, concurrency,
retry, dead letter) and the run log.

**Where A meets B.** A's trigger could carry `--fresh`: run this event on a copy
of this machine's save point and destroy it. That is B as an option of A, not a
second product, and it is the reason to keep `snapshots restore --as` alive.

## 4. A `tiny` class

Measured on `job` (a `large`: 2 vCPU, 8 GB) while its agent idled after a run:
4.4 GB used; four Chromium processes 1.5 GB, two Claude processes 0.6 GB, the
receiver 68 MB, one Python process 1.2 GB. The work it does is drive a browser,
call APIs and compile PDFs; it does not build or run a language server.

A `tiny` at **1 vCPU, 2 GB, 10 GB disk** would hold one agent and one browser if
three things hold: a single Chromium (the agents' shared browser, I-246; the
stray-browser rule, I-1124), no second agent, and OOM rules that spare the
receiver and the agent and kill the browser tab first (I-576's keystroke path
has the mechanism: `MemoryLow` on the slices that matter, systemd-oomd on the
rest). Four `tiny` guests fit one 8 GB seat, so a plan could carry "four
always-on agents" instead of "one large". Burst cores (I-1126) give it a build's
worth of CPU when it needs one. Decide: the memory line (2 GB or 3 GB), whether
`tiny` may run a second agent at all, and the price.

## 5. Tensions to settle

- Landing vs deck: one has to give; the product is now closer to the deck.
- "It's just a machine" vs "a framework with named parts": the named part that
  answers "this sounds like Hermes" is the trigger, not the machine.
- Non-developers: the job agent is the first use that is not a developer's, and
  it still needed the owner at a terminal for captchas (`repose-ask` is the
  bridge; the phone reply path is the gap, I-1146).
- The owner's goal (`owner-goal-recognition`): recognition and a hire, not a
  startup; that argues for the trigger layer and the job agent as the public
  demo over pricing work.

## Decide next

Approach A as the first build (triggers: event and schedule, admission,
lifecycle, outcome, state), with B as `--fresh` later; `tiny` as its own
proposal with a measured memory line; and the landing's "What we sell" rewritten
as a decision, or kept and the deck told it is a promise, not a description.
