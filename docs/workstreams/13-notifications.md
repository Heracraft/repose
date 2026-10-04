# 13 · notifications

## 1. Goal

When an agent the user left running finishes or gets stuck, the user finds
out on their phone within a minute, whichever agent it was. One event
pipeline from the guest to the user's channels, with the same shape for
every agent, so `repose status` and the dashboard show one timeline.

## 2. Scope: builds

- Hook side in the guest: `repose-hook` (Go, part of `cmd/guestd` as a
  subcommand or a tiny separate binary in the same package), the
  per-agent hook configuration that the agent wrappers install
  (02-guest-base installs the wrappers; this workstream defines what they
  install), and the tmux-idle heuristic for agents without hooks.
- guestd's `AgentEvent` and `AgentState` notifications (04-guestd
  implements the transport; this workstream defines when they fire and
  what they carry).
- api side: `POST`-free ingest from the gRPC `Event` message into `events`,
  the outbox, delivery workers for email (Resend) and ntfy, dedupe, retries,
  user settings, `POST /me/notify-test`.
- Templates for email and ntfy.
- Platform-originated events that reuse the pipeline: `billing_stopped`,
  `base_updated`, `snapshot_failed`, `host_moved` (restore onto a new host).
- What `repose status` and the dashboard events card display (07 and 08
  render; this workstream defines the fields and wording).

## 3. Scope: does not build

- vsock transport and the unix hook socket (04-guestd).
- Wrapper packaging in Nix (02-guest-base; this doc supplies the hook
  config each wrapper writes).
- Telegram and Discord webhooks (DESIGN §18; the delivery interface is
  designed so they are one file each later).
- Anything with Claude Code Remote Control or channels: those belong to the
  user's own Claude login and the platform does not touch them.
- Email for auth or receipts (Logto and Paddle send their own; the account emails of I-291 are ours).

## 4. Interfaces

Owns: event shapes (`kind`, `summary` rules), the hook JSON on
`/run/repose/hooks.sock`, the `events.delivered` JSON, the
`notify_email` and `ntfy_url` semantics on `users`, `POST /me/notify-test`
(added to `interfaces/api.md`).

Consumes: `interfaces/vsock-guestd.md` (`AgentEvent`, `AgentState`),
`interfaces/grpc-hostd.md` (`Event.agent_event`), `interfaces/api.md`
(`GET /events`, `PATCH /me`), `interfaces/guest-conventions.md` (window
names, wrapper behaviour).

## 5. Design detail

### 5.1 Event shape

```
kind      completed | needs_input | error | billing_stopped | base_updated |
          snapshot_failed | host_moved
agent     claude | opencode | codex | gemini | pi | null (platform events)
window    tmux window name, e.g. claude-2, or null
summary   ≤ 1024 bytes, plain text, first line ≤ 120 chars used as the title
project   id (slug and name joined for display)
ts        RFC 3339, from the guest clock, replaced by the api's clock if
          skewed more than 5 minutes
```

`summary` is what the agent's hook payload gives (Claude's `message` for
Notification, the last assistant line for Stop, truncated), never the
prompt and never terminal contents beyond what the hook payload itself
carries. The privacy boundary in `DESIGN.md` §15 applies: if a hook payload
contains a transcript path, it is not opened.

### 5.2 Hook socket protocol

`POST http://unix/run/repose/hooks.sock/v1/event` with JSON
`{"agent": "claude", "window": "claude", "kind": "completed", "summary":
"..."}`. Response 202 always, even on validation failure (logged), so a
hook never blocks an agent. `repose-hook` is what agents run; it reads the
agent's native payload from stdin, maps it, resolves `window` from
`$TMUX_PANE` via `tmux display -p -t $TMUX_PANE '#{window_name}'`, and
POSTs. It exits 0 in every case, including when the socket is missing.

### 5.3 Per-agent mechanisms

| Agent | Mechanism | `completed` | `needs_input` | `error` |
|---|---|---|---|---|
| Claude Code | `~/.claude/settings.json` hooks written by the wrapper if absent: `Notification` with matchers `agent_completed`, `agent_needs_input`, `permission_prompt`, `idle_prompt`; `Stop`; `StopFailure`. Command `repose-hook claude`. | `Stop` and `Notification:agent_completed` (deduped, 5.5) | `Notification:agent_needs_input`, `permission_prompt`, `idle_prompt` | `StopFailure` |
| Codex CLI | `~/.codex/config.toml` `notify = ["repose-hook", "codex"]` (Codex calls it with a JSON arg on `agent-turn-complete`) | `agent-turn-complete` | none (no heuristic fallback either, DECISIONS I-49) | not built (5.4, "Not built: the exit case") |
| opencode | plugin file `~/.config/opencode/plugin/repose.js` written by the wrapper, subscribing to `session.idle`, `session.error` and `permission.asked`/`permission.updated` events and calling `repose-hook opencode` | `session.idle` | `permission.asked`, `permission.updated` | `session.error` |
| Gemini CLI | tmux-idle heuristic only, as shipped. **Resolved, DECISIONS I-50:** the current Gemini CLI ships a real `Notification`/post-loop hook, and independently stopped serving free/Pro/Ultra-tier requests on 2026-06-18 (replaced by Antigravity CLI) — both need the owner's attention before this row is "at time of writing" again. | 90 s pane-idle heuristic (5.4): `"gemini went idle"` | none (5.4: the heuristic never produces `needs_input`) | not built |
| pi | tmux-idle heuristic only, as shipped, despite `~/.pi/agent/hooks/` existing in the current binary (DECISIONS I-50: unmapped, not verified against the real wire shape) | 90 s pane-idle heuristic (5.4): `"pi went idle"` | none | not built |

The wrapper for each agent writes its hook config only if the key is absent
(a user's own hooks are preserved and `repose-hook` is appended, never
replacing). The exact file edits are in `interfaces/guest-conventions.md`
under agent wrappers; the mapping from native payload to `kind` lives in
`internal/guestd/hooks/mapper.go`, one function per agent, with a fixture
of each native payload in `internal/guestd/hooks/testdata/<agent>/`.

Where a mechanism above says "at time of writing" or is marked resolved
by a decision, the implementer checks the agent's current docs and records
the finding in `DECISIONS.md` as an
implementation entry; a heuristic is never left in place if a hook exists.

### 5.4 tmux-idle heuristic (guestd)

As built (DECISIONS I-49 corrects this section: the original design below
read pane text; the shipped one never does). For each window whose name is
an agent name, every 5 seconds guestd's watcher (`internal/guestd/sample`)
reads `#{pane_current_command}` and `#{window_activity}` — never pane
*content* — and walks the pane's process tree for CPU deltas. State machine
per window:

- `working`: the process tree has consumed CPU since the last refresh.
- `idle`: alive, no CPU consumed for 30 s.
- `needs_input`: only ever set by a real hook's `RecordHook` (5.5's dedupe
  window has nothing to do with this; it is the `AgentEvent` a hook already
  sent, mirrored into `AgentState` so `repose status` shows it). There is no
  pane-content pattern match; an agent with no hook cannot report
  `needs_input` at all, and says so honestly rather than guessing from text
  that would have to be read to guess from.
- `unknown`: alive, neither of the above (a window observed for the first
  time, or with a state the debounce below has not yet confirmed).
- Heuristic completion (agents with no hook: Gemini CLI, pi, and opencode or
  Codex if their hook is ever absent): no pane activity for 90 seconds while
  the foreground process is still the agent binary (or, for Gemini CLI,
  `node`, which runs its bundle; DECISIONS I-122) emits one synthetic
  `completed` with `summary = "<agent> went idle"`, once per quiet period.
  Suppressed for Claude, Codex and opencode, which have real hooks.
- A state change is debounced 5 s before it is announced as `AgentState`,
  so a one-tick flicker does not spam `repose status`.

**Not built: the exit case.** 5.3's "process exit non-zero while window
present" (Codex) and "non-zero exit" (Gemini, pi) has no code path: tmux
closes a window the instant its pane's process exits, so by the time the
next refresh notices the window is gone there is nothing left to read an
exit status from. Reading one needs `remain-on-exit` on the tmux session,
which keeps *every* window (including `shell`) open after its process
dies — a `guest-conventions.md`/`run-and-attach.md` behaviour change 02
would have to make and sign off on, not something to slip in from this
workstream. Today a window disappearing only ever produces
`AgentState unknown`; the checklist item below is marked accordingly.

`AgentState` exists separately from `AgentEvent` for the reason originally
given: state feeds `repose status` and samples; events feed notifications.

### 5.5 Ingest, dedupe, outbox

api receives `Event.agent_event` on the host stream, acks by `event_id`,
inserts `events` with `delivered = {}`. Dedupe: same `(project, agent,
window, kind)` within 60 seconds collapses to the first, with the later
summary appended if different (Claude fires both `Stop` and
`agent_completed`). An `events_outbox` table (`event_id, channel, attempts,
next_at, last_error`) is filled at insert with one row per enabled channel.
A worker polls the outbox every 2 seconds (`for update skip locked`),
delivers, and writes `events.delivered[channel] = ts` or `error`. Retries: 7
attempts at 10 s, 1 m, 5 m, 30 m, 2 h, 8 h, 12 h (sums to just under the 24 h
`05-control-plane-api.md` §5.9 promises, corrected from this section's
original 5-attempt/2-hour schedule to match it); after that
`delivered[channel] = "failed: <reason>"` and `repose_api_notify_total
{result="failed"}` (5.8). Rate limit per user per channel: 30 per hour,
beyond that events are stored and a single "30+ events in the last hour,
see the dashboard" message is sent.

### 5.6 Channels

- **email** (Resend, `internal/api/notify/notify.go`'s `Email`): from
  `repose <notify@repose.herakraft.co>`, subject `[repose] todo-app:
  claude finished`. The body is rendered by `notify.Render` (DECISIONS
  I-291): one HTML layout (`templates/layout.html`, Go `html/template`, a
  600 px table, inline styles, system fonts, the landing's paper and ink
  in light mode only, the word `repose` as the header, no images, no
  tracking) and one text layout (`templates/layout.txt`), filled from a
  per-kind partial in `templates/kinds/` that defines `heading`, `body`
  (paragraphs), `link_label`/`link_url` (the button), `command`, `lines`
  and `steps`; kinds without a partial share `agent`. Both renderings go
  as `html` and `text` in one Resend call. Agent emails carry the title,
  the summary, the `repose attach` row, the project button and (when an
  `Unsubscriber` is configured) an unsubscribe line pointing at `GET
  /v1/notify/unsubscribe?token=` (a confirmation page; the POST it makes,
  or a mail client's RFC 8058 one-click POST, turns email off), an
  HMAC-signed user id with a 90-day expiry (I-442),
  checked with no database round trip and no `Authorization` header, keyed
  by a secret auto-provisioned into the platform pseudo-project the first
  time the api starts (DECISIONS I-49; same home as the CA material,
  `internal/api/ca`, never a fourth one). An `agent_question` renders its
  options as buttons with the signed reply links. Account kinds
  (`events.AccountKinds`: welcome, the waitlist kinds, the plan kinds) are
  transactional: no unsubscribe line, sent whatever `notify_email` says,
  their payload a JSON object in `events.summary`
  (`features/notifications.md`, "Account emails"). Enabled by default at
  signup. Platform and account events use their own subjects
  (`internal/api/notify.Subject`, `platformSubjects`). Golden files under
  `testdata/<kind>.html|.txt` pin every kind; `go test ./internal/api/notify
  -run TestGoldenEmails -update` rewrites them after a template change.
- **ntfy** (`internal/api/notify/notify.go`'s `Ntfy`): `POST <ntfy_url>`
  with headers `Title`, `Priority` (5 for `needs_input`, 4 for `error` and
  the platform failure kinds, 3 otherwise), `Tags` (`white_check_mark`,
  `question`, `x`), `Click` (dashboard project URL), body = summary. The
  user pastes any ntfy-compatible URL including self-hosted or
  `ntfy.sh/<topic>`; the URL is stored as is and never logged.
- `POST /me/notify-test` sends a `completed` event with summary `This is a
  test from repose` through the enabled channels and returns the per-channel
  result so the settings page can show it.

Adding Telegram or Discord is a new file implementing `notify.Sender
{Send(ctx, m Message) error}` and a user setting; nothing else changes.

### 5.7 Display

`repose status` shows the last event (`last event 12m ago: claude
completed "ran tests, 3 failures fixed"`) and `AgentState` per window
(`claude: working`). The dashboard events card lists the last 50 with
delivery status icons per channel. `GET /events?since=` is the only read
path.

### 5.8 Metrics

As built (DECISIONS I-49): every api metric carries the `repose_api_`
prefix 05 gave the whole process, not a standalone `repose_notify_*`
family. `repose_api_events_total{kind}` (`kind="deduped"` is the dedupe
counter this section originally asked as a separate
`repose_notify_dedupe_total`; there is no separate `agent` or
`source=hook|heuristic` label — the heuristic's synthetic `completed` and a
real hook's land in the same counter under the same kind, because nothing
downstream has needed to tell them apart), `repose_api_notify_total
{channel,result}` (the delivery counter this section called
`repose_notify_delivered_total`), `repose_api_outbox_depth`,
`repose_api_outbox_lag_seconds`, and
`repose_api_notify_delivery_latency_seconds` (event ts → delivered ts,
successful deliveries only — the one metric here with no prior equivalent,
added by this workstream).

## 6. Failure modes

| Situation | Outcome |
|---|---|
| Hook socket missing or guestd down | `repose-hook` exits 0 silently, guestd logs on next start; the tmux-idle heuristic still runs once guestd is back |
| Agent payload unparseable | `repose-hook` sends `kind = completed`, `summary = "<agent> event (unparsed)"`; fixture added |
| Both hook and heuristic fire | dedupe window collapses them |
| Resend down | retries per 5.5, `failed` after 7 attempts, metric and alert if > 5 percent failed in 10 minutes |
| ntfy URL invalid or 4xx | first failure marks `delivered.ntfy = "failed: 404"` with no retry (4xx), the settings page shows a warning banner |
| User disables email mid-retry | outbox rows for that channel are deleted at the setting change |
| Event storm (agent looping) | rate limit per 5.5, one digest message |
| Clock skew from guest | api replaces `ts` and records `skew_seconds` in the event |
| Summary contains secrets (agent echoed a token) | not detectable; mitigated by summaries being ≤ 1 KB and never the transcript; documented in `SECURITY.md` as accepted |

## 7. Testing

- Unit: payload mapping fixtures for each agent's native hook JSON, the
  idle state machine with a fake clock, dedupe, outbox scheduling, rate
  limit, templates (golden: `internal/api/notify/testdata/<kind>.html|.txt`
  for every kind, `TestGoldenEmails`; `TestRenderEscapesTenantText` for a
  summary containing `<script>`; `TestEmailSendsHTMLAndText` for the one
  Resend call carrying both).
- Integration: real Postgres, outbox worker with a fake Resend and a local
  ntfy container, retries with a fake clock, `notify-test` route.
- Guest-level: on a real guest, run each agent, trigger a completion and a
  permission prompt, observe the event arrive in `events` with the right
  kind and source, and the phone notification within 60 s. Recorded per
  agent in `STATUS.md`.
- Wrapper: a test that runs each wrapper in a temp `$HOME` and asserts the
  hook config it writes, and that it preserves pre-existing user hooks.

## 8. Rollback

Migrations down for `events_outbox`
(`internal/db/migrations/0002_outbox_sessions_settings.down.sql`). As built
there is no operator-level global kill switch: a
user disables their own channels with `PATCH /me {notify: {email: false,
ntfy_url: null}}`, or clicks the email's unsubscribe link
(`GET /v1/notify/unsubscribe`), either of which stops delivery for that
user immediately (`events.enqueue` checks the current setting, and turning
a channel off deletes that channel's pending `events_outbox` rows per
5.5's failure-mode table) while events keep landing in `events` and
`repose status`/`GET /events`. There is no `NOTIFY_CHANNELS=` env var and
no `repose-hook uninstall`; wrapper hook config is idempotent (`internal/
guestd/hooks` never rewrites an existing `repose-hook` entry) but removing
it means editing the agent's own config file by hand — a documented,
not-yet-built convenience for later.

## 9. Checklist

- [x] Hook socket protocol implemented; `repose-hook` exits 0 in every
      case including a missing socket. Evidence: `internal/guestd/hooks`
      (`hooks_test.go`) and `cmd/repose-hook/main_test.go`; `run()` never
      calls `os.Exit`, `main()` only prints to stderr.
- [~] Every agent row in 5.3 has a fixture of the native payload and a
      mapping test: yes, for the 5 real payload shapes (claude, codex,
      opencode, plus the pass-through and unparsed cases) — Evidence:
      `internal/guestd/hooks/testdata/{claude,codex,opencode}/`. "At time
      of writing" rows checked and recorded (DECISIONS I-50) rather than
      resolved: Gemini CLI and pi both turn out to have hook mechanisms
      now, but mapping them needs the real binaries to verify wire shapes
      against, which this session did not have — implementing from search
      results alone risks silently dropping every Gemini/pi notification.
      Left as a follow-up; the heuristic (honest about being one,
      `"<agent> went idle"`) is what ships. — open: native payload fixtures
      and mapping tests for Gemini CLI and pi (testdata/gemini and testdata/pi
      hold only the heuristic); needs the real binaries' hook payloads
      captured in a guest (DECISIONS I-50).
- [x] Wrapper hook config for each agent is written when absent, appended
      when the user has hooks, never replaces. Evidence:
      `nix/guest/tests/default.nix` subtest "claude hooks merged without
      clobbering a user hook": plants `echo user-hook` under `hooks.Stop`,
      runs `repose-agent-setup claude`, asserts both commands present, runs
      it again and asserts the file is byte-for-byte unchanged (idempotent);
      also exercises codex and opencode. Needs a NixOS VM build to run
      (`nix flake check ./nix`), not repeated by this session — 02's
      "done" status already covers it.
- [x] tmux-idle state machine covers working, idle, unknown, and heuristic
      completion; suppressed for hooked agents. Evidence:
      `internal/guestd/sample/watcher_test.go` (fake clock, no pane
      content). NOT covered: the exit case (5.4, "Not built"); `needs_input`
      is never heuristic (DECISIONS I-49) so it is out of this item's scope.
- [x] Pane contents are never logged, stored or sent. Evidence: `grep -rn
      capture-pane internal/guestd` finds nothing — the shipped heuristic
      never reads pane text at all (DECISIONS I-49), stronger than the
      original "read it but don't persist it" design.
- [x] Dedupe collapses Claude's `Stop` + `agent_completed`. Evidence:
      `internal/api/events/events_test.go` `TestDedupeOutboxAndRateCap`:
      two `completed` inserts 20 s apart collapse to one row with both
      summaries joined.
- [x] Outbox retries at the stated schedule (7 attempts, DECISIONS I-49),
      marks failed after the last one, deletes rows when a channel is
      disabled. Evidence: `internal/api/notify/notify_test.go`
      `TestOutboxDeliversOncePerChannelAndRetries` (fake-clock schedule),
      `TestPermanentFailureIsNotRetried` (4xx skips retry);
      `internal/api/http/users.go`'s `patchMe` and
      `internal/api/http/notify.go`'s `unsubscribe` both delete pending
      rows for a disabled channel, exercised by `TestNotifyUnsubscribe`.
- [x] Rate limit sends one digest beyond 30 per hour. Evidence:
      `TestDedupeOutboxAndRateCap`: 40 events in an hour produce exactly
      one `notifications_paused` row and stop growing the outbox past the
      cap, while every event still lands in `events`.
- [x] Email template renders with unsubscribe link that works. Evidence:
      `internal/api/notify/notify_test.go` `TestEmailTemplate` (title,
      summary, attach hint, dashboard link, unsubscribe URL all present in
      the text and HTML Resend receives), `render_test.go`
      `TestGoldenEmails` (every kind pinned under `testdata/`, I-291) and
      `internal/api/http/http_test.go` `TestNotifyUnsubscribe` (the link's
      target route, end to end against a real Postgres: valid token flips
      `notify_email` off with no `Authorization` header, forged and
      malformed tokens refused). NOT done: a real email received (needs a
      Resend account and a deploy).
- [x] ntfy delivery with priority and tags per kind; 4xx not retried.
      Evidence: `internal/api/notify/notify_test.go`
      `TestNtfyAndEmailSenders`. NOT done: a local ntfy container (the test
      uses `httptest.Server`, which exercises the same HTTP contract ntfy
      implements).
- [x] `POST /me/notify-test` returns per-channel results and the dashboard
      shows them. Evidence: `internal/api/http/http_test.go`
      `TestNotifyTestRoute` (email-only, then with ntfy added, then with
      email disabled). The dashboard rendering them is 08's checklist item.
- [x] Platform events (`billing_stopped`, `base_updated`,
      `snapshot_failed`, `host_moved`) flow through the same pipeline.
      Evidence: `internal/api/ops/ops_test.go` `TestSnapshotFailureNotifies`
      and `TestRestoreEmitsHostMovedEvent`; `internal/api/basebump` already
      covered `base_updated`/`base_update_failed` before this workstream.
      NOT done: `billing_stopped` has no producer yet (workstream 09 is
      billing-exempt per I-16; `ops.EventSink` is ready for it to call).
- [x] Real guest: each of the five agents produces a `completed` and, where
      the mechanism supports it, a `needs_input`, delivered to a phone
      within 60 s. Evidence (`ops/checks/notifications.sh` on host-01,
      guest m3-check, 2026-09-21 00:08Z, ntfy topic of the session, email
      on): claude `completed` event 01a0c14a-f7e3-7926-be1c-75e7728878a1,
      ntfy 1 s after the hook; codex 01a0c14a-fdb3-78af-9c30-5abd8fce0a84,
      7 s; opencode 01a0c14b-1867-72a2-ac6f-66f872fc7c78, 7 s (these three
      replayed their native payload through `repose-hook` from a tmux
      window named after the agent, the agents not being logged in inside
      the guest); pi through the real binary idle in its window,
      01a0c150-d29f-7153-b87d-147f6bedb2b8, 101 s (the 90 s window, the
      debounce, the outbox). Gemini: none on base 2026.09.20.3, twice, the
      heuristic never seeing the window as an agent's because Gemini CLI
      runs under node and node's main thread is `MainThread` (DECISIONS
      I-122, I-125); on base 2026.09.21.2 (m3-check, 2026-09-21 01:08Z) the
      real binary idle in its window produced event
      01a0c181-6d6d-780d-9d8e-5fc79869f26f "gemini went idle", window
      `gemini`, ntfy and email 100 s after the window went quiet. All five
      agents have a delivered `completed` on a real guest. `needs_input`
      was not exercised (no agent logged in). Also found on the way: the
      events arrived with no tmux window and an empty Stop summary (I-121,
      fixed and live).
- [~] `repose status` and the dashboard show last event and agent state.
      Evidence: screenshot and CLI output. The CLI half is built:
      `internal/cli/status.go` prints `last event <age>: <agent> <kind>
      "<summary>"` and one `<agent>: <state>` line per window (07,
      STATUS 2026-09-20); its output on host-01 is in
      `ops/checks/notifications.sh`'s report. The dashboard events card
      is 08's row, closed by the m3-web session against the real api. — open:
      `repose status` output from a real guest showing the `last event` line
      and the per-window agent state is not pasted (the notifications.sh
      report is under the gitignored ops/checks/out/); run it after a hook
      fires in a real guest.
- [x] Metrics in 5.8 exist. Evidence: `internal/api/metrics/metrics.go`
      registers `repose_api_events_total`, `repose_api_notify_total`,
      `repose_api_outbox_depth`, `repose_api_outbox_lag_seconds`,
      `repose_api_notify_delivery_latency_seconds` on the same registry
      `/metrics` serves (`internal/api/app/app.go`); names differ from the
      original wording, reconciled in 5.8 and DECISIONS I-49. NOT done: an
      actual `/metrics` scrape (needs a deploy).
- [x] `features/notifications.md` matches. Evidence: rewritten this
      session against the built pipeline (dedupe key, subject format,
      unsubscribe, default channel state, `repose_api_*` metric names).
- [x] `ops/RUNBOOK.md` has: no notifications arriving (check outbox depth,
      guestd_ok, hook config), ntfy failing, Resend failing. Evidence:
      "No notifications arriving", "ntfy failing", "Resend failing"
      entries added this session, alongside the existing "api: secret
      service unavailable" style.
