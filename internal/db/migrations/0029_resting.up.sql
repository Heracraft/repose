-- An idle machine rests: its vCPUs stop and its memory stays resident on
-- the host, so every process, socket and open file survives and a wake is
-- a resume (DECISIONS I-818). The state stays `running` -- a resting
-- minute is a running minute, which is what bills it (billing/rollup.go)
-- and what the plan's memory counts, since the memory is still held.
--
-- resting_since is when the machine's current rest began, null while it is
-- awake: the api writes it from hostd's GuestResting event and reconciles
-- it from Hello, so it survives an api restart.
alter table projects add column resting_since timestamptz;
-- keep_awake keeps a machine out of the rest rule for the jobs the rule
-- gets wrong: a bot waiting on an outbound connection it must not drop
-- (DECISIONS I-820). It takes effect at once, because the api, not hostd,
-- decides what may rest, so no running machine has to be told.
alter table projects add column keep_awake boolean not null default false;
-- The marker on the minute, so the rest rule reads its own history and a
-- resting minute is told from a busy one in the samples. False for every
-- row written before this, and for a guest on a hostd older than I-818.
alter table meter_samples add column resting boolean not null default false;
