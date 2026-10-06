-- The introductory offer (DECISIONS I-497): $20 a month and 100 GB of
-- egress for Solo's first three charges, charged by a recurring Paddle
-- discount. The webhook marks a subscription that carries the configured
-- introductory discount and copies when Paddle says it ends, so the
-- emails, GET /billing and the egress allowance follow the offer.
alter table subscriptions add column intro boolean not null default false;
alter table subscriptions add column intro_until timestamptz;
