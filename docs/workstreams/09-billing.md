# 09 · billing

> **Superseded in part (2026-09-27, DECISIONS I-289).** §1 to §4 and
> §5.1 to §5.10 describe the hourly design on Stripe as it was built and
> merged; they are kept as history. What runs is §5.11, "Plans on
> Paddle", and the §9 checklist is that design's. `docs/PRICING.md` is
> what is sold.

## 1. Goal

Turn the meter samples hostd already sends into money, correctly, from the
first hour. Stripe is the ledger of record for charges; `usage_hours` is the
ledger of record for what was used; a nightly reconciliation proves they
agree. A user who never stops a guest pays exactly the flat cap, and a user
who stops it half the time pays half.

## 2. Scope: builds

- `internal/billing/` in the api: Stripe customer lifecycle, SetupIntent for
  card on file, trial credit ledger, hourly rollup from `meter_samples` into
  `usage_hours`, pricing and cap logic, hourly usage record push to Stripe,
  monthly invoices via Stripe Billing, webhooks, past-due handling,
  suspension, reconciliation job, admin overrides.
- The `usage_hours`, `invoices` tables and a `credit_ledger` table (added to
  `interfaces/db-schema.md` by this workstream).
- The `/usage`, `/billing/*` routes in `interfaces/api.md` (implemented in
  05's HTTP layer, logic here).
- `repose-admin billing` subcommands: `credit <user> <cents> <reason>`,
  `suspend`, `unsuspend`, `reconcile [--month]`, `explain <project> <hour>`.
- The Stripe test-mode fixture that reproduces the CHECKLIST usage pattern
  (one large guest, 100 hours, 40 GB, 10 GB egress) and asserts the invoice.
- `PRICING.md` numbers wired as constants in one file,
  `internal/billing/prices.go`, with a test that fails if the doc and the
  constants disagree (the test parses the table in `PRICING.md`).

### Billing-exempt accounts (DECISIONS I-16)

`repose-admin users exempt <handle>` sets `billing_status=exempt`. Exempt
users pass the card and trial checks, still accrue `usage_hours`, and are
never pushed to Stripe. With no `STRIPE_*` variables the api starts normally
and billing routes answer `503 billing_disabled`.

## 3. Scope: does not build

- Sampling (03-hostd sends `Samples`; 05 writes `meter_samples`). This
  workstream reads `meter_samples` and nothing upstream of it.
- The dashboard billing page UI (08). This workstream provides the routes
  and the Stripe objects it needs.
- Tax (Stripe Tax is switched on in the Stripe dashboard with no code;
  documented in §5.9), VAT ids, invoices in currencies other than USD.
- Idle auto-stop (R1-5, later). Billing must not depend on it.
- Egress shaping and counting (01-host-nixos, 03-hostd). Billing reads the
  bytes.

## 4. Interfaces

Owns: `usage_hours`, `invoices`, `credit_ledger` in
`interfaces/db-schema.md`; the `/usage` and `/billing/*` route semantics in
`interfaces/api.md`.

Consumes: `interfaces/grpc-hostd.md` (`GuestSample` fields: `state`,
`class`, `disk_alloc_bytes`, `net_tx_bytes_delta`), `interfaces/db-schema.md`
(`meter_samples`, `projects`, `users`).

## 5. Design detail

> §5.1 to §5.10 are the superseded hourly design (Stripe meters, the cap,
> the trial credit, reconciliation), kept as history: the tables and
> columns they name still exist (`usage_hours`, `credit_ledger`,
> `invoices`, db-schema.md marks the unused ones). §5.11 is the design in
> the code since I-289.

### 5.1 Prices

From `DESIGN.md` §14 and `PRICING.md`, in cents, USD:

| Item | Value |
|---|---|
| small cap / month | 4900 |
| large cap / month | 9900 |
| xl cap / month | 19900 |
| small hourly | ceil(4900 / 720) = 7 |
| large hourly | ceil(9900 / 720) = 14 |
| xl hourly | ceil(19900 / 720) = 28 |
| storage per GB-month | 10 |
| egress included per project per month | 500 GB |
| egress per GB beyond | 5 |
| trial credit | 1000 |

"Month" is the user's Stripe billing period (anchored when the card is
added and the subscription created, stored truncated to the hour,
DECISIONS I-179), not the calendar month. The cap applies per project per billing period to guest-hours
only; storage and egress are always additive. A project that changes class
mid-period is capped at the sum of `hours_in_class × hourly` bounded by the
larger class's cap; simpler rules were considered and rejected because they
either let a downgrade-then-upgrade evade the cap or overcharge a one-hour
XL trial. Recorded here as the rule; `explain` prints the arithmetic.

### 5.2 Customer and card

On first `GET /me` after Logto sign-up, the api creates a Stripe customer
(`metadata.user_id`, email) and stores `stripe_customer_id`. `POST
/billing/setup` creates a SetupIntent (`usage: off_session`,
`payment_method_types: [card]`) and returns its client secret; with
`{"flow": "checkout"}` it creates a Stripe Checkout session in setup mode
instead (billing address required and saved on the customer) and returns
its URL, which is what the dashboard uses (DECISIONS I-182). The
`setup_intent.succeeded` webhook attaches the payment method as the
customer's default, copies its billing address onto the customer, creates
the subscription (adopting a live one Stripe already has, I-181) and sets
`users.billing_status` from `trial` (no card) to `trial` (with card,
`has_card = true`); a `trial` account whose credit is already spent goes
to `active` in the same statement (I-184). A guest cannot be created or
started while `has_card` is false, whatever the trial balance: that is R2-10,
card before compute.

### 5.3 Trial credit

`credit_ledger (id, user_id, cents, reason, ref, created_at)`; sign-up
inserts `+1000 "trial"`. Hourly usage first debits the ledger (a negative
row per hour with `ref = usage_hours pk`) until the balance is zero, and only
the remainder becomes a Stripe usage record. A user with a positive balance
and a card is `trial`; when the balance hits zero they become `active`
and the next hour is billed — the transition happens in the same
transaction as the debit that exhausted the balance, because the card gate
refuses a `trial` account with no credit (`trial_depleted`) and an account
that merely used its trial credit must not be locked out of its own guests.
The transition needs a card; an account whose card was removed before its
credit ran out stays `trial` at zero, is refused `trial_depleted`, and
moves to `active` when a card arrives (DECISIONS I-184). `repose-admin billing credit` adds rows for
goodwill or refunds. Balance is `sum(cents)`, computed with an index, never
cached on `users` (the cached column was rejected because two hourly jobs
racing would drift it). `users.trial_credit_cents` remains as a *projection*
of the ledger, updated by an `after insert` trigger inside the same
transaction and under the same row lock as the ledger row, so it cannot
drift; `sum(cents)` is still the balance of record and is what `Balance`
returns (DECISIONS I-78).

### 5.4 Hourly rollup

The rollup is `internal/billing.Rollup` (DECISIONS I-78; workstream 05 had
built it as `internal/api/meter.Rollup` against the calendar month).
`usage_hours` rows carry the `period_start` and `period_end` they were
priced in, so the running totals a later hour reads cannot drift from the
rule that priced the earlier ones.

A job at `:05` past each hour, idempotent, keyed on `(project_id, hour)`:

1. For each project with any `meter_samples` in the hour, or with
   `state != destroyed` (storage accrues while a project exists):
   `running_seconds = count(samples where state = running) × 60`, capped
   at 3600; a sample is a minute. Missing samples (host unreachable) count
   as not running for that minute, and a `billing_gap` metric records it so
   a host outage is visible as under-billing rather than a silent overcharge.
2. `class` = the class in the last sample of the hour (class changes require
   a stopped guest, so within an hour of running it is constant).
3. `gb_alloc` = `disk_alloc_bytes` of the last sample, or
   `projects.volume_bytes` if no sample.
4. `egress_bytes = sum(net_tx_bytes_delta)`.
5. `cost_cents` = guest part + storage part + egress part:
   - guest part: `hourly[class] × running_seconds / 3600`, rounded half up,
     then reduced so that the running total of guest parts for this project
     in the billing period does not exceed the cap (`cap[class]`, see 5.1
     for class changes).
   - storage part: `gb_alloc × 10 / hours_in_period`, fractional cents kept
     as a running remainder per project so the period sums exactly to
     `gb_alloc × 10`.
   - egress part: 0 until the project's period egress exceeds 500 GB, then
     `5 × GB` over, computed on the period running total so the threshold
     hour is charged only for the excess.
6. Insert `usage_hours`, then apply the credit ledger, then push a Stripe
   usage record for the remainder (5.5).

`hours_in_period` is the period's actual length in hours.

### 5.5 Stripe objects

**As built (DECISIONS I-77).** Stripe retired the `usage_type = metered` /
`aggregate_usage = sum` subscription-item usage records this section was
written against; the shape below is the same contract expressed with
billing meters, which is what the current API and `stripe-go` v83 offer.

One product `repose`, three **meters** — `repose_compute_cents`,
`repose_storage_cents`, `repose_egress_cents` — and three metered prices in
USD, one per meter, billing scheme per unit at 1 cent. Each user has one
subscription carrying those three prices, created at first card attach with
the period anchored then (`users.stripe_subscription_id`,
`users.billing_anchor`). Trial credit is not a Stripe line, because a
negative line is not allowed: the credit is consumed before the push and
the invoice shows a "trial credit applied" memo line.

Each `usage_hours` row is pushed as up to three meter events with
`timestamp = hour + 59:59` (the hour's last second, so the hour lands in
the same period on Stripe's side as on the rollup's, DECISIONS I-179), `payload.stripe_customer_id`, `payload.value` in cents,
and `identifier = usage:<project_id>:<hour>:<compute|storage|egress>`, which
is the idempotency key: Stripe enforces it as unique over a rolling window
of at least 24 hours, and it is sent as the HTTP idempotency key as well.
`usage_hours.stripe_usage_record_id` holds `usage:<project_id>:<hour>` and a
row that has one is never pushed again. A part worth zero cents is not
pushed at all. An exempt account's rows are marked `exempt` instead
(DECISIONS I-16).

The rejected alternative was one price per class with per-hour quantities;
it makes the cap impossible to express in Stripe and doubles the number of
prices whenever a class is added. Cents as the unit keeps Stripe a ledger of
amounts we computed.

The Stripe configuration is `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`,
`STRIPE_PRICE_{COMPUTE,STORAGE,EGRESS}`, `STRIPE_METER_{COMPUTE,STORAGE,
EGRESS}` (the event names, defaulted to the three above),
`STRIPE_METER_ID_{COMPUTE,STORAGE,EGRESS}` (the `mtr_...` ids reconciliation
reads summaries from), `STRIPE_PORTAL_CONFIGURATION` and
`STRIPE_AUTOMATIC_TAX`. `repose-admin billing stripe-bootstrap`
(`ops/stripe/bootstrap.sh`) creates every object from the secret key and
prints the whole block (DECISIONS I-180). A secret key without the webhook secret and the three
prices is refused at start rather than silently billing nothing; with no
secret key at all the api starts normally and the billing routes answer
`503 billing_disabled`. `ops/AZURE-SETUP.md` step 17 runs the bootstrap.

### 5.6 Invoices and dunning

Stripe Billing issues the invoice at period end and charges the default
card. Webhooks handled (all idempotent on `event.id` stored in a
`stripe_events` table): `invoice.paid` (insert or update `invoices`, set
`active`; a $0 invoice is recorded and changes nothing else, I-184), `invoice.payment_failed` (set `past_due`, `past_due_since`),
`customer.subscription.deleted`, `setup_intent.succeeded`,
`payment_method.detached` (set `has_card = false`), `charge.refunded`
(credit ledger row). Stripe's own retry schedule (Smart Retries) is on.

Past due: a job every hour stops every running guest of a user whose
`past_due_since` is older than 3 days (snapshot first, reason
`billing`), sends the `billing_stopped` notification, and sets
`billing_status = suspended`. Nothing is destroyed; R4-11's 30-day
retention starts at suspension. On `invoice.paid` the status returns to
`active` and guests stay stopped until the user starts them.

### 5.7 Reconciliation

`repose-admin billing reconcile --month 2026-10` (and a nightly job for
the current period): for each user, sum `usage_hours.cost_cents` minus
credit rows, compare with the sum of Stripe usage record quantities for the
period. Any difference over 0 cents is a `billing_mismatch` alert with the
project and hour; the job never fixes it silently. `explain <project>
<hour>` prints every input and each step of 5.4 for one row, which is the
tool for answering a support ticket.

### 5.8 Limits and abuse hooks

`users.project_limit` defaults to 3 and `xl_limit` to 1 until the first
`invoice.paid`, then 10 and 10. `repose-admin suspend <user> <reason>`
stops guests and blocks starts; `unsuspend` reverses. Both write
`audit_log`.

### 5.9 Tax and legal

Stripe Tax enabled on the subscription with `automatic_tax`. Customer
address collected by the Stripe card form (`billing_details.address`
required). No code beyond passing `automatic_tax: {enabled: true}`. Refunds
are manual in the Stripe dashboard plus a `credit` row for the record.

### 5.10 Cost display

`GET /usage` sums `usage_hours` for the range; `cost_today_cents` and
`cost_month_cents` on `Project` are computed by the same function for the
user's local day and the current billing period, so the CLI, dashboard and
invoice never disagree.

### 5.11 Plans on Paddle (I-289)

What runs since 2026-09-27, with the exact names.

**The plan table.** `internal/billing/plans.go`: `Plan{ID, Name,
PriceCents, Currency, TrialDays, Seats, MemoryGB, DiskGB, EgressGB, ...}`,
`Solo` (solo, 2900, 7, 1, 8, 100, 250), `Plus` (plus, 5900, 7, 2, 16,
250, 500) and `Pro` (pro, 9900, 7, 4, 32, 500, 1000) since I-362, no
project count since I-569 (`ProjectCap = 100` for every account,
`AccountProjectCap`), `Plans`, `PlanByID`, `SmallestFor(class)`,
`EgressHardStopMultiplier = 4`, `OveragePerGBCents = 5`, `SeatGB = 8`,
`ClassMemoryGB` (small 4, large 8, xl 16), `OverageCents(plan, bytes)`
(whole GB over, rounded up, at 5 cents), `PriceVersion = "plan-v1"`.
`Price(Inputs)` normalises an hour and prices nothing: every cents column
of `usage_hours` is 0 from plan-v1. `TestPlansMatchPricingDoc` parses the
table in `PRICING.md`.

**Configuration.** `config.go`: `PADDLE_API_KEY` (the prefix `pdl_sdbx_`
means the sandbox, anything else live: `Environment()`),
`PADDLE_WEBHOOK_SECRET`, `PADDLE_CLIENT_TOKEN`, `PADDLE_PRICE_SOLO`,
`PADDLE_PRICE_PLUS`, `PADDLE_PRICE_PRO`, `PADDLE_PRODUCT_OVERAGE`, `PADDLE_DISCOUNT_INTRO`
(I-497), `PADDLE_PORTAL_RETURN_URL`
(default `DASHBOARD_URL/billing`), `BILLING_ENFORCE`, `SEATS_TOTAL`.
`Validate` refuses a key without the secret, all three prices, the overage
product and the introductory discount. No key: the routes answer `503 billing_disabled` and the gate
refuses every non-exempt account with `subscription_required`.

**The Paddle client.** `paddle.go`: `Paddle` over `net/http`, base URL from
the environment (`Config.BaseURL` overrides it for tests), headers
`Authorization: Bearer`, `Paddle-Version: 1`; three tries on 429 and 5xx
honouring `Retry-After`; `PaddleError{Status, Type, Code, Detail}` never
carries the key. Calls: `FindCustomerByEmail`, `CreateCustomer`,
`EnsureCustomer` (stores `users.paddle_customer_id`; called at checkout,
not at first sign-in), `CreateCheckoutTransaction` (items
`[{price_id, quantity 1}]`, `customer_id`, `custom_data.user_id`,
`collection_mode automatic`, and `discount_id` on a first Solo checkout,
I-497), `GetSubscription`,
`UpdateSubscriptionItems` (`prorated_immediately` up,
`prorated_next_billing_period` down), `CancelSubscription`
(`next_billing_period` | `immediately`), `ResumeScheduledChange`
(`scheduled_change: null`), `CreateOneTimeCharge` (`POST
/subscriptions/{id}/charge`, a non-catalog price under
`PADDLE_PRODUCT_OVERAGE`, `unit_price.amount` in cents as a string),
`PortalSession` (`urls.general.overview`,
`urls.subscriptions[].update_subscription_payment_method`),
`ListTransactions` (billed, completed, past_due; 24), `InvoicePDF`, and
the bootstrap's `ListProducts`, `CreateProduct`, `ListPrices`,
`CreatePlanPrice` (`billing_cycle {month, 1}`, `trial_period {day, 7}`,
`custom_data.repose = <plan>`), `ListDiscounts`, `CreateIntroDiscount`
(`flat`, `recur`, `maximum_recurring_intervals`, `restrict_to` the plan's
price, `custom_data.repose = intro-<plan>-<cents>-<months>`; I-497),
`ListNotificationSettings`, `CreateNotificationSetting`.

**The record.** `subscriptions.go`: `Sub` mirrors the table; `IsLive` is
`trialing|active|past_due`; `LiveSubscription(user)`,
`GetSubscription(id)`, `LatestSubscription(user)`; `Sub.Period(at)` is the
current billing period (a calendar month when Paddle has not set one).
`projectStatus` writes `users.billing_status`: trialing → `trial`, active
→ `active`, past_due → `past_due` with `past_due_since` kept from the
first failure, canceled and paused → `none`; `exempt` and `suspended` are
never touched by it.

**The webhook.** `webhook.go`: `Webhooks.Handle(body, header)` verifies
`Paddle-Signature` (`ts=…;h1=…`, HMAC-SHA256 of `ts:body` with every
accepted secret, constant time, `SignatureSkew` five minutes, several
`h1` during a rotation), inserts `paddle_events (id, type, occurred_at)`
first (`ErrDuplicate` on conflict, answered 200), applies, then writes
`processed_at` or `error`. `subscription.*` upserts the row from `data`
(plan from `items[0].price.id` against the configured price ids,
`current_billing_period`, `next_billed_at`, `items[0].trial_dates.ends_at`,
`scheduled_change {action cancel}` → `cancel_at`; the user from
`custom_data.user_id`, else `paddle_customer_id`), projects the status,
sets `has_card`, calls `Seats.Converted` on the first live row, stops the
machines on `canceled` (reason `ended`), and emits `subscription_cancelled`,
`subscription_ended`, `plan_changed`. `transaction.completed` returns a
past-due account to `active`, clears `past_due_since` and a
`suspended_reason = billing` suspension (an operator's stays), leaves a
trialing subscription's $0 checkout alone, and stamps an overage line's
`paddle_transaction_id`. `transaction.payment_failed` on a live
subscription makes the account `past_due` and emits `payment_failed`
(day 0); a checkout that fails has no subscription and changes nothing.
Unknown types are recorded and ignored. `Sign(secret, ts, body)` is the
scheme the fake and the tests use.

**The gate.** `gate.go`: `Gate.Check(user, Request{Class, Disk,
AddHeldBytes, VolumeBytes, Project})` (I-585) returns a `*Refusal{Reason, Message, Detail}`: `suspended`,
`subscription_required` (`detail.waitlist` from the waitlist row),
`past_due`, `egress_limit` (the period's `usage_hours.egress_bytes` over
the account at or past `EgressHardStopBytes`, `detail.until`),
`plan_limit` (`RunningMemory` over `running|starting|restoring|creating|
building` except `Project`, plus the class, against `MemoryGB`;
`detail.projects` the slugs), `disk_limit` (`HeldDisk` of every live
project, each its `disk_held_bytes` or else `volume_bytes`, plus
`AddHeldBytes`, or a `VolumeBytes` past the plan's disk; I-585, which
replaced `AllocatedDisk`). `message` is the whole sentence ("Your Solo
plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at
https://repose.herakraft.co/billing."). Exempt passes everything;
`Enforce = false` passes everything. Call sites: `POST /projects` (class
and the default volume), `/projects/:id/start` (class, excluding itself),
`/projects/restore` and `/snapshots/:sid/restore` (class when starting,
the volume when new), `/fork` (class when starting, the volumes of all
N), `PATCH /projects/:id {class}` to a bigger class, `/resize` (the
growth). `LimitsFor(user, sub)` is `/me`'s `limits`: the account's
project cap (I-569: 100, or `users.project_limit` when higher), `xl` 1
when the plan has 16 GB, memory, disk, egress; an exempt or plan-less
account has `xl_limit`. The project count check stays a `400 invalid`
with `{reason: project_limit, limit, projects, requested}` and reads the
account's cap; the xl count limit is gone (memory decides).
`WaitlistPlace` reads the 0008 waitlist row for `/me`, `/billing` and the
gate's detail.

**Overage and the hard stop.** `overage.go`: `Overage.Run` hourly. For
each live subscription with `next_billed_at` within `ChargeWindow` (3 h)
and `overage_charged_for <> period_start`: `OverageCents` of the period's
egress; when over, insert `overage_charges (subscription_id, period_start,
egress_gb, cents)` (the primary key makes a retry safe: an existing row is
not sent again), `CreateOneTimeCharge(..., next_billing_period)`, store
`paddle_transaction_id` when Paddle bills at once; always set
`overage_charged_for`. A refused charge leaves the row without an id and
the period unmarked, logs `overage_charged result=error`, and counts
`repose_api_billing_overage_charges_total{result="error"}`
(`OverageChargeFailed`); nothing sends it twice. Then the hard stop: any
live subscription whose period egress passed four times the allowance
gets `stopUserMachines` (reason `egress`) and one `egress_stopped` event
per period (guarded by the events table). `ChargePeriod(sub,
effectiveFrom)` is the same for one account, used by `repose-admin
billing overage-now` and by account deletion (`immediately`).

**Dunning.** `dunning.go`: `Dunning.Run` hourly: day 2 emits a second
`payment_failed` once (no second event since `past_due_since`); day 3
(`Grace`) stops the machines (reason `past_due`), emits `billing_stopped`
per project, sets `suspended` with `suspended_reason = billing` and an
audit row; `trial_ending` goes out once when `trial_end` is within 48 h.
`Enforce = false` sends the emails and stops nothing.

**Machines stopped for billing.** `stop.go`: `stopUserMachines` enqueues
a stop with `snapshot: true`, `reason: billing` for every running or
starting project (an op in progress is left for the next run), counts
`repose_api_billing_stops_total{reason}` (`past_due|ended|egress`) and
logs `billing_stopped`.

**Account events.** `events.go`: `AccountEvent(q, user, at, kind,
summary)` inserts an `events` row with `user_id` and no project plus an
email outbox row, in the caller's transaction, the way `waitlist.Admit`
does. Kinds: `trial_ending`, `payment_failed`, `subscription_cancelled`,
`subscription_ended`, `plan_changed`, `egress_stopped` (`AccountKinds`);
`billing_stopped` stays per project through `events.Ingest.Platform`.
The notifier's templates for the kinds are I-291's.

**The routes.** `service.go` behind `internal/api/http/billing.go`:
`Overview` (`GET /billing`: `subscription`, `usage`, `plans[].available`
from `Seats.Reserve`, `seats` from `Seats.Count`, `waitlist`, `paddle
{environment, client_token}`), `Checkout` (`Seats.Reserve` first:
`WaitlistedError` → `503 waitlisted` with `{position, joined_at, email}`
and the sentence; a live subscription → `409 conflict subscribed`; then
`EnsureCustomer` and the transaction), `ChangePlan` (upgrade at once with
a free seat else `409 no_seat`; downgrade scheduled at `period_end`, kept
in `scheduled_plan`, refused `409 over_plan {running_gb,
disk_held_gb, disk_allocated_gb}` while the account does not fit), `Cancel`
(`next_billing_period`, `cancel_at`, `subscription_cancelled`; `409
already_cancelled`), `Resume` (`409 not_cancelled`), `Portal` (`{"for":
"payment_method"}` for the deep link), `Invoices` (Paddle's transactions
in the documented shape, `pdf_url` from `InvoicePDF`), `CloseAccount`
(`DELETE /me`: `ChargePeriod(immediately)` then
`CancelSubscription(immediately)`, in that order). `/me`'s `billing` and
`limits` come from the live subscription; new accounts are inserted
`none` with no credit; a suspended account may call `GET /me`, `GET
/billing`, `POST /billing/portal`. `Project` carries
`running_seconds_today` and `running_seconds_month`; `cost_*_cents` and
`idle.hourly_cents` are 0 for one release.

**Seats.** `seats.go`: `SubscriptionSeats{Pool, Total}` implements
`waitlist.Seats` from `subscriptions` alone (held = seats of live rows,
free = `SEATS_TOTAL` minus held, never waitlists) until the seats
workstream's implementation replaces it in `internal/api/app`.

**Admin.** `repose-admin billing show HANDLE` (`LoadAccount`: the
subscription, plan, period, running memory, disk, hours, egress, the
overage arithmetic and lines), `rollup [--hour]`, `explain PROJECT HOUR`
(the row's inputs and the period's overage arithmetic), `suspend`,
`unsuspend`, `overage-now HANDLE`, `paddle-bootstrap [--webhook-url]
[--no-webhook] [--live]` (`bootstrap.go`: products, prices and the
introductory discount found by `custom_data.repose`, the destination by URL; prints the `PADDLE_*`
block; refuses a live key without `--live`; `ops/paddle/bootstrap.sh`).
`credit`, `reconcile`, `resync`, `cycle-now` and `stripe-bootstrap` are
gone.

**Observability.** Metrics `repose_api_billing_webhook_total{kind,result}`,
`repose_api_billing_overage_charges_total{result}`,
`repose_api_billing_gate_refused_total{reason}`,
`repose_api_billing_subscriptions_total{plan,status}`,
`repose_api_billing_stops_total{reason}`; log events `webhook_received`,
`overage_charged`, `gate_refused` (user_id, reason, plan); alerts
`PaddleWebhookRejected`, `OverageChargeFailed`, `BillingStopped`; the
Billing dashboard from `ops/dashboards/gen.py`.

**Tests.** `internal/billing`: `fakepaddle_test.go` (an `httptest` Paddle
that signs webhooks), `TestPlansMatchPricingDoc`,
`TestPaddleClientRetriesAndErrors`, `TestEnsureCustomer`,
`TestWebhookSignatureSkewAndDedupe`, `TestWebhookSubscriptionLifecycle`,
`TestWebhookRefusesForeignPrices`, `TestWebhookTransactions`,
`TestWebhookResolvesByCustomer`, `TestGateEveryReason`, `TestLimitsFor`,
`TestOverageChargeOnce`, `TestEgressHardStop`, `TestDunningDays`,
`TestDunningEnforceFalse`, `TestTrialEnding`,
`TestRollupWritesUsageWithoutPrices`, `TestAccountAndExplain`,
`TestBootstrapIsIdempotent`, `TestCheckout`,
`TestPlanChangesCancelResume`, `TestPortalAndInvoices`,
`TestCloseAccountChargesThenCancels`, `TestSubscriptionSeatsStub`;
`TestPaddleSandbox` runs against the real sandbox with
`REPOSE_PADDLE_SANDBOX_KEY` and skips without it. `internal/api/http`:
`TestBillingGateBlocksCompute`, `TestProjectCap` (I-569),
`TestBillingEnforceFalseLetsStartsThrough`, `TestBillingDisabledRoutes`,
`TestBillingRoutes`, `TestProjectCarriesRunningSeconds`.
`internal/admin`: `TestBillingSubcommands`. `internal/cli`:
`TestPaymentRequiredMessage`, `TestStatusShowsHours`,
`TestClassSpecsMatchBillingAndHost`.

## 6. Failure modes

| Situation | Outcome |
|---|---|
| Stripe unreachable during the hourly push | `usage_hours` row exists with null record id; the next hourly run retries every null row; alert `StripePushBacklog` (`repose_api_billing_stripe_push_backlog_seconds`) if any row is older than 6 hours |
| Duplicate webhook delivery | ignored by `stripe_events` primary key |
| Webhook signature invalid | 400, logged with the event type only, alert after 5 in 10 minutes |
| Sample gap for a running guest | under-billed minutes, `billing_gap` metric, never estimated |
| Class changed mid-period | cap rule 5.1, `explain` shows it |
| Card removed while guests run | `has_card = false`, guests keep running until invoice failure path; starts blocked with `payment_required` |
| Trial balance negative (race) | impossible by construction: debit inside the same transaction as the `usage_hours` insert with `select ... for update` on the user row |
| Reconciliation mismatch | alert `BillingMismatch` (`repose_api_billing_mismatch_cents`) with details; humans decide; `credit` for the fix |
| Suspended user calls start | `payment_required` with `detail.reason = suspended` |

## 7. Testing

- Unit: pricing table parity test against `PRICING.md`; rollup math with
  golden hours (partial hour, cap hit mid-hour, class change, storage
  remainder summing exactly, egress threshold hour); ledger debit ordering.
- Integration with real Postgres: rollup over fixture `meter_samples`,
  idempotent re-run produces no change, credit ledger transaction under
  concurrency (two runners).
- Stripe test mode, in CI with a test key: the CHECKLIST fixture (one large
  guest, 100 running hours across a period, 40 GB, 10 GB egress) pushed with
  a test clock advanced to period end; assert invoice lines: compute 1400,
  storage 400, egress 0, total 1800 minus trial credit 1000 = 800. Failed
  payment with test card `4000000000000341` triggers the 3-day stop with the
  clock advanced. As built: `TestStripeTestModeM4Gate`, run with
  `REPOSE_STRIPE_TEST_KEY` (DECISIONS I-185, `docs/ops/M4-GATE.md` §2). The
  1400/400/0/1800 are asserted on `usage_hours`; the invoice's three lines
  are the parts left after the credit (a negative line is not allowed,
  §5.5), which sum to 800 and equal `usage_hours`' own split per line.
- Webhook replay test with recorded fixtures.
- Real: the owner's own card charged once in live mode for a small amount,
  invoice checked against `explain` output.

## 8. Rollback

Migrations for `usage_hours`, `invoices`, `credit_ledger`, `stripe_events`
have down scripts. Stripe prices are versioned by id; a price change creates
a new price and updates the subscription items, never edits the old price.
Turning billing off is setting `BILLING_ENFORCE=false`, which keeps rolling
up and pushing but stops blocking starts and stopping guests, and must be
logged as an audit event when flipped.

## 9. Checklist

Re-done for I-289 (2026-09-27, ws/paddle). Test evidence is from
`go test -race ./internal/billing/ ./internal/api/... ./internal/admin/
./internal/cli/` against Postgres; rows whose evidence is Paddle's
sandbox wait for the key and name the `docs/ops/M4-GATE.md` step.

- [x] `plans.go` equals the `PRICING.md` table; the parity test parses the
      doc. Evidence: `--- PASS: TestPlansMatchPricingDoc`,
      `TestOverageAndClassMemory`.
- [x] The Paddle client retries 429 and 5xx three times honouring
      Retry-After, never retries a 4xx, and its errors carry Paddle's code
      and never the key. Evidence: `TestPaddleClientRetriesAndErrors`.
- [x] The customer is created at checkout, found by email when Paddle
      already has one, and stored once. Evidence: `TestEnsureCustomer`,
      `TestCheckout` (the transaction's `custom_data.user_id`, `items`,
      `collection_mode`).
- [x] The webhook verifies `Paddle-Signature` (wrong secret, missing
      header, tampered body, five-minute skew both ways refused; a second
      `h1` and a rotation secret accepted), dedupes on `event_id` with a
      200 duplicate, records unknown types and foreign customers.
      Evidence: `TestWebhookSignatureSkewAndDedupe`,
      `TestBillingRoutes` (the route: no bearer, 400 on a bad signature
      logging the type only, 200 on a duplicate).
- [x] Every subscription event projects the account and emits its email:
      created → trial + `Seats.Converted` once, activated → active,
      updated with the other price → `plan_changed`, a scheduled cancel →
      `subscription_cancelled`, canceled → `subscription_ended` and the
      machines stopped with a snapshot, past_due/paused/resumed as
      documented. Evidence: `TestWebhookSubscriptionLifecycle`,
      `TestWebhookResolvesByCustomer`, `TestWebhookRefusesForeignPrices`.
- [x] `transaction.completed` reactivates a past-due account, lifts a
      billing suspension and not an operator's, leaves a trial's $0
      checkout alone and stamps the overage line's transaction id;
      `transaction.payment_failed` moves a live subscription to past_due
      with one `payment_failed` email and ignores a failed checkout.
      Evidence: `TestWebhookTransactions`.
- [x] The gate refuses with every reason of api.md's table, the message
      is the whole sentence, exempt and `BILLING_ENFORCE=false` pass, and
      a deploy without Paddle refuses `subscription_required`. Evidence:
      `TestGateEveryReason` (unit) and `TestBillingGateBlocksCompute`
      (create, start, class change, resize, restore; suspended allow-list),
      `TestProjectCap` (I-569), `TestBillingEnforceFalseLetsStartsThrough`,
      `TestBillingDisabledRoutes`, `TestSignInAndProjectsLifecycle`
      (`/me` defaults `none`, Pro's `limits`, `plan_limit` naming the
      machines), `TestFork` (the account project cap, I-569).
- [x] The overage line: 50 GB over on Solo is one charge of 250 cents
      with `effective_from next_billing_period` under the overage
      product; a retry sends none; under the allowance nothing is sent
      and the period is marked; a refused charge leaves the row for the
      operator and is not sent twice; an immediate charge stores the
      transaction id. Evidence: `TestOverageChargeOnce`.
- [x] The hard stop at four times the allowance stops the running
      machines once per period with `egress_stopped`, the gate refuses
      `egress_limit` until `period_end`, and `BILLING_ENFORCE=false`
      stops nothing. Evidence: `TestEgressHardStop`.
- [x] Dunning: day 0 from the webhook, day 2 once however often the tick
      runs, day 3 the stop with `snapshot: true`, `billing_stopped`,
      `suspended` with reason billing and an audit row; a payment
      reactivates without starting the machine; `trial_ending` once at 48
      hours. Evidence: `TestDunningDays`, `TestDunningEnforceFalse`,
      `TestTrialEnding`.
- [x] The rollup writes hours, disk and egress with every cents column 0,
      stamps the subscription's period, records a gap, and is idempotent.
      Evidence: `TestRollupWritesUsageWithoutPrices`,
      `internal/api/meter` `TestIngestAndSyntheticDayRollup`.
- [x] Account deletion charges the pending overage immediately and then
      cancels immediately. Evidence: `TestCloseAccountChargesThenCancels`
      (the request order on the fake), `TestBillingRoutes` (`DELETE /me`
      leaves the row canceled).
- [x] Plan changes: upgrade at once prorated, refused without a seat;
      downgrade scheduled at period_end, refused `over_plan` while the
      account does not fit, kept through a webhook, undone by choosing the
      plan again; cancel and resume with their 409s. Evidence:
      `TestPlanChangesCancelResume`, `TestBillingRoutes`.
- [x] `GET /billing`, `/me`, `Project` and `GET /billing/invoices` have
      api.md's shapes. Evidence: `TestBillingRoutes`, `TestCheckout`
      (plans, seats, paddle block), `TestPortalAndInvoices` (every
      invoice key), `TestProjectCarriesRunningSeconds`.
- [x] The bootstrap creates six objects once, finds them on a rerun,
      prints the block without the key, refuses a live key without
      `--live`. Evidence: `TestBootstrapIsIdempotent`,
      `TestBillingSubcommands` (the command).
- [x] `repose-admin billing show|rollup|explain|suspend|unsuspend|
      overage-now|paddle-bootstrap` exist, print the arithmetic and write
      audit_log; the removed commands are usage errors. Evidence:
      `TestBillingSubcommands`, `TestAccountAndExplain`.
- [x] The CLI prints the api's `payment_required` sentence verbatim and
      exits 7, falls back to the plan sentence for an old api, shows hours
      in `status` and `ls`, names the plan a class needs, drops the idle
      rate. Evidence: `TestPaymentRequiredMessage`, `TestStatusShowsHours`,
      `TestClassSpecsMatchBillingAndHost`, `TestResizeClass`,
      `TestIdleLineOnStatusAndProjects`; `go test ./internal/cli -run
      TestDocs` green.
- [x] Metrics, log events, alerts and the dashboard exist under the
      names of §5.11. Evidence: `internal/obs/metrics`
      `TestAPIFamily` (the five families and their labels),
      `ops/alerts_test.yaml` (six cases; `promtool` is not installed on
      the dev box, so the file is written to its format and unrun),
      `python3 ops/dashboards/gen.py --check`.
- [x] `PRICING.md`, `features/pricing.md`, `apps/web` `billing.md` and
      `limits.md`, `ops/RUNBOOK.md` ("PaddleWebhookRejected",
      "OverageChargeFailed", "BillingStopped", "Customer disputes a
      charge", "Move a user between plans by hand"), `OBSERVABILITY.md`,
      `M4-GATE.md` describe the plans. Evidence: re-read 2026-09-27.
- [ ] Sandbox gate: a checkout with the test card makes a `trialing`
      subscription and the account `trial`; a simulated
      `transaction.completed` makes it `active`; a simulated
      `transaction.payment_failed` makes it `past_due` and the tick stops
      the machine on day 3; an overage for a known egress appears on the
      next transaction to the cent. Evidence: `docs/ops/M4-GATE.md` §2 to
      §4 with the ids pasted. Offline: `TestPaddleSandbox` (skipped
      without `REPOSE_PADDLE_SANDBOX_KEY`). — waits on: owner (the sandbox
      key).
- [ ] Live: one charge of the owner's own card, refunded or not at their
      choice. Evidence: the transaction id and `billing show`. — waits on:
      owner (live account, domain review).
