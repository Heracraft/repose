# Observability, for operators

Signal locations, how to look at one project, and the fields that must
never be in a log. The design rules and the full metric list are in
`../workstreams/10-observability.md`.

## Where things are

```
hosts (NixOS)          journald ─▶ Fluent Bit ─▶ Loki  (labels host, component, service_name; guest_id for console and guest@/virtiofsd@ units)
                       hostd :9101 /metrics, node_exporter :9100,
                       Fluent Bit :2021 /api/v1/metrics/prometheus  ◀─ Prometheus scrape over WireGuard
edge (NixOS)           gateway :9102 /metrics; journald ─▶ Fluent Bit ─▶ Loki
Coolify VM (Ubuntu)    api, web containers: stdout ─▶ Coolify log drain ─▶ Loki; api :9103 /metrics over WireGuard
Postgres               meter_samples, proc_samples, usage_hours, events, audit_log (Grafana Postgres datasource, read-only role)
personal server        Loki, Prometheus, Grafana, Alertmanager, calert (Google Chat to the owner)
```

Every exporter binds the host's WireGuard address and nothing else; a repose
binary refuses a metrics address that would bind every interface. The files
that configure the personal server are in `ops/` and its README has the
install order; `docker compose -f ops/dev/docker-compose.yml up -d` is the
same Grafana and the same dashboards locally, with no data.

Nothing observability-related runs on hosts beyond Fluent Bit and the two
exporters, and nothing is reachable from the internet: every scrape and
ship goes over the edge's WireGuard.

## Looking at one project

1. `repose-admin projects show <id-or-slug>`: state, host, class, last
   snapshot, current signals. Start here. It prints no cost: plan-v1
   prices nothing, so every cents column in `usage_hours` is zero, and
   the row that read them printed `$0.00` for every project (I-808). What
   an account owes is its plan, its egress against the allowance, and the
   disk its projects hold: `repose-admin billing show <handle>`.
2. Grafana "Per-guest resources", variable `project_id`: CPU, memory, net,
   disk, and the signals timeline (sessions, tmux clients, agent state)
   from `meter_samples`.
3. Logs. Loki queries:
   - hostd events for the guest: `{component="hostd"} | json | guest_id="<guest id>"`
   - the guest's console: `{component="console", guest_id="<guest id>"}`
   - api requests: `{component="api"} | json | project_id="<id>"`
   - gateway sessions: `{component="gateway"} | json | event=~"session_.*" | project_id="<id>"`
4. Builds: `repose-admin ops list --project <id>` then `ops log <op id>`
   for the full Nix output (stored in `build_logs`, not Loki).
5. Events the user saw: `select ts, kind, agent, summary, delivered from
   events where project_id = ... order by ts desc`.

## Looking at one host

Grafana "Host capacity", variable `host_id`. Then on the host itself:
`systemctl list-units 'guest@*'`, `lvs vg-guests`, `nft list counters`,
`journalctl -u hostd -f`.

Capacity (DECISIONS I-738): `repose_host_mem_reserved_bytes` and
`repose_host_mem_free_bytes` are memory, and `repose_host_cpu_cores` with
`repose_host_load1` are CPU. `cpu_cores` is the host's physical cores, not
its SMT threads, because busy guest vCPUs per physical core is what
`HostCPUSaturated` divides and what the typing latency follows;
`sum(rate(repose_host_guest_cpu_seconds_total[5m]))` is the numerator.
`load1` is the same number the heartbeat puts in the `hosts.load1` column,
exported because Prometheus scrapes exporters, not the database.

The guest freeze (DECISIONS I-652, I-796): a freeze that leaves a running
guest unfrozen costs that guest everything it was running, because a host
restart then boots it cold. Every path that does so counts
`repose_host_guest_freeze_unfrozen_total{reason}` and logs at Error with
the event `guest_freeze`. `reason` is `estimate` (the snapshot would have
outlasted `repose.host.freeze.deadline` at the host's
`writeRateMBs`), `deadline` (the deadline passed before the snapshot
started), `no_room` (the thin pool), `failed`, `no_key` (Key Vault) or
`busy` (another freeze or thaw held the lock). `GuestLeftUnfrozen` reads
it over 6 hours and names the reason; the runbook's section is the
arithmetic to check before raising the deadline. The
`guests frozen` line carries `frozen`, `unfrozen`, `guests` and
`duration_ms`, which is what says whether the deadline was the limit.

`repose_host_guest_thaw_discarded_total{reason}` is the other half of the
same loss: a guest the freeze saved whose image the next start could not
give back cold boots with its agents gone exactly as an unfrozen one
does. `reason` is `incomplete`, `too_old`, `changed`, `no_key`, `failed`
or `window`; `GuestImageDiscarded` reads it and the event is
`guest_thaw`. A guest that stopped, moved host or came back running on
its own is not counted: nothing was owed to it.

Both are *running totals*, not what one hostd saw. A shutdown freeze's
counter is never scraped, because the host goes down behind it, and a
thaw's is raised before the metrics listener is up, so either one counted
per process would publish the same number after every reboot -- and
`increase()` reads a repeat as no rise, which is how the same guest lost
at every restart alerts once and then never again. hostd keeps the totals
beside its freeze records (`/var/lib/repose/hostd/freeze/.tally.json`)
and publishes them at each start, so the series only ever rises.

## The launch numbers

Liveness was instrumented and margin was not: before I-800 no dashboard
and no alert read `repose_api_seats_total`, and no revenue figure existed
anywhere. Three places answer the money questions now.

1. Grafana "Billing", row **The money**. MRR and what is in trial; seat
   **fill** (`seats_held / seats_total`, the margin number: ladder C
   clears an AX162-2 at 22 of its 30 Solo seats, so 75 percent is break
   even) and seat **use** (the memory actually running over the memory the
   sold seats may run, which is capacity bought and idle); and the three
   seat lines, delivered, sold and running.
2. `ops/sql/launch.sql` (I-804). Twelve queries for what cannot be a
   Prometheus series because it is per-account: signups and how many ever
   ran a machine, the trial funnel by plan, activation in the product's own
   terms, the subscribers who have gone quiet, cohort retention, the
   founding offer's burn, the plan mix with its waiting downgrades, MRR
   that left, the dunning list, the waitlist's throughput and what it
   wasted, disk against the plan, and the ops still open. Run them with
   `psql "$DATABASE_URL" -f ops/sql/launch.sql`, as `grafana_ro`.
3. The daily digest (I-810): `ops/digest/digest.py`, a timer on the
   personal server, one Chat message at 08:00 with the eight numbers worth
   reading every day in the launch window. It reuses calert's webhook and
   needs no new secret. A source that is down prints `?` and the rest of
   the message still goes.

Activation is the one of these the product is sold on: `usage_hours` keeps
`attended_seconds` and `unattended_agent_seconds` per project-hour (I-806),
so "the agent kept working after the laptop closed" is a number. It cannot
be backfilled -- `meter_samples` is kept 90 days and `usage_hours` for
ever -- which is why it shipped before launch rather than after.

Billing (DECISIONS I-289, I-604): `repose_api_billing_webhook_total{kind,result}`
counts Polar webhook deliveries (`ok`, `duplicate`, `ignored` for an
event about a customer or product repose does not know (I-678),
`bad_signature`, `error`), `repose_api_billing_overage_charges_total{result}` the egress
overage events sent to Polar's meter, `repose_api_billing_gate_refused_total{reason,plan}` the
`payment_required` refusals (the plan since I-802, which separates an
upgrade from a plan shape that is wrong), `repose_api_billing_subscriptions_total{plan,status}`
the subscription events applied and `repose_api_billing_stops_total{reason}`
the machines the api stopped for billing. The log lines are
`webhook_received` (kind, result), `overage_charged` (user_id, result,
cents, gb), `gate_refused` (user_id, reason, plan) and `disk_over_plan`
(user_id, plan; once a period, with its email, I-585): never the body of a
webhook, an email, a card or an amount a tenant typed.

`repose_api_subscriptions{plan,status}` is the live subscriptions and
`repose_api_mrr_cents{status}` the plan revenue a month they stand for
(I-802). Both are set from the minute tick on every api replica and take
no lock, so read them with `max()`, never `sum()`: the scrape lands on one
replica of two. MRR is the api's reading of the `subscriptions` table at
the plan table's prices, with the founding discount taken off while it
runs; Polar is the ledger of what was actually charged, and the two are
reconciled by hand. It is right only while one list price per plan is
live, because no price is stored per subscription
(`proposals/2026-10-11-price-level.md` §6).

The fleet as a whole: `repose_api_seats_total` and `repose_api_seats_held`
are the seats (8 GB running at once each, DECISIONS I-290; per ready host
the smaller of its 8 GB blocks and what its cores can serve, I-739,
I-784) and those held by live subscriptions and unexpired invitations; `repose_api_waitlist_waiting`
is the users waiting for one, `repose_api_waitlist_joined_total`,
`_invited_total`, `_converted_total` and `_expired_total` the traffic
through the list. The api logs `waitlist_join` (user_id, position),
`waitlist_invite` (count) and `waitlist_expire` (count), never an email
address. `repose-admin seats` and `repose-admin waitlist list` name them.

Named secrets: at start every api process rewrites the secret rows still
bound to their name alone in the project-bound form (DECISIONS I-474) and
logs `secrets_reseal` (resealed, refused, failed, pass) after each pass,
`secrets_reseal_fail` (code, err; and resealed, refused, failed when the
pass got as far as the rows) before a retry, and at the end one of two
lines. `secrets_name_only_none` (refused): a pass at least 15 minutes
after start found no row left to rewrite and none it could not read,
which is the signal that step 3 of I-433 (dropping the name-only read
path) can ship. `secrets_reseal_incomplete` (failed, refused, warn): two
passes in a row skipped the same number of rows because Key Vault would
not unwrap their data key (a disabled or purged key version, a corrupt
`dek_wrapped`); step 3 waits until those rows are repaired or deleted and
an api restart logs `secrets_name_only_none`. `code` is
`key_service_unavailable` when Key Vault does not answer
`CurrentVersion` or three distinct data keys in a row fail to unwrap, and
`db` when Postgres fails, including when the pass cannot take its lock.
`refused` counts rows left as they are: name-only rows under the
platform's data key outside the platform project, and rows that open in
neither form. `failed` counts rows skipped because their data key did not
unwrap. No line carries a name, a project or a value.

## Looking for abuse

Grafana "Abuse": fleet-wide top `comm` by CPU over 24 hours, top projects
by egress, guests at 100 percent CPU with zero sessions for over 24
hours, blocked outbound attempts by reason, and the api's automatic stops.
A miner is a `comm` you do not recognise at the top of the first
panel. Confirm with `repose-admin exec <id> -- ps -o comm,pcpu --sort
-pcpu | head` (audited), then the runbook's "Suspend a user".

Some of it is automatic (DECISIONS I-238..I-240). The host drops a
guest's tcp 25, the mining-pool ports and new flows over 200 a second,
and counts each per guest: `repose_host_egress_blocked_total{reason}`,
`repose_host_egress_blocked_guests{reason}` (the EgressBlocked alert), and
an `egress_blocked` hostd log line naming the guest, never an address. A
sample naming a known miner stops the guest with a snapshot
(`repose_api_abuse_stops_total`, MinerStopped, `abuse_stop` in the api's
log, `repose-admin abuse list`); a third stop in 24 hours holds the
project until `repose-admin abuse clear`. Six hours at full CPU with no
session, tmux client or agent is BusyUnattended. The user is never
suspended automatically.

## Log field rules

Every line: `ts`, `level`, `component`, `event`, `msg`. Context when it
exists: `request_id`, `command_id`, `op_id`, `project_id`, `guest_id`,
`host_id`, `user_id`, and bounded enums (`state`, `reason`, `kind`,
`class`, `result`), counts, byte sizes, durations in milliseconds.

**Never in a log field, a metric label, a trace attribute, or a build log
line stored by us:**

- prompts, agent output, terminal contents, anything typed in a guest
- process command-line arguments or environment variables (process
  *names* are fine, that is the documented sample)
- file paths inside a guest (`/home/dev/todo-app/src/secret.ts` is a
  tenant's business)
- secret names together with values; values ever
- tokens, JWTs, refresh tokens, certificate bodies, private keys, join
  tokens
- user email, GitHub login, handle (use `user_id`)
- git remote URLs (they can carry embedded tokens); log `project_id`
- user IP addresses and user agents (the gateway may keep a per-IP
  counter in memory for rate limiting; it does not log the address)
- card details of any kind, including last four (Polar holds the card; the api never sees it)

`internal/obs` enforces the rules in code (in three packages, so that guestd links a logger and not an exporter:
DECISIONS I-59):

- Every logger comes from `obs.NewLogger`, which puts `component` on the line
  itself, so a call site cannot omit it, and replaces the value of any field
  named `token`, `secret`, `password`, `authorization`, `cert`, `key`,
  `email`, `handle`, `remote_url`, `prompt`, `args`, `argv`, `env`,
  `cmdline`, `command_line` or `user_agent` with `[redacted]`. Matching is on
  the exact name, so `cert_serial`, `key_id` and `token_used` still read.
- Every metric comes from a registry `internal/obs/metrics` built, which
  refuses a name outside `repose_` and any label outside the low-cardinality
  list, at registration: a series labelled by `project_id` stops the binary
  at startup instead of filling Prometheus.
- `internal/obs/obslint`, run by a unit test over `cmd/` and `internal/`,
  refuses the standard library's `log`, a logger or registry built outside
  `obs`, a log call that does not name its `event`, and a never-log field
  name at a call site, including the wider list (`path`, `output`,
  `transcript`, `ip`, `jwt` and the rest) that redaction does not cover.

None of that replaces the reviewer: redaction covers only the names listed
above. The privacy policy promises the process-sample boundary in plain
words, so a log line that crosses it breaks that promise, and the line
stays in the logs after the incident is fixed.

## Retention

| Store | Keeps |
|---|---|
| Loki console logs | 30 days |
| Loki component logs | 90 days |
| Prometheus | 90 days |
| `meter_samples` | 90 days (monthly partitions) |
| `proc_samples` | 30 days |
| `usage_hours`, `events`, `audit_log`, `build_logs` | indefinite (`build_logs` trimmed to the last 20 ops per project) |

Loki's retention is `ops/loki/retention.yaml` and does nothing unless the
compactor runs with `retention_enabled`. Prometheus's is the flag
`--storage.tsdb.retention.time=90d`, not a config key. The two Postgres
sample tables are partitioned by month and dropped by
`repose_partitions_maintain()` (`ops/sql/partitions.sql`), which the api
calls hourly and logs `partition_drop_fail` when it fails; because a whole
month has to age out, the real retention is 90 or 30 days plus up to a month.

## Alerts

The rules in `ops/alerts.yaml`, routed by Alertmanager
(`ops/alertmanager/repose-route.yaml`) through calert to the owner's Google
Chat space. A `page` goes out after 20 s and repeats hourly; the rest wait for
their group and repeat every 12 hours. The always-firing `Watchdog` goes
only to an Uptime Kuma push monitor, which posts to the same space when the
pushes stop, so a dead Prometheus or Alertmanager is heard too, and a
dead personal server as long as Kuma runs somewhere else. Azure's own view of the three VMs (a platform reboot, planned
maintenance, a VM not running) is emailed by Azure Monitor and needs none
of this (RUNBOOK "Azure alert", DECISIONS I-648). Each alert name is a heading in `RUNBOOK.md`, and
`ops/check.sh` fails if one is missing. Each also has a `promtool` test
case, because an alert with a wrong expression looks like a quiet system until
the incident it was written for. Silence with `amtool silence add alertname=...
--duration 2h --comment "..."` and say why in the comment; a silence with
no comment is deleted by a nightly job.
