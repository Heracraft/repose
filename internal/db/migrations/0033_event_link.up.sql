-- The link an agent attached to a message or a question with
-- `repose-notify --link URL --label TEXT` (DECISIONS I-931), checked by
-- internal/msglink. Tenant content, like the summary beside it: shown to
-- the owner and drawn as a button, never logged.
alter table events add column link_url text;
alter table events add column link_label text;
