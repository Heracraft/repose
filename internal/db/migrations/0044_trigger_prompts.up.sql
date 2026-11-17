-- 0044_trigger_prompts: the hand-off a prompt typed in the browser takes
-- between the api's two processes (DECISIONS I-1266).
--
-- `repose triggers add -p TEXT` writes the prompt into the machine over
-- SSH and sends the api a path; a browser has no SSH. The api runs as two
-- processes and only API_MODE=grpc holds a host's stream (I-87, I-163,
-- I-1162), so the http process that takes the POST cannot reach the
-- machine: anything it knows at request time and the grpc process needs
-- at send time has to be on a row, which is what `trigger_runs` already
-- is for a fire.
--
-- The table is unlogged and holds at most one row per trigger: the text
-- never reaches the WAL, a replica or a physical backup, the grpc process
-- deletes the row the moment the machine has it, and a sweeper deletes
-- any row older than ten minutes whether it was delivered or not. The
-- `triggers` row still holds a path and never the text, which is what
-- 0043's own comment promises.
create unlogged table trigger_prompts (
    project_id   uuid not null references projects(id) on delete cascade,
    trigger_name text not null,
    -- Where it goes in the machine, read by the guest exactly as the
    -- runner reads a trigger's prompt: relative to the workdir.
    workdir      text not null,
    prompt_path  text not null,
    -- The prompt. At most 64 KB, which is the runner's own cap.
    body         text not null,
    created_at   timestamptz not null default now(),
    primary key (project_id, trigger_name)
);

-- The sweeper reads by age across every project.
create index trigger_prompts_age on trigger_prompts (created_at);
