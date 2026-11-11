-- Monthly partitions for meter_samples, proc_samples and rest_verdicts, and
-- the drop that makes the retention in docs/ops/OBSERVABILITY.md real:
-- meter_samples 90 days, proc_samples and rest_verdicts 30 days.
--
-- Why partitions and not `delete from ... where ts < ...`: these two tables
-- are the only ones that grow with time rather than with users. At 60-second
-- samples, a hundred projects write 4.3 million meter_samples rows a month
-- and far more proc_samples; a monthly `delete` of that would rewrite the
-- indexes and hold locks on the table the rollup reads. Dropping a partition
-- is instant and gives the space back.
--
-- Who runs this: nobody, in production. The api does the same work in Go
-- (`db.EnsurePartitions` and `db.DropExpiredPartitions`, called at start and
-- by its daily job, logging `partition_drop_fail` and counting
-- `repose_api_partition_drop_fail_total` when it fails: the §6 failure mode
-- where disk grows and nothing else breaks). This file is the operator's
-- version of it — a psql session on a control plane whose api is down, and
-- the fixture ops/dev/pgcheck.sh exercises so that a retention that never
-- deletes anything cannot pass unnoticed.
--
-- Idempotent throughout: every create is `if not exists`, and the drop only
-- touches partitions whose whole range is older than the retention.

-- The tables themselves are created partitioned by the migration
-- (docs/interfaces/db-schema.md); this file assumes:
--
--   create table meter_samples (...) partition by range (ts);
--   create table proc_samples  (...) partition by range (ts);
--   create table rest_verdicts (...) partition by range (ts);

create or replace function repose_partition_name(base text, month date)
returns text language sql immutable as $$
  select base || '_' || to_char(month, 'YYYYMM');
$$;

-- Create the partition for one month of one table, if it is missing.
create or replace function repose_partition_create(base text, month date)
returns text language plpgsql as $$
declare
  part text := repose_partition_name(base, month);
  start_at date := date_trunc('month', month)::date;
  end_at date := (date_trunc('month', month) + interval '1 month')::date;
begin
  if to_regclass(part) is not null then
    return part;
  end if;
  execute format(
    'create table %I partition of %I for values from (%L) to (%L)',
    part, base, start_at, end_at);
  -- The rollup reads by (project_id, ts); the dashboards read by ts. The
  -- primary key of the parent gives the first, so only the second is added.
  execute format('create index on %I (ts)', part);
  return part;
end;
$$;

-- Drop every partition of base whose range ends before now() - keep.
-- Returns the names dropped, so the caller can log what it did.
create or replace function repose_partition_drop_old(base text, keep interval)
returns setof text language plpgsql as $$
declare
  part record;
  cutoff timestamptz := now() - keep;
begin
  for part in
    select c.relname, pg_get_expr(c.relpartbound, c.oid) as bound
    from pg_class c
    join pg_inherits i on i.inhrelid = c.oid
    join pg_class p on p.oid = i.inhparent
    where p.relname = base and c.relkind = 'r'
  loop
    -- The bound reads:
    --   FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')
    -- for a timestamptz key, and without the time part for a date one, so the
    -- upper bound is taken as everything between the last quotes. A partition
    -- is droppable only when that bound is already past, so a partition still
    -- being written to is never touched.
    if (regexp_match(part.bound, 'TO \(''([^'']+)''\)'))[1]::timestamptz <= cutoff then
      execute format('drop table %I', part.relname);
      return next part.relname;
    end if;
  end loop;
end;
$$;

-- What the api calls hourly: this month's and next month's partitions exist,
-- and anything past retention is gone. Next month's matters at 00:00 on the
-- first, when a missing partition would reject every insert.
create or replace function repose_partitions_maintain()
returns table (action text, partition_name text) language plpgsql as $$
declare
  m date;
  dropped text;
begin
  foreach m in array array[
    date_trunc('month', now())::date,
    (date_trunc('month', now()) + interval '1 month')::date
  ] loop
    action := 'create'; partition_name := repose_partition_create('meter_samples', m); return next;
    action := 'create'; partition_name := repose_partition_create('proc_samples', m); return next;
    action := 'create'; partition_name := repose_partition_create('rest_verdicts', m); return next;
  end loop;

  -- docs/ops/OBSERVABILITY.md: 90 days and 30 days. A partition is only
  -- dropped once its whole month is past the cutoff, so the effective
  -- retention is the stated period plus up to a month, which is the price of
  -- monthly partitions and is documented rather than silently different.
  for dropped in select * from repose_partition_drop_old('meter_samples', interval '90 days') loop
    action := 'drop'; partition_name := dropped; return next;
  end loop;
  for dropped in select * from repose_partition_drop_old('proc_samples', interval '30 days') loop
    action := 'drop'; partition_name := dropped; return next;
  end loop;
  -- rest_verdicts is kept as long as proc_samples and not as long as
  -- meter_samples, because a verdict whose process names have aged out is
  -- half an answer: `cpu` as the condition and nothing to say what spent it
  -- (DECISIONS I-1152).
  for dropped in select * from repose_partition_drop_old('rest_verdicts', interval '30 days') loop
    action := 'drop'; partition_name := dropped; return next;
  end loop;
end;
$$;

-- Backfill, for the first deployment or after a restore: create the
-- partitions for the retention window that is still in range, so an import
-- of old samples has somewhere to land.
create or replace function repose_partitions_backfill()
returns setof text language plpgsql as $$
declare
  m date;
begin
  m := date_trunc('month', now() - interval '90 days')::date;
  while m <= (date_trunc('month', now()) + interval '1 month')::date loop
    return next repose_partition_create('meter_samples', m);
    return next repose_partition_create('proc_samples', m);
    return next repose_partition_create('rest_verdicts', m);
    m := (m + interval '1 month')::date;
  end loop;
end;
$$;

-- What it looks like when it has run:
--
--   select * from repose_partitions_maintain();
--    action |    partition_name
--   --------+----------------------
--    create | meter_samples_202609
--    create | proc_samples_202609
--    create | meter_samples_202610
--    create | proc_samples_202610
--    drop   | proc_samples_202607
--
--   select relname, pg_size_pretty(pg_total_relation_size(oid))
--   from pg_class where relname like 'meter_samples_%' order by relname;
