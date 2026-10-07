-- Reverts 0019: the plan's disk counts volume sizes again.
alter table projects drop column disk_held_at;
alter table projects drop column disk_held_bytes;
