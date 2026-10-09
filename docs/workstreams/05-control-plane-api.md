# 05. control-plane-api

## 1. Goal

`api` is the one stateful authority: who exists, which project lives on which
host, what a guest should look like, who may SSH where, what each hour cost.
Everything else (CLI, dashboard, gateway, hostd) is a client of it. It runs
as a stateless container on Coolify with Postgres beside it.

## 2. Scope: builds

- `cmd/api/main.go`, `cmd/repose-admin/main.go`, and `internal/api/`
  (`http`, `auth`, `grpc`, `scheduler`, `ca`, `secrets`, `config`, `ops`,
  `snapshots`, `events`, `notify`, `meter`, `billinghooks`, `internalroutes`,
  `ratelimit`), plus `internal/db/` (sqlc or pgx queries, migrations) and
  `internal/otel/`.
- Every HTTP route in `interfaces/api.md`, with request validation, error
  envelope, `X-Request-Id`, and rate limits.
- Logto JWT verification (JWKS cache, `aud` = the API resource, `iss` =
  Logto issuer), first-sign-in user creation with handle derivation.
- The gRPC server side of `interfaces/grpc-hostd.md`: `Register`, `Rotate`,
  `Session`, mTLS with the platform's host CA, host table maintenance,
  heartbeat tracking, command dispatch with persistence, result handling,
  `Samples` ingest, `Event` ingest, `BuildLog` persistence and fan-out.
- The scheduler (placement function and capacity accounting).
- SSH CA: user certificates, host certificates for gateway and guests,
  revocation list, `/internal/ca`.
- Secrets: envelope encryption with per-user DEKs wrapped by Azure Key Vault.
- Config revisions: fragment storage, menu rendering to a fragment, `Build`
  orchestration, SSE log streaming, `ApplyConfig` after a successful build,
  base version bumps with the hold flag.
- Ops: every long operation is an `ops` row with a state the CLI polls.
- Snapshots: scheduling is the host's; the api records, lists, restores,
  expires (30-day rule) and deletes blobs.
- Events and the notification outbox (delivery is workstream 13; the api
  owns the row and the outbox worker calls 13's senders).
- Metering: `meter_samples` ingest, hourly rollup into `usage_hours`
  (hours, disk, egress; no price since I-289: the plan table, the Polar
  wiring (I-604), the gate and the overage line belong to 09; the api owns the
  rollup job and calls 09's package).
- `repose-admin`: `hosts add` (mints a join token), `hosts list|drain|
  retire`, `users suspend|unsuspend|limits`, `projects exec` (audited),
  `db migrate|status`, `ca rotate`, `base release <version>`.
- Dockerfile (distroless, non-root), `/healthz` (DB ping plus migration
  state), `/readyz`, graceful shutdown that drains the gRPC streams.
- OpenTelemetry: traces and metrics via OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT`
  is set, no-op otherwise; Prometheus `/metrics` always.

### Billing-exempt accounts (DECISIONS I-16)

`repose-admin users exempt <handle>` sets `billing_status=exempt`. Exempt
users pass the plan gate and still accrue `usage_hours`. With no
`POLAR_ACCESS_TOKEN` the api starts normally, billing routes answer
`503 billing_disabled` and every other account is refused compute with
`subscription_required` (I-289).

## 3. Scope: does not build

- The plan table, the Polar client, the webhook, the gate and the overage
  line: workstream 09. The api exposes `/billing/*` by calling 09's package.
- Email and ntfy senders: workstream 13. The api runs the outbox loop.
- The dashboard: workstream 08.
- The gateway's relay: workstream 06. The api serves `/internal/*`.
- The Nix evaluation policy and menu catalog contents: workstream 12
  supplies the catalog file and the menu-to-fragment renderer as a Go
  package (`internal/menu`, DECISIONS I-44); the api calls it.
- Host provisioning, Coolify configuration, Key Vault creation: workstream
  11. The api needs a Key Vault URL and a managed identity or client
  credentials in env.

## 4. Interfaces owned / consumed

Owned: `interfaces/api.md`, `interfaces/db-schema.md` (except the usage and
invoices tables, shared with 09), the CA side of `interfaces/ssh-gateway.md`,
the server side of `interfaces/grpc-hostd.md`.

Consumed: `interfaces/grpc-hostd.md` message shapes (03), `internal/menu`
(12), `internal/billing` (09), `internal/notify` senders (13).

Fake it provides: `internal/fakes/api` for the CLI, dashboard and gateway.
It consumes `internal/fakes/hostd` in its own tests.

## 5. Design detail

### 5.1 Process layout

```
main
 ├─ db.Connect, db.Migrate (when --migrate)
 ├─ http.Server :8080         user routes, /healthz, /readyz (never /metrics: I-136)
 ├─ http.Server :9103         /metrics alone (API_METRICS_LISTEN; /internal is on the grpc app, I-42)
 ├─ grpc.Server :8443         hosts (mTLS), same process
 ├─ hostmgr                    one goroutine per connected host stream
 ├─ opsworker                  drives ops rows through hostd commands
 ├─ outbox                     notifications, snapshot expiry
 ├─ rollup                     hourly meter rollup (leader-elected via pg advisory lock)
 └─ certcleanup                expired certificates and revocations pruning
```

Coolify runs 1 replica at first; all background loops take a Postgres
advisory lock (`pg_try_advisory_lock(<loop id>)`) so a second replica does
not double-run them. The ops loop polls every 500 ms and also `LISTEN`s on
`repose_ops`; an enqueue or a command result in a process that does not
hold the ops lock raises `NOTIFY repose_ops`, so the driver starts the next
phase at once instead of at its next poll (DECISIONS I-163). HTTP and gRPC are replica-safe. hostd streams
reconnect to whichever replica they land on; commands for a host are routed
by looking up which replica holds its stream in a `host_sessions` table
(`host_id, replica_id, since`) and, if another replica holds it, forwarded
over an internal gRPC hop. In the first release with one replica, the
lookup always resolves locally, but the table exists so the second replica
is a config change.

Two ports because Coolify's proxy only handles HTTP on 443; the gRPC port
is exposed with a Coolify port mapping on 8443 (a mapping on the api app
does cost rolling deploys; the decision recorded as I-2 is to run the gRPC
listener in a **separate Coolify app** `api-grpc` from the same image with
`--mode grpc`, so the HTTP app keeps rolling deploys and the gRPC app
restarts with a few seconds of stream loss that hostd's reconnect absorbs).

### 5.2 Authentication and users

`internal/api/auth`: middleware fetches Logto's JWKS from
`<issuer>/oidc/jwks`, caches 1 hour, verifies RS256 or ES384, checks `aud`
equals `https://api.repose.herakraft.co` and `exp`. The `sub` claim is the
Logto user id. On first request for an unknown `sub`, the middleware calls
Logto's Management API (`GET /api/users/<sub>`) to read email and the
GitHub identity, derives `handle` from the GitHub login (lowercase, `[^a-z0-9-]`
replaced by `-`, collapsed, trimmed, max 32; on collision append `-2`,
`-3`), and inserts the user with `billing_status = trial`,
`trial_credit_cents = 1000`, `project_limit = 3`, `xl_limit = 1`. Failure to
reach Logto's Management API yields `internal: identity provider
unavailable` and no user row.

Suspended users (`suspended_at` set) get `forbidden: account suspended`
on every route except `GET /me` and `/billing/portal`.

### 5.3 Projects and the scheduler

`POST /projects`: validate name (`[A-Za-z0-9._-]{1,64}`), derive slug,
check limits (`project_limit`, `xl_limit`), check billing (`payment_required`
when `billing_status = past_due` or trial credit is zero), insert
`projects` with `state = creating`, create a `config_revisions` row from the
default fragment (empty user fragment on the current `base_versions`
release), enqueue an op `create` and return immediately with the project
and `op_id`.

The `create` op:

1. `Build` on a host chosen by `scheduler.Pick(class)`: hosts in state
   `ready`, not draining, heartbeat within 90 s, with `free_mem_bytes -
   reserve >= class RAM` and `pool_free >= volume_bytes`; pick the one with
   the most free memory (spread, not bin-pack, because a host loss then
   hurts fewer users; bin-packing is a later decision when hosts are many).
   Reserve the memory in the same transaction by inserting the host id into
   `projects.host_id` and summing reservations per host in a view
   `host_reservations`. `capacity` error to the user if no host fits, with
   an operator alert.
   No `Build` is sent when the chosen host already runs the closure: the
   applied revision of another live project there has the same fragment
   text on the same published base version. The revision is marked `built`
   with that closure (`kernel_changed` false) and the op goes straight to
   step 2 (DECISIONS I-160, event `build_reused`).
2. On `Build` success, `CreateGuest` with the closure, the project's
   secrets (decrypted for transport, see 5.6), `env {TZ, LANG}`, the user
   CA public key, principals `[project_id]`, hooks config.
3. On success, `state = running`, `started_at`, event `guest_state_changed`.
   On failure at any step, `state = error`, op error recorded, event.

`start`, `stop`, `destroy`, `resize`, `snapshot`, `restore` are ops of the
same shape, each mapping to one or two hostd commands. `stop` sends
`StopGuest{snapshot_first: true}` unless `snapshot: false` in the body.
`destroy` sets the project `destroying` in the DELETE's transaction, sends
`StopGuest{snapshot_first: false}` (skipped when already stopped), then a
final `Snapshot{reason: stop}` of the stopped volume whose row gets
`expires_at = now() + 30 days`, then `DestroyGuest{keep_volume: false}`,
then sets `destroyed_at` (DECISIONS I-165). A failed destroy leaves the
project in `error` with `last_error` and a `destroy_failed` event.
`GET /projects/destroyed` and `POST /projects/restore` (I-167) list the
destroyed projects that can still be restored and restore one by name as
a new project.

`PATCH /projects/:id {class}` requires `state = stopped`; it updates the
reservation and the next `start` uses the new class.

### 5.4 hostmgr and command persistence

Every hostd command the api sends is an `ops` row (`command_id` column)
before it is sent. `hostmgr` holds, per connected host, a channel of
pending `ApiMessage`s. On `Hello`, it queries `ops where host_id = ? and
state in ('pending','running')` and re-sends them. On `Result`, it updates
the op and wakes the op's waiter. `BuildLog` lines are inserted into
`build_logs` in batches of 50 or 200 ms and published on a per-op
in-process broadcast that the SSE handler subscribes to (with a
`since=seq` catch-up from the table for reconnecting clients).

`Samples` are inserted into `meter_samples` and `proc_samples` with a
single `COPY` per message. `projects.signals` is not a column; `GET
/projects/:id` reads the latest sample. `Event`s are inserted into
`events` and acked by `event_id`; duplicates (same `event_id`) are ignored
by a unique index.

Heartbeats update `hosts.last_heartbeat_at` and free-memory columns. A
sweeper marks hosts `unreachable` after 90 s of silence and back to
`ready` on the next heartbeat; projects on an unreachable host show
`state` unchanged but `GET /projects/:id` adds `host_unreachable: true`.

### 5.5 SSH CA

`internal/api/ca`: two ed25519 keys generated by `repose-admin ca init`
and stored encrypted in the `secrets` mechanism under a reserved project id
(`00000000-0000-7000-8000-000000000000`, the platform pseudo-project). In
memory after decryption at start.

`POST /certs`: parse the OpenSSH public key, verify each `project_id`
belongs to the user, mint a certificate per `interfaces/ssh-gateway.md`
(serial from a `certificates_serial` sequence, principals = project ids,
12 h validity, the three extensions), insert `certificates`, return it with
the gateway address and the host CA public key. Rate limit 10 per minute
per user.

Guest host keys: `CreateGuest` is preceded by the api generating an ed25519
host key pair for the guest, signing a host certificate with principal
`<guest_ip>` and `<slug>.<handle>`, and passing both in `CreateGuest` (the
grpc doc gains `host_key` and `host_cert` fields on `CreateGuest` and
`Restore`; recorded as I-3 and added to the interface doc by this
workstream). Gateway host certificate: `repose-admin ca sign-host
--principal ssh.repose.herakraft.co` prints it for workstream 06's
deployment.

Revocation: `POST /certs/revoke` sets `revoked_at`; `/internal/revoked?since=`
returns serials revoked after `since`; a push to connected gateways is
not built (the gateway polls every 30 s, which bounds the exposure and
keeps the api stateless toward gateways).

### 5.6 Secrets

`internal/api/secrets`: on first secret for a user, generate a 32-byte
DEK, wrap it with Key Vault (`WrapKey` on the key `repose-dek-kek`, RSA-OAEP-256)
and store the wrapped DEK on every secret row (`dek_wrapped`, so a per-user
DEK rotation is a rewrap of each row). Values are AES-256-GCM with a random
96-bit nonce prefixed to the ciphertext and the secret name as additional
authenticated data, so a ciphertext cannot be moved between names.

Unwrapped DEKs are cached in memory for 10 minutes. Key Vault unavailable:
`PUT` returns `internal: key service unavailable`; reads for a guest start
also fail closed with the same code, and the op retries with backoff for
up to 30 minutes before erroring. Values are decrypted only to build the
`CreateGuest` or `UpdateSecrets` command, never returned by any route, and
never logged; the gRPC message is the only place plaintext exists outside
the guest.

### 5.7 Config revisions and builds

`PUT /projects/:id/config` accepts `fragment` (max 256 KB, must parse as
Nix, checked by `nix-instantiate --parse` in the api container, which has
Nix installed for this purpose only) or `menu` (rendered by
`menu.Catalog.Render(selection)` from `internal/menu` to a fragment). Insert a
revision `building`, enqueue op `build`, return `revision_id` and `op_id`.
The build op sends `Build` to the project's host (or, if the project has
no host yet, to `scheduler.Pick`), streams logs, and on success sets the
revision `built` and, if the guest is running, sends `ApplyConfig`. The
result's `reboot_required` is surfaced on the op as `reboot_required: true`
and the revision stays `built` until the user confirms with `POST
.../revisions/:rev/apply?reboot=true`. On success `applied`,
`projects.config_revision_id` updated.

Base bumps: `repose-admin base release <version> --nix-rev <rev>
--changelog <file> [--security]` inserts `base_versions`. A daily job (at
04:00 UTC, or immediately for `--security`) finds projects with
`base_version < latest` and `hold_base_updates = false`, and enqueues a
`build` op per project reusing its current fragment. Failures do not stop
the sweep; each failure is an event `base_update_failed` on the project.

### 5.8 Snapshots

`Snapshot` results and nightly `snapshot_done` events both insert
`snapshots` rows (`reason` from the event). A daily expiry job deletes
blobs older than 7 days for live projects, and blobs past `expires_at` for
destroyed ones, through the Blob SDK with the api's identity, then sets
`deleted_at`. Restore: `POST .../snapshots/:sid/restore` requires the
project `stopped` (or `as_new_project`, which creates a project row first),
enqueues op `restore` which sends `Restore` then, on success, `StartGuest`
unless the body says `start: false`.

### 5.9 Events and outbox

`events` rows come from hostd `Event{agent_event}` and from state changes
the api itself makes. The outbox loop selects undelivered rows (`delivered`
lacks a key for each enabled channel), calls `notify.Email` and
`notify.Ntfy` (workstream 13), records the timestamp or error per channel,
and retries errors with backoff up to 24 hours. `GET /projects/:id/events`
reads the table.

### 5.10 Metering rollup

Every hour at :05, under the advisory lock, for the previous hour:
`running_seconds` = count of `meter_samples` rows with `state = running` in
that hour times 60 (capped at 3600); `gb_alloc` = max `disk_alloc` in the
hour; `egress_bytes` = sum of `net_tx` deltas. `cost_cents` =
`billing.Price(class, running_seconds, gb_alloc, egress_bytes)` (09 owns
the function; since I-289 it normalises and prices nothing). Upsert into
`usage_hours`. Samples missing for a whole hour (host down) produce a row
with zeros and an operator alert, never a guess.

### 5.11 Internal routes

`/internal/*` requires the gateway's client certificate (issued by
`repose-admin ca sign-client --name gateway`). `route` resolves
`<slug>.<handle>` to the project, returns `guest_ip`, `state`,
`principals`. `sessions` upserts into an in-memory map that feeds the
`ssh_sessions` figure in `GET /projects/:id` alongside guestd's own count
(both are shown; they should agree).

### 5.12 Rate limits and validation

Token bucket per user id in memory (single replica) with the limits in
`api.md`; `rate_limited` with `Retry-After`. Every request body is decoded
with `DisallowUnknownFields`, and every path id is validated as UUIDv7
before the database is touched.

### 5.13 repose-admin

A separate binary sharing `internal/`, talking to the database directly
(it runs inside the Coolify network, `repose-admin` is an exec into the
api container). `hosts add --provider azure --sku D64s_v5 --region eastus`
inserts a `registering` host row and prints a single-use join token (random
32 bytes, hashed in the row, 24-hour validity). `projects exec <id> --
<argv>` inserts an `audit_log` row, sends `Exec` with the audit id, prints
output. `users suspend <handle> --reason` stops all guests via ops and sets
`suspended_at`.

### 5.14 Deployment on Coolify

`Dockerfile`: multi-stage, `golang:1.25` builder, `gcr.io/distroless/static`
runtime plus a second stage for the Nix parse check (`nixos/nix` slim, only
`nix-instantiate`), user `nonroot`. Coolify app `api`: Dockerfile build,
health check `GET /healthz` every 10 s, rolling deploy on. Env from
Coolify: `DATABASE_URL`, `LOGTO_ISSUER`, `LOGTO_M2M_CLIENT_ID/SECRET`,
`API_RESOURCE`, `KEYVAULT_URL`, `KEYVAULT_KEY_NAME`,
`AZURE_CLIENT_ID/SECRET/TENANT_ID`, `BLOB_ACCOUNT_URL`, `BLOB_CONTAINER`,
`POLAR_*` (09, I-604), `RESEND_API_KEY` (13), `OTEL_EXPORTER_OTLP_ENDPOINT`
(optional), `GRPC_SERVER_CERT/KEY`, `GATEWAY_HOST/PORT`, `API_MODE`. The
full list with defaults is `ops/coolify/api.env.example`; the CA material
is not an environment variable (I-42). Coolify app `api-grpc`: same
image, `API_MODE=grpc`, port mappings 8443 (gRPC) and 8444 (`/internal`
over mTLS, I-42), no rolling deploy. The image is `cmd/api/Dockerfile`.

Migrations run by a Coolify pre-deploy command `repose-admin db migrate`
so a new image never starts against an old schema.

> Amended by DECISIONS I-90 (2026-09-20): Coolify's pre-deploy command runs
> in the previous container and is skipped when there is none, so the api
> applies migrations itself at start (`API_MIGRATE`, default on) and
> generates the CA on its first start; `repose-admin db migrate` and
> `ca init` remain as manual commands.

> Amended by DECISIONS I-87 (2026-09-20): Postgres and its backup are a
> compose resource, `ops/coolify/postgres/docker-compose.yml`; the apps
> stay as above, with `ops/coolify/*.env.example` as their env; the grpc
> certificate is issued from the CA for `GRPC_SERVER_NAMES` (no files); the
> image's `HEALTHCHECK` is `api -healthcheck`, and Coolify's own check is
> off on both api apps because the image is distroless.

### 5.14a What the implementation settled (I-42)

The ops engine, the placement of `/internal`, where the CA keys live, the
guest host-certificate principals, the sample insert path and the schema
additions are recorded in `DECISIONS.md` I-42; `interfaces/db-schema.md`
is the schema as built. Workstreams 09, 12 and 13 replace the interfaces
in `internal/billing`, `internal/menu` (which replaced the api's own
`internal/nixmenu` stand-in, I-119) and `internal/api/notify`
without touching the routes.

### 5.15 Observability

Structured JSON logs (`zerolog`) with `component`, `request_id`,
`user_id`, `project_id`, `host_id` where relevant; never the fields listed
in `ops/OBSERVABILITY.md`. Metrics prefix `repose_api_`: HTTP request
counts and latency by route and status, gRPC stream count, hosts by state,
ops by kind and state, build durations, outbox lag, rollup duration,
secrets operations, cert issues and revokes, scheduler `capacity` errors.
Traces around every op and every hostd command.

## 6. Failure modes

| Failure | Behaviour | Visible outcome |
|---|---|---|
| Logto JWKS unreachable | serve from cache up to 24 h, then `unauthenticated: identity provider unavailable` | CLI: `login service unavailable, retry in a minute`; alert |
| Logto Management API down at first sign-in | no user row, `internal` | CLI: `could not create your account; try again` |
| No host with capacity | op `error`, `capacity` | CLI: `no capacity right now; you have not been charged`; operator alert `capacity_exhausted` |
| Host stream drops mid-op | op stays `running`; resent on Hello; if the host stays unreachable 10 min, op `error: host unreachable` | CLI shows `waiting for host` then the error |
| Build fails | revision `failed` with error and line; op `error` | CLI prints the Nix error and `fragment.nix:<line>`; exit 10 |
| Key Vault down | secret writes fail; guest start retries 30 min then errors | CLI: `secret service unavailable`; alert |
| Polar refuses the overage event | `overage_charges` row without a `sent_ref`, `OverageChargeFailed`; the next hourly tick resends it under the same external id | operator |
| Postgres down | `/healthz` fails, Coolify does not route; hostd streams drop and buffer samples | alert |
| Duplicate `Result` or `Event` | ignored by unique indexes | none |
| Snapshot expiry deletes a blob a restore is reading | restore holds a `snapshots.deleted_at is null` check inside the same transaction that the expiry job uses `select ... for update skip locked` on | none |
| Two api replicas run the rollup | advisory lock; second logs `rollup: not leader` | none |
| Suspended user calls any route | `forbidden: account suspended` | CLI prints it |

## 7. Testing

- Unit: handle derivation, slug rules, scheduler `Pick` (table over host
  states and free memory), cert minting (parse with `ssh.ParsePublicKey`
  and check principals, validity, extensions), secrets round-trip with a
  fake Key Vault (`internal/fakes/kv`), menu render, rollup arithmetic with
  synthetic samples.
- Integration against a real Postgres (testcontainers or a `DATABASE_URL`
  in CI): every route in `api.md` via `httptest` with a signed test JWT
  (test JWKS served in-process), every op end to end against
  `internal/fakes/hostd`, including Hello replay after a simulated stream
  drop.
- Contract test: a generated table of routes from `api.md` (a small parser
  over the markdown tables) compared with the router's registered routes,
  failing on any missing or extra.
- Load: 200 concurrent `GET /projects` and 20 concurrent SSE build streams
  against the fake, p99 under 200 ms.

## 8. Rollback

Coolify keeps previous images; rollback is selecting the previous one from its deployment history, and it is the one deploy that is not automatic (a push to `main` rolls forward by itself, `docs/ops/coolify.md` fact 16).
Migrations are additive within a release (new columns nullable, new tables)
and destructive changes wait one release, so the previous image runs
against the new schema. `repose-admin db migrate --down 1` exists and is
exercised in CI against a seeded database.

## 9. Checklist

Ticked 2026-09-20 by the M3 integration session from the evidence in
`STATUS.md` (05 done-local line, 2026-09-20) and the test names in the
tree; `[~]` is a row whose local half is closed and whose real-path half
waits on host-01 (`ops/checks/README.md` names the script that closes it).

- [x] Every route in `interfaces/api.md` is registered with the documented
      method, path, body and response shape. Evidence:
      `internal/api/http/http_test.go` `TestRouteContract` (the route table
      parsed from `api.md` by `internal/api/apidoc`, 41 routes, none
      missing or extra) and `internal/api/apidoc/apidoc_test.go`
      `TestLoadFindsEveryTable`.
- [~] A Logto-issued token for a new GitHub user creates a user row with
      the derived handle, trial credit 1000, limits 3 and 1. Evidence:
      `internal/api/auth/auth_test.go`
      `TestFirstSignInCreatesUserAndCollisionsSuffix` and
      `internal/api/http/http_test.go` `TestSignInAndProjectsLifecycle`
      (the row read back: `trial_credit_cents`, `project_limit`,
      `xl_limit`, `billing_status`). Real first sign-in: the M2 owner-run
      gate (2026-09-20, in progress on host-01) is the first real token;
      the row is pasted by that session. — open: no production user row from a
      first GitHub sign-in is pasted (the owner's first sign-in was email,
      I-100; `heracraft` came by rename; the second human is M5 step 3); the
      trial figure is now 336 cents (I-205), so the pasted row should read
      336, not 1000.
- [x] Handle collision produces `-2`. Evidence: `TestDeriveHandle` and
      `TestFirstSignInCreatesUserAndCollisionsSuffix` (`internal/api/auth`).
- [x] `POST /projects` enforces project and XL limits and
      `payment_required`. Evidence: `TestSignInAndProjectsLifecycle`
      (limits from `GET /me` and the refusals) and
      `internal/api/http/billing_test.go` `TestBillingGateBlocksCompute`,
      `TestBillingEnforceFalseLetsStartsThrough` (`payment_required
      {reason: card_required}`, I-42).
- [x] Scheduler never places on a draining, unreachable or full host, and
      reserves memory transactionally under concurrent creates (100
      parallel creates on a host with room for 30 yield exactly 30 running
      and 70 `capacity`). Evidence: `internal/api/scheduler/scheduler_test.go`
      `TestPickTable` (draining, unreachable, stale heartbeat, memory and
      pool) and `TestConcurrentPlacementsReserveExactly` (100 creates, 30
      placed, 70 `capacity`).
- [x] Ops survive an api restart and a host stream drop: kill the api
      mid-build, restart, the build completes. Evidence:
      `internal/api/ops/ops_test.go` `TestOpSurvivesApiRestart` and
      `TestOpSurvivesStreamDrop` against `internal/fakes/hostd` (the
      command is re-sent with the same `command_id` on the next `Hello`).
      On the real path: `ops/checks/resilience.sh grpc|http`.
- [x] SSE build log delivers every line in order and supports `since`.
      Evidence: `internal/api/http/http_test.go`
      `TestSSEDeliversEveryLineInOrderWithSince` (10k lines through the
      store, `?since=` and `Last-Event-ID` resume) and
      `TestSSELiveStreamAndConcurrentLoad`.
- [x] Certificates: a cert for project A is refused by the gateway route
      check for project B; revocation appears in `/internal/revoked` within
      one call. Evidence: `internal/api/ca/ca_test.go`
      `TestInitLoadAndUserCert` (principals are the project ids; the
      revoked list before and after) and the gateway's principal check in
      `internal/gateway` (06); on host-01, `ops/checks/isolation-host01.sh`
      runs `TestCertificateForACannotOpenBAtGateway` and
      `TestRevokedCertificateRejected`.
- [x] Guest host keys and certificates are generated per guest and passed
      in `CreateGuest`; `interfaces/grpc-hostd.md` updated (I-3). Evidence:
      `interfaces/grpc-hostd.md` `CreateGuest` row (`host_key`,
      `host_cert`); `internal/api/ca/ca_test.go`
      `TestGatewayAndGuestHostCerts`; `TestLifecycle` (`internal/api/ops`)
      with the fake hostd asserting the fields; host-01 booted with them at
      M2 (STATUS m2-integration).
- [~] Secrets: value round-trips; ciphertext for name A fails to decrypt
      under name B; DEK rewrap after a Key Vault key version bump works
      without touching ciphertext. Evidence: `internal/api/secrets/secrets_test.go`
      `TestRoundTripAndAAD`, `TestRewrapAfterKeyVersionBump`,
      `TestDEKCacheLimitsKeyVaultCalls`, `TestKeyVaultDownFailsClosed`.
      Real Key Vault: the CA init at first start in production wrapped and
      unwrapped a real DEK (DECISIONS I-91, 2026-09-20); `repose-admin
      secrets rewrap` against the real key is not yet run. — open: `az
      keyvault key rotate` then `repose-admin secrets rewrap` (RUNBOOK)
      against the production Key Vault, with the before/after key version and
      a secret read back, is not recorded.
- [x] No route ever returns a secret value; `rg` for the decrypt function
      shows callers only in the hostd command builders. Evidence
      (2026-09-20): `rg -n 'DecryptForGuest\(' internal/api cmd` finds the
      definition, `internal/api/ops/phases.go:156,213` (the CreateGuest,
      StartGuest, Restore and UpdateSecrets builders), `phases.go:383` (the
      same values for build-log redaction and the secret-in-fragment
      refusal, I-42) and `secrets.go:370` (the platform's own CA rows);
      `GET /secrets` reads `secrets.Meta`, which has no value field. Real
      path (`ops/checks/secrets.sh`, host-01, m3-check, 2026-09-20 23:50Z):
      the value was in no response, in neither api container's logs, not
      in hostd's journal, `build_logs`, `events` or `audit_log`; the guest
      file was `dev dev 400` on tmpfs in a `dev 700` directory, exported
      in a login shell, gone within 5 s of `rm`; a fragment carrying it
      was refused (`fragment contains the value of secret
      M3_CHECK_SECRET`).
- [x] Config: fragment with a parse error is rejected at `PUT` with the
      line; menu selection renders to a fragment that builds. Evidence:
      `internal/api/config/parse_test.go` `TestParseCheck` and
      `internal/api/http/http_test.go` `TestConfigRoutes` (the `invalid`
      with `fragment_line`); `internal/menu` `TestEveryEntryRendersAndRoundTrips`
      and `TestRealNixAllEntriesEvaluate`. The real build on host-01 is
      `ops/checks/menu.sh`. — closed: parse error rejected with its line on
      the deployed api (12 §9 canonical-cases row, M5 session 2026-09-21
      02:29Z, `syntax.nix:1:34`, exit 10); menu build on host-01 in RESEARCH
      §13 (`PUT /config {menu: bun}`, op done in 5 s, `bun` 1.4.2 on PATH).
- [x] `reboot_required` blocks apply until confirmed. Evidence:
      `internal/api/ops/ops_test.go` `TestBuildFailureAndRebootRequired`.
- [x] Base bump job builds every unheld project and skips held ones.
      Evidence: `internal/api/basebump/basebump_test.go`
      `TestSweepBuildsUnheldSkipsHeld` and `internal/basebump`
      `TestThreeProjects`. On the real path: `ops/checks/resilience.sh bump`
      (base 2026.09.21.1 was published and smoked 2026-09-21 00:25Z:
      create 43 s, snapshot 17 s, stop 5 s, start 15 s, destroy 21 s). Real
      sweeps 2026-09-21: 02:29:22Z (2026.09.21-m3-0220, security) built
      the four unheld projects and skipped the held one, 4 min to 9 min
      after the publish; 02:59:18Z (2026.09.21.3) built six. What those
      sweeps exposed and what fixed it: bumps built against the old base
      (I-134), a sweep lost to an api-grpc redeploy (I-141), the
      activation stranding guestd (I-143), the clone race (I-144), the
      failure wording (I-145) and the skip rule (I-146). Proof sweeps
      after those deployed: 2026.09.21.4 (04:34Z) rebuilt every project,
      retried nuru-playground's failed clone through one serialized
      checkout, switched the LTS guests in place with guestd untouched,
      and left the 7.2.6 guest with reboot_required; 2026.09.21.5
      (05:00Z, guests already on a base with I-143) switched m3-held and
      m3-iso-c through the transient unit, the op `done` at 05:00:22Z,
      guestd's self-restart 5 s later onto the new binary with no
      `guestd_lost` (the gap is under the 60 s threshold). age-calculator
      (guestd stopped since 02:59Z, awaiting the owner) got the I-145
      wording each time and nothing else.
- [x] Snapshot expiry removes blobs on schedule and never one referenced
      by a running restore. Evidence: `internal/api/snapshots/expiry_test.go`
      `TestExpiryRules` with the fake blob store (`restoring_op_id` guard).
      On the real path: `ops/checks/resilience.sh expiry`. A refused blob
      delete no longer ends the run: `TestExpiryContinuesPastOneFailedBlob`
      (I-130).
- [x] Expiry has deleted a real blob in production. Evidence: after
      I-131's role assignment (applied 01:52Z), the first run at 01:58Z
      deleted the two rows the 01:27Z run had been refused on,
      `01a0c191-7345-7672-987b-876f81e0ca3d` and
      `01a0c191-c412-7ba6-b233-e227e58625ba`, both `deleted_at 2026-09-21
      01:58:24.774Z`, each with an api `snapshot_expired` line at 01:58:25Z
      and no 403; a rerun at 02:00Z with the restore started through the
      api kept the snapshot the restore held (`restoring_op_id` set),
      deleted the other aged row at 02:01:20Z and the held one at
      02:02:37Z once the restore finished, and kept the newest
      (`ops/checks/out/resilience-20260921T015959Z.txt`).
- [x] Outbox delivers each event once per channel and retries failures.
      Evidence: `internal/api/notify/notify_test.go`
      `TestOutboxDeliversOncePerChannelAndRetries`,
      `TestPermanentFailureIsNotRetried`.
- [x] Hourly rollup produces the documented figures for a synthetic day
      (one large guest 10 h running, 40 GB, 3 GB egress). Evidence:
      `internal/api/meter/meter_test.go` `TestIngestAndSyntheticDayRollup`
      (36000 running seconds, 3 GB, no cents: plan-v1, I-289). The Stripe
      push half of this row went with I-289.
- [x] `/internal/*` rejects requests without the gateway client cert.
      Evidence: `internal/api/app/app_test.go`
      `TestProcessDevModeAndInternalMTLS`. On the live control plane the
      edge reaches `/internal` at `10.255.255.1:8444` only with the
      gateway certificate signed by `repose-admin ca sign-client`
      (DECISIONS I-92, M2 session); the `curl` outputs without a
      certificate are pasted by the M2 session. — closed: STATUS 2026-09-20
      m2-integration progress line (steps 1-2: `/internal/hosts` answers with
      the gateway certificate, refused without) and
      security/review-2026-09-21.md M5-4 (8444 from the edge: a bare TLS
      connection is refused).
- [x] Rate limits return `rate_limited` with `Retry-After`. Evidence:
      `internal/api/http/http_test.go` `TestRateLimits` and
      `internal/api/ratelimit/ratelimit_test.go` `TestBucket`.
- [x] `repose-admin` subcommands all exist and `hosts add` produces a
      token that registers a host exactly once. Evidence:
      `internal/admin/admin_test.go` `TestAdminSurface` (every I-9
      subcommand); host-01 registered with the real api on 2026-09-20
      through a token from `hosts add` and is `ready` (`repose-admin hosts
      list`, M2 session, STATUS); the second-use refusal on the real host
      is `nix/hosts/tests` host-services against `hostdev` (a second token
      after `host.json` exists is not consumed). — closed: host-01's real
      registration (STATUS 2026-09-20 m2-integration step 3 line, 18:15Z); the
      once-only half is `TestRegisterSessionSendSweep` in internal/api/hostmgr
      ("token accepted twice") and nix/hosts/tests host-services (second token
      not consumed, host.json untouched).
- [~] `/healthz` fails when Postgres is down or migrations are pending;
      Coolify stops routing. Evidence: `internal/api/http/http_test.go`
      `TestHealthz` (DB ping and migration state). The staging DB pause is
      not done (no staging control plane exists). — waits on: owner (a staging
      control plane, or permission to pause production Postgres and watch Coolify
      stop routing).
- [ ] Rolling deploy on Coolify serves requests throughout a deploy.
      Evidence: a `while curl` loop with no failures during a deploy.
      Owner: the m3-web session (`ops/deploy-probe.sh`). — waits on: owner
      (the Traefik drain on the Coolify proxy): the probe measured 14 failures
      in 29,841 responses over seven switchovers (STATUS 2026-09-20 m3-web
      second progress line, ops/coolify.md fact 13), so "no failures" does not
      hold yet; rerun `ops/deploy-probe.sh` after.
- [x] `api-grpc` restarts are absorbed by hostd reconnect with no lost
      commands. Evidence: deploy during a build, the build completes.
      `ops/checks/resilience.sh grpc` on host-01. — closed: RESEARCH §13
      (api-grpc SIGKILLed 8 s into a build: back in 6 s, hostd
      `stream_connect` 33 s after, the build op done with no lost command) and
      STATUS 2026-09-21 m3-integration "Step 5 on the real api" line.
- [x] Logs contain none of the forbidden fields. Evidence:
      `internal/obs/logger_test.go` `TestRedaction` (a planted value under
      every never-log name the api and hostd use is absent from the
      output; the handler redacts by name, DECISIONS I-52, I-60) and
      `internal/obs/obslint` over `cmd` and `internal` in `go test`.
- [x] `repose-admin db migrate --down 1` works in CI. Evidence:
      `.github/workflows/ci.yml` step "repose-admin db migrate --down 1 on
      a seeded database" (migrate, seed, down 1, up) and
      `internal/db/db_test.go` `TestMigrateUpDownUp`.
- [x] No `TODO`, `FIXME`, `panic(` outside main, `_ = err` under `cmd/api`,
      `cmd/repose-admin`, `internal/api`, `internal/db`. Evidence
      (2026-09-20): `just done-check "cmd/api cmd/repose-admin
      internal/api internal/db"` finds two `panic(err)` in test fixtures
      (`internal/api/http/billing_test.go:157`,
      `internal/api/notify/notify_test.go:287`, each commented as a bug in
      the test itself) and no leftovers.
- [x] `ops/RUNBOOK.md` has an entry per row in section 6. Evidence: the
      entries "api: login service unavailable", "api: could not create
      your account", "api: no capacity right now", "api: op stuck waiting
      for host", "api: build failed", "api: secret service unavailable",
      "OverageChargeFailed", "api: Postgres down", "api: duplicate results or
      events", "api: snapshot deleted under a restore", "api: rollup or
      expiry not running on one replica", "api: user reports \"account
      suspended\"".
- [ ] A destroy of a running project on host-01 reads `destroying` at
      once and ends with a `stop`-reason snapshot expiring in 30 days,
      and `POST /projects/restore {slug}` brings it back under its name
      (I-165, I-167). Evidence: `repose projects` right after `repose
      destroy`, the destroy op's phases, and `repose restore NAME`
      reaching `running`; locally `TestRestoreByName` and
      `TestDestroyStopsFirstAndReportsItsFailure` pass. — open: STATUS
      2026-09-23 conductor 03:40Z records the live run (destroy accepted in
      0.28 s, done on the host in about 4 s, restore 12 s, marker sha256
      identical) but not the `repose projects` line reading `destroying`, the
      snapshot's `stop` reason and 30-day expiry, or the `repose restore NAME`
      transcript; paste them from the next destroy/restore.
- [x] `docs/DECISIONS.md` carries I-2 (separate gRPC app) and I-3 (guest
      host keys in CreateGuest). Evidence: the entries.
