-- 0048_host_owner (DECISIONS I-1366): a host has an owning account, and a
-- project says whether it wants that account's own hardware or the hosted
-- fleet.
--
-- owner_account_id null is a host repose runs, which is every host that
-- exists today. Set, it is a machine the user owns and registered with a
-- join token minted for their account: placement puts only that account's
-- projects on it and never anyone else's, and it is outside the seat
-- count, the waitlist and the pool gauges, because none of those describe
-- hardware repose bought (docs/workstreams/19-mac-mini.md item 19a).
-- There is no host_join_tokens table: the single-use token has always
-- lived on the hosts row as join_token_hash, so minting one for an
-- account is this column being set at the mint.
alter table hosts add column owner_account_id uuid references users(id);

-- Placement reads the owner on every create, so the index is the one the
-- "does this account own a host" test and the per-owner listing both use.
create index hosts_owner on hosts (owner_account_id) where owner_account_id is not null;

-- Host names are unique per owner, not globally (19c's finding): two
-- accounts may both call their machine `mac-mini`, and an operator host
-- named host-01 must stay unique among the operator's own. Two partial
-- indexes rather than `unique (owner_account_id, name)`, because
-- Postgres treats nulls as distinct in a unique index and the operator's
-- hosts all have the null.
alter table hosts drop constraint hosts_name_key;
create unique index hosts_name_operator on hosts (name) where owner_account_id is null;
create unique index hosts_name_owner on hosts (owner_account_id, name) where owner_account_id is not null;

-- Where a project's machine goes when it is placed (I-1366): `own` is the
-- account's own hardware when it owns any, and the hosted fleet when it
-- owns none; `hosted` is the fleet whatever the account owns. Default
-- `own`, so a user who buys a Mac gets their machines on it without
-- saying so per project, and nothing changes for an account that owns no
-- host. A project is pinned to its host for life (DESIGN.md section 3),
-- so this is read at a create and at a re-placement and never after.
alter table projects add column placement text not null default 'own'
  check (placement in ('own', 'hosted'));
