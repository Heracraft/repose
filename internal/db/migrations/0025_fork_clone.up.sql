-- A fork whose copies land on their source's host clones its volume
-- there (DECISIONS I-701): hosts.features is what the host's hostd said it
-- can do in its last Hello ("local_clone"), empty for an older hostd. A
-- fork that has to go through the store takes a snapshot of its own,
-- reason 'fork', which expires a day later and does not count as the
-- project's newest (I-703).
alter table hosts add column features text[] not null default '{}';
alter table snapshots drop constraint snapshots_reason_check;
alter table snapshots add constraint snapshots_reason_check check (reason in ('scheduled','stop','manual','fork'));
