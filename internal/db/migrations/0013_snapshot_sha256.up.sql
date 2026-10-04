-- Snapshot digests (DECISIONS I-462): the hex SHA-256 of the blob hostd
-- uploaded, recorded here so a restore can check the stored blob against
-- something the snapshot store cannot change. Null for a snapshot taken
-- before I-462, which restores unchecked.
alter table snapshots add column sha256 text;
