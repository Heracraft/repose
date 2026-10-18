-- The size classes go from 2 GB to 4 GB of memory per guest vCPU
-- (DECISIONS I-784, amending R3-2, I-734 and I-739): small 1, large 2,
-- xl 4 vCPU, with the memory and the disk of every class unchanged. This
-- view is the one copy of the class vCPU figure that Go cannot reach
-- (scheduler.ClassVCPUSQL is the other SQL copy, and every query that is
-- not a view calls it); TestHostReservationsVCPUsMatchClassVCPU is what
-- holds the two together.
--
-- Committed vCPUs halve the moment this view is replaced, while the
-- guests already running keep the vCPUs they were started with until
-- their next cold start (hostd reads the class at start, and a
-- freeze/restore carries the saved vCPU count, DECISIONS I-784).
-- Placement therefore reads a host as having room that its running
-- guests have not yet given back. The host that this is true of today is
-- host-01, whose two xl and one large go from 20 committed vCPUs to 10
-- against 12 allowed, so it begins taking new projects again while its
-- running guests still hold 20; HostCPUSaturated (I-738) is the backstop
-- that says when that costs typing latency.
create or replace view host_reservations as
  select host_id,
         coalesce(sum(case class when 'small' then 4::bigint<<30 when 'large' then 8::bigint<<30 else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests,
         coalesce(sum(case class when 'small' then 1 when 'large' then 2 else 4 end), 0)::integer as vcpus
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;
