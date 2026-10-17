-- Placement counts committed guest vCPUs against a host's physical cores,
-- not the SMT threads vcpus holds (DECISIONS I-735, I-736). hostd reports
-- cores in HostInfo from I-735 on; Register and Rotate record vcpus / 2
-- for a hostd older than that, which is the exact core count on every
-- shape repose can run (DESIGN.md section 4: Intel, two threads per core).
-- The backfill does the same for a host that will not register again: the
-- join token is single use, so host-01 would otherwise sit at 0 cores for
-- ever. 0 remains possible and means unknown; no CPU ratio is applied to
-- such a host.
alter table hosts add column cores integer not null default 0;
update hosts set cores = vcpus / 2 where vcpus > 1 and cores = 0;

-- The same rows that reserve memory also commit guest vCPUs, so the
-- reservation view carries both (DECISIONS I-736). Class vCPUs are
-- small 2, large 4, xl 8, two GB of memory each (R3-2).
create or replace view host_reservations as
  select host_id,
         coalesce(sum(case class when 'small' then 4::bigint<<30 when 'large' then 8::bigint<<30 else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests,
         coalesce(sum(case class when 'small' then 2 when 'large' then 4 else 8 end), 0)::integer as vcpus
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;
