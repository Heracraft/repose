-- Messages the owner sent an agent with `repose say` (DECISIONS I-1144).
-- One row per message, whatever became of it, so "did it get there" has
-- an answer the api can give.
--
-- text is tenant content stored the way events.summary and
-- questions.text are: plain text, shown to the owner on the dashboard
-- and the CLI, and never written to a log line, a metric or a trace. It
-- is not a secret and lives nowhere a secret does.
--
-- state: delivered (the machine typed it into the agent's terminal),
-- waiting (the agent was mid-turn and it goes in at the end of the
-- turn), replaced (a later message for the same window took its place
-- before it went in), failed (the machine refused it, with why in
-- `error`). The machine is the authority on delivered and waiting: the
-- api stores what the SayToAgent command answered.
create table says (
  id           uuid primary key,
  project_id   uuid not null references projects(id) on delete cascade,
  user_id      uuid not null references users(id),
  tmux_window  text not null default '',  -- the window it went to, as the machine resolved it
  text         text not null,
  sender       text not null default '',  -- the account's name, as the agent is told it
  from_owner   boolean not null default true,
  state        text not null,
  error        text,                      -- a short reason on failed, never the machine's message
  command_id   uuid not null,             -- the SayToAgent command (idempotency)
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  constraint says_state check (state in ('delivered','waiting','replaced','failed'))
);
create index says_project on says(project_id, created_at desc);
create index says_waiting on says(project_id, tmux_window) where state = 'waiting';
create trigger says_updated_at before update on says for each row execute function set_updated_at();
