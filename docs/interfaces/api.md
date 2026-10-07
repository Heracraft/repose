# HTTP API

Base: `https://api.repose.herakraft.co/v1`. JSON. Auth: `Authorization:
Bearer <Logto access token>` for user routes (resource
`https://api.repose.herakraft.co`, verified against Logto JWKS, `sub` is the
user id). Internal routes under `/internal/` are for the gateway and use the
gateway's mTLS client certificate: CN `gateway`, from the host CA; any other
client certificate, a host's included, fails the TLS handshake (I-431). Errors: `{ "error": { "code": "...",
"message": "...", "detail": {...} } }` with codes `unauthenticated`,
`forbidden`, `not_found`, `invalid`, `conflict`, `payment_required`,
`capacity`, `waitlisted`, `rate_limited`, `billing_disabled`, `internal`.
Three codes come with a 503 and are answers, not outages: `capacity` (no
host can take the machine), `waitlisted` and `billing_disabled`; any other
5xx is the api failing. This is the one list; the dashboard's outage bar
(`ANSWER_503` in `apps/web/src/lib/api/errors.ts`) and its docs point here
(I-393).
`waitlisted` (503) refuses a plan's checkout while the fleet has no free
seat and puts the user on the waitlist; `detail` is `{position, joined_at,
email}` and `message` is the whole sentence (DECISIONS I-269, I-290).
`payment_required` (402) carries `detail.reason`, listed under "Usage and
billing".
Every response
carries `X-Request-Id`.

## Users

| Method | Path | Body / result |
|---|---|---|
| GET | `/me` | `{id, handle, email, github_login, tz, created_at, billing: {status: none\|trial\|active\|past_due\|suspended\|exempt, plan: solo\|plus\|pro\|null, seats, period_end, trial_end, cancel_at, has_card, trial_credit_cents}, limits: {projects, xl, memory_gb, disk_gb, egress_gb}, waitlist: {position, joined_at, invited_at, hold_until}\|null}` (`status` is a projection of the subscription, I-289: `none` is an account with no plan yet, `trial` a `trialing` subscription; `trial_credit_cents` is always 0 and `xl` is 1 on Pro, 0 otherwise, both kept one release; `waitlist` is set while the user holds a place, I-290) |
| PATCH | `/me` | `{tz?, notify: {email?: bool, ntfy_url?: string\|null}}`. An `ntfy_url` must be http(s); one whose host is `localhost` or a literal non-public address is `400 invalid` (DECISIONS I-444) |
| DELETE | `/me` | begins cancellation (stops guests, 30-day retention) |
| POST | `/me/notify-test` | sends a test event to every configured channel → `{email: ok\|error, ntfy: ok\|error}` |
| GET | `/me/config` | the account's personal layer, `machine.nix` (DECISIONS I-490): `{revision_id, fragment, created_at, source: cli\|dashboard, opted_out: [slug]}`; `revision_id`, `created_at` and `source` are null and `fragment` is `""` when the account has none. `opted_out` lists the live projects that keep it off |
| PUT | `/me/config` | `{fragment, base_revision_id?, source?: cli\|dashboard}` → `{revision_id, fragment, created_at, source, projects: [{project_id, slug, revision_id, op_id?, merged?, running}], unchanged?}`. `fragment: ""` removes the layer. Validated like a project fragment: 256 KB, then Nix's parser, whose `400 invalid` names `machine.nix:L:C` with `detail.personal_line`; a secret's value in it fails the build that would carry it (`machine.nix contains the value of secret NAME`). `base_revision_id`, when present, must be the account's current revision (`""` or null when it has none), else `409 conflict` "machine.nix on your account changed since this copy was pushed" with `detail: {current_revision_id, source, created_at}`; the CLI sends the revision it last pushed. The same text as the current one answers `unchanged: true` and rebuilds nothing. A new text gives every live project that has not opted out, running ones first and at most 50, a revision with its current fragment and the new layer, and a build op queued behind whatever the project is doing: a running machine switches in place, a stopped one at its next start (`projects[].running`). A build of an earlier personal change that has not started takes the new text instead (`merged: true`, no `op_id`). A personal build that fails leaves the machine on its current revision and records a `personal_failed` event naming `machine.nix` |
| GET | `/me/config/revisions` | `[{revision_id, created_at, source, bytes}]`, newest first, at most 50 |
| GET | `/notify/unsubscribe?token=` | no auth; the link in an email's unsubscribe line and its `List-Unsubscribe` header (13-notifications.md §5.6, DECISIONS I-442). The token is signed and names the user and an expiry 90 days after the email was sent; a token of the earlier shape (the user id alone, no expiry) is still accepted until the release after next. Answers an HTML page with a button that POSTs; a GET changes nothing, because mail scanners open links. Expired → `410`, invalid → `400`, both HTML |
| POST | `/notify/unsubscribe?token=` | no auth; sets `notify_email = false` and drops queued email rows, answering an HTML confirmation. `token` may also be a form field; a mail client's RFC 8058 one-click POST (`List-Unsubscribe=One-Click` body, token in the query) is the same call. Expired → `410`, invalid → `400` |

`handle` is derived from the GitHub login at first sign-in, lowercased, `[a-z0-9-]`,
unique; it is the second half of the SSH login name.

## Projects

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects` | `[Project]` |
| POST | `/projects` | `{name, remote_url?, class, tz?, agent_default?, multiplexer?, expires_in_s?, personal_opt_out?}` → `Project` (`multiplexer`, added with I-502, is `tmux` or `herdr`, default `tmux`; the CLI sends the value it picked, see `features/run-and-attach.md` "Choosing the multiplexer"; any other value is `400 invalid`, and `herdr` while the newest published base predates herdr is `409 conflict` `base_update_needed`, below; `personal_opt_out: true`, added with I-490, keeps the account's `machine.nix` off this machine, as `repose run --no-personal` does; otherwise the first revision carries it, and when the host has no closure of that combination the machine is created on the project layer alone and the combined revision is built and applied right after, as a `build` op queued behind the create; `agent_default` defaults to `claude`; the CLI sends `config.toml`'s `default_agent`, I-241) (409 if `(user, remote_url)` or `(user, name)` exists). `expires_in_s` (600..86400, added with I-347) makes the project temporary: `expires_at = now() + expires_in_s`, and the api destroys it with no snapshot once that has passed (`features/stop-start-destroy.md` "Temporary machines"); outside that range, or with a `remote_url`, it is `400 invalid`. A temporary project counts toward the limits like any other. No waitlist gate here since I-290: the billing gate runs as on start, so a user without a plan gets `402 payment_required` `detail.reason = subscription_required`, and one whose allocated disk would pass the plan gets `disk_limit` |
| GET | `/projects/destroyed?before=&limit=` | `[DestroyedProject]`: the user's destroyed projects that still have a restorable snapshot, newest destroy first (I-167), `limit` (1..200, default 100) of them. `before=<project id>` (DECISIONS I-420) gives the ones destroyed before that one, ordered by `(destroyed_at, id)`; an id that is not one of the user's destroyed projects gives `[]`; a bad `limit` or `before` is `400 invalid`. Added with I-167 |
| POST | `/projects/restore` | `{slug \| project_id \| snapshot_id, name?, start?: bool=true}` → `202 {op_id, project_id, name, slug, snapshot_id, snapshot_created_at, from_project_id}`. Restores as a new project called `name` (default: the source's name). `slug` means the live project with that slug if there is one, else the user's destroyed projects with it; the newest restorable snapshot among them is used unless `snapshot_id` names one. `404 not_found` when nothing can be restored (`detail.reason: "no_snapshot"` when the project exists); `409 conflict` with `detail: {reason: "name_taken", name}` when a live project holds the name, and with `detail.reason: "destroying"` when `slug` names a live project whose destroy has not taken its final snapshot yet (retry in a few seconds; I-190). The new project gets the source's class, volume size, configuration and, when no live project has it, its `remote_url` (I-167). Added with I-167 |
| GET | `/projects/:id` | `Project` |
| PATCH | `/projects/:id` | `{class?, hold_base_updates?, agent_default?, multiplexer?, tz?, expires_at?: null, personal_opt_out?}` (`multiplexer`, added with I-502, `tmux` or `herdr`, any state but `destroying`: the stored value changes at once and reaches the guest in `project_json` at its next start, never sooner (I-502); the answer's `Project.multiplexer` is the new value; `herdr` on a project whose `base_version` predates herdr is `409 conflict` `base_update_needed`, below; any other value is `400 invalid`; `personal_opt_out`, added with I-490, turns the account's `machine.nix` off or back on for this machine and queues a revision without or with it, applied as a personal change is; `expires_at: null` keeps a temporary project, `repose keep`: it is a normal one from then on; any other value is `400 invalid`, and a project already `destroying` answers `409 conflict`; added with I-347. Class change requires stopped; `tz` is an IANA zone name, `400 invalid` otherwise, and is what the guest's next start writes to `/etc/repose/env`: the CLI sends it when the laptop's zone differs from the project's, I-198. `tz` was added with I-198) |
| DELETE | `/projects/:id` | destroy (volume deleted, last snapshot kept 30 days) → `202 {op_id, state}`; the destroy is finished only when that op is `done` (`GET /projects/:id` then answers `404`). The project's state is `destroying` from the moment the DELETE answers; the op stops the guest, snapshots the stopped volume (reason `stop`) and deletes it (I-165). A failed destroy leaves the project in `error` with `last_error` and records a `destroy_failed` event, which notifies. A DELETE while a destroy op is open answers with that op. A dead guestd does not fail it (I-156). A temporary project's destroy is `destroy_guest` alone: no snapshot is taken and the ones it has expire at once, so it cannot be restored (I-347). `state` was added with I-165; `op_id` was always there |
| POST | `/projects/:id/start` | → `{op_id, restart, create}`; `restart: true` when the project was in `error` or running with its guestd not answering, and the op stops and reboots it on its newest built revision (I-157). `restart` was added with I-157. `create: true` (added with I-406, absent before) when the project has no guest because its create failed before one was made (no host with capacity, a failed build): the op is a `create` (`build`, `create_guest`, with a build log), the project reads `creating` from the answer on, and its placement is redone if its host cannot take it; `restart` is then false. `403 forbidden` with `detail.reason: "abuse_hold"` when the api stopped the project three times within 24 hours because a cryptocurrency miner was running, until an operator runs `repose-admin abuse clear` (added with I-239); the message names the process and the terms. A restore with `start` from a held project's snapshots (`POST /projects/restore`, `.../snapshots/:sid/restore`) is refused the same way, and so is every copy of a held project whether it starts or not (`POST /projects/restore`, `.../snapshots/:sid/restore` with `as_new_project`, `POST /projects/:id/fork`; I-460); an in-place restore without `start` is not. A stopped project whose only open ops are personal builds (a `machine.nix` change queued them, I-490) is not refused: the builds not started yet are queued again behind the start, so the machine boots on what it has and switches in place once built; one already building is waited for. `409 conflict` with `detail.reason: "restore_unfinished"` (added with I-461) when the project's newest create or restore is a restore that failed in its build or restore phase (after the old guest was destroyed): the project has no usable volume, and another restore is the way back |
| POST | `/projects/:id/stop` | `{snapshot: bool=true}` → `{op_id}` |
| GET | `/projects/:id/ops/:op_id` | `{state: pending\|running\|done\|error, error?: {code, message, detail?, fragment_line?, personal_line?}, log_url?, version, project_state, phase?}`; `message` is the sentence to show the user, `detail` the host's own wording for operators (I-159). Answers for a destroyed project's ops too. `?wait=<duration>` (`20s`, `1500ms`, or whole seconds; capped at 20 s) makes it a long-poll (I-236): the answer comes when the op's `version` differs from `?seen=<version>` (from the op as the request found it, without `seen`), when the op is `done` or `error`, when the wait runs out, or when the api begins a drain; a held (or already changed) answer carries the header `Repose-Long-Poll: 1`. Past 4 held reads per user the request is answered at once without the header, and the client pauses before its next read. `version` is opaque and changes with the op's state or phase or the project's state; `project_state` is the project's `state`; `phase` names the phase a `running` op is in (`build`, `start_guest`, ...). `version`, `project_state`, `phase` and `wait` were added with I-236; a client that ignores them polls as before |
| GET | `/projects/:id/ops/:op_id/log` | SSE stream of `BuildLog` lines (`id:` = seq, `data:` = `{seq, line, ts}`; `ts`, when the line reached the api, since I-322, and a client must accept a line without it), then a `done` event whose data is `{state}`; `?since=<seq>` or `Last-Event-ID` resumes after a line. Browsers cannot set headers on EventSource, so this route also accepts `?access_token=<jwt>`; the token is never logged and the route is the only one that accepts it. |
| POST | `/projects/:id/resize` | `{volume_bytes}` (grow only) |
| GET | `/projects/:id/route` | `{host_id, host_name?, host_state?, guest_ip, state, host_unreachable}` (used by CLI for `status` detail; `host_name` is what it shows, I-192) |
| GET | `/projects/:id/samples?window=1h\|24h\|7d` | `{window, step_s, vcpus, memory_bytes, points: [{ts, cpu, mem_used_bytes, cpu_pressure, host_cpu_wait, disk_used_bytes}], procs: [{comm, cpu_s, rss_max_bytes}]}`: the machine over the window from its minute samples while running (I-492, I-493). `window` defaults to `1h`; `step_s` is 60, 300 or 3600. `cpu` is the share of the class's vCPUs used, `cpu_pressure` the share of the time a task in the guest waited for a vCPU, `host_cpu_wait` the share of the time the machine waited for a host CPU (all 0 to 1). `mem_used_bytes` is memory in use as the guest sees it. `mem_used_bytes` and `cpu_pressure` are null in a bucket whose samples came from a guest older than I-493. `procs` is the eight process names with the most CPU in the window, their CPU seconds and largest RSS. Any other `window` is `invalid` |

```
Project { id, name, slug, remote_url, class, state, host_id?, guest_ip?,
          agent_default, hold_base_updates, base_version, config_revision_id,
          volume_bytes, disk_used_bytes?, created_at, started_at?,
          root_used_bytes?, root_size_bytes?,          -- the guest's root filesystem (I-567)
          signals?: {ssh_sessions, tmux_clients, agents: [{agent, window, state}],
                     guestd_ok},
          cost_today_cents, cost_month_cents,          -- 0 since I-289, kept one release
          running_seconds_today, running_seconds_month, -- what `repose status` shows (I-289)
          last_snapshot_at?, last_error?, host_unreachable, tz,
          idle?: {since, hourly_cents, memory_gb},     -- hourly_cents 0 since I-289, kept one release
          expires_at?,                                 -- set while temporary (I-347)
          personal_opt_out,                            -- machine.nix kept off (I-490)
          multiplexer }                                -- tmux|herdr, from the next start (I-502)

DestroyedProject { id, name, slug, class, remote_url?, volume_bytes,
          destroyed_at, name_free, restorable_until?,
          snapshot: {id, created_at, bytes, reason, expires_at?} }
```

`disk_used_bytes` is the thin volume's allocated blocks from the newest
sample (`lvs data_percent`), which keep a deleted file's blocks until the
guest's weekly `fstrim`; it is unchanged and the CLI and dashboard no
longer show it. `root_used_bytes` and `root_size_bytes` are the guest's
root filesystem from the same sample, statfs's blocks less those
available to `dev` and its blocks, in bytes: what the guest's writes run
out of, and what `repose status`, `repose ls` and the dashboard's Disk
card show (I-567). Both are absent when the newest sample has none (a
stopped project, a guest or hostd older than I-567, a guest that did not
answer); a client treats that as unknown.

`last_error` is the sentence the last failed op left (I-159), `null`
once an op succeeds; on a `stopped` project it may instead be
`abuse_stopped: stopped: a cryptocurrency miner (<name>) was running;
mining is not allowed on repose, see the terms at <url>`, left by a stop
the api made itself (added with I-239), which the CLI and the
dashboard show; `host_unreachable` is true while the project's host
has missed heartbeats for 90 seconds; `signals.guestd_ok` is false when
the newest sample found the environment's agent not answering (I-157).
The api has returned all three since I-157/I-159; they are documented
here since I-167. `signals` is absent until the first sample.
`idle` (DECISIONS I-262) is present only on a `running` project that has
gone 24 hours with no sample showing an SSH session, a tmux client, a
working agent (an agent in `idle` or `needs_input` is not working) or
guestd not answering, counted from `started_at`, and whose newest sample
is at most 10 minutes old; `since` is when that stretch began (looking
back at most 14 days, so on a longer stretch it is 14 days ago);
`hourly_cents` is 0 since I-289 (a plan buys memory, not hours) and
`memory_gb` the class's share of it. `running_seconds_today` (the user's
local day) and `running_seconds_month` (the subscription's current period,
else the calendar month) replace the cost fields, which answer 0 for one
release. Older clients ignore them; an api without them means "not idle"
and no hours.

A `DestroyedProject`'s `snapshot` is its newest restorable snapshot and
`restorable_until` that snapshot's `expires_at` (30 days after the
destroy); `name_free` says whether a restore can take the old name.

`slug` is `name` lowercased, `[a-z0-9-]`, unique per user; it is the first
half of the SSH login name and the tmux session name.

`multiplexer` (DECISIONS I-502) is what runs the machine's terminals
from its next start: `tmux` or `herdr`. It is the stored setting; what a
running machine runs can differ until it stops, and the CLI asks the
guest for that. `signals.agents` lists both multiplexers' agents, keyed
by `window` (a tmux window name or a herdr agent key); `signals.tmux_clients`
counts tmux clients only and is 0 on herdr, whose clients are in
`ssh_sessions`. Old shape, one release: a client that sends no
`multiplexer` gets `tmux` on POST and no change on PATCH, and a client
that ignores the field in `Project` sees what it saw before. The api
writes `multiplexer` into `project_json` on every create, start and
restore (`grpc-hostd.md`, I-26).

**The base gate.** `409 conflict` with `detail: {reason:
"base_update_needed", base_version, needs}` answers a POST or PATCH
asking for `herdr` when the base the machine would run is older than the
first base with herdr: for a PATCH the project's `base_version` (null
counts as older), for a POST the newest published base, and for a fork or
a restore as a new project the base the copy keeps, which is the
source's `base_version` (the newest published base when that is null;
I-549).
"Older" compares `base_versions.released_at`; a version with no row
counts as older. The first
base with herdr is `herdrMinBase` in `internal/api/http/multiplexer.go`;
while it is empty, or names no row of `base_versions`, every request for
`herdr` is refused this way with `needs: ""`. `message` is `<slug> runs
base <base_version>; herdr needs <needs> or newer.`, `<slug> has no base
yet; herdr needs <needs> or newer.` when `base_version` is null, or
`herdr is not available yet.` when `needs` is empty. A request for `tmux` is never
gated. Fork and restore as a new project copy the source's
`multiplexer`, and fall back to `tmux` when the gate would refuse;
an in-place restore keeps the project's.

## Config

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/config` | `{revision_id, fragment, menu?: MenuSelection, base_version, applied_at?, personal, personal_revision_id, personal_opt_out}`; `personal` is the `machine.nix` text the active revision carries (`""` for none) and `personal_revision_id` the account revision it came from (added with I-490) |
| PUT | `/projects/:id/config` | `{fragment}` or `{menu: MenuSelection}` (the api renders menu → fragment) → `{revision_id, op_id}`; build starts immediately; apply happens when the build succeeds. The revision carries the account's current `machine.nix` unless the project opted out (I-490). Open personal builds (queued by a `machine.nix` save) do not refuse it with `conflict`: one not started yet is superseded (its revision becomes `failed` with `superseded by revision <id>`), one already building is queued behind |
| GET | `/projects/:id/config/revisions` | list with `{revision_id, created_at, status: building\|applied\|failed, error?, fragment_line?, personal, personal_opt_out, personal_revision_id?, personal_line?}`; `personal` (bool) says the revision carries `machine.nix`, `personal_line` is an error's line in it (added with I-490) |
| POST | `/projects/:id/config/revisions/:rev/apply` | re-apply an older successful revision |
| GET | `/catalog` | menu catalog: `[{id, label, group, kind, description, options?: [{id, type, values, default}]}]` (packages and services the dashboard menu offers; `kind` is `service|package|agent|runtime`, `options` are enums the menu shows as selects; from `internal/menu`, DECISIONS I-44) |

A `MenuSelection` is an array whose items are either a catalog entry
`{id, options?: {name: value}}` or any nixpkgs package by attribute path
`{package: "python312Packages.black"}` (DECISIONS I-220); an item carries
one of `id` and `package`, never both, and a `package` item has no
`options`. A `package` matches
`^[A-Za-z_][A-Za-z0-9_+-]*(\.[A-Za-z_][A-Za-z0-9_+-]*)*$`, at most 200
characters, is not a catalog id (the catalog id means the catalog entry),
and appears once; anything else is `invalid` naming it. The array may be
empty (every item removed; the project stays menu-managed). GET returns the
selection as it was PUT. The api renders catalog entries in catalog order,
then the packages by name, into the generated fragment; a package nixpkgs
does not have fails the build with `eval_failed`
`nixpkgs has no package "<name>"; search https://search.nixos.org/packages`
(nix-build-contract.md "What the user reads"). The older shape, `[{id,
options?}]` only, is the same array without `package` items and stays valid.

## Certificates

| Method | Path | Body / result |
|---|---|---|
| POST | `/certs` | `{public_key (OpenSSH format), project_ids: [..]}` → `{certificate (OpenSSH cert), expires_at, gateway: {host, port, host_ca_pub}}`; one cert may carry several principals |
| POST | `/certs/revoke` | `{serial}` or `{all: true}` |

## Secrets

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/secrets` | `[{name, created_at, updated_at}]` (never values) |
| PUT | `/projects/:id/secrets/:name` | `{value}` (base64, max 64 KB) → pushed to a running guest via `UpdateSecrets` |
| DELETE | `/projects/:id/secrets/:name` | |

Names: `[A-Z][A-Z0-9_]{0,63}`. The names `ssh_host_ed25519_key`,
`ssh_host_ed25519_key-cert.pub` and `user_ca.pub` are reserved for the guest's
sshd material (delivered by hostd into the same tmpfs from the explicit
`CreateGuest` fields, see I-3 and I-10) and are rejected with `invalid`.
PUT also rejects `BASH_ENV`, `ENV` and `REPOSE_ENV_GEN` with `invalid`: the
guest uses them to keep each command's secrets current (I-475). A secret by
one of those names stored before is still listed and deletable.

## Snapshots

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/snapshots` | `[{id, created_at, bytes, reason}]` |
| POST | `/projects/:id/snapshots` | manual snapshot → `{op_id}`; the op, once `done`, carries `result: {snapshot_id}` (the api has always set it; documented with I-254) |
| POST | `/projects/:id/snapshots/:sid/restore` | `{as_new_project?: name, start?: bool=true}` → `{op_id, project_id}`; without `as_new_project`, replaces the stopped project's volume. `:id` may be a destroyed project. `POST /projects/restore` is the same restore resolved by name |
| POST | `/projects/:id/fork` | `{snapshot_id, count?: 1..10 = 1, name?, class?, start?: bool=true, request_id?: uuid}` → `202 {snapshot_id, snapshot_created_at, from_project_id, projects: [{project_id, name, slug, class, op_id}]}` (I-254). Restores `snapshot_id`, which must be one of the live project `:id`'s, into `count` new projects called `<name>-<k>` (default `name` is `<slug>-fork`; `k` the lowest numbers no live project uses; trimmed to a 40-character slug), each with the source's volume size, configuration and named secrets, class `class` (default the source's), and no `remote_url`, so the source keeps its checkout. All are created in one transaction: past the project limit (or, for `xl`, the xl limit) for all `count`, nothing is created and the answer is `400 invalid` with `detail: {limit, projects, requested}` (or `{xl_limit, xl, requested}`). A request with a `request_id` seen before answers with the projects that request made. Each project's restore op is its own; one failing leaves the others. Added with I-254 |

## Events and logs

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/events?since=&before=&limit=` | `[{id, ts, kind, agent?, summary}]`, newest first, `limit` (1..200, default 50) of them. `before=<event id>` (DECISIONS I-414) gives the events older than that one, ordered by `(ts, id)`, and ignores `since`; an id that is not one of the project's events gives `[]`. A bad `limit` or `before` is `400 invalid`. A client pages back by passing the last id of each page until a page is shorter than `limit`; an api older than I-414 ignores both and answers with its newest 50 |
| GET | `/projects/:id/logs?since=&kind=console\|build\|ops` | last 10k lines, JSON lines. Every line has `ts` and `kind`; a `build` line also has `op_id`, `seq` and `line` (`ts` and `kind` on build lines since I-322; a client must accept a build line without them, which an older api sends). `since` (RFC 3339, fractional seconds allowed) keeps the lines after it: for `build`, lines that reached the api after it (since I-322; before, `since` was ignored for `build`), for `ops`, ops created at or after it |

Event kinds are those of `features/notifications.md`; `agent_message`
(from `repose-notify`) and `agent_question` (from `repose-ask`, whose
`summary` is the question text) were added by DECISIONS I-244. A client
that does not know a kind shows it by name. `idle_running` (source
`api`, once per idle stretch; the summary names the class, the hourly
rate and `repose stop <slug>`) was added by DECISIONS I-262.
`temp_expiring` (source `api`, once per temporary project an hour before
its `expires_at`, only when it was made with more than an hour; the
summary names the time left and `repose keep <slug>`) and
`temp_destroyed` (source `api`, when a destroy the api started at the
expiry finishes) were added by DECISIONS I-347.

### Build log lines (DECISIONS I-320)

A build op's log is Nix's stderr as hostd streams it, plus a few lines of
hostd's and the api's own that name a step. Clients may read these to show
progress; a client must treat any other line as plain output and must not
fail on a line it does not know. Each is the whole line:

| Line | From | Means |
|---|---|---|
| `waiting for a build slot` | hostd | the host is running as many builds as it allows; this one is queued |
| `fetching the base` | hostd | the host is cloning the platform base this build needs, once per base per host |
| `evaluating configuration` | hostd | `nix eval` of the fragment has started |
| `building <name>` | hostd | `nix build` of the evaluated system has started |
| `built <store path>` | hostd | the build finished and passed the closure check |
| `switching the machine` | api | the build's apply phase was sent to the running guest |

Nix's own `these N derivations will be built:` and `these N paths will be
fetched (X MiB download, Y MiB unpacked):` lines (and their singular
forms) are Nix's, not part of this contract, and may change with Nix.

## Questions (DECISIONS I-244, I-245)

A question is what an agent asked with `repose-ask` in the guest:
`Question = {id, project_id, project (slug), agent, window, text, options:
[string] (0 to 3), state: pending|answered|cancelled|expired|no_channel,
answer: string|null, answered_via: dashboard|cli|ntfy|email|null,
created_at, expires_at, answered_at: time|null}`. `text` and `answer` are
tenant content (at most 1 KB each): shown to the owner, never logged.

| Method | Path | Body / result |
|---|---|---|
| GET | `/questions?state=pending\|all` | `{questions: [Question]}` across the user's projects, newest first, at most 50; `pending` (the default) lists only questions still waiting |
| GET | `/projects/:id/questions?state=pending` | `{questions: [Question]}`, pending first, then the latest, at most 20 |
| POST | `/projects/:id/questions/:qid/answer` | `{answer, via?: cli\|dashboard}` → `Question`. With options, `answer` must equal one of them (case does not matter; the stored answer uses the option's spelling), else `invalid` with `detail.options`. A question no longer pending (answered, expired, cancelled) is `conflict` with `detail.question`: the first answer wins |
| POST | `/projects/:id/questions/:qid/cancel` | no body → `Question` in state `cancelled`; the waiting `repose-ask` exits 5. `conflict` when it is no longer pending |
| GET | `/questions/reply?token=&via=` | no auth; the one-click link an email or ntfy notification carries for one option. The token is signed (the unsubscribe key, a separate domain) and names the question, the option and the question's expiry. Answers an HTML page showing the question and a button that POSTs; a GET never answers, because mail scanners open links. Expired → `410`, invalid → `400`, both HTML |
| POST | `/questions/reply?token=&via=` | no auth; answers with the token's option (`token` and `via` may also be form fields). ntfy's `http` button calls it. `200` HTML on success; a question already answered or closed → `409` naming the answer it has; 20 tries per question per minute, then `429` |

## Usage and billing

Plans, seats and the waitlist are DECISIONS I-289 and I-290; the numbers are
`docs/PRICING.md`. `billing_disabled` (503) is every route below when the
api has no `PADDLE_API_KEY`.

| Method | Path | Body / result |
|---|---|---|
| GET | `/usage?from=&to=` | per project per day: `{guest_hours: {small,large,xl}, gb_months, egress_gb, cost_cents, credit_cents}` (`cost_cents` is the egress overage share and `credit_cents` is 0 from `price_version = plan-v1`; both stay for one release) |
| GET | `/billing` | `{subscription: {plan: solo\|pro, status: trialing\|active\|past_due\|paused\|canceled, seats, period_start, period_end, next_billed_at, trial_end, cancel_at, scheduled_plan, next_charge_cents\|null, intro_until\|null}\|null, usage: {running_gb, memory_gb, disk_allocated_gb, disk_gb, egress_gb, egress_included_gb, overage_cents, projects, project_limit}, plans: [{id: solo\|pro, name, price_cents, currency: "USD", trial_days, seats, memory_gb, disk_gb, egress_gb, project_limit, available: bool, intro_price_cents, intro_months, intro_egress_gb}], intro_eligible: bool, seats: {total, held, free, waiting}, waitlist: {position, joined_at, invited_at, hold_until}\|null, paddle: {environment: sandbox\|live, client_token}}`. `usage` is for the current period (or the last 30 days without a subscription); `plans[].available` is whether that plan's seats are free for this user right now. Added by I-497, all additive: `intro_price_cents`, `intro_months` and `intro_egress_gb` are a plan's introductory price and egress allowance (0 for none); while a subscription's offer runs, `usage.egress_included_gb`, the overage and `/me`'s `limits.egress_gb` use the introductory allowance; `intro_eligible` is whether this user's checkout of such a plan gets it (a first subscription, with the discount configured); `next_charge_cents` is what Paddle charges at `next_billed_at`; `intro_until` is when the introductory price ends, null when there is none or Paddle has not fixed it |
| POST | `/billing/checkout` | `{plan}` → `{transaction_id, client_token, environment}`; the dashboard opens Paddle.js with the transaction. The api creates the Paddle customer if needed and a transaction for the plan's price with its seven-day trial and `custom_data.user_id`. `503 waitlisted` with `{position, joined_at, email}` when the plan's seats are not free (the user is on the waitlist from then on; an invited user's hold counts toward their own checkout); `409 conflict` `detail.reason = subscribed` when a subscription exists (change it with `/billing/plan`); `400 invalid` for an unknown plan |
| POST | `/billing/waitlist` | no body → `{position, joined_at}`; joins the waitlist without trying a checkout, idempotent (a second call answers the same place) |
| POST | `/billing/plan` | `{plan}` → `{plan, scheduled_plan, effective_at}`. A move to a plan with more seats (Solo to Plus or Pro, Plus to Pro) takes effect at once (Paddle prorates, `proration_billing_mode = prorated_immediately`) and needs the extra seats free, else `409 conflict` `detail.reason = no_seat` (a subscriber is never put on the waitlist). A move to fewer seats is scheduled for `period_end` and refused with `409 conflict` `detail.reason = over_plan` and `detail: {running_gb, disk_allocated_gb}` while the running memory or allocated disk would not fit |
| POST | `/billing/cancel` | no body → `{cancel_at}`: the subscription ends at `period_end` (during the trial, at `trial_end`); machines run until then. `409 conflict` `detail.reason = already_cancelled` |
| POST | `/billing/resume` | no body → `{plan, period_end}`: undoes a scheduled cancellation before it takes effect. `409 conflict` `detail.reason = not_cancelled` |
| POST | `/billing/portal` | no body → `{url}` of Paddle's customer portal (card, receipts, address); `{"for": "payment_method"}` → the portal deep link that updates the payment method |
| GET | `/billing/invoices` | from Paddle's transactions for the customer, newest first, up to 24: `[{id, number, status, currency, amount_cents, subtotal_cents, tax_cents, created_at, period_start, period_end, hosted_url, pdf_url}]` (`pdf_url` from Paddle's invoice PDF; `hosted_url` is the same link or null) |
| POST | `/billing/webhook` | Paddle's notification endpoint. No bearer token: the `Paddle-Signature` header (`ts=...;h1=...`) is the authentication, an HMAC-SHA256 of `ts:body` with `PADDLE_WEBHOOK_SECRET`, refused when `ts` is more than five minutes off. Handles `subscription.created|activated|trialing|updated|past_due|paused|resumed|canceled`, `transaction.completed|payment_failed`; idempotent on `event_id` (the `paddle_events` primary key); a duplicate answers `200 {received, duplicate}`, a bad signature `400 invalid` with the event type logged and nothing else |
| GET | `/public/seats` | no auth → `{total, free, waiting}`; the landing page's count. Cached for a minute |

`payment_required` (402) is every compute gate (`POST /projects` with a
start, `/projects/:id/start`, restore, fork, resize while running, growing
a volume) and carries `detail.reason`:

| `detail.reason` | When | `detail` also carries |
|---|---|---|
| `subscription_required` | no subscription in `trialing\|active\|past_due` (a new account, an ended one) | `waitlist: {position, joined_at}\|null` |
| `plan_limit` | the running memory plus this machine's class would pass the plan's memory | `plan, limit_gb, used_gb, projects: [slug]` (the machines using it) |
| `disk_limit` | the allocated disk plus this project's volume would pass the plan's disk | `plan, limit_gb, used_gb` |
| `egress_limit` | this period's egress passed four times the allowance; machines are stopped until `period_end` | `plan, limit_gb, used_gb, until` |
| `past_due` | the last payment failed (day 0 to 3) | |
| `suspended` | three days past due, or an operator suspension | |

`message` is the whole sentence in every case, so an older CLI that prints
it is right. A `suspended` account may only call `GET /me`, `GET /billing`
and `POST /billing/portal`.

## Internal (gateway)

| Method | Path | Body / result |
|---|---|---|
| GET | `/internal/route?login=<slug>.<handle>` | `{project_id, guest_ip, state, principals}` |
| GET | `/internal/revoked?since=` | `[serial]` |
| GET | `/internal/ca` | `{user_ca_pub, host_ca_pub}` |
| POST | `/internal/sessions` | `{project_id, event: opened\|closed, cert_serial, session_id}` (gateway reports, feeds signals; `session_id` names the relay, up to 64 of `[A-Za-z0-9-]`, so two connections under one certificate are two sessions; absent from a gateway older than I-176, accepted for one release as one session per certificate) → `{open}` |
| GET | `/internal/hosts` | `[{host_id, wg_pubkey, wg_ip, guest_cidr, state}]` for the edge's WireGuard peer sync |
| POST | `/internal/gateway-certs` | `{public_key, project_id}` → `{certificate}`: 5-minute user certificate for the gateway's own key, principal = project id, key_id suffixed `:via-gateway` |
| POST | `/internal/events` | `{source_ip, agent, kind, summary}`: hook events that reached the edge over HTTP because guestd was unavailable; the api maps `source_ip` to a project and dedupes on `(project_id, agent, kind, ts to the second)`. `kind` is one a guest may report (`completed`, `needs_input`, `error`, `agent_message`); any other is `400 invalid` (DECISIONS I-441). An `agent` that is not one guestd sends (`claude`, `opencode`, `codex`, `gemini`, `pi`, `shell`) is stored as no agent |

## Rate limits

Per user: 600 GET requests/min, 60/min for every other method, 10/min on
`POST /certs`, 5/min on `PUT /config` (I-187: a waiting CLI polls twice a
second and shares the budget with the user's dashboard). A refusal is
`429 rate_limited` with `Retry-After` in seconds; nothing ran, so the
client may send the same request again after it. A held op read (`?wait`) is
one GET however long it is held. Per gateway: unlimited on
internal.

Hook events on `POST /internal/events` count toward the project's 600
guest-raised events an hour together with the ones hostd relays over vsock
(DECISIONS I-445). Past the cap the answer is still 202, with a nil
`event_id` and nothing stored.

## Fake

`internal/fakes/api`: an `httptest.Server` implementing every route above
with an in-memory store, deterministic ids, and a switch to make any route
return any error code. The CLI and dashboard tests run against it.
