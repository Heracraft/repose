-- Reverts 0041: the view counts whole vCPUs under the old column name and
-- `tiny` is not a class again. Any project still on `tiny` would fail the
-- CHECK, so they are moved up to `small` first -- the smallest class that
-- survives the revert, and more memory than the project had, never less.
update projects set class = 'small' where class = 'tiny';

alter table projects drop constraint projects_class_check;
alter table projects add constraint projects_class_check
  check (class in ('small','large','xl'));

drop view host_reservations;
create view host_reservations as
  select host_id,
         coalesce(sum(case class when 'small' then 4::bigint<<30 when 'large' then 8::bigint<<30 else 16::bigint<<30 end), 0)::bigint as reserved_bytes,
         count(*)::integer as guests,
         coalesce(sum(case class when 'small' then 1 when 'large' then 2 else 4 end), 0)::integer as vcpus
    from projects
   where host_id is not null
     and state in ('creating','building','starting','running','stopping','restoring')
   group by host_id;
