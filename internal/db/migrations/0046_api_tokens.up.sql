-- 0046_api_tokens (DECISIONS I-1314): a bearer an agent or a script calls
-- the api with, in place of a Logto login. The row holds the token's
-- sha256 and never the token: a copy of the table opens nothing. scopes
-- are the areas it may touch (I-1316); project_id, when set, binds it to
-- one project (I-1318). expires_at is required: a token without an end
-- is a login that never logs out. revoked_at keeps the row 30 days so
-- `repose tokens ls` can say "revoked" to the person who still holds it.
create table api_tokens (
  id            uuid primary key,
  user_id       uuid not null references users(id) on delete cascade,
  name          text not null,
  prefix        text not null,
  token_sha256  bytea not null unique,
  scopes        text[] not null,
  project_id    uuid references projects(id) on delete cascade,
  expires_at    timestamptz not null,
  created_at    timestamptz not null default now(),
  last_used_at  timestamptz,
  revoked_at    timestamptz
);
-- One live token per name and account; a revoked one frees the name.
create unique index api_tokens_live_name on api_tokens (user_id, name) where revoked_at is null;
create index api_tokens_user on api_tokens (user_id, created_at desc);
