-- 0047_github_choice (DECISIONS I-1338, I-1340): the account's answer to
-- "what do my machines use for GitHub", and what GitHub said about the
-- token's life.
--
-- gh_login_copy is the account saying whether a laptop may copy its
-- GitHub CLI login into the machines. True by default, which is what
-- every account has done until now. It is a refusal the api holds and
-- never a permission: a laptop that has turned the copy off in its own
-- config.toml stays off whatever this says, so the column can only ever
-- subtract (I-422 as amended by I-1340). The api still never sees a
-- login; this is a preference about one.
alter table users add column gh_login_copy boolean not null default true;

-- What GitHub answered for the GH_TOKEN the account stored: its
-- expiration, as its own `github-authentication-token-expiration` header
-- gave it, or null when it gave none. Informational only — nothing is
-- refused by it; it is what the week-ahead warning reads. Cleared when
-- the account's GH_TOKEN goes.
alter table users add column gh_token_expires_at timestamptz;
