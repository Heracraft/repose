-- Billing moves from Paddle to Polar (DECISIONS I-604). The columns lose
-- the provider's name, subscriptions say which provider a row came from,
-- and the overage line's reference is the external id the event was sent
-- with. A live Paddle subscription can never hear from Paddle again, so it
-- ends here and its account returns to having no plan: a billing
-- suspension is lifted with it (an operator's stays), and the Paddle
-- customer id is dropped so the first Polar event stores Polar's.
-- Paddle's ids start sub_; Polar's are UUIDs, so a rollback and rerun
-- leaves Polar's rows alone. source_modified_at is the provider's
-- modified_at of the newest payload applied, so an older event delivered
-- late cannot undo a newer one.
alter table users rename column paddle_customer_id to billing_customer_id;
alter table subscriptions rename column paddle_customer_id to customer_id;
alter table subscriptions add column provider text not null default 'polar' check (provider in ('paddle','polar'));
alter table subscriptions add column source_modified_at timestamptz;
update subscriptions set provider = 'paddle' where id like 'sub\_%';
alter table paddle_events rename to billing_events;
alter index paddle_events_received rename to billing_events_received;
alter table overage_charges rename column paddle_transaction_id to sent_ref;

update users u set billing_status = 'none', past_due_since = null,
       suspended_at = case when u.suspended_reason = 'billing' then null else u.suspended_at end,
       suspended_reason = case when u.suspended_reason = 'billing' then null else u.suspended_reason end
 where (u.billing_status in ('trial','active','past_due')
        or (u.billing_status = 'suspended' and u.suspended_reason = 'billing'))
   and exists (select 1 from subscriptions s where s.user_id = u.id and s.provider = 'paddle'
               and s.status in ('trialing','active','past_due'));
update subscriptions set status = 'canceled', cancel_at = coalesce(cancel_at, now())
 where provider = 'paddle' and status in ('trialing','active','past_due','paused');
update users set billing_customer_id = null where billing_customer_id like 'ctm\_%';
