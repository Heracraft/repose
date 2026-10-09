# 09 · billing

> **Superseded in part (2026-09-27, DECISIONS I-289).** §1 to §4 and
> §5.1 to §5.10 describe the hourly design on Stripe as it was built and
> merged; they are kept as history. What runs is §5.11, "Plans on
> Polar", and the §9 checklist is that design's. `docs/PRICING.md` is
> what is sold.
>
> **Amended by I-604 (2026-10-08).** Billing moved from Paddle to Polar:
> §5.11 describes the Polar client, webhook, overage meter and bootstrap
> that replaced the Paddle ones, and §9 is re-done for them.

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

### 5.11 Plans on Polar (I-289, I-604)

What runs since 2026-09-27, on Polar since 2026-10-08, with the exact names.

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

**Configuration.** `config.go`: `POLAR_ACCESS_TOKEN` (an organization
access token), `POLAR_ENVIRONMENT` (`sandbox` or `production`, required
with the token because Polar's tokens do not say which they belong to),
`POLAR_WEBHOOK_SECRET`, `POLAR_PRODUCT_SOLO`, `POLAR_PRODUCT_PLUS`,
`POLAR_PRODUCT_PRO`, `POLAR_DISCOUNT_INTRO` (I-497),
`POLAR_PORTAL_RETURN_URL` (default `DASHBOARD_URL/billing`),
`BILLING_ENFORCE`, `SEATS_TOTAL`. `Validate` refuses a token without the
environment, the secret, all three products (three different ones) and
the introductory discount. No token: the routes answer `503
billing_disabled` and the gate refuses every non-exempt account with
`subscription_required`. No `PADDLE_*` variable is read.

**The Polar client.** `polar.go`: `Polar` over `net/http`, base URL
`https://api.polar.sh/v1` or `https://sandbox-api.polar.sh/v1` from the
environment (`Config.BaseURL` overrides it for tests), headers
`Authorization: Bearer` and `Polar-Version: 2026-10` (`APIVersion`, to be
moved before July 2027, RUNBOOK "Polar API version"); three tries on 429
and 5xx honouring `Retry-After`; `PolarError` never carries the token.
Calls: `CreateCheckout` (one product, `external_customer_id` = the user
id, `customer_email`, `metadata.user_id`, `allow_discount_codes: false`,
`discount_id` on a first Solo checkout, `customer_ip_address` for the tax
country, `success_url` `<dashboard>/billing?checkout=done`, `return_url`
`<dashboard>/billing`), `GetSubscription`, `ChangeProduct`
(`proration_behavior` `invoice` up, `next_period` down; `discount_id:
null` when leaving Solo's offer),
`ClearPendingUpdate` (`pending_update: null`), `SetCancelAtPeriodEnd`,
`RevokeSubscription`, `SendOverage` (`POST /events/ingest`, one
`egress_overage` event with `external_id`), `CustomerPortal` (a customer
session for the external id), `ListOrders`, `OrderInvoiceURL`,
`GenerateOrderInvoice`, and the bootstrap's `ListMeters`,
`CreateOverageMeter`, `ListProducts`, `CreatePlanProduct`,
`ListDiscounts`, `CreateIntroDiscount`, `ListWebhookEndpoints`,
`CreateWebhookEndpoint`, `UpdateWebhookEndpoint`, `GetOrganization`,
`UpdateOrganization`.

**The catalog.** One monthly product per plan, found by
`metadata.repose = plan-<id>-<cents>`: the fixed price, a metered price of
5 cents a unit on the meter "Egress overage" (events named
`egress_overage`, the sum of `metadata.gb`), and a seven-day trial. The
introductory discount is fixed, $9 off, `repeating` for 3 months,
restricted to Solo's product, with no code; Polar counts the months from
the first charged period, so the first three charges after the trial get
it, and as a fixed discount it applies to the whole order, metered usage
included, which leaves the overage at full price. The organization has
one subscription per customer, trial abuse prevention, plan and seat
changes off in the customer portal, metered usage shown, prices
exclusive of tax, and the customer emails repose sends itself turned off
(`subscription_trial_conversion_reminder`, `subscription_past_due`,
`subscription_cancellation`, `subscription_revoked`,
`subscription_updated`; repose's are I-291's `trial_ending`,
`payment_failed`, `subscription_cancelled`, `subscription_ended`,
`plan_changed`). Polar still sends the receipts, the subscription
confirmation, the renewal receipts and the card-expiring reminder.

**The record.** `subscriptions.go`: `Sub` mirrors the table; `IsLive` is
`trialing|active|past_due`; `LiveSubscription(user)`,
`GetSubscription(id)`, `LatestSubscription(user)`; `Sub.Period(at)` is the
current billing period (a calendar month when Polar has not set one).
`projectStatus` writes `users.billing_status`: trialing → `trial`, active
→ `active`, past_due → `past_due` with `past_due_since` kept from the
first failure, canceled and paused → `none`; `exempt` and `suspended` are
never touched by it. `intro_until` is the trial's end (or the start) plus
`IntroMonths`, since Polar's subscription names no end for the discount.

**The webhook.** `webhook.go`: `Webhooks.Handle(body, headers)` verifies
the Standard Webhooks headers (`webhook-id`, `webhook-timestamp`,
`webhook-signature` with one or more `v1,<base64>`), an HMAC-SHA256 of
`id.timestamp.body` keyed by the base64 after `whsec_` or by the whole
secret (Polar's older secrets and `polar listen`'s), with every accepted
secret, constant time, `SignatureSkew` five minutes; inserts
`billing_events (id, type, occurred_at)` keyed on `webhook-id` first
(`ErrDuplicate` on conflict, answered 200), applies, then writes
`processed_at` or `error`. `subscription.created|updated|active|canceled|
uncanceled|revoked|past_due` upsert the row from the payload (plan from
`product_id` against the configured products, `pending_update.product_id`
the scheduled plan, `current_period_start|end`, `trial_end`, `ends_at`
with `cancel_at_period_end` → `cancel_at`, `discount_id` → `intro`; the
user from `metadata.user_id`, then the customer's external id, then
`users.billing_customer_id`), store the customer id, project the status,
set `has_card`, call `Seats.Converted` on the first live row, stop the
machines on `canceled` (reason `ended`), and emit `subscription_cancelled`,
`subscription_ended`, `plan_changed`. Polar's `unpaid` is stored as
`canceled`; `incomplete` and `incomplete_expired` are recorded with no
row. The first move to `past_due` makes the account `past_due` and emits
`payment_failed` (day 0); Polar retries 2, 7, 14 and 21 days after the
failure, and a retry that fails again changes nothing. `order.paid` for a
subscription returns a past-due account to `active`, clears
`past_due_since` and a `suspended_reason = billing` suspension (an
operator's stays), and leaves a trialing subscription's $0 checkout
alone. Unknown types are recorded and ignored. `Sign(secret, id, ts,
body)` is the scheme the fake and the tests use.

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
each live subscription with `period_end` within `ChargeWindow` (3 h) and
`overage_charged_for <> period_start`: `OverageCents` of the period's
egress; when over, insert `overage_charges (subscription_id, period_start,
egress_gb, cents)` (the primary key makes a retry safe), `SendOverage`
with the external id `overage:<subscription>:<period start unix>`, store
it in `sent_ref`; always set `overage_charged_for`. Polar bills the meter's
units through the plan's metered price on the renewal order; an event it
receives after the renewal lands on the next period's order. A refused
event leaves the row without `sent_ref` and the period unmarked, logs
`overage_charged result=error`, and counts
`repose_api_billing_overage_charges_total{result="error"}`
(`OverageChargeFailed`); the next tick sends it again under the same
external id, which Polar dedupes. Then the hard stop: any live
subscription whose period egress passed four times the allowance gets
`stopUserMachines` (reason `egress`) and one `egress_stopped` event per
period (guarded by the events table). `ChargePeriod(sub)` is the same for
one account, used by `repose-admin billing overage-now` and by account
deletion.

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
from `Seats.Reserve`, `seats` from `Seats.Count`, `waitlist`; the
`paddle` object is gone since I-604), `Checkout` (`Seats.Reserve` first:
`WaitlistedError` → `503 waitlisted` with `{position, joined_at, email}`
and the sentence; a live subscription → `409 conflict subscribed`; then
`CreateCheckout`, answered `{url}`; Polar creates the customer when the
checkout completes), `ChangePlan` (upgrade at once with a free seat else
`409 no_seat`, proration `invoice`, and an upgrade from Solo sends
`discount_id: null` so the introductory $9 does not follow onto Plus or
Pro; downgrade scheduled at `period_end`
with `next_period`, kept in `scheduled_plan` from Polar's
`pending_update`, undone by choosing the current plan again; refused `409 over_plan {running_gb,
disk_held_gb, disk_allocated_gb}` while the account does not fit), `Cancel`
(`cancel_at_period_end`, `cancel_at`, `subscription_cancelled`; `409
already_cancelled`), `Resume` (`409 not_cancelled`), `Portal` (a customer
session; `{"for": "payment_method"}` gets the same portal, Polar has no
deep link; `409 no_subscription` for a user who never subscribed),
`Invoices` (Polar's orders, paid, pending, refunded and partially
refunded, in the documented shape, `pdf_url` once Polar has generated the
invoice, which the listing asks for), `CloseAccount` (`DELETE /me`:
`ChargePeriod` first; with an overage owed, `cancel_at_period_end` so
Polar bills it on the final order, else `RevokeSubscription` at once; no
new period is charged either way). `/me`'s `billing` and
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
`unsuspend`, `overage-now HANDLE`, `polar-bootstrap [--webhook-url URL]
[--no-webhook] [--production]` (`bootstrap.go`: the token and environment
from the environment, never a flag; the meter, products and introductory
discount found by `metadata.repose`, the webhook endpoint by URL (raw,
the eight events, `api_version` `APIVersion`, PATCHed when it differs),
the organization's settings; prints the `POLAR_*` block; refuses
production without `--production`; `ops/polar/bootstrap.sh`). `show`
prints `polar customer` and each overage line's sent ref.
`credit`, `reconcile`, `resync`, `cycle-now` and `stripe-bootstrap` are
gone.

**Observability.** Metrics `repose_api_billing_webhook_total{kind,result}`,
`repose_api_billing_overage_charges_total{result}`,
`repose_api_billing_gate_refused_total{reason}`,
`repose_api_billing_subscriptions_total{plan,status}`,
`repose_api_billing_stops_total{reason}`; log events `webhook_received`,
`overage_charged`, `gate_refused` (user_id, reason, plan); alerts
`BillingWebhookRejected`, `OverageChargeFailed`, `BillingStopped`; the
Billing dashboard from `ops/dashboards/gen.py`.

**Tests.** `internal/billing`: `fakepolar_test.go` (an `httptest` Polar
that signs webhooks), `TestPlansMatchPricingDoc`, `TestPolarConfig`,
`TestPolarClientRetriesAndErrors`, `TestPolarSendOverage`,
`TestWebhookSignatureSkewAndDedupe`, `TestWebhookLegacySecret`,
`TestWebhookSubscriptionLifecycle`, `TestWebhookRefusesForeignProducts`,
`TestWebhookOrderPaid`, `TestWebhookResolvesByCustomer`,
`TestGateEveryReason`, `TestLimitsFor`, `TestOverageChargeOnce`,
`TestEgressHardStop`, `TestDiskOverPlanEmail`, `TestDunningDays`,
`TestDunningEnforceFalse`, `TestTrialEnding`,
`TestRollupWritesUsageWithoutPrices`, `TestAccountAndExplain`,
`TestBootstrapIsIdempotent`, `TestCheckout`, `TestIntroOffer`,
`TestPlanChangesCancelResume`, `TestPlanChangePlusPro`,
`TestPortalAndInvoices`, `TestCloseAccount`, `TestSubscriptionSeatsStub`;
`TestPolarSandbox` (`polar_sandbox_test.go`) runs against the real
sandbox with `POLAR_ACCESS_TOKEN` and `POLAR_ENVIRONMENT=sandbox`, skips
without the token and refuses production; with `REPOSE_POLAR_E2E_LISTEN`
it serves the webhook for `polar listen` and walks the whole flow.
`internal/api/http`: `TestBillingGateBlocksCompute`, `TestProjectCap`
(I-569), `TestBillingEnforceFalseLetsStartsThrough`,
`TestBillingDisabledRoutes`, `TestBillingRoutes`,
`TestProjectCarriesRunningSeconds`. `internal/admin`:
`TestBillingSubcommands`. `internal/cli`: `TestPaymentRequiredMessage`,
`TestStatusShowsHours`, `TestClassSpecsMatchBillingAndHost`.

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

Re-done for I-289 (2026-09-27, ws/paddle) and again for I-604
(2026-10-08, ws/polar). Test evidence is from `go test
./internal/billing/ ./internal/api/http/ ./internal/admin/` against
Postgres; rows whose evidence is Polar's sandbox name the
`docs/ops/M4-GATE.md` step.

- [x] `plans.go` equals the `PRICING.md` table; the parity test parses the
      doc. Evidence: `--- PASS: TestPlansMatchPricingDoc`,
      `TestOverageAndClassMemory`.
- [x] The Polar client sends `Polar-Version: 2026-10`, retries 429 and
      5xx three times honouring Retry-After, never retries a 4xx, and its
      errors carry Polar's type and detail and never the token; the
      configuration needs `POLAR_ENVIRONMENT` with the token. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestPolarClientRetriesAndErrors`, `TestPolarConfig`.
- [x] The checkout is a Polar session for the plan's product with the
      user as external customer, the introductory discount on a first
      Solo checkout and discount codes off, answered `{url}`. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestCheckout`, `TestIntroOffer`.
- [x] The webhook verifies the Standard Webhooks signature (wrong secret,
      missing header, tampered body, five-minute skew both ways refused;
      a rotation secret and Polar's older raw-string secrets accepted),
      dedupes on `webhook-id` with a 200 duplicate, records unknown types
      and foreign customers. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestWebhookSignatureSkewAndDedupe`, `TestWebhookLegacySecret`,
      `TestBillingRoutes` (the route).
- [x] Every subscription event projects the account and emits its email:
      created (trialing) → trial + `Seats.Converted` once, active → active,
      another product → `plan_changed`, a pending update → the scheduled
      downgrade, `cancel_at_period_end` → `subscription_cancelled`,
      past_due → the first payment failure with one `payment_failed`
      email, canceled → `subscription_ended` and the machines stopped with
      a snapshot. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar. `TestWebhookSubscriptionLifecycle`,
      `TestWebhookResolvesByCustomer`, `TestWebhookRefusesForeignProducts`.
- [x] `order.paid` reactivates a past-due account, lifts a billing
      suspension and not an operator's, and leaves a trial's $0 checkout
      alone. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar. `TestWebhookOrderPaid`.
- [x] The gate refuses with every reason of api.md's table, the message
      is the whole sentence, exempt and `BILLING_ENFORCE=false` pass, and
      a deploy without Polar refuses `subscription_required`. Evidence:
      `TestGateEveryReason` (unit) and `TestBillingGateBlocksCompute`
      (create, start, class change, resize, restore; suspended allow-list),
      `TestProjectCap` (I-569), `TestBillingEnforceFalseLetsStartsThrough`,
      `TestBillingDisabledRoutes`, `TestSignInAndProjectsLifecycle`
      (`/me` defaults `none`, Pro's `limits`, `plan_limit` naming the
      machines), `TestFork` (the account project cap, I-569).
- [x] The overage line: egress over the allowance within three hours of
      the period's end is one `egress_overage` event to the GB under the
      external id `overage:<subscription>:<period start unix>`, stored in
      `sent_ref`; a retry sends none; under the allowance nothing is sent
      and the period is marked; a refused event leaves the row unsent and
      the next run sends it under the same external id. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestOverageChargeOnce`, `TestPolarSendOverage`.
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
- [x] Account deletion sends the pending overage first; with some owed
      the subscription is cancelled at the period's end so Polar bills it,
      with none it is revoked at once. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar. `TestCloseAccount`,
      `TestBillingRoutes` (`DELETE /me`).
- [x] Plan changes: upgrade at once prorated, refused without a seat;
      downgrade scheduled at period_end, refused `over_plan` while the
      account does not fit, kept through a webhook, undone by choosing the
      plan again; cancel and resume with their 409s. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestPlanChangesCancelResume`, `TestPlanChangePlusPro`,
      `TestBillingRoutes`.
- [x] `GET /billing`, `/me`, `Project` and `GET /billing/invoices` have
      api.md's shapes (no `paddle` object since I-604). Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestBillingRoutes`, `TestCheckout` (plans, seats),
      `TestPortalAndInvoices` (every invoice key, Polar's orders),
      `TestProjectCarriesRunningSeconds`.
- [x] `polar-bootstrap` creates the meter, the three products, the
      introductory discount and the webhook endpoint once, finds them on a
      rerun, sets the organization, prints the block without the token,
      refuses production without `--production`. Evidence (I-604): `--- PASS` on 2026-10-08 in ws/polar.
      `TestBootstrapIsIdempotent`, `TestBillingSubcommands` (the command).
- [x] `repose-admin billing show|rollup|explain|suspend|unsuspend|
      overage-now|polar-bootstrap` exist, print the arithmetic and write
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
      `limits.md`, `ops/RUNBOOK.md` ("BillingWebhookRejected",
      "OverageChargeFailed", "BillingStopped", "Customer disputes a
      charge", "Move a user between plans by hand", "Polar API version"),
      `OBSERVABILITY.md`, `M4-GATE.md` describe the plans on Polar.
      Evidence: re-read 2026-09-27, rewritten for Polar 2026-10-08 (I-604).
- [ ] Sandbox gate: a checkout with Stripe's test card makes a
      `trialing` subscription and the account `trial`; ending the trial
      makes it `active`; a renewal on the failing card makes it
      `past_due` and the tick stops the machine on day 3; a payment lifts
      it; an overage for a known egress is billed to the cent on the
      renewal order (or counted on the meter's quantities until that
      order exists). Evidence: `docs/ops/M4-GATE.md` §2 to §5 with the ids
      pasted. Offline: `TestPolarSandbox` (skipped without
      `POLAR_ACCESS_TOKEN`; end to end with `REPOSE_POLAR_E2E_LISTEN`).
- [ ] Live: one charge of the owner's own card, refunded or not at their
      choice. Evidence: the order id and `billing show`. Waits on the
      owner (the production organization and token).
