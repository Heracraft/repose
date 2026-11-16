-- Reverts 0039_says. A message already typed into an agent's terminal is
-- in the terminal; only the record of it goes.
drop table says;
