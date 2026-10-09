-- Reverts 0020's names; rows ended by it stay ended.
alter table overage_charges rename column sent_ref to paddle_transaction_id;
alter index billing_events_received rename to paddle_events_received;
alter table billing_events rename to paddle_events;
alter table subscriptions drop column source_modified_at;
alter table subscriptions drop column provider;
alter table subscriptions rename column customer_id to paddle_customer_id;
alter table users rename column billing_customer_id to paddle_customer_id;
