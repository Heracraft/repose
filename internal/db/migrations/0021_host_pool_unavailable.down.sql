-- Reverts 0021: the scheduler reads only pool_free_bytes again, which a
-- hostd with an unavailable pool sends as 0.
alter table hosts drop column pool_unavailable;
