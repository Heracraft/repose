# vsock: hostd ⇄ guestd

Inside a guest, `guestd` listens on vsock port 5000. hostd connects once per
guest (CID assigned at create) and keeps the connection; guestd accepts one
connection at a time and drops the older one. Framing: length-prefixed
protobuf (`proto/repose/guestd/v1/guestd.proto`), request/response with
`request_id`, plus unsolicited `Notify` messages from guestd.

No network listener exists in guestd. A fake for hosts without vsock (dev
machines) uses a Unix socket at `/run/repose/guestd.sock` with the same
framing; hostd picks by flag.

## Requests (hostd → guestd)

| Request | Fields | Response | Notes |
|---|---|---|---|
| `Ping` | | version, uptime_s, boot_id | also the readiness check after boot; `version` is the guestd *protocol* version (`1` today), which is what hostd compares before sending a request a guest may not know |
| `Freeze` | | | `fsfreeze -f /`; guestd sets a 10 s watchdog that thaws if no `Thaw` arrives |
| `Thaw` | | | |
| `Switch` | system_closure, force_reboot, registration (bytes, I-67) | rebooted (bool), needs_reboot (bool), output (capped 32 KB) | loads `registration` (a `nix-store --dump-db` of the closure) into the guest's nix database when present, then runs `<closure>/bin/switch-to-configuration switch`; if the closure's kernel or initrd differ from the running one, responds `needs_reboot=true` and does nothing unless `force_reboot` |
| `RegisterPaths` | registration (bytes) | | `nix-store --load-db` of a `nix-store --dump-db` listing, so paths the guest sees through the shared store are valid in its own database; hostd sends the booted closure's listing right after `Ready`, before `WriteSecrets`. Writes `/run/repose/paths-registered` (DECISIONS I-67) |
| `GrowFs` | | new_bytes | `resize2fs` after the host grew the volume |
| `WriteSecrets` | list {name, bytes} | | writes `/run/repose/secrets/<name>` 0400 dev on tmpfs, rewrites `/run/repose/secrets.env`. The list is the **whole set**: a secret present in the guest and absent from the list is removed, which is how `repose secrets rm` reaches a running guest. The three reserved names of DECISIONS I-10 are never removed this way. Validation is per request: one bad name or oversized value rejects the batch and writes nothing (DECISIONS I-30) |
| `SetPrincipals` | list | | writes `/etc/ssh/principals/dev`, reloads sshd |
| `SetupProject` | project_slug, remote_url, tz, lang, project_json | | creates tmux session named slug in the checkout (`guest-conventions.md` "The checkout"; since I-368 it makes no directory, and a machine with no checkout gets none), `/home/dev/<slug>` as a symlink to the previous slug's checkout on a volume from before I-368 restored under a new name (I-255), git init and `origin` only in a checkout it finds, writes `/home/dev/.repose/project.json` from project_json (the api's record, I-26; built from the other fields when empty, and refused as `invalid` when not JSON) |
| `Sample` | | GuestSignals + repeated ProcSample (shapes in grpc-hostd.md) + partial (bool) | hostd calls every 60 s. guestd walks `/proc` on the call and serves the tmux and Docker signals from a 5 s background refresh, so a sample costs under 20 ms and never forks (DECISIONS I-31). `partial` is set when a signal is missing rather than zero. The same 5 s refresh sets `oom_score_adj` (I-200): -800 for the tmux server and each agent window's agent process, 0 for any other `dev` process holding a negative value |
| `Exec` | argv, timeout_s, as_user | exit_code, stdout, stderr | operator only; hostd audits every call |
| `Shutdown` | timeout_s | | `systemctl poweroff` after flushing |
| `AnswerQuestion` | question_id, status (`answered\|cancelled\|expired\|no_channel`), answer | | closes a question a `repose-ask` is waiting on (DECISIONS I-244); a repeat for a question already closed is a no-op, an unknown question_id is `not_found` (the guest rebooted, and its questions with it). The answer is tenant text and is never logged |

## Notifications (guestd → hostd)

| Notify | Fields | Origin |
|---|---|---|
| `Ready` | boot_id | after network up and sshd listening |
| `AgentEvent` | agent, tmux_window, kind (completed\|needs_input\|error\|agent_message), summary | agent hooks via the unix socket `/run/repose/hooks.sock`; `agent_message` is `repose-notify` (DECISIONS I-244), whose agent may also be `shell` |
| `AgentState` | agent, tmux_window, state | on change, debounced 5 s |
| `Question` | question_id (UUIDv7, chosen by guestd), agent, tmux_window, text (1 KB), options (0 to 3, 64 bytes each), timeout_s, state (`""` open, else `cancelled\|expired` when the asker gave up or timed out) | `repose-ask` through the hook socket. Sent when the ask opens, again for every open question on each new hostd connection (the api ignores a repeat), and once more when guestd closes it itself. Open questions live in `/run/repose/questions/<id>.json` (root, 0600) so a guestd restart keeps them (DECISIONS I-244) |
| `Warning` | kind, detail | kinds: `disk_high` (over 90 percent), `inotify_exhausted`, `docker_down`, `freeze_timeout`, `store_path_missing` (a path in the running system is absent from the share, which means the host GC'd it), `oom` (the kernel killed a process for memory; detail carries the process name), `tmux_down` (no tmux server for `dev`). Each kind is sent at most once per 10 minutes. See DECISIONS I-11 and I-29 |

## Hook socket

Agents (via their wrappers) POST JSON to the Unix socket
`/run/repose/hooks.sock` (HTTP over unix, group `dev`):
`{"agent":"claude","kind":"completed","summary":"...","window":"claude"}`.
guestd relays as `AgentEvent`. `agent` must be one of the five the platform
ships, `kind` one of `completed|needs_input|error`, and `summary` is truncated
to 1 KB. The api treats a guest's `AgentEvent` as guest-sourced whatever it
says (DECISIONS I-441): a kind outside `completed|needs_input|error|agent_message`
is stored as `error`, and an agent outside the five and `shell` as no agent. `window` is optional: without it guestd resolves the calling process's
`$TMUX_PANE` through `SO_PEERCRED`, and failing that uses the agent name. The
wrapper for each agent is in `guest-conventions.md`; the payload mapping per
agent is `internal/guestd/hooks` with a recorded fixture per shape in its
`testdata/`.

The same socket serves `repose-notify` and `repose-ask` (DECISIONS I-244;
`guest-conventions.md` "Messages and questions"): `POST /notify {agent?,
window?, text}` → 204, relayed as an `AgentEvent` of kind `agent_message`;
`POST /ask {agent?, window?, text, options?, timeout_s?}` → `201 {id, state:
open, expires_at}`, announced as a `Question`; `GET /ask/<id>?wait=<s>` (at
most 25) → `{id, state, answer?, expires_at}` once the question closes or
the wait ends; `DELETE /ask/<id>` → 204, closes it `cancelled`. An agent not
among the five is recorded as `shell`. At most 16 questions may be open at
once (`429 too_many_questions`); guestd expires an open question at its
timeout (default 30 minutes, at most 24 hours). The hook POST to `/` is
unchanged.

## Failure behaviour

- If hostd loses the connection, it retries every 2 s; after 60 s the guest is
  marked `guestd_ok=false` in samples and an alert fires; the guest keeps
  running.
- A `Freeze` without a `Thaw` within 10 s thaws itself and returns a
  `Warning{kind:"freeze_timeout"}`.
- `Switch` output is always returned, even on failure, and failure leaves
  the previous system active (switch-to-configuration semantics).
- A request this guest's protocol version does not know is answered
  `invalid_argument` with the version in the message, so a newer hostd
  degrades per request instead of failing outright.

## Dev mode and the client

`guestd --dev-socket <path>` serves the same framing on a unix socket, for
tests and for machines without vsock. `guestd call <request> [json]` is the
client side of this document: it is what the NixOS VM test drives and what an
operator uses on a guest that has lost hostd (`ops/RUNBOOK.md`, "Guest
unresponsive"). hostd's own client is `internal/vsockrpc`.
