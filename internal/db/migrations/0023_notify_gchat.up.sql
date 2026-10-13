-- Google Chat is a third notification channel (DECISIONS I-692). gchat_url
-- is the space's incoming webhook in its canonical form
-- (https://chat.googleapis.com/v1/spaces/<id>/messages?key=..&token=..): a
-- credential, never logged and never returned by the api, which shows the
-- space id instead. gchat_broken_at is when Google first refused it (401,
-- 403 or 404); delivery stops until a new URL is saved or a test succeeds.
alter table users add column gchat_url text, add column gchat_broken_at timestamptz;
