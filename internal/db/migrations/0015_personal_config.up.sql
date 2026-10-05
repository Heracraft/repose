-- The personal layer (DECISIONS I-490): an account's machine.nix, a
-- home-manager module every machine of the account gets beside its
-- project fragment. Each save is a row; the newest row is the account's
-- current text, and an empty fragment means none.
create table personal_revisions (
  id         uuid primary key,
  user_id    uuid not null references users(id),
  fragment   text not null,
  -- Where the save came from: cli (repose config --global, or run's push
  -- of ~/.config/repose/machine.nix) or dashboard.
  source     text not null default 'cli' check (source in ('cli', 'dashboard')),
  created_at timestamptz not null default now()
);
create index personal_revisions_user on personal_revisions(user_id, created_at desc);

-- A project can opt out of the personal layer (repose run --no-personal,
-- or the dashboard's switch).
alter table projects add column personal_opt_out boolean not null default false;

-- Each project revision records the personal text it was built with, so
-- a rebuild, a restore or a copy reproduces it; '' means none. Whether the
-- project had opted out is recorded beside it, and personal_line is the
-- error's line in machine.nix, as fragment_line is in the fragment.
alter table config_revisions add column personal text not null default '';
alter table config_revisions add column personal_revision_id uuid references personal_revisions(id);
alter table config_revisions add column personal_opt_out boolean not null default false;
alter table config_revisions add column personal_line integer;
