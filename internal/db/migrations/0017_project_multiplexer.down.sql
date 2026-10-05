-- Reverts 0017: every project runs tmux again.
alter table projects drop column multiplexer;
