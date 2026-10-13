-- Reverts 0025: fork snapshots read as manual ones, hosts forget their
-- features.
update snapshots set reason = 'manual' where reason = 'fork';
alter table snapshots drop constraint snapshots_reason_check;
alter table snapshots add constraint snapshots_reason_check check (reason in ('scheduled','stop','manual'));
alter table hosts drop column features;
