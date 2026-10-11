-- Running machines start again after a host reboot (DECISIONS I-645).
-- hostd marks each guest a host restart took down, and Hello carries the
-- mark; the api's OnHello inserts one row per (project, boot), and the
-- resume tick decides each row once, in its own transaction with the
-- start it enqueues. host_lost_at is the host's last heartbeat before
-- the Hello: a stop, destroy, restore or resize asked for after it means
-- the owner wanted the machine down. hello_state is the guest's state in
-- that Hello, which the project takes when an op that was open then
-- ends. outcome is pending until decided; starting while the start op
-- runs; then one of the closed outcomes.
create table guest_resumes (
  id uuid primary key,
  project_id uuid not null references projects(id) on delete cascade,
  user_id uuid not null references users(id) on delete cascade,
  host_id uuid not null references hosts(id) on delete cascade,
  guest_id uuid not null,
  boot_id text not null,
  host_boot_at timestamptz,
  interrupted_at timestamptz not null,
  host_lost_at timestamptz,
  hello_state text not null,
  outcome text not null default 'pending' check (outcome in
    ('pending', 'starting', 'started', 'already_running', 'user_stopped', 'destroyed', 'temp_expired', 'window_passed', 'refused', 'failed')),
  reason text,
  op_id uuid,
  attempts integer not null default 0,
  created_at timestamptz not null default now(),
  decided_at timestamptz,
  unique (project_id, boot_id)
);
create index guest_resumes_undecided on guest_resumes (host_id, created_at) where decided_at is null;

-- One machines_restarted notice per (user, host, boot), however many
-- times the tick sees the group settle (D5: no second notice).
create table resume_notices (
  user_id uuid not null references users(id) on delete cascade,
  host_id uuid not null references hosts(id) on delete cascade,
  boot_id text not null,
  event_id uuid,
  sent_at timestamptz not null default now(),
  primary key (user_id, host_id, boot_id)
);
