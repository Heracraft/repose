# Notifications

When an agent finishes or gets stuck, the user hears about it on their phone
or in their inbox, whichever agent it was. Agents' own notification features
keep working on top.

## What the user sees

```
$ repose notify set --ntfy https://ntfy.sh/heracraft-repose-8f3a
email: on
ntfy: https://ntfy.sh/heracraft-repose-8f3a

$ repose notify test
email: ok
ntfy: ok
```

`notify set` takes `--email on|off` and `--ntfy URL|none` (I-8) and sends
no test; `notify test` does, and exits 1 when neither channel answered ok.

On the phone (ntfy):

```
repose · todo-app
claude finished: "Added auth flow, 14 tests green, committed 3f9e2a1"
```

```
repose · todo-app
codex needs input: "Should I drop the legacy sessions table?"
```

In `repose status`:

```
todo-app  running 3h12m  large
  agents     codex needs input
  ...
  last event 2m ago, codex needs input "Should I drop the legacy sessions table?"
```

## Behaviour that must hold

Events (see agents.md for how each agent produces them):

- Kinds: `completed`, `needs_input`, `error`, plus the platform-originated
  `billing_stopped`, `base_updated`, `base_update_failed`, `snapshot_failed`,
  `destroy_failed` (DECISIONS I-165: the CLI no longer waits for a
  destroy, so its failure is announced), `personal_failed` (I-490: the
  account's machine.nix did not build or switch on that project, which
  keeps its revision), `boot_failed` (I-590: a start or forced reboot
  gave the machine a new system that never reached Ready, and it runs its
  previous one; title `<project>: new system did not boot`), `host_moved` and
  `abuse_stopped` (I-239: the guest was stopped because a cryptocurrency
  miner was running; the summary says which process and, on the third
  stop in 24 hours, that the project cannot start until reviewed),
  `idle_running` (DECISIONS I-262: a running guest with no SSH session,
  no tmux client and no working agent for 24 hours (a laptop herdr's
  bridge is an SSH session, I-511); once per idle
  stretch, title `<project>: unused for 24h, holding plan memory` since
  I-617, never a stop),
  `temp_expiring` and `temp_destroyed` (DECISIONS I-347: a temporary
  machine an hour before its end, titled `<project>: destroyed in an
  hour`, and its end, `<project>: temporary machine destroyed`), and
  the agent-sent `agent_message` and `agent_question` (DECISIONS I-244,
  below). Each carries the agent name (agent kinds only), the
  tmux window or herdr agent key (I-504), a summary of at most 1 KB, and
  a timestamp. On a herdr project, herdr's `blocked` sets gemini's and
  pi's state to needs input without a notification; their finished turns
  notify as `completed`.
- The summary is what the agent's hook provided, truncated. It may include
  the agent's own last message. It never includes the prompt the user typed
  or terminal contents beyond what the hook payload carries.
- Heuristic events (pane idle for agents without hooks) are labelled as
  such in the message: `gemini went idle` rather than `gemini finished`.
  As shipped, the heuristic never reads pane *text* at all — only whether
  the pane's process tree has used CPU recently — which is stronger than
  "never sent", not weaker (DECISIONS I-49).
- An event reaches the API within 10 seconds of the hook (90 seconds for
  heuristics), and the outbox worker picks up undelivered rows every 2
  seconds. Delivery results are stored per channel on the event row.
- Duplicate suppression: the same `(project, agent, kind)` within 60
  seconds is one event, with a later summary appended to the first rather
  than dropped. A Claude `Stop` hook that fires twice for one turn is the
  reason.
- Rate cap: at most 30 notifications per project per hour; past that, one
  message says notifications are paused for this project until the top of
  the hour, and events are still recorded (never dropped, just not
  delivered past the cap).

Agents can message you and ask questions (DECISIONS I-244, I-245):

- `repose-notify TEXT` in any guest shell sends TEXT (1 KB) as an
  `agent_message`: title `<project>: <agent> says`, ntfy priority 3. It
  returns at once. Messages are never collapsed into each other and count
  against the 30-an-hour cap like any event.
- `repose-ask [--options A,B,C] [--timeout 30m] QUESTION` sends an
  `agent_question` (title `<project>: <agent> asks`, ntfy priority 5) and
  blocks until the owner answers, then prints the answer. Exit codes: 0
  answered, 1 guestd unreachable, 2 usage, 3 timeout, 4 no channel on, 5
  cancelled (dismissed, guest stopped, or the question is gone after a
  reboot), 130 interrupted. At most 3 options; timeout 30 minutes by
  default, 24 hours at most.
- The owner answers from ntfy (one `http` button per option, calling a
  signed reply link; no login), email (one link per option, which opens a
  page whose button answers; replying to the email does nothing), the
  dashboard's project page (buttons or a text box, and Dismiss), or the
  laptop: `repose questions` lists the waiting ones, `repose reply
  [PROJECT] [ANSWER]` answers, and lists instead of guessing when several
  are waiting. The first answer wins everywhere; a later one is refused
  with the answer that won.
- The answer reaches the waiting command within seconds over the
  hostd↔guestd channel, and survives a hostd, api or guestd restart; a
  guest that stops or reboots ends its questions.
- Question and answer text are tenant content: shown to the owner, never
  logged, and stored like an event summary.

Channels (`workstreams/13-notifications.md` §5.6):

- Email through Resend, to the account's email, one message per event,
  subject `[repose] <project>: <agent> <verb>` for agent events (e.g.
  `[repose] todo-app: claude finished`) or a dedicated line for platform
  and account events (`internal/api/notify.Subject`). Every email is
  rendered twice from one content, as HTML and as plain text, and both go
  in the one Resend call (DECISIONS I-291): one HTML layout
  (`internal/api/notify/templates/layout.html`, a 600 px table, inline
  styles, system fonts, the landing's paper and ink in light mode only,
  the word `repose` as the header, no images, no tracking) and one text
  layout, filled from a per-kind partial under `templates/kinds/`. Golden
  files under `internal/api/notify/testdata/<kind>.html|.txt` pin every
  kind (`TestGoldenEmails`, `-update` rewrites them). Everything a tenant
  wrote (a summary, a project name, a question) is escaped where it is
  placed. An agent email carries the summary, a `repose attach` row, a
  button to the project and, once the platform's signing key exists (from
  the api's first start), an unsubscribe line and a matching
  `List-Unsubscribe` header (RFC 8058 one-click). The link opens a page
  whose button turns email off with no login; a GET alone changes
  nothing, and the link expires 90 days after the email (I-442); an `agent_question` renders its options as buttons
  calling the signed reply links. On by default at signup; no time-based
  default change.
- ntfy: the user sets any ntfy-compatible URL, including a self-hosted
  server; the platform POSTs the message with a title, a priority (higher
  for `needs_input` and failures), and a `click` URL pointing at the
  project in the dashboard. Setting the URL sends nothing; a test is sent
  only on request (`repose notify test` / the dashboard's test button,
  `POST /me/notify-test`).
  Topic URLs are stored as configuration, not secrets, but never logged.
- Both can be on. Neither is required. Turning one off deletes its
  already-queued deliveries rather than sending one more batch to a
  channel the user just disabled.

## Account emails

Account events name a user and no project (`events.user_id`, migration
0007) and are transactional: sent whatever `notify_email` says, by email
only (ntfy is a project channel), and without an unsubscribe line, since
each answers something the user did or is about to be charged for
(DECISIONS I-269, I-291). The list is `events.AccountKinds`; the outbox
reads it, so a producer and the sender cannot disagree. A producer writes
one with `events.InsertAccount(ctx, tx, userID, now, kind, payload)` in
its own transaction (the row and the email are one commit) or
`Ingest.Account`; `payload` is marshalled into `events.summary` as a small
JSON object of the fields the template renders, and a template renders
gracefully when a field is missing. Paddle's own receipts and
payment-failure emails stay on in Paddle's dashboard, so a failed payment
produces Paddle's email about the card and ours about the machines.

| Kind | Subject | When, and who writes it | `summary` fields |
|---|---|---|---|
| `welcome` | Welcome to repose | first sign-in, `internal/api/auth/users.go` in the user's insert transaction: install, `repose run`, choose a plan; the seven free days | none |
| `waitlist_joined` | You're on the waitlist | `waitlist.Service.Reserve` or `Join` created the row: the place, that a seat is 8 GB running at once, that an email comes at their turn and the hold is 72 hours | `{position}` |
| `waitlist_invited` | A seat is yours for 72 hours | the invite tick or `repose-admin waitlist admit` (replaces `waitlist_admitted`): choose a plan at `/billing` before `hold_until`, what an expired hold means | `{hold_until}` |
| `waitlist_expired` | Your seat hold ran out | the tick, when `hold_until` passed unconverted: back on the list at position N | `{position}` |
| `trial_ending` | Your free week ends soon | billing, two days before `trial_end`: the plan, the amount, the charge date, the cancel link | `{plan, amount_cents, charge_at}` |
| `payment_failed` | Your payment failed | billing, day 0 and day 2 of `past_due`: update the card in Paddle's portal, machines run three days, then stop | `{plan, amount_cents, portal_url?}` |
| `subscription_cancelled` | Your plan is ending | billing, on a cancellation: the end date, machines stop then, snapshots kept 30 days, resume link | `{plan, ends_at}` |
| `subscription_ended` | Your plan has ended | billing, at the end: machines stopped, the retention date, how to come back | `{plan, ended_at, retention_until}` |
| `plan_changed` | Your plan changed | billing, on an upgrade or a scheduled downgrade | `{from_plan, to_plan, effective_at}` |
| `egress_stopped` | Your machines were stopped: egress limit | billing, at four times the egress allowance: the period's egress, the limit, until when, the upgrade link | `{plan, egress_gb, limit_gb, until}` |
| `disk_over_plan` | Your projects hold more than your plan's disk | billing's hourly tick, once a period, when the projects hold more than the plan's disk (I-585): what they hold, the plan's disk, that machines keep running and starting, what is refused until they hold less, and that a deleted file stops counting within a day or at the machine's stop | `{plan, held_gb, limit_gb}` |

`billing_stopped` (`Your guests were stopped for non-payment`) and
`abuse_stopped` (`Your guest was stopped: a cryptocurrency miner was
running`) stay project events with their own subjects, rendered through
the same layout. Dates in payloads are RFC 3339 and render as `4 October
2026 at 14:00 UTC`; amounts are cents and render as `$29.00`; plans are
`solo|plus|pro` and render as their names.

Agents' own features are untouched: Claude Code Remote Control works from a
guest when the user logged in with a subscription inside it; Claude channels
(Telegram, Discord) work if the user configures them. The platform does not
proxy or intercept these.

Dashboard and CLI:

- `repose status` shows the last event per agent window.
- `repose events`, its own command, lists
  the last 50 events with timestamps.
- `repose questions` and `repose reply` (above); the dashboard project
  page shows pending questions above everything else.
- The dashboard project page's Events card shows the event stream, newest
  first, with the agent and the summary. It answers the first half of "I
  got nothing": whether the event happened at all. It does not yet answer
  the second half — whether a delivery failed — because
  `GET /projects/:id/events` returns `{id, ts, kind, agent, summary}` and
  carries no per-channel outcome (DECISIONS I-96). Until it does, that
  half is an operator question: `repose-admin` and the
  `repose_api_outbox_*` metrics, and `ops/RUNBOOK.md` "No notifications
  arriving" is written for exactly that call.

## Depends on

Workstreams 13 (delivery, dedupe, rate cap, Resend, ntfy, unsubscribe), 04
(hook socket, AgentEvent, the tmux-idle watcher), 03 (Event forwarding), 05
(ingest, events routes, `PATCH /me` notify settings, the ops engine's
platform-event hook), 07 (`notify` and `events` commands), 08 (settings and
event stream pages), 02 (`repose-hook` and wrappers), 09 (`billing_stopped`'s
producer, `internal/billing/dunning.go`, and the plan emails of I-291).

## Deferred

Telegram and Discord webhooks. Web push from the dashboard. A platform
mobile app. Per-project channel overrides. Digest mode. Per-channel
delivery status on the event stream, which needs `events_outbox`'s state
on the events route before the dashboard can render it.
