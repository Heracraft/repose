-- The launch window's questions, as SQL (DECISIONS I-804).
--
-- Why a file of queries and not metrics: a Prometheus series may not be
-- labelled by project_id or user_id (internal/obs/metrics refuses it at
-- registration and the binary stops at startup), and every question below
-- is per-account or per-cohort. Postgres holds the answers already:
-- `usage_hours` is kept indefinitely, `events` and `audit_log` with it.
-- So these need no Go, no migration and no deploy -- only psql, or a
-- Grafana panel with the `repose-postgres` datasource.
--
-- How to run one:
--
--     psql "$DATABASE_URL" -f ops/sql/launch.sql          -- all of them
--     psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
--       -c "$(sed -n '/-- Q3/,/;/p' ops/sql/launch.sql)"  -- just one
--
-- Read-only. Run them as `grafana_ro`, not as the api's role.
--
-- Every figure here is the api's own record. Polar is the ledger of what
-- was charged, and the two are reconciled by hand
-- (`repose-admin billing show <handle>`).

-- Q1. Signups a day, and how many ever got a machine running.
-- The denominator of everything else. A signup that never ran a machine
-- is a landing-page or onboarding problem, not a pricing one.
-- `started_at` is set the first time a project reaches running, so it
-- survives the machine being stopped again.
select date_trunc('day', u.created_at)::date as day,
       count(*)                                           as signups,
       count(*) filter (where p.projects > 0)              as made_a_project,
       count(*) filter (where p.started > 0)               as ran_a_machine
  from users u
  left join lateral (
    select count(*)                                       as projects,
           count(*) filter (where pr.started_at is not null) as started
      from projects pr where pr.user_id = u.id
  ) p on true
 where u.id <> '00000000-0000-7000-8000-000000000000'  -- the platform account
   and u.deleted_at is null
 group by 1 order by 1 desc limit 60;

-- Q2. The trial funnel, by plan.
-- A trial holds its full seats for seven days and pays nothing, so the
-- conversion rate is what says whether the fleet's held seats are worth
-- holding. `converted` counts subscriptions that reached a charged state.
select s.plan,
       count(*)                                                     as started,
       count(*) filter (where s.status = 'trialing')                 as in_trial,
       count(*) filter (where s.status in ('active','past_due'))     as converted,
       count(*) filter (where s.status = 'canceled'
                          and s.trial_end is not null
                          and s.cancel_at <= s.trial_end)            as cancelled_in_trial,
       round(100.0 * count(*) filter (where s.status in ('active','past_due'))
             / nullif(count(*) filter (where s.status <> 'trialing'), 0), 1)
                                                                     as conversion_pct
  from subscriptions s
 group by 1 order by 1;

-- Q3. Activation, in the product's own terms (I-806).
-- The pitch is that the agent keeps working after the laptop closes. This
-- is the only query that measures it. A subscriber with hours but no
-- unattended agent seconds is using a cloud VM, not this product, and is
-- next month's churn.
select u.handle, s.plan, s.status,
       round(sum(h.running_seconds) / 3600.0, 1)             as machine_hours,
       round(sum(h.attended_seconds) / 3600.0, 1)            as attended_hours,
       round(sum(h.unattended_agent_seconds) / 3600.0, 1)    as agent_alone_hours,
       round(100.0 * sum(h.unattended_agent_seconds)
             / nullif(sum(h.running_seconds), 0), 1)         as agent_alone_pct
  from subscriptions s
  join users u on u.id = s.user_id
  join projects p on p.user_id = s.user_id
  join usage_hours h on h.project_id = p.id
 where s.status in ('trialing','active','past_due')
   and h.hour >= now() - interval '30 days'
 group by 1, 2, 3 order by agent_alone_hours desc;

-- Q4. Subscribers who have gone quiet.
-- Paying and not running anything for a week. The stall list: one email
-- each, before the renewal that they cancel.
select u.handle, s.plan, s.status, s.period_end::date as renews,
       coalesce(max(h.hour), s.created_at)::date      as last_hour_with_a_machine,
       round(extract(epoch from now() - coalesce(max(h.hour), s.created_at)) / 86400.0, 1) as days_quiet
  from subscriptions s
  join users u on u.id = s.user_id
  left join projects p on p.user_id = s.user_id
  left join usage_hours h on h.project_id = p.id and h.running_seconds > 0
 where s.status in ('trialing','active','past_due')
 group by 1, 2, 3, 4, s.created_at
having coalesce(max(h.hour), s.created_at) < now() - interval '7 days'
 order by days_quiet desc;

-- Q5. Retention by signup cohort, in weeks.
-- week_1 and week_4 are the subscribers of each cohort still live. With
-- tens of accounts this is a handful of rows; read it as counts, not
-- percentages.
select date_trunc('week', u.created_at)::date as cohort,
       count(distinct u.id)                                                     as signed_up,
       count(distinct s.user_id)                                               as subscribed,
       count(distinct s.user_id) filter (where s.status in ('active','past_due')) as still_paying
  from users u
  left join subscriptions s on s.user_id = u.id
 where u.id <> '00000000-0000-7000-8000-000000000000'  -- the platform account
   and u.deleted_at is null
 group by 1 order by 1 desc limit 12;

-- Q6. The founding offer's burn (I-672).
-- Thirty places, $10 off Solo for twelve charges each. `places_left` is
-- the same number GET /public/seats publishes as founding_left; the tail
-- is what it still costs.
select count(*)                                                        as places_taken,
       greatest(0, 30 - count(*))                                      as places_left,
       count(*) filter (where intro_until > now())                     as still_discounted,
       sum(greatest(0, ceil(extract(epoch from intro_until - now()) / 2592000.0)))::int * 10
                                                                       as dollars_of_discount_left
  from subscriptions
 where intro and status in ('trialing','active','past_due');

-- Q7. The plan mix as it stands, and the downgrades already waiting.
-- A plan change leaves no audit_log row (nothing writes one) and
-- subscriptions keeps no history of the plan it was on, so the direction
-- of past changes is only in Polar's dashboard and in
-- `repose_api_billing_subscriptions_total{plan,status}`. What this table
-- does hold is `scheduled_plan`: a downgrade that takes effect at the next
-- renewal, which is churn with a date on it.
select s.plan, s.status, count(*) as subscriptions, sum(s.seats) as seats,
       count(*) filter (where s.scheduled_plan is not null)      as downgrade_waiting,
       count(*) filter (where s.cancel_at is not null)           as cancelling,
       min(s.period_end)::date                                   as next_renewal
  from subscriptions s
 where s.status in ('trialing','active','past_due')
 group by 1, 2 order by 1, 2;

-- Q8. MRR that left, and why it was recorded as leaving.
-- `repose_api_mrr_cents` is what stands today; this is what went.
select date_trunc('week', coalesce(s.cancel_at, s.updated_at))::date as week,
       s.plan, count(*) as subscriptions,
       sum(case s.plan when 'pro' then 169 when 'plus' then 89 else 49 end) as dollars_a_month
  from subscriptions s
 where s.status = 'canceled' and coalesce(s.cancel_at, s.updated_at) >= now() - interval '90 days'
 group by 1, 2 order by 1 desc;

-- Q9. Involuntary churn: the dunning path.
-- A card that failed is revenue that wanted to stay. Day 3 of past_due
-- stops the machines (PRICING.md "Failed payments").
select u.handle, s.plan, u.past_due_since::date as past_due_since,
       round(extract(epoch from now() - u.past_due_since) / 86400.0, 1) as days,
       u.billing_status, u.suspended_reason
  from users u join subscriptions s on s.user_id = u.id
 where u.billing_status in ('past_due','suspended') or s.status = 'past_due'
 order by u.past_due_since;

-- Q10. The waitlist's throughput, and what it wasted.
-- An invitation holds a seat for 72 hours. One that expired held a seat
-- the fleet paid for and nobody used.
select count(*)                                                      as on_the_list,
       count(*) filter (where invited_at is null)                     as waiting,
       count(*) filter (where invited_at is not null and converted_at is not null) as converted,
       count(*) filter (where invited_at is not null and converted_at is null
                          and hold_until > now())                     as holding_now,
       count(*) filter (where invited_at is not null and converted_at is null
                          and hold_until <= now())                    as expired_unused,
       round(avg(extract(epoch from converted_at - invited_at) / 3600.0)
             filter (where converted_at is not null), 1)              as hours_to_convert
  from waitlist;

-- Q11. Disk against the plan, per account.
-- Disk is a hard cap with no overage (I-585), so an account near its
-- ceiling is a create that is about to be refused. `allocated` is what
-- the host's disk pays for; `used` is what the files take. A wide gap is
-- untrimmed blocks, which the daily trim and every stop reclaim.
select u.handle, s.plan,
       pg_size_pretty(sum(l.disk_alloc))                             as allocated,
       pg_size_pretty(sum(l.disk_used))                              as used,
       (case s.plan when 'pro' then 500 when 'plus' then 250 else 100 end) as plan_gb,
       round(100.0 * sum(l.disk_alloc) / 1073741824.0
             / (case s.plan when 'pro' then 500 when 'plus' then 250 else 100 end), 1) as pct_of_plan
  from subscriptions s
  join users u on u.id = s.user_id
  join projects p on p.user_id = s.user_id and p.destroyed_at is null
  join lateral (select disk_alloc, disk_used from meter_samples m
                 where m.project_id = p.id order by m.ts desc limit 1) l on true
 where s.status in ('trialing','active','past_due')
 group by 1, 2 order by pct_of_plan desc;

-- Q12. Ops still open, and the oldest of them.
-- A pending op older than a few minutes is a user waiting. The index on
-- ops(state) makes this cheap.
select kind, state, count(*) as n,
       round(extract(epoch from now() - min(created_at)) / 60.0, 1) as oldest_minutes
  from ops where state in ('pending','running')
 group by 1, 2 order by oldest_minutes desc;
