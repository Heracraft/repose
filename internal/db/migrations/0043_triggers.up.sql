-- Triggers (DECISIONS I-1248..I-1262): an event or a time starts an agent
-- session on the project's machine.
--
-- What is NOT in these tables is the point of I-1248. A trigger's prompt
-- is a file in the machine and this row holds its path; a payload's bytes
-- are a file in the machine and this row holds only how many there were.
-- The privacy policy's sentence -- repose never records the prompts you
-- give an agent -- is kept by the schema and not by a convention.
--
-- What is here as tenant content, declared so in db-schema.md beside
-- events.summary: trigger_runs.summary (the agent's own
-- `repose-notify --done` words) and the one free-text reason, a gate's
-- last line of stderr. Both are shown to the owner and never logged.
create table triggers (
  id           uuid primary key,
  project_id   uuid not null references projects(id) on delete cascade,
  name         text not null,
  kind         text not null check (kind in ('route', 'schedule')),

  -- A route trigger fires on a delivery to this route, whose target the
  -- api points at the guest's receiver (I-1250). The route is removed
  -- with the trigger unless the user made it first or another trigger
  -- uses it, so this is `on delete set null` and not cascade: a route
  -- deleted by hand must leave a trigger that says what it was.
  route_id     uuid references webhook_routes(id) on delete set null,

  -- A schedule trigger's clock (I-1252). `spec` is the canonical
  -- spelling internal/triggerspec stores, `zone` an IANA name.
  -- `anchor_at` is what `every D` counts from, so the clock's
  -- one-minute spread of a fleet's 07:00 cannot drift it; `next_at` is
  -- the claim point, and the tick writes the following fire in the same
  -- statement that claims this one.
  spec         text,
  zone         text,
  anchor_at    timestamptz,
  next_at      timestamptz,

  prompt_path  text not null,
  workdir      text not null default 'checkout',
  agent        text,
  timeout_s    integer not null default 3600 check (timeout_s between 0 and 86400),
  concurrency  integer not null default 1 check (concurrency between 1 and 3),
  gate         text,
  dedupe       text,
  notify       text not null default 'failed' check (notify in ('failed', 'never')),
  enabled      boolean not null default true,
  allow_start  boolean not null default false,
  stop_after   boolean not null default false,
  catch_up     boolean not null default false,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),

  unique (project_id, name),
  -- A schedule has a spec and a zone; a route trigger has a route. The
  -- check is here because the clock reads `next_at` of every enabled row
  -- and a row with half a schedule would be a fire that never lands.
  constraint triggers_kind_fields check (
    (kind = 'schedule' and spec is not null and zone is not null)
    or (kind = 'route' and spec is null)
  )
);
create index triggers_due on triggers (next_at) where enabled and next_at is not null;
create index triggers_route on triggers (route_id) where route_id is not null;
create trigger triggers_updated_at before update on triggers for each row execute function set_updated_at();

-- One run. `pending` and `sending` are the hand-off between the api's two
-- processes, which is I-1162's lesson from `say`: only API_MODE=grpc
-- holds a host's stream, so the clock tick and the http process both
-- write a row and a worker in the grpc process claims it. Every surface
-- draws both as `queued`.
--
-- A run a delivery started arrives from the guest already `working`, and
-- its row is written by the api when the TriggerRun notification lands,
-- so this table holds runs nobody asked the api for.
create table trigger_runs (
  id            uuid primary key,
  project_id    uuid not null references projects(id) on delete cascade,
  -- A run outlives the trigger being removed: `repose triggers runs`
  -- after an `rm` still shows what the thing did, and the name is kept
  -- on the row for exactly that.
  trigger_id    uuid references triggers(id) on delete set null,
  trigger_name  text not null,
  source        text not null check (source in ('route', 'clock', 'hand')),
  state         text not null check (state in ('pending', 'sending', 'working', 'done', 'failed', 'timeout', 'skipped')),
  reason        text,
  summary       text,
  link_url      text,
  link_label    text,
  tmux_window   text,
  dedupe_key    text,
  delivery_id   uuid,
  payload_bytes bigint not null default 0,
  -- A refusal the edge reported coalesces: one row per trigger, reason
  -- and hour, with the count on it. 600 deliveries to a stopped machine
  -- would otherwise evict every real run from the 20 a trigger keeps.
  skips         integer not null default 1,
  -- started_machine is set on a run whose own fire started the machine,
  -- which is the only run `--stop-after` may stop it after.
  started_machine boolean not null default false,
  -- What this fire asked for. It is on the row because the row IS the
  -- hand-off between the api's two processes (I-1162): the http one
  -- writes it and the grpc one sends it, so anything held in the
  -- asking process's memory would be lost between them.
  payload_path  text,
  dry_run       boolean not null default false,
  payload_run   uuid,
  command_id    uuid,
  started_at    timestamptz,
  finished_at   timestamptz,
  created_at    timestamptz not null default now()
);
create index trigger_runs_trigger on trigger_runs (trigger_id, created_at desc);
create index trigger_runs_project on trigger_runs (project_id, created_at desc);
create index trigger_runs_handoff on trigger_runs (created_at) where state in ('pending', 'sending');
-- The unused notice reads this (I-1260): a run in the window is use, and
-- a 14-second run falls between two of I-262's minute samples, so the
-- notice cannot infer it from the sampler.
create index trigger_runs_use on trigger_runs (project_id, finished_at desc) where state <> 'skipped';
create unique index trigger_runs_command on trigger_runs (command_id) where command_id is not null;
-- The coalescing lookup: the open skip row of this trigger, reason and
-- hour (I-1254).
create index trigger_runs_coalesce on trigger_runs (trigger_id, reason, created_at desc) where state = 'skipped';
