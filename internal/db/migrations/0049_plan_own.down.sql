-- Reverts 0049. Fails while any subscription is on own or has an own
-- change scheduled; move those first, since no hosted plan means the same.
alter table subscriptions drop constraint subscriptions_plan_check;
alter table subscriptions add constraint subscriptions_plan_check check (plan in ('solo','plus','pro'));
alter table subscriptions drop constraint subscriptions_scheduled_plan_check;
alter table subscriptions add constraint subscriptions_scheduled_plan_check check (scheduled_plan in ('solo','plus','pro'));
