# Database schema

Postgres 16. Migrations in `internal/db/migrations/` numbered
`NNNN_name.up.sql` / `.down.sql`, applied by `api` at start (`--migrate`) and
by `repose-admin db migrate`. Every table has `created_at timestamptz not
null default now()`; mutable tables also have `updated_at` maintained by a
trigger. Ids are `uuid` (UUIDv7 generated in Go). Money is `bigint` cents.

```sql
users        (id pk, logto_sub text unique, handle text unique, email text,
              github_login text, tz text, notify_email bool, ntfy_url text,
              billing_customer_id text unique,  -- Polar's customer id; was stripe_customer_id (0008, I-289),
                                               -- then paddle_customer_id (0020, I-604)
              billing_anchor timestamptz,   -- signup; unused since I-289, the period is the subscription's
              past_due_since timestamptz,   -- first failed payment; the 3-day stop reads it
              billing_status text,  -- none|trial|active|past_due|suspended|exempt (I-16, I-289): a
                                    -- projection of subscriptions.status; 'none' is no plan yet
              has_card bool, trial_credit_cents bigint, project_limit int, xl_limit int,
              suspended_at, suspended_reason text, cancelled_at, deleted_at)
              -- project_limit raises the account's project cap above 100 and
              -- never lowers it (I-569)
              -- trial_credit_cents is a projection of credit_ledger maintained by a
              -- trigger inside the same transaction as the ledger row; the balance of
              -- record is sum(credit_ledger.cents) (09 §5.3)

hosts        (id pk, name text unique, hostname text, sku text, provider text, region text,
              mem_bytes bigint, vcpus int, pool_bytes bigint, guest_cidr cidr,  -- pool_bytes: registered, then the newest heartbeat's (I-586)
              wg_pubkey text, wg_ip inet, state text,  -- registering|ready|draining|unreachable|retired|lost
              draining bool, free_mem_bytes bigint, pool_free_bytes bigint, load1 float, running_guests int,
              last_heartbeat_at, cert_serial text, prev_cert_serial text (0012, I-432), cert_expires_at,
              nixos_system text, ch_version text,
              join_token_hash text, join_token_expires_at, registered_at)   -- the token itself is never stored

projects     (id pk, user_id fk, name text, slug text, remote_url text,
              class text, state text, host_id fk null, guest_id uuid null,
              guest_ip inet null, vsock_cid int null,
              agent_default text, hold_base_updates bool, base_version text,
              config_revision_id uuid null, volume_bytes bigint,
              tz text, host_unreachable bool, last_error text,
              started_at, stopped_at, destroyed_at,
              expires_at null,  -- a temporary project's end (0010, I-347)
              personal_opt_out bool,  -- machine.nix kept off this project (0015, I-490)
              multiplexer text not null default 'tmux'
                check (multiplexer in ('tmux','herdr')),  -- what the next start runs (0017, I-502)
              disk_held_bytes bigint null, disk_held_at timestamptz null,
                -- what the volume holds, which the plan's disk counts (0019, I-585):
                -- the newest sample's thin volume allocated blocks, or, with
                -- disk_held_at null, the estimate a new project starts with
                -- (1 GB; a restore's or fork's source figure); both null
                -- counts volume_bytes
              unique (user_id, slug) where destroyed_at is null,
              unique (user_id, remote_url) where destroyed_at is null)

host_reservations (view: host_id, reserved_bytes, guests)  -- class RAM summed over projects placed on the host in a state that holds memory

config_revisions (id pk, project_id fk, fragment text, menu jsonb null,
              base_version text, status text,  -- building|built|applied|failed
              system_closure text null, closure_bytes bigint null,
              kernel_changed bool, reboot_required bool,
              error text null, fragment_line int null, built_at, applied_at,
              personal text,  -- the machine.nix text it is built with, '' for none (0015, I-490)
              personal_revision_id fk null, personal_opt_out bool, personal_line int null)

personal_revisions (id pk, user_id fk, fragment text,  -- one save of an account's machine.nix (0015, I-490);
              source text, created_at)                  -- the newest row is current, '' means none; source cli|dashboard

ops          (id pk, project_id fk null, kind text, state text,  -- pending|running|done|error
              step int, command_id uuid unique, host_id fk,
              params jsonb,          -- {phases: [...], ...} fixed at enqueue (I-42)
              command_result jsonb,  -- the hostd Result for the current phase
              result jsonb, error jsonb null, revision_id uuid, snapshot_id uuid, audit_id uuid,
              reboot_required bool, sent_at, started_at, finished_at)

build_logs   (op_id fk, seq bigint, line text, ts timestamptz default now(),  -- ts: 0009, I-322
              primary key (op_id, seq))

secrets      (id pk, project_id fk, name text, ciphertext bytea,
              dek_wrapped bytea, kv_key_version text, unique (project_id, name))
              -- the three reserved names of I-10 hold the guest's sshd material (I-42);
              -- the platform pseudo-project 00000000-0000-7000-8000-000000000000 holds the CA keys

certificates (serial bigint pk, user_id fk, project_ids uuid[], public_key_fp text,
              key_id text, kind text,  -- user|gateway|host
              issued_at, expires_at, revoked_at)

snapshots    (id pk, project_id fk, host_id fk, blob_path text unique, bytes bigint,
              reason text,  -- scheduled|stop|manual
              taken_at, expires_at, deleted_at, restoring_op_id uuid null,  -- set while a restore reads it; expiry skips it
              sha256 text null)  -- hex SHA-256 of the blob, checked on restore; null before 0013 (I-462)

events       (id pk, project_id fk null, user_id fk null,  -- one of them is set (0007, I-269):
              -- user_id alone for an account event (waitlist_invited, the plan emails of I-291)
              ts timestamptz, ts_second bigint, kind text, agent text null,
              tmux_window text null, summary text, source text,  -- host|http|api
              skew_seconds int null, host_event_id text unique,
              delivered jsonb)  -- {email: ts|"error: ..."|"failed: ...", ntfy: ...}
              -- unique (project_id, agent, kind, ts_second) for kinds completed|needs_input|error

events_outbox (event_id fk, channel text, attempts int, next_at, last_error text,
              primary key (event_id, channel))

meter_samples (ts timestamptz, project_id, host_id, state text, class text,
              cpu_ns bigint, mem_rss bigint, net_tx bigint, net_rx bigint,
              disk_alloc bigint, disk_used bigint, ssh_sessions int,
              tmux_clients int, agents jsonb, docker_containers int, guestd_ok bool,
              cpu_pressure_us bigint, host_cpu_wait_us bigint, mem_used bigint,  -- 0014, I-493; 0 before it
              root_used bigint, root_size bigint,  -- 0018, I-567: the guest's root filesystem; 0 before it
              primary key (project_id, ts))  -- partitioned by month, 90-day retention

proc_samples (ts, project_id, comm text, cpu_ns bigint, rss bigint,
              primary key (project_id, ts, comm))  -- partitioned by month, 30-day retention

usage_hours  (project_id fk, hour timestamptz, class text, running_seconds int,
              gb_alloc bigint, egress_bytes bigint, cost_cents bigint,
              guest_cents bigint, storage_cents bigint, egress_cents bigint, storage_remainder bigint,
              period_start timestamptz, period_end timestamptz,  -- the billing period the hour falls in
              credit_cents bigint,          -- taken from credit_ledger; cost_cents - credit_cents is pushed
              price_version text,           -- PRICING.md "Changing prices"; rows are never repriced
              stripe_usage_record_id text null,  -- 'usage:<project_id>:<hour>', or 'exempt' (I-16)
              gap bool, primary key (project_id, hour))

credit_ledger (id pk, user_id fk, cents bigint, reason text, ref text, created_at)
              -- reason: trial|usage|goodwill|refund|adjustment; ref is
              -- '<project_id>:<hour>' for a usage debit, unique among reason='usage'

subscriptions (id text pk,  -- the provider's subscription id (0008, I-289; Polar's since 0020, I-604)
              user_id fk, customer_id text,  -- was paddle_customer_id (0020)
              provider text not null default 'polar',  -- paddle|polar (0020, I-604); rows before it are 'paddle'
                                       -- 0020 ends every live 'paddle' row and sets its account's billing_status to none
              plan text,  -- solo|plus|pro (0011, I-362)
              status text,  -- trialing|active|past_due|paused|canceled; at most one live per user
              seats int, period_start, period_end, next_billed_at, trial_end,
              cancel_at null,          -- a scheduled cancellation takes effect here
              scheduled_plan text null, -- a downgrade waiting for period_end
              overage_charged_for timestamptz null,  -- period_start of the last period whose egress line was sent
              intro boolean,           -- carries the introductory discount (0016, I-497)
              intro_until timestamptz null,  -- the trial's end (or the start) plus the intro months (I-604)
              created_at, updated_at)

billing_events (id text pk, type text, occurred_at, received_at, processed_at, error text)
              -- was paddle_events (0020, I-604); the webhook-id is the dedupe key for replays

overage_charges (subscription_id fk, period_start, egress_gb numeric, cents bigint,
              sent_ref text null, created_at, primary key (subscription_id, period_start))
              -- one egress line per period, written before it is sent (I-289); sent_ref,
              -- was paddle_transaction_id (0020, I-604), is the external id
              -- 'overage:<subscription>:<period start unix>' Polar accepted the meter event under

invoices     (id pk, user_id fk, stripe_invoice_id text unique, period_start,
              period_end, total_cents, status text)  -- unused since I-289; invoices are read from Polar's orders (I-604)

audit_log    (id pk, ts, actor text, action text, target text, detail jsonb)
              -- every Exec, every admin action, every cert issue and revoke

abuse_events (id pk, project_id fk, user_id fk, ts, kind text,  -- miner
              detail jsonb,        -- {"process": "<name>"}: a process name, never arguments
              op_id uuid null,     -- the stop op
              hold bool,           -- set on the stop that makes three uncleared ones within 24 h
              cleared_at null, cleared_by text null)
              -- one row per guest the api stopped by itself (0005, I-239); a project
              -- with an uncleared held row cannot start until `repose-admin abuse clear`

questions    (id pk,               -- chosen by guestd (UUIDv7); a re-announcement inserts nothing
              project_id fk, guest_id uuid, event_id uuid,  -- the agent_question event that notified the owner
              agent text, tmux_window text null, text text, options text[],  -- 0..3
              state text,          -- pending|answered|cancelled|expired|no_channel
              answer text null, answered_via text null,  -- dashboard|cli|ntfy|email
              expires_at, answered_at null,
              delivered_at null, delivery text null,  -- ok|gone|given_up|guest: the close reached the guest, or why not
              deliver_command_id uuid null, deliver_attempts int, deliver_next_at null)
              -- repose-ask (0006, I-244/I-245); text and answer are tenant content stored
              -- like events.summary: plain, shown to the owner, never logged

waitlist     (user_id pk fk, joined_at, invited_at null, hold_until null,
              invited_by text null,    -- auto | the operator's audit actor
              converted_at null,       -- the invited user's subscription arrived
              expired_invites int)     -- holds that ran out; each moves joined_at to now
              -- the seats waitlist (0007 I-269, 0008 I-290): a row is kept after
              -- conversion for the count; invited_at with hold_until in the
              -- future holds one seat

base_versions (version text pk, nix_rev text, changelog text, released_at,
              security bool)

host_sessions (host_id pk, replica_id text, since)          -- which api replica holds the host's stream
gateway_sessions (project_id fk, cert_serial bigint, session_id text default '', opened_at,
              primary key (project_id, cert_serial, session_id))   -- one row per gateway relay (0004, I-176)
settings     (key text pk, value text)                       -- edge_wg_pubkey, edge_wg_endpoint (repose-admin edge init)
schema_migrations (version int pk, name text, applied_at)
```

Indexes: `projects(user_id)`, `projects(host_id) where state in ('running',
'starting')`, `projects(expires_at) where expires_at is not null and
destroyed_at is null` (0010), `events(project_id, ts desc)`, `snapshots(project_id, taken_at
desc)`, `certificates(user_id) where revoked_at is null`, `usage_hours(hour)`,
`usage_hours(project_id, period_start)`, `abuse_events(project_id, ts desc)`,
`questions(project_id, created_at desc)`, `questions(expires_at) where state
= 'pending'`, `questions(deliver_next_at) where state <> 'pending' and
delivered_at is null`, `questions(deliver_command_id)`, `usage_hours(hour) where
stripe_usage_record_id is null` (unused since I-289), `credit_ledger(user_id, created_at)`,
`subscriptions(user_id) where status in (live) unique`, `subscriptions(user_id,
created_at desc)`, `subscriptions(next_billed_at) where status in (live)`,
`billing_events(received_at desc)` (`billing_events_received`, 0020),
`ops(state) where state in ('pending','running')`, `events_outbox(next_at)`,
`events(user_id, ts desc) where user_id is not null`, `waitlist(joined_at,
user_id) where invited_at is null`, `waitlist(hold_until) where invited_at
is not null and converted_at is null`.

Migrations `0001_init`, `0002_outbox_sessions_settings`, `0003_billing`,
`0004_gateway_session_id`, `0005_abuse_events`, `0006_questions`, `0007_waitlist`, `0008_plans`, `0009_build_log_ts`, `0010_project_expires_at`, `0011_plan_plus`, `0012_host_prev_cert_serial` and `0013_snapshot_sha256` create all of this; `repose-admin db migrate --down 1` reverts one. Partitions of the
sample tables are created for the current and next month at start and by
the daily job, which also drops partitions past retention.

Rules:

- No `delete` of `projects` rows; `destroyed_at` is set and the row stays for
  usage history. `users.deleted_at` likewise.
- `meter_samples` and `proc_samples` are append-only and never joined to
  from request paths; the hourly rollup reads them once. The one request
  path that reads them is `GET /projects/:id/samples` (I-492): one
  project, at most seven days, a range scan on each primary key. Both are partitioned
  by month: the api creates the current and next month's partitions at start
  and in its daily job, which also drops the ones past retention and logs
  `partition_drop_fail` with `repose_api_partition_drop_fail_total` when it
  cannot. `ops/sql/partitions.sql` is the same maintenance as SQL functions,
  for an operator with psql and for the dashboard tests; either way a
  partition is dropped only once its whole month is past the retention period,
  so the effective retention is 90 or 30 days plus up to a month.
- Secrets values never appear in `audit_log.detail` or anywhere but
  `secrets.ciphertext`.
- `credit_ledger` is append-only. A usage debit that has to change is
  corrected by an `adjustment` row referring to the same `ref`, never by
  updating or deleting the original; `usage_hours` is the ledger of record
  for what was used and is corrected the same way, with a `credit` row
  rather than an edit (`ops/RUNBOOK.md` "BillingMismatch").
