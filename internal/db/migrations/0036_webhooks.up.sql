-- Public webhook routes into a project's machine (DECISIONS I-1110).
--
-- A route is per-project server state, as keep_awake is (I-820): the edge
-- must know it whether or not a laptop is open, and the api is the only
-- place both the CLI and the dashboard write. The token is held twice --
-- sha256 for the edge's lookup, ciphertext under the account's data key
-- so `repose webhooks url` can print it again -- which keeps it in the
-- two homes "Secrets have three homes" allows and gives a copy of the
-- edge's memory no working URL.
create table webhook_routes (
  id             uuid primary key,
  project_id     uuid not null references projects(id) on delete cascade,
  name           text not null,
  port           integer not null,
  path           text not null,
  allow_start    boolean not null default false,
  token_sha256   bytea not null,
  ciphertext     bytea not null,
  dek_wrapped    bytea not null,
  kv_key_version text not null,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  unique (project_id, name)
);
create unique index webhook_routes_token on webhook_routes (token_sha256);
create trigger webhook_routes_updated_at before update on webhook_routes for each row execute function set_updated_at();

-- A token that existed and was rotated or removed. The edge answers 410
-- for 30 days and 404 after it, so a sender that obeys the 410 lets go
-- of a URL repose can no longer route. A destroyed project needs no row
-- here: its routes are tombstoned by the destroy.
create table webhook_tombstones (
  token_sha256 bytea primary key,
  project_id   uuid not null,
  at           timestamptz not null default now()
);
create index webhook_tombstones_at on webhook_tombstones (at);

-- The last 20 deliveries per route, for `repose webhooks` and the
-- dashboard: never the path (it holds the token), the query, a header or
-- a body byte (docs/ops/OBSERVABILITY.md).
create table webhook_deliveries (
  id          uuid primary key,
  route_id    uuid not null references webhook_routes(id) on delete cascade,
  at          timestamptz not null default now(),
  status      integer not null,
  outcome     text not null,
  duration_ms integer not null,
  bytes_in    bigint not null,
  bytes_out   bigint not null
);
create index webhook_deliveries_route_at on webhook_deliveries (route_id, at desc);

-- Reply bytes per project per hour, which the billing rollup adds to
-- egress (DECISIONS I-1120). Not read off webhook_deliveries: that keeps
-- the newest 20 a route for the user to look at, so an hour with a
-- thousand deliveries would bill twenty of them.
create table webhook_egress (
  project_id uuid not null references projects(id) on delete cascade,
  hour       timestamptz not null,
  bytes      bigint not null default 0,
  primary key (project_id, hour)
);

-- When a delivery last started a stopped machine, one row per project:
-- the "at most once per 10 minutes" of the --start rule has to survive an
-- api restart, so it is a row and not a counter in memory.
create table webhook_starts (
  project_id uuid primary key references projects(id) on delete cascade,
  at         timestamptz not null
);
