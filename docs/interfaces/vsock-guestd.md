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
| `WriteSecrets` | list {name, bytes} | | writes `/run/repose/secrets/<name>` 0400 dev on tmpfs, rewrites `/run/repose/secrets.env`, `/run/repose/secrets.refresh` and `/run/repose/secrets.state` (formats in `guest-conventions.md`), and pipes `set-environment -g`/`-gu` lines into dev's tmux server with `tmux source-file -`, never as arguments; no tmux server, or a tmux failure, does not fail the request (DECISIONS I-475, I-476). A name in `BASH_ENV`, `ENV`, `REPOSE_ENV_GEN` or `__repose_*` is written as a file and not exported. The list is the **whole set**: a secret present in the guest and absent from the list is removed, which is how `repose secrets rm` reaches a running guest. The three reserved names of DECISIONS I-10 are never removed this way. Validation is per request: one bad name or oversized value rejects the batch and writes nothing (DECISIONS I-30) |
| `SetPrincipals` | list | | writes `/etc/ssh/principals/dev`, reloads sshd |
| `SetupProject` | project_slug, remote_url, tz, lang, project_json | | starts the session unit `project_json`'s `multiplexer` names (`repose-tmux-session.service` for `tmux`, an absent key or any other value; `repose-herdr-server.service` for `herdr`; DECISIONS I-503, `guest-conventions.md` "Session units"), after writing `project.json`. For tmux that unit creates the tmux session named slug in the checkout (`guest-conventions.md` "The checkout"; since I-368 it makes no directory, and a machine with no checkout gets none), `/home/dev/<slug>` as a symlink to the previous slug's checkout on a volume from before I-368 restored under a new name (I-255), git init and `origin` only in a checkout it finds, writes `/home/dev/.repose/project.json` from project_json (the api's record, I-26; built from the other fields when empty, and refused as `invalid` when not JSON). The message is unchanged; `multiplexer` rides inside `project_json`, so an older hostd passes it through and an older api's file, without the key, starts tmux as before |
| `Sample` | | GuestSignals + repeated ProcSample (shapes in grpc-hostd.md) + partial (bool) + cpu_pressure_us_total + mem_used_bytes (I-493: the `/proc/pressure/cpu` "some" total since boot, and MemTotal less MemAvailable; 0 when unreadable, never a reason for `partial`) + root_used_bytes + root_size_bytes (I-567: statfs of `/`, blocks less those available to `dev` and blocks, in bytes; both 0 when statfs fails, and absent, so 0, from a guestd older than I-567, which hostd accepts; never a reason for `partial`) | hostd calls every 60 s. guestd walks `/proc` on the call and serves the tmux and Docker signals from a 5 s background refresh, so a sample costs under 20 ms and never forks (DECISIONS I-31). `partial` is set when a signal is missing rather than zero. The same 5 s refresh sets `oom_score_adj` (I-200): -800 for the tmux server and each agent window's agent process, -800 for the herdr server and each agent in its tree (I-505; by binary name, `node` left out, so Gemini CLI under herdr gets no -800, I-535), -800 for `dev`'s `sshd-session`, every `tmux: client` and every `herdr` process and -900 for `dev`'s user manager (I-576), 0 for any other `dev` process holding a negative value; and nice -5 on each herdr server thread, 0 for any other process in its tree below 0 (I-505). Agents come from both multiplexers' sources, unioned (I-504); `tmux_clients` stays tmux's count and is 0 on a herdr machine |
| `Exec` | argv, timeout_s, as_user | exit_code, stdout, stderr | operator only; hostd audits every call |
| `Shutdown` | timeout_s | | `systemctl poweroff` after flushing |
| `AnswerQuestion` | question_id, status (`answered\|cancelled\|expired\|no_channel`), answer | | closes a question a `repose-ask` is waiting on (DECISIONS I-244); a repeat for a question already closed is a no-op, an unknown question_id is `not_found` (the guest rebooted, and its questions with it). The answer is tenant text and is never logged |

## Notifications (guestd → hostd)

| Notify | Fields | Origin |
|---|---|---|
| `Ready` | boot_id | after network up and sshd listening |
| `AgentEvent` | agent, tmux_window, kind (completed\|needs_input\|error\|agent_message), summary | agent hooks via the unix socket `/run/repose/hooks.sock`; `agent_message` is `repose-notify` (DECISIONS I-244), whose agent may also be `shell`. Also `completed` for gemini and pi when herdr reports a finished turn (I-504). `tmux_window` carries the agent's key: a tmux window name or a herdr agent key (`guest-conventions.md` "herdr"), at most 64 bytes; the field keeps its name and number (I-504) |
| `AgentState` | agent, tmux_window, state | on change, debounced 5 s; `tmux_window` as in `AgentEvent` |
| `Question` | question_id (UUIDv7, chosen by guestd), agent, tmux_window, text (1 KB), options (0 to 3, 64 bytes each), timeout_s, state (`""` open, else `cancelled\|expired` when the asker gave up or timed out) | `repose-ask` through the hook socket. Sent when the ask opens, again for every open question on each new hostd connection (the api ignores a repeat), and once more when guestd closes it itself. Open questions live in `/run/repose/questions/<id>.json` (root, 0600) so a guestd restart keeps them (DECISIONS I-244) |
| `Warning` | kind, detail | kinds: `disk_high` (over 90 percent), `inotify_exhausted`, `docker_down`, `freeze_timeout`, `store_path_missing` (a path in the running system is absent from the share, which means the host GC'd it), `oom` (the kernel killed a process for memory; detail carries the process name), `tmux_down` (no tmux server for `dev`, sent only when `project.json` names tmux or has no `multiplexer`), `herdr_down` (`project.json` names herdr and its socket refused, or answered a protocol below 22, on two refreshes in a row; DECISIONS I-507). Neither is sent before this guestd has run SetupProject, which starts the session unit, or 60 s after guestd started (I-562). A hostd from before I-507 relays `herdr_down` as `guest_other`; hostd and the api take the kind first and the base that sends it ships after. Each kind is sent at most once per 10 minutes. See DECISIONS I-11 and I-29 |

hostd treats every notification as written by whoever is root in the guest
(DECISIONS I-445): `AgentEvent`, `Question` and `Warning` pass a per-guest
budget of 30 at once and one every 2 s after, and are bounded to the kinds,
states and sizes above before they become api events
(`docs/interfaces/grpc-hostd.md`, "Guest-raised events"). A `Warning`'s
detail is not forwarded as sent: hostd keeps the numbers of `disk_high` and
`inotify_exhausted` in the shape guestd writes them and the process name of
`oom`, and drops the rest. The same holds for a `SampleResult`: its agent
list, process list and process names are capped and cleaned (I-446).

## Hook socket

Agents (via their wrappers) POST JSON to the Unix socket
`/run/repose/hooks.sock` (HTTP over unix, group `dev`):
`{"agent":"claude","kind":"completed","summary":"...","window":"claude"}`.
guestd relays as `AgentEvent`. `agent` must be one of the five the platform
ships, `kind` one of `completed|needs_input|error`, and `summary` is truncated
to 1 KB. The api treats a guest's `AgentEvent` as guest-sourced whatever it
says (DECISIONS I-441): a kind outside `completed|needs_input|error|agent_message`
is stored as `error`, and an agent outside the five and `shell` as no agent. `window` is optional: without it guestd resolves the calling process's
`$TMUX_PANE` through `SO_PEERCRED`, and failing that relays the event under the
agent name and changes no tmux window's agent state (a herdr pane is not the
tmux window named after its agent). A `window` that starts `herdr:` names a
herdr pane (`herdr:<pane_id>`, sent by `repose-hook` from `$HERDR_PANE_ID`,
DECISIONS I-506): guestd resolves it through its herdr source to the
agent's key and records the hook there; one that resolves to no agent
(no herdr, an unknown pane, an id over 58 bytes or outside
`[A-Za-z0-9:_-]`) is relayed as a window-less hook is, with the agent
name as `tmux_window`, and changes no state. A guestd from before I-506 treats the
value as a tmux window name that matches none, with the same result. The
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
