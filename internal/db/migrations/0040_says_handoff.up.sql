-- The says row is the hand-off between the api's two processes
-- (DECISIONS I-1162, amending I-1144). In production the api runs as
-- `API_MODE=http`, which serves `repose say`, and `API_MODE=grpc`,
-- which holds every hostd stream (I-87, I-163). Only the second can
-- send a SayToAgent, so the first stores the row and waits on it while
-- the second delivers it and writes back what the machine answered.
--
-- Two states are added for that hand-off, both of them short-lived and
-- neither of them shown to anyone: `pending` (stored, nothing has
-- picked it up) and `sending` (the delivery worker has claimed it and
-- is waiting for the machine). A row that is still in either 40 s
-- after it was stored is failed by the worker's next pass, which is
-- how a worker that died mid-delivery stops a message arriving long
-- after the sender was told it did not.
alter table says drop constraint says_state;
alter table says add constraint says_state
  check (state in ('pending','sending','delivered','waiting','replaced','failed'));
-- The machine's own answer, which the waiting process can no longer
-- read from the command's result because it did not send the command.
-- `replaced` is the flag the sender is shown; `refusal` is the
-- machine's sentence naming the agent windows, kept only for the two
-- refusals that name them and shown to the sender as it is.
alter table says add column replaced boolean not null default false;
alter table says add column refusal text;
create index says_pending on says(created_at) where state in ('pending','sending');
