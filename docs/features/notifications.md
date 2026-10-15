# Notifications

The user hears on their phone, in their inbox or in a Google Chat space
when an agent says it is done, asks something or gets stuck, whichever agent it was. Agents' own notification features
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

`notify set` takes `--email on|off`, `--ntfy URL|off` (I-8; `none`, the first spelling, still works, I-619) and `--gchat URL|off` (I-692) and sends
no test; `notify test` does, and exits 1 when a channel failed.

```
$ repose notify set --gchat 'https://chat.googleapis.com/v1/spaces/AAAAx1yZ/messages?key=...&token=...'
email: on
ntfy: off
gchat: on, space AAAAx1yZ

$ repose notify
email: on
ntfy: off
gchat: broken since 2026-10-12 (Google refused the webhook)
```

On the phone (ntfy):

```
repose · todo-app
claude is done: "Added auth flow, 14 tests green, committed 3f9e2a1"
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

- Kinds: `needs_input`, `error`, `agent_done`, plus the platform-originated
  `billing_stopped`, `base_updated`, `base_update_failed`, `snapshot_failed`,
  `destroy_failed` (DECISIONS I-165: the CLI no longer waits for a
  destroy, so its failure is announced), `stop_failed` (I-710, for the
  same reason: `repose stop` returns once the stop is under way; title
  `<project>: stop failed`), `personal_failed` (I-490: the
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
  hour`, and its end, `<project>: temporary machine destroyed`),
  `machines_restarted` (DECISIONS I-645: one per user per host restart,
  naming no project, titled `The server running your machines
  restarted`, with a line per machine started again or not and why; it
  follows `notify_email`, the ntfy URL and the Google Chat webhook like
  any machine notification,
  and is never sent twice for one restart; the per-machine
  `host_restarted` project event notifies nobody), and
  the agent-sent `agent_message`, `agent_question` and `agent_done`
  (DECISIONS I-244, I-688, below). A finished turn (`completed`: Claude's
  `Stop`, codex `agent-turn-complete`, opencode's idle, herdr's completion
  and the tmux-idle heuristic) is recorded as an event and sets the
  agent's state, and queues no delivery (I-688). Each carries the agent name (agent kinds only), the
  tmux window or herdr agent key (I-504), a summary of at most 1 KB, and
  a timestamp. On a herdr project, herdr's `blocked` sets gemini's and
  pi's state to needs input without a notification; their finished turns
  are recorded as `completed`.
- The summary is what the agent's hook provided, truncated. It may include
  the agent's own last message. It never includes the prompt the user typed
  or terminal contents beyond what the hook payload carries.
- Heuristic events (pane idle for a gemini or pi that has sent no hook
  yet, I-690) are labelled as such in the message: `gemini went idle`
  rather than `gemini finished`.
  As shipped, the heuristic never reads pane *text*. It reads only whether
  the pane's process tree has used CPU recently, a stronger guarantee than
  "never sent" (DECISIONS I-49).
- An event reaches the API within 10 seconds of the hook (90 seconds for
  heuristics), and the outbox worker picks up undelivered rows every 2
  seconds. Delivery results are stored per channel on the event row.
- Duplicate suppression: the same `(project, agent, kind)` within 60
  seconds is one event, with a later summary appended to the first rather
  than dropped. A Claude `Stop` hook that fires twice for one turn is the
  reason.
- Rate cap: at most 30 notifications per project in any hour. The count
  is the project's events of notifying kinds in the last hour that queued
  a delivery (a row still in the outbox, or a result in `delivered`);
  before I-691 it counted outbox rows, which are deleted once sent, so it
  never fired. Past it, one `notifications_paused` per hour says when the
  cap lifts, as a time in the user's zone (UTC when none is set): `30
  notifications in the last hour. Paused until 14:32 UTC.` Events are
  still recorded; only delivery stops.
- Presence hold (DECISIONS I-689): while the owner is at the machine,
  nothing an agent reports reaches their phone. Present means the
  project's latest guest sample, at most 2 minutes old, shows a tmux
  client or an SSH session (`meter_samples.tmux_clients + ssh_sessions >
  0`; a laptop herdr bridge is an SSH session, I-511). The outbox worker
  checks it at send time for `agent_question`, `agent_done`,
  `agent_message`, `needs_input` and `error`: while present it moves the
  row's `next_at` on by 1 minute without counting an attempt and writes
  `held` to `delivered[channel]`, for at most 10 minutes from the event,
  then sends. Before sending such a row, the worker drops it and writes
  `dropped: resolved` when what it reports is over: the question is no
  longer pending (answered anywhere, answered in the terminal,
  withdrawn, expired), or a `needs_input` has a later event from the same
  agent and window. Platform and account kinds are never held.
- Delivery runs on up to 8 workers. Each user's rows on one channel go in
  order on one lane, so a hanging ntfy server delays its owner's rows
  only (I-691). Resend gets `Idempotency-Key: <event id>/email`, so a
  retried send cannot mail twice.
- `delivered[channel]` holds a send time, `held` while the presence hold
  waits, `dropped: resolved`, `error: <why>` while retrying or `failed:
  <why>` after the last try. The why is a few words
  (`the server answered 403`, `no answer within 15 seconds`, `could not
  connect`, `not configured on this server`), never the error text, which
  for a network error names the URL (I-691).

Agents can message you and ask questions (DECISIONS I-244, I-245):

- `repose-notify TEXT` in any guest shell sends TEXT (1 KB) as an
  `agent_message`: title `<project>: <agent> says`, ntfy priority 3. It
  returns at once. Messages are never collapsed into each other and count
  against the 30-an-hour cap like any event.
- `repose-notify --done TEXT` sends TEXT (1 KB) as an `agent_done`: title
  `<project>: <agent> is done`, email subject `[repose] <project>: <agent>
  is done`, ntfy priority 4. The agent says the work the owner asked for
  is finished, since a finished turn notifies nobody. Like a message it is
  never collapsed and counts against the cap (I-688).
- `repose-ask [--options A,B,C] [--timeout 30m] QUESTION` sends an
  `agent_question` (title `<project>: <agent> asks`, ntfy priority 5) and
  blocks until the owner answers, then prints the answer. Exit codes: 0
  answered, 1 guestd unreachable, 2 usage, 3 timeout, 4 no channel on, 5
  cancelled (dismissed, withdrawn by the agent, guest stopped, or the
  question is gone after a reboot), 6 answered in the terminal, 130
  interrupted. At most 3 options; timeout 30 minutes by default, 24 hours
  at most.
- Answering in the terminal (I-690): any prompt the owner sends the agent
  that asked closes its open questions; all five agents report the prompt
  through a hook (gemini and pi through the platform's extensions). The
  guest matches the agent process, not the window, and never reads the
  prompt. `repose-ask --cancel` withdraws the agent's own open questions
  (`--cancel ID` one, `--cancel --all` every one on the machine), and a
  question whose asker was killed closes after 90 seconds without a poll.
  Each close reaches the api with its reason and is re-sent on hostd
  reconnects for 10 minutes, so a lost close no longer leaves a question
  pending until it expires.
- The owner answers from ntfy (one `http` button per option, calling a
  signed reply link; no login), Google Chat (one `openLink` button per
  option to the same link with `via=gchat`, whose GET shows the confirm
  page), email (one link per option, which opens a
  page whose button answers; replying to the email does nothing), the
  dashboard's "Waiting on you" card on /projects and on the project page
  (buttons or a text box, and Dismiss; I-693), or the laptop: `repose
  questions` lists the waiting ones, `repose reply [PROJECT] [ANSWER]`
  answers and `repose reply --dismiss [PROJECT]` dismisses (I-690), both
  listing instead of guessing when several are waiting. A question the
  owner answered in the agent's terminal, or the agent withdrew, closes
  from the guest with a reason: the api stores `answered` with no answer
  and `answered_via='terminal'`, or `cancelled` with
  `answered_via='agent'`, and says "answered in the terminal" or
  "withdrawn by the agent" wherever it describes a closed question
  (I-690). The first answer wins everywhere; a later one is refused
  with the answer that won.
- The answer reaches the waiting command within seconds over the
  hostd↔guestd channel, and survives a hostd, api or guestd restart; a
  guest that stops or reboots ends its questions.
- Question and answer text are tenant content: shown to the owner, never
  logged, and stored like an event summary.

Channels (`workstreams/13-notifications.md` §5.6):

- Email through Resend, to the account's email, one message per event,
  subject `[repose] <project>: <agent> <verb>` for agent events (e.g.
  `[repose] todo-app: claude is done`) or a dedicated line for platform
  and account events (`internal/api/notify.Subject`). Every email is
  rendered twice from one content, as HTML and as plain text, and both go
  in the one Resend call (DECISIONS I-291): one HTML layout
  (`internal/api/notify/templates/layout.html`, a 560 px table, inline
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
  nothing, and the link expires 90 days after the event the email
  reports, so a retry sends the same bytes under its Idempotency-Key
  (I-442, I-691). An
  `agent_question` email puts a grey line under the title (agent,
  window, `asked 28 Sep 18:00 UTC`, `open until 18:30 UTC`, in the
  user's zone, UTC when none is set), the question
  on a sunken block, one bordered button per option on its own line to
  the signed reply link, and a link to the project; a question without
  options gets a `Write an answer` button to the project instead. An
  `agent_done` email has the same grey line and the report on a sunken
  block, then the `repose attach` row and a button to the project
  (I-688). On by default at signup; no time-based
  default change.
- ntfy: the user sets any ntfy-compatible URL, including a self-hosted
  server; the platform POSTs the message with a title, a priority (5 for
  `needs_input` and `agent_question`, 4 for `agent_done`, failures and
  `machines_restarted` (I-645), 3 for the rest), no tags (ntfy shows a tag as an
  emoji), and a `Click` URL to the project's page (`/projects` for a
  notice that names no project) (I-691). Setting the URL sends nothing; a
  test is sent only on request (`repose notify test` / the dashboard's
  test button, `POST /me/notify-test`), at most 6 a minute, titled
  `repose: test`.
  Topic URLs are stored as configuration, not secrets, but never logged.
- Google Chat (DECISIONS I-692): the user saves a space's incoming
  webhook, which needs a Google Workspace account and is created in a
  desktop browser. The URL must be
  `https://chat.googleapis.com/v1/spaces/<id>/messages?key=..&token=..`,
  is stored with only its key and token, and is a credential: never
  logged (`gchat_url` and `webhook` are on the logger's deny list), never
  returned by the api, the CLI or the dashboard, which show the space id,
  and never in `events.delivered` or an error. A message is one `cardsV2`
  card plus a plain `fallbackText`: a header with the project and
  `<agent> <verb>`, the tenant text HTML-escaped in a `textParagraph`,
  and `openLink` buttons: one per option to its signed reply link;
  Answer (to the project) for a question without reply links; Dashboard
  (to `/projects`) for a user notice or an event with no project; else
  Open project. A close reply has none. A notice whose text opens with
  its subject (`machines_restarted`) takes the email heading as its card
  title, and `fallbackText` (the phone notification) leaves the subject
  out; a platform subtitle and its text are joined by a line break, not
  a third colon.
  Each message has its own `threadKey` (`e-<event id>`, or
  `q-<question id>` for a question) with
  `messageReplyOption=REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD`, and a
  `requestId` of the event id so a retry posts once. When a question
  that reached Chat closes, a `question_closed` user event (no project,
  gchat only) posts a reply in its thread saying how: `Answered yes from
  the dashboard`, `Answered in the terminal`, `Withdrawn by the agent`,
  `Expired without an answer`. A queued question card whose question
  has closed is dropped, not posted, like a held row (`dropped:
  resolved`, I-689). One that posts while its question
  closes calls `questions.ReplyIfClosed` after `delivered.gchat` is
  written, so it still gets its reply; `InsertQuestionClosed` writes at
  most one `question_closed` per question. Posts to one space are at least 1.1 s
  apart; 429 and 5xx retry; 400 fails the row. 401, 403 and 404 set
  `users.gchat_broken_at` once and write one `notify_channel_broken`
  account email in the same transaction; no gchat row is queued or sent
  while it is set, and the channel does not count for `repose-ask`.
  Saving a URL, or a `notify test` that gets through, clears it.
- All can be on. None is required. Turning one off (`PATCH /me` or the
  unsubscribe link) deletes its already-queued deliveries, the user
  notices such as `machines_restarted` included, rather than sending one
  more batch to a channel the user just disabled; queued account mail
  stays (I-691). The channels a user has on come from one function,
  `events.Channels`.

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
gracefully when a field is missing. Polar sends, as
merchant of record, the order confirmation (the receipt), the subscription
confirmation, the renewal receipts (`subscription_cycled`,
`subscription_cycled_after_trial`), the card-expiring reminder and the
uncanceled, paused and resumed notices; its 7-day renewal reminder is for
plans of six months or more, so never repose's. `polar-bootstrap` turns
off Polar's emails that repeat these kinds (I-604):
`subscription_trial_conversion_reminder` (ours is `trial_ending`),
`subscription_past_due` (`payment_failed`), `subscription_cancellation`
(`subscription_cancelled`), `subscription_revoked` (`subscription_ended`)
and `subscription_updated` (`plan_changed`). A failed payment produces
one email, ours.

| Kind | Subject | When, and who writes it | `summary` fields |
|---|---|---|---|
| `welcome` | Welcome to repose | first sign-in, `internal/api/auth/users.go` in the user's insert transaction: install, `repose run`, choose a plan; the seven free days | none |
| `waitlist_joined` | You're on the waitlist | `waitlist.Service.Reserve` or `Join` created the row: the place, that a seat is 8 GB running at once, that an email comes at their turn and the hold is 72 hours | `{position}` |
| `waitlist_invited` | A seat is yours for 72 hours | the invite tick or `repose-admin waitlist admit` (replaces `waitlist_admitted`): choose a plan at `/billing` before `hold_until`, what an expired hold means | `{hold_until}` |
| `waitlist_expired` | Your seat hold ran out | the tick, when `hold_until` passed unconverted: back on the list at position N | `{position}` |
| `trial_ending` | Your free week ends soon | billing, two days before `trial_end`: the plan, the amount, the charge date, the cancel link | `{plan, amount_cents, charge_at}` |
| `payment_failed` | Your payment failed | billing, day 0 and day 2 of `past_due`: update the card in Polar's customer portal, machines run three days, then stop | `{plan, amount_cents, portal_url?}` |
| `subscription_cancelled` | Your plan is ending | billing, on a cancellation: the end date, machines stop then, snapshots kept 30 days, resume link | `{plan, ends_at}` |
| `subscription_ended` | Your plan has ended | billing, at the end: machines stopped, the retention date, how to come back | `{plan, ended_at, retention_until}` |
| `plan_changed` | Your plan changed | billing, on an upgrade or a scheduled downgrade | `{from_plan, to_plan, effective_at}` |
| `egress_stopped` | Your machines were stopped: egress limit | billing, at four times the egress allowance: the period's egress, the limit, until when, the upgrade link | `{plan, egress_gb, limit_gb, until}` |
| `notify_channel_broken` | Google Chat notifications stopped | the outbox, the first time Google refuses the user's Chat webhook with 401, 403 or 404 (I-692), in the transaction that sets `gchat_broken_at`: the space, the date, that nothing goes to Google Chat until a new URL is saved or a test gets through | `{channel, space, since}` |
| `disk_over_plan` | Your projects hold more than your plan's disk | billing's hourly tick, once a period, when the projects hold more than the plan's disk (I-585): what they hold, the plan's disk, that machines keep running and starting, what is refused until they hold less, and that a deleted file stops counting within a day or at the machine's stop | `{plan, held_gb, limit_gb}` |

`billing_stopped` (`Your machines were stopped: a payment failed`) and
`abuse_stopped` (`Your machine was stopped: a cryptocurrency miner was
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
- `repose questions` and `repose reply` (above); the dashboard shows
  pending questions in one "Waiting on you" card, first on /projects
  (grouped by project, 3 rows before "Show all N") and across the project
  page under its status line. Both answer in place. A question that closes
  while on screen, wherever it was answered, stays 8 seconds as one line
  naming the answer and where it came from (`answered_via`), then leaves
  (I-693). A cancel reads "Dismissed" only with `answered_via` dashboard,
  "Withdrawn by the agent" with `agent`, and "The agent stopped waiting"
  with none (`asker_gone`, `interrupted`, a machine stop).
- The dashboard project page's Events card shows the event stream, newest
  first, with the agent and the summary. It answers the first half of "I
  got nothing": whether the event happened. `GET /projects/:id/events`
  also returns each event's `delivered` (a send time, `held`, `dropped:
  resolved`, `error: <why>` or `failed: <why>` per channel), which
  answers the second half; `ops/RUNBOOK.md` "No notifications arriving"
  reads it.

## Depends on

Workstreams 13 (delivery, dedupe, rate cap, Resend, ntfy, Google Chat, unsubscribe), 04
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
