-- Reverts 0036: the rest rule keeps no record of why it said no. The
-- partitions go with the table.
drop table if exists rest_verdicts;
