-- A subset of docs/interfaces/db-schema.md: the tables the dashboards query,
-- with the two partitioned ones partitioned.
--
-- This is a fixture, not a migration. Workstream 05 owns
-- internal/db/migrations/ and the real schema; until those exist there is
-- nothing to run a dashboard's SQL against, and a dashboard whose SQL has
-- never been executed is a dashboard that fails the first time somebody needs
-- it. ops/dev/pgcheck.sh loads this, fills it with synthetic samples, runs
-- every panel's query, and exercises the partition maintenance.
--
-- When 05's migrations land, this file's job shrinks to whatever they do not
-- cover, and pgcheck.sh should run against a migrated database instead.

create table if not exists abuse_events (
  id uuid primary key,
  project_id uuid not null,
  user_id uuid not null,
  ts timestamptz not null default now(),
  kind text not null,
  detail jsonb not null default '{}',
  op_id uuid,
  hold boolean not null default false,
  cleared_at timestamptz,
  cleared_by text
);

create table if not exists users (
  id uuid primary key,
  logto_sub text unique,
  handle text unique,
  email text,
  github_login text,
  tz text,
  notify_email bool default true,
  ntfy_url text,
  stripe_customer_id text unique,
  billing_status text default 'trial',
  trial_credit_cents bigint default 1000,
  project_limit int default 3,
  xl_limit int default 1,
  suspended_at timestamptz,
  cancelled_at timestamptz,
  deleted_at timestamptz,
  created_at timestamptz not null default now()
);

create table if not exists hosts (
  id uuid primary key,
  hostname text,
  sku text,
  provider text,
  region text,
  mem_bytes bigint,
  vcpus int,
  pool_bytes bigint,
  guest_cidr cidr,
  wg_pubkey text,
  wg_ip inet,
  state text,
  last_heartbeat_at timestamptz,
  cert_serial text,
  cert_expires_at timestamptz,
  nixos_system text,
  ch_version text,
  created_at timestamptz not null default now()
);

create table if not exists projects (
  id uuid primary key,
  user_id uuid not null references users(id),
  name text,
  slug text,
  remote_url text,
  class text,
  state text,
  host_id uuid references hosts(id),
  guest_id uuid,
  guest_ip inet,
  vsock_cid int,
  agent_default text,
  hold_base_updates bool default false,
  base_version text,
  config_revision_id uuid,
  volume_bytes bigint,
  tz text,
  multiplexer text not null default 'tmux',
  disk_held_bytes bigint,
  disk_held_at timestamptz,
  started_at timestamptz,
  stopped_at timestamptz,
  destroyed_at timestamptz,
  created_at timestamptz not null default now(),
  unique (user_id, slug)
);

create table if not exists snapshots (
  id uuid primary key,
  project_id uuid not null references projects(id),
  host_id uuid references hosts(id),
  blob_path text,
  bytes bigint,
  reason text,
  taken_at timestamptz,
  expires_at timestamptz,
  deleted_at timestamptz,
  created_at timestamptz not null default now()
);

-- The two that grow with time, partitioned by month so retention is a drop
-- rather than a delete (ops/sql/partitions.sql).
create table if not exists meter_samples (
  ts timestamptz not null,
  project_id uuid not null,
  host_id uuid,
  state text,
  class text,
  cpu_ns bigint,
  mem_rss bigint,
  net_tx bigint,
  net_rx bigint,
  disk_alloc bigint,
  disk_used bigint,
  ssh_sessions int,
  tmux_clients int,
  agents jsonb,
  docker_containers int,
  guestd_ok bool,
  cpu_pressure_us bigint,
  host_cpu_wait_us bigint,
  mem_used bigint,
  root_used bigint,
  root_size bigint,
  primary key (project_id, ts)
) partition by range (ts);

create table if not exists proc_samples (
  ts timestamptz not null,
  project_id uuid not null,
  comm text not null,
  cpu_ns bigint,
  rss bigint,
  primary key (project_id, ts, comm)
) partition by range (ts);

create table if not exists usage_hours (
  project_id uuid not null references projects(id),
  hour timestamptz not null,
  class text,
  running_seconds int,
  gb_alloc bigint,
  egress_bytes bigint,
  cost_cents bigint,
  stripe_usage_record_id text,
  primary key (project_id, hour)
);

create index if not exists usage_hours_hour on usage_hours (hour);
