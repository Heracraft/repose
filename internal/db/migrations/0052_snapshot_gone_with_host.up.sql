-- The name of the account's own host a snapshot was kept on, set when
-- that host is removed (DECISIONS I-1542): the snapshot was on that
-- host's disk, the removal deleted it, and repose had no copy. Null for
-- every other snapshot. A restore of one is refused naming the host.
alter table snapshots add column if not exists gone_with_host text;
