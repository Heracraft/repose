-- Reverts 0014: no personal layer.
alter table config_revisions drop column personal_line;
alter table config_revisions drop column personal_opt_out;
alter table config_revisions drop column personal_revision_id;
alter table config_revisions drop column personal;
alter table projects drop column personal_opt_out;
drop table personal_revisions;
