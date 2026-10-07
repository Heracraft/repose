# Workstream 10: observability

## 1. Goal

Every component emits structured logs and Prometheus metrics from its first
commit, shipped to the Loki, Grafana and Prometheus stack that already runs on
the owner's personal server. The signals that later decide idle policy,
pricing, and abuse response are recorded from milestone M1 onward, because a
policy designed without a month of data is a guess.

## 2. Scope: builds

- `internal/obs`: one Go package used by every binary. `obs.Logger`
  (structured JSON via `log/slog`), `obs.Metrics` (a `prometheus.Registry`
  with the `repose_` namespace pre-set), `obs.Tracer` (OpenTelemetry with an
  OTLP exporter that is a no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset).
- The metric and log-event naming rules below, enforced by a test that fails
  on any metric outside the `repose_` namespace or any log call missing
  `component`.
- Fluent Bit configuration in `nix/hosts/fluent-bit.nix`: journald input for
  host units, a tail input for every `/var/lib/repose/guests/*/console.log`,
  output to Loki over WireGuard with labels `host`, `component`, and for
  console logs `guest_id`.
- Prometheus on the personal server: scrape configs for hosts (`hostd`
  `:9100` node_exporter and `:9101` hostd, both bound to the WireGuard
  address), the edge (gateway `:9102`), the Coolify VM (api `:9103` bound to
  the WireGuard address, since the Coolify VM is also a WireGuard peer of
  the edge for this purpose).
- Grafana dashboards as JSON in `ops/dashboards/`, provisioned by the
  personal server's Grafana: host capacity, per-guest resources, builds,
  gateway, snapshots, billing, abuse.
- Alert rules as Prometheus rules in `ops/alerts.yaml`, one per alert in
  `../CHECKLIST.md`, routed to the existing Alertmanager (ntfy to the owner).
- Retention: Loki 30 days for console logs and 90 days for component logs;
  Prometheus 90 days; `meter_samples` 90 days and `proc_samples` 30 days in
  Postgres (partition drops, see `interfaces/db-schema.md`).

## 3. Scope: does not build

- Sampling itself. hostd and guestd collect `Samples` (workstreams 03 and
  04); this workstream defines what the samples must contain and how they
  are exported.
- The hourly rollup into `usage_hours` (workstream 09).
- Abuse *response* tooling (`repose-admin suspend`, workstream 05). This
  workstream builds the dashboards that show what to respond to.
- A traces backend. `obs.Tracer` is wired; Tempo or equivalent is a later
  decision.

## 4. Interfaces

Owns: metric names, log event names and fields, the `obs` package API, the
Fluent Bit label set, dashboard and alert definitions.

Consumes: everything. Every other workstream calls `obs`.

## 5. Design detail

### Log events

One JSON object per line on stdout (journald on NixOS hosts, Docker logs on
Coolify). Required fields on every line: `ts` (RFC 3339 UTC), `level`,
`component` (`api|hostd|guestd|gateway|cli|admin`), `event` (lower_snake,
the semantic name of what happened), `msg` (human sentence). Contextual
fields when they exist: `request_id`, `command_id`, `op_id`, `project_id`,
`guest_id`, `host_id`, `user_id`. Never `handle`, `email`, `remote_url`
(it can contain a token), certificate bodies, secret names *and* values,
prompts, terminal contents, process arguments, IP addresses of users, user
agents.

The reason for the never-list: a Loki query is the fastest way to leak a
tenant's data to whoever holds the Grafana password, and logs outlive the
incident that justified them. Bounded enums, ids, counts and durations
carry the same diagnostic value.

Events are intentional, not `fmt.Sprintf` residue. A list of the events each
component must emit:

- hostd: `guest_create`, `guest_start`, `guest_stop`, `guest_destroy`,
  `guest_state`, `build_start`, `build_done`, `build_fail`, `switch_done`,
  `snapshot_start`, `snapshot_done`, `snapshot_fail`, `restore_done`,
  `restore_fail` (I-403), `stream_connect`,
  `stream_disconnect`, `guestd_lost`, `guestd_regained`, `pool_warning`,
  `store_warning`, `egress_blocked` (a guest over a block's threshold,
  DECISIONS I-238..I-240), `boot_fallback` (a boot that never reached
  Ready booted the guest's last good closure; carries the code, never
  console text, I-590).
- guestd: `ready`, `freeze`, `thaw`, `freeze_timeout`, `switch`,
  `agent_event`, `agent_state`, `oom_priority` (I-200, counts only),
  `hook_bad_payload`, `agent_question` (I-244: ids, states, counts and
  byte sizes, never the text), plus the one-per-request
  events 04 emits: `grow_fs`, `write_secrets`, `set_principals`,
  `setup_project`, `sample` (debug, carries `duration_ms`), `exec` and
  `shutdown`, and `warning` (carries `kind`, the enumeration in
  `interfaces/vsock-guestd.md`).
- api: `request` (method, route, status, duration_ms), `cert_issue`,
  `cert_revoke`, `schedule` (host chosen, free memory), `schedule_fail`,
  `schedule_wait` (no host fits until a guest being stopped is down, I-408),
  `command_send`, `command_result`, `build_reused` (a create whose closure
  was already on the host, DECISIONS I-160), `rollup_done`, `webhook_received`,
  `overage_charged`, `gate_refused` (I-289),
  `notify_send`, `notify_fail`, `admin_action`, `abuse_stop` (I-239),
  `waitlist_join`, `waitlist_admit`, `waitlist_admit_fail` (I-269),
  `boot_fallback` (a start whose boot fell back, I-590), `op_console`
  (a failed boot's console not stored; never its text, I-592).
- gateway: `session_open`, `session_close`, `auth_fail` (reason enum:
  `bad_cert|expired|revoked|wrong_principal|stopped|not_found`; since
  I-599 also `counted` and, when it counts towards the source's ban,
  `failures`; at most 2 a second with 60 at once, the dropped ones counted
  in the next line's `suppressed`), `auth_ban` (a source banned:
  `source_prefix`, `failures`, `window_s`, `ban_s`; at most one every 10 s
  with 20 at once, I-599), `route_fail`, `dial_fail`.
- cli: only to a local file `~/.config/repose/cli.log` at debug level when
  `--verbose`; nothing is shipped from laptops.

### Metrics

Prefix `repose_`. Labels are low-cardinality only: `component`, `host_id`,
`class`, `state`, `kind`, `reason`, `route`, `status`. `project_id` and
`guest_id` are never labels in Prometheus; per-project figures come from
`meter_samples` in Postgres and the billing dashboard queries there. The
reason: 30 guests per host across a fleet is fine, but a label that grows
with every project ever created makes Prometheus the first thing to fall
over.

Families:

- Host (hostd): `repose_host_mem_free_bytes`, `repose_host_mem_reserved_bytes`,
  `repose_host_pool_free_bytes`, `repose_host_store_bytes`,
  `repose_host_guests{state,class}`, `repose_host_builds_running`,
  `repose_host_build_duration_seconds{result}` (histogram),
  `repose_host_snapshot_duration_seconds{reason,result}`,
  `repose_host_snapshot_bytes`, `repose_host_stream_connected` (0/1),
  `repose_host_commands_total{kind,result}`, `repose_host_guestd_lost`
  (gauge, count of guests with `guestd_ok=false`),
  `repose_host_guest_cpu_seconds_total{class}` (summed over guests),
  `repose_host_guest_net_bytes_total{direction}`,
  `repose_host_egress_blocked_total{reason}` and
  `repose_host_egress_blocked_guests{reason}` (reason `smtp`, `stratum`,
  `flows`; I-238..I-240),
  `repose_host_guest_notify_dropped_total{reason}` (guest notifications
  not forwarded: `rate_limited`, `invalid`, `queue_full`; I-445).
- API: `repose_api_requests_total{route,method,status}`,
  `repose_api_request_duration_seconds{route}`,
  `repose_api_hosts{state}`, `repose_api_projects{state,class}`,
  `repose_api_schedule_total{result}`, `repose_api_certs_issued_total`,
  `repose_api_certs_revoked_total`, `repose_api_rollup_lag_seconds`,
  `repose_api_notify_total{channel,result}`,
  `repose_api_billing_webhook_total{kind,result}`,
  `repose_api_billing_overage_charges_total{result}`,
  `repose_api_billing_gate_refused_total{reason}`,
  `repose_api_billing_subscriptions_total{plan,status}`,
  `repose_api_billing_stops_total{reason}` (I-289),
  `repose_api_snapshot_age_seconds` (max over running projects; the alert
  input), `repose_api_abuse_stops_total{kind}`,
  `repose_api_abuse_held_projects`,
  `repose_api_abuse_busy_unattended_projects` (I-239),
  `repose_api_seats_total`, `repose_api_seats_held`,
  `repose_api_waitlist_waiting`, `repose_api_waitlist_joined_total`,
  `repose_api_waitlist_invited_total`, `repose_api_waitlist_converted_total`,
  `repose_api_waitlist_expired_total` (I-269, I-290),
  `repose_api_host_reports_refused_total{reason}` (`foreign_guest`,
  `project_cap`, `bad_snapshot`; I-445, I-447),
  `repose_api_samples_failed_total{reason}` (`guest_fields`, `insert`;
  I-446).
- Gateway: `repose_gateway_sessions` (gauge), `repose_gateway_sessions_total`,
  `repose_gateway_auth_fail_total{reason}`, `repose_gateway_dial_fail_total`,
  `repose_gateway_route_duration_seconds`.
- Fleet-level abuse views are Grafana queries over Postgres `proc_samples`
  (top `comm` by CPU across all projects, top egress by project), not
  Prometheus series.

### Day-one signals

Recorded in `meter_samples` every 60 seconds per project, from DESIGN §15:
state, class, CPU ns delta, RSS, net tx/rx delta, disk allocated and used,
SSH sessions open, tmux clients attached, agent processes with tmux window
and state (`working|idle|needs_input|unknown`), Docker containers running,
`guestd_ok`. Plus `proc_samples`: `comm`, CPU ns delta, RSS. The gateway's
`POST /internal/sessions` cross-checks `ssh_sessions`.

These are the inputs to three decisions the owner deferred: what an
unattended agent looks like from outside (idle policy), whether memory or
CPU binds first (tier shapes and E-series vs D-series), and what abuse looks
like (miners, fork bombs, egress). Nothing in this list identifies a user
beyond their project id.

### Dashboards

| Dashboard | Panels |
|---|---|
| Overview | the quick look, Prometheus and Loki only: targets down, hosts ready, hostd streams, guestd lost, log shipping failures, newest snapshot age; guests running and in error, SSH sessions, builds, memory reserved, pool used; trends; recent WARN/ERROR from the platform's services; links to the rest |
| Host capacity | reserved vs free memory per host, pool free, store size, guests by state, builds running, stream connected |
| Per-guest resources | from Postgres: CPU, RSS, net, disk for one project over time; signals timeline |
| Builds | duration histogram, failures by error code, queue depth, eval vs build time |
| Gateway | sessions, auth failures by reason, dial failures, route latency |
| Snapshots | age per running project (table), bytes per day, failures |
| Billing | subscriptions by plan and status, webhook results, gate refusals by reason, overage lines, hours per class, egress per account and per hour |
| Abuse | top 20 `comm` by CPU fleet-wide (24h), top projects by egress, guests with 100 percent CPU and zero sessions for over 24h |

### Alerts

Each maps to a `../ops/RUNBOOK.md` entry of the same name.

| Alert | Rule | Severity |
|---|---|---|
| `HostMemory80` | `reserved / (reserved+free) > 0.8` for 10m | warn |
| `HostUnreachable` | `repose_api_hosts{state="unreachable"} > 0` for 2m | page |
| `SnapshotStale` | `repose_api_snapshot_age_seconds > 36*3600` | warn |
| `BuildQueueStuck` | `repose_host_builds_running >= 2` and no `build_done` for 45m | warn |
| `GatewayAuthSpike` | `rate(repose_gateway_auth_fail_total[5m]) > 1` | warn |
| `EgressHigh` | Postgres: any project over 1 TB in 24h (checked hourly by api, exported as `repose_api_egress_alert_projects`) | warn |
| `PoolFull` | `pool_free_bytes / pool_bytes < 0.1` | page |
| `StoreFull` | host root fs > 85 percent | warn |
| `GuestdLost` | `repose_host_guestd_lost > 0` for 5m | warn |
| `RollupLag` | `repose_api_rollup_lag_seconds > 2*3600` | warn |
| `PaddleWebhookRejected` | `sum(increase(repose_api_billing_webhook_total{result="bad_signature"}[10m])) >= 5` | warn |
| `OverageChargeFailed` | `increase(repose_api_billing_overage_charges_total{result="error"}[1h]) > 0` | warn |
| `BillingStopped` | `sum by (reason) (increase(repose_api_billing_stops_total[1h])) > 0` | info |
| `EgressBlocked` | `repose_host_egress_blocked_guests > 0` for 2m (I-238..I-240) | warn |
| `SamplesFailing` | `sum by (reason) (increase(repose_api_samples_failed_total[15m])) > 0` (I-446) | warn |
| `HostReportsRefused` | `sum by (reason) (increase(repose_api_host_reports_refused_total{reason=~"foreign_guest\|bad_snapshot"}[15m])) > 0` (I-447) | warn |
| `MinerStopped` | `sum by (kind) (increase(repose_api_abuse_stops_total[15m])) > 0` (I-239) | warn |
| `BusyUnattended` | `max(repose_api_abuse_busy_unattended_projects) > 0` for 15m (I-239) | warn |

### Traces

`obs.Tracer` wraps the api's HTTP handlers, the gRPC stream handling, and
the Postgres calls with OpenTelemetry spans. With no OTLP endpoint set the
exporter is a no-op and the cost is one context value per request. When a
backend exists, setting one environment variable turns it on.

## 6. Failure modes

| Failure | Outcome |
|---|---|
| Loki unreachable from a host | Fluent Bit buffers to disk (`/var/lib/fluent-bit`, 1 GB cap), retries; a host-side `fluent_bit_output_retries_failed_total` alert fires after 30m. Guests are unaffected. |
| Prometheus cannot scrape a host | `up{job="hosts"} == 0` alert; hostd keeps sampling into Postgres via the gRPC stream, so billing data is not lost. |
| A component emits a metric outside `repose_` | the `obs` package test fails at build time. |
| A log line carries a forbidden field | a reviewer catches it (checklist item); `obs.Logger` additionally redacts any field named `token`, `secret`, `password`, `authorization`, `cert` at runtime as a floor. |
| `proc_samples` partition drop fails | the api logs `partition_drop_fail` and alerts; disk grows but nothing else breaks. |

## 7. Testing

- Unit: `obs` naming tests; redaction test with every forbidden field name.
- Integration: a `docker compose` in `ops/dev/` with Loki, Prometheus and
  Grafana for local development; the dashboards must load without errors
  against it (`grafana-cli` lint in CI).
- On a real host (M1): Fluent Bit ships a guest's console log within 10
  seconds of a `guest_start`, visible in Grafana Explore by `guest_id`.
- Alert rules: `promtool test rules` with a fixture per alert.

## 8. Rollback

Disable Fluent Bit's Loki output and Prometheus's scrape job; nothing
depends on either for correctness. Dashboards and alerts are files; revert
the commit.

## 9. Checklist

Closed 2026-09-20/21 by the m3-web session unless marked otherwise. The
two open rows are the two that need a machine of the owner's.

- [x] `internal/obs` exists, every binary uses it, and no binary imports
      `log` or `fmt.Print*` for logging. Evidence: `internal/obs/obslint`
      is a test over `cmd` and `internal` rather than a grep anyone has
      to remember to run, and it passes; the remaining `"log"` and
      `fmt.Print` hits are `main` bootstrap lines, the linter's own
      fixtures, and the two test fixtures `cmd/fakeapi` and
      `cmd/fake-logto`. `go test ./internal/obs/...` green across obs,
      instrument, metrics and obslint.
- [x] Every event in §5 is emitted by its component. Evidence:
      `go test ./internal/obs -run TestEventsEmitted`, which lists each
      event's call site and fails on one a built component does not
      emit; `obs.PendingEvents` carries the ones owed by unbuilt
      workstreams (`webhook_received`, 09) so the test does not pass by
      silence.
- [x] Every metric family in §5 exists with exactly those labels.
      Evidence: `families_host_test.go` and `families_api_test.go` hold
      the families to §5, and the registry refuses a metric outside
      `repose_` or with a label off the low-cardinality list *at
      registration*, so a bad name stops the binary at start. Against a
      real host: hostd on host-01 serves **195 `repose_host_*` series**
      on `10.255.0.2:9101`. One family is registered and emits nothing —
      see the last row.
- [ ] Fluent Bit on a real host ships journald and guest console logs with
      the documented labels. Evidence: Loki query screenshot or output for
      one guest. **Open: needs the Loki URL.** Everything below it is
      ready — the api carries `loki_url` in `RegisterResponse` and fills
      it from a setting (I-95), `repose-admin edge loki <url>` is live on
      the deployed image and answers "no Loki recorded; hosts ship
      nothing", and `fluent-bit.service` refuses to start without one
      rather than retrying an empty host name for ever. Fluent Bit
      itself answers on host-01 (`10.255.0.2:2021`,
      `/api/v1/metrics/prometheus`, 200). — open: the Loki URL now exists
      (DECISIONS I-170, `http://10.255.0.3:3100`; archive/HANDOFF-2026-09.md "Owner's
      monitoring server joined": the edge and host-01 ship, streams carry
      `service_name`), but no Loki query output for one guest's console
      stream with the documented labels is recorded; paste a `logcli` or
      Grafana Explore result for one guest.
- [ ] Prometheus scrapes hosts, edge and api over WireGuard. Evidence:
      `up` series listed. **Open: needs the monitoring server's
      WireGuard public key.** Two of the three halves are done: the
      exporters answer (edge `9100`/`9102`, host-01 `9100`/`9101`/`2021`,
      `api-grpc` `9104`), and I-94 gave the edge the forward rules a
      scrape needs — but they are *inert* until a peer is declared, and
      the live edge's forward chain is still `established,related` and a
      drop, because `edge-01.nix` sets no `monitoring.peerCIDRs`. When
      the key arrives it is a `staticPeers` entry plus that option and
      one rebuild; `ops/prometheus/wireguard-peer.conf` has all three
      parts and the `/etc/hosts` line the api's router needs (I-133).
      Closing this row also means checking that
      `curl https://api.repose.herakraft.co/metrics` from outside
      answers 403. — waits on: owner (CHECKLIST-AUDIT.md "Waits on the owner", the api's
      `/metrics` Traefik router labels in Coolify, I-133). The WireGuard
      peer is live (I-170) and archive/HANDOFF-2026-09.md records hosts, hostd, fluent-bit,
      gateway and api-grpc `up` in the owner's Prometheus, but the `up`
      series are not listed anywhere and the outside 403 check is not
      recorded.
- [x] All seven dashboards load and every panel renders with data from a
      real host. Evidence: all seven load into a real Grafana 12.4.0 with
      no provisioning error (`ops/check.sh --grafana`: host capacity 12
      panels, per-guest 8, builds 8, gateway 8, snapshots 6, billing 11,
      abuse 6), and against production series **41 of 43 Prometheus panel
      queries return data** (`ops/dashboards/validate.py --query`,
      host-01, 2026-09-21 04:15Z). The two that did not were Stripe's,
      off by I-16, and are gone with I-289 — there is no panel left that
      is empty for a reason of its own.
- [x] All eleven alerts exist, have a `promtool` test, and have a RUNBOOK
      entry. Evidence: 18 rules now (the eleven plus I-56's two,
      billing's three, and `FluentBitLogShipperDown`, below); `ops/check.sh` runs `promtool check rules`,
      `promtool test rules` (17 cases) and a grep asserting every alert
      name is a RUNBOOK heading, and all three pass. Loaded against
      production series they evaluate healthy, none firing and none in
      error.
- [x] Retention set: Loki 30/90 days, Prometheus 90 days, partition drops
      scheduled and exercised once. Evidence: `ops/loki/retention.yaml`
      with the compactor, the retention flag in
      `ops/prometheus/prometheus.yml`'s header, and
      `EnsurePartitions`/`repose_partitions_maintain()` with the
      `PartitionDropFail` series and alert behind it.
- [x] The never-log list is enforced by redaction and stated in
      `../ops/OBSERVABILITY.md`. Evidence: the log handler redacts by
      substring match with an exact allowlist for the ids that contain
      one (I-60), pinned by the naming and redaction tests in
      `internal/obs`; the same sentence is in `docs/SECURITY.md` and in
      the published privacy policy, which `tests-live/public.spec.ts`
      asserts verbatim on the served page.
- [x] `OTEL_EXPORTER_OTLP_ENDPOINT` unset produces no network calls.
      Evidence: `internal/obs/instrument/trace_test.go`
      `TestNoDialWithoutAnEndpoint`, plus `TestTracingOffByDefault` and
      `TestGlobalTracerIsNoopWhenOff`.

**A finding of mine that is already fixed, corrected here so the record
does not mislead:** at 2026-09-20 23:50Z `repose_host_guests` had no
series at all on host-01 with three guests running, so "Guests by state"
was blank, and I handed it over with a hypothesis about startup order.
The m3 session had found it independently and fixed it as
**DECISIONS I-116**, with the better diagnosis: a `GaugeVec` with no
children exposes no series at all, so the panel read "no data" where it
should have read 0. `refreshGuestGauge` now pre-initialises every state
and class at zero before counting. Verified on host-01 after that
shipped: 30 series, `running=5` across two classes, and the panel
returns data. My startup-order hypothesis was never confirmed and is
moot — the pre-initialisation makes the symptom impossible either way.
