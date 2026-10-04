-- Reverts 0012: snapshots carry no digest and restores are unchecked.
alter table snapshots drop column sha256;
