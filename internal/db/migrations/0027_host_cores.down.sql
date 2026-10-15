-- Reverts 0027: placement reads only vcpus, the SMT thread count, again,
-- and the reservation view carries memory alone. A column cannot be
-- dropped by create or replace, so the view is recreated from 0001.
drop view if exists host_reservations;
create view host_reservations as
  select host_id,
         coalesce(sum(case class when 'small' then 4::bigint<<30 when 'large' then 8::bigint<<30 else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;
alter table hosts drop column cores;
