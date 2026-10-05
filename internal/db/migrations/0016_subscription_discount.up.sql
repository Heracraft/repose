-- The introductory price (DECISIONS I-497) is a recurring Paddle discount
-- on the subscription. The webhook records which discount and when Paddle
-- says it ends, so the trial_ending and payment_failed emails name the
-- amount Paddle charges, and GET /billing can say until when.
alter table subscriptions add column discount_id text;
alter table subscriptions add column discount_ends_at timestamptz;
