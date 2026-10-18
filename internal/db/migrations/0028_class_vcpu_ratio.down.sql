-- Reverts 0028: the view sums the 2-GB-per-vCPU class figures of 0027
-- again (small 2, large 4, xl 8). The column list does not change, so
-- create or replace is enough both ways.
create or replace view host_reservations as
  select host_id,
         coalesce(sum(case class when 'small' then 4::bigint<<30 when 'large' then 8::bigint<<30 else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests,
         coalesce(sum(case class when 'small' then 2 when 'large' then 4 else 8 end), 0)::integer as vcpus
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;
