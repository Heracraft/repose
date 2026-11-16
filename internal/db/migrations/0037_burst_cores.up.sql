-- Burst cores: the CPU time a guest used above the base cores its class
-- sells (DECISIONS I-1126, I-1128).
--
-- meter_samples.burst_cpu_ns is one minute's burst, nanoseconds, as the
-- host's controller counted it second by second. Null on a sample from a
-- hostd older than I-1126 and on every sample from a host without the
-- `burst` feature, which is every host until one is measured; billing
-- reads null as no burst.
alter table meter_samples add column burst_cpu_ns bigint;

-- usage_hours.burst_cpu_ns is the hour's burst for a project, summed
-- from the samples. Not null with a default, like the other usage
-- columns, so a period's total needs no coalesce per row.
alter table usage_hours add column burst_cpu_ns bigint not null default 0;
