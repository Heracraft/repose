-- The `tiny` class: 2 GB of memory and half a guest vCPU, four to a seat
-- (DECISIONS I-1168, amending R3-2, I-784, I-736, I-739 and I-737).
--
-- Two things change here. The projects CHECK gains the class, and the
-- host_reservations view -- the one copy of the class vCPU figure that Go
-- cannot reach (scheduler.ClassVCPUSQL is the other SQL copy) -- counts
-- the sold CPU in **thousandths of a vCPU** instead of whole ones,
-- because I-784's 4 GB per vCPU has no whole answer below `small`:
-- `tiny` sells 0.5 cores for its 2 GB, so four of them are one seat's
-- 8 GB and one seat's 2 guest vCPUs, and the invariant holds for any mix.
--
-- The column is renamed with the unit it now carries (vcpus ->
-- vcpu_milli) rather than silently holding a different unit under the old
-- name: a reader of the old name would have divided a host's committed
-- CPU by a thousand. TestHostReservationsVCPUsMatchClassVCPU is what
-- holds this to scheduler.ClassMilliVCPU.
--
-- Nothing about the figures of the other three classes changes: small is
-- 1000, large 2000, xl 4000, which is what they sold before in the unit
-- they sold it in.
drop view host_reservations;
create view host_reservations as
  select host_id,
         coalesce(sum(case class
                        when 'tiny' then 2::bigint<<30
                        when 'small' then 4::bigint<<30
                        when 'large' then 8::bigint<<30
                        else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests,
         coalesce(sum(case class
                        when 'tiny' then 500
                        when 'small' then 1000
                        when 'large' then 2000
                        else 4000 end), 0)::integer as vcpu_milli
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;

alter table projects drop constraint projects_class_check;
alter table projects add constraint projects_class_check
  check (class in ('tiny','small','large','xl'));
