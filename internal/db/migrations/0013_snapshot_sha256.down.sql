-- Reverts 0013: snapshots carry no digest and restores are unchecked.
alter table snapshots drop column sha256;
