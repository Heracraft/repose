-- The Own hardware plan (DECISIONS I-1364): $10 a month for one host the
-- user owns. The plan ids stay text; only the check constraints learn
-- 'own'. Without this a webhook for an Own hardware subscription fails on
-- the constraint and the account never gets its plan.
alter table subscriptions drop constraint subscriptions_plan_check;
alter table subscriptions add constraint subscriptions_plan_check check (plan in ('solo','plus','pro','own'));
alter table subscriptions drop constraint subscriptions_scheduled_plan_check;
alter table subscriptions add constraint subscriptions_scheduled_plan_check check (scheduled_plan in ('solo','plus','pro','own'));
