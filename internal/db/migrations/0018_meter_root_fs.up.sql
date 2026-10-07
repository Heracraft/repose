-- The guest's root filesystem per sample (DECISIONS I-567): used and size
-- in bytes as statfs in the guest sees them, guest-written. disk_used is
-- the thin volume's allocated blocks, which keep a deleted file's blocks
-- until the guest's weekly fstrim; it stays what the operator view reads.
-- Zero for rows written before I-567 and for a guest that did not say.
alter table meter_samples add column root_used bigint not null default 0;
alter table meter_samples add column root_size bigint not null default 0;
