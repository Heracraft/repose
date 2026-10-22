-- Activation, in the product's own terms (DECISIONS I-806). The pitch is
-- that the agent keeps working after the laptop closes, and nothing
-- measured whether it does: a launch cohort's activation could be read
-- only from meter_samples, which is kept 90 days, so by month four the
-- question would be unanswerable. usage_hours is kept indefinitely, so
-- the rollup writes the two seconds columns beside the hours it already
-- writes, out of the same samples.
--
-- attended_seconds: minutes in the hour with somebody attached, by SSH
-- session or tmux client.
-- unattended_agent_seconds: minutes with an agent at work and nobody
-- attached. This is the claim. An agent "at work" is one whose state is
-- neither idle nor needs_input, the same test idle.usedSQL makes.
--
-- Both are derived from samples about a minute apart, so each is a
-- multiple of 60 and is an estimate of the same quality as
-- running_seconds. Neither carries what the agent did, only that it ran.
alter table usage_hours
  add column attended_seconds         integer not null default 0,
  add column unattended_agent_seconds integer not null default 0;
