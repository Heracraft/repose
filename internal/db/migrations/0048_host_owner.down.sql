-- Reverts 0048. Every host becomes a host repose runs, names are unique
-- across the whole table again, and no project asks for own hardware.
-- A host a user owns would make the name index below fail if two
-- accounts had taken the same name, which is the honest outcome: the
-- rows have to be looked at before the column can go.
alter table projects drop column placement;
drop index hosts_name_owner;
drop index hosts_name_operator;
alter table hosts add constraint hosts_name_key unique (name);
drop index hosts_owner;
alter table hosts drop column owner_account_id;
