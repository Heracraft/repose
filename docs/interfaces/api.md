# HTTP API

Base: `https://api.repose.run/v1`. JSON. Auth: `Authorization:
Bearer <Logto access token>` for user routes, or `Bearer rpt_...`, an API
token the user minted (DECISIONS I-1314): the api looks it up by its
sha256, loads its user, and refuses the call with `403 forbidden` when
the route needs a scope the token lacks (`detail.reason = token_scope`,
`detail.needs`, `detail.scopes`), when the token is bound to another
project (`token_project`), or when the route is one no token opens
(`token_never`: the account, billing, SSH certificates, the browser view,
tokens). An expired or revoked token is `401 unauthenticated` with
`detail.reason = token_expired` or `token_revoked`. `GET /me` made with a
token adds `token`, the row `GET /me/tokens` lists for it. The
routes a scope opens are listed in docs/features/tokens.md. The Logto
resource is (resource
`https://api.repose.herakraft.co`, an opaque audience that kept its name
when the api moved to `api.repose.run`, DECISIONS I-1156; verified against
Logto JWKS, `sub` is the user id). Internal routes under `/internal/` are for the gateway and use the
gateway's mTLS client certificate: CN `gateway`, from the host CA; any other
client certificate, a host's included, fails the TLS handshake (I-431). Errors: `{ "error": { "code": "...",
"message": "...", "detail": {...} } }` with codes `unauthenticated`,
`forbidden`, `not_found`, `invalid`, `conflict`, `payment_required`,
`capacity`, `waitlisted`, `rate_limited`, `billing_disabled`, `internal`.
Three codes come with a 503 and are answers: `capacity` (no
host can take the machine), `waitlisted` and `billing_disabled`; any other
5xx is the api failing. This is the one list; the dashboard's outage bar
(`ANSWER_503` in `apps/web/src/lib/api/errors.ts`) and its docs point here
(I-393).
`waitlisted` (503) refuses a plan's checkout while the fleet has no free
seat and puts the user on the waitlist; `detail` is `{position, joined_at,
email}` and `message` is the whole sentence (DECISIONS I-269, I-290).
`payment_required` (402) carries `detail.reason`, listed under "Usage and
billing".
A create or an as-new restore past the account's project cap is `400
invalid` with `detail: {reason: "project_limit", limit, projects,
requested?}`; the cap is 100 live projects,
running or stopped, on every plan (DECISIONS I-569; `reason` added
then, `limit` and `projects` were there before).
Every response
carries `X-Request-Id`, and `X-Repose-CLI-Latest` (the newest CLI release,
`vX.Y.Z`) once the api has read it from the releases/latest redirect; a
client must accept an answer without it (DECISIONS I-626).
`X-Repose-CLI-Oldest` (`vX.Y.Z`) is the oldest CLI release this api still
answers as this document describes. It is absent while every release is,
which is the state `clirelease.Oldest = ""` ships in; the commit that
removes a request or answer shape raises it to the first release that no
longer carries that shape. A client must accept an answer without it, and
the header never refuses anything: a CLI below it says so on stderr and
goes on (DECISIONS I-1088).

## Users

| Method | Path | Body / result |
|---|---|---|
| GET | `/me` | `{id, handle, email, github_login, tz, created_at, billing: {status: none\|trial\|active\|past_due\|suspended\|exempt, plan: solo\|plus\|pro\|own\|null, seats, period_end, trial_end, cancel_at, has_card, trial_credit_cents}, limits: {projects, xl, memory_gb, disk_gb, egress_gb, hosts}, notify: {email, ntfy_url, gchat: {space, broken_at}\|null, hold_seconds}, github: {login_copy, token_expires_at}, waitlist: {position, joined_at, invited_at, hold_until}\|null}` (`status` is a projection of the subscription, I-289: `none` is an account with no plan yet, `trial` a `trialing` subscription; `trial_credit_cents` is always 0 and `xl` is 1 on Pro, 0 otherwise, both kept one release; `waitlist` is set while the user holds a place, I-290; `limits.projects` is the account's project cap, running or stopped, the same on every plan: 100, or `users.project_limit` when an operator set it higher, I-569; `notify.gchat` is the Google Chat channel, I-692: `space` is the id from the webhook URL and `broken_at` is when Google refused it, else null; the URL itself is never returned; `notify.hold_seconds` is the presence hold, I-1282: how long a report waits while the owner is at the machine, 0 for off; `limits.hosts` is how many hosts of their own the account may register, I-1364: the plan's figure, 1 on the Own hardware plan and on a billing-exempt account, 0 otherwise, and `POST /hosts` refuses past it; `github` is the account's GitHub answer, I-1340 and I-1342: `login_copy` is whether a laptop may copy this account's GitHub CLI login into its machines, and `token_expires_at` is what GitHub said about the `GH_TOKEN` the account stored, null when it said nothing or when that secret is gone; the token itself is a secret and is never returned) |
| PATCH | `/me` | `{tz?, notify: {email?: bool, ntfy_url?: string\|null, gchat_url?: string\|null, hold_seconds?: int}, github?: {login_copy?: bool, token_expires_at?: time\|null}}`. `github.login_copy` false tells every laptop of the account to stop copying its GitHub CLI login (I-1340); a laptop that has turned the copy off in its own `config.toml` stays off whatever it says, so the field can only subtract. `github.token_expires_at` is what GitHub answered for the `GH_TOKEN` just stored, or null to clear; a time more than a day in the past or more than ten years ahead is `400 invalid`, and nothing is ever refused by it (I-1342). `DELETE /me/secrets/GH_TOKEN` clears it. `hold_seconds` is 0 (off) to 3600, else `400 invalid`; it holds `agent_done`, `agent_message` and `error` only (I-1276: a question and a stopped agent are never held). An `ntfy_url` must be http(s); one whose host is `localhost` or a literal non-public address is `400 invalid` (DECISIONS I-444). A `gchat_url` must be `https://chat.googleapis.com/v1/spaces/<id>/messages?key=..&token=..`, else `400 invalid` without quoting it; it is stored with only its key and token as query (a pasted `threadKey` is dropped), and saving one clears `broken_at`. `null` or `""` clears either URL and drops that channel's queued notifications (I-692) |
| DELETE | `/me` | begins cancellation (stops guests, 30-day retention) |
| POST | `/me/notify-test` | sends a test event to every configured channel → `{email?: ok\|error, ntfy?: ok\|error, ntfy_error?, gchat?: ok\|error, gchat_error?}`; a channel that is off is left out (a broken Google Chat webhook is tried, and an `ok` clears `broken_at`, I-692), and `ntfy_error` (added with I-622) says in a few words why the user's ntfy URL failed: `the server answered 403`, `the address is not on the public internet`, `the server redirects, and repose follows no redirect`, `no answer within 15 seconds`, `the host name does not resolve`, `could not connect`; `gchat_error` uses the same words, plus `the server answered <status>: the webhook was deleted, or its space removed it` for 401, 403 and 404 (the status is the one Google answered). It never repeats the URL. The message is its own kind, titled `repose: test` (body `This is a test from repose.`), and is never stored as an event (I-691). At most 6 a minute per user; the seventh is `429 rate_limited` with `Retry-After` (I-691) |
| GET | `/me/config` | the account's personal layer, `machine.nix` (DECISIONS I-490): `{revision_id, fragment, created_at, source: cli\|dashboard, opted_out: [slug]}`; `revision_id`, `created_at` and `source` are null and `fragment` is `""` when the account has none. `opted_out` lists the live projects that keep it off |
| PUT | `/me/config` | `{fragment, base_revision_id?, source?: cli\|dashboard}` → `{revision_id, fragment, created_at, source, projects: [{project_id, slug, revision_id, op_id?, merged?, running}], unchanged?}`. `fragment: ""` removes the layer. Validated like a project fragment: 256 KB, then Nix's parser, whose `400 invalid` names `machine.nix:L:C` with `detail.personal_line`; a secret's value in it fails the build that would carry it (`machine.nix contains the value of secret NAME`). `base_revision_id`, when present, must be the account's current revision (`""` or null when it has none), else `409 conflict` "machine.nix on your account changed since this copy was pushed" with `detail: {current_revision_id, source, created_at}`; the CLI sends the revision it last pushed. The same text as the current one answers `unchanged: true` and rebuilds nothing. A new text gives every live project that has not opted out, running ones first and at most 50, a revision with its current fragment and the new layer, and a build op queued behind whatever the project is doing: a running machine switches in place, a stopped one at its next start (`projects[].running`). A build of an earlier personal change that has not started takes the new text instead (`merged: true`, no `op_id`). A personal build that fails leaves the machine on its current revision and records a `personal_failed` event naming `machine.nix` |
| GET | `/me/config/revisions` | `[{revision_id, created_at, source, bytes}]`, newest first, at most 50 |
| GET | `/me/tokens` | the account's API tokens (DECISIONS I-1314): `[{id, name, prefix, scopes: [read\|projects\|config\|secrets\|triggers\|agents], project: {id, slug}\|null, expires_at, created_at, last_used_at\|null, revoked_at\|null}]`, live ones first, then those revoked within 30 days. Never the token |
| POST | `/me/tokens` | `{name, scopes: [...], project_id?, expires_in_days?}` → 201, the row above plus `token`, the one answer that carries it (`rpt_` and 43 characters). `name` matches `[a-z0-9][a-z0-9-]{0,31}` and is free among the account's live tokens, else `409 conflict`, `detail.reason = name_taken`; `scopes` is one or more of the six, else `400 invalid`, `detail.reason = scopes`; `expires_in_days` is 1 to 365, 30 unless given (I-1320); `project_id` binds the token to that project (I-1318), `404 not_found` for one the account does not have. A 21st live token is `400 invalid`, `detail.reason = token_limit` |
| DELETE | `/me/tokens/:name` | 204; the token stops working at once and the row stays 30 days as `revoked_at`. `404 not_found` for a name the account has no live token by |
| GET | `/notify/unsubscribe?token=` | no auth; the link in an email's unsubscribe line and its `List-Unsubscribe` header (13-notifications.md §5.6, DECISIONS I-442). The token is signed and names the user and an expiry 90 days after the email was sent; a token of the earlier shape (the user id alone, no expiry) is still accepted until the release after next. Answers an HTML page with a button that POSTs; a GET changes nothing, because mail scanners open links. Expired → `410`, invalid → `400`, both HTML |
| POST | `/notify/unsubscribe?token=` | no auth; sets `notify_email = false` and drops queued email rows, answering an HTML confirmation. `token` may also be a form field; a mail client's RFC 8058 one-click POST (`List-Unsubscribe=One-Click` body, token in the query) is the same call. Expired → `410`, invalid → `400` |

`handle` is derived from the GitHub login at first sign-in, lowercased, `[a-z0-9-]`,
unique; it is the second half of the SSH login name.

## Hosts you own (DECISIONS I-1366)

A host the user owns is a machine of their own that joined their account:
their projects run on it and nobody else's ever does, and it is outside
the seat count, the waitlist and the host gauges, because repose did not
buy it. These three routes are the whole of it; the hosts repose runs are
the operator's (`repose-admin hosts`) and appear on none of them.

| Method | Path | Body / result |
|---|---|---|
| GET | `/hosts` | `[Host]`, oldest first: the hosts this account owns. A host repose runs is never listed, and neither is another account's, so an empty list is the answer for almost every account |
| POST | `/hosts` | `{name?}`, or no body at all, which is the same as no name → `201`, the `Host` plus `join_token`, `join_token_expires_at` and `install`: the one answer that carries the token, as a minted API token is. `name` matches `[a-z0-9][a-z0-9-]{0,31}` and is free among this account's hosts; without it the api names the host `host-1`, `host-2`, … in the account, and the caller never invents a name. The token is single-use and valid 24 hours, the same token `repose-admin hosts add` mints, and the host trades it for its identity with `Register` (`grpc-hostd.md`) |
| DELETE | `/hosts/{id}` | `204`: the host is drained and retired, its certificate serials and its unused join token cleared so it cannot come back on them, and a machine that is still dialling is refused. A host that still holds live machines is `409 conflict` `host_has_projects` with their slugs, **unless its state is `unreachable` or `lost`**, where it is retired with them still on it (I-1420): a destroy's phases are skipped only for `lost` and `retired` (I-924), so refusing left an account unable to remove the host, unable to destroy the machines and unable to add a replacement against the plan's one slot. The machines are not destroyed, and every `repose rm` on them then drains to no phases; what is lost is said at the client's confirmation, not here. Already retired answers `204` too, so a repeated remove is not an error. The row stays, so `GET /hosts` still lists it as `retired`; `repose host ls` and the dashboard leave a removed host out, because it is not hardware the user has. It also clears `last_error` on the account's machines that are in `error`, hold no host and failed on `capacity`: a placement that found no host of the account's own frees the machine and leaves its sentence on the row, so without this "your host mac-mini has not registered yet" outlives the host and the dashboard names hardware the user no longer has. The ops keep the history, and the next start says what is true then |

```
Host { id, name, state, provider?, arch?, mem_bytes, vcpus, cores,
       projects, running_projects, last_heartbeat_at?, registered_at?,
       created_at,
       join_token?, join_token_expires_at?,     -- the POST answer only
       install?: {command, argument, url} }     -- the POST answer only
```

`state` is `hosts.state`: `registering` (the token is minted and nothing
has used it yet), `ready`, `draining`, `unreachable` (no heartbeat for 90
seconds, which for a machine at home is "switched off"), `retired`,
`lost`. `mem_bytes`, `vcpus` and `cores` are 0 and `arch`,
`last_heartbeat_at` and `registered_at` are absent until the host
registers; `arch` is the CPU architecture it reported (`x86_64`,
`aarch64`, DECISIONS I-1448), omitted when unknown and never a
`/nix/store/...` closure path. `projects` counts the account's live projects
placed on it and `running_projects` those of them in a state that holds
memory, which is what the dashboard's list shows.

`install` is the one line to run on the machine, with its argument
already in it, the argument on its own, and the address of the page that
explains it. `argument` is the join bundle (DECISIONS I-1380): one
base64url string, no padding, of `{"v": 1, "token": <join_token>, "wg":
{"private_key", "address", "edge_pubkey", "edge_endpoint"}}`, where `wg`
is the machine's place on the hub, minted by this request and absent
when the deployment has no edge recorded (`repose-admin edge init`).
`command` is the `host_install_command` setting (`repose-admin hosts
install-command`) with `%s` replaced by `argument`, and `url` is the
dashboard's `/docs/own-hardware`. `command` is `""` while no setting has
been recorded, and a client then shows `argument` and the page instead of
a line it made up itself. The setting is a setting and not an
environment variable for the reason `loki_url` is (DECISIONS I-95): it
names something outside this deployment, and moving the installer must
not be a redeploy of the api.

What the request does on the hub (I-1380): it allocates the host's guest
`/22` and WireGuard address (kept on a reissue), mints a bootstrap key
pair and stores the public half on the row, so `GET /internal/hosts`
lists the host from this moment and the edge peers it before the machine
boots. `Register` (`grpc-hostd.md`) keeps the addresses and mints a fresh
key pair as it always has, so the private key in the bundle is dead after
registration. The private key is in this answer and nowhere else: not in
a log line, not in the database.

Refusals of `POST /hosts`, beside the envelope's own:

| Code | Status | When |
|---|---|---|
| `invalid` | 400 | `detail.reason = "name"`: a `name` outside `[a-z0-9][a-z0-9-]{0,31}` |
| `conflict` | 409 | `detail.reason = "name_taken"`: this account already has a host by that name. Names are unique per account, so another account's `mac-mini` is no bar and is never named. A **removed** host's name is not taken: the row it kept for the history takes the name back, with a new token, the same id and nothing of the machine it was |
| `conflict` | 409 | `detail.reason = "host_limit"`, `detail.limit`: the account already owns as many hosts as its plan allows (one on the Own hardware plan, I-1364; I-1366). The figure is the plan's, read from `billing.Plan.Hosts`, and lives nowhere else |
| `payment_required` | 402 | `detail.reason = "own_hardware_plan_required"`: the account's plan sells no host (`limit` 0). The `message` names the plan, its price and its host count from the plan table (I-1364), so a caller who reads only the refusal knows what a host costs. A billing-exempt account is allowed one, as it is allowed everything else |

`DELETE /hosts/{id}` answers `404 not_found` for an id that is not one of
the account's hosts, and `409 conflict` with `detail: {reason:
"host_has_projects", projects: [slug]}` while live projects are still
placed there. A project moves host only by restore (DESIGN.md §6), so
the machines go first: destroy them, or restore them onto another host.

## Projects

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects` | `[Project]` |
| POST | `/projects` | `{name, remote_url?, class, tz?, agent_default?, multiplexer?, expires_in_s?, personal_opt_out?, placement?}` → `Project` (`placement`, added with I-1366, is `own` or `hosted`, default `own`: `own` puts the machine on a host this account owns when it owns any and on the hosted fleet when it owns none, `hosted` asks for the fleet whatever it owns. Any other value is `400 invalid`, `detail.reason = "placement"`; `hosted` from an account whose plan sells no memory on repose's own hosts is `402 payment_required`, `detail.reason = "hosted_plan_required"`. Since I-1390 placement is answered inside this request, before the row is written: a project no host can take is `503 capacity` with the sentence the create op used to fail with, `detail.reason` `no_own_host` (with `detail.host`, `detail.host_state` and `detail.hosted_allowed`, whether the plan lets it ask for `hosted`) when the account's own hardware is what refused, `no_capacity` or `no_cpu` for the fleet, and nothing is written; the 201 carries `host_id`. An api older than I-1390 answers 201 and fails the op instead, and a client that handled that still works, since the words are the same. `multiplexer`, added with I-502, is `tmux` or `herdr`, default `tmux`; the CLI sends the value it picked, see `features/run-and-attach.md` "Choosing the multiplexer"; any other value is `400 invalid`, and `herdr` while the newest published base predates herdr is `409 conflict` `base_update_needed`, below; `personal_opt_out: true`, added with I-490, keeps the account's `machine.nix` off this machine, as `repose run --no-personal` does; otherwise the first revision carries it, and when the host has no closure of that combination the machine is created on the project layer alone and the combined revision is built and applied right after, as a `build` op queued behind the create; `agent_default` defaults to `claude`; the CLI sends `config.toml`'s `default_agent`, I-241) (409 if `(user, remote_url)` or `(user, name)` exists). `expires_in_s` (600..86400, added with I-347) makes the project temporary: `expires_at = now() + expires_in_s`, and the api destroys it with no snapshot once that has passed (`features/stop-start-destroy.md` "Temporary machines"); outside that range, or with a `remote_url`, it is `400 invalid`. A temporary project counts toward the project cap and the plan like any other. No waitlist gate here since I-290: the billing gate runs as on start, so a user without a plan gets `402 payment_required` `detail.reason = subscription_required`, and one whose projects hold more than the plan's disk with a new volume's first gigabyte added gets `disk_limit` (I-585) |
| GET | `/projects/destroyed?before=&limit=` | `[DestroyedProject]`: the user's destroyed projects that still have a restorable snapshot, newest destroy first (I-167), `limit` (1..200, default 100) of them. `before=<project id>` (DECISIONS I-420) gives the ones destroyed before that one, ordered by `(destroyed_at, id)`; an id that is not one of the user's destroyed projects gives `[]`; a bad `limit` or `before` is `400 invalid`. Added with I-167 |
| POST | `/projects/restore` | `{slug \| project_id \| snapshot_id, name?, start?: bool=true}` → `202 {op_id, project_id, name, slug, snapshot_id, snapshot_created_at, snapshot_bytes, from_project_id}` (`snapshot_bytes`, the stored size, added with I-595). Restores as a new project called `name` (default: the source's name). `slug` means the live project with that slug if there is one, else the user's destroyed projects with it; the newest restorable snapshot among them is used unless `snapshot_id` names one. `404 not_found` when nothing can be restored (`detail.reason: "no_snapshot"` when the project exists); `409 conflict` with `detail: {reason: "name_taken", name}` when a live project holds the name, and with `detail.reason: "destroying"` when `slug` names a live project whose destroy has not taken its final snapshot yet (retry in a few seconds; I-190). The new project gets the source's class, volume size, configuration and, when no live project has it, its `remote_url` (I-167). Added with I-167 |
| GET | `/projects/:id` | `Project`, plus `op_id` when the project has an op pending or running (added with I-722; ops are serialised per project, so it is the one in flight). `repose run` follows that op instead of polling states: a `waitState` that returned on `error` used to be followed by a start, so one user action produced two ops and the create's error was never read (teksafari-org, 2026-10-13). The list route does not carry it (a query per project is an N+1), and a client that ignores it behaves as before |
| PATCH | `/projects/:id` | `{class?, hold_base_updates?, agent_default?, multiplexer?, tz?, expires_at?: null, expires_in_s?, personal_opt_out?, keep_awake?}` (`keep_awake`, added with I-820, keeps this machine out of the rest rule and takes effect at once, in both directions: the rule reads the column on its next pass, and a machine already frozen when it is turned on is woken by the request itself. Nothing is pushed to the machine, because the api, not hostd, decides what rests; the answer's `Project.keep_awake` is the new value. `multiplexer`, added with I-502, `tmux` or `herdr`, any state but `destroying`: the stored value changes at once and reaches the guest in `project_json` at its next start, never sooner (I-502); the answer's `Project.multiplexer` is the new value; `herdr` on a project whose `base_version` predates herdr is `409 conflict` `base_update_needed`, below; any other value is `400 invalid`; `personal_opt_out`, added with I-490, turns the account's `machine.nix` off or back on for this machine and queues a revision without or with it, applied as a personal change is; `expires_at: null` keeps a temporary project, `repose keep`: it is a normal one from then on; any other value is `400 invalid`, and a project already `destroying` answers `409 conflict`; added with I-347. `expires_in_s` (600..86400, added with I-612) keeps a temporary project temporary and sets `expires_at = now() + expires_in_s`, `repose keep NAME 3h`; outside that range, on a project that is not temporary, or beside `expires_at: null`, it is `400 invalid`, and a project already `destroying` answers `409 conflict`. Class change requires stopped; `tz` is an IANA zone name, `400 invalid` otherwise, and is what the guest's next start writes to `/etc/repose/env`: the CLI sends it when the laptop's zone differs from the project's, I-198. `tz` was added with I-198) |
| DELETE | `/projects/:id` | destroy (volume deleted, last snapshot kept 30 days) → `202 {op_id, state, final_snapshot}`; the destroy is finished only when that op is `done` (`GET /projects/:id` then answers `404`). The project's state is `destroying` from the moment the DELETE answers; the op stops the guest, snapshots the stopped volume (reason `stop`) and deletes it (I-165). A failed destroy leaves the project in `error` with `last_error` and records a `destroy_failed` event, which notifies. A DELETE while a destroy op is open answers with that op. A dead guestd does not fail it (I-156). A temporary project's destroy is `destroy_guest` alone: no snapshot is taken and the ones it has expire at once, so it cannot be restored (I-347). `final_snapshot` is whether this destroy will take one, which a client needs before any phase has run because it says what is kept the moment this route answers: false for a temporary project and false when the project's host is `lost` or `retired`, where every phase including the snapshot is skipped (I-924). A client that does not see the field must read it as true, which is what it was for every host that was still there; `repose rm` promised a snapshot for a machine on a Mac the user had deleted, whose snapshot count was 0 (ENG-119, I-1418). `state` was added with I-165; `final_snapshot` with I-1418; `op_id` was always there |
| POST | `/projects/:id/start` | → `{op_id, restart, create}`; `restart: true` when the project was in `error` or running with its guestd not answering, and the op stops and reboots it on its newest built revision (I-157). `restart` was added with I-157. `create: true` (added with I-406, absent before) when the project has no guest because its create failed before one was made (no host with capacity, a failed build): the op is a `create` (`build`, `create_guest`, with a build log), the project reads `creating` from the answer on, and its placement is redone if its host cannot take it; `restart` is then false. `403 forbidden` with `detail.reason: "abuse_hold"` when the api stopped the project three times within 24 hours because a cryptocurrency miner was running, until an operator runs `repose-admin abuse clear` (added with I-239); the message names the process and the terms. A restore with `start` from a held project's snapshots (`POST /projects/restore`, `.../snapshots/:sid/restore`) is refused the same way, and so is every copy of a held project whether it starts or not (`POST /projects/restore`, `.../snapshots/:sid/restore` with `as_new_project`; I-460); an in-place restore without `start` is not. A stopped project whose only open ops are personal builds (a `machine.nix` change queued them, I-490) is not refused: the builds not started yet are queued again behind the start, so the machine boots on what it has and switches in place once built; one already building is waited for. `409 conflict` with `detail.reason: "restore_unfinished"` (added with I-461) when the project's newest create or restore is a restore that failed in its build or restore phase (after the old guest was destroyed): the project has no usable volume, and another restore is the way back. A `start` op that ends `done` with the machine running but not as asked carries `result.warning: {code, message}` (I-590): its new system did not boot and it runs its previous one (`boot_failed`), or its switch to a newer revision failed (the apply's code); `message` is the sentence to show. An older api never sets it |
| POST | `/projects/:id/stop` | `{snapshot: bool=true}` → `{op_id}`; the op, once `done`, carries `result: {snapshot_id}` when the stop recorded a snapshot (the api has always set it; documented with I-570, which reads it). The project reads `stopping` once the engine picks the op up, which can be after the `202`. A stop op that fails leaves the project in `error` with `last_error` (`stopping <slug> failed: ...`) and, since I-710, records a `stop_failed` event, which notifies; the CLI waits only until the engine has claimed the op, not for it to finish. A stop's snapshot that fails after the guest is down keeps `snapshot_failed` (I-158) |
| GET | `/projects/:id/ops/:op_id` | `{state: pending\|running\|done\|error, error?: {code, message, detail?, fragment_line?, personal_line?}, log_url?, version, project_state, phase?}`; `message` is the sentence to show the user, `detail` the host's own wording for operators (I-159). Answers for a destroyed project's ops too. `?wait=<duration>` (`20s`, `1500ms`, or whole seconds; capped at 20 s) makes it a long-poll (I-236): the answer comes when the op's `version` differs from `?seen=<version>` (from the op as the request found it, without `seen`), when the op is `done` or `error`, when the wait runs out, or when the api begins a drain; a held (or already changed) answer carries the header `Repose-Long-Poll: 1`. Past 4 held reads per user the request is answered at once without the header, and the client pauses before its next read. `version` is opaque and changes with the op's state or phase or the project's state; `project_state` is the project's `state`; `phase` names the phase a `running` op is in (`build`, `start_guest`, `wake`, ...; `wake`, added with I-818, resumes a frozen machine before the first phase of the plan that reaches its guest, and is skipped when there is nothing to wake). `version`, `project_state`, `phase` and `wait` were added with I-236; a client that ignores them polls as before |
| GET | `/projects/:id/ops/:op_id/log` | SSE stream of `BuildLog` lines (`id:` = seq, `data:` = `{seq, line, ts}`; `ts`, when the line reached the api, since I-322, and a client must accept a line without it), then a `done` event whose data is `{state}`; `?since=<seq>` or `Last-Event-ID` resumes after a line. Browsers cannot set headers on EventSource, so this route also accepts `?access_token=<jwt>`; the token is never logged and the route is the only one that accepts it. |
| POST | `/projects/:id/resize` | `{volume_bytes}` (grow only) |
| GET | `/projects/:id/route` | `{host_id, host_name?, host_state?, guest_ip, state, host_unreachable}` (used by CLI for `status` detail; `host_name` is what it shows, I-192) |
| GET | `/projects/:id/samples?window=1h\|24h\|7d` | `{window, step_s, vcpus, memory_bytes, points: [{ts, cpu, mem_used_bytes, cpu_pressure, host_cpu_wait, disk_used_bytes}], procs: [{comm, cpu_s, rss_max_bytes}]}`: the machine over the window from its minute samples while running (I-492, I-493). `window` defaults to `1h`; `step_s` is 60, 300 or 3600. `vcpus` is the cores the class sells and is fractional on `tiny` (0.5, I-1168); `cpu` is the share of the class's vCPUs used, `cpu_pressure` the share of the time a task in the guest waited for a vCPU, `host_cpu_wait` the share of the time the machine waited for a host CPU (all 0 to 1). `mem_used_bytes` is memory in use as the guest sees it. `mem_used_bytes` and `cpu_pressure` are null in a bucket whose samples came from a guest older than I-493. `procs` is the eight process names with the most CPU in the window, their CPU seconds and largest RSS. Any other `window` is `invalid` |

```
Project { id, name, slug, remote_url, class, state, host_id?, host_name?, guest_ip?,
          agent_default, hold_base_updates, base_version, config_revision_id,
          volume_bytes, disk_used_bytes?, created_at, started_at?,
          root_used_bytes?, root_size_bytes?,          -- the guest's root filesystem (I-567)
          signals?: {ssh_sessions, tmux_clients, agents: [{agent, window, state}],
                     guestd_ok},
          cost_today_cents, cost_month_cents,          -- 0 since I-289, kept one release
          running_seconds_today, running_seconds_month, -- what `repose status` shows (I-289)
          last_snapshot_at?, last_error?, host_unreachable, tz,
          idle?: {since, hourly_cents, memory_gb},     -- hourly_cents 0 since I-289, kept one release
          resting?: {since},                           -- frozen while idle, state still running (I-818)
          keep_awake,                                  -- kept out of the rest rule (I-820)
          expires_at?,                                 -- set while temporary (I-347)
          personal_opt_out,                            -- machine.nix kept off (I-490)
          multiplexer,                                 -- tmux|herdr, from the next start (I-502)
          placement }                                  -- own|hosted, where it is placed (I-1366)
                                                       -- host_name: the owned host's name beside host_id,
                                                       -- absent on a host repose runs (I-1392)

DestroyedProject { id, name, slug, class, remote_url?, volume_bytes,
          destroyed_at, name_free, restorable_until?,
          snapshot: {id, created_at, bytes, reason, expires_at?} }
```

`disk_used_bytes` is the thin volume's allocated blocks from the newest
sample (`lvs data_percent`), which keep a deleted file's blocks until the
guest's `fstrim` (daily, and at every stop since I-585); it is unchanged
and the CLI and dashboard no longer show it. Since I-585 the same figure,
kept on the projects row as `disk_held_bytes` and sampled for a stopped
project as for a running one, is what the plan's disk counts. `root_used_bytes` and `root_size_bytes` are the guest's
root filesystem from the same sample, statfs's blocks less those
available to `dev` and its blocks, in bytes: what the guest's writes run
out of, and what `repose status`, `repose ls` and the dashboard's Disk
card show (I-567). Both are absent when the newest sample has none (a
stopped project, a guest or hostd older than I-567, a guest that did not
answer); a client treats that as unknown.

`last_error` is the sentence the last failed op left (I-159), `null`
once an op succeeds; on a `running` project it may be the warning of the
start that left it running but not as asked (I-590): `boot_failed: its
new system did not boot, so it runs its previous one: ...`, which the
CLI and the dashboard show, or the code and sentence of a start's apply
that failed; on a `stopped` project it may instead be
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

`resting` (DECISIONS I-818) is present only on a `running` project whose
machine is frozen: its vCPUs are stopped and its memory is resident on
its host, so every process, socket and open file survives, and the first
thing that reaches it -- an SSH connection, a port forward, a secret, an
answer to a question -- wakes it. `since` is when the current rest began.
The state stays `running`: resting is a marker, never an eleventh state,
and a frozen machine bills and holds its plan memory exactly as a running
one does. There is no 24-hour gate like `idle`'s, because a machine rests
about fifteen minutes after it goes quiet. `idle` and `resting` say
different things and both may be set: a machine nobody has used for a day
is holding plan memory whether or not it is frozen. Absent means "not
frozen", which is what an api older than I-818 says to every client. The
word for it in the CLI, the dashboard and the docs is "frozen" (I-683);
`resting` is the name on the wire, the row and the sample. The
dashboard's look for it was built behind `FROZEN_LOOK` while only the
fake api sent the field (I-683); the flag goes with this field, which is
real.

`keep_awake` (DECISIONS I-820) is false by default and keeps a machine out
of the rest rule, for the jobs the rule gets wrong: a bot waiting on an
outbound connection it must not drop. It is set through `PATCH
/projects/:id` above.

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

`placement` (DECISIONS I-1366) is where the machine goes when it is
placed: `own`, the default, is a host this account owns when it owns any
and the hosted fleet when it owns none; `hosted` is the fleet whatever it
owns. A project is pinned to its host for life (DESIGN.md §3), so the
field is read at the create and at a re-placement (`POST
/projects/:id/start` with `create: true`, I-406) and never after, which is
why `PATCH /projects/:id` does not take it: nothing would honour it. An
account whose plan sells no memory on repose's own hosts never lands
there, whatever the field says (I-1366): a re-placement of such a project
goes to its owner's hardware. A create for `own` while the account's own
host has not registered yet, or cannot take the machine, is refused at
the request with `503 capacity` and a message that names the host and
its state, rather than quietly using the fleet, and nothing is written
(I-1390; before it the create op failed and left the row in `error`). An
api older than I-1366 has no field and places as it always did.

**The base gate.** `409 conflict` with `detail: {reason:
"base_update_needed", base_version, needs}` answers a POST or PATCH
asking for `herdr` when the base the machine would run is older than the
first base with herdr: for a PATCH the project's `base_version` (null
counts as older), for a POST the newest published base, and for a
restore as a new project the base the copy keeps, which is the source's
`base_version` (the newest published base when that is null; I-549).
"Older" compares `base_versions.released_at`; a version with no row
counts as older. The first
base with herdr is `herdrMinBase` in `internal/api/http/multiplexer.go`;
while it is empty, or names no row of `base_versions`, every request for
`herdr` is refused this way with `needs: ""`. `message` is `<slug> runs
base <base_version>; herdr needs <needs> or newer.`, `<slug> has no base
yet; herdr needs <needs> or newer.` when `base_version` is null, or
`herdr is not available yet.` when `needs` is empty. A request for `tmux` is never
gated. A restore as a new project copies the source's `multiplexer`,
and falls back to `tmux` when the gate would refuse; an in-place restore
keeps the project's.

## Config

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/config` | `{revision_id, fragment, menu?: MenuSelection, base_version, applied_at?, personal, personal_revision_id, personal_opt_out}`; `personal` is the `machine.nix` text the active revision carries (`""` for none) and `personal_revision_id` the account revision it came from (added with I-490) |
| PUT | `/projects/:id/config` | `{fragment}` or `{menu: MenuSelection}` (the api renders menu → fragment) → `{revision_id, op_id}`; build starts immediately; apply happens when the build succeeds. The revision carries the account's current `machine.nix` unless the project opted out (I-490). Open personal builds (queued by a `machine.nix` save) do not refuse it with `conflict`: one not started yet is superseded (its revision becomes `failed` with `superseded by revision <id>`), one already building is queued behind. A project that is `creating`, `building`, `stopping` (since I-710), `destroying` or `destroyed` answers `409 conflict` "<slug> is <state>" |
| GET | `/projects/:id/config/revisions` | list, newest first, with `{revision_id, created_at, status: building\|applied\|failed, error?, fragment_line?, personal, personal_opt_out, personal_revision_id?, personal_line?}`; `personal` (bool) says the revision carries `machine.nix`, `personal_line` is an error's line in it (added with I-490) |
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
| POST | `/certs/revoke` | `{serial}` or `{all: true}` → `{revoked}`, how many certificates were revoked. The connections they opened close within 30 seconds |

## Secrets

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/secrets` | `[{name, created_at, updated_at}]` (never values) |
| PUT | `/projects/:id/secrets/:name` | `{value}` (base64, max 64 KB) → pushed to a running guest via `UpdateSecrets` |
| DELETE | `/projects/:id/secrets/:name` | |
| GET | `/me/secrets` | `[{name, created_at, updated_at}]` (never values): the account's secrets (I-697) |
| PUT | `/me/secrets/:name` | `{value}` (base64, max 64 KB) → `{name, pushed, overridden_by: [slug]}`. Sets an account secret, which every project of the account gets with its own set; a project secret of the same name wins, and `overridden_by` names the live projects that have one. `pushed` is how many running machines were sent `UpdateSecrets` (each running project without a secret of its own by that name). Added with I-697 |
| DELETE | `/me/secrets/:name` | `204`; `404 not_found` when the account has none by that name. The running machines it reached are sent `UpdateSecrets`. Added with I-697 |

Names: `[A-Z][A-Z0-9_]{0,63}`. The names `ssh_host_ed25519_key`,
`ssh_host_ed25519_key-cert.pub` and `user_ca.pub` are reserved for the guest's
sshd material (delivered by hostd into the same tmpfs from the explicit
`CreateGuest` fields, see I-3 and I-10) and are rejected with `invalid`.
PUT also rejects `BASH_ENV`, `ENV` and `REPOSE_ENV_GEN` with `invalid`: the
guest uses them to keep each command's secrets current (I-475). A secret by
one of those names stored before is still listed and deletable.
Account secrets take the same names under the same rules. A machine's set
is its project's secrets plus the account's secrets the project has none
of its own for; `GET /projects/:id/secrets` lists the project's alone.

## Webhook routes (DECISIONS I-1110)

A public URL into the project's machine, set by the CLI and the
dashboard. `docs/features/webhooks.md` is the behaviour.

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/webhooks` | `[{route_id, name, port, path, allow_start, url, created_at, updated_at, last_delivery?}]`. `url` carries the token: the caller owns the project, and a list without it would make `webhooks url` a round trip per row |
| POST | `/projects/:id/webhooks` | `{name, target}` or `{name, port, path}`, with `allow_start?`. Creates (201) or, on a name that exists, changes the target and `allow_start` and keeps the token (200). `target` is `PORT` or `PORT/PATH`. `name` matches `[a-z0-9][a-z0-9-]{0,31}`; a bad name or target, or a 21st route, is `400 invalid` |
| GET | `/projects/:id/webhooks/:name` | The route, with `deliveries`: the newest 20 |
| POST | `/projects/:id/webhooks/:name/rotate` | A new token at once; the old hash becomes a tombstone for 30 days. Returns the route with the new `url` |
| DELETE | `/projects/:id/webhooks/:name` | 204; the token becomes a tombstone for 30 days. `404 not_found` for a name the project does not have |

A destroy removes the project's routes and tombstones each token, in the
destroy's own transaction, so `repose restore` does not bring a URL back
(I-1114).

The token is minted by the api and stored twice: `sha256(token)` for the
edge's lookup, and the token as ciphertext under the account's data key
with the same envelope as a named secret, so it can be printed again.

## The machine's browser view (DECISIONS I-1216)

The dashboard's view of the machine browser the agents drive. Two routes,
and both are new with I-1216, so there is no older shape to keep
accepting. `docs/features/browser.md` is the behaviour.

| Method | Path | Body / result |
|---|---|---|
| POST | `/projects/:id/browser/view` | `{}` (no body) → `{expires_at, stream_path, resting}`. Mints a ticket for one connect and sets it as a cookie: `repose_view=<43 characters of base64url>; HttpOnly; Secure; SameSite=Strict; Path=/v1/browser/stream; Max-Age=900`. **The body carries no ticket and no password**: the ticket is in the cookie so no script, extension or devtools export can read it, and the guest's VNC password never leaves the edge. `resting: true` means the machine's vCPUs are stopped and this mint did not wake it, because one was woken for it inside the last 10 minutes; the page shows the desktop-off state instead of billing a resume. 10 mints per user per hour |
| GET | `/browser/stream` | A WebSocket (RFC 6455, version 13, no extensions, no subprotocol) carrying the already-authenticated RFB stream, binary frames in both directions, 1 MiB per message. The ticket comes from the cookie; **the route carries no project id** -- the ticket names the project, and the handler resolves nothing a caller chose |

The stream closes when the machine's desktop does, after 120 s with no
frame in either direction, after 4 h, or when the ticket has carried
2 GiB. The close frame says which: status 1000 and a reason from the
closed set `closed`, `idle`, `cap`, `budget`, `too_large`, `error`. A page
that reconnects does so on `closed` or on a transport error and never
after `idle`, `cap` or `budget`, because a reconnect mints again and a
mint is where a wake can happen. The page mints again on every connect and reconnect, so expiry is
this credential's revocation: nothing has to be revoked by hand when a
machine stops, and a ticket cannot outlive its 15 minutes. The view's
bytes do not count against the project's egress allowance (they never
cross a guest's uplink, so nothing could meter them), and an open view
holds the machine awake at the running rate.

Refusals, beside the usual envelope codes:

| Code | Status | When |
|---|---|---|
| `view_expired` | 401 | No cookie, or a ticket that has expired, was never minted, or whose project or account has changed under it. The page mints another |
| `view_busy` | 409 | That project already has a view open. Xvnc is `-AlwaysShared`, so a second viewer would work and double the frame cost for nothing |
| `view_budget` | 409 | The ticket has carried its 2 GiB. The page mints another |
| `view_capacity` | 503 | This api or the edge's relay is at its concurrent-view cap |
| `unavailable` | 503 | No view relay is configured on this api, or the edge did not answer |
| `conflict` | 409 | The machine is not running (`detail.state`), has no address, or its desktop did not answer (`detail.reason` is the relay's code) |
| `rate_limited` | 429 | The 11th mint in an hour, or too many connects from one source prefix at once |
| `forbidden` | 403 | An `Origin` that is not the dashboard's, or a source prefix refused for 10 minutes after 20 refused tickets |

The mint's answer is the one place the api sets a cookie, and that is why
`Access-Control-Allow-Origin` can name an origin instead of answering `*`
(I-79): a browser drops a `Set-Cookie` from a cross-origin fetch that was
not credentialed, and refuses a credentialed answer that says `*`. The
rule is read off the request's `Origin` and not off the route, so every
route answers the dashboard's own origin by name with
`Access-Control-Allow-Credentials: true`, and every other origin gets the
wildcard as before. Only `DASHBOARD_URL`'s own origin is ever echoed.

## Triggers (DECISIONS I-1248..I-1262)

A trigger binds a webhook route or a schedule to an agent session on the
project's machine. `docs/features/triggers.md` is the behaviour.

`Trigger = {trigger_id, name, kind: route|schedule, route?: {name, url},
spec?, zone?, next_at?, prompt_path, workdir, agent?, timeout_s,
concurrency, gate?, dedupe?, notify: failed|never, enabled, allow_start,
stop_after, catch_up, created_at, updated_at, last_run?: TriggerRun}`.
`url` carries the token, as a route's does: the caller owns the project.
`prompt_path` is a path in the machine; **no answer ever carries the
prompt's text and no row ever stores it** (I-1248), and nor is a payload.
A prompt typed in a browser is sent on `prompt` and passes through: the
api holds it for one request and the hand-off that follows it, writes it
into the machine, and forgets it (I-1266).

`TriggerRun = {run_id, trigger_id?, trigger, source: route|clock|hand,
state: queued|working|done|failed|timeout|skipped, reason?, summary?,
link?: {url, label?}, window?, dedupe_key?, payload_bytes, payload_kept,
skips?, started_machine, started_at?, finished_at?, created_at}`.
`reason` is the bounded enum of `features/triggers.md` §4.1, except
`gated`, which carries a gate's own last line of stderr; that and
`summary` (1 KB) are tenant content, shown to the owner and never
logged. `payload_kept` is `payload_bytes > 0`, which is what
`fire --run` can replay: a payload lives as long as its run row.
`skips` is present and above 1 on a row that stands for several
refusals: an edge-side refusal coalesces into one row per trigger,
reason and hour (I-1254), so a machine stopped for a week cannot evict
the runs that ran. `trigger_id` is absent once the trigger is removed,
and `trigger` is the name it had. The hand-off states `pending` and
`sending` are answered as `queued`, as a `say`'s are.

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/triggers` | `[Trigger]`, by name |
| POST | `/projects/:id/triggers` | `{name, kind?, route?, spec?, zone?, prompt?, prompt_path?, workdir?, agent?, timeout_s?, concurrency?, gate?, dedupe?, notify?, enabled?, allow_start?, stop_after?, catch_up?}`. Creates (201) or, on a name that exists, changes the fields sent and keeps the rest and the route's token (200). `kind` absent with no `route` or `spec` leaves the door as it is, which is how `repose triggers on`, `off` and a flag-only `add` reach it; on a name the project does not have that is `404 not_found`. Setting `enabled` true, or changing `spec` or `zone`, re-arms the clock from now and re-anchors (I-1252): a trigger that was off for a week must not fire the moment it comes back. A bad name, spec, zone, dedupe key, workdir or agent, a `dedupe` on a schedule, or a 21st trigger, is `400 invalid` with `detail.reason`; `prompt_at` for the `@`-prompt that `pi` refuses (I-780); a trigger on a temporary machine is `409 conflict`, `detail.reason = temporary_machine` (I-347). A new trigger on a project whose multiplexer is `herdr` is `400 invalid`: a run is an agent in its own tmux window, and such a machine has no tmux session (I-501). A trigger that predates the switch can still be changed, turned off and removed. `prompt` is the prompt's TEXT, which the dashboard has no SSH to write with (I-1266): the api writes it into the machine at `prompt_path` under `workdir` and stores none of it, so the row still holds a path. It needs a machine to write into, so a project that is not `running` answers `409 conflict`, `detail.reason = machine_not_running`, in the words `repose triggers add -p` refuses the same thing with, while `prompt_path` alone is still accepted there; over 64 KB is `400 invalid`, `detail.reason = prompt_too_large`. A client that sends neither is the shape before I-1266 and is unchanged |
| GET | `/projects/:id/triggers/:name` | The trigger, with `runs`: the newest 20 |
| DELETE | `/projects/:id/triggers/:name` | 204. A route the trigger made goes with it unless another trigger uses it or the user made it first; the token is tombstoned as I-1114 tombstones every route. `404 not_found` for a name the project does not have |
| POST | `/projects/:id/triggers/:name/fire` | `{payload_run?: run_id, dry_run?: bool, payload_path?}` → `TriggerRun` in `queued`, `source: hand`. The row is the hand-off and carries all three, because only the grpc process can send the `RunTrigger` and anything held in the asking process's memory would be lost between them (I-1162). `payload_path` is the file `repose triggers fire --payload FILE` wrote over SSH and must be `~/.repose/triggers/<name>/fire-payload`, else `400 invalid`; the bytes never come through the api. `payload_run` naming a run whose payload is gone is `400 invalid`, `detail.reason = payload_gone`. A fire runs a trigger that is `off`: `off` shuts the doors, and a fire is the owner asking |
| GET | `/trigger-runs?limit=` | `[TriggerRun]` across the user's projects, newest first, `limit` 1..200 (default 20), each row with `project` (the slug). "What did my agents do last night" is the question the layer exists for, and a per-project listing cannot answer it; `repose triggers runs --all` and the dashboard read this, as they read the cross-project listings of questions and snapshots |
| GET | `/projects/:id/trigger-runs?trigger=&limit=` | `[TriggerRun]`, newest first, `limit` 1..200 (default 20). 20 runs are kept per trigger (I-1262) |
| GET | `/projects/:id/trigger-runs/:run_id?wait=20s` | The run. With `wait`, the long poll of the "Projects" section: it answers when the run reaches a terminal state or the wait runs out, and `Repose-Long-Poll: 1` marks a held read |

The clock runs in the `API_MODE=grpc` process alone, once a minute,
under `db.LockTriggers`, and claims a due trigger by writing its next
fire in the claiming statement, so a fire happens once across replicas
and once across a restart (I-1252).

## Snapshots

| Method | Path | Body / result |
|---|---|---|
| GET | `/snapshots?limit=&before=` | `[{id, project_id, project, project_destroyed, created_at, bytes, reason, expires_at}]` across the user's projects, newest `taken_at` first, at most 200 (default 100); `before` is a snapshot id and pages by keyset on `(taken_at, id)`. Destroyed projects' snapshots are included, with `project_destroyed: true`: they are kept 30 days and are the set a home folder could not otherwise reach. Added with I-904; an older api answers `404` and the CLI falls back to naming a project |
| GET | `/projects/:id/snapshots` | `[{id, created_at, bytes, reason, expires_at}]`; `reason` is `scheduled`, `stop` or `manual`. `fork` (I-703) is no longer written: `repose fork` is gone (I-1082), and a migration narrows the column once no `fork` row is live |
| POST | `/projects/:id/snapshots` | `{reason?: "fork"}` (body optional) → `{op_id}`; the op, once `done`, carries `result: {snapshot_id}` (the api has always set it; documented with I-254). No body is a manual snapshot. `reason: "fork"` (I-703) is accepted for one release after I-1082 and is then refused as `invalid`: only an old CLI's fork fallback ever sent it, and nothing reaches that fallback now |
| POST | `/projects/:id/snapshots/:sid/restore` | `{as_new_project?: name, start?: bool=true}` → `{op_id, project_id}`; without `as_new_project`, replaces the stopped project's volume. `:id` may be a destroyed project. `POST /projects/restore` is the same restore resolved by name |
| POST | `/projects/:id/fork` | **Removed** (I-1082, superseding I-254 and I-701). Every body answers `400 invalid` with `detail: {reason: "fork_removed"}` and the message `repose fork was removed; ``repose run --worktree -p PROMPT`` runs another agent on its own branch on the same machine`, which an old CLI prints after its own `Could not fork X:`. The route itself goes one release after v0.1.45 |

## Events and logs

| Method | Path | Body / result |
|---|---|---|
| GET | `/projects/:id/events?since=&before=&after=&kind=&limit=` | `[{id, ts, kind, agent?, window?, summary, link?: {url, label?}, source, delivered}]` (`link` is an agent's `--link`, I-931), newest first, where `delivered` is `{<channel>: <RFC 3339 send time> \| "held" \| "dropped: resolved" \| "error: <why>" \| "failed: <why>"}`, empty when nothing was queued (`held` and `dropped: resolved` since I-689; `<why>` is a few words such as `the server answered 403`, never a URL, since I-691), `limit` (1..200, default 50) of them. `before=<event id>` (DECISIONS I-414) gives the events older than that one, ordered by `(ts, id)`, and ignores `since`; an id that is not one of the project's events gives `[]`. `after=<event id>` (I-662) gives the `limit` events newer than that one and nearest to it, still newest first, ordered by `(ts, id)`, and ignores `since`; an id that is not one of the project's events gives `[]`. `kind=<k>[,<k>...]` (I-662) keeps only those kinds; each must match `^[a-z_.]{1,64}$` (dotted kinds such as `snapshot.created` since I-682). Every answer carries the header `Repose-Event-Count: <n>`, the number of the project's events that match `kind`, whatever `since`, `before`, `after` and `limit` say; the api lists it in `Access-Control-Expose-Headers`. A bad `limit`, `before`, `after` or `kind`, or `before` and `after` together, is `400 invalid`, and so is a `since` that is not RFC 3339 (I-609; before, the api read it as no limit). A client pages back by passing the last id of each page until a page is shorter than `limit`, and forward by passing the first id as `after`; an api older than I-414 ignores `before` and `limit` and answers with its newest 50, and one older than I-662 sends no count header and ignores `after` and `kind` |
| GET | `/projects/:id/logs?since=&kind=console\|build\|ops` | last 10k lines, JSON lines. Every line has `ts` and `kind`; a `build` line also has `op_id`, `seq` and `line` (`ts` and `kind` on build lines since I-322; a client must accept a build line without them, which an older api sends). `since` (RFC 3339, fractional seconds allowed; any other value is `400 invalid` since I-609) keeps the lines after it: for `build`, lines that reached the api after it (since I-322; before, `since` was ignored for `build`), for `ops`, ops created at or after it, for `console`, lines of ops finished after it. A `console` line (I-592) is one line of what a boot that never reached Ready printed, from the project's last 20 ops, oldest first, each with its op's finish time, `op_id` and `kind: "console"`; an op from before I-592 gives its error's sentence as one line with the op's kind, as every console line was before. A machine that booted cleanly has none. An `ops` line is one op, oldest first: `{ts (its creation), op_id, kind (the op's kind), state, finished_at, duration_ms?, error?: {code, message}}`, with no `line` |

Event kinds are those of `features/notifications.md`; `agent_message`
(from `repose-notify`) and `agent_question` (from `repose-ask`, whose
`summary` is the question text) were added by DECISIONS I-244, and
`agent_done` (from `repose-notify --done`, the agent's report in
`summary`) by I-688, which also stopped `completed` from notifying; it is
still listed here. A client that does not know a kind shows it by name. `idle_running` (source
`api`, once per idle stretch; the summary names the class, the hourly
rate and `repose stop <slug>`) was added by DECISIONS I-262.
`temp_expiring` (source `api`, once per temporary project an hour before
its `expires_at`, only when it was made with more than an hour; the
summary names the time left and `repose keep <slug>`) and
`temp_destroyed` (source `api`, when a destroy the api started at the
expiry finishes) were added by DECISIONS I-347. `host_restarted` (source
`api`, notifying nobody; the summary says when the server was back) is
written on each machine a host restart stopped, and
`machines_restarted` is the user's one notice per host restart, an event
with no project that `GET /projects/:id/events` never lists: its summary
is the notice's text, a line per machine (DECISIONS I-645).

`POST /projects/:id/start` on a project whose host is `unreachable`
answers `409 conflict` with `detail: {host_state: "unreachable"}` and the
message ``<slug>'s server is not answering; machines that were running on
it start again by themselves when it is back, and `repose start <slug>`
works then`` (I-645; before, it told the user to restore a snapshot onto
another host, which `retired` and `lost` still say).

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

## Messages for an agent (DECISIONS I-1144)

A message is what the owner typed with `repose say`: `Say = {id,
project_id, window, text, sender, state:
delivered|waiting|replaced|failed, replaced?: bool, created_at}`. `text`
is tenant content (at most 4 KB): shown to the owner, typed into the
agent's terminal, never logged. `window` is the agent window the machine
resolved the message to, as `repose ps` names it. `sender` is the
account's handle, which the machine puts in front of the message only
when the sender is not the project's owner; today it always is.

The api does not queue: it sends a `SayToAgent` down the host's channel
and answers with what the machine said. `delivered` means the text is in
the agent's terminal now; `waiting` means the agent was mid-turn and the
machine types it in at the end of the turn. One message waits per
window, so a second while one waits answers `replaced: true` and the
earlier row moves to `replaced`. A `waiting` row becomes `delivered` on
the machine's `agent_prompted` event, which is the only thing that knows
a mid-turn message went in at the end of the turn.

| Method | Path | Body / result |
|---|---|---|
| POST | `/projects/:id/say` | `{text, agent?}` → `Say`. `agent` names the window (`claude`, `codex-2`, `t-apply`); without it the machine uses its one agent window, whatever it is named, and leaves a trigger's run out of that pick while the user has an agent of their own (I-1356). `400 invalid` for an empty message, for one over 4 KB (`detail.reason = text_too_long`) and when several agents run and none was named (`detail.reason = several_agents`; the message names the windows). `409 conflict` when the machine is not running (`detail.reason = not_running`), when no agent is running there (`detail.reason = no_agent`), and when the machine did not take the message within 20 seconds or its host is not connected (`detail.reason = machine_unreachable`). A delivery the machine made records an `agent_prompted` event, which notifies nobody and carries the sender and the window, never the message |

## Questions (DECISIONS I-244, I-245)

A question is what an agent asked with `repose-ask` in the guest:
`Question = {id, project_id, project (slug), agent, window, text, options:
[string] (0 to 3), state: pending|answered|cancelled|expired|no_channel,
answer: string|null, answered_via: dashboard|cli|ntfy|email|gchat|terminal|agent|null,
created_at, expires_at, answered_at: time|null}`. `text` and `answer` are
tenant content (at most 1 KB each): shown to the owner, never logged.
Since I-690 a guest's close carries a reason: `terminal` (the owner
answered the agent in its terminal) is stored as `state: answered`,
`answer: null`, `answered_via: terminal`; `withdrawn` (the agent took it
back with `repose-ask --cancel`) as `state: cancelled`, `answered_via:
agent`; any other reason, or none from a hostd older than I-690, as a
plain `cancelled` (or `expired`) with `answered_via: null`. A client
shows them as "answered in the terminal" and "withdrawn by the agent",
the words a `conflict` message and the reply page use. `gchat` is an
answer from a Google Chat card's reply link (I-692).
Since I-931 a question may carry `link: {url, label?}`, the ask's
`--link`, absent otherwise. Since I-929 a question can ask for a named secret: `secret: NAME` is then
set (absent otherwise), `options` is empty, and the answer is the
secret's value, sent as `value` and stored as the project's secret NAME
exactly as `PUT /projects/:id/secrets/:name` stores it. Such a question
is answered with `state: answered`, `answer: null`, never with the value.

| Method | Path | Body / result |
|---|---|---|
| GET | `/questions?state=pending\|all` | `{questions: [Question]}` across the user's projects, newest first, at most 50; `pending` (the default) lists only questions still waiting |
| GET | `/projects/:id/questions?state=pending` | `{questions: [Question]}`, pending first, then the latest, at most 20 |
| POST | `/projects/:id/questions/:qid/answer` | `{answer, via?: cli\|dashboard}` → `Question`. With options, `answer` must equal one of them (case does not matter; the stored answer uses the option's spelling), else `invalid` with `detail.options`. A question no longer pending (answered, expired, cancelled) is `conflict` with `detail.question`: the first answer wins. A question with `secret` takes `{value, via?}` instead (I-929): `value` is base64, 1 byte to 64 KB, stored as the project's secret, audited as `secret_put` with the question's id, and pushed to a running machine; the question closes when that push has finished. `answer` for such a question, or `value` for one without `secret`, is `invalid` (`detail.secret` names the secret). Two answers at once store one value: the second is `conflict` at once and stores nothing. Other pending questions of the project that ask for the same name close with it |
| POST | `/projects/:id/questions/:qid/cancel` | `{via?: cli\|dashboard}` or no body → `Question` in state `cancelled`, `answered_via` the `via` sent (`dashboard` when there is no body: the dashboard's Dismiss sends none, `repose reply --dismiss` sends `cli`, I-692); the waiting `repose-ask` exits 5. `conflict` when it is no longer pending, with the `Describe` message (`answered in the terminal`, `withdrawn by the agent`, `already answered: yes`, ...) |
| POST | `/projects/:id/questions/:qid/hold` | no body → `Question` (DECISIONS I-1242). The owner is answering on the dashboard: `expires_at` becomes now + 5 minutes when that is later than it is, never past `created_at` + 24 h + 5 min. A hold that would move it by less than 30 s is a no-op that still answers `200` with the question, so a page holding every 60 s keeps it alive and an abandoned tab stops holding within 5 minutes. A hold that took is carried to the guest at once as `AnswerQuestion` status `extended` with `expires_unix`, best effort: a failed delivery is logged by reason and the next hold carries the newer deadline. A question no longer pending is `conflict` with `detail.question`, as `answer` is |
| GET | `/questions/reply?token=&via=` | no auth; the one-click link an email, ntfy or Google Chat notification carries for one option; `via` is `email`, `ntfy` or `gchat` (anything else is recorded as `ntfy`). The token is signed (the unsubscribe key, a separate domain) and names the question, the option and the question's expiry. Answers an HTML page showing the question and a button that POSTs; a GET never answers, because mail scanners open links. Expired → `410`, invalid → `400`, both HTML |
| POST | `/questions/reply?token=&via=` | no auth; answers with the token's option (`token` and `via` may also be form fields). ntfy's `http` button calls it. `200` HTML on success; a question already answered or closed → `409` naming the answer it has; 20 tries per question per minute, then `429` |

## Usage and billing

Plans, seats and the waitlist are DECISIONS I-289 and I-290; the numbers are
`docs/PRICING.md`. `billing_disabled` (503) is every route below when the
api has no `POLAR_ACCESS_TOKEN`. Billing runs on Polar since I-604.

| Method | Path | Body / result |
|---|---|---|
| GET | `/usage?from=&to=` | per project per day: `{guest_hours: {tiny,small,large,xl}, gb_months, egress_gb, cost_cents, credit_cents}` (`tiny` is new with I-1168; a client from before it reads the key it does not know as absent, and the other three keys keep their names and meaning) (`cost_cents` is the egress overage share and `credit_cents` is 0 from `price_version = plan-v1`; both stay for one release) |
| GET | `/billing` | `{subscription: {plan: solo\|plus\|pro\|own, status: trialing\|active\|past_due\|paused\|canceled, seats, period_start, period_end, next_billed_at, trial_end, cancel_at, scheduled_plan, next_charge_cents\|null, intro_until\|null}\|null, usage: {running_gb, memory_gb, disk_held_gb, disk_allocated_gb, disk_gb, egress_gb, egress_included_gb, overage_cents, projects, project_limit}, plans: [{id: solo\|plus\|pro\|own, name, price_cents, currency: "USD", trial_days, seats, memory_gb, disk_gb, egress_gb, hosts, project_limit, available: bool, intro_price_cents, intro_months, intro_egress_gb}], intro_eligible: bool, seats: {total, held, free, waiting}, waitlist: {position, joined_at, invited_at, hold_until}\|null}`. The `paddle` object (`{environment, client_token}`) is gone since I-604 with no release of overlap: it named Paddle objects that no longer exist, the dashboard that read it ships in the same deploy, and the CLI never read it. Since I-604 `status` uses Polar's words: Polar's `unpaid` is reported as `canceled`, and `paused` stays in the enum though repose offers no pause. `usage` is for the current period (or the last 30 days without a subscription); `plans[].available` is whether that plan's seats are free for this user now. The route writes nothing: reading it never joins the waitlist and never ends a lapse (I-1033). `waitlist` is null for a billing-exempt account, which holds no place in the queue (I-1090). Added by I-497, all additive: `intro_price_cents`, `intro_months` and `intro_egress_gb` are a plan's introductory price and egress allowance (0 for none); while a subscription's offer runs, `usage.egress_included_gb`, the overage and `/me`'s `limits.egress_gb` use the introductory allowance; `intro_eligible` is whether this user's checkout of such a plan gets it (a first subscription, with the discount configured); `next_charge_cents` is what Polar charges at `next_billed_at`; `intro_until` is when the introductory price ends (since I-604 the trial's end, or the start, plus `intro_months`), null when there is none. Since I-672 the introductory price is Solo's founding $39 for 12 months: `intro_egress_gb` is 0, and `intro_eligible` also needs a founding place left. Since I-569 a plan sells no project count: `usage.project_limit` and `plans[].project_limit` are 100 on every plan, kept one release, and `/me`'s `limits.projects` is the account's cap. Added by I-1364, additive: `plans[].hosts` is how many hosts of their own the plan allows (1 on Own hardware, 0 on the hosted plans), and `plans` lists only the plans this deployment sells — a plan with no Polar product id configured (`POLAR_PRODUCT_OWN`) is absent, while a subscription that already exists on it is still reported in `subscription`. An own-hardware subscription's `usage.memory_gb`, `usage.disk_gb` and `usage.egress_included_gb` are 0, which means the plan caps none of them (the host's own memory and disk do) rather than that it allows nothing, and `usage.overage_cents` is 0 because the plan meters no egress. Since I-585 the plan's disk counts what the projects' volumes hold, not their sizes: `usage.disk_held_gb` is that, in GB rounded up to a tenth (each live project's `disk_held_bytes`, or its `volume_bytes` while it has none), and `usage.disk_allocated_gb`, kept one release, carries the same figure rounded up to a whole GB. Since I-1396, with no subscription (an account with no plan, or a billing-exempt one) `usage.memory_gb`, `usage.disk_gb` and `usage.egress_included_gb` are 0 and `usage.overage_cents` is 0, as on an own-hardware subscription: no plan caps the account, and a client measures it against nothing. Until then they were Solo's figures, and the dashboard measured a billing-exempt account against them |
| POST | `/billing/checkout` | `{plan}` → `{url}`; the dashboard sends the browser to that Polar hosted checkout. The api creates a Polar checkout session for the plan's product (fixed price, metered overage price, seven-day trial) with `external_customer_id` = the user id, the account's email, the introductory discount when `intro_eligible`, `metadata.user_id` and the browser's address for the tax country; Polar returns the browser to `/billing?checkout=done`. Until I-604 the answer was `{transaction_id, client_token, environment}` for Paddle.js; that shape is not kept a release (the reasons are under `GET /billing`). `503 waitlisted` with `{position, joined_at, email}` when the plan's seats are not free (the user is on the waitlist from then on; an invited user's hold counts toward their own checkout); `409 conflict` `detail.reason = subscribed` when a subscription exists (change it with `/billing/plan`); `409 conflict` `detail.reason = exempt` for a billing-exempt account, which has no plan to choose (I-1090); `400 invalid` for an unknown plan, and for a plan this deployment does not sell, which to a client is the same thing: the message lists the plans that are for sale (`plan must be solo, plus and pro`, I-1364) |
| POST | `/billing/waitlist` | no body → `{position, joined_at}`; joins the waitlist without trying a checkout, idempotent (a second call answers the same place). `409 conflict` `detail.reason = exempt` for a billing-exempt account, which has no place to wait for (I-1090) |
| POST | `/billing/plan` | `{plan}` → `{plan, scheduled_plan, effective_at}`. A move to a plan with more seats (Solo to Plus or Pro, Plus to Pro) takes effect at once (Polar charges the prorated difference now, `proration_behavior = invoice`; I-604) and needs the extra seats free, else `409 conflict` `detail.reason = no_seat` (a subscriber is never put on the waitlist). A move to fewer seats is scheduled for `period_end` (Polar `next_period`, held as `pending_update`; choosing the current plan again clears it) and refused with `409 conflict` `detail.reason = plan_kind` when the move crosses between the hosted plans and the Own hardware plan, which cannot be changed between at all (I-1364: cancel and choose the other one when it ends), and with `409 conflict` `detail.reason = over_plan` and `detail: {running_gb, disk_held_gb, disk_allocated_gb}` while the running memory or the disk the projects hold would not fit (`disk_held_gb` added and `disk_allocated_gb` set to the same figure, rounded up, by I-585; the latter is kept one release) |
| POST | `/billing/cancel` | no body → `{cancel_at}`: the subscription ends at `period_end` (during the trial, at `trial_end`); machines run until then. `409 conflict` `detail.reason = already_cancelled` |
| POST | `/billing/resume` | no body → `{plan, period_end}`: undoes a scheduled cancellation before it takes effect. `409 conflict` `detail.reason = not_cancelled` |
| POST | `/billing/portal` | no body → `{url}` of a Polar customer session in Polar's customer portal (card, orders, billing address, cancellation at period end); `{"for": "payment_method"}` is accepted and answers the same portal, since Polar has no deep link (I-604). `409 conflict` `detail.reason = no_subscription` for a user who never subscribed |
| GET | `/billing/invoices` | from Polar's orders for the customer (I-604), newest first, up to 24: `[{id, number, status, currency, amount_cents, subtotal_cents, tax_cents, created_at, period_start, period_end, hosted_url, pdf_url}]`. `status` is `paid`, `pending`, `refunded` or `partially_refunded` (draft and void orders are left out); `subtotal_cents` is the net after the discount and before tax; `pdf_url` is Polar's invoice PDF and `hosted_url` the same link, both null until Polar has generated the invoice, which the listing asks Polar to do |
| POST | `/billing/webhook` | Polar's webhook endpoint (I-604). No bearer token: the Standard Webhooks headers `webhook-id`, `webhook-timestamp` and `webhook-signature` (`v1,<base64>`, several space-separated allowed) are the authentication, an HMAC-SHA256 of `id.timestamp.body` keyed by `POLAR_WEBHOOK_SECRET` (the base64 after `whsec_`, or the whole string for Polar's older secrets and `polar listen`'s; both are tried), refused when the timestamp is more than five minutes off. Handles `subscription.created|updated|active|canceled|uncanceled|revoked|past_due` and `order.paid`; idempotent on `webhook-id` (the `billing_events` primary key); a duplicate answers `200 {received, duplicate}`; an event about a customer or product repose does not know answers `200 {received, ignored}` and keeps its reason in `billing_events.error`, so Polar neither retries it nor counts it toward disabling the endpoint (I-678); a bad signature `400 invalid` with the event type logged and nothing else. Until I-604 it took Paddle's `Paddle-Signature` header; that shape is not accepted any more, since no Paddle destination points here |
| GET | `/public/seats` | no auth → `{total, free, waiting, founding_left}`; the landing page's count. Cached for a minute. `founding_left` (added by I-672) is how many places Solo's founding price has left, `null` when there is no offer |

`payment_required` (402) is every compute gate (`POST /projects` with a
start, `/projects/:id/start`, restore, resize while running, growing
a volume) and carries `detail.reason`:

| `detail.reason` | When | `detail` also carries |
|---|---|---|
| `subscription_required` | no subscription in `trialing\|active\|past_due` (a new account, an ended one) | `waitlist: {position, joined_at}\|null` |
| `plan_limit` | the running memory plus this machine's class would pass the plan's memory | `plan, limit_gb, used_gb, projects: [slug]` (the machines using it) |
| `disk_limit` | since I-585: what the projects hold plus about what this adds (a new project's first 1 GB; a restore's source figure; nothing for growing a disk) would pass the plan's disk, or the disk asked for is larger than the plan's whole disk. A start is never refused for disk | `plan, limit_gb`, and either `used_gb` (held, rounded up to a GB), `held_gb`, `need_gb` (to a tenth), or `volume_gb` for a disk past the plan |
| `egress_limit` | this period's egress passed four times the allowance; machines are stopped until `period_end` | `plan, limit_gb, used_gb, until` |
| `hosted_plan_required` | since I-1366: `POST /projects` asked for `placement: "hosted"` and the plan sells no memory on repose's own hosts (the Own hardware plan) | `plan` |
| `past_due` | the last payment failed (day 0 to 3) | |
| `suspended` | three days past due, or an operator suspension | |

`message` is the whole sentence in every case, so an older CLI that prints
it is right. A `suspended` account may only call `GET /me`, `GET /billing`
and `POST /billing/portal`; every other route answers `402
payment_required` with `detail.reason: suspended` and the same sentence
(I-600; an older api answers `403 forbidden` "account suspended").

## Internal (gateway)

| Method | Path | Body / result |
|---|---|---|
| GET | `/internal/route?login=<slug>.<handle>` | `{project_id, login, guest_ip, state, principals, host_unreachable, resting, host_owner_id, user_id}`. `host_owner_id` (DECISIONS I-1368) is the account that owns the project's host, `null` for a host repose runs, and `user_id` is the project's owner; the gateway routes a connection to an owned host only for that account, checked against the certificate's `key_id` on a login and against these two fields on a webhook delivery, and refuses the rest with `foreign_host`. Both are absent from an api older than I-1368, which a gateway reads as a host repose runs. `?project_id=<uuid>` in place of `login=` is the same answer for a project named by id, which is what a webhook delivery reads (I-1110): it knows the project the URL names and no login. `login` is `<slug>.<handle>`, the principal the guest's host certificate carries, which the dial still has to check; it is new with I-1110 and absent from an older api. `host_unreachable` has always been sent and is documented here since I-818; `resting` (I-818) says the machine is frozen, so the gateway asks for a wake below before it dials. The state is still `running` and nothing in the gateway refuses on `resting`: the gateway caches this answer for five seconds, which makes the flag stale in both directions, which is why the wake is idempotent |
| POST | `/internal/wake` | `{project_id, reason?}` → `{was_resting, wake_ms}` (DECISIONS I-818): resume a frozen machine, answering once it is awake. Held: the gateway waits on it inside its dial, and the api answers within 8 seconds either way. Idempotent, and `was_resting: false` means the machine was already awake, which is what makes the cached flag above harmless. `reason` is a lower_snake word of at most 32 letters for the host's log line (`connect`); anything else is ignored. A machine that is not running, has no guest, or sits on a host whose Hello did not name the `pause` feature is answered `was_resting: false` with nothing sent. A machine that did not wake answers `internal` with `<slug> did not wake`: a failed wake is a failure, and the one thing about a pause a user is told |
| GET | `/internal/revoked?since=` | `[serial]` |
| GET | `/internal/ca` | `{user_ca_pub, host_ca_pub}` |
| POST | `/internal/sessions` | `{project_id, event: opened\|closed, cert_serial, session_id}` (gateway reports, feeds signals; `session_id` names the relay, up to 64 of `[A-Za-z0-9-]`, so two connections under one certificate are two sessions; absent from a gateway older than I-176, accepted for one release as one session per certificate) → `{open}` |
| GET | `/internal/hosts` | `[{host_id, name, wg_pubkey, wg_ip, guest_cidr, state}]` for the edge's WireGuard peer sync: every host with a key, including one that has not registered while its join token is live (DECISIONS I-1380), and never an unregistered one whose token expired or that was removed |
| POST | `/internal/gateway-certs` | `{public_key, project_id}` → `{certificate}`: 5-minute user certificate for the gateway's own key, principal = project id, key_id suffixed `:via-gateway` |
| GET | `/internal/webhook-routes?since=<version>` | `{version, routes: [{route_id, project_id, name, port, path, allow_start, token_sha256, trigger?, trigger_id?}], tombstones: [sha256], unchanged}` (DECISIONS I-1110; `trigger` and `trigger_id` since I-1250, set when the route is a trigger's, so the edge knows a refused delivery has a run row to write): the table the edge holds in memory, with the hex sha256 of each token and never a token. `since` the caller's version answers `{version, unchanged: true}` and no rows, which is what makes the edge's 5 s poll cheap. `tombstones` are the hashes of tokens rotated or removed inside the 30-day window, which the edge answers 410 for |
| POST | `/internal/webhook-start` | `{project_id, route_id}` → `{started, reason}` (I-1114): start a stopped machine for a delivery on a route with `--start`, at most once per project per 10 minutes. The owner's own start path, so plan memory and an abuse hold refuse it as they would `repose start`; `started: false` with `reason` `too_soon`, `not_stopped` or `start_refused` is not an error. The edge answers 503 either way |
| POST | `/internal/webhook-deliveries` | `{deliveries: [{route_id, at, status, outcome, duration_ms, bytes_in, bytes_out}]}` → 204 (I-1110): the edge's batch, every 10 s. `outcome` must be one of `delivered`, `woke`, `stopped`, `started`, `start_refused`, `wake_failed`, `no_listener`, `timeout`, `too_large`, `rate_limited`, `body_dropped`, `unavailable`, `not_found`, `gone`; any other word is `400 invalid`, so nothing a sender chose reaches a row. For a route a trigger owns (I-1250), every outcome but `delivered`, `woke`, `started` and `body_dropped` also writes a `skipped` trigger run, with the reason `features/triggers.md` §4.1 maps that outcome to (`stopped`, `busy`, `error`, `route_gone`, `payload_too_large`, `too_many_fires`) and coalesced into one row per trigger, reason and hour. That is the only way a user sees a trigger whose machine has been stopped for a week, and the coalescing is why six hundred refusals do not evict the twenty runs that ran. A route removed while its batch was in flight is skipped, not an error. The api keeps the newest 20 per route, and banks `bytes_out` in `webhook_egress` by the hour, where the prune cannot reach it (I-1120) |
| POST | `/internal/events` | `{source_ip, agent, kind, summary}`: hook events that reached the edge over HTTP because guestd was unavailable; the api maps `source_ip` to a project and dedupes on `(project_id, agent, kind, ts to the second)`. `kind` is one a guest may report (`completed`, `needs_input`, `error`, `agent_message`, `agent_done` since I-688); any other is `400 invalid` (DECISIONS I-441). An `agent` that is not one guestd sends (`claude`, `opencode`, `codex`, `gemini`, `pi`, `shell`) is stored as no agent |

## Rate limits

Per user: 600 GET requests/min, 60/min for every other method, 10/min on
`POST /certs`, 5/min on `PUT /config`, 10 an **hour** on
`POST /projects/:id/browser/view` (I-187: a waiting CLI polls twice a
second and shares the budget with the user's dashboard; the view's mint is
where a wake can be asked for, so it is counted by the hour, I-1216). A refusal is
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
