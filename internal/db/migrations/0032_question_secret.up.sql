-- A question that asks for a named secret (DECISIONS I-929).
--
-- `repose-ask --secret NAME` names the secret; the owner answers with its
-- value, which goes to the secrets table like any `repose secrets set`
-- and never to this row: `answer` stays null. `secret_op_id` is the
-- UpdateSecrets op the answer queued, and the delivery worker carries the
-- answer to the guest only once that op has finished, so the asker's next
-- command reads the new value.
alter table questions add column secret_name text;
alter table questions add column secret_op_id uuid;
