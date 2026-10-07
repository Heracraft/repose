-- The terminal multiplexer a project's next start runs (DECISIONS I-502):
-- tmux, the default and every existing project's value, or herdr (I-501).
-- The api writes it into project_json on each create, start and restore.
-- 0017 because solo-at-20 holds 0016; the two touch different tables, so
-- either order applies.
alter table projects add column multiplexer text not null default 'tmux'
  check (multiplexer in ('tmux', 'herdr'));
