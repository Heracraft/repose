-- The dashboard's browser view (DECISIONS I-1216): a ticket that
-- authenticates one connect to a project's desktop, and the wake rule
-- that stops a re-mint loop from pumping resumes.
--
-- The ticket is held as sha256 and nothing else -- one home, not the two
-- a webhook route needs (0036): the user never sees a view ticket and
-- never needs it printed again, so there is no reason to keep a
-- decryptable copy, and a dump of this table gives no working ticket.
-- `id` is what the two log lines carry (`ticket_id`): the ticket and its
-- hash are both never-log (docs/ops/OBSERVABILITY.md).
--
-- `bytes` is the session budget. It lives on the row rather than in the
-- api's memory because the attack it bounds is reconnecting: a
-- per-connection counter hands out a fresh 2 GiB on every reconnect, and
-- a 15-minute ticket with the bytes already spent on it does not.
create table browser_view_tickets (
  id           uuid primary key,
  hash         bytea not null,
  project_id   uuid not null references projects(id) on delete cascade,
  user_id      uuid not null references users(id) on delete cascade,
  created_at   timestamptz not null default now(),
  expires_at   timestamptz not null,
  last_used_at timestamptz,
  bytes        bigint not null default 0
);
create unique index browser_view_tickets_hash on browser_view_tickets (hash);
create index browser_view_tickets_project on browser_view_tickets (project_id);

-- When a mint last woke a resting machine for the view, one row per
-- project: the "at most once per 10 minutes" rule has to survive an api
-- restart, so it is a row and not a counter in memory (the same reason
-- webhook_starts is one, 0036).
create table browser_view_wakes (
  project_id uuid primary key references projects(id) on delete cascade,
  at         timestamptz not null
);
