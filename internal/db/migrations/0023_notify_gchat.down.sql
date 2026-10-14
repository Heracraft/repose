delete from events_outbox where channel = 'gchat';
alter table users drop column gchat_broken_at, drop column gchat_url;
