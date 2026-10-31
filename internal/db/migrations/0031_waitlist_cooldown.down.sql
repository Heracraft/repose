drop index if exists waitlist_cooldown;
alter table waitlist drop column if exists cooldown_until;
