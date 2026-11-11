-- Why a running machine did not freeze, written down at the time
-- (DECISIONS I-1152).
--
-- The rest rule (I-818, I-844) runs every minute for every running
-- project and leaves two numbers behind: how many machines it paused and
-- how many it would have paused. Neither answers the owner's question of
-- 2026-11-10 -- "I'm not attached, the agent is idle, but the machine is
-- still running and not frozen" -- because the rule keeps no record of
-- which of its terms said no. Afterwards the samples are still there and
-- the verdict is not, and re-deriving it means re-implementing the rule
-- in SQL and hoping the two agree.
--
-- So the rule writes its own verdict down. One row per running machine
-- per five minutes (internal/api/resting.VerdictEvery), from the rule's
-- own tick; no second timer, and a fifth of meter_samples' cadence.
--
-- What is NOT here, because it is already somewhere else: the signals
-- (meter_samples, 90 days) and the process names with their CPU
-- (proc_samples, 30 days). sample_ts is the key into both: it is the ts
-- of the newest meter_samples row the verdict read, so
-- `join proc_samples p on p.project_id = v.project_id and p.ts =
-- v.sample_ts` gives the processes that were burning CPU in the minute
-- the verdict is about. Nothing from inside a guest is copied here.
create table rest_verdicts (
  ts         timestamptz not null,  -- the tick this verdict was taken at
  project_id uuid not null,
  host_id    uuid,
  -- The newest meter_samples row the verdict read; null when the machine
  -- had no sample in the window at all. The join key to meter_samples and
  -- proc_samples.
  sample_ts  timestamptz,

  -- would_rest is the rule's own verdict: every term of I-818 passed.
  would_rest boolean not null,
  -- blocked_by is the column to read: the one condition that kept this
  -- machine awake, lower_snake, '' when nothing did. The rule's own terms
  -- are `resting` (it IS frozen, which blocks nothing and is excluded
  -- from the "still warm" count), `host_unreachable`, `host_not_ready`,
  -- `keep_awake`, `op`, `question`, `session`, `too_young`,
  -- `samples_missing`, `attached`, `agent_working`, `guestd_down`, `cpu`,
  -- `net` and the catch-all `not_quiet`. Past the rule: `host_no_pause`
  -- (the rule said yes and the host does not take pauses: I-844's dry
  -- run) and `host_<why_not>` (the pause was sent and hostd refused with
  -- that word, e.g. `host_timer_due`).
  --
  -- Several terms can fail at once; blocked_by names the first in the
  -- order the columns below are written, and the counts beside it say
  -- what else was true. The order runs from what the user asked for
  -- (keep_awake, an op, a question, a session) to what the machine was
  -- doing (attached, an agent, CPU, network), because the first is an
  -- answer and the second is a measurement.
  blocked_by text not null,

  -- The terms that live only in Postgres and nowhere in the samples.
  host_pauses    boolean not null,  -- the host's Hello named `pause`
  keep_awake     boolean not null,
  ops_open       integer not null default 0,
  sessions_open  integer not null default 0,  -- a gateway session: an attach, a held forward, a browser viewer
  questions_open integer not null default 0,

  -- What the quiet window looked like, over the newest `Samples` samples
  -- the rule reads. samples_seen below Samples, or a sample_ts older than
  -- the freshness floor, is `samples_missing`: the sampler, not the
  -- machine.
  samples_seen   integer not null default 0,
  samples_quiet  integer not null default 0,
  attached_max   integer not null default 0,  -- the largest ssh_sessions + tmux_clients in the window
  agents_working integer not null default 0,  -- samples with an agent outside idle/needs_input
  guestd_down    integer not null default 0,  -- samples with guestd_ok false
  cpu_ns_max     bigint  not null default 0,  -- the busiest sample's cpu_ns, against the 6 s line
  net_bytes_max  bigint  not null default 0,  -- the busiest sample's net_rx + net_tx, against 64 KiB

  -- hostd's own lower_snake refusal word for the most recent pause it
  -- turned down for this project, '' when there is none recent. The api
  -- learns it from PauseResult and nothing kept it.
  why_not text not null default '',

  primary key (project_id, ts)
) partition by range (ts);

-- The owner's question is "which condition, over the last fortnight", so
-- the scan is by ts and then by blocked_by. The primary key serves the
-- per-project reads.
create index rest_verdicts_ts_blocked on rest_verdicts (ts, blocked_by);
