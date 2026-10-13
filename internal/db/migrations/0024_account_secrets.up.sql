-- Account secrets (DECISIONS I-697): a named secret set once for every
-- project of an account. The same envelope as secrets: AES-256-GCM under
-- the user's data key, the data key wrapped by Key Vault, both on the
-- row; the ciphertext is bound to the user and the name. Delivered with
-- each project's own set, where a project secret of the same name wins.
create table account_secrets (
  id             uuid primary key,
  user_id        uuid not null references users(id) on delete cascade,
  name           text not null,
  ciphertext     bytea not null,
  dek_wrapped    bytea not null,
  kv_key_version text not null,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now(),
  unique (user_id, name)
);
create trigger account_secrets_updated_at before update on account_secrets for each row execute function set_updated_at();
